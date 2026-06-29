// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/faro"
	"github.com/rknightion/synthkit/internal/telemetryspec/profiles"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// buildRUMApp builds an app workload with a "frontend" entry + "api" backend; the
// frontend carries the rum_faro profile. The binding carries a RUMCapture.
func buildRUMApp(t *testing.T, rum *coretest.RUMCapture) *Workload {
	t.Helper()
	// Ensure rum_faro profile is registered.
	_, ok := profiles.Lookup("rum_faro")
	if !ok {
		t.Fatal("rum_faro profile not registered — missing import?")
	}
	cfg := &Config{
		Traffic: Traffic{OffPeakRPS: 10, PeakRPS: 50},
		Services: []ServiceNode{
			{
				Name:     "fe",
				Type:     "frontend",
				Entry:    true,
				Profiles: []string{"rum_faro"},
				Calls:    []string{"api"},
			},
			{
				Name: "api",
				Type: "web",
			},
		},
	}
	w, err := build(cfg, core.Binding{
		Name:    "rum-test",
		Env:     coretest.Env(),
		Cluster: coretest.Cluster(),
		RUM:     rum,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return w.(*Workload)
}

// buildRUMBatch mints a small request batch with BrowserOrigin=true on each request,
// correlated to this workload.
func buildRUMBatch(w *Workload, n int) []*ledger.Request {
	eng := shape.New("", nil)
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	batch := make([]*ledger.Request, 0, n)
	for range n {
		r := &ledger.Request{
			Correlation:   ledger.NewCorrelation(),
			Workload:      w.Name(),
			Env:           "prod",
			Route:         "GET /api/v1/data",
			BrowserOrigin: true,
			Start:         now,
			Duration:      200 * time.Millisecond,
		}
		r.Outcome, r.StatusCode, r.ErrorKind = ledger.OutcomeSuccess, 200, ""
		_ = eng
		batch = append(batch, r)
	}
	return batch
}

// TestBrowserSpanNameIsMethodOnly: the rum_faro browser CLIENT span (the frontend entry root) must
// RENDER its name as the HTTP method only ("GET"), not the full route — per signals/logs.md
// [slug: logs-browser-spans] (M3). This is the integration check the profile-shape unit test can't make:
// it caught that NameTemplate must reference the existing "http.method" attr key to resolve.
func TestBrowserSpanNameIsMethodOnly(t *testing.T) {
	w := buildRUMApp(t, &coretest.RUMCapture{})
	batch := buildRUMBatch(w, 1) // Route "GET /api/v1/data" → method "GET"
	r := batch[0]
	tc := &coretest.TraceCapture{}
	world := coretest.World(nil, nil, tc)
	if err := w.ProjectBatch(context.Background(), time.Now(), world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}
	var found bool
	for _, res := range tc.Resources {
		for _, sp := range res.Spans {
			if sp.SpanID == r.SpanID { // the entry CLIENT root = the browser span
				found = true
				if sp.Name != "GET" {
					t.Errorf("browser span name = %q, want %q (method-only, not the full route)", sp.Name, "GET")
				}
			}
		}
	}
	if !found {
		t.Fatal("entry root span (SpanID == r.SpanID) not found")
	}
}

// TestActionNameIsSlashFree: the Faro user-action name must be a low-card, SLASH-FREE intent label —
// a "/" breaks the FEO action/session drill-in URL ("not found"). The route PATH (with slashes) belongs
// in page_id/view, not the action name. (Repro: the /v1/assist action 404'd in FEO; others worked.)
func TestActionNameIsSlashFree(t *testing.T) {
	cases := map[string]string{
		"POST /v1/assist":   "submit assist",
		"GET /api/v1/data":  "load data",
		"DELETE /v1/things": "submit things",
		"GET /":             "load",
	}
	for route, want := range cases {
		got := appActionName(route)
		if got != want {
			t.Errorf("appActionName(%q) = %q, want %q", route, got, want)
		}
		if strings.Contains(got, "/") {
			t.Errorf("appActionName(%q) = %q contains a slash — breaks FEO drill-in", route, got)
		}
	}
}

// ─── Lane 0: RUMCapture tests ──────────────────────────────────────────────────

// TestRUMCapture_ImplementsInterface: *RUMCapture satisfies core.RUMSink.
func TestRUMCapture_ImplementsInterface(t *testing.T) {
	var _ core.RUMSink = (*coretest.RUMCapture)(nil)
}

// TestRUMCapture_RecordsPayloads: Write accumulates payloads; All returns them.
func TestRUMCapture_RecordsPayloads(t *testing.T) {
	rc := &coretest.RUMCapture{}
	p1 := faro.Payload{Meta: faro.Meta{Session: faro.Session{ID: "sess-1"}}}
	p2 := faro.Payload{Meta: faro.Meta{Session: faro.Session{ID: "sess-2"}}}
	if err := rc.Write(context.Background(), []faro.Payload{p1}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := rc.Write(context.Background(), []faro.Payload{p2}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	all := rc.All()
	if len(all) != 2 {
		t.Fatalf("All() = %d payloads, want 2", len(all))
	}
	if all[0].Meta.Session.ID != "sess-1" {
		t.Errorf("all[0].SessionID = %q, want sess-1", all[0].Meta.Session.ID)
	}
	if all[1].Meta.Session.ID != "sess-2" {
		t.Errorf("all[1].SessionID = %q, want sess-2", all[1].Meta.Session.ID)
	}
}

// ─── Lane 1: rumEnabled + Signals ─────────────────────────────────────────────

// TestRumEnabled_WithFrontendProfileAndSink: rumEnabled() returns true when the entry
// node carries rum_faro + binding.RUM is set.
func TestRumEnabled_WithFrontendProfileAndSink(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)
	if !w.rumEnabled() {
		t.Fatal("rumEnabled() = false, want true (frontend entry + rum_faro profile + RUM sink)")
	}
}

// TestRumEnabled_NilSink: rumEnabled() returns false when no RUM sink is wired.
func TestRumEnabled_NilSink(t *testing.T) {
	cfg := &Config{
		Services: []ServiceNode{
			{Name: "fe", Type: "frontend", Entry: true, Profiles: []string{"rum_faro"}},
		},
	}
	w, err := build(cfg, core.Binding{Name: "no-rum", Env: coretest.Env()})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if w.(*Workload).rumEnabled() {
		t.Fatal("rumEnabled() = true, want false (no RUM sink)")
	}
}

// TestRumEnabled_NoRumFaroProfile: rumEnabled() returns false when the entry node
// doesn't carry rum_faro.
func TestRumEnabled_NoRumFaroProfile(t *testing.T) {
	rum := &coretest.RUMCapture{}
	cfg := &Config{
		Services: []ServiceNode{
			{Name: "fe", Type: "frontend", Entry: true}, // no rum_faro profile
		},
	}
	w, err := build(cfg, core.Binding{Name: "no-rum-profile", Env: coretest.Env(), RUM: rum})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if w.(*Workload).rumEnabled() {
		t.Fatal("rumEnabled() = true, want false (no rum_faro profile on entry)")
	}
}

// TestAppRUM_SignalsContainsRUM: Signals() includes core.RUM when rumEnabled.
func TestAppRUM_SignalsContainsRUM(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)
	sigs := w.Signals()
	if !slices.Contains(sigs, core.RUM) {
		t.Fatalf("Signals() does not contain core.RUM: %v", sigs)
	}
}

// TestAppRUM_SignalsNoRUMWhenDisabled: Signals() does NOT include RUM when rumEnabled=false.
func TestAppRUM_SignalsNoRUMWhenDisabled(t *testing.T) {
	cfg := &Config{
		Services: []ServiceNode{
			{Name: "api", Type: "web", Entry: true},
		},
	}
	w, err := build(cfg, core.Binding{Name: "no-rum", Env: coretest.Env()})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	sigs := w.(*Workload).Signals()
	if slices.Contains(sigs, core.RUM) {
		t.Fatal("Signals() must not include core.RUM when rumEnabled=false")
	}
}

// ─── Lane 1: projectRUM ────────────────────────────────────────────────────────

// TestProjectRUM_EmitsOneBeaconPerBrowserRequest: projectRUM posts exactly one Faro
// beacon per browser-origin request in the batch; non-browser requests are skipped.
func TestProjectRUM_EmitsOneBeaconPerBrowserRequest(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)

	eng := shape.New("", nil)
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)

	// 2 browser-origin + 1 non-browser.
	batch := []*ledger.Request{
		{Correlation: ledger.NewCorrelation(), Workload: w.Name(), Route: "GET /api/v1/data", BrowserOrigin: true, Start: now, Duration: 200 * time.Millisecond},
		{Correlation: ledger.NewCorrelation(), Workload: w.Name(), Route: "GET /api/v1/items", BrowserOrigin: true, Start: now, Duration: 150 * time.Millisecond},
		{Correlation: ledger.NewCorrelation(), Workload: w.Name(), Route: "POST /api/v1/data", BrowserOrigin: false, Start: now, Duration: 100 * time.Millisecond},
	}
	for _, r := range batch {
		r.Outcome, r.StatusCode, r.ErrorKind = ledger.OutcomeSuccess, 200, ""
	}
	_ = eng

	ctx := context.Background()
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tc := &coretest.TraceCapture{}
	world := coretest.World(mc, lc, tc)

	if err := w.ProjectBatch(ctx, now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	payloads := rum.All()
	if len(payloads) != 2 {
		t.Fatalf("RUM payloads = %d, want 2 (one per browser-origin request)", len(payloads))
	}
}

// TestProjectRUM_NilSinkSkips: projectRUM is a no-op when b.RUM is nil.
func TestProjectRUM_NilSinkSkips(t *testing.T) {
	cfg := &Config{
		Traffic: Traffic{OffPeakRPS: 5, PeakRPS: 20},
		Services: []ServiceNode{
			{Name: "fe", Type: "frontend", Entry: true, Profiles: []string{"rum_faro"}},
		},
	}
	w, err := build(cfg, core.Binding{Name: "no-rum", Env: coretest.Env()})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	batch := []*ledger.Request{
		{Correlation: ledger.NewCorrelation(), Workload: w.(*Workload).Name(), Route: "GET /", BrowserOrigin: true, Start: now, Duration: 100 * time.Millisecond},
	}
	batch[0].Outcome, batch[0].StatusCode, batch[0].ErrorKind = ledger.OutcomeSuccess, 200, ""

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	// Should not panic or error.
	if err := w.(*Workload).ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch with nil RUM sink: %v", err)
	}
}

