// SPDX-License-Identifier: AGPL-3.0-only

package loki

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/pushhook"
)

// ---------------------------------------------------------------------------
// High-cardinality stream-label assertion (I14) — ported from predecessor loki_test.go
// ---------------------------------------------------------------------------

// TestHighCardAssertion proves the sink rejects a UUID-class key used as a stream label.
// This is the guard that stops an emitter bug from exploding stream cardinality on a
// production-shaped stack. Runs in dry-run so nothing is pushed.
func TestHighCardAssertion(t *testing.T) {
	s := New("http://example/loki/api/v1/push", "u", "tok", true)

	// Legal: low-card labels + high-card keys in structured metadata only.
	ok := []Stream{{
		Labels: map[string]string{"source": "webservice", "env": "prod", "level": "info"},
		Lines: []Line{{
			T:    time.Now(),
			Body: "ok",
			Meta: map[string]string{"trace_id": "abc", "correlation_id": "def"},
		}},
	}}
	if err := s.Write(context.Background(), ok); err != nil {
		t.Fatalf("legal stream must pass: %v", err)
	}

	// Illegal: correlation_id as a STREAM label.
	bad := []Stream{{
		Labels: map[string]string{"source": "webservice", "correlation_id": "def"},
		Lines:  []Line{{T: time.Now(), Body: "bad"}},
	}}
	err := s.Write(context.Background(), bad)
	if err == nil || !strings.Contains(err.Error(), "correlation_id") {
		t.Fatalf("high-card stream label must be rejected, got err=%v", err)
	}
}

// TestHighCardAssertionAllDefaultKeys checks every key in DefaultForbiddenStreamLabels is rejected.
func TestHighCardAssertionAllDefaultKeys(t *testing.T) {
	s := New("http://unused", "u", "tok", true)
	for _, k := range DefaultForbiddenStreamLabels {
		t.Run(k, func(t *testing.T) {
			bad := []Stream{{
				Labels: map[string]string{"source": "svc", k: "somevalue"},
				Lines:  []Line{{T: time.Now(), Body: "bad"}},
			}}
			err := s.Write(context.Background(), bad)
			if err == nil || !strings.Contains(err.Error(), k) {
				t.Fatalf("key %q as stream label must be rejected, got err=%v", k, err)
			}
		})
	}
}

// TestHighCardAssertionExtraForbidden proves the wiring layer can extend the forbidden set
// (e.g. "model" for AI workloads is high-card for Loki streams even though it is a legal
// Mimir label).
func TestHighCardAssertionExtraForbidden(t *testing.T) {
	s := New("http://unused", "u", "tok", true, "model")

	badModel := []Stream{{
		Labels: map[string]string{"source": "svc", "model": "gpt-4.1"},
		Lines:  []Line{{T: time.Now(), Body: "bad"}},
	}}
	if err := s.Write(context.Background(), badModel); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("extra-forbidden key 'model' as stream label must be rejected, got err=%v", err)
	}

	// Same key in metadata must still pass.
	ok := []Stream{{
		Labels: map[string]string{"source": "svc"},
		Lines:  []Line{{T: time.Now(), Body: "ok", Meta: map[string]string{"model": "gpt-4.1"}}},
	}}
	if err := s.Write(context.Background(), ok); err != nil {
		t.Fatalf("model in structured metadata must pass: %v", err)
	}
}

