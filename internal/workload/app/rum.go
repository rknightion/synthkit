// SPDX-License-Identifier: AGPL-3.0-only

package app

// projectRUM emits Faro/RUM beacons for browser-origin requests in the batch.
//
// Mirrors webservice/rum.go beaconFor with the following additions per a live reference
// capture (signals/logs.md [slug: logs-faro-rum], 2026-06-16):
//   - ALL 5 web-vital measurements (LCP/CLS/INP/TTFB/FCP) with sub-fields + context.
//   - Events: session_start, faro.user.action (parent), faro.tracing.fetch (child).
//   - Exception on any error outcome (OutcomeServerError or OutcomeClientError).
//
// Each request produces EXACTLY one faro.Payload; non-browser requests are skipped.
// ONLY ledger IDs flow here (I9) — no IDs are minted in this file.

import (
	"context"
	"math"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/faro"
)

// faroSDKName / faroSDKVersion match webservice/webservice.go constants (Faro Web SDK).
const (
	appFaroSDKName    = "faro-web"
	appFaroSDKVersion = "2.7.0"
)

// projectRUM emits one Faro beacon per browser-origin request. It is called from
// ProjectBatch only when w.b.RUM != nil (rumEnabled — the caller already guards this).
func (w *Workload) projectRUM(ctx context.Context, world *core.World, batch []*ledger.Request) error {
	// The entry node's declared navigation inventory (frontend-only; empty ⇒ single-page behavior).
	var pages []PageDecl
	if w.graph.entry != nil {
		pages = w.graph.entry.decl.Pages
	}

	payloads := make([]faro.Payload, 0, len(batch))
	for _, r := range batch {
		if !r.BrowserOrigin {
			continue
		}
		// Draw the navigation session (RUM-only page-views before the traced assist). Empty when
		// no pages are declared ⇒ behavior is identical to today (assist beacon only).
		session := appNavSession(r, pages, world.Shape)
		if len(session) == 0 {
			payloads = append(payloads, w.appBeaconFor(r, world.Shape, true))
			continue
		}
		// Emit nav page-views FIRST (chronological, spread BEFORE the assist), then the assist LAST.
		base := r.RenderStart()
		k := len(session) // assist sits at step k
		prevView := ""
		for i, step := range session {
			ts := base.Add(-time.Duration(k-i) * navStepDuration)
			payloads = append(payloads, w.appNavBeacon(r, step, prevView, i == 0, ts, world.Shape))
			prevView = step.page.Name
		}
		// Assist beacon LAST — session_start already emitted by the first nav beacon, so drop it here.
		payloads = append(payloads, w.appBeaconFor(r, world.Shape, false))
	}
	if len(payloads) == 0 {
		return nil
	}
	return w.b.RUM.Write(ctx, payloads)
}

// appBeaconFor builds one browser-origin beacon for the (traced) assist request.
// Template: webservice/rum.go beaconFor. withSessionStart governs whether the ambient
// session_start event is emitted: true for a standalone single-page beacon (today's behavior),
// false when a preceding nav session already opened the session (avoids a duplicate session_start).
func (w *Workload) appBeaconFor(r *ledger.Request, eng *shape.Engine, withSessionStart bool) faro.Payload {
	ts := r.RenderStart()
	actionID := r.RequestID
	act := appActionName(r.Route)

	meta := w.appFaroMeta(r)

	events := make([]faro.Event, 0, 3)
	if withSessionStart {
		// Ambient session marker (drives FEO Sessions view).
		events = append(events, faro.Event{Name: "session_start", Domain: "session", Timestamp: ts})
	}
	events = append(events,
		// User-action PARENT (id + name, NO parentId). action_id == request_id.
		faro.Event{
			Name:      "faro.user.action",
			Domain:    "browser",
			Timestamp: ts,
			Action:    &faro.Action{ID: actionID, Name: act},
			Attributes: map[string]string{
				"userActionEventType":  appActionEventType(r.Route),
				"userActionImportance": "normal",
			},
		},
		// CHILD faro.tracing.fetch: carries the ledger trace_id/span_id.
		// SpanID = r.SpanID (the entry CLIENT root span emitted by projectTraces with
		// SpanID: r.SpanID, parent="") — this is the browser fetch span in the app model.
		// Using r.BrowserSpanID would dangle: that id is never emitted as an actual span.
		faro.Event{
			Name:      "faro.tracing.fetch",
			Domain:    "browser",
			Timestamp: ts,
			Trace:     &faro.TraceContext{TraceID: r.TraceID, SpanID: r.SpanID},
			Action:    &faro.Action{Name: act, ParentID: actionID},
			Attributes: map[string]string{
				"component":        "fetch",
				"http.method":      appRouteMethod(r.Route),
				"http.status_code": appItoa(r.StatusCode),
				"http.scheme":      "https",
				"session.id":       r.SessionID,
				"url.template":     appRoutePath(r.Route),
			},
		},
	)

	// 5 web-vital measurements per a live reference capture (signals/logs.md [slug: logs-faro-rum]).
	// Each carries: primary key + sub-fields + delta (= primary value) + context rating +
	// navigation_type. The collector derives value_* twins from the posted plain key — synth
	// posts only the plain key (contract: do NOT post value_*).
	measurements := w.appWebVitals(r, ts, act, actionID, eng)

	p := faro.Payload{Events: events, Measurements: measurements, Meta: meta}

	// Exception on any error outcome (5xx or 4xx — tied to the action via parentId).
	// signals/logs.md [slug: logs-faro-rum]: kind=exception; type/value/stacktrace.
	// SpanID = r.SpanID (the entry CLIENT root span — same as faro.tracing.fetch above).
	if r.Outcome == ledger.OutcomeServerError || r.Outcome == ledger.OutcomeClientError {
		exType, exValue := appExceptionFor(r.Outcome)
		p.Exceptions = []faro.Exception{{
			Type:      exType,
			Value:     exValue,
			Timestamp: ts,
			Trace:     &faro.TraceContext{TraceID: r.TraceID, SpanID: r.SpanID},
			Action:    &faro.Action{Name: act, ParentID: actionID},
			Stacktrace: &faro.Stacktrace{Frames: []faro.Frame{
				{Function: "submitRequest", Filename: "https://app/static/app.js", Lineno: 1, Colno: 42},
			}},
		}}
	}

	return p
}