// TestProjectRUM_BeaconHasSessionID: every beacon has a non-empty session ID (required
// by the Faro collector — it rejects beacons with X-Faro-Session-Id missing).
func TestProjectRUM_BeaconHasSessionID(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)
	batch := buildRUMBatch(w, 3)

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	for i, p := range rum.All() {
		if p.Meta.Session.ID == "" {
			t.Errorf("beacon[%d]: empty session ID (collector will reject it)", i)
		}
	}
}

// TestProjectRUM_BeaconHasWebVitals: each beacon carries all 5 web-vital measurements.
func TestProjectRUM_BeaconHasWebVitals(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)
	batch := buildRUMBatch(w, 1)

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	payloads := rum.All()
	if len(payloads) == 0 {
		t.Fatal("no payloads emitted")
	}
	p := payloads[0]
	if len(p.Measurements) != 5 {
		t.Fatalf("beacon has %d measurements, want 5 (LCP/CLS/INP/TTFB/FCP)", len(p.Measurements))
	}

	// Each must be type=web-vitals.
	for i, m := range p.Measurements {
		if m.Type != "web-vitals" {
			t.Errorf("measurement[%d] type=%q, want web-vitals", i, m.Type)
		}
	}

	// Each must carry the main metric key + delta + context_rating.
	wantKeys := map[string]string{
		"lcp": "lcp", "cls": "cls", "inp": "inp", "ttfb": "ttfb", "fcp": "fcp",
	}
	seenKeys := map[string]bool{}
	for _, m := range p.Measurements {
		for k := range wantKeys {
			if _, ok := m.Values[k]; ok {
				seenKeys[k] = true
			}
		}
	}
	for k := range wantKeys {
		if !seenKeys[k] {
			t.Errorf("no measurement carries Values key %q", k)
		}
	}
}

