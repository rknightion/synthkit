// SPDX-License-Identifier: AGPL-3.0-only

package app

// rum_session.go enriches a browser-origin request into an ordered multi-page Faro SESSION:
// the user navigates several declared pages (RUM-only page-views — NO backend trace, NO gen_ai)
// before the traced assist request. All beacons in the session share r.SessionID.
//
// The session SHAPE (length + page/action sequence) is DETERMINISTIC per request: it is derived
// from the request's TraceID (a stable per-request value), not from the global shape RNG, so the
// same request always renders the same navigation path (a session is one fixed user journey).
// Page/action/session-length are body FIELDS, never labels → the -dump inventory stays run-stable.
// IDs only via ledger.NewSpanID() (I9); action names are SLASH-FREE (a "/" breaks FEO drill-in).

import (
	"time"

	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/faro"
)

// navStepDuration is the spacing between consecutive page-views in a session window (a user
// spends a beat on each page before navigating). Nav steps are spread strictly BEFORE the assist.
const navStepDuration = 1500 * time.Millisecond

// maxNavSteps is the upper bound on navigation page-views in one session (1..maxNavSteps).
const maxNavSteps = 4

// navStep is one chosen page-view in a session: the page plus the user-action performed on it.
type navStep struct {
	page   PageDecl
	action string
}

// appNavSession draws a deterministic 1..maxNavSteps page-view sequence for the request from the
// declared Pages. eng is accepted for API conformance with the other draw helpers, but the
// structural choice is keyed off the request (r.TraceID) so the session is reproducible per request
// (test: same request → identical page sequence). Returns nil when there are no pages.
func appNavSession(r *ledger.Request, pages []PageDecl, eng *shape.Engine) []navStep {
	_ = eng
	if len(pages) == 0 {
		return nil
	}
	// Deterministic per-request stream from the TraceID hex.
	d := newReqDraw(r.TraceID)
	k := 1 + d.intn(maxNavSteps) // 1..maxNavSteps
	steps := make([]navStep, 0, k)
	for range k {
		page := pages[d.intn(len(pages))]
		steps = append(steps, navStep{page: page, action: appNavAction(page, d)})
	}
	return steps
}

// appNavAction picks a slash-free user-action for the page. Declared actions are slash-free by
// the blueprint contract; when none are declared, synthesize "view <lastPathSegment>" (slash-free).
func appNavAction(page PageDecl, d *reqDraw) string {
	if len(page.Actions) > 0 {
		return page.Actions[d.intn(len(page.Actions))]
	}
	seg := lastPathSegment(page.Path)
	if seg == "" {
		return "view page"
	}
	return "view " + seg
}

// lastPathSegment returns the final non-empty path segment (slash-free), e.g. "/reports/weekly"
// → "weekly", "/" → "". Mirrors appActionResource's segment logic for a raw path.
func lastPathSegment(p string) string {
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

// appNavBeacon builds one nav (page-view) beacon. first marks the session's first beacon (it carries
// session_start instead of view_changed). prevView is the previous page's view name (for view_changed).
// ts is the page-view's timestamp (earlier than the assist). Nav beacons carry NO trace and NO
// exception — they are pure browser navigation, no backend call.
func (w *Workload) appNavBeacon(r *ledger.Request, step navStep, prevView string, first bool, ts time.Time, eng *shape.Engine) faro.Payload {
	actionID := ledger.NewSpanID()
	act := step.action

	events := make([]faro.Event, 0, 2)
	if first {
		events = append(events, faro.Event{Name: "session_start", Domain: "session", Timestamp: ts})
	} else {
		events = append(events, faro.Event{
			Name:       "view_changed",
			Domain:     "browser",
			Timestamp:  ts,
			Attributes: map[string]string{"fromView": prevView, "toView": step.page.Name},
		})
	}
	// User-action PARENT (no trace context — nav is not backed by a backend fetch).
	events = append(events, faro.Event{
		Name:      "faro.user.action",
		Domain:    "browser",
		Timestamp: ts,
		Action:    &faro.Action{ID: actionID, Name: act},
		Attributes: map[string]string{
			"userActionEventType":  "click",
			"userActionImportance": "normal",
		},
	})

	// Per-page web-vitals (reuse the assist vitals builder; nav beacons carry no Trace context, so
	// strip the trace/action join the assist beacon uses — keep the action join only).
	measurements := w.appWebVitals(r, ts, act, actionID, eng)
	for i := range measurements {
		measurements[i].Trace = nil
	}

	return faro.Payload{
		Events:       events,
		Measurements: measurements,
		Meta:         w.appNavMeta(r, step.page),
	}
}

// appNavMeta is appFaroMeta scoped to a navigation page (Page/View reflect the nav page, not the
// request route). It reuses the shared app/browser/SDK identity so the assist beacon's meta stays
// identical to today.
func (w *Workload) appNavMeta(r *ledger.Request, page PageDecl) faro.Meta {
	m := w.appFaroMeta(r)
	m.Page = faro.Page{ID: page.Path, URL: "https://app.example.com" + page.Path}
	m.View = faro.View{Name: page.Name}
	return m
}

// ── deterministic per-request draw ────────────────────────────────────────────

// reqDraw is a tiny deterministic PRNG seeded from a stable per-request value (the TraceID). It is
// NOT cryptographic and NOT math/rand — it is an explicit splitmix64-style stream so the session
// SHAPE is reproducible for a given request without touching the global shape RNG. Structural-only:
// it never affects the -dump label inventory (page/action/length are body fields).
type reqDraw struct{ s uint64 }

// newReqDraw seeds the stream from the TraceID hex (FNV-1a over the bytes for a 64-bit seed).
func newReqDraw(traceID string) *reqDraw {
	const offset = 1469598103934665603
	const prime = 1099511628211
	h := uint64(offset)
	for i := 0; i < len(traceID); i++ {
		h ^= uint64(traceID[i])
		h *= prime
	}
	if h == 0 {
		h = 1
	}
	return &reqDraw{s: h}
}

// next advances the splitmix64 stream.
func (d *reqDraw) next() uint64 {
	d.s += 0x9E3779B97F4A7C15
	z := d.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// intn returns a deterministic value in [0,n); 0 when n<=0.
func (d *reqDraw) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(d.next() % uint64(n))
}
