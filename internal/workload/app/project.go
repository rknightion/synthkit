// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/semconv"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// ProjectBatch is the trace/log/RUM lane: handed EXACTLY this instance's freshly minted batch,
// it emits ONE trace across the service graph + each node's correlated logs + (when rumEnabled)
// Faro/RUM beacons for browser-origin requests. Empty batch → no-op.
func (w *Workload) ProjectBatch(ctx context.Context, now time.Time, world *core.World, batch []*ledger.Request) error {
	if len(batch) == 0 {
		return nil
	}
	if world.Traces != nil {
		if err := w.projectTraces(ctx, world, batch); err != nil {
			return err
		}
	}
	if world.Logs != nil {
		if err := w.projectLogs(ctx, world, batch); err != nil {
			return err
		}
	}
	if w.b.RUM != nil {
		if err := w.projectRUM(ctx, world, batch); err != nil {
			return err
		}
	}
	return nil
}

// projectTraces builds ONE trace per request across the graph: the entry node's root span + one
// CLIENT span per edge (in the caller's resource) + the callee's SERVER span (for instrumented
// services). All spans share r.TraceID; parenting is by pre-minted span id (order-independent).
// One otlp.Resource per node (its identity), emitted in node-declaration order (deterministic).
func (w *Workload) projectTraces(ctx context.Context, world *core.World, batch []*ledger.Request) error {
	res := map[string]*otlp.Resource{}
	get := func(n *node) *otlp.Resource {
		r := res[n.decl.Name]
		if r == nil {
			r = &otlp.Resource{Attrs: w.identity(n).resourceAttrs()}
			res[n.decl.Name] = r
		}
		return r
	}

	for _, r := range batch {
		base := r.RenderStart()
		dur := r.Duration
		if dur <= 0 {
			dur = time.Millisecond
		}
		entry := w.graph.entry
		er := get(entry)
		eName, eAttrs, eChildren := w.nodeSpan(entry, entry.kind.rootKind, r.SpanID, r.Route, r, nil, base, base.Add(dur), world)
		er.Spans = append(er.Spans, otlp.Span{
			Name:    eName,
			TraceID: r.TraceID, SpanID: r.SpanID, ParentID: "",
			Kind:  entry.kind.rootKind,
			Start: base, End: base.Add(dur),
			Status: spanStatus(r.Outcome),
			Attrs:  eAttrs,
		})
		er.Spans = append(er.Spans, eChildren...)
		// In-process agentic flow on the ENTRY node (parent = the entry span; full request window).
		if entry.agenticFlow != nil {
			er.Spans = append(er.Spans, emitAgentFlow(entry.agenticFlow, r, r.SpanID, base, base.Add(dur))...)
		}

		// Each hop NESTS within its caller's span window (children start shortly after the parent
		// starts, not after it ends — a correct waterfall for chains deeper than one hop, review M1).
		// r.Calls is DFS pre-order, so a parent hop's start is always recorded before its children.
		reqEnd := base.Add(dur)
		startOf := map[string]time.Time{r.SpanID: base}
		for i := range r.Calls {
			call := r.Calls[i]
			callee := w.graph.byName[call.Target]
			if callee == nil {
				continue
			}
			caller := entry
			callerSpanID := r.SpanID
			if call.ParentHopIndex >= 0 && call.ParentHopIndex < len(r.Calls) {
				p := r.Calls[call.ParentHopIndex]
				if pn := w.graph.byName[p.Target]; pn != nil {
					caller = pn
					callerSpanID = p.PeerSpanID
				}
			}
			parentStart := startOf[callerSpanID]
			if parentStart.IsZero() {
				parentStart = base
			}
			hopStart := parentStart.Add(time.Duration(float64(reqEnd.Sub(parentStart)) * 0.1))
			cd := call.Duration
			if cd < time.Millisecond {
				cd = time.Millisecond
			}
			end := hopStart.Add(cd)
			if end.After(reqEnd) {
				end = reqEnd
			}
			startOf[call.SpanID] = hopStart
			if call.PeerSpanID != "" {
				startOf[call.PeerSpanID] = hopStart
			}
			// CLIENT span in the CALLER's resource (the caller's outbound view — universal correlation
			// attrs; for a db/cache leaf with a resolved db_instance the stable-semconv DB identity
			// (db.system.name / db.namespace / server.address) decorates THIS span — the caller's
			// CLIENT span IS the db leaf (it emits no SERVER span), faithful to real OTel/Beyla DB
			// instrumentation. A node's own DSL SpanSpecs live on its STRUCTURAL span, not on each edge.
			cAttrs := universalAttrs(r)
			if callee.kind.leaf && callee.dbIdentity != nil {
				for k, v := range callee.dbIdentity {
					cAttrs[k] = v
				}
			}
			cr := get(caller)
			cr.Spans = append(cr.Spans, otlp.Span{
				Name:    "call " + call.Target,
				TraceID: r.TraceID, SpanID: call.SpanID, ParentID: callerSpanID,
				Kind:  otlp.KindClient,
				Start: hopStart, End: end,
				Status: callStatus(call),
				Attrs:  cAttrs,
			})
			// SERVER span in the CALLEE's resource (instrumented services only) — the callee's structural
			// span, where its DSL SpanSpecs decorate it (matching kind) or spawn child spans (differing kind).
			if callee.kind.serverSpan && call.PeerSpanID != "" {
				sr := get(callee)
				sName, sAttrs, sChildren := w.nodeSpan(callee, otlp.KindServer, call.PeerSpanID, serverSpanName(callee, call), r, &r.Calls[i], hopStart, end, world)
				sr.Spans = append(sr.Spans, otlp.Span{
					Name:    sName,
					TraceID: r.TraceID, SpanID: call.PeerSpanID, ParentID: call.SpanID,
					Kind:  otlp.KindServer,
					Start: hopStart, End: end,
					Status: callStatus(call),
					Attrs:  sAttrs,
				})
				sr.Spans = append(sr.Spans, sChildren...)
				// In-process agentic flow on this CALLEE node (parent = its SERVER span; hop window).
				if callee.agenticFlow != nil {
					sr.Spans = append(sr.Spans, emitAgentFlow(callee.agenticFlow, r, call.PeerSpanID, hopStart, end)...)
				}
			}
		}
	}

	resources := make([]otlp.Resource, 0, len(res))
	for _, n := range w.graph.nodes { // declaration order → deterministic
		if rr := res[n.decl.Name]; rr != nil {
			resources = append(resources, *rr)
		}
	}
	return world.Traces.Write(ctx, resources)
}

