// SPDX-License-Identifier: AGPL-3.0-only

package faro

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

// TestURLBuildsCollectPath verifies the appKey is appended as a single path segment
// with no double slash, regardless of a trailing slash on the collector base.
func TestURLBuildsCollectPath(t *testing.T) {
	cases := map[string]string{
		"https://faro-collector-prod-gb-south-1.grafana.net/collect":  "https://faro-collector-prod-gb-south-1.grafana.net/collect/KEY",
		"https://faro-collector-prod-gb-south-1.grafana.net/collect/": "https://faro-collector-prod-gb-south-1.grafana.net/collect/KEY",
	}
	for base, want := range cases {
		if got := New(base, "KEY", true).URL(); got != want {
			t.Errorf("New(%q).URL() = %q, want %q", base, got, want)
		}
	}
}

// TestPayloadJSONContract pins the JSON field names the collector decodes. A rename
// here (e.g. trace_id → traceId) would silently break ingestion, so assert the wire shape.
func TestPayloadJSONContract(t *testing.T) {
	p := Payload{
		Meta: Meta{
			SDK:     SDK{Name: "faro-web", Version: "2.7.0"},
			App:     App{Name: "acme-frontend", Namespace: "ui", Version: "2.4.1", Environment: "prod"},
			Session: Session{ID: "abc"},
			Page:    Page{ID: "/", URL: "https://x/"},
			View:    View{Name: "/"},
			Browser: Browser{Name: "Chrome", OS: "Mac OS 10.15.7"},
		},
		Measurements: []Measurement{{
			Type:    "web-vitals",
			Values:  map[string]float64{"lcp": 312},
			Context: map[string]string{"rating": "good"},
			Trace:   &TraceContext{TraceID: "t", SpanID: "s"},
		}},
		Events: []Event{
			{Name: "faro.user.action", Domain: "browser", Action: &Action{ID: "act-1", Name: "submit-query"},
				Attributes: map[string]string{"userActionDuration": "124"}},
			{Name: "faro.tracing.fetch", Domain: "browser", Action: &Action{Name: "submit-query", ParentID: "act-1"},
				Attributes: map[string]string{"http.method": "POST"}},
		},
		Exceptions: []Exception{{Type: "TypeError", Value: "boom", Stacktrace: &Stacktrace{Frames: []Frame{{Filename: "app.js", Lineno: 1}}}}},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	meta := raw["meta"].(map[string]any)
	app := meta["app"].(map[string]any)
	if app["name"] != "acme-frontend" {
		t.Errorf("meta.app.name = %v", app["name"])
	}
	if meta["sdk"].(map[string]any)["name"] != "faro-web" {
		t.Errorf("meta.sdk.name missing/wrong")
	}
	m0 := raw["measurements"].([]any)[0].(map[string]any)
	if m0["type"] != "web-vitals" {
		t.Errorf("measurement.type = %v", m0["type"])
	}
	if m0["values"].(map[string]any)["lcp"].(float64) != 312 {
		t.Errorf("measurement.values.lcp wrong")
	}
	if tr := m0["trace"].(map[string]any); tr["trace_id"] != "t" || tr["span_id"] != "s" {
		t.Errorf("measurement.trace ids wrong: %v", tr)
	}
	events := raw["events"].([]any)
	// Parent user-action event: action.id + action.name, NO parentId (the User-actions view
	// groups parents on event_name=faro.user.action + action_id; collector JSON names are id/name/parentId).
	parent := events[0].(map[string]any)
	if parent["name"] != "faro.user.action" {
		t.Errorf("parent event.name = %v, want faro.user.action", parent["name"])
	}
	pa := parent["action"].(map[string]any)
	if pa["id"] != "act-1" || pa["name"] != "submit-query" {
		t.Errorf("parent action id/name wrong: %v", pa)
	}
	if _, hasParent := pa["parentId"]; hasParent {
		t.Errorf("parent action must omit parentId, got %v", pa["parentId"])
	}
	// Child fetch event: action.name + action.parentId (collector → action_parent_id), NO own id.
	e0 := events[1].(map[string]any)
	if e0["name"] != "faro.tracing.fetch" {
		t.Errorf("event.name = %v", e0["name"])
	}
	ca := e0["action"].(map[string]any)
	if ca["parentId"] != "act-1" || ca["name"] != "submit-query" {
		t.Errorf("child action name/parentId wrong: %v", ca)
	}
	x0 := raw["exceptions"].([]any)[0].(map[string]any)
	if x0["type"] != "TypeError" {
		t.Errorf("exception.type = %v", x0["type"])
	}
}

// TestWritePostsOneBeaconPerPayload verifies each non-empty payload is POSTed once to
// the collect URL and empty payloads are skipped.
func TestWritePostsOneBeaconPerPayload(t *testing.T) {
	var posts, bodiesSeen, sessHdrSeen int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&posts, 1)
		// Handlers run on concurrent goroutines (maxConcurrentPOSTs) — keep all access atomic/local.
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			atomic.AddInt64(&bodiesSeen, 1)
		}
		if r.Header.Get("X-Faro-Session-Id") != "" {
			atomic.AddInt64(&sessHdrSeen, 1)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	s := New(srv.URL+"/collect", "KEY", false)
	payloads := []Payload{
		{Meta: Meta{App: App{Name: "a"}, Session: Session{ID: "s1"}}, Events: []Event{{Name: "e", Timestamp: time.Now()}}},
		{Meta: Meta{App: App{Name: "a"}, Session: Session{ID: "s2"}}},                                       // no signals — must be skipped
		{Meta: Meta{App: App{Name: "a"}}, Exceptions: []Exception{{Type: "T"}}},                             // no session id — must be skipped
		{Meta: Meta{App: App{Name: "a"}, Session: Session{ID: "s3"}}, Exceptions: []Exception{{Type: "T"}}}, // posts
	}
	if err := s.Write(context.Background(), payloads); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := atomic.LoadInt64(&posts); got != 2 {
		t.Errorf("posts = %d, want 2 (no-signal + no-session payloads skipped)", got)
	}
	if atomic.LoadInt64(&bodiesSeen) != 2 {
		t.Errorf("non-empty bodies = %d, want 2", bodiesSeen)
	}
	if atomic.LoadInt64(&sessHdrSeen) != 2 {
		t.Errorf("X-Faro-Session-Id headers seen = %d, want 2 (required by collector)", sessHdrSeen)
	}
}

