// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"strconv"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// The causal projection (internal/causal, design §5): requests minted by a causal workload carry
// hop facts (status, attempt, offset, fault) that every signal family reads. A request that is
// not causal projects exactly as before; a healthy causal request carries legacy-shaped facts and
// so also projects as before, apart from the M10 root status rules, which only change failures.

// Span attribute names stamped on failing causal spans. Sourced in signals/traces.md
// (OpenTelemetry semantic-conventions v1.44.0, HTTP spans and the error registry).
const (
	attrHTTPResponseStatusCode = "http.response.status_code"
	attrHTTPRequestResendCount = "http.request.resend_count"
	attrErrorType              = "error.type"
	errorTypeOther             = "_OTHER"
)

// causalLayout reports whether a request's hop facts follow the causal timeline (a touched causal
// request: something failed, waited, slowed, timed out or retried). Such hops carry an Offset
// from RenderStart, attempts and statuses; every other request keeps the legacy nesting.
func causalLayout(r *ledger.Request) bool {
	if !r.Causal {
		return false
	}
	if r.Fault != nil {
		return true
	}
	for _, c := range r.Calls {
		if c.Offset != 0 || c.Attempt != 0 || c.StatusCode != 0 || c.TimedOut || c.Fault != nil {
			return true
		}
	}
	return false
}

// causalFailing reports a causal request carrying a Fault or a failure (design M6): only these
// get per-node log facts and drive span-metric latency cycling.
func causalFailing(r *ledger.Request) bool {
	if !r.Causal {
		return false
	}
	if r.Fault != nil || r.Outcome != ledger.OutcomeSuccess {
		return true
	}
	for _, c := range r.Calls {
		if c.Failed {
			return true
		}
	}
	return false
}

// entryRootKind is the entry's root span kind: CONSUMER for a worker entry (a message consumer of
// traffic.from_resource), else the service type's root kind.
func (w *Workload) entryRootKind() otlp.SpanKind {
	if w.graph.entry.decl.Worker {
		return otlp.KindConsumer
	}
	return w.graph.entry.kind.rootKind
}

func (w *Workload) successStatus() int {
	if s := w.cfg.Traffic.SuccessStatus; s != 0 {
		return s
	}
	return 200
}

// rootStatus is the root span status. A legacy request keeps spanStatus. A causal request follows
// M10: a SERVER root is ERROR only for a 5xx response, a CLIENT root for any 4xx or 5xx, and a
// CONSUMER or INTERNAL root for any failure.
func rootStatus(kind otlp.SpanKind, r *ledger.Request) otlp.StatusCode {
	if !r.Causal {
		return spanStatus(r.Outcome)
	}
	var failed bool
	switch kind {
	case otlp.KindServer:
		failed = r.StatusCode >= 500
	case otlp.KindClient:
		failed = r.StatusCode >= 400
	default:
		failed = r.Outcome != ledger.OutcomeSuccess
	}
	if failed {
		return otlp.StatusError
	}
	return otlp.StatusUnset
}

// serverStatus is a callee SERVER span's status. On the causal timeline it is ERROR only for a
// 5xx response (M10): a 4xx answer (rule, token, throttle) stays UNSET and a timed-out call's
// server span, whose own answer the caller never saw, stays UNSET.
func serverStatus(r *ledger.Request, c ledger.Call) otlp.StatusCode {
	if !causalLayout(r) {
		return callStatus(c)
	}
	if !c.TimedOut && c.StatusCode >= 500 {
		return otlp.StatusError
	}
	return otlp.StatusUnset
}

// sharesFault reports whether a failing direct child of hop parent (-1 = the entry) carries f,
// which makes the child, not the parent, nearer the origin.
func sharesFault(r *ledger.Request, parent int, f *ledger.Fault) bool {
	if f == nil {
		return false
	}
	for _, c := range r.Calls {
		if c.ParentHopIndex == parent && c.Failed && c.Fault == f {
			return true
		}
	}
	return false
}

// rootIsOrigin reports that the request's fault originated at the entry itself (an entry resource
// rejection or an entry error_spike).
func rootIsOrigin(r *ledger.Request) bool {
	return r.Fault != nil && r.Outcome != ledger.OutcomeSuccess && !sharesFault(r, -1, r.Fault)
}

// callIsOrigin reports that hop i is the deepest failing hop holding its fault.
func callIsOrigin(r *ledger.Request, i int) bool {
	c := r.Calls[i]
	return c.Failed && c.Fault != nil && !sharesFault(r, i, c.Fault)
}

