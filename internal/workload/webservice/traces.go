// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/semconv"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// correlation attribute keys (the generic cross-system join set, AI-specific keys
// removed — extract §2.4). the correlation span attr / request_id / session_id appear on EVERY
// span. ONLY ledger IDs flow here — nothing is minted in the projection lane (I9).
const (
	attrCorrelationID = semconv.AttrCorrelationID // the §4.1 correlation span attribute
	attrRequestID     = "request_id"
	attrSessionID     = "session_id"
)

// projectTraces emits the de-AI'd span tree for each request as ONE OTLP export with
// multiple Resource blocks: an optional browser resource (browser-origin requests) plus
// the backend service resource, carrying the connected tree. Tempo assembles the tree by
// trace_id + parent_span_id across resources (I2 multi-Resource). Span timing starts from
// r.RenderStart() (I11). Each request's spans share r.TraceID (correlation invariant).
func (w *Workload) projectTraces(ctx context.Context, world *core.World, batch []*ledger.Request) error {
	resources := make([]otlp.Resource, 0, 2)

	backendRes := otlp.Resource{Attrs: w.backendResourceAttrs(), Spans: make([]otlp.Span, 0, len(batch)*2)}
	var browserRes otlp.Resource
	browserRes.Attrs = w.browserResourceAttrs()

	// calleeRes holds one resource block per distinct service-hop callee in this batch.
	calleeRes := map[string]*otlp.Resource{}

	for _, r := range batch {
		base := r.RenderStart()
		dur := r.Duration
		if dur <= 0 {
			dur = time.Millisecond
		}

		// Backend SERVER span. Parent is the browser span for browser-origin requests,
		// else root ("").
		serverParent := ""
		if r.BrowserOrigin && r.BrowserSpanID != "" {
			serverParent = r.BrowserSpanID
			// Browser CLIENT root span (RUM-origin): wraps the backend with a slightly
			// wider window (browser-only timing).
			browserStart := base.Add(-15 * time.Millisecond)
			browserRes.Spans = append(browserRes.Spans, otlp.Span{
				Name:     "HTTP " + routeMethod(r.Route),
				TraceID:  r.TraceID,
				SpanID:   r.BrowserSpanID,
				ParentID: "", // trace root
				Kind:     otlp.KindClient,
				Start:    browserStart,
				End:      base.Add(dur).Add(15 * time.Millisecond),
				Status:   spanStatus(r.Outcome),
				Attrs:    w.browserSpanAttrs(r),
			})
		}

		backendRes.Spans = append(backendRes.Spans, otlp.Span{
			Name:     r.Route,
			TraceID:  r.TraceID,
			SpanID:   r.SpanID,
			ParentID: serverParent,
			Kind:     otlp.KindServer,
			Start:    base,
			End:      base.Add(dur),
			Status:   spanStatus(r.Outcome),
			Attrs:    w.serverSpanAttrs(r),
		})

		// One CLIENT span per downstream hop. Each hop's CLIENT span parents to the backend
		// SERVER (ParentHopIndex == -1) or to its parent hop's INNER span (the parent's
		// PeerSpanID if it is a service hop, else its SpanID). All hop span ids are pre-minted
		// at mint time (Task 5), so parenting by id is order-independent. service hops also get a
		// SERVER span in the callee's own resource block (calleeRes, declared above).
		cursor := base.Add(time.Duration(float64(dur) * 0.1)) // small pre-processing gap
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

			// CLIENT span parent = backend SERVER (-1) or the parent hop's inner span.
			parentID := r.SpanID
			if call.ParentHopIndex >= 0 && call.ParentHopIndex < len(r.Calls) {
				p := r.Calls[call.ParentHopIndex]
				parentID = p.SpanID
				if p.PeerSpanID != "" {
					parentID = p.PeerSpanID
				}
			}

			st := hopStampers[call.Kind]
			if st == nil {
				// defensive default: an AI kind never falls back to db shaping.
				if fixture.IsAIKind(call.Kind) {
					st = hopStampers[fixture.KindLLMModel]
				} else {
					st = hopStampers["db"]
				}
			}
			backendRes.Spans = append(backendRes.Spans, otlp.Span{
				Name:     st.spanName(call),
				TraceID:  r.TraceID,
				SpanID:   call.SpanID,
				ParentID: parentID,
				Kind:     otlp.KindClient,
				Start:    cursor,
				End:      end,
				Status:   callStatus(call),
				Attrs:    st.clientAttrs(w, r, call),
			})

			// service + llm_gateway hops: a SERVER span in the peer's own resource block (the
			// callee service / the connected gateway node, Path-B). Name + attrs come from the
			// stamper (gateway → gen_ai server view carrying portkey_trace_id).
			if rattrs, emit := st.peerResource(w, call); emit && call.PeerSpanID != "" {
				cr := calleeRes[call.Target]
				if cr == nil {
					cr = &otlp.Resource{Attrs: rattrs, Spans: make([]otlp.Span, 0, 1)}
					calleeRes[call.Target] = cr
				}
				pname, pattrs := st.peerSpan(w, r, call)
				cr.Spans = append(cr.Spans, otlp.Span{
					Name:     pname,
					TraceID:  r.TraceID,
					SpanID:   call.PeerSpanID,
					ParentID: call.SpanID,
					Kind:     otlp.KindServer,
					Start:    cursor,
					End:      end,
					Status:   callStatus(call),
					Attrs:    pattrs,
				})
			}
			cursor = end
		}
	}

	if len(browserRes.Spans) > 0 {
		resources = append(resources, browserRes)
	}
	resources = append(resources, backendRes)
	for _, cr := range calleeRes {
		resources = append(resources, *cr)
	}
	return world.Traces.Write(ctx, resources)
}