// TestWriteGzipsBeacon verifies each beacon is gzip-compressed with Content-Encoding: gzip
// and the decompressed body round-trips to the JSON payload contract. The Faro collector
// accepts gzip (the browser SDK uses it), so this cuts beacon transfer with no contract change.
func TestWriteGzipsBeacon(t *testing.T) {
	var gotEncoding, gotType string
	var decoded Payload
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
			t.Errorf("decompressed body is not valid payload JSON: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	s := New(srv.URL+"/collect", "KEY", false)
	p := Payload{Meta: Meta{App: App{Name: "acme-frontend"}, Session: Session{ID: "s1"}}, Events: []Event{{Name: "e", Timestamp: time.Now()}}}
	if err := s.Write(context.Background(), []Payload{p}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if gotEncoding != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", gotEncoding)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotType)
	}
	if decoded.Meta.App.Name != "acme-frontend" || decoded.Meta.Session.ID != "s1" {
		t.Errorf("decompressed beacon did not round-trip: %+v", decoded.Meta)
	}
}

// TestWriteSurfacesError returns the first non-2xx as an error.
func TestWriteSurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	s := New(srv.URL, "KEY", false)
	err := s.Write(context.Background(), []Payload{{Meta: Meta{Session: Session{ID: "s1"}}, Events: []Event{{Name: "e"}}}})
	if err == nil {
		t.Fatal("expected error on 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should carry the status code, got %q", err)
	}
}

// TestPostRetriesTransportError proves a transport-level failure (e.g. the EOF the collector
// returns when it closes an idle keep-alive connection the client tries to reuse — Go does NOT
// auto-retry POSTs, so this would otherwise surface as a tick error) is retried rather than
// failing the beacon. The handler kills the first connection mid-request (client sees a
// connection error / EOF), then accepts the retry.
func TestPostRetriesTransportError(t *testing.T) {
	var attempts int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&attempts, 1)
		if n == 1 {
			// Hijack and close without writing a response → client gets a connection error / EOF.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("server does not support hijacking")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			conn.Close()
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	s := New(srv.URL, "KEY", false)
	p := Payload{Meta: Meta{App: App{Name: "acme-frontend"}, Session: Session{ID: "s1"}}, Events: []Event{{Name: "e", Timestamp: time.Now()}}}
	if err := s.Write(context.Background(), []Payload{p}); err != nil {
		t.Fatalf("Write should succeed after retrying the transport error, got: %v", err)
	}
	if got := atomic.LoadInt64(&attempts); got < 2 {
		t.Errorf("attempts = %d, want ≥2 (first killed, retry succeeds)", got)
	}
}

