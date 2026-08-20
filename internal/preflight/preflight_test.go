// SPDX-License-Identifier: AGPL-3.0-only

package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang/snappy"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/sink/promrw/writev2"
	"google.golang.org/protobuf/proto"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func validConfig() *config.Config {
	return &config.Config{
		DryRun:       true,
		PromRWURL:    "https://prom.example/api/prom/push",
		PromUser:     "123456",
		OTLPEndpoint: "https://otlp.example/otlp",
		OTLPUser:     "234567",
		LokiURL:      "https://logs.example/loki/api/v1/push",
		LokiUser:     "345678",
		Token:        "test-token-should-never-appear",
	}
}

func TestCheckReportsReadyForAcceptedLocalEndpoints(t *testing.T) {
	seen := make(map[string]bool)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		if user, password, ok := r.BasicAuth(); !ok || user == "" || password != "test-token-should-never-appear" {
			t.Errorf("request did not carry expected Basic authentication")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	cfg := validConfig()
	cfg.PromRWURL = srv.URL + "/api/prom/push"
	cfg.LokiURL = srv.URL + "/loki/api/v1/push"
	cfg.OTLPEndpoint = srv.URL + "/otlp"

	results, err := Check(context.Background(), cfg, Options{Client: srv.Client(), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.State != StateReady {
			t.Errorf("%s state = %q, want %q", result.Lane, result.State, StateReady)
		}
	}
	for _, path := range []string{"/api/prom/push", "/loki/api/v1/push", "/otlp/v1/traces"} {
		if !seen[path] {
			t.Errorf("preflight did not probe %s", path)
		}
	}
}

func TestCheckSendsValidEmptyProbePayloads(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read %s body: %v", r.URL.Path, err)
		}
		switch r.URL.Path {
		case "/api/prom/push":
			raw, err := snappy.Decode(nil, body)
			if err != nil {
				t.Errorf("decode Prometheus probe: %v", err)
				break
			}
			var request writev2.Request
			if err := proto.Unmarshal(raw, &request); err != nil {
				t.Errorf("unmarshal Prometheus probe: %v", err)
			}
			if len(request.Timeseries) != 0 || len(request.Symbols) != 1 || request.Symbols[0] != "" {
				t.Errorf("Prometheus probe is not a valid empty RW2 request: timeseries=%d symbols=%q", len(request.Timeseries), request.Symbols)
			}
		case "/loki/api/v1/push":
			var request struct {
				Streams []json.RawMessage `json:"streams"`
			}
			if err := json.Unmarshal(body, &request); err != nil || request.Streams == nil || len(request.Streams) != 0 {
				t.Errorf("Loki probe is not a valid empty push request: body=%q err=%v", body, err)
			}
		case "/otlp/v1/traces":
			if len(body) != 0 {
				t.Errorf("OTLP empty protobuf request has %d bytes", len(body))
			}
		default:
			t.Errorf("unexpected probe path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	cfg := validConfig()
	cfg.PromRWURL = srv.URL + "/api/prom/push"
	cfg.LokiURL = srv.URL + "/loki/api/v1/push"
	cfg.OTLPEndpoint = srv.URL + "/otlp"

	if _, err := Check(context.Background(), cfg, Options{Client: srv.Client(), Timeout: time.Second}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDistinguishesUnauthorizedResponses(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statusCode int
		wantReason Reason
	}{
		{name: "HTTP 401", statusCode: http.StatusUnauthorized, wantReason: ReasonHTTP401},
		{name: "HTTP 403", statusCode: http.StatusForbidden, wantReason: ReasonHTTP403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "test-token-should-never-appear", tc.statusCode)
			}))
			defer srv.Close()
			cfg := validConfig()
			cfg.PromRWURL = srv.URL + "/api/prom/push"
			cfg.LokiURL = srv.URL + "/loki/api/v1/push"
			cfg.OTLPEndpoint = srv.URL + "/otlp"

			results, err := Check(context.Background(), cfg, Options{Client: srv.Client(), Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			for _, result := range results {
				if result.State != StateUnauthorized || result.Reason != tc.wantReason {
					t.Errorf("%s = %+v, want unauthorized/%s", result.Lane, result, tc.wantReason)
				}
				if strings.Contains(fmt.Sprint(result), cfg.Token) {
					t.Fatalf("result exposed token: %+v", result)
				}
			}
		})
	}
}

func TestCheckClassifiesEndpointPathFailures(t *testing.T) {
	for _, statusCode := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(statusCode)
			}))
			defer srv.Close()
			cfg := validConfig()
			cfg.PromRWURL = srv.URL + "/api/prom/push"
			cfg.LokiURL = srv.URL + "/loki/api/v1/push"
			cfg.OTLPEndpoint = srv.URL + "/otlp"

			results, err := Check(context.Background(), cfg, Options{Client: srv.Client(), Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			for _, result := range results {
				if result.State != StateUnreachable || result.Reason != ReasonEndpointPath {
					t.Errorf("%s = %+v, want unreachable/%s", result.Lane, result, ReasonEndpointPath)
				}
			}
		})
	}
}

func TestCheckClassifiesDNSFailures(t *testing.T) {
	cfg := validConfig()
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.DNSError{Err: "test resolver failure", Name: "redacted.invalid", IsNotFound: true}
	})}

	results, err := Check(context.Background(), cfg, Options{Client: client, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.State != StateUnreachable || result.Reason != ReasonDNS {
			t.Errorf("%s = %+v, want unreachable/%s", result.Lane, result, ReasonDNS)
		}
	}
}

func TestCheckClassifiesTLSFailures(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	cfg := validConfig()
	cfg.PromRWURL = srv.URL + "/api/prom/push"
	cfg.LokiURL = srv.URL + "/loki/api/v1/push"
	cfg.OTLPEndpoint = srv.URL + "/otlp"

	results, err := Check(context.Background(), cfg, Options{Client: &http.Client{}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.State != StateUnreachable || result.Reason != ReasonTLS {
			t.Errorf("%s = %+v, want unreachable/%s", result.Lane, result, ReasonTLS)
		}
	}
}

func TestCheckBoundsEachNetworkProbe(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	cfg := validConfig()
	cfg.PromRWURL = srv.URL + "/api/prom/push"
	cfg.LokiURL = srv.URL + "/loki/api/v1/push"
	cfg.OTLPEndpoint = srv.URL + "/otlp"

	started := time.Now()
	results, err := Check(context.Background(), cfg, Options{Client: srv.Client(), Timeout: 20 * time.Millisecond})
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded probes took %v", elapsed)
	}
	for _, result := range results {
		if result.State != StateUnreachable || result.Reason != ReasonTimeout {
			t.Errorf("%s = %+v, want unreachable/%s", result.Lane, result, ReasonTimeout)
		}
	}
}

func TestStaticReportsEachMandatoryLaneWithoutValues(t *testing.T) {
	cfg := validConfig()
	results, err := Static(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %v, want one result per mandatory lane", results)
	}
	for _, result := range results {
		if result.State != StateStaticValid {
			t.Errorf("%s state = %q, want %q", result.Lane, result.State, StateStaticValid)
		}
	}
	emitted := fmt.Sprint(results)
	for _, value := range []string{cfg.PromRWURL, cfg.PromUser, cfg.OTLPEndpoint, cfg.OTLPUser, cfg.LokiURL, cfg.LokiUser, cfg.Token} {
		if strings.Contains(emitted, value) {
			t.Fatalf("static report exposed configured value %q: %s", value, emitted)
		}
	}
}