// TestProjectRUM_BeaconHasEvents: each beacon carries session_start, faro.user.action,
// and faro.tracing.fetch events.
func TestProjectRUM_BeaconHasEvents(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)
	batch := buildRUMBatch(w, 1)

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	p := rum.All()[0]
	eventNames := map[string]bool{}
	for _, e := range p.Events {
		eventNames[e.Name] = true
	}
	for _, want := range []string{"session_start", "faro.user.action", "faro.tracing.fetch"} {
		if !eventNames[want] {
			t.Errorf("beacon missing event %q (got events: %v)", want, eventNames)
		}
	}
}

// TestProjectRUM_FetchEventCarriesTraceID: the faro.tracing.fetch event carries the
// ledger TraceID (request correlation join to backend spans).
func TestProjectRUM_FetchEventCarriesTraceID(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)
	batch := buildRUMBatch(w, 1)
	wantTraceID := batch[0].TraceID

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	p := rum.All()[0]
	for _, e := range p.Events {
		if e.Name == "faro.tracing.fetch" {
			if e.Trace == nil || e.Trace.TraceID != wantTraceID {
				t.Errorf("faro.tracing.fetch trace_id = %v, want %q", e.Trace, wantTraceID)
			}
			return
		}
	}
	t.Fatal("no faro.tracing.fetch event found")
}