// appWebVitals builds all 5 web-vital measurements per a live reference capture.
// Sub-fields and context keys are from signals/logs.md [slug: logs-faro-rum]:
//   - LCP: element_render_delay, resource_load_delay, resource_load_duration, time_to_first_byte; ctx element, rating, navigation_type
//   - CLS: largest_shift_time/value (omitted when cls=0); ctx rating, navigation_type
//   - INP: input_delay, interaction_time, next_paint_time; ctx interaction_target, interaction_type, load_state, rating, navigation_type
//   - TTFB: request_duration, waiting_duration; ctx rating, navigation_type
//   - FCP: first_byte_to_fcp, time_to_first_byte; ctx load_state, rating, navigation_type
//
// Each vital is drawn per-request from a plausible distribution via eng (world.Shape)
// so dashboards show real variance rather than flat lines.
func (w *Workload) appWebVitals(r *ledger.Request, ts time.Time, act, actionID string, eng *shape.Engine) []faro.Measurement {
	// M1: SpanID = r.SpanID (the entry CLIENT root span emitted by projectTraces).
	tc := &faro.TraceContext{TraceID: r.TraceID, SpanID: r.SpanID}
	a := &faro.Action{Name: act, ParentID: actionID}
	navType := "navigate"

	// clamp clamps v into [lo, hi].
	clamp := func(v, lo, hi float64) float64 {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}

	// web_vitals_degraded failure mode: per-vital FailFactor calls scoped to the entry
	// node name (e.g. "web_vitals_degraded@web-frontend"). The scope is the frontend
	// node name so per-node incident targeting is respected. magAt1 values are per spec:
	// LCP & TTFB 2.5, INP 2.0, FCP 2.0, CLS 1.8.
	entryName := ""
	if w.graph.entry != nil {
		entryName = w.graph.entry.decl.Name
	}
	lcpFactor := eng.FailFactor(ts, "web_vitals_degraded", entryName, 2.5)
	clsFactor := eng.FailFactor(ts, "web_vitals_degraded", entryName, 1.8)
	inpFactor := eng.FailFactor(ts, "web_vitals_degraded", entryName, 2.0)
	ttfbFactor := eng.FailFactor(ts, "web_vitals_degraded", entryName, 2.5)
	fcpFactor := eng.FailFactor(ts, "web_vitals_degraded", entryName, 2.0)

	// LCP: Largest Contentful Paint — lognormal draw, median ~1200ms, range [400, 4000].
	// Thresholds: good ≤ 2500ms, needs-improvement ≤ 4000ms.
	// Upper clamp is scaled by lcpFactor so degraded vitals can exceed the healthy ceiling.
	lcp := clamp(math.Exp(7.09+0.55*eng.NormFloat64())*lcpFactor, 400, 4000*lcpFactor) // ln(1200)≈7.09
	lcpRating := appVitalRating(lcp, 2500, 4000)
	// Sub-fields are proportional splits of lcp (element_render_delay dominates).
	lcpTTFB := clamp(lcp*0.17, 20, 500)
	lcpRLD := clamp(lcp*0.08, 5, 200)
	lcpRLDur := clamp(lcp*0.50, 50, 2500)
	lcpERD := lcp - lcpTTFB - lcpRLD - lcpRLDur
	if lcpERD < 0 {
		lcpERD = 0
	}

	// CLS: Cumulative Layout Shift — mostly small; draw in [0, 0.4], most mass < 0.1.
	// Thresholds: good ≤ 0.1, needs-improvement ≤ 0.25.
	// Upper clamp is scaled by clsFactor so degraded CLS can exceed the healthy ceiling.
	cls := clamp(math.Abs(eng.NormFloat64())*0.06*clsFactor, 0, 0.4*clsFactor)
	clsRating := appVitalRating(cls, 0.1, 0.25)

	// INP: Interaction to Next Paint — lognormal draw, median ~150ms, range [20, 500].
	// Thresholds: good ≤ 200ms, needs-improvement ≤ 500ms.
	// Upper clamp is scaled by inpFactor so degraded INP can exceed the healthy ceiling.
	inp := clamp(math.Exp(5.01+0.50*eng.NormFloat64())*inpFactor, 20, 500*inpFactor) // ln(150)≈5.01
	inpRating := appVitalRating(inp, 200, 500)
	// Sub-fields: input_delay + interaction_time + next_paint_time (proportional).
	inpInputDelay := clamp(inp*0.15, 1, 80)
	inpInteraction := clamp(inp*0.55, 5, 300)
	inpNextPaint := inp - inpInputDelay - inpInteraction
	if inpNextPaint < 0 {
		inpNextPaint = 0
	}

	// TTFB: Time To First Byte — lognormal draw, median ~350ms, range [80, 900].
	// Thresholds: good ≤ 800ms, needs-improvement ≤ 1800ms.
	// Upper clamp is scaled by ttfbFactor so degraded TTFB can exceed the healthy ceiling.
	ttfb := clamp(math.Exp(5.86+0.45*eng.NormFloat64())*ttfbFactor, 80, 900*ttfbFactor) // ln(350)≈5.86
	ttfbRating := appVitalRating(ttfb, 800, 1800)
	// Sub-fields: request_duration (network) + waiting_duration (server processing).
	ttfbReq := clamp(ttfb*0.45, 10, 400)
	ttfbWait := ttfb - ttfbReq
	if ttfbWait < 0 {
		ttfbWait = 0
	}

	// FCP: First Contentful Paint — lognormal draw, median ~900ms, range [400, 2500].
	// Thresholds: good ≤ 1800ms, needs-improvement ≤ 3000ms.
	// Upper clamp is scaled by fcpFactor so degraded FCP can exceed the healthy ceiling.
	fcp := clamp(math.Exp(6.80+0.45*eng.NormFloat64())*fcpFactor, 400, 2500*fcpFactor) // ln(900)≈6.80
	fcpRating := appVitalRating(fcp, 1800, 3000)
	// Sub-fields: time_to_first_byte + first_byte_to_fcp.
	fcpTTFB := clamp(fcp*0.22, 30, 400)
	fcpToFCP := fcp - fcpTTFB
	if fcpToFCP < 0 {
		fcpToFCP = 0
	}

	// CLS measurement: largest_shift_time/value are ONLY included when cls > 0
	// (live capture: sub-fields absent for perfectly-stable layouts with cls=0).
	clsValues := map[string]float64{"cls": cls, "delta": cls}
	if cls > 0 {
		clsValues["largest_shift_time"] = clamp(eng.Float64()*5000, 50, 5000) // ms into page load
		clsValues["largest_shift_value"] = cls                                // dominant shift == total for synthetic
	}

	return []faro.Measurement{
		{
			Type:      "web-vitals",
			Timestamp: ts,
			Values: map[string]float64{
				"lcp":                    lcp,
				"delta":                  lcp,
				"element_render_delay":   lcpERD,
				"resource_load_delay":    lcpRLD,
				"resource_load_duration": lcpRLDur,
				"time_to_first_byte":     lcpTTFB,
			},
			Context: map[string]string{
				"rating":          lcpRating,
				"navigation_type": navType,
			},
			Trace:  tc,
			Action: a,
		},
		{
			Type:      "web-vitals",
			Timestamp: ts,
			Values:    clsValues,
			Context: map[string]string{
				"rating":          clsRating,
				"navigation_type": navType,
			},
			Trace:  tc,
			Action: a,
		},
		{
			Type:      "web-vitals",
			Timestamp: ts,
			Values: map[string]float64{
				"inp":              inp,
				"delta":            inp,
				"input_delay":      inpInputDelay,
				"interaction_time": inpInteraction,
				"next_paint_time":  inpNextPaint,
			},
			Context: map[string]string{
				"rating":           inpRating,
				"navigation_type":  navType,
				"interaction_type": "pointer",
				"load_state":       "dom-content-loaded",
			},
			Trace:  tc,
			Action: a,
		},
		{
			Type:      "web-vitals",
			Timestamp: ts,
			Values: map[string]float64{
				"ttfb":             ttfb,
				"delta":            ttfb,
				"request_duration": ttfbReq,
				"waiting_duration": ttfbWait,
			},
			Context: map[string]string{
				"rating":          ttfbRating,
				"navigation_type": navType,
			},
			Trace:  tc,
			Action: a,
		},
		{
			Type:      "web-vitals",
			Timestamp: ts,
			Values: map[string]float64{
				"fcp":                fcp,
				"delta":              fcp,
				"first_byte_to_fcp":  fcpToFCP,
				"time_to_first_byte": fcpTTFB,
			},
			Context: map[string]string{
				"rating":          fcpRating,
				"navigation_type": navType,
				"load_state":      "dom-content-loaded",
			},
			Trace:  tc,
			Action: a,
		},
	}
}