// originOnClient reports whether a hop's origin span is the caller's CLIENT span: a transport
// failure (no server span), a timeout, or a callee with no SERVER span of its own.
func originOnClient(c ledger.Call, callee *node) bool {
	return c.PeerSpanID == "" || c.TimedOut || !callee.kind.serverSpan
}

// errorTypeOf is the error.type value of a failing span: the origin's own type at the origin,
// else the mapped HTTP status as a string, else the fault's type (timeout, a transport class).
func errorTypeOf(f *ledger.Fault, status int, origin bool) string {
	if origin && f != nil && f.ErrorType != "" {
		return f.ErrorType
	}
	if status != 0 {
		return strconv.Itoa(status)
	}
	if f != nil && f.ErrorType != "" {
		return f.ErrorType
	}
	return errorTypeOther
}

// decorateRoot stamps a failing causal root span: error.type, the response status on an HTTP
// SERVER root, and the cause message when the root is the origin and the span is ERROR.
func decorateRoot(sp *otlp.Span, r *ledger.Request) {
	if !r.Causal || r.Outcome == ledger.OutcomeSuccess {
		return
	}
	origin := rootIsOrigin(r)
	sp.Attrs[attrErrorType] = errorTypeOf(r.Fault, r.StatusCode, origin)
	if sp.Kind == otlp.KindServer && r.StatusCode != 0 {
		sp.Attrs[attrHTTPResponseStatusCode] = r.StatusCode
	}
	if origin && sp.Status == otlp.StatusError {
		sp.StatusMsg = r.Fault.Message
	}
}

// decorateClient stamps a causal hop's CLIENT span: the resend ordinal on any resend, and on a
// failure error.type, the status seen (service hops only) and the cause message at the origin.
func decorateClient(sp *otlp.Span, r *ledger.Request, i int, callee *node) {
	if !causalLayout(r) {
		return
	}
	c := r.Calls[i]
	if c.Attempt >= 1 {
		sp.Attrs[attrHTTPRequestResendCount] = c.Attempt
	}
	if !c.Failed {
		return
	}
	origin := callIsOrigin(r, i) && originOnClient(c, callee)
	sp.Attrs[attrErrorType] = errorTypeOf(c.Fault, c.StatusCode, origin)
	if c.StatusCode != 0 && !callee.kind.leaf {
		sp.Attrs[attrHTTPResponseStatusCode] = c.StatusCode
	}
	if origin && c.Fault != nil {
		sp.StatusMsg = c.Fault.Message
	}
}

// decorateServer stamps a failing causal hop's callee SERVER span (a 4xx or 5xx answer).
func decorateServer(sp *otlp.Span, r *ledger.Request, i int, callee *node) {
	if !causalLayout(r) {
		return
	}
	c := r.Calls[i]
	if !c.Failed || c.TimedOut || c.StatusCode < 400 {
		return
	}
	origin := callIsOrigin(r, i) && !originOnClient(c, callee)
	sp.Attrs[attrErrorType] = errorTypeOf(c.Fault, c.StatusCode, origin)
	sp.Attrs[attrHTTPResponseStatusCode] = c.StatusCode
	if origin && c.Fault != nil && sp.Status == otlp.StatusError {
		sp.StatusMsg = c.Fault.Message
	}
}

// causalHopWindow positions hop c on the causal timeline: client span [start, end) and the callee
// server span's end (its own duration for a timed-out call), both clipped at the request end.
func causalHopWindow(base, reqEnd time.Time, c ledger.Call) (start, end, serverEnd time.Time) {
	start = base.Add(c.Offset)
	cd := max(c.Duration, time.Millisecond)
	end = start.Add(cd)
	serverEnd = end
	if c.ServerDuration > 0 {
		serverEnd = start.Add(c.ServerDuration)
	}
	if end.After(reqEnd) {
		end = reqEnd
	}
	if serverEnd.After(reqEnd) {
		serverEnd = reqEnd
	}
	return start, end, serverEnd
}