// TestProjectRUM_ExceptionOnServerError: an error-outcome request generates an exception
// in the beacon; a success request does not.
func TestProjectRUM_ExceptionOnServerError(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)

	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	errReq := &ledger.Request{
		Correlation:   ledger.NewCorrelation(),
		Workload:      w.Name(),
		Route:         "GET /api/v1/data",
		BrowserOrigin: true,
		Start:         now,
		Duration:      200 * time.Millisecond,
	}
	errReq.Outcome, errReq.StatusCode = ledger.OutcomeServerError, 500

	okReq := &ledger.Request{
		Correlation:   ledger.NewCorrelation(),
		Workload:      w.Name(),
		Route:         "GET /api/v1/data",
		BrowserOrigin: true,
		Start:         now,
		Duration:      150 * time.Millisecond,
	}
	okReq.Outcome, okReq.StatusCode = ledger.OutcomeSuccess, 200

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{errReq, okReq}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	payloads := rum.All()
	if len(payloads) != 2 {
		t.Fatalf("want 2 payloads, got %d", len(payloads))
	}
	// First payload (error) must have an exception.
	if len(payloads[0].Exceptions) == 0 {
		t.Error("error-outcome beacon must carry an exception")
	}
	// Second payload (success) must have no exceptions.
	if len(payloads[1].Exceptions) != 0 {
		t.Errorf("success-outcome beacon must not carry exceptions, got %d", len(payloads[1].Exceptions))
	}
}

// TestProjectRUM_MeasurementsCarryDelta: every measurement carries a "delta" Values key
// equal to the primary metric value (per the Faro contract).
func TestProjectRUM_MeasurementsCarryDelta(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)
	batch := buildRUMBatch(w, 1)

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	p := rum.All()[0]
	for i, m := range p.Measurements {
		if _, ok := m.Values["delta"]; !ok {
			t.Errorf("measurement[%d] (type=%s) missing 'delta' key", i, m.Type)
		}
	}
}

// TestProjectRUM_MinterSetsBrowserOrigin: when the app has rumEnabled, minted requests
// from a frontend entry have BrowserOrigin=true (the minter must set it).
func TestProjectRUM_MinterSetsBrowserOrigin(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)

	eng := shape.New("", nil)
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	// Mint a generous batch so at least some requests are BrowserOrigin=true.
	batch := w.Minter().Mint(now, 60.0, eng)
	if len(batch) == 0 {
		t.Fatal("minter returned empty batch at 60s tick")
	}
	hadBrowser := false
	for _, r := range batch {
		if r.BrowserOrigin {
			hadBrowser = true
			break
		}
	}
	if !hadBrowser {
		t.Error("no request in minted batch has BrowserOrigin=true (minter must set it when rumEnabled)")
	}
}

// ─── M1: beacon SpanID joins the emitted span ─────────────────────────────────

