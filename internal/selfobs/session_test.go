// SPDX-License-Identifier: AGPL-3.0-only

package selfobs

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	traces "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

// exportedResources runs one real Start/emit/Shutdown cycle against a local OTLP
// receiver and returns the resource attributes it received, by signal path.
func exportedResources(t *testing.T, opts Options) map[string][]map[string]string {
	t.Helper()
	var mu sync.Mutex
	got := map[string][]map[string]string{}
	record := func(path string, attrs []*common.KeyValue) {
		m := map[string]string{}
		for _, a := range attrs {
			m[a.Key] = a.Value.GetStringValue()
		}
		mu.Lock()
		got[path] = append(got[path], m)
		mu.Unlock()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			defer gz.Close()
			reader = gz
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Error(err)
			return
		}
		switch r.URL.Path {
		case "/v1/metrics":
			var req metrics.ExportMetricsServiceRequest
			if err := proto.Unmarshal(body, &req); err != nil {
				t.Error(err)
			}
			for _, rm := range req.ResourceMetrics {
				record(r.URL.Path, rm.Resource.Attributes)
			}
		case "/v1/traces":
			var req traces.ExportTraceServiceRequest
			if err := proto.Unmarshal(body, &req); err != nil {
				t.Error(err)
			}
			for _, rs := range req.ResourceSpans {
				record(r.URL.Path, rs.Resource.Attributes)
			}
		case "/v1/logs":
			var req logs.ExportLogsServiceRequest
			if err := proto.Unmarshal(body, &req); err != nil {
				t.Error(err)
			}
			for _, rl := range req.ResourceLogs {
				record(r.URL.Path, rl.Resource.Attributes)
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	opts.Enabled, opts.Endpoint, opts.User, opts.Password, opts.MetricInterval = true, srv.URL, "user", "token", time.Hour
	so, err := Start(opts, Gauges{})
	if err != nil {
		t.Fatal(err)
	}
	_ = so.ObserveTick(context.Background(), "bp", "host", "instance", func(context.Context) error { return nil })
	so.EmitEvent("config_change", nil, "session test")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	so.Shutdown(ctx)
	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/v1/metrics", "/v1/traces", "/v1/logs"} {
		if len(got[path]) == 0 {
			t.Fatalf("no real export observed at %s", path)
		}
	}
	return got
}

func TestSessionAttributionOnRealExports(t *testing.T) {
	// The SDK merges OTEL_RESOURCE_ATTRIBUTES underneath every provider resource;
	// it must not decide session attribution in either mode.
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "synthkit.ha.session.id=forged,synthkit.ha.session.attribution=lease")
	const identity = "11111111-2222-3333-4444-555555555555/00112233445566778899aabbccddeeff"
	forged := map[string]string{AttrSessionID: "forged", AttrSessionAttribution: "forged", "ha.role": "leader"}
	want := SessionID(identity)
	if len(want) != 64 || want == SessionID(identity[:len(identity)-1]+"0") {
		t.Fatalf("session ID is not a full-width digest of the identity: %q", want)
	}
	for path, resources := range exportedResources(t, Options{LeaseIdentity: identity, Tags: forged}) {
		for _, attrs := range resources {
			if attrs[AttrSessionAttribution] != SessionLease || attrs[AttrSessionID] != want {
				t.Fatalf("%s: lease session attribution %v, want id %s", path, attrs, want)
			}
			for k, v := range attrs {
				if strings.Contains(v, "11111111-2222") || strings.Contains(v, "00112233445566778899") {
					t.Fatalf("%s: %s exports the raw Pod UID or nonce: %q", path, k, v)
				}
			}
		}
	}
	// Missing attribution is explicit and never inferred from forged tags or environment.
	for path, resources := range exportedResources(t, Options{Tags: forged}) {
		for _, attrs := range resources {
			if attrs[AttrSessionAttribution] != SessionUnknown || attrs[AttrSessionID] != SessionUnknown {
				t.Fatalf("%s: missing attribution not explicit unknown: %v", path, attrs)
			}
		}
	}
}