// appFaroMeta identifies the app/session/env in the beacon meta. Session.ID is
// required by the Faro collector (X-Faro-Session-Id) — it is the ledger SessionID,
// never minted here (I9).
func (w *Workload) appFaroMeta(r *ledger.Request) faro.Meta {
	// Use the entry node's name as the browser service name (mirrors webservice's
	// browserServiceName which returns the workload name + "-browser").
	svcName := w.b.Name
	if w.graph.entry != nil {
		svcName = w.graph.entry.decl.Name
	}
	ns := svcName
	env := w.env
	// Page.ID = route path; Page.URL = a plausible browser URL for the page.
	// signals/logs.md [slug: logs-faro-rum]: page_id / page_url body fields.
	routePath := appRoutePath(r.Route)
	pageURL := "https://app.example.com" + routePath
	return faro.Meta{
		SDK: faro.SDK{Name: appFaroSDKName, Version: appFaroSDKVersion},
		App: faro.App{
			Name:        svcName,
			Namespace:   ns,
			Version:     serviceVersion,
			Environment: env,
		},
		Browser: faro.Browser{Name: "Chrome", Version: "126.0", OS: "Windows", Language: "en-US"},
		Page:    faro.Page{ID: routePath, URL: pageURL},
		Session: faro.Session{ID: r.SessionID},
		View:    faro.View{Name: routePath},
	}
}