// TestProjectRUM_FetchEventSpanID_EqualsRequestSpanID (M1): the faro.tracing.fetch event
// and all web-vital measurements must reference r.SpanID — the entry CLIENT root span
// emitted by projectTraces (SpanID: r.SpanID, parent=""). Using r.BrowserSpanID would
// dangle because that id is never emitted as an actual span.
func TestProjectRUM_FetchEventSpanID_EqualsRequestSpanID(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)
	batch := buildRUMBatch(w, 1)
	r := batch[0]
	wantSpanID := r.SpanID
	forbiddenSpanID := r.BrowserSpanID

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	p := rum.All()[0]

	// faro.tracing.fetch event must carry r.SpanID.
	fetchFound := false
	for _, e := range p.Events {
		if e.Name != "faro.tracing.fetch" {
			continue
		}
		fetchFound = true
		if e.Trace == nil {
			t.Fatal("faro.tracing.fetch: Trace is nil")
		}
		if e.Trace.SpanID != wantSpanID {
			t.Errorf("faro.tracing.fetch SpanID = %q, want r.SpanID %q (not BrowserSpanID %q)", e.Trace.SpanID, wantSpanID, forbiddenSpanID)
		}
		if e.Trace.SpanID == forbiddenSpanID {
			t.Errorf("faro.tracing.fetch SpanID = BrowserSpanID %q — this id is never emitted; join would dangle", forbiddenSpanID)
		}
	}
	if !fetchFound {
		t.Fatal("faro.tracing.fetch event not found in beacon")
	}

	// All web-vital measurements must carry r.SpanID.
	for i, m := range p.Measurements {
		if m.Trace == nil {
			t.Errorf("measurement[%d] (%q): Trace is nil", i, m.Type)
			continue
		}
		if m.Trace.SpanID != wantSpanID {
			t.Errorf("measurement[%d] (%q) SpanID = %q, want r.SpanID %q", i, m.Type, m.Trace.SpanID, wantSpanID)
		}
	}
}

// TestProjectRUM_ExceptionSpanID_EqualsRequestSpanID (M1): exception trace must also
// reference r.SpanID.
func TestProjectRUM_ExceptionSpanID_EqualsRequestSpanID(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)

	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	errReq := &ledger.Request{
		Correlation:   ledger.NewCorrelation(),
		Workload:      w.Name(),
		Route:         "POST /api/v1/assist",
		BrowserOrigin: true,
		Start:         now,
		Duration:      200 * time.Millisecond,
	}
	errReq.Outcome, errReq.StatusCode = ledger.OutcomeServerError, 500
	wantSpanID := errReq.SpanID

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{errReq}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	p := rum.All()[0]
	if len(p.Exceptions) == 0 {
		t.Fatal("expected exception on 5xx outcome")
	}
	ex := p.Exceptions[0]
	if ex.Trace == nil {
		t.Fatal("exception Trace is nil")
	}
	if ex.Trace.SpanID != wantSpanID {
		t.Errorf("exception SpanID = %q, want r.SpanID %q", ex.Trace.SpanID, wantSpanID)
	}
}

// ─── M2: web vitals vary across requests ──────────────────────────────────────

// TestProjectRUM_WebVitalsVaryAcrossRequests (M2): emitting N beacons must produce
// non-identical LCP values — vitals are drawn from a distribution, not constants.
func TestProjectRUM_WebVitalsVaryAcrossRequests(t *testing.T) {
	const n = 20
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)
	batch := buildRUMBatch(w, n)

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	payloads := rum.All()
	if len(payloads) != n {
		t.Fatalf("want %d payloads, got %d", n, len(payloads))
	}

	// Collect LCP values from every beacon.
	var lcpVals []float64
	for _, p := range payloads {
		for _, m := range p.Measurements {
			if v, ok := m.Values["lcp"]; ok {
				lcpVals = append(lcpVals, v)
				break
			}
		}
	}
	if len(lcpVals) != n {
		t.Fatalf("got %d LCP values, want %d", len(lcpVals), n)
	}

	// At least 2 distinct LCP values across N=20 requests (extremely unlikely to all be equal
	// with a continuous distribution — this would require 19 identical random draws).
	first := lcpVals[0]
	allSame := true
	for _, v := range lcpVals[1:] {
		if v != first {
			allSame = false
			break
		}
	}
	if allSame {
		t.Errorf("all %d LCP values are identical (%v) — vitals must vary (M2 regression: frozen-metrics anti-pattern)", n, first)
	}
}

