// SPDX-License-Identifier: AGPL-3.0-only

package jsondata

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ledger"
)

// ── fake Source ──────────────────────────────────────────────────────────────────────

type fakeSource struct {
	blueprints []string
	reqs       []*ledger.Request
}

func (f *fakeSource) Blueprints() []string { return f.blueprints }

func (f *fakeSource) Recent(blueprint string, _ time.Time, _ time.Duration) []*ledger.Request {
	if blueprint == "" {
		return f.reqs
	}
	var out []*ledger.Request
	for _, r := range f.reqs {
		// Workload is used as a blueprint proxy in fake data.
		if strings.HasPrefix(r.Workload, blueprint) {
			out = append(out, r)
		}
	}
	return out
}

// WindowStats mirrors Recent over the fake's held requests — the fake has no cap, so count/distinct
// are derived directly (the cap-independence property is unit-tested in the ledger package).
func (f *fakeSource) WindowStats(blueprint string, now time.Time, window time.Duration) ledger.WindowStats {
	reqs := f.Recent(blueprint, now, window)
	seen := map[string]struct{}{}
	for _, r := range reqs {
		seen[r.Workload] = struct{}{}
	}
	wls := make([]string, 0, len(seen))
	for w := range seen {
		wls = append(wls, w)
	}
	sort.Strings(wls)
	return ledger.WindowStats{Count: len(reqs), Workloads: wls}
}

// makeRequest builds a synthetic ledger.Request for testing.
func makeRequest(blueprint, workload, env, cluster, route string, outcome ledger.Outcome) *ledger.Request {
	c := ledger.NewCorrelation()
	return &ledger.Request{
		Correlation: c,
		Workload:    workload,
		Env:         env,
		Cluster:     cluster,
		Route:       route,
		Start:       time.Now().Add(-time.Minute),
		Duration:    250 * time.Millisecond,
		Outcome:     outcome,
		StatusCode:  200,
	}
}

func newTestServer(src Source) http.Handler { return NewServer(src) }

// ── helper: do a GET request and return the recorder ────────────────────────────────

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	h.ServeHTTP(rec, req)
	return rec
}

func getWithHeader(h http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	h.ServeHTTP(rec, req)
	return rec
}

func post(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	h.ServeHTTP(rec, req)
	return rec
}

// ── route index ──────────────────────────────────────────────────────────────────────

func TestRouteIndex(t *testing.T) {
	h := newTestServer(&fakeSource{})
	rec := get(h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("/ want 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"routes", "/healthz", "/blueprints", "/request_correlation_sample", "/requests"} {
		if !strings.Contains(body, want) {
			t.Errorf("/ index missing %q in body:\n%s", want, body)
		}
	}
}

func TestRouteIndexContentType(t *testing.T) {
	h := newTestServer(&fakeSource{})
	rec := get(h, "/")
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("/ Content-Type want application/json, got %q", ct)
	}
}

func TestRouteIndexNotFound(t *testing.T) {
	h := newTestServer(&fakeSource{})
	rec := get(h, "/nonexistent-path")
	if rec.Code != http.StatusNotFound {
		t.Errorf("/nonexistent want 404, got %d", rec.Code)
	}
}

// ── /healthz ─────────────────────────────────────────────────────────────────────────

func TestHealthz(t *testing.T) {
	h := newTestServer(&fakeSource{})
	rec := get(h, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Errorf("/healthz body want 'ok', got %q", rec.Body.String())
	}
}

// ── GET-only invariant (I26) ─────────────────────────────────────────────────────────

func TestGETOnlyRoutes(t *testing.T) {
	h := newTestServer(&fakeSource{blueprints: []string{"acme"}})
	paths := []string{"/", "/blueprints", "/request_correlation_sample", "/requests"}
	for _, p := range paths {
		rec := post(h, p)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s want 405, got %d", p, rec.Code)
		}
	}
}

// ── /blueprints ───────────────────────────────────────────────────────────────────────