// backendResourceAttrs are the resource attributes on the backend service block (every
// backend span). k8s pod/deployment/node from the placement so traces↔target_info↔kube_pod_info
// join to one pod entity (I12). k8s.node.name is omitted when empty (I13). Legacy
// deployment.environment DROPPED — deployment.environment.name only (clean cutover).
// §5 canon dims (context/use_case/team) added when non-empty (pruned per I13).
func (w *Workload) backendResourceAttrs() map[string]any {
	attrs := map[string]any{
		semconv.AttrServiceName:               w.name,
		semconv.AttrServiceNamespace:          w.namespace,
		semconv.AttrServiceVersion:            w.version,
		semconv.AttrDeploymentEnvironmentName: w.env,
		"k8s.cluster.name":                    w.cluster,
		"k8s.namespace.name":                  w.namespace,
		"k8s.pod.name":                        w.podName,
		"k8s.deployment.name":                 w.name,
		"telemetry.sdk.language":              backendSDKLanguage,
		"telemetry.sdk.name":                  "opentelemetry",
	}
	if w.nodeName != "" {
		attrs["k8s.node.name"] = w.nodeName
	}
	// §5 canon attrs (§5): omit when empty (I13).
	if w.context != "" {
		attrs[semconv.AttrContext] = w.context
	}
	if w.useCase != "" {
		attrs[semconv.AttrUseCase] = w.useCase
	}
	if w.team != "" {
		attrs[semconv.AttrTeam] = w.team
	}
	return attrs
}

// browserResourceAttrs are the resource attributes on the browser (Faro) block.
// Legacy deployment.environment DROPPED — deployment.environment.name only (clean cutover).
// §5 canon dims (context/use_case/team) added when non-empty (pruned per I13).
func (w *Workload) browserResourceAttrs() map[string]any {
	attrs := map[string]any{
		semconv.AttrServiceName:               w.browserServiceName(),
		semconv.AttrServiceNamespace:          frontendNamespace,
		semconv.AttrServiceVersion:            w.version,
		semconv.AttrDeploymentEnvironmentName: w.env,
		"telemetry.sdk.language":              browserSDKLanguage,
		"telemetry.sdk.name":                  "opentelemetry",
		"telemetry.distro.name":               faroDistroName,
		"gf.feo11y.app.id":                    w.feoAppID,
		"gf.feo11y.app.name":                  w.browserServiceName(),
		"telemetry.distro.version":            faroSDKVersion,
		"browser.language":                    "en-US",
		"browser.mobile":                      false,
		"browser.platform":                    "Win32",
	}
	// §5 canon attrs (§5): omit when empty (I13).
	if w.context != "" {
		attrs[semconv.AttrContext] = w.context
	}
	if w.useCase != "" {
		attrs[semconv.AttrUseCase] = w.useCase
	}
	if w.team != "" {
		attrs[semconv.AttrTeam] = w.team
	}
	return attrs
}

// peerServerAttrs are the SERVER-span attributes on a service-hop callee (correlation only;
// the callee resource carries service identity). HTTP attrs mirror the caller's request shape.
func (w *Workload) peerServerAttrs(r *ledger.Request) map[string]any {
	a := map[string]any{
		"http.method":      routeMethod(r.Route),
		"http.status_code": r.StatusCode,
	}
	w.stampCorrelation(a, r)
	return a
}

// serverSpanAttrs: HTTP server attributes + the universal correlation set.
func (w *Workload) serverSpanAttrs(r *ledger.Request) map[string]any {
	a := map[string]any{
		"http.method":      routeMethod(r.Route),
		"http.route":       routePath(r.Route),
		"http.status_code": r.StatusCode,
	}
	w.stampCorrelation(a, r)
	return a
}

// browserSpanAttrs: fetch attributes + correlation + user-action linkage.
func (w *Workload) browserSpanAttrs(r *ledger.Request) map[string]any {
	a := map[string]any{
		"component":        "fetch",
		"http.method":      routeMethod(r.Route),
		"http.status_code": r.StatusCode,
		"http.scheme":      "https",
		"enduser.id":       r.SessionID,
		"session.id":       r.SessionID,
		"app.user_action":  actionName(r.Route),
		// app.user_action_id == request_id ties the browser action to every hop.
		"app.user_action_id": r.RequestID,
	}
	w.stampCorrelation(a, r)
	return a
}