// universalAttrs is the correlation attr set stamped on every app span (stampCorrelation parity).
func universalAttrs(r *ledger.Request) map[string]any {
	return map[string]any{
		semconv.AttrCorrelationID: r.CorrelationID,
		"request_id":              r.RequestID,
		"session_id":              r.SessionID,
	}
}

// nodeSpan builds a node's STRUCTURAL span (name + attrs) plus any CHILD spans declared via its DSL
// SpanSpecs. A SpanSpec whose Kind matches the structural span's kind (or is unset) DECORATES it —
// its interpolated NameTemplate becomes the span name and its attributes merge in (e.g. rum_faro on a
// frontend CLIENT root → the browser fetch span). A SpanSpec whose Kind DIFFERS emits a CHILD span
// parented to the structural span (e.g. a gen_ai_client CLIENT spec on a backend SERVER span → a
// "chat gpt-4o" CLIENT child), matching real gen_ai instrumentation instead of smearing gen_ai.* onto
// the server span (review #3). Child span ids come from the ledger (I9); children are timed inside the
// structural [start,end] window.
func (w *Workload) nodeSpan(n *node, structuralKind otlp.SpanKind, structuralSpanID, defaultName string,
	r *ledger.Request, call *ledger.Call, start, end time.Time, world *core.World) (name string, attrs map[string]any, children []otlp.Span) {
	name = defaultName
	attrs = universalAttrs(r)
	ctx := telemetryspec.EvalCtx{Ref: reqRefs(r, call), Rand: world.Shape.Float64, Norm: world.Shape.NormFloat64}
	childStart := start.Add(time.Duration(float64(end.Sub(start)) * 0.1))
	for _, sp := range n.spans {
		spAttrs := make(map[string]any, len(sp.Attributes))
		for k, vm := range sp.Attributes {
			spAttrs[k] = evalAttr(vm, ctx)
		}
		if sp.Kind == "" || spanKindOf(sp.Kind) == structuralKind {
			if nm := interpolateName(sp.NameTemplate, spAttrs); nm != "" {
				name = nm
			}
			for k, v := range spAttrs {
				attrs[k] = v
			}
			continue
		}
		children = append(children, otlp.Span{
			Name:    interpolateName(sp.NameTemplate, spAttrs),
			TraceID: r.TraceID, SpanID: ledger.NewSpanID(), ParentID: structuralSpanID,
			Kind:  spanKindOf(sp.Kind),
			Start: childStart, End: end,
			Status: otlp.StatusUnset,
			Attrs:  spAttrs,
		})
	}
	return name, attrs, children
}

// spanKindOf maps a DSL span-kind string to the otlp.SpanKind (default internal).
func spanKindOf(k string) otlp.SpanKind {
	switch k {
	case telemetryspec.SpanKindServer:
		return otlp.KindServer
	case telemetryspec.SpanKindClient:
		return otlp.KindClient
	default:
		return otlp.KindInternal
	}
}

