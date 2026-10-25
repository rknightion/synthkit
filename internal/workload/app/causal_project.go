// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"math"
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
// not causal projects exactly as before. A successful causal request, healthy or touched, is laid
// out on the engine's timeline (each hop at its Offset), so a subtree a fault never reaches keeps
// the same span durations on healthy and faulted requests; its statuses, logs and span-metric rows
// stay as before. A request failed only by the entry's baseline draw keeps the legacy facts.

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

// causalFailing reports a causal request carrying a Fault or a failure on the causal timeline
// (design M6): only these get per-node log facts and drive span-metric latency cycling. A request
// that failed only from the legacy baseline error draw carries no Fault and no causal hop facts,
// so it projects exactly as legacy (root ruling D85, design §10.5).
func causalFailing(r *ledger.Request) bool {
	if !causalLayout(r) {
		return false
	}
	if r.Fault != nil || r.Outcome != ledger.OutcomeSuccess {
		return true
	}
	for _, c := range r.Calls {
		if c.Failed || c.Fault != nil {
			return true
		}
	}
	return false
}

// hopProto is the wire protocol a node answers over, which decides its span-status rule and
// whether its spans carry http.response.status_code.
type hopProto uint8

const (
	protoHTTP    hopProto = iota // an instrumented HTTP service: stamps http.response.status_code
	protoGRPC                    // a gRPC service: gRPC span-status rule, no HTTP status code
	protoNonHTTP                 // a worker, stream or job node: no HTTP status code stamped
	protoLeaf                    // a db or cache leaf: no server span of its own
)

// protoOf classifies a node by its declared service type. Every instrumented type without its own
// protocol is an HTTP service, an llm node included (a provider API is called over HTTP); worker,
// stream and job nodes (message and run kinds) answer over their own protocols and are not.
func protoOf(n *node) hopProto {
	switch {
	case n == nil || n.kind.leaf:
		return protoLeaf
	case n.decl.Type == "grpc":
		return protoGRPC
	}
	switch n.decl.Type {
	case "worker", "stream", "job":
		return protoNonHTTP
	}
	return protoHTTP
}

// httpNode reports whether a node answers over HTTP. Only HTTP hops carry
// http.response.status_code.
func httpNode(n *node) bool { return protoOf(n) == protoHTTP }

// httpStatusRule reports whether the protocol follows the HTTP span-status rule (M10): only a 5xx
// SERVER answer is ERROR. A worker, stream or job node keeps this shape, which it had when it
// was classed with HTTP; only its status-code stamping is withdrawn.
func (p hopProto) httpStatusRule() bool { return p == protoHTTP || p == protoNonHTTP }

// grpcServerError reports whether a failed answer counts as an error on a gRPC SERVER span. The
// causal facts carry HTTP-class statuses; read through the google.rpc code mapping, only the 5xx
// class (INTERNAL, UNKNOWN, UNIMPLEMENTED, UNAVAILABLE, DATA_LOSS, DEADLINE_EXCEEDED) is a server
// error. The semconv v1.44.0 gRPC server span lists UNKNOWN, DEADLINE_EXCEEDED, UNIMPLEMENTED,
// INTERNAL, UNAVAILABLE and DATA_LOSS as the errors; a throttle (RESOURCE_EXHAUSTED, 429) and a
// rule or argument rejection (INVALID_ARGUMENT, 400-class) are the caller's, so they stay UNSET.
// The client side is unchanged: a CLIENT span is ERROR on any non-OK answer.
func grpcServerError(status int) bool { return status >= 500 }

// entryRootKind is the entry's root span kind: CONSUMER for a worker entry (a message consumer of
// traffic.from_resource), else the service type's root kind.
func (w *Workload) entryRootKind() otlp.SpanKind {
	if w.graph.entry.decl.Worker {
		return otlp.KindConsumer
	}
	return w.graph.entry.kind.rootKind
}

