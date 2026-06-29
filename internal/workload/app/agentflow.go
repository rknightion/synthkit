// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"hash/fnv"
	"time"

	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// minSpanDur floors every agent-flow span so nested children never collapse to a zero-duration span
// (which would land latency observations in the 0.0 bucket and skew p95).
const minSpanDur = time.Millisecond

// emitAgentFlow builds the nested in-process gen_ai span subtree for one request, parented under
// parentSpanID (the node's structural SERVER span). Shape (real LangChain/LangGraph instrumentation):
//
//	invoke_workflow <wf>            SPAN_KIND_INTERNAL  (child of parentSpanID)
//	  └─ invoke_agent <agent>       SPAN_KIND_INTERNAL  (one agent drawn from the pool per request)
//	       ├─ execute_tool <tool>   SPAN_KIND_INTERNAL  (a deterministic subset of the agent's tools)
//	       └─ chat <model>          SPAN_KIND_CLIENT    (the LLM call — omitted when f.OmitChat)
//
// All spans share r.TraceID + the three correlation keys (universalAttrs), carry gen_ai-semconv
// attributes (names/ops from internal/genai), and NEVER stamp content keys. Per-request agent + tool
// selection is deterministic off r.SpanID so the -dump inventory is stable. A Grafana Cloud
// metrics-generator can turn these spans into traces_spanmetrics_* keyed on span_name/span_kind
// (synthkit emits no spanmetrics for them itself), so span-name-scoped dashboards populate once
// SPAN_KIND_INTERNAL is in the generator's set.
func emitAgentFlow(f *AgenticFlow, r *ledger.Request, parentSpanID string, start, end time.Time) []otlp.Span {
	if f == nil || f.Workflow == "" || len(f.Agents) == 0 {
		return nil
	}
	seed := fnvSeed(r.SpanID)

	spans := make([]otlp.Span, 0, 8)

	// ── invoke_workflow (child of the backend SERVER span) ───────────────────────────────────────
	wfID := ledger.NewSpanID()
	wfStart, wfEnd := inset(start, end, 0.02)
	spans = append(spans, flowSpan(r, genai.SpanName(genai.OpInvokeWorkflow, f.Workflow), parentSpanID, wfID,
		otlp.KindInternal, wfStart, wfEnd, map[string]any{
			genai.AttrOperationName: genai.OpInvokeWorkflow,
			genai.AttrWorkflowName:  f.Workflow,
		}))

	// ── invoke_agent (one agent drawn per request, child of the workflow span) ───────────────────
	agent := f.Agents[int(seed%uint64(len(f.Agents)))]
	agID := ledger.NewSpanID()
	agStart, agEnd := inset(wfStart, wfEnd, 0.05)
	spans = append(spans, flowSpan(r, genai.SpanName(genai.OpInvokeAgent, agent.Name), wfID, agID,
		otlp.KindInternal, agStart, agEnd, map[string]any{
			genai.AttrOperationName: genai.OpInvokeAgent,
			genai.AttrAgentName:     agent.Name,
		}))

	// ── execute_tool* + chat: sequenced within the agent window ──────────────────────────────────
	tools := pickTools(agent.Tools, seed)
	slots := len(tools)
	if !f.OmitChat {
		slots++ // the chat leaf gets the final slot
	}
	if slots == 0 {
		return spans
	}
	for i, tool := range tools {
		ts, te := slot(agStart, agEnd, i, slots)
		spans = append(spans, flowSpan(r, genai.SpanName(genai.OpExecuteTool, tool), agID, ledger.NewSpanID(),
			otlp.KindInternal, ts, te, map[string]any{
				genai.AttrOperationName: genai.OpExecuteTool,
				genai.AttrToolName:      tool,
			}))
	}
	if !f.OmitChat {
		cs, ce := slot(agStart, agEnd, slots-1, slots)
		inTok, outTok := tokenShape(seed)
		spans = append(spans, flowSpan(r, genai.SpanName(genai.OpChat, r.Model), agID, ledger.NewSpanID(),
			otlp.KindClient, cs, ce, map[string]any{
				genai.AttrOperationName:         genai.OpChat,
				genai.AttrRequestModel:          r.Model,
				genai.AttrResponseModel:         r.Model, // response model == request model (§6.1 fallback visibility)
				genai.AttrProviderName:          r.Provider,
				genai.AttrInputTokens:           inTok,
				genai.AttrOutputTokens:          outTok,
				genai.AttrReasoningOutputTokens: outTok / 10, // extended-thinking fraction (§6.1)
			}))
	}
	return spans
}