// interpolateName resolves {{attr.key}} placeholders in a span NameTemplate from the span's evaluated
// attributes, trimming the result; an unresolved placeholder collapses to empty.
func interpolateName(tmpl string, attrs map[string]any) string {
	if tmpl == "" {
		return ""
	}
	out := tmpl
	for k, v := range attrs {
		out = strings.ReplaceAll(out, "{{"+k+"}}", fmt.Sprint(v))
	}
	for {
		i := strings.Index(out, "{{")
		if i < 0 {
			break
		}
		j := strings.Index(out[i:], "}}")
		if j < 0 {
			break
		}
		out = out[:i] + out[i+j+2:]
	}
	return strings.TrimSpace(out)
}

// serverSpanName names a callee's SERVER span. Real HTTP server spans are named "{METHOD} {route}"
// (predecessor tracetree.go BackendHTTPRoute; web_service beyla_traces.go uses r.Route) — the route
// string already carries the method (e.g. "POST /v1/assist"). A node names its server span from its
// OWN declared routes (drawn deterministically per request from the peer span id, so a multi-route
// node varies stably across the -dump sample); a node that declares no routes (agent/db/internal
// service) falls back to its node name. The ENTRY's root span is named separately from r.Route (the
// minter's per-request entry-route draw) — this covers the non-entry callees.
func serverSpanName(callee *node, call ledger.Call) string {
	routes := callee.decl.Routes
	if len(routes) == 0 {
		return callee.decl.Name
	}
	return routes[routeIdx(call.PeerSpanID, len(routes))]
}

// routeIdx maps a per-request seed (a span id) to a stable index in [0,n) via FNV-1a, so a node's
// drawn route is deterministic for a given request without minting/storing it in the ledger (I9).
func routeIdx(seed string, n int) int {
	if n <= 1 {
		return 0
	}
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(seed); i++ {
		h ^= uint64(seed[i])
		h *= prime
	}
	return int(h % uint64(n))
}

// projectLogs emits each node's declared log streams correlated to the request batch. Lines are
// grouped into streams by (node, source, level) so high-card join keys never multiply streams —
// they ride in structured metadata (loki.Line.Meta), low-card identity in stream labels.
func (w *Workload) projectLogs(ctx context.Context, world *core.World, batch []*ledger.Request) error {
	type key struct{ node, source, level string }
	streams := map[key]*loki.Stream{}
	var order []key

	for _, r := range batch {
		// Request logs are COMPLETION events ("… served"): timestamp them at the trace's end
		// (RenderStart+Duration ≈ now), not at the backdated request start, so logs land with the
		// span end and stay in the recent window (parity with webservice/logs.go).
		dur := r.Duration
		if dur <= 0 {
			dur = time.Millisecond
		}
		completedAt := r.RenderStart().Add(dur)
		level := logLevel(r.Outcome)
		for _, n := range w.graph.nodes {
			if len(n.logs) == 0 {
				continue
			}
			id := w.identity(n)
			ctxEval := telemetryspec.EvalCtx{Ref: reqRefs(r, nil), Rand: world.Shape.Float64, Norm: world.Shape.NormFloat64}
			for _, spec := range n.logs {
				k := key{n.decl.Name, spec.Source, level}
				s := streams[k]
				if s == nil {
					s = &loki.Stream{Labels: w.streamLabels(id, spec, level)}
					streams[k] = s
					order = append(order, k)
				}
				body := map[string]any{}
				meta := map[string]string{}
				for bk, vm := range spec.Body {
					if vm.IsHighCardRef() {
						if _, sv := vm.Eval(ctxEval); sv != "" {
							meta[bk] = sv
						}
						continue
					}
					body[bk] = evalAttr(vm, ctxEval)
				}
				s.Lines = append(s.Lines, loki.Line{T: completedAt, Body: jsonBody(body), Meta: meta})
			}
		}
	}

	out := make([]loki.Stream, 0, len(order))
	for _, k := range order {
		out = append(out, *streams[k])
	}
	return world.Logs.Write(ctx, out)
}

// streamLabels stamps node identity + the author's low-card stream labels. Author labels are
// evaluated deterministically (EvalCtx{}) — capability matrix guarantees const/const_str/enum, so
// the stream-label KEY set is run-stable (enum collapses to a deterministic value in v1).
func (w *Workload) streamLabels(id nodeIdentity, spec telemetryspec.LogSpec, level string) map[string]string {
	labels := id.streamBaseLabels(spec.Source, level)
	for k, vm := range spec.StreamLabels {
		if _, s := vm.Eval(telemetryspec.EvalCtx{}); s != "" {
			labels[k] = s
		}
	}
	return labels
}

func spanStatus(o ledger.Outcome) otlp.StatusCode {
	if o == ledger.OutcomeServerError {
		return otlp.StatusError
	}
	return otlp.StatusUnset
}

func callStatus(c ledger.Call) otlp.StatusCode {
	if c.Failed {
		return otlp.StatusError
	}
	return otlp.StatusUnset
}