// ---------------------------------------------------------------------------
// Observe hook
// ---------------------------------------------------------------------------

func TestObserveDryRun(t *testing.T) {
	s := New("http://unused/collect", "KEY", true)
	var got pushhook.Event
	s.Observe = func(_ context.Context, ev pushhook.Event) { got = ev }

	payloads := []Payload{
		{Meta: Meta{Session: Session{ID: "s1"}}, Events: []Event{{Name: "e1"}}},
		{Meta: Meta{Session: Session{ID: "s2"}}, Events: []Event{{Name: "e2"}}},
	}
	if err := s.Write(context.Background(), payloads); err != nil {
		t.Fatalf("dry-run Write: %v", err)
	}
	if got.Sink != "faro" {
		t.Errorf("Sink = %q, want faro", got.Sink)
	}
	// Items = len(payloads) on the dry-run path (before skipping empty/no-session beacons)
	if got.Items != 2 {
		t.Errorf("Items = %d, want 2", got.Items)
	}
	if !got.DryRun {
		t.Errorf("DryRun = false, want true")
	}
	if got.Blueprint != "" {
		t.Errorf("Blueprint = %q, want empty (faro has no blueprint label)", got.Blueprint)
	}
}

func TestObserveNilSafe(t *testing.T) {
	// No Observe set: must not panic.
	s := New("http://unused/collect", "KEY", true)
	if err := s.Write(context.Background(), []Payload{
		{Meta: Meta{Session: Session{ID: "s1"}}, Events: []Event{{Name: "e"}}},
	}); err != nil {
		t.Fatalf("nil-observer Write: %v", err)
	}
}

func TestObserveLivePushSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	s := New(srv.URL+"/collect", "KEY", false)
	var got pushhook.Event
	s.Observe = func(_ context.Context, ev pushhook.Event) { got = ev }

	payloads := []Payload{
		{Meta: Meta{App: App{Name: "a"}, Session: Session{ID: "s1"}}, Events: []Event{{Name: "e", Timestamp: time.Now()}}},
		{Meta: Meta{App: App{Name: "a"}, Session: Session{ID: "s2"}}, Events: []Event{{Name: "e", Timestamp: time.Now()}}},
	}
	if err := s.Write(context.Background(), payloads); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got.Sink != "faro" || got.Status != 200 || got.Err != nil {
		t.Fatalf("unexpected event: %+v", got)
	}
	if got.Items != 2 {
		t.Errorf("Items = %d, want 2 (posted beacons)", got.Items)
	}
	if got.DryRun {
		t.Errorf("DryRun should be false on a live push")
	}
}

// TestDryRunDoesNotPost confirms dry-run never opens a socket.
func TestDryRunDoesNotPost(t *testing.T) {
	var posts int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&posts, 1)
	}))
	defer srv.Close()
	s := New(srv.URL, "KEY", true)
	if err := s.Write(context.Background(), []Payload{{Events: []Event{{Name: "e"}}}}); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&posts) != 0 {
		t.Error("dry-run must not POST")
	}
}

// ---------------------------------------------------------------------------
// Retry: 429 → success within one Write
// ---------------------------------------------------------------------------

// TestPostRetriesOn429 proves that a 429 response from the Faro collector triggers a
// retry and the beacon succeeds when the server accepts on the next attempt.
// This covers the gap in the old hand-rolled retry which only retried transport errors.
func TestPostRetriesOn429(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	s := New(srv.URL+"/collect", "KEY", false)
	p := Payload{
		Meta:   Meta{App: App{Name: "acme-frontend"}, Session: Session{ID: "s1"}},
		Events: []Event{{Name: "e", Timestamp: time.Now()}},
	}
	if err := s.Write(context.Background(), []Payload{p}); err != nil {
		t.Fatalf("expected success after 429 retry, got: %v", err)
	}
	if n := atomic.LoadInt64(&calls); n < 2 {
		t.Errorf("calls = %d, want ≥2 (first 429 + at least one retry)", n)
	}
}
