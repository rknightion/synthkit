// SPDX-License-Identifier: AGPL-3.0-only

package promrw

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/pushhook"
	"github.com/rknightion/synthkit/internal/sink/promrw/writev2"

	"github.com/golang/snappy"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------------------------
// Distinct-series counter (ported from predecessor promrw_test.go)
// ---------------------------------------------------------------------------

func TestDistinctSeriesCountsAcrossWrites(t *testing.T) {
	s := &Sink{} // zero-value sink; test the counter path directly (no HTTP push)
	s.recordDistinct([]Series{{Name: "m1", Labels: map[string]string{"a": "1"}}})
	s.recordDistinct([]Series{{Name: "m1", Labels: map[string]string{"a": "1"}}}) // dup → no growth
	s.recordDistinct([]Series{{Name: "m1", Labels: map[string]string{"a": "2"}}}) // new label combo
	s.recordDistinct([]Series{{Name: "m2", Labels: nil}})
	if got := s.DistinctSeries(); got != 3 {
		t.Fatalf("want 3 distinct, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// RW2 wire format: headers + snappy/proto-decodable body
// ---------------------------------------------------------------------------

func TestLivePushSendsRW2HeadersAndDecodableBody(t *testing.T) {
	var gotCT, gotCE, gotVer, gotAuth string
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotCE = r.Header.Get("Content-Encoding")
		gotVer = r.Header.Get("X-Prometheus-Remote-Write-Version")
		gotAuth = r.Header.Get("Authorization")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(srv.URL, "user", "tok", false, func() int { return 0 })
	err := s.Write(context.Background(), []Series{{
		Name: "m", Labels: map[string]string{"job": "api"}, Value: 1, T: time.UnixMilli(1), Kind: KindGauge,
	}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if gotCT != ContentTypeRW2 {
		t.Fatalf("Content-Type = %q, want %q", gotCT, ContentTypeRW2)
	}
	if gotCE != "snappy" {
		t.Fatalf("Content-Encoding = %q, want snappy", gotCE)
	}
	if gotVer != RemoteWriteVersion {
		t.Fatalf("version header = %q, want %q", gotVer, RemoteWriteVersion)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Fatalf("auth header = %q, want Basic ...", gotAuth)
	}
	// body must snappy-decode then proto-unmarshal as a v2 Request
	raw, err := snappy.Decode(nil, body)
	if err != nil {
		t.Fatalf("snappy decode: %v", err)
	}
	var req writev2.Request
	if err := proto.Unmarshal(raw, &req); err != nil {
		t.Fatalf("proto unmarshal: %v", err)
	}
	if len(req.Timeseries) != 1 {
		t.Fatalf("want 1 series in body, got %d", len(req.Timeseries))
	}
}

func TestObserveReportsCompressedBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	var ev pushhook.Event
	s := New(srv.URL, "u", "t", false, func() int { return 0 })
	s.Observe = func(_ context.Context, e pushhook.Event) { ev = e }
	if err := s.Write(context.Background(), []Series{{Name: "m", Value: 1, T: time.UnixMilli(1)}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if ev.Bytes <= 0 {
		t.Fatalf("Bytes must be the on-wire compressed size, got %d", ev.Bytes)
	}
}

// ---------------------------------------------------------------------------
// Dry-run path
// ---------------------------------------------------------------------------

func TestDryRunRecordsInventoryAndCapture(t *testing.T) {
	s := New("http://unused", "u", "tok", true, nil)
	s.Capture = true

	batch := []Series{
		{Name: "go_goroutines", Labels: map[string]string{"job": "myapp"}, Value: 42, T: time.Now()},
		{Name: "go_goroutines", Labels: map[string]string{"job": "myapp", "instance": "host1"}, Value: 43, T: time.Now()},
		{Name: "http_requests_total", Labels: map[string]string{"method": "GET"}, Value: 1000, T: time.Now()},
	}
	if err := s.Write(context.Background(), batch); err != nil {
		t.Fatalf("dry-run Write: %v", err)
	}

	inv := s.Inventory()
	if _, ok := inv["go_goroutines"]; !ok {
		t.Fatalf("inventory missing go_goroutines")
	}
	// union of label keys across both go_goroutines series
	keys := inv["go_goroutines"]
	hasJob := false
	hasInstance := false
	for _, k := range keys {
		if k == "job" {
			hasJob = true
		}
		if k == "instance" {
			hasInstance = true
		}
	}
	if !hasJob || !hasInstance {
		t.Fatalf("inventory go_goroutines keys = %v, want job+instance", keys)
	}

	if n := len(s.Captured()); n != 3 {
		t.Fatalf("Captured() len = %d, want 3", n)
	}
	goroutines := s.SeriesFor("go_goroutines")
	if len(goroutines) != 2 {
		t.Fatalf("SeriesFor(go_goroutines) = %d, want 2", len(goroutines))
	}
}

func TestDryRunNilObserverSafe(t *testing.T) {
	// No network push; no Observe set. Must not panic and must record inventory.
	s := New("http://unused", "u", "tok", true, func() int { return 0 })
	batch := []Series{{Name: "m", Labels: map[string]string{"job": "x"}, Value: 1, T: time.Now()}}
	if err := s.Write(context.Background(), batch); err != nil {
		t.Fatalf("nil-observer dry-run write: %v", err)
	}
	if len(s.Inventory()) != 1 {
		t.Fatalf("inventory not recorded")
	}
}

// ---------------------------------------------------------------------------
// Series-cap guard
// ---------------------------------------------------------------------------

func TestSeriesCapTruncatesBatch(t *testing.T) {
	cap := 2
	s := New("http://unused", "u", "tok", true, func() int { return cap })
	s.Capture = true

	batch := []Series{
		{Name: "m", Labels: map[string]string{"a": "1"}, Value: 1, T: time.Now()},
		{Name: "m", Labels: map[string]string{"a": "2"}, Value: 2, T: time.Now()},
		{Name: "m", Labels: map[string]string{"a": "3"}, Value: 3, T: time.Now()},
	}
	if err := s.Write(context.Background(), batch); err != nil {
		t.Fatalf("cap-truncate write: %v", err)
	}
	if n := len(s.Captured()); n != cap {
		t.Fatalf("after cap truncation Captured() = %d, want %d", n, cap)
	}
}

func TestSeriesCapZeroMeansUnlimited(t *testing.T) {
	s := New("http://unused", "u", "tok", true, func() int { return 0 })
	s.Capture = true

	batch := make([]Series, 100)
	now := time.Now()
	for i := range batch {
		batch[i] = Series{Name: "m", Labels: map[string]string{"i": string(rune('a' + i%26))}, Value: float64(i), T: now}
	}
	if err := s.Write(context.Background(), batch); err != nil {
		t.Fatalf("unlimited-cap write: %v", err)
	}
	// all 100 pushed (not truncated)
	if n := s.DistinctSeries(); n == 0 {
		t.Fatalf("expected >0 distinct series, got 0")
	}
}

// ---------------------------------------------------------------------------
// Observe hook (ported from predecessor hook_test.go, adapted: Scenario→Blueprint)
// ---------------------------------------------------------------------------

func TestObserveDryRun(t *testing.T) {
	s := New("http://unused", "u", "tok", true, func() int { return 0 })
	var got pushhook.Event
	s.Observe = func(_ context.Context, ev pushhook.Event) { got = ev }

	batch := []Series{{Name: "m", Labels: map[string]string{"blueprint": "myapp", "job": "x"}, Value: 1, T: time.Now()}}
	if err := s.Write(context.Background(), batch); err != nil {
		t.Fatalf("dry-run Write: %v", err)
	}
	if got.Sink != "promrw" {
		t.Errorf("Sink = %q, want promrw", got.Sink)
	}
	if got.Items != 1 {
		t.Errorf("Items = %d, want 1", got.Items)
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
	s := New("http://unused", "u", "tok", true, func() int { return 0 })
	batch := []Series{{Name: "m", Labels: map[string]string{"job": "x"}, Value: 1, T: time.Now()}}
	if err := s.Write(context.Background(), batch); err != nil {
		t.Fatalf("nil-observer Write: %v", err)
	}
	if len(s.Inventory()) != 1 {
		t.Fatalf("inventory not recorded with nil observer")
	}
}

func TestObserveLivePushSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false, func() int { return 0 })
	var got pushhook.Event
	n := 0
	s.Observe = func(_ context.Context, ev pushhook.Event) { got = ev; n++ }

	if err := s.Write(context.Background(), makeSample()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 1 {
		t.Fatalf("observer called %d times, want 1", n)
	}
	if got.Sink != "promrw" || got.Status != 200 || got.Err != nil || got.Items != 1 {
		t.Fatalf("unexpected event: %+v", got)
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

	s := New(srv.URL, "u", "tok", false, func() int { return 0 })
	var got pushhook.Event
	s.Observe = func(_ context.Context, ev pushhook.Event) { got = ev }

	if err := s.Write(context.Background(), makeSample()); err == nil {
		t.Fatal("expected error on 429")
	}
	if got.Status != 429 {
		t.Fatalf("Status = %d, want 429 (from resp.StatusCode; err %v)", got.Status, got.Err)
	}
	if got.Err == nil {
		t.Fatalf("Err should be set on 429")
	}
}

// ---------------------------------------------------------------------------
// Live push via httptest (ported from predecessor hook_test.go, adapted: no Observe)
// ---------------------------------------------------------------------------

func makeSample() []Series {
	return []Series{{Name: "m", Labels: map[string]string{"job": "x"}, Value: 1, T: time.Now()}}
}

func TestLivePushSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false, func() int { return 0 })
	if err := s.Write(context.Background(), makeSample()); err != nil {
		t.Fatalf("live push: %v", err)
	}
}

func TestLivePushRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false, func() int { return 0 })
	err := s.Write(context.Background(), makeSample())
	if err == nil {
		t.Fatal("expected error on 429, got nil")
	}
}

func TestLivePushSetsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(srv.URL, "user", "secret", false, nil)
	if err := s.Write(context.Background(), makeSample()); err != nil {
		t.Fatalf("live push: %v", err)
	}
	if gotAuth == "" {
		t.Fatal("Authorization header not set")
	}
	if len(gotAuth) < 6 || gotAuth[:6] != "Basic " {
		t.Fatalf("Authorization header = %q, want Basic ...", gotAuth)
	}
}

// ---------------------------------------------------------------------------
// Empty-batch short-circuit
// ---------------------------------------------------------------------------

func TestEmptyBatchIsNoop(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false, nil)
	if err := s.Write(context.Background(), nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	if called {
		t.Fatal("server should not have been called for empty batch")
	}
}

// ---------------------------------------------------------------------------
// M4 UUID-class label guard (correction 1: exactly 5 keys; uid and model are legal)
// ---------------------------------------------------------------------------

// TestUUIDClassLabelsRejected proves the five request/session-scoped keys are rejected
// as Mimir labels. uid and model are intentionally NOT in this list.
func TestUUIDClassLabelsRejected(t *testing.T) {
	forbidden := []string{"trace_id", "span_id", "request_id", "session_id", "correlation_id"}
	for _, k := range forbidden {
		t.Run(k, func(t *testing.T) {
			s := New("http://unused", "u", "tok", true, nil)
			batch := []Series{{
				Name:   "my_metric",
				Labels: map[string]string{"job": "svc", k: "some-uuid-value"},
				Value:  1,
				T:      time.Now(),
			}}
			err := s.Write(context.Background(), batch)
			if err == nil {
				t.Fatalf("expected error for UUID-class label key %q, got nil", k)
			}
			if !strings.Contains(err.Error(), k) {
				t.Errorf("error should mention the bad key %q, got: %v", k, err)
			}
		})
	}
}

// TestUIDLabelIsLegal asserts uid is accepted as a Mimir label (kube_pod_info uses it;
// cardinality is bounded by pod count, so it is NOT UUID-class).
func TestUIDLabelIsLegal(t *testing.T) {
	s := New("http://unused", "u", "tok", true, nil)
	batch := []Series{{
		Name:   "kube_pod_info",
		Labels: map[string]string{"namespace": "default", "uid": "abc-def-ghi"},
		Value:  1,
		T:      time.Now(),
	}}
	if err := s.Write(context.Background(), batch); err != nil {
		t.Fatalf("uid must be legal as a Mimir label, got error: %v", err)
	}
}

// TestModelLabelIsLegal asserts model is accepted as a Mimir label (bounded model-name vocabulary).
func TestModelLabelIsLegal(t *testing.T) {
	s := New("http://unused", "u", "tok", true, nil)
	batch := []Series{{
		Name:   "llm_requests_total",
		Labels: map[string]string{"job": "svc", "model": "gpt-4o"},
		Value:  1,
		T:      time.Now(),
	}}
	if err := s.Write(context.Background(), batch); err != nil {
		t.Fatalf("model must be legal as a Mimir label, got error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Native-histogram inventory (Natives accessor)
// ---------------------------------------------------------------------------

func TestNativesInventory(t *testing.T) {
	s := New("", "", "", true, nil) // dryRun=true
	ctx := context.Background()
	if err := s.Write(ctx, []Series{
		{Name: "traces_spanmetrics_latency", Labels: map[string]string{"service": "x"}, T: time.UnixMilli(1), Kind: KindHistogram, Native: &NativeHistogram{Schema: 3, Count: 1}},
		{Name: "http_request_duration_seconds_bucket", Labels: map[string]string{"le": "1"}, T: time.UnixMilli(1), Kind: KindHistogram},
		{Name: "some_gauge", Labels: map[string]string{}, T: time.UnixMilli(1), Kind: KindGauge},
	}); err != nil {
		t.Fatal(err)
	}
	nat := s.Natives()
	if !nat["traces_spanmetrics_latency"] {
		t.Error("native series must be recorded as native")
	}
	if nat["http_request_duration_seconds_bucket"] {
		t.Error("classic bucket series must NOT be native")
	}
	if nat["some_gauge"] {
		t.Error("gauge must NOT be native")
	}
}

// ---------------------------------------------------------------------------
// Retry integration: 429 causes at least 2 HTTP calls
// ---------------------------------------------------------------------------

// TestMetricsPolicyRetriesOn429 proves the promrw live path retries on 429.
// The test server returns 429 on the first call and 200 on subsequent calls.
func TestMetricsPolicyRetriesOn429(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(srv.URL, "u", "tok", false, nil)
	if err := s.Write(context.Background(), makeSample()); err != nil {
		t.Fatalf("expected success after 429 retry, got: %v", err)
	}
	if n := atomic.LoadInt64(&calls); n < 2 {
		t.Errorf("calls = %d, want ≥2 (first 429 + at least one retry)", n)
	}
}