// consumerOperation is the messaging.operation.name of a worker entry's root span: the entry
// processes the messages it is handed, the "process" operation of semantic-conventions v1.44.0
// messaging-spans.md (Process span, example operation names `process`; CONSUMER kind).
const consumerOperation = "process"

// entryRootName is the entry root span's default name for request route. A worker entry (a
// message consumer of traffic.from_resource) names its CONSUMER root `{messaging.operation.name}
// {destination}` per messaging-spans.md Span name, with the bound queue resource as the
// destination (the declared route is a request path, not a queue name). Every other entry keeps
// the route. A declared span name_template of the root's kind still overrides it (nodeSpan).
func (w *Workload) entryRootName(route string) string {
	if q := w.workerQueue(); q != "" {
		return consumerOperation + " " + q
	}
	return route
}

// workerQueue is the queue a worker entry consumes ("" when the entry is not a bound worker).
func (w *Workload) workerQueue() string {
	if !w.graph.entry.decl.Worker || w.m == nil || w.m.causal == nil {
		return ""
	}
	return w.m.causal.fromQueue
}

func (w *Workload) successStatus() int {
	if s := w.cfg.Traffic.SuccessStatus; s != 0 {
		return s
	}
	return 200
}

// rootStatus is the root span status. A request off the causal timeline keeps spanStatus. A
// causal request follows M10: an HTTP SERVER root is ERROR only for a 5xx response, a gRPC SERVER
// root only for a failure that maps to a gRPC server error (grpcServerError), a CLIENT root for
// any 4xx or 5xx, and a CONSUMER or INTERNAL root for any failure (a failed message or call is a
// failed operation; root ruling D85).
func rootStatus(kind otlp.SpanKind, p hopProto, r *ledger.Request) otlp.StatusCode {
	if !causalLayout(r) {
		return spanStatus(r.Outcome)
	}
	var failed bool
	switch {
	case kind == otlp.KindServer && p.httpStatusRule():
		failed = r.StatusCode >= 500
	case kind == otlp.KindServer && p == protoGRPC:
		failed = r.Outcome != ledger.OutcomeSuccess && grpcServerError(r.StatusCode)
	case kind == otlp.KindClient:
		failed = r.StatusCode >= 400
	default:
		failed = r.Outcome != ledger.OutcomeSuccess
	}
	if failed {
		return otlp.StatusError
	}
	return otlp.StatusUnset
}