// TestProjectRUM_WebVitalRatingsCorrelate (M2): drawn value and rating must agree
// (rating must reflect the actual drawn value, not a stale constant).
func TestProjectRUM_WebVitalRatingsCorrelate(t *testing.T) {
	const n = 30
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)
	batch := buildRUMBatch(w, n)

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	// Map of primary key → (goodMax, niMax) thresholds.
	thresholds := map[string][2]float64{
		"lcp":  {2500, 4000},
		"cls":  {0.1, 0.25},
		"inp":  {200, 500},
		"ttfb": {800, 1800},
		"fcp":  {1800, 3000},
	}

	for _, p := range rum.All() {
		for _, m := range p.Measurements {
			for key, thresh := range thresholds {
				v, ok := m.Values[key]
				if !ok {
					continue
				}
				want := appVitalRating(v, thresh[0], thresh[1])
				got := m.Context["rating"]
				if got != want {
					t.Errorf("beacon %s: value=%.4f → expected rating %q, got %q (value↔rating mismatch)", key, v, want, got)
				}
			}
		}
	}
}

// ─── M3: browser span name = method only ──────────────────────────────────────

// TestReqRefs_MethodKey (M3): reqRefs must expose "method" (HTTP verb only) and
// "original_span_name" ("HTTP "+method) for the rum_faro profile NameTemplate.
func TestReqRefs_MethodKey(t *testing.T) {
	r := &ledger.Request{
		Correlation: ledger.NewCorrelation(),
		Route:       "POST /v1/assist",
	}
	r.Outcome, r.StatusCode = ledger.OutcomeSuccess, 200

	refs := reqRefs(r, nil)

	if got := refs["method"]; got != "POST" {
		t.Errorf("refs[method] = %q, want %q", got, "POST")
	}
	if got := refs["original_span_name"]; got != "HTTP POST" {
		t.Errorf("refs[original_span_name] = %q, want %q", got, "HTTP POST")
	}
}

// TestReqRefs_MethodKey_GET (M3): GET route parses correctly too.
func TestReqRefs_MethodKey_GET(t *testing.T) {
	r := &ledger.Request{
		Correlation: ledger.NewCorrelation(),
		Route:       "GET /api/v1/data",
	}
	r.Outcome, r.StatusCode = ledger.OutcomeSuccess, 200

	refs := reqRefs(r, nil)

	if got := refs["method"]; got != "GET" {
		t.Errorf("refs[method] = %q, want %q", got, "GET")
	}
	if got := refs["original_span_name"]; got != "HTTP GET" {
		t.Errorf("refs[original_span_name] = %q, want %q", got, "HTTP GET")
	}
}

// ─── Multi-page browser SESSION ───────────────────────────────────────────────

