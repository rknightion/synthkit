// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"
	"time"

	"github.com/rknightion/synthkit/internal/beyla"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// projectBeylaBatch is the ebpf_only trace lane: it renders each request's boundary-only
// Beyla view (projectBeylaTraces) and ships the whole batch as ONE OTLP export, mirroring
// projectTraces' single-Write contract. Empty / non-Traces context → no-op.
func (w *Workload) projectBeylaBatch(ctx context.Context, world *core.World, batch []*ledger.Request) error {
	resources := make([]otlp.Resource, 0, len(batch))
	for _, r := range batch {
		resources = append(resources, w.projectBeylaTraces(r)...)
	}
	if len(resources) == 0 {
		return nil
	}
	return world.Traces.Write(ctx, resources)
}

// projectBeylaTraces renders Beyla's BOUNDARY-ONLY trace view of one request: a SERVER span
// at the entry, INTERNAL "in queue" + "processing" children, and one CLIENT span per
// downstream hop (db→db.client, service→http.client, llm→gen_ai client). There are NO
// internal business spans (Beyla observes only kernel-visible boundaries). All trace/span
// IDs come from the ledger — nothing is minted here (I9). Returns nil unless the context's
// emission matrix has Traces on (ebpf_only); in coexist_sdk the SDK owns the trace.
//
// The resource block carries the eBPF distro marker (telemetry.distro.name =
// opentelemetry-ebpf-instrumentation) — the load-bearing "came from Beyla" flag.
func (w *Workload) projectBeylaTraces(r *ledger.Request) []otlp.Resource {
	if !beyla.Emission(w.cfg.beylaContext()).Traces {
		return nil
	}

	base := r.RenderStart()
	dur := r.Duration
	if dur <= 0 {
		dur = time.Millisecond
	}

	res := otlp.Resource{
		Attrs: w.beylaResourceAttrs(),
		Spans: make([]otlp.Span, 0, 3+len(r.Calls)),
	}

	// Boundary SERVER span (entry).
	res.Spans = append(res.Spans, otlp.Span{
		Name:     r.Route,
		TraceID:  r.TraceID,
		SpanID:   r.SpanID,
		ParentID: "",
		Kind:     otlp.KindServer,
		Start:    base,
		End:      base.Add(dur),
		Status:   spanStatus(r.Outcome),
		Attrs:    w.beylaServerAttrs(r),
	})

	// INTERNAL "in queue" then "processing" children when there is queue time to model.
	// Queue = the leading 10% of the request window; processing = the remainder up to the
	// first hop (or the whole window when there are no hops). Their ids are NOT minted —
	// they reuse the per-hop span ids deterministically would be wrong, so they parent to
	// the SERVER span and carry no own child relationship downstream. Beyla emits these as
	// children of the boundary SERVER span; synthkit gives them the SERVER span as parent.
	queueDur := time.Duration(float64(dur) * 0.1)
	queueEnd := base.Add(queueDur)
	res.Spans = append(res.Spans,
		otlp.Span{
			Name:     "in queue",
			TraceID:  r.TraceID,
			SpanID:   beylaChildID(r.SpanID, "queue"),
			ParentID: r.SpanID,
			Kind:     otlp.KindInternal,
			Start:    base,
			End:      queueEnd,
			Status:   otlp.StatusOK,
			Attrs:    map[string]any{beyla.AttrSpanMetricsSkip: true},
		},
		otlp.Span{
			Name:     "processing",
			TraceID:  r.TraceID,
			SpanID:   beylaChildID(r.SpanID, "proc"),
			ParentID: r.SpanID,
			Kind:     otlp.KindInternal,
			Start:    queueEnd,
			End:      base.Add(dur),
			Status:   otlp.StatusOK,
			Attrs:    map[string]any{beyla.AttrSpanMetricsSkip: true},
		},
	)

	// One CLIENT span per downstream hop (db.*/http.*/gen_ai.* boundary).
	cursor := queueEnd
	for i := range r.Calls {
		call := r.Calls[i]
		cd := call.Duration
		if cd < time.Millisecond {
			cd = time.Millisecond
		}
		end := cursor.Add(cd)
		if end.After(base.Add(dur)) {
			end = base.Add(dur)
		}
		res.Spans = append(res.Spans, otlp.Span{
			Name:     beylaClientSpanName(call),
			TraceID:  r.TraceID,
			SpanID:   call.SpanID,
			ParentID: r.SpanID,
			Kind:     otlp.KindClient,
			Start:    cursor,
			End:      end,
			Status:   callStatus(call),
			Attrs:    w.beylaClientAttrs(call),
		})
		cursor = end
	}

	return []otlp.Resource{res}
}

