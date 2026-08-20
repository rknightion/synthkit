// SPDX-License-Identifier: AGPL-3.0-only

// Package preflight validates and probes the mandatory Grafana Cloud sink lanes.
package preflight

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/golang/snappy"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/sink/promrw/writev2"
	"google.golang.org/protobuf/proto"
)

// State is a redacted preflight outcome.
type State string

const (
	StateStaticValid  State = "statically valid"
	StateReady        State = "ready"
	StateUnauthorized State = "unauthorized"
	StateUnreachable  State = "unreachable"
)

// Reason adds a non-sensitive classification to a state.
type Reason string

const (
	ReasonHTTP401      Reason = "http-401"
	ReasonHTTP403      Reason = "http-403"
	ReasonEndpointPath Reason = "endpoint-path"
	ReasonDNS          Reason = "dns"
	ReasonTLS          Reason = "tls"
	ReasonTimeout      Reason = "timeout"
	ReasonConnection   Reason = "connection"
	ReasonHTTPStatus   Reason = "http-status"
)

// Result reports one mandatory lane without endpoint or credential values.
type Result struct {
	Lane   string
	State  State
	Reason Reason
}

// Options bounds and controls explicit network preflight checks.
type Options struct {
	Client  *http.Client
	Timeout time.Duration
}

// Static validates mandatory configuration without performing network I/O.
func Static(cfg *config.Config) ([]Result, error) {
	if err := cfg.ValidateMandatory(); err != nil {
		return nil, err
	}
	return []Result{
		{Lane: "prometheus", State: StateStaticValid},
		{Lane: "loki", State: StateStaticValid},
		{Lane: "otlp", State: StateStaticValid},
	}, nil
}

// Check performs explicit bounded network and authentication checks after static validation.
func Check(ctx context.Context, cfg *config.Config, opts Options) ([]Result, error) {
	results, err := Static(cfg)
	if err != nil {
		return nil, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	hc := *client
	hc.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	promBody, err := proto.Marshal(&writev2.Request{Symbols: []string{""}})
	if err != nil {
		return nil, fmt.Errorf("preflight: build empty Prometheus request")
	}

	probes := []struct {
		lane        string
		url         string
		user        string
		body        []byte
		contentType string
		headers     map[string]string
	}{
		{
			lane: "prometheus", url: cfg.PromRWURL, user: cfg.PromUser,
			body: snappy.Encode(nil, promBody), contentType: "application/x-protobuf;proto=io.prometheus.write.v2.Request",
			headers: map[string]string{"Content-Encoding": "snappy", "X-Prometheus-Remote-Write-Version": "2.0.0"},
		},
		{lane: "loki", url: cfg.LokiURL, user: cfg.LokiUser, body: []byte(`{"streams":[]}`), contentType: "application/json"},
		{lane: "otlp", url: strings.TrimRight(cfg.OTLPEndpoint, "/") + "/v1/traces", user: cfg.OTLPUser, contentType: "application/x-protobuf"},
	}
	for i, spec := range probes {
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		req, reqErr := http.NewRequestWithContext(requestCtx, http.MethodPost, spec.url, bytes.NewReader(spec.body))
		if reqErr != nil {
			cancel()
			return nil, fmt.Errorf("preflight: %s request could not be built", spec.lane)
		}
		req.Header.Set("Content-Type", spec.contentType)
		for key, value := range spec.headers {
			req.Header.Set(key, value)
		}
		req.SetBasicAuth(spec.user, cfg.Token)
		resp, requestErr := hc.Do(req)
		cancel()
		if requestErr != nil {
			results[i].State = StateUnreachable
			results[i].Reason = classifyTransportError(requestErr)
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			results[i].State = StateReady
			continue
		}
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			results[i].State = StateUnauthorized
			results[i].Reason = ReasonHTTP401
		case resp.StatusCode == http.StatusForbidden:
			results[i].State = StateUnauthorized
			results[i].Reason = ReasonHTTP403
		case resp.StatusCode >= 300 && resp.StatusCode < 400,
			resp.StatusCode == http.StatusNotFound,
			resp.StatusCode == http.StatusMethodNotAllowed:
			results[i].State = StateUnreachable
			results[i].Reason = ReasonEndpointPath
		default:
			results[i].State = StateUnreachable
			results[i].Reason = ReasonHTTPStatus
		}
	}
	return results, nil
}

func classifyTransportError(err error) Reason {
	if errors.Is(err, context.DeadlineExceeded) {
		return ReasonTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ReasonDNS
	}
	var verificationErr *tls.CertificateVerificationError
	var recordHeaderErr tls.RecordHeaderError
	var unknownAuthorityErr x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var certificateInvalidErr x509.CertificateInvalidError
	if errors.As(err, &verificationErr) || errors.As(err, &recordHeaderErr) ||
		errors.As(err, &unknownAuthorityErr) || errors.As(err, &hostnameErr) ||
		errors.As(err, &certificateInvalidErr) {
		return ReasonTLS
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "tls") || strings.Contains(lower, "certificate") {
		return ReasonTLS
	}
	return ReasonConnection
}