func TestBlueprintsRoute(t *testing.T) {
	src := &fakeSource{
		blueprints: []string{"acme", "beta"},
		reqs: []*ledger.Request{
			makeRequest("acme", "acme-api", "prod", "acme-prod", "GET /v1/items", ledger.OutcomeSuccess),
			makeRequest("acme", "acme-worker", "prod", "acme-prod", "POST /v1/jobs", ledger.OutcomeSuccess),
		},
	}
	h := newTestServer(src)
	rec := get(h, "/blueprints")
	if rec.Code != http.StatusOK {
		t.Fatalf("/blueprints want 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "blueprints") {
		t.Errorf("/blueprints body missing 'blueprints' key:\n%s", body)
	}
	if !strings.Contains(body, "acme") {
		t.Errorf("/blueprints body missing 'acme':\n%s", body)
	}
}

// ── /request_correlation_sample ─────────────────────────────────────────────────────────────

func TestRequestCorrelationSampleShape(t *testing.T) {
	rq := makeRequest("acme", "acme-api", "prod", "acme-prod", "GET /v1/items", ledger.OutcomeSuccess)
	src := &fakeSource{
		blueprints: []string{"acme"},
		reqs:       []*ledger.Request{rq},
	}
	h := newTestServer(src)
	rec := get(h, "/request_correlation_sample")
	if rec.Code != http.StatusOK {
		t.Fatalf("/request_correlation_sample want 200, got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, rec.Body.String())
	}

	samples, ok := payload["samples"].([]any)
	if !ok {
		t.Fatalf("want samples array, got %T; body: %s", payload["samples"], rec.Body.String())
	}
	if len(samples) == 0 {
		t.Fatal("want at least one sample")
	}

	// All correlation keys must be present in each sample.
	correlationKeys := []string{
		"correlation_id", "trace_id", "span_id", "session_id", "request_id",
	}
	// Identity dims.
	identityKeys := []string{"workload", "env", "cluster", "route"}
	// Facts.
	factKeys := []string{"status", "duration_ms", "calls"}

	s := samples[0].(map[string]any)
	for _, k := range append(append(correlationKeys, identityKeys...), factKeys...) {
		if _, present := s[k]; !present {
			t.Errorf("sample missing key %q; sample: %v", k, s)
		}
	}
	// Hints array must be present.
	if _, ok := s["hints"]; !ok {
		t.Error("sample missing 'hints'")
	}
}

func TestRequestCorrelationAllKeys(t *testing.T) {
	// Verify all six Correlation fields propagate to the JSON.
	rq := makeRequest("acme", "acme-api", "prod", "acme-prod", "GET /v1/items", ledger.OutcomeSuccess)
	src := &fakeSource{blueprints: []string{"acme"}, reqs: []*ledger.Request{rq}}
	h := newTestServer(src)
	rec := get(h, "/request_correlation_sample")

	body := rec.Body.String()
	// The IDs minted by NewCorrelation must appear somewhere in the payload.
	for _, id := range []string{rq.CorrelationID, rq.TraceID, rq.SpanID} {
		if !strings.Contains(body, id) {
			t.Errorf("request_correlation_sample missing correlation id %q", id)
		}
	}
}

// ── /requests — window parsing + default ────────────────────────────────────────────

func TestRequestsWindowDefault(t *testing.T) {
	// No window param → should return rows without error.
	src := &fakeSource{
		blueprints: []string{"acme"},
		reqs: []*ledger.Request{
			makeRequest("acme", "acme-api", "prod", "acme-prod", "GET /v1/items", ledger.OutcomeSuccess),
		},
	}
	h := newTestServer(src)
	rec := get(h, "/requests")
	if rec.Code != http.StatusOK {
		t.Fatalf("/requests want 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "rows") {
		t.Error("/requests body missing 'rows'")
	}
}

func TestRequestsWindowParam(t *testing.T) {
	src := &fakeSource{
		blueprints: []string{"acme"},
		reqs: []*ledger.Request{
			makeRequest("acme", "acme-api", "prod", "acme-prod", "GET /v1/items", ledger.OutcomeSuccess),
		},
	}
	h := newTestServer(src)
	// explicit window
	rec := get(h, "/requests?blueprint=acme&window=30m")
	if rec.Code != http.StatusOK {
		t.Fatalf("/requests?window=30m want 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["window"] != "30m0s" {
		t.Errorf("window want '30m0s', got %v", payload["window"])
	}
}

func TestRequestsInvalidWindow(t *testing.T) {
	// Invalid window string → falls back to 15m default without error.
	src := &fakeSource{blueprints: []string{"acme"}}
	h := newTestServer(src)
	rec := get(h, "/requests?window=BADVALUE")
	if rec.Code != http.StatusOK {
		t.Fatalf("/requests?window=BADVALUE want 200, got %d", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Should have fallen back to the 15m default.
	if payload["window"] != "15m0s" {
		t.Errorf("bad window should fall back to 15m0s, got %v", payload["window"])
	}
}

// ── row cap ──────────────────────────────────────────────────────────────────────────

func TestRequestsRowCap(t *testing.T) {
	// Build >500 requests.
	reqs := make([]*ledger.Request, 600)
	for i := range reqs {
		reqs[i] = makeRequest("acme", "acme-api", "prod", "acme-prod", "GET /v1/items", ledger.OutcomeSuccess)
	}
	src := &fakeSource{blueprints: []string{"acme"}, reqs: reqs}
	// fakeSource.Recent("acme", ...) filters by workload prefix "acme" so all 600 match.
	h := newTestServer(src)
	rec := get(h, "/requests?blueprint=acme")
	if rec.Code != http.StatusOK {
		t.Fatalf("/requests want 200, got %d", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rows, ok := payload["rows"].([]any)
	if !ok {
		t.Fatalf("rows not an array, got %T", payload["rows"])
	}
	if len(rows) > maxRequestRows {
		t.Errorf("row cap %d exceeded: got %d rows", maxRequestRows, len(rows))
	}
}

// ── CORS echo (I26) ──────────────────────────────────────────────────────────────────

func TestCORSOriginEchoed(t *testing.T) {
	h := newTestServer(&fakeSource{})
	rec := getWithHeader(h, "/blueprints", map[string]string{
		"Origin": "https://grafana.example.com",
	})
	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "https://grafana.example.com" {
		t.Errorf("CORS origin: want 'https://grafana.example.com', got %q", got)
	}
}

func TestCORSRequestedHeadersReflected(t *testing.T) {
	h := newTestServer(&fakeSource{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/blueprints", nil)
	req.Header.Set("Origin", "https://grafana.example.com")
	req.Header.Set("Access-Control-Request-Headers", "x-grafana-device-id, Authorization")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS preflight want 204, got %d", rec.Code)
	}
	got := rec.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(got, "x-grafana-device-id") {
		t.Errorf("CORS reflect headers want x-grafana-device-id, got %q", got)
	}
}

func TestCORSNoOriginStar(t *testing.T) {
	// No Origin header → Access-Control-Allow-Origin: *
	h := newTestServer(&fakeSource{})
	rec := get(h, "/healthz")
	// healthz bypasses middleware so check via a real middleware route.
	rec = get(h, "/blueprints")
	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "*" {
		t.Errorf("no-origin CORS want '*', got %q", got)
	}
}

// ── no-cache header ──────────────────────────────────────────────────────────────────

func TestNoCacheHeader(t *testing.T) {
	h := newTestServer(&fakeSource{blueprints: []string{"acme"}})
	rec := get(h, "/blueprints")
	cc := rec.Header().Get("Cache-Control")
	if cc != "no-store" {
		t.Errorf("Cache-Control want 'no-store', got %q", cc)
	}
}