// buildRUMAppWithPages builds a RUM app whose frontend entry carries a Pages navigation
// inventory (so projectRUM emits an ordered multi-page session before the assist beacon).
func buildRUMAppWithPages(t *testing.T, rum *coretest.RUMCapture, pages []PageDecl) *Workload {
	t.Helper()
	if _, ok := profiles.Lookup("rum_faro"); !ok {
		t.Fatal("rum_faro profile not registered — missing import?")
	}
	cfg := &Config{
		Traffic: Traffic{OffPeakRPS: 10, PeakRPS: 50},
		Services: []ServiceNode{
			{
				Name:     "fe",
				Type:     "frontend",
				Entry:    true,
				Profiles: []string{"rum_faro"},
				Calls:    []string{"api"},
				Pages:    pages,
			},
			{Name: "api", Type: "web"},
		},
	}
	w, err := build(cfg, core.Binding{
		Name:    "rum-test",
		Env:     coretest.Env(),
		Cluster: coretest.Cluster(),
		RUM:     rum,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return w.(*Workload)
}

// samplePages is a small navigation inventory with slash-free actions (and one page with a
// slashy path + empty actions to exercise the fallback).
func samplePages() []PageDecl {
	return []PageDecl{
		{Path: "/document-library", Name: "Document Library", Actions: []string{"search documents", "open document"}},
		{Path: "/editor", Name: "Editor", Actions: []string{"edit content"}},
		{Path: "/reports/weekly", Name: "Weekly Reports"}, // no actions → fallback
		{Path: "/settings", Name: "Settings", Actions: []string{"update profile"}},
	}
}

// eventNamesOf collects the set of event names in a payload.
func eventNamesOf(p faro.Payload) map[string]bool {
	out := map[string]bool{}
	for _, e := range p.Events {
		out[e.Name] = true
	}
	return out
}

// TestProjectRUM_EmptyPages_SingleBeaconUnchanged: with NO Pages, projectRUM emits exactly ONE
// payload per browser-origin request, carrying session_start + faro.user.action + faro.tracing.fetch
// (today's shape — the critical back-compat invariant).
func TestProjectRUM_EmptyPages_SingleBeaconUnchanged(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum) // no Pages
	batch := buildRUMBatch(w, 1)

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	payloads := rum.All()
	if len(payloads) != 1 {
		t.Fatalf("empty-Pages: got %d payloads, want 1 (back-compat: single assist beacon)", len(payloads))
	}
	names := eventNamesOf(payloads[0])
	for _, want := range []string{"session_start", "faro.user.action", "faro.tracing.fetch"} {
		if !names[want] {
			t.Errorf("assist beacon missing event %q (got %v)", want, names)
		}
	}
}

// TestProjectRUM_Session_MultiPage: with a Pages list, a browser-origin request emits >1 payloads;
// the FIRST carries session_start; subsequent NAV beacons carry view_changed (fromView/toView); the
// LAST beacon is the assist beacon (faro.tracing.fetch with the request's TraceID); exactly ONE
// session_start across the whole session.
func TestProjectRUM_Session_MultiPage(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMAppWithPages(t, rum, samplePages())
	batch := buildRUMBatch(w, 1)
	wantTraceID := batch[0].TraceID

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	payloads := rum.All()
	if len(payloads) < 2 {
		t.Fatalf("with Pages: got %d payloads, want >1 (nav session + assist)", len(payloads))
	}

	// Exactly ONE session_start across the entire session, and it is on the FIRST beacon.
	sessionStarts := 0
	for i, p := range payloads {
		for _, e := range p.Events {
			if e.Name == "session_start" {
				sessionStarts++
				if i != 0 {
					t.Errorf("session_start found on beacon[%d], want only beacon[0]", i)
				}
			}
		}
	}
	if sessionStarts != 1 {
		t.Errorf("session has %d session_start events, want exactly 1", sessionStarts)
	}

	// Nav beacons after the first carry view_changed with fromView/toView.
	navCount := len(payloads) - 1 // last is the assist beacon
	for i := 1; i < navCount; i++ {
		names := eventNamesOf(payloads[i])
		if !names["view_changed"] {
			t.Errorf("nav beacon[%d] missing view_changed event (got %v)", i, names)
		}
		for _, e := range payloads[i].Events {
			if e.Name == "view_changed" {
				if e.Attributes["fromView"] == "" || e.Attributes["toView"] == "" {
					t.Errorf("nav beacon[%d] view_changed missing fromView/toView: %v", i, e.Attributes)
				}
			}
		}
	}

	// The LAST beacon is the assist beacon: faro.tracing.fetch with the request TraceID.
	last := payloads[len(payloads)-1]
	foundFetch := false
	for _, e := range last.Events {
		if e.Name == "faro.tracing.fetch" {
			foundFetch = true
			if e.Trace == nil || e.Trace.TraceID != wantTraceID {
				t.Errorf("assist beacon fetch trace = %v, want TraceID %q", e.Trace, wantTraceID)
			}
		}
	}
	if !foundFetch {
		t.Error("last (assist) beacon has no faro.tracing.fetch event")
	}
}

// TestProjectRUM_Session_NavActionsSlashFree: every faro.user.action name across all beacons is
// slash-free (a "/" breaks FEO drill-in). Uses a Pages inventory incl. a slashy-path page with no
// declared actions (fallback path must also be slash-free).
func TestProjectRUM_Session_NavActionsSlashFree(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMAppWithPages(t, rum, samplePages())
	batch := buildRUMBatch(w, 5)

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	for i, p := range rum.All() {
		for _, e := range p.Events {
			if e.Name != "faro.user.action" || e.Action == nil {
				continue
			}
			if strings.Contains(e.Action.Name, "/") {
				t.Errorf("beacon[%d] faro.user.action name %q contains a slash — breaks FEO drill-in", i, e.Action.Name)
			}
		}
	}
}

// TestProjectRUM_Session_SharedSessionID: all beacons in a request's session carry
// Meta.Session.ID == r.SessionID.
func TestProjectRUM_Session_SharedSessionID(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMAppWithPages(t, rum, samplePages())
	batch := buildRUMBatch(w, 1)
	wantSession := batch[0].SessionID

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	payloads := rum.All()
	if len(payloads) < 2 {
		t.Fatalf("want a multi-beacon session, got %d", len(payloads))
	}
	for i, p := range payloads {
		if p.Meta.Session.ID != wantSession {
			t.Errorf("beacon[%d] Meta.Session.ID = %q, want %q (shared session)", i, p.Meta.Session.ID, wantSession)
		}
	}
}

// TestProjectRUM_Session_NavBeaconsHaveNoTrace: nav beacons carry NO faro.tracing.fetch and NO
// exception; only the assist (last) beacon carries trace context.
func TestProjectRUM_Session_NavBeaconsHaveNoTrace(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMAppWithPages(t, rum, samplePages())
	batch := buildRUMBatch(w, 1)

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	payloads := rum.All()
	if len(payloads) < 2 {
		t.Fatalf("want a multi-beacon session, got %d", len(payloads))
	}
	navBeacons := payloads[:len(payloads)-1]
	for i, p := range navBeacons {
		for _, e := range p.Events {
			if e.Name == "faro.tracing.fetch" {
				t.Errorf("nav beacon[%d] has a faro.tracing.fetch event — nav is pure browser, no backend", i)
			}
		}
		if len(p.Exceptions) != 0 {
			t.Errorf("nav beacon[%d] has %d exceptions — nav must carry none", i, len(p.Exceptions))
		}
	}
}

// TestProjectRUM_Session_Deterministic: the same request yields the same session SHAPE (the page
// sequence) across two independent runs — the structural draws are deterministic per request.
func TestProjectRUM_Session_Deterministic(t *testing.T) {
	pages := samplePages()
	eng := shape.New("", nil)
	r := &ledger.Request{
		Correlation: ledger.NewCorrelation(),
		Route:       "GET /api/v1/data",
		Start:       time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC),
		Duration:    200 * time.Millisecond,
	}

	seq := func() []string {
		steps := appNavSession(r, pages, eng)
		paths := make([]string, len(steps))
		for i, s := range steps {
			paths[i] = s.page.Path
		}
		return paths
	}

	first := seq()
	second := seq()
	if len(first) == 0 {
		t.Fatal("appNavSession returned an empty session")
	}
	if len(first) != len(second) {
		t.Fatalf("session length not deterministic: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("session page[%d] not deterministic: %q vs %q", i, first[i], second[i])
		}
	}
}

// ─── m4: Meta.Page is set ─────────────────────────────────────────────────────

// TestProjectRUM_BeaconMetaPageSet (m4): every beacon must carry Meta.Page with non-empty
// ID and URL (signals/logs.md [slug: logs-faro-rum]: page_id / page_url body fields).
func TestProjectRUM_BeaconMetaPageSet(t *testing.T) {
	rum := &coretest.RUMCapture{}
	w := buildRUMApp(t, rum)
	batch := buildRUMBatch(w, 3)

	world := coretest.World(nil, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	for i, p := range rum.All() {
		if p.Meta.Page.ID == "" {
			t.Errorf("beacon[%d]: Meta.Page.ID is empty (page_id missing)", i)
		}
		if p.Meta.Page.URL == "" {
			t.Errorf("beacon[%d]: Meta.Page.URL is empty (page_url missing)", i)
		}
	}
}