// TestNoExtraForbiddenDoesNotBlockDefaultLegalKeys ensures a key not in the forbidden set
// is accepted as a stream label.
func TestNoExtraForbiddenDoesNotBlockDefaultLegalKeys(t *testing.T) {
	s := New("http://unused", "u", "tok", true)
	legal := []Stream{{
		Labels: map[string]string{"source": "svc", "level": "warn", "env": "prod"},
		Lines:  []Line{{T: time.Now(), Body: "hello"}},
	}}
	if err := s.Write(context.Background(), legal); err != nil {
		t.Fatalf("legal stream labels must not be rejected: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Gzip wire format (ported from predecessor loki_test.go)
// ---------------------------------------------------------------------------

// TestWriteGzipsBody verifies the push body is gzip-compressed and the Content-Encoding
// header is set, and that the server can transparently decode it back to the JSON contract.
func TestWriteGzipsBody(t *testing.T) {
	var gotEncoding, gotType string
	var decoded pushBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Content-Encoding")
		gotType = r.Header.Get("Content-Type")
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("body is not valid gzip: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, _ := io.ReadAll(zr)
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Errorf("decompressed body is not valid push JSON: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	streams := []Stream{{
		Labels: map[string]string{"source": "webservice", "env": "prod", "level": "info"},
		Lines:  []Line{{T: time.Now(), Body: "hello world", Meta: map[string]string{"trace_id": "abc"}}},
	}}
	if err := s.Write(context.Background(), streams); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if gotEncoding != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", gotEncoding)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotType)
	}
	if len(decoded.Streams) != 1 || decoded.Streams[0].Stream["source"] != "webservice" {
		t.Errorf("decompressed body did not round-trip: %+v", decoded)
	}
}

// ---------------------------------------------------------------------------
// Timestamp wire format: nanosecond epoch strings
// ---------------------------------------------------------------------------

func TestTimestampIsNanosecondEpochString(t *testing.T) {
	now := time.Now()
	var decoded pushBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, _ := gzip.NewReader(r.Body)
		raw, _ := io.ReadAll(zr)
		_ = json.Unmarshal(raw, &decoded)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	if err := s.Write(context.Background(), []Stream{{
		Labels: map[string]string{"source": "svc"},
		Lines:  []Line{{T: now, Body: "ts-check"}},
	}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(decoded.Streams) == 0 || len(decoded.Streams[0].Values) == 0 {
		t.Fatalf("no values decoded: %+v", decoded)
	}
	v := decoded.Streams[0].Values[0]
	tsStr, ok := v[0].(string)
	if !ok {
		t.Fatalf("timestamp is %T, want string", v[0])
	}
	want := now.UnixNano()
	got, parseErr := parseInt64(tsStr)
	if parseErr != nil {
		t.Fatalf("timestamp %q is not a valid int64: %v", tsStr, parseErr)
	}
	if got != want {
		t.Errorf("timestamp = %d, want %d", got, want)
	}
}

// parseInt64 is a helper for the test above; keeps the test stdlib-only.
func parseInt64(s string) (int64, error) {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, &badCharErr{c}
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

type badCharErr struct{ r rune }

func (e *badCharErr) Error() string { return "non-digit character: " + string(e.r) }

// ---------------------------------------------------------------------------
// Structured metadata 3-tuple vs 2-tuple
// ---------------------------------------------------------------------------

func TestStructuredMetadataOmittedWhenEmpty(t *testing.T) {
	var decoded pushBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, _ := gzip.NewReader(r.Body)
		raw, _ := io.ReadAll(zr)
		_ = json.Unmarshal(raw, &decoded)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	// Line with no Meta → 2-tuple; line with Meta → 3-tuple.
	if err := s.Write(context.Background(), []Stream{{
		Labels: map[string]string{"source": "svc"},
		Lines: []Line{
			{T: time.Now(), Body: "no-meta"},
			{T: time.Now(), Body: "with-meta", Meta: map[string]string{"trace_id": "abc"}},
		},
	}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(decoded.Streams) == 0 || len(decoded.Streams[0].Values) != 2 {
		t.Fatalf("unexpected decoded: %+v", decoded)
	}
	noMeta := decoded.Streams[0].Values[0]
	withMeta := decoded.Streams[0].Values[1]
	if len(noMeta) != 2 {
		t.Errorf("no-meta tuple len = %d, want 2", len(noMeta))
	}
	if len(withMeta) != 3 {
		t.Errorf("with-meta tuple len = %d, want 3", len(withMeta))
	}
}

// ---------------------------------------------------------------------------
// HTTP error handling
// ---------------------------------------------------------------------------

func TestHTTPErrorIsReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	err := s.Write(context.Background(), []Stream{{
		Labels: map[string]string{"source": "svc"},
		Lines:  []Line{{T: time.Now(), Body: "hello"}},
	}})
	if err == nil {
		t.Fatal("expected error on 429, got nil")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %q, want to contain 429", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Dry-run inventory
// ---------------------------------------------------------------------------

func TestDryRunInventory(t *testing.T) {
	s := New("http://unused", "u", "tok", true)
	streams := []Stream{
		{
			Labels: map[string]string{"source": "webservice", "env": "prod"},
			Lines: []Line{
				{T: time.Now(), Body: "line1", Meta: map[string]string{"trace_id": "t1", "span_id": "s1"}},
				{T: time.Now(), Body: "line2", Meta: map[string]string{"trace_id": "t2"}},
			},
		},
		{
			Labels: map[string]string{"source": "coredns", "env": "prod"},
			Lines: []Line{
				{T: time.Now(), Body: "dns query", Meta: map[string]string{"request_id": "r1"}},
			},
		},
	}
	if err := s.Write(context.Background(), streams); err != nil {
		t.Fatalf("dry-run Write: %v", err)
	}
	streamInv, metaInv := s.Inventory()

	// stream-label inventory
	ws := streamInv["webservice"]
	if !containsAll(ws, "source", "env") {
		t.Errorf("webservice stream keys = %v, want source+env", ws)
	}
	// metadata inventory
	wm := metaInv["webservice"]
	if !containsAll(wm, "trace_id", "span_id") {
		t.Errorf("webservice meta keys = %v, want trace_id+span_id", wm)
	}
	cd := streamInv["coredns"]
	if !containsAll(cd, "source", "env") {
		t.Errorf("coredns stream keys = %v, want source+env", cd)
	}
}

func containsAll(haystack []string, needles ...string) bool {
	set := make(map[string]struct{}, len(haystack))
	for _, s := range haystack {
		set[s] = struct{}{}
	}
	for _, n := range needles {
		if _, ok := set[n]; !ok {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Observe hook (ported from predecessor loki/hook_test.go, adapted: Scenario→Blueprint)
// ---------------------------------------------------------------------------

func TestObserveDryRun(t *testing.T) {
	s := New("http://unused", "u", "tok", true)
	var got pushhook.Event
	s.Observe = func(_ context.Context, ev pushhook.Event) { got = ev }

	streams := []Stream{{
		Labels: map[string]string{"source": "svc", "blueprint": "myapp"},
		Lines:  []Line{{T: time.Now(), Body: "hello"}, {T: time.Now(), Body: "world"}},
	}}
	if err := s.Write(context.Background(), streams); err != nil {
		t.Fatalf("dry-run Write: %v", err)
	}
	if got.Sink != "loki" {
		t.Errorf("Sink = %q, want loki", got.Sink)
	}
	if got.Items != 2 {
		t.Errorf("Items = %d, want 2 (line count)", got.Items)
	}
	if !got.DryRun {
		t.Errorf("DryRun = false, want true")
	}
	if got.Blueprint != "myapp" {
		t.Errorf("Blueprint = %q, want myapp", got.Blueprint)
	}
}

func TestObserveNilSafe(t *testing.T) {
	// No Observe set: must not panic and must behave identically (inventory unchanged).
	s := New("http://unused", "u", "tok", true)
	streams := []Stream{{
		Labels: map[string]string{"source": "svc"},
		Lines:  []Line{{T: time.Now(), Body: "hello"}},
	}}
	if err := s.Write(context.Background(), streams); err != nil {
		t.Fatalf("nil-observer Write: %v", err)
	}
	streamInv, _ := s.Inventory()
	if len(streamInv) == 0 {
		t.Fatalf("inventory not recorded with nil observer")
	}
}

func TestObserveLivePushSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	var got pushhook.Event
	s.Observe = func(_ context.Context, ev pushhook.Event) { got = ev }

	if err := s.Write(context.Background(), []Stream{{
		Labels: map[string]string{"source": "svc", "blueprint": "myapp"},
		Lines:  []Line{{T: time.Now(), Body: "hello"}, {T: time.Now(), Body: "world"}},
	}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got.Sink != "loki" || got.Status != 204 || got.Items != 2 || got.Blueprint != "myapp" {
		t.Fatalf("unexpected event: %+v", got)
	}
	if got.Bytes <= 0 {
		t.Fatalf("Bytes should be >0 (gzip body), got %d", got.Bytes)
	}
	if got.DryRun {
		t.Errorf("DryRun should be false on a live push")
	}
}

func TestObserveLivePushRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	var got pushhook.Event
	s.Observe = func(_ context.Context, ev pushhook.Event) { got = ev }

	if err := s.Write(context.Background(), []Stream{{
		Labels: map[string]string{"source": "svc"},
		Lines:  []Line{{T: time.Now(), Body: "hello"}},
	}}); err == nil {
		t.Fatal("expected error on 429")
	}
	if got.Status != 429 || got.Err == nil {
		t.Fatalf("unexpected event: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// Empty-stream / empty-batch short-circuit
// ---------------------------------------------------------------------------

func TestEmptyBatchIsNoop(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	if err := s.Write(context.Background(), nil); err != nil {
		t.Fatalf("empty Write: %v", err)
	}
	if called {
		t.Fatal("server should not be called for empty batch")
	}
}

func TestStreamWithNoLinesIsSkipped(t *testing.T) {
	pushed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pushed = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	// stream has labels but zero lines
	if err := s.Write(context.Background(), []Stream{{
		Labels: map[string]string{"source": "svc"},
		Lines:  nil,
	}}); err != nil {
		t.Fatalf("zero-line Write: %v", err)
	}
	if pushed {
		t.Fatal("server should not be called when all streams have zero lines")
	}
}

// ---------------------------------------------------------------------------
// Retry: 503 → retry → success
// ---------------------------------------------------------------------------

// TestLokiRetriesOn503 proves the loki live path retries on a 503 response and
// succeeds when the server recovers on the next attempt.
func TestLokiRetriesOn503(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false)
	err := s.Write(context.Background(), []Stream{{
		Labels: map[string]string{"source": "svc"},
		Lines:  []Line{{T: time.Now(), Body: "hello"}},
	}})
	if err != nil {
		t.Fatalf("expected success after 503 retry, got: %v", err)
	}
	if n := atomic.LoadInt64(&calls); n < 2 {
		t.Errorf("calls = %d, want ≥2 (first 503 + at least one retry)", n)
	}
}