// dbSpanAttrs: db.* semconv on the CLIENT hop (schema only, no row content). For caches we
// model redis with db.system=redis.
func (w *Workload) dbSpanAttrs(r *ledger.Request, call ledger.Call) map[string]any {
	a := map[string]any{
		"db.system":    dbSystem(call),
		"db.name":      call.Target,
		"db.operation": dbOperation(callSpec{Kind: call.Kind, Target: call.Target, Engine: call.Engine}),
		"db.statement": dbStatement(call),
	}
	w.stampCorrelation(a, r)
	return a
}

// aiSpanAttrs builds the gen_ai.* CLIENT-span attributes for an AI hop (vocabulary from
// internal/genai). Content is NEVER emitted (no strip-listed key); only operation/provider/
// model + the kind-specific subject attr + synthetic token counts (inference hops) + the
// request-correlation keys (conversation/portkey_trace_id/correlation).
func (w *Workload) aiSpanAttrs(r *ledger.Request, c ledger.Call) map[string]any {
	a := map[string]any{}
	if c.AI != nil {
		a[genai.AttrOperationName] = c.AI.Op
		if c.AI.Provider != "" {
			a[genai.AttrProviderName] = c.AI.Provider
		}
		if c.AI.Model != "" {
			a[genai.AttrRequestModel] = c.AI.Model
			a[genai.AttrResponseModel] = c.AI.Model
		}
		switch c.Kind {
		case fixture.KindAgent:
			a[genai.AttrAgentName] = c.AI.Subject
		case fixture.KindTool:
			a[genai.AttrToolName] = c.AI.Subject
		case fixture.KindWorkflow:
			a[genai.AttrWorkflowName] = c.AI.Subject
		case fixture.KindRetrieval:
			a[genai.AttrDataSourceID] = c.AI.Subject
		}
		// Inference hops carry synthetic token COUNTS only — never content.
		if c.AI.Model != "" {
			in, out := synthTokens(c.SpanID)
			a[genai.AttrInputTokens] = in
			a[genai.AttrOutputTokens] = out
			// gen_ai.usage.reasoning.output_tokens (§6.1) only on the actual LLM inference hop
			// (chat at the model/gateway) — NOT on retrieval/tool/agent/workflow orchestration spans.
			if c.Kind == fixture.KindLLMModel || c.Kind == fixture.KindLLMGateway {
				a[genai.AttrReasoningOutputTokens] = out / 5 // ≈20% of output are reasoning tokens
			}
		}
	}
	a[genai.AttrConversationID] = r.SessionID
	a["portkey_trace_id"] = r.PortkeyTraceID
	w.stampCorrelation(a, r)
	return a
}

// synthTokens derives a deterministic, plausible (input, output) token pair from a hop's
// span id (stable per render; input ≈ 3× output, matching real LLM ratios). Content-free.
func synthTokens(spanID string) (int, int) {
	seed := 0
	if len(spanID) >= 4 {
		if v, err := strconv.ParseInt(spanID[:4], 16, 0); err == nil {
			seed = int(v)
		}
	}
	in := 200 + seed%1800 // 200–2000 input tokens
	out := 60 + (seed/3)%600
	return in, out
}

// stampCorrelation puts the universal join keys on a span (ONLY ledger IDs — I9).
func (w *Workload) stampCorrelation(a map[string]any, r *ledger.Request) {
	a[attrCorrelationID] = r.CorrelationID
	a[attrRequestID] = r.RequestID
	a[attrSessionID] = r.SessionID
}

// dbSystem maps the call engine to the db.system semconv value.
func dbSystem(call ledger.Call) string {
	switch call.Engine {
	case "redis":
		return "redis"
	case "mysql":
		return "mysql"
	case "postgres":
		return "postgresql"
	}
	if call.Kind == "cache" {
		return "redis"
	}
	return "postgresql"
}

// dbStatement returns a content-free statement template (schema only — no row data).
func dbStatement(call ledger.Call) string {
	if call.Kind == "cache" {
		return "GET ?"
	}
	return "SELECT * FROM ? WHERE id = ?"
}

// callStatus maps a failed hop to span status.
func callStatus(call ledger.Call) otlp.StatusCode {
	if call.Failed {
		return otlp.StatusError
	}
	return otlp.StatusOK
}

// spanStatus maps a request outcome to OTLP span status.
func spanStatus(o ledger.Outcome) otlp.StatusCode {
	if o == ledger.OutcomeServerError {
		return otlp.StatusError
	}
	return otlp.StatusOK
}

// routeMethod / routePath split a "GET /v1/items" route into its method and path.
func routeMethod(route string) string {
	if i := strings.IndexByte(route, ' '); i > 0 {
		return route[:i]
	}
	return "GET"
}

func routePath(route string) string {
	if i := strings.IndexByte(route, ' '); i > 0 {
		return route[i+1:]
	}
	return route
}

// actionName is the low-card user-action name for a route (the RUM user-action label).
func actionName(route string) string {
	if routeMethod(route) == "GET" {
		return "load-page"
	}
	return "submit-form"
}