// hopView is one node's view of its own part of a request: the entry (index -1) or a hop's callee.
type hopView struct {
	node     string
	index    int    // hop index; -1 = the entry
	spanID   string // the node's own span (callee SERVER span, or the CLIENT span of a leaf)
	reached  bool   // the request arrived at the node (false: a transport failure)
	status   int
	failed   bool          // the node's own answer failed
	fault    *ledger.Fault // cause of the node's failure or slowness
	attempt  int
	upstream string        // the callee whose unabsorbed failure this node answered with
	origin   bool          // the fault originated here: at the node's server, or on its own failed client call
	dur      time.Duration // the node's own span duration
	end      time.Time     // the node's span end on the causal timeline; zero = request end
}

// hopViews lists the entry's view and every hop callee's view, in hop order. A legacy request's
// hops carry no status: a hop is the success status or, when failed, a 500.
func (w *Workload) hopViews(r *ledger.Request) []hopView {
	layout := causalLayout(r)
	ok := w.successStatus()
	lastChild := map[int]int{}
	for i, c := range r.Calls {
		lastChild[c.ParentHopIndex] = i
	}
	upstreamOf := func(parent int, failed bool) string {
		j, has := lastChild[parent]
		if !failed || !has || !r.Calls[j].Failed {
			return ""
		}
		return r.Calls[j].Target
	}
	// clientOrigin: the node's unabsorbed failing call is itself the origin, on its CLIENT span.
	clientOrigin := func(parent int) bool {
		j, has := lastChild[parent]
		if !has || !r.Causal || !callIsOrigin(r, j) {
			return false
		}
		callee := w.graph.byName[r.Calls[j].Target]
		return callee != nil && originOnClient(r.Calls[j], callee)
	}
	entryFailed := r.Outcome != ledger.OutcomeSuccess
	entryUp := upstreamOf(-1, entryFailed && r.Causal)
	views := []hopView{{
		node: w.graph.entry.decl.Name, index: -1, spanID: r.SpanID, reached: true,
		status: r.StatusCode, failed: entryFailed, fault: r.Fault, dur: r.Duration,
		upstream: entryUp,
		origin:   r.Causal && entryFailed && (rootIsOrigin(r) || entryUp != "" && clientOrigin(-1)),
	}}
	base, reqEnd := r.RenderStart(), r.RenderStart().Add(r.Duration)
	for i, c := range r.Calls {
		callee := w.graph.byName[c.Target]
		if callee == nil {
			continue
		}
		v := hopView{node: c.Target, index: i, spanID: c.PeerSpanID, fault: c.Fault, attempt: c.Attempt, dur: c.Duration}
		if v.spanID == "" {
			v.spanID = c.SpanID
		}
		switch {
		case !r.Causal:
			v.reached = true
			v.status = ok
			if c.Failed {
				v.status = 500
			}
			v.failed = c.Failed
		default:
			// A serverSpan callee without a peer span, or a leaf whose call failed with no status
			// short of a timeout, never received the request.
			v.reached = c.PeerSpanID != "" || (!callee.kind.serverSpan && !(c.Failed && c.StatusCode == 0 && !c.TimedOut))
			v.failed = c.Failed && !c.TimedOut
			v.status = c.StatusCode
			if v.status == 0 {
				v.status = ok
			}
			if c.TimedOut && c.ServerDuration > 0 {
				v.dur = c.ServerDuration
			}
			v.upstream = upstreamOf(i, v.failed)
			v.origin = v.failed && (callIsOrigin(r, i) && !originOnClient(c, callee) || v.upstream != "" && clientOrigin(i))
		}
		if layout {
			_, end, serverEnd := causalHopWindow(base, reqEnd, c)
			v.end = end
			if c.ServerDuration > 0 {
				v.end = serverEnd
			}
		}
		views = append(views, v)
	}
	return views
}

// causalRefs adds the node's own hop facts and the cause fields to a request's ref set (design
// §5.2). Error fields are set only on the node's failure; the cause fields also on a slowed hop.
func causalRefs(refs map[string]string, v hopView) {
	refs["span_id"] = v.spanID
	refs["status"] = strconv.Itoa(v.status)
	refs["outcome"] = outcomeOf(v.status).String()
	refs["attempt"] = strconv.Itoa(v.attempt)
	refs["upstream"] = v.upstream
	if v.index >= 0 {
		refs["error_code"] = "" // the request's error code belongs to the entry's answer
	}
	if f := v.fault; f != nil {
		refs["fault_resource"] = f.Resource
		refs["fault_mode"] = f.Mode
		refs["error_message"] = f.Message
	}
	if !v.failed {
		return
	}
	refs["error_type"] = errorTypeOf(v.fault, v.status, v.origin)
	if v.fault != nil && v.origin {
		refs["exception_type"] = v.fault.Exception
	}
}

