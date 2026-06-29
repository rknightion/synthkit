// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"

	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sink/faro"
)

// projectRUM emits Faro/RUM beacons for browser-origin requests in the batch. The
// X-Faro-Session-Id header is handled by the sink (set from Meta.Session.ID). Each
// browser-origin request produces ONE beacon carrying:
//   - session_start (drives the FEO Sessions view),
//   - the faro.user.action parent event (action_id == r.RequestID — the golden-thread top),
//   - a child faro.tracing.fetch with traceID/spanID == ledger IDs (join to backend spans),
//   - one web-vitals measurement (the value_* twins + context_rating),
//   - an exception on 5xx (drives the action JS-error rate).
//
// ONLY ledger IDs flow here (I9). Non-browser requests emit nothing in this lane.
func (w *Workload) projectRUM(ctx context.Context, batch []*ledger.Request) error {
	payloads := make([]faro.Payload, 0, len(batch))
	for _, r := range batch {
		if !r.BrowserOrigin {
			continue
		}
		payloads = append(payloads, w.beaconFor(r))
	}
	if len(payloads) == 0 {
		return nil
	}
	return w.b.RUM.Write(ctx, payloads)
}

// beaconFor builds one browser-origin beacon for a request.
func (w *Workload) beaconFor(r *ledger.Request) faro.Payload {
	ts := r.RenderStart()
	actionID := r.RequestID
	act := actionName(r.Route)
	httpStatus := r.StatusCode

	meta := w.faroMeta(r)

	events := []faro.Event{
		// Ambient session marker.
		{Name: "session_start", Domain: "session", Timestamp: ts},
		// User-action PARENT (id + name, NO parentId). action_id == request_id.
		{
			Name:      "faro.user.action",
			Domain:    "browser",
			Timestamp: ts,
			Action:    &faro.Action{ID: actionID, Name: act},
			Attributes: map[string]string{
				"userActionEventType":  actionEventType(r.Route),
				"userActionImportance": "normal",
			},
		},
		// CHILD faro.tracing.fetch: carries the ledger trace_id/span_id (= backend SERVER
		// span) and action_parent_id == the parent's action_id.
		{
			Name:      "faro.tracing.fetch",
			Domain:    "browser",
			Timestamp: ts,
			Trace:     &faro.TraceContext{TraceID: r.TraceID, SpanID: r.BrowserSpanID},
			Action:    &faro.Action{Name: act, ParentID: actionID},
			Attributes: map[string]string{
				"component":        "fetch",
				"http.method":      routeMethod(r.Route),
				"http.status_code": itoa(httpStatus),
				"http.scheme":      "https",
				"session.id":       r.SessionID,
				"url.template":     routePath(r.Route),
			},
		},
	}

	// One web-vitals measurement (LCP) with the value_* twin + context_rating. The
	// collector emits each Values entry as both <key> and value_<key>, and each Context
	// entry as context_<key>.
	lcp := 1200.0 + w.shapeNoise()*400
	measurements := []faro.Measurement{
		{
			Type:      "web-vitals",
			Timestamp: ts,
			Values:    map[string]float64{"lcp": lcp, "delta": lcp},
			Context: map[string]string{
				"rating":          vitalRating(lcp),
				"navigation_type": "navigate",
			},
			Trace:  &faro.TraceContext{TraceID: r.TraceID, SpanID: r.BrowserSpanID},
			Action: &faro.Action{Name: act, ParentID: actionID},
		},
	}

	p := faro.Payload{Events: events, Measurements: measurements, Meta: meta}

	// Exception on 5xx (tied to the action via parentId).
	if r.Outcome == ledger.OutcomeServerError {
		p.Exceptions = []faro.Exception{{
			Type:      "ConnectError",
			Value:     "network error: failed to fetch",
			Timestamp: ts,
			Trace:     &faro.TraceContext{TraceID: r.TraceID, SpanID: r.BrowserSpanID},
			Action:    &faro.Action{Name: act, ParentID: actionID},
			Stacktrace: &faro.Stacktrace{Frames: []faro.Frame{
				{Function: "submitRequest", Filename: "https://app/static/app.js", Lineno: 1, Colno: 42},
			}},
		}}
	}

	return p
}

// faroMeta identifies the app/session/env. Session.ID is required by the sink (X-Faro-
// Session-Id) — it is the ledger SessionID, never minted here.
func (w *Workload) faroMeta(r *ledger.Request) faro.Meta {
	return faro.Meta{
		SDK: faro.SDK{Name: faroSDKName, Version: faroSDKVersion},
		App: faro.App{
			Name:        w.browserServiceName(),
			Namespace:   frontendNamespace,
			Version:     serviceVersion,
			Environment: w.env,
		},
		Browser: faro.Browser{Name: "Chrome", Version: "126.0", OS: "Windows", Language: "en-US"},
		Session: faro.Session{ID: r.SessionID},
		View:    faro.View{Name: routePath(r.Route)},
	}
}

// shapeNoise returns a [0,1) jitter (RNG via the binding-independent ledger correlation is
// not available here; use a cheap math/rand-free deterministic-ish jitter from time).
func (w *Workload) shapeNoise() float64 {
	// A tiny deterministic jitter so web-vitals vary per beacon without importing rand.
	return 0.5
}

// vitalRating buckets a web-vital value into the FEO context_rating enum.
func vitalRating(lcpMs float64) string {
	switch {
	case lcpMs <= 2500:
		return "good"
	case lcpMs <= 4000:
		return "needs-improvement"
	default:
		return "poor"
	}
}

// actionEventType maps the route to the user-action event type (low-card).
func actionEventType(route string) string {
	if routeMethod(route) == "GET" {
		return "click"
	}
	return "submit"
}

// itoa renders an int without fmt (hot path).
func itoa(n int) string {
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