// appVitalRating buckets a web-vital value into the FEO context_rating enum.
// Thresholds: good ≤ goodMax, needs-improvement ≤ niMax, poor otherwise.
func appVitalRating(v, goodMax, niMax float64) string {
	switch {
	case v <= goodMax:
		return "good"
	case v <= niMax:
		return "needs-improvement"
	default:
		return "poor"
	}
}

// appActionName returns a low-card user-action name from the route (mirrors webservice's actionName).
// appActionName builds the Faro user-action NAME — a low-card, SLASH-FREE intent label ("load data",
// "submit assist"). It must NOT embed the route PATH: a "/" in an action name breaks the FEO
// action/session drill-in URL (the slash is read as a path separator → "not found"). Real Faro action
// names are verbs/intents, not paths (the page path lives in page_id/view, where slashes are fine).
func appActionName(route string) string {
	verb := "submit"
	if appRouteMethod(route) == "GET" {
		verb = "load"
	}
	if res := appActionResource(route); res != "" {
		return verb + " " + res
	}
	return verb
}

// appActionResource returns the LAST non-empty path segment of the route (slash-free), e.g.
// "POST /v1/assist" → "assist", "GET /api/v1/data" → "data", "GET /" → "".
func appActionResource(route string) string {
	p := appRoutePath(route)
	end := len(p)
	for end > 0 && p[end-1] == '/' {
		end--
	}
	start := 0
	for i := 0; i < end; i++ {
		if p[i] == '/' {
			start = i + 1
		}
	}
	return p[start:end]
}

// appActionEventType maps the route method to the user-action event type.
func appActionEventType(route string) string {
	if appRouteMethod(route) == "GET" {
		return "click"
	}
	return "submit"
}

// appExceptionFor returns a (type, value) pair for the exception based on outcome.
func appExceptionFor(outcome ledger.Outcome) (string, string) {
	if outcome == ledger.OutcomeServerError {
		return "ConnectError", "network error: failed to fetch"
	}
	return "ClientError", "request failed: bad request"
}

// appRouteMethod extracts the HTTP method from a route string like "GET /api/v1/data".
func appRouteMethod(route string) string {
	for i := 0; i < len(route); i++ {
		if route[i] == ' ' {
			return route[:i]
		}
	}
	return route
}

// appRoutePath extracts the path from a route string like "GET /api/v1/data".
func appRoutePath(route string) string {
	for i := 0; i < len(route); i++ {
		if route[i] == ' ' {
			return route[i+1:]
		}
	}
	return "/"
}

// appItoa renders an int without fmt (mirrors webservice/rum.go itoa).
func appItoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