// projectCausalLogs is projectLogs for a causal request carrying a Fault or a failure: each node
// writes its lines from its own hop facts, once per attempt that reached it, at that attempt's end.
// Unreached nodes log nothing.
func (w *Workload) projectCausalLogs(r *ledger.Request, world *core.World, line func(n *node, level string, t time.Time, ctx telemetryspec.EvalCtx)) {
	completedAt := r.RenderStart().Add(max(r.Duration, time.Millisecond))
	views := w.hopViews(r)
	for _, n := range w.graph.nodes {
		if len(n.logs) == 0 {
			continue
		}
		for _, v := range views {
			if v.node != n.decl.Name || !v.reached {
				continue
			}
			refs := reqRefs(r, nil)
			causalRefs(refs, v)
			if !n.decl.tracesEnabled() {
				delete(refs, "span_id")
			}
			t := completedAt
			if !v.end.IsZero() && v.end.Before(completedAt) {
				t = v.end
			}
			line(n, logLevel(outcomeOf(v.status)), t, telemetryspec.EvalCtx{Ref: refs, Rand: world.Shape.Float64, Norm: world.Shape.NormFloat64})
		}
	}
}

// causalSpanWindow holds the metric window's ledger sample when the workload is causal and the
// window holds a request with a Fault or a failure (M6); nil otherwise, keeping every span-metric
// row on its legacy path.
type causalSpanWindow struct {
	w    *Workload
	reqs []*ledger.Request
}

func (w *Workload) causalSpanWindow(now time.Time, world *core.World) *causalSpanWindow {
	if w.m == nil || w.m.causal == nil || world.Ledger == nil {
		return nil
	}
	reqs := world.Ledger.ActiveFor(w.Name(), now, interval)
	for _, r := range reqs {
		if causalFailing(r) {
			return &causalSpanWindow{w: w, reqs: reqs}
		}
	}
	return nil
}

func seconds(d time.Duration) float64 { return d.Seconds() }

// serverDurations are the durations (seconds) of node n's own SERVER/root spans in the window
// whose span-metric status is status.
func (cw *causalSpanWindow) serverDurations(n *node, status string) []float64 {
	if cw == nil {
		return nil
	}
	var out []float64
	for _, r := range cw.reqs {
		if n == cw.w.graph.entry {
			if spanMetricStatus(rootStatus(cw.w.entryRootKind(), r)) == status {
				out = append(out, seconds(r.Duration))
			}
			continue
		}
		for _, c := range r.Calls {
			if c.Target != n.decl.Name || c.PeerSpanID == "" || spanMetricStatus(serverStatus(r, c)) != status {
				continue
			}
			d := c.Duration
			if c.ServerDuration > 0 {
				d = c.ServerDuration
			}
			out = append(out, seconds(d))
		}
	}
	return out
}

// clientDurations are the durations (seconds) of the CLIENT spans calling target in the window;
// status "" matches every status.
func (cw *causalSpanWindow) clientDurations(target, status string) []float64 {
	if cw == nil {
		return nil
	}
	var out []float64
	for _, r := range cw.reqs {
		for _, c := range r.Calls {
			if c.Target == target && (status == "" || spanMetricStatus(callStatus(c)) == status) {
				out = append(out, seconds(c.Duration))
			}
		}
	}
	return out
}

// serverEdgeDurations are the callee SERVER span durations (seconds) of calls to target.
func (cw *causalSpanWindow) serverEdgeDurations(target string) []float64 {
	if cw == nil {
		return nil
	}
	var out []float64
	for _, r := range cw.reqs {
		for _, c := range r.Calls {
			if c.Target != target || c.PeerSpanID == "" {
				continue
			}
			d := c.Duration
			if c.ServerDuration > 0 {
				d = c.ServerDuration
			}
			out = append(out, seconds(d))
		}
	}
	return out
}

// edgeVolume scales an edge's volume by calls to the callee in the sample per sampled request
// (retries add calls, dropped calls remove them); unchanged when there is no causal window.
func (cw *causalSpanWindow) edgeVolume(target string, volume float64) float64 {
	if cw == nil || len(cw.reqs) == 0 {
		return volume
	}
	n := 0
	for _, r := range cw.reqs {
		for _, c := range r.Calls {
			if c.Target == target {
				n++
			}
		}
	}
	return volume * float64(n) / float64(len(cw.reqs))
}