// beylaResourceAttrs is the Beyla-origin resource block (dotted semconv form). It carries the
// eBPF distro marker instead of an SDK's telemetry.sdk.* identity. Standalone mode swaps the
// k8s.* identity for host.* attrs.
func (w *Workload) beylaResourceAttrs() map[string]any {
	a := map[string]any{
		"service.name":             w.name,
		"service.namespace":        w.namespace,
		"service.version":          serviceVersion,
		"telemetry.sdk.language":   backendSDKLanguage,
		"telemetry.sdk.name":       beyla.SDKNameBeyla,
		beyla.AttrDistroName:       beyla.DistroName,
		"telemetry.distro.version": beyla.DistroVersionUnset,
	}
	if w.cfg.beylaMode() == beyla.ModeStandalone {
		a["host.name"] = w.name + "-host"
		a["host.id"] = w.podName
		return a
	}
	a["k8s.cluster.name"] = w.cluster
	a["k8s.namespace.name"] = w.namespace
	a["k8s.pod.name"] = w.podName
	a["k8s.deployment.name"] = w.name
	return a
}

// beylaServerAttrs: HTTP server boundary attributes + correlation. span.metrics.skip is
// stamped so the trace-derived spanmetrics generator skips Beyla's own spans (Beyla
// self-emits the span/service-graph families).
func (w *Workload) beylaServerAttrs(r *ledger.Request) map[string]any {
	a := map[string]any{
		beyla.AttrHTTPRequestMethod:      routeMethod(r.Route),
		beyla.AttrHTTPRoute:              routePath(r.Route),
		beyla.AttrHTTPResponseStatusCode: r.StatusCode,
		beyla.AttrSpanMetricsSkip:        true,
	}
	w.stampCorrelation(a, r)
	return a
}

// beylaClientAttrs: per-hop CLIENT boundary attributes (db.*/http.*/gen_ai.*) + correlation +
// span.metrics.skip.
func (w *Workload) beylaClientAttrs(c ledger.Call) map[string]any {
	a := map[string]any{beyla.AttrSpanMetricsSkip: true}
	switch {
	case c.AI != nil:
		a[genai.AttrOperationName] = c.AI.Op
		if c.AI.Provider != "" {
			a[genai.AttrProviderName] = c.AI.Provider
		}
		if c.AI.Model != "" {
			a[genai.AttrRequestModel] = c.AI.Model
			a[genai.AttrResponseModel] = c.AI.Model
		}
	case c.Kind == "db" || c.Kind == "cache":
		a[beyla.AttrDBOperationName] = beylaDBOp(c.Kind)
		a[beyla.AttrDBSystemName] = beylaDBSystem(c.Kind)
	default:
		a[beyla.AttrHTTPRequestMethod] = "POST"
		a[beyla.AttrServerAddress] = c.Target
		a[beyla.AttrHTTPResponseStatusCode] = 200
	}
	return a
}

// beylaClientSpanName names a hop's CLIENT boundary span (gen_ai form for AI hops; db op +
// target otherwise).
func beylaClientSpanName(c ledger.Call) string {
	if c.AI != nil {
		return genai.SpanName(genaiOp(c.AI), genaiSubject(c.AI))
	}
	if fixture.IsAIKind(c.Kind) {
		return c.Kind + " " + c.Target
	}
	return beylaDBOp(c.Kind) + " " + c.Target
}

// beylaChildID derives a deterministic 16-hex span id for an INTERNAL boundary child from the
// parent SERVER span id. NOT a ledger mint — it is a stable transform of an existing ledger
// id (the queue/processing sub-spans are Beyla-internal, not request-scoped correlation ids),
// so the I9 "never mint request-scoped ids" rule is preserved (no new trace/correlation id).
func beylaChildID(parentSpanID, tag string) string {
	// FNV-1a over parent+tag → 8 bytes hex; deterministic, collision-safe enough for synth.
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(parentSpanID); i++ {
		h ^= uint64(parentSpanID[i])
		h *= prime
	}
	for i := 0; i < len(tag); i++ {
		h ^= uint64(tag[i])
		h *= prime
	}
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 0; i < 16; i++ {
		out[15-i] = hexdigits[h&0xf]
		h >>= 4
	}
	return string(out)
}