// serverStatus is a callee SERVER span's status. On the causal timeline an HTTP callee is ERROR
// only for a 5xx response (M10): a 4xx answer (rule, token, throttle) stays UNSET. A gRPC callee
// is ERROR only for the statuses semconv v1.44.0 lists as gRPC server errors (grpcServerError),
// and for a call that timed out, whose deadline the server also met (DEADLINE_EXCEEDED). Any
// other non-HTTP callee is ERROR for any failed answer. A timed-out HTTP call's server span,
// whose own answer the caller never saw, stays UNSET.
func serverStatus(r *ledger.Request, c ledger.Call, p hopProto) otlp.StatusCode {
	if !causalLayout(r) {
		return callStatus(c)
	}
	switch {
	case p == protoGRPC:
		if c.Failed && c.TimedOut || grpcServerError(c.StatusCode) {
			return otlp.StatusError
		}
	case c.TimedOut:
	case c.StatusCode >= 500 || !p.httpStatusRule() && c.Failed && c.StatusCode >= 400:
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

// decorateRoot stamps a failing causal root span: the response status on an HTTP SERVER root,
// and, only when the span is ERROR, error.type and (at the origin) the cause message. A 4xx HTTP
// SERVER answer stays UNSET without error.type (semconv v1.44.0 http-spans note [4]); its cause
// rides the log. A baseline failure off the causal timeline is not decorated.
func decorateRoot(sp *otlp.Span, r *ledger.Request, http bool) {
	if !causalLayout(r) || r.Outcome == ledger.OutcomeSuccess {
		return
	}
	if sp.Kind == otlp.KindServer && http && r.StatusCode != 0 {
		sp.Attrs[attrHTTPResponseStatusCode] = r.StatusCode
	}
	if sp.Status != otlp.StatusError {
		return
	}
	origin := rootIsOrigin(r)
	sp.Attrs[attrErrorType] = errorTypeOf(r.Fault, r.StatusCode, origin)
	if origin {
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
	if c.StatusCode != 0 && httpNode(callee) {
		sp.Attrs[attrHTTPResponseStatusCode] = c.StatusCode
	}
	if origin && c.Fault != nil {
		sp.StatusMsg = c.Fault.Message
	}
}

// decorateServer stamps a failing causal hop's callee SERVER span (a 4xx or 5xx answer, or a gRPC
// timeout): the response status on an HTTP callee, and, only when the span is ERROR, error.type
// and (at the origin) the cause message. A 4xx HTTP answer stays UNSET without error.type (semconv
// v1.44.0 http-spans note [4]), as does a gRPC answer that is not a server error.
func decorateServer(sp *otlp.Span, r *ledger.Request, i int, callee *node) {
	if !causalLayout(r) {
		return
	}
	c := r.Calls[i]
	if !c.Failed {
		return
	}
	if c.TimedOut {
		if sp.Status != otlp.StatusError {
			return
		}
	} else if c.StatusCode < 400 {
		return
	}
	if httpNode(callee) {
		sp.Attrs[attrHTTPResponseStatusCode] = c.StatusCode
	}
	if sp.Status != otlp.StatusError {
		return
	}
	origin := callIsOrigin(r, i) && !originOnClient(c, callee)
	sp.Attrs[attrErrorType] = errorTypeOf(c.Fault, c.StatusCode, origin)
	if origin && c.Fault != nil {
		sp.StatusMsg = c.Fault.Message
	}
}

// onResendPath reports whether call is a resend attempt or sits under one.
func onResendPath(r *ledger.Request, call *ledger.Call) bool {
	if call == nil || !r.Causal {
		return false
	}
	c := *call
	for range len(r.Calls) + 1 {
		if c.Attempt >= 1 {
			return true
		}
		if c.ParentHopIndex < 0 || c.ParentHopIndex >= len(r.Calls) {
			return false
		}
		c = r.Calls[c.ParentHopIndex]
	}
	return false
}

// unitRand is a uniform and a standard-normal source backed by causal.UnitDraw of seed: each call
// hashes a fresh purpose, so it consumes no engine draw.
func unitRand(seed string) (uniform, normal func() float64) {
	d := causal.UnitDraw(seed)
	i := 0
	uniform = func() float64 {
		i++
		return d("span/" + strconv.Itoa(i))
	}
	normal = func() float64 { // Box-Muller
		u1 := math.Max(uniform(), 1e-12)
		return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*uniform())
	}
	return uniform, normal
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
		if !has || !layout || !callIsOrigin(r, j) {
			return false
		}
		callee := w.graph.byName[r.Calls[j].Target]
		return callee != nil && originOnClient(r.Calls[j], callee)
	}
	entryFailed := r.Outcome != ledger.OutcomeSuccess
	entryUp := upstreamOf(-1, entryFailed && layout)
	views := []hopView{{
		node: w.graph.entry.decl.Name, index: -1, spanID: r.SpanID, reached: true,
		status: r.StatusCode, failed: entryFailed, fault: r.Fault, dur: r.Duration,
		upstream: entryUp,
		origin:   layout && entryFailed && (rootIsOrigin(r) || entryUp != "" && clientOrigin(-1)),
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
		case !causalLayout(r):
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
// Unreached nodes log nothing. A line's DSL values draw from the request's hash unit, like a resend
// span's (design §4.1), so how many attempts a fault produced never shifts the engine's later draws.
// line receives the node's own failing status when its answer failed (0 otherwise), for the
// gateway export log's status field.
func (w *Workload) projectCausalLogs(r *ledger.Request, line func(n *node, level string, t time.Time, ctx telemetryspec.EvalCtx, failStatus int)) {
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
			rnd, norm := unitRand(r.TraceID + "\x00log\x00" + n.decl.Name + "\x00" + v.spanID)
			failStatus := 0
			if v.failed && v.status >= 400 {
				failStatus = v.status
			}
			line(n, logLevel(outcomeOf(v.status)), t, telemetryspec.EvalCtx{Ref: refs, Rand: rnd, Norm: norm, Draw: requestDraw(r)}, failStatus)
		}
	}
}

// causalSpanWindow holds the metric window's ledger sample when the workload is causal and the
// window holds a request with a Fault or a failure (M6); nil otherwise, keeping every span-metric
// row on its legacy path.
//
// Inside a gated window a row leaves the legacy latency path only when one of its own hops in the
// window carries the fault (SKT-0168.09.35): a hop that has a Fault, failed, is a resend, or has
// such a hop beneath it (the entry covers its whole request). Every other row keeps the legacy
// draw exactly. A faulted row keeps the legacy draw too, scaled per observation by how far that
// hop's real duration sits from the row's healthy real duration (the mean of the row's latest
// fault-free hops, remembered across ticks), so the fault moves a row by the factor it moved the
// real span and never by the gap between the real and legacy duration scales. Until the workload
// has seen a healthy hop of a row there is no basis to scale by, and the row stays on the draw.
type causalSpanWindow struct {
	w    *Workload
	reqs []*ledger.Request
	hurt map[*ledger.Request][]bool // per gated request: whether each call hop carries the fault
}

func (w *Workload) causalSpanWindow(now time.Time, world *core.World) *causalSpanWindow {
	if w.m == nil || w.m.causal == nil || world.Ledger == nil {
		return nil
	}
	reqs := world.Ledger.ActiveFor(w.Name(), now, interval)
	w.learnHealthySpans(reqs)
	var cw *causalSpanWindow
	for _, r := range reqs {
		if !causalFailing(r) {
			continue
		}
		if cw == nil {
			cw = &causalSpanWindow{w: w, reqs: reqs, hurt: map[*ledger.Request][]bool{}}
		}
		cw.hurt[r] = faultedHops(r)
	}
	return cw
}

// faultedHops marks each call hop of r that carries a fault: its own Fault, failure or resend, or
// one in the subtree beneath it (the hop's span covers its children's time).
func faultedHops(r *ledger.Request) []bool {
	marked := make([]bool, len(r.Calls))
	for i, c := range r.Calls {
		if c.Fault == nil && !c.Failed && c.Attempt == 0 {
			continue
		}
		for j := i; j >= 0 && j < len(marked) && !marked[j]; j = r.Calls[j].ParentHopIndex {
			marked[j] = true
		}
	}
	return marked
}

func entryFaulted(r *ledger.Request, marked []bool) bool {
	if r.Fault != nil || r.Outcome != ledger.OutcomeSuccess {
		return true
	}
	for _, m := range marked {
		if m {
			return true
		}
	}
	return false
}

func seconds(d time.Duration) float64 { return d.Seconds() }

// Healthy-reference keys: a node's SERVER/root spans and one caller's CLIENT spans calling a
// target (SKT-0197: each caller's CLIENT row follows only its own hops).
func serverRefKey(node string) string { return "server\x00" + node }
func clientRefKey(caller, target string) string {
	return "client\x00" + caller + "\x00" + target
}

// hopCaller is the node whose CLIENT span makes hop i: the target of the hop it nests under, or
// the entry for a direct child of the entry's own span.
func (w *Workload) hopCaller(r *ledger.Request, i int) *node {
	if p := r.Calls[i].ParentHopIndex; p >= 0 && p < len(r.Calls) {
		if n := w.graph.byName[r.Calls[p].Target]; n != nil {
			return n
		}
	}
	return w.graph.entry
}

// hopEdgeClient is the service-graph client of hop i: its nearest traced caller up the hop's
// ancestry, as serviceGraphMetricEdges pairs a callee with the last traced service above it;
// nil when no caller above the hop is traced.
func (w *Workload) hopEdgeClient(r *ledger.Request, i int) *node {
	for {
		caller := w.hopCaller(r, i)
		if caller.decl.tracesEnabled() {
			return caller
		}
		p := r.Calls[i].ParentHopIndex
		if caller == w.graph.entry || p < 0 || p >= len(r.Calls) {
			return nil
		}
		i = p
	}
}

func serverHopSeconds(c ledger.Call) float64 {
	if c.ServerDuration > 0 {
		return seconds(c.ServerDuration)
	}
	return seconds(c.Duration)
}

// learnHealthySpans records, per reference key, the mean real duration of the window's
// fault-free hops. It reads only the ledger, so it takes no engine draw.
func (w *Workload) learnHealthySpans(reqs []*ledger.Request) {
	type acc struct{ sum, n float64 }
	sums := map[string]*acc{}
	add := func(key string, v float64) {
		a := sums[key]
		if a == nil {
			a = &acc{}
			sums[key] = a
		}
		a.sum += v
		a.n++
	}
	entry := w.graph.entry.decl.Name
	for _, r := range reqs {
		marked := faultedHops(r)
		if !entryFaulted(r, marked) {
			add(serverRefKey(entry), seconds(r.Duration))
		}
		for i, c := range r.Calls {
			if marked[i] {
				continue
			}
			add(clientRefKey(w.hopCaller(r, i).decl.Name, c.Target), seconds(c.Duration))
			if c.PeerSpanID != "" {
				add(serverRefKey(c.Target), serverHopSeconds(c))
			}
		}
	}
	if len(sums) == 0 {
		return
	}
	if w.healthySpan == nil {
		w.healthySpan = map[string]float64{}
	}
	for k, a := range sums {
		w.healthySpan[k] = a.sum / a.n
	}
}

// scaled is one observation's factor on the legacy draw: the hop's real duration over its row's
// healthy reference for a faulted hop with a reference, else 1.
func (cw *causalSpanWindow) scaled(key string, d float64, faulted bool) float64 {
	if !faulted {
		return 1
	}
	if ref := cw.w.healthySpan[key]; ref > 0 {
		return d / ref
	}
	return 1
}

// rowFactors returns the factors when at least one hop of the row carries the fault, else nil
// (the row stays on the legacy draw).
func rowFactors(factors []float64, anyFaulted bool) []float64 {
	if !anyFaulted {
		return nil
	}
	return factors
}

// serverFactors are the legacy-draw factors of node n's own SERVER/root spans in the window whose
// span-metric status is status; nil when none of them carries the fault.
func (cw *causalSpanWindow) serverFactors(n *node, status string) []float64 {
	if cw == nil {
		return nil
	}
	key := serverRefKey(n.decl.Name)
	var out []float64
	hit := false
	for _, r := range cw.reqs {
		marked, gated := cw.hurt[r]
		if n == cw.w.graph.entry {
			if spanMetricStatus(rootStatus(cw.w.entryRootKind(), protoOf(n), r)) == status {
				f := gated && entryFaulted(r, marked)
				hit = hit || f
				out = append(out, cw.scaled(key, seconds(r.Duration), f))
			}
			continue
		}
		for i, c := range r.Calls {
			if c.Target != n.decl.Name || c.PeerSpanID == "" || spanMetricStatus(serverStatus(r, c, protoOf(n))) != status {
				continue
			}
			f := gated && marked[i]
			hit = hit || f
			out = append(out, cw.scaled(key, serverHopSeconds(c), f))
		}
	}
	return rowFactors(out, hit)
}

// clientFactors are the legacy-draw factors of caller's CLIENT spans calling target in the
// window; status "" matches every status. nil when none of them carries the fault. Another
// caller's hops to the same target never scale caller's row (SKT-0197).
func (cw *causalSpanWindow) clientFactors(caller *node, target, status string) []float64 {
	if cw == nil {
		return nil
	}
	return cw.clientHopFactors(target, status, func(r *ledger.Request, i int) bool { return cw.w.hopCaller(r, i) == caller })
}

// edgeClientFactors are the legacy-draw factors of the CLIENT spans of the service-graph edge
// from client to target: the hops to target whose nearest traced caller is client.
func (cw *causalSpanWindow) edgeClientFactors(client *node, target string) []float64 {
	if cw == nil {
		return nil
	}
	return cw.clientHopFactors(target, "", func(r *ledger.Request, i int) bool { return cw.w.hopEdgeClient(r, i) == client })
}

// clientHopFactors scales each matching hop to target against the healthy reference of its own
// (caller, target) CLIENT spans.
func (cw *causalSpanWindow) clientHopFactors(target, status string, match func(*ledger.Request, int) bool) []float64 {
	var out []float64
	hit := false
	for _, r := range cw.reqs {
		marked, gated := cw.hurt[r]
		for i, c := range r.Calls {
			if c.Target != target || (status != "" && spanMetricStatus(callStatus(c)) != status) || !match(r, i) {
				continue
			}
			f := gated && marked[i]
			hit = hit || f
			out = append(out, cw.scaled(clientRefKey(cw.w.hopCaller(r, i).decl.Name, target), seconds(c.Duration), f))
		}
	}
	return rowFactors(out, hit)
}

// serverEdgeFactors are the legacy-draw factors of the callee SERVER spans of the service-graph
// edge from client to target: the calls to target whose nearest traced caller is client.
func (cw *causalSpanWindow) serverEdgeFactors(client *node, target string) []float64 {
	if cw == nil {
		return nil
	}
	key := serverRefKey(target)
	var out []float64
	hit := false
	for _, r := range cw.reqs {
		marked, gated := cw.hurt[r]
		for i, c := range r.Calls {
			if c.Target != target || c.PeerSpanID == "" || cw.w.hopEdgeClient(r, i) != client {
				continue
			}
			f := gated && marked[i]
			hit = hit || f
			out = append(out, cw.scaled(key, serverHopSeconds(c), f))
		}
	}
	return rowFactors(out, hit)
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

// latGroup is one span-metric row group's latency source in a gated window. It takes exactly the
// legacy path's number of latency draws (legacy), whatever the gated rows observe, so the engine's
// later draws do not shift: each observation draws while the budget lasts, scaled by the row's
// factors in turn (see causalSpanWindow); drain takes the draws the rows did not.
type latGroup struct {
	lat  func() float64
	left int
	last float64
}

// latGroup returns nil (the legacy path: source returns lat itself) when there is no gated window;
// legacy is evaluated only in a gated window.
func (cw *causalSpanWindow) latGroup(lat func() float64, legacy func() int) *latGroup {
	if cw == nil {
		return &latGroup{lat: lat, left: -1}
	}
	return &latGroup{lat: lat, left: legacy()}
}

func (g *latGroup) draw() float64 {
	if g.left > 0 {
		g.left--
		g.last = g.lat()
	}
	return g.last
}

// source is a row's latency source: lat itself off a gated window; else the legacy draws within
// the budget, each scaled by the row's factors in turn (nil factors: the draws unscaled, exactly
// as on the legacy path).
func (g *latGroup) source(factors []float64) func() float64 {
	if g.left < 0 {
		return g.lat
	}
	i := 0
	return func() float64 {
		v := g.draw()
		if len(factors) == 0 {
			return v
		}
		v *= factors[i%len(factors)]
		i++
		return v
	}
}

// drain takes the legacy draws the gated rows did not.
func (g *latGroup) drain() {
	for g.left > 0 {
		g.draw()
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