// cycled returns a latency source that still takes lat's draw on every call (so the engine's
// draw sequence is unchanged) but reports the window's real span durations in turn. With no
// durations it is lat itself.
func cycled(lat func() float64, durs []float64) func() float64 {
	if len(durs) == 0 {
		return lat
	}
	i := 0
	return func() float64 {
		_ = lat()
		v := durs[i%len(durs)]
		i++
		return v
	}
}

// causalProviders wires the resource and outcomes value providers for one metric evaluation
// (design §5.4). Only a resource or outcomes value reads them; every other value is unchanged.
func (w *Workload) causalProviders(ctx *telemetryspec.EvalCtx, spec telemetryspec.MetricSpec, n *node, from, now time.Time, world *core.World) {
	switch spec.Value.Kind() {
	case telemetryspec.KindResource:
		if w.m == nil || w.m.causal == nil {
			return
		}
		model := w.m.causal.model
		ctx.Resource = func(name, field string) (float64, bool) {
			r, _, ok := w.cfg.resources.Resolve(name, w.env)
			if !ok {
				return 0, false
			}
			return resourceField(model, r.Name, field, from, now)
		}
	case telemetryspec.KindOutcomes:
		if world == nil || world.Ledger == nil {
			return
		}
		window := now.Sub(from)
		hops := func() []hopView {
			var out []hopView
			for _, r := range world.Ledger.ActiveFor(w.Name(), now, window) {
				for _, v := range w.hopViews(r) {
					if v.node == n.decl.Name && v.reached {
						out = append(out, v)
					}
				}
			}
			return out
		}
		ctx.Outcomes = func(v telemetryspec.OutcomeValue, labels map[string]string) float64 {
			hs := hops()
			match := 0
			for _, h := range hs {
				if outcomeMatches(v, labels, h.status) {
					match++
				}
			}
			if v.EffectiveMeasure() == telemetryspec.MeasureShare {
				if len(hs) == 0 {
					return 0
				}
				return float64(match) / float64(len(hs))
			}
			return float64(match)
		}
		ctx.OutcomeLatencies = func(v telemetryspec.OutcomeValue, labels map[string]string) []float64 {
			var out []float64
			for _, h := range hops() {
				if outcomeMatches(v, labels, h.status) {
					out = append(out, seconds(h.dur))
				}
			}
			return out
		}
	}
}

// outcomeMatches reports whether status matches the value's statuses, or the series' value of its
// status_label.
func outcomeMatches(v telemetryspec.OutcomeValue, labels map[string]string, status int) bool {
	want := v.Status
	if v.StatusLabel != "" {
		want = []string{labels[v.StatusLabel]}
	}
	for _, s := range want {
		switch s {
		case telemetryspec.StatusSuccess:
			if status < 400 {
				return true
			}
		case telemetryspec.Status4xx:
			if status >= 400 && status < 500 {
				return true
			}
		case telemetryspec.Status5xx:
			if status >= 500 && status < 600 {
				return true
			}
		default:
			if s == strconv.Itoa(status) {
				return true
			}
		}
	}
	return false
}

// resourceField reads one design §5.4 field of a resource: gauge fields from its state at now,
// counter fields as window sums over (from, now].
func resourceField(m *causal.Model, name, field string, from, now time.Time) (float64, bool) {
	switch field {
	case "rejected", "admitted", "failed":
		win, ok := m.Window(name, from, now)
		if !ok {
			return 0, false
		}
		switch field {
		case "rejected":
			return win.Rejected, true
		case "admitted":
			return win.Admitted, true
		default:
			return win.Failed, true
		}
	}
	st, ok := m.State(name, now)
	if !ok {
		return 0, false
	}
	switch field {
	case "demand_rps":
		return st.Demand, true
	case "capacity":
		return st.Capacity, true
	case "utilization":
		return st.Utilization, true
	case "in_use":
		return st.InUse, true
	case "waiting":
		return st.Waiting, true
	case "wait_seconds":
		return st.WaitMS / 1000, true
	case "backlog":
		return st.Backlog, true
	case "oldest_age_seconds":
		return st.OldestAgeSec, true
	case "latency_factor":
		return st.LatencyFactor, true
	case "hold_seconds":
		return st.HoldMS / 1000, true
	case "served_rps":
		return st.ServedRPS, true
	case "failed_share":
		return st.FailedShare, true
	}
	return 0, false
}
