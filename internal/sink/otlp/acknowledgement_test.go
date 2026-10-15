// SPDX-License-Identifier: AGPL-3.0-only

package otlp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/pushhook"
)

func TestPartialLogAcceptanceIsNotSuccessfulOrRetried(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		resources := decodeResourceLogs(t, body)
		count := 0
		for _, resource := range resources {
			for _, scope := range resource.ScopeLogs {
				count += len(scope.LogRecords)
			}
		}
		if count != 2 {
			t.Errorf("actual encoded log records %d", count)
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		// ExportLogsServiceResponse.partial_success.rejected_log_records = 1.
		_, _ = w.Write([]byte{0x0a, 0x02, 0x08, 0x01})
	}))
	defer server.Close()
	sink := NewLogs(server.URL, "test", "test", false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := sink.Write(ctx, []LogResource{{Attrs: map[string]any{"service.name": "ack-proof"}, Records: []LogRecord{{Time: time.Unix(1, 0), Body: "one"}, {Time: time.Unix(2, 0), Body: "two"}}}})
	if err == nil {
		t.Fatal("partial OTLP acceptance was reported as full success")
	}
	if calls.Load() != 1 {
		t.Fatalf("partial success retried %d times", calls.Load())
	}
}

type ackRoundTripper func(*http.Request) (*http.Response, error)

func (f ackRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type ackReadWitness struct {
	io.ReadCloser
	read     bytes.Buffer
	observed chan struct{}
	signaled bool
}

func (r *ackReadWitness) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.read.Write(p[:n])
	if !r.signaled && strings.Contains(r.read.String(), "private receiver diagnostic") {
		r.signaled = true
		close(r.observed)
	}
	return n, err
}

func TestCanceledRejectedReplyDoesNotExposeReceiverBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(400)
		_, _ = io.WriteString(w, "private receiver diagnostic")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	sink := NewLogs(server.URL, "test", "test", false)
	observed := make(chan struct{})
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	sink.eg.hc.Transport = ackRoundTripper(func(r *http.Request) (*http.Response, error) {
		resp, err := transport.RoundTrip(r)
		if err == nil {
			resp.Body = &ackReadWitness{ReadCloser: resp.Body, observed: observed}
		}
		return resp, err
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- sink.Write(ctx, []LogResource{{Records: []LogRecord{{Time: time.Unix(1, 0), Body: "one"}}}})
	}()
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal("client never consumed response diagnostic")
	}
	cancel()
	err := <-result
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation identity lost: %v", err)
	}
	if strings.Contains(err.Error(), "private receiver diagnostic") {
		t.Fatal("cancellation exposed receiver response body")
	}
}

func TestOTLPResponseAcknowledgementStates(t *testing.T) {
	for _, tc := range []struct {
		name, body         string
		status             int
		want               pushhook.AckStatus
		accepted, rejected int64
		bad                bool
	}{
		{"full-protobuf", "", 200, pushhook.AckAccepted, 2, 0, false},
		{"malformed-diagnostic-json", `{"partialSuccess":{"errorMessage":42}}`, 200, pushhook.AckUnknown, 0, 0, true},
		{"warning-json", `{"partialSuccess":{"rejectedLogRecords":"0","errorMessage":"private diagnostic"}}`, 200, pushhook.AckAccepted, 2, 0, false},
		{"partial-exponent", `{"partialSuccess":{"rejectedLogRecords":1e0}}`, 200, pushhook.AckPartial, 1, 1, true},
		{"all-rejected-snake", `{"partial_success":{"rejected_log_records":"2"}}`, 200, pushhook.AckRejected, 0, 2, true},
		{"count-exceeds-request", `{"partialSuccess":{"rejectedLogRecords":"3"}}`, 200, pushhook.AckUnknown, 0, 0, true},
		{"negative", `{"partialSuccess":{"rejectedLogRecords":"-1"}}`, 200, pushhook.AckUnknown, 0, 0, true},
		{"duplicate-alias", `{"partialSuccess":{"rejectedLogRecords":"0","rejected_log_records":"1"}}`, 200, pushhook.AckUnknown, 0, 0, true},
		{"unknown-fields", `{"future":42,"partialSuccess":{"rejectedLogRecords":"0","future":true}}`, 200, pushhook.AckAccepted, 2, 0, false},
		{"malformed-protobuf", string([]byte{255}), 200, pushhook.AckUnknown, 0, 0, true},
		{"deferred", "", 202, pushhook.AckDeferred, 0, 0, false},
		{"legacy-empty-204", "", 204, pushhook.AckUnknown, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			sink := NewLogs(server.URL, "test", "test", false)
			var event pushhook.Event
			sink.Observe = func(_ context.Context, e pushhook.Event) { event = e }
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := sink.Write(ctx, []LogResource{{Attrs: map[string]any{"service.name": "ack-proof"}, Records: []LogRecord{{Time: time.Unix(1, 0), Body: "one"}, {Time: time.Unix(2, 0), Body: "two"}}}})
			if (err != nil) != tc.bad || calls.Load() != 1 {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
			a := event.Acknowledgement
			if a.Status != tc.want || a.Expected != 2 || a.Accepted != tc.accepted || a.Rejected != tc.rejected || a.Unit != "log_records" {
				t.Fatalf("acknowledgement %+v", a)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("receiver diagnostics escaped sanitization")
			}
		})
	}
}

func TestOTLPJSONCountIsSignalSpecific(t *testing.T) {
	for _, tc := range []struct{ signal, key, unit string }{{"otlp", "rejectedSpans", "spans"}, {"otlpmetrics", "rejectedDataPoints", "data_points"}, {"otlplogs", "rejectedLogRecords", "log_records"}} {
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"partialSuccess":{"` + tc.key + `":"1"}}`))}
		a, err := otlpAcknowledgement(resp, tc.signal, 2)
		if err == nil || a.Status != pushhook.AckPartial || a.Rejected != 1 || a.Unit != tc.unit {
			t.Fatalf("%s: %+v %v", tc.signal, a, err)
		}
	}
}

type boundedAckReader struct{ remaining, read int }

func (r *boundedAckReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	for i := range p[:n] {
		p[i] = 0
	}
	r.remaining -= n
	r.read += n
	return n, nil
}

func TestOTLPAcknowledgementReadIsBounded(t *testing.T) {
	r := &boundedAckReader{remaining: 20 << 20}
	a, err := otlpAcknowledgement(&http.Response{StatusCode: 200, Body: io.NopCloser(r)}, "otlplogs", 2)
	if err == nil || a.Status != pushhook.AckUnknown || r.read > maxAcknowledgementBytes+1 {
		t.Fatalf("oversized response: read=%d ack=%+v err=%v", r.read, a, err)
	}
}