// flowSpan assembles one agent-flow span: universalAttrs (correlation keys) merged with the gen_ai
// attrs, the shared trace id, and a floored, parent-bounded interval.
func flowSpan(r *ledger.Request, name, parentID, spanID string, kind otlp.SpanKind, start, end time.Time, attrs map[string]any) otlp.Span {
	a := universalAttrs(r)
	for k, v := range attrs {
		a[k] = v
	}
	if !end.After(start) {
		end = start.Add(minSpanDur)
	}
	return otlp.Span{
		Name: name, TraceID: r.TraceID, SpanID: spanID, ParentID: parentID,
		Kind: kind, Start: start, End: end, Status: otlp.StatusUnset, Attrs: a,
	}
}

// inset shrinks [start,end] inward by frac at the front (keeping the end), flooring duration.
func inset(start, end time.Time, frac float64) (time.Time, time.Time) {
	d := end.Sub(start)
	s := start.Add(time.Duration(float64(d) * frac))
	if !end.After(s.Add(minSpanDur)) {
		s = start
	}
	return s, end
}

// slot returns the i-th of n sequential sub-intervals of [start,end], each floored at minSpanDur.
func slot(start, end time.Time, i, n int) (time.Time, time.Time) {
	if n <= 0 {
		return start, end
	}
	total := end.Sub(start)
	step := total / time.Duration(n)
	if step < minSpanDur {
		step = minSpanDur
	}
	s := start.Add(step * time.Duration(i))
	e := s.Add(step)
	if e.After(end) || i == n-1 {
		e = end
	}
	if !e.After(s) {
		e = s.Add(minSpanDur)
	}
	return s, e
}

// pickTools deterministically selects a non-empty prefix of the agent's tool pool for this request
// (count varies 1..len by the seed). Empty pool ⇒ no tools.
func pickTools(pool []string, seed uint64) []string {
	if len(pool) == 0 {
		return nil
	}
	n := 1 + int(seed%uint64(len(pool)))
	return pool[:n]
}

// tokenShape derives deterministic, plausible input/output token counts for the chat leaf (FIELDS on
// the span, never labels — bounded but request-varying).
func tokenShape(seed uint64) (in, out int64) {
	in = 200 + int64(seed%800)       // ~200–1000
	out = 50 + int64((seed>>16)%450) // ~50–500
	return in, out
}

// genAIClientChatNameTemplate is the gen_ai_client profile's flat chat SpanSpec name template
// (internal/telemetryspec/profiles/gen_ai_client.go). A node with an agentic flow emits its own chat
// leaf, so this exact spec is dropped to avoid a duplicate chat span (kept narrow on purpose).
const genAIClientChatNameTemplate = "{{gen_ai.operation.name}} {{gen_ai.request.model}}"

// dropChatSpanSpec returns specs with the gen_ai_client profile's flat chat SpanSpec removed
// (Kind=="client" AND the exact chat NameTemplate). Other client SpanSpecs are preserved.
func dropChatSpanSpec(specs []telemetryspec.SpanSpec) []telemetryspec.SpanSpec {
	out := specs[:0]
	for _, sp := range specs {
		if sp.Kind == telemetryspec.SpanKindClient && sp.NameTemplate == genAIClientChatNameTemplate {
			continue
		}
		out = append(out, sp)
	}
	return out
}

// fnvSeed hashes a span id to a stable per-request seed (mirrors the routeIdx FNV idiom).
func fnvSeed(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}
