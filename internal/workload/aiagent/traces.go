// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// providerOpenAI is the provider value that triggers the openai_v2 instrumentation child span.
const providerOpenAI = "openai"

// openAIScopeName / openAIServerAddr are the captured openai_v2 instrumentation identity (Lane B).
const (
	openAIScopeName  = "opentelemetry.instrumentation.openai_v2"
	openAIServerAddr = "api.openai.com"
)

// agentsBaseScope is the framework-orchestrator envelope scope name for general agents.
const agentsBaseScope = "agents.base"

// buildTraceResources builds the Lane B span tree per spec §2 into []otlp.Resource. Coding agents
// emit a 2-level tree (root generateText/streamText CLIENT → execute_tool INTERNAL children) under
// scope "sigil.<agent>" on a service.name=sigil/job=sigil resource. General agents add an
// "agents.base" `agent.<name>.chat` INTERNAL envelope parent and (openai provider) an
// opentelemetry.instrumentation.openai_v2 `chat <model>` CLIENT child, on the full k8s resource.
//
// The root generateText/streamText span uses each turn's artifact ids, which EQUAL the matching
// Generation.TraceID/SpanID (one source per turn). NO message content lands on the generateText
// span; tool args/result on execute_tool only under capture_mode=full. NO span Events.
func buildTraceResources(res ResourceID, agent AgentDecl, r *ledger.Request, gens []sigil.Generation, arts []turnArtifacts, assembled []sigil.AssembledTurn) []otlp.Resource {
	if len(gens) == 0 {
		return nil
	}
	coding := sigil.IsCoding(agent.Archetype)

	// Group spans by (scope-name) within the right resource. Coding uses ONE resource (sigil) with
	// one scope sigil.<agent>. General uses ONE resource (chatservice/k8s) but THREE scopes:
	// agents.base, sigil.<agent>, openai_v2 (openai only).
	resAttrs := resourceAttrs(res, agent, r)

	if coding {
		scope := otlp.Scope{Name: sigil.ScopeName(agent.Name)}
		var spans []otlp.Span
		for i := range gens {
			spans = append(spans, codingTurnSpans(agent, r, gens[i], arts[i], assembled[i])...)
		}
		return []otlp.Resource{{Attrs: resAttrs, Scope: scope, Spans: spans}}
	}

	// General: collect spans into three scope-keyed resources (same resource attrs).
	var envelopeSpans, llmSpans, openaiSpans []otlp.Span
	for i := range gens {
		env, llm, oai := generalTurnSpans(agent, r, gens[i], arts[i], assembled[i])
		envelopeSpans = append(envelopeSpans, env...)
		llmSpans = append(llmSpans, llm...)
		openaiSpans = append(openaiSpans, oai...)
	}
	out := []otlp.Resource{
		{Attrs: resAttrs, Scope: otlp.Scope{Name: agentsBaseScope}, Spans: envelopeSpans},
		{Attrs: resAttrs, Scope: otlp.Scope{Name: sigil.ScopeName(agent.Name)}, Spans: llmSpans},
	}
	if len(openaiSpans) > 0 {
		out = append(out, otlp.Resource{Attrs: resAttrs, Scope: otlp.Scope{Name: openAIScopeName}, Spans: openaiSpans})
	}
	return out
}

// codingTurnSpans builds one turn's coding span subtree: a root generateText/streamText CLIENT span
// (gen_ai identity/usage + sigil.* attrs; NO message content) and one execute_tool INTERNAL child
// per tool_call in the generation output.
func codingTurnSpans(agent AgentDecl, r *ledger.Request, gen sigil.Generation, art turnArtifacts, _ sigil.AssembledTurn) []otlp.Span {
	root := otlp.Span{
		Name:    spanName(gen.OperationName, gen.Model),
		TraceID: art.traceID,
		SpanID:  art.rootSpanID,
		Kind:    otlp.KindClient,
		Start:   art.start,
		End:     art.end,
		Status:  spanStatus(gen),
		Attrs:   rootSpanAttrs(agent, r, gen),
	}
	spans := []otlp.Span{root}
	spans = append(spans, toolSpans(agent, gen, art)...)
	return spans
}

// generalTurnSpans builds one turn's general span subtree across the three scopes:
//
//	agents.base       agent.<name>.chat       INTERNAL (envelope parent)
//	sigil.<agent>     generateText <model>    CLIENT   (child of envelope)
//	sigil.<agent>     execute_tool <tool>     INTERNAL (children of the LLM call)
//	openai_v2         chat <model>            CLIENT   (openai only; child of the LLM call)
func generalTurnSpans(agent AgentDecl, r *ledger.Request, gen sigil.Generation, art turnArtifacts, _ sigil.AssembledTurn) (envelope, llm, openai []otlp.Span) {
	envSpanID := ledger.NewSpanID()
	envStart, envEnd := art.start, art.end

	envAttrs := map[string]any{
		genai.AttrAgentName: agent.Name,
	}
	if tools := strings.Join(agent.Tools, ","); tools != "" {
		envAttrs[sigil.AttrAgentAvailableTools] = tools
	}
	if peers := strings.Join(agent.Subagents, ","); peers != "" {
		envAttrs[sigil.AttrAgentPeerAgents] = peers
	}
	if agent.CaptureMode == sigil.CaptureFull && gen.SystemPrompt != "" {
		envAttrs[sigil.AttrAgentSystem] = gen.SystemPrompt
		envAttrs[sigil.AttrAgentUserPrompt] = firstUserText(gen)
	}
	envelope = []otlp.Span{{
		Name:    "agent." + agent.Name + ".chat",
		TraceID: art.traceID,
		SpanID:  envSpanID,
		Kind:    otlp.KindInternal,
		Start:   envStart,
		End:     envEnd,
		Status:  spanStatus(gen),
		Attrs:   envAttrs,
	}}

	// The LLM-call root uses the turn's artifact root span id (== Generation.SpanID) and parents to
	// the envelope.
	root := otlp.Span{
		Name:     spanName(gen.OperationName, gen.Model),
		TraceID:  art.traceID,
		SpanID:   art.rootSpanID,
		ParentID: envSpanID,
		Kind:     otlp.KindClient,
		Start:    art.start,
		End:      art.end,
		Status:   spanStatus(gen),
		Attrs:    rootSpanAttrs(agent, r, gen),
	}
	if len(gen.ParentGenerationIDs) > 0 {
		root.Attrs[sigil.AttrParentGenerationIDs] = strings.Join(gen.ParentGenerationIDs, ",")
	}
	llm = []otlp.Span{root}
	llm = append(llm, toolSpans(agent, gen, art)...)

	// openai provider → an openai_v2 chat <model> CLIENT child of the LLM call.
	if r.Provider == providerOpenAI {
		openai = []otlp.Span{{
			Name:     genai.SpanName(genai.OpChat, gen.Model),
			TraceID:  art.traceID,
			SpanID:   ledger.NewSpanID(),
			ParentID: art.rootSpanID,
			Kind:     otlp.KindClient,
			Start:    art.start,
			End:      art.end,
			Status:   spanStatus(gen),
			Attrs: map[string]any{
				genai.AttrOperationName: genai.OpChat,
				"gen_ai.system":         r.Provider,
				genai.AttrRequestModel:  gen.Model,
				"server.address":        openAIServerAddr,
			},
		}}
	}
	return envelope, llm, openai
}

// toolSpans builds the execute_tool INTERNAL child spans for a generation's tool calls. Tool
// args/result are stamped ONLY under capture_mode=full (the content-bearing path). Span ids are
// minted from the ledger (I9); each is timed within the turn window.
func toolSpans(agent AgentDecl, gen sigil.Generation, art turnArtifacts) []otlp.Span {
	calls := outputToolCalls(gen)
	if len(calls) == 0 {
		return nil
	}
	full := agent.CaptureMode == sigil.CaptureFull
	total := art.end.Sub(art.start)
	step := total / time.Duration(len(calls))
	if step <= 0 {
		step = time.Millisecond
	}
	out := make([]otlp.Span, 0, len(calls))
	for i, tc := range calls {
		ts := art.start.Add(step * time.Duration(i))
		te := ts.Add(step)
		if te.After(art.end) || i == len(calls)-1 {
			te = art.end
		}
		if !te.After(ts) {
			te = ts.Add(time.Millisecond)
		}
		attrs := map[string]any{
			genai.AttrToolName:   tc.Name,
			genai.AttrToolType:   "function",
			genai.AttrToolCallID: tc.ID,
		}
		if full {
			if len(tc.InputJSON) > 0 {
				attrs["gen_ai.tool.call.arguments"] = string(tc.InputJSON)
			}
			if res := toolResultFor(gen, tc.ID); res != "" { // I13: omit an absent result, never ""
				attrs["gen_ai.tool.call.result"] = res
			}
		}
		out = append(out, otlp.Span{
			Name:     genai.SpanName(genai.OpExecuteTool, tc.Name),
			TraceID:  art.traceID,
			SpanID:   ledger.NewSpanID(),
			ParentID: art.rootSpanID,
			Kind:     otlp.KindInternal,
			Start:    ts,
			End:      te,
			Status:   otlp.StatusUnset,
			Attrs:    attrs,
		})
	}
	return out
}

// rootSpanAttrs builds the gen_ai identity + usage + sigil.* attrs for a generateText/streamText
// root span. NO message content is stamped here. Cache/reasoning tokens use the sigil-LOCAL
// UNDERSCORE attr keys (R-m2), distinct from internal/genai's dotted keys.
func rootSpanAttrs(agent AgentDecl, r *ledger.Request, gen sigil.Generation) map[string]any {
	a := map[string]any{
		genai.AttrOperationName:  gen.OperationName,
		genai.AttrProviderName:   gen.Provider,
		"gen_ai.system":          gen.Provider, // legacy key (still emitted by sigil sdk)
		genai.AttrRequestModel:   gen.Model,
		genai.AttrResponseModel:  gen.Model,
		genai.AttrInputTokens:    gen.Usage.Input,
		genai.AttrOutputTokens:   gen.Usage.Output,
		genai.AttrConversationID: gen.ConversationID,
		sigil.AttrGenerationID:   gen.ID,
		sigil.AttrSDKName:        agent.SDK,
		sigil.AttrUserID:         userID(agent, r),
	}
	if gen.Usage.CacheRead > 0 {
		a[sigil.AttrCacheReadInputTokens] = gen.Usage.CacheRead
	}
	if gen.Usage.CacheWrite > 0 {
		a[sigil.AttrCacheWriteInputTokens] = gen.Usage.CacheWrite
	}
	if gen.Usage.Reasoning > 0 {
		a[sigil.AttrReasoningTokens] = gen.Usage.Reasoning
	}
	if gen.ThinkingEnabled != nil {
		a[sigil.AttrThinkingEnabled] = *gen.ThinkingEnabled
	}
	// sigil.tag.<k> per declared tag.
	for k, v := range agent.Tags {
		a[sigil.TagPrefix+k] = v
	}
	return a
}

// resourceAttrs builds the OTLP resource attribute set from the declared ResourceID. Coding fleets
// use service.name=sigil/job=sigil; general fleets use the full service+k8s set. An absent dimension
// is OMITTED (never "" / "NA"; I13). For a coding fleet that declared nothing, fall back to the
// captured sigil/sigil identity.
func resourceAttrs(res ResourceID, agent AgentDecl, r *ledger.Request) map[string]any {
	a := map[string]any{}
	put := func(k, v string) {
		if v != "" {
			a[k] = v
		}
	}
	svc := res.ServiceName
	if svc == "" && sigil.IsCoding(agent.Archetype) {
		svc = "sigil"
	}
	put("service.name", svc)
	put("service.namespace", res.ServiceNamespace)
	put("service.version", res.ServiceVersion)
	put("deployment.environment.name", coalesce(res.DeploymentEnvironment, r.Env))
	put("k8s.cluster.name", coalesce(res.K8sCluster, r.Cluster))
	put("k8s.namespace.name", res.K8sNamespace)
	put("k8s.deployment.name", res.K8sDeployment)
	put("cloud.region", res.CloudRegion)
	job := res.Job
	if job == "" && sigil.IsCoding(agent.Archetype) {
		job = "sigil"
	}
	put("job", job)
	return a
}

// coalesce returns the first non-empty argument.
func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// userID returns the per-conversation user identity: a real-ish email for coding agents, a synthetic
// UUID (the conversation id) for general agents (matching captured reality).
func userID(agent AgentDecl, r *ledger.Request) string {
	if sigil.IsCoding(agent.Archetype) {
		return "user-" + shortHash(r.SessionID) + "@example.com"
	}
	return r.SessionID
}

// spanName builds the sigil generateText/streamText span name ("generateText <model>").
func spanName(op, model string) string {
	if model == "" {
		return op
	}
	return op + " " + model
}

// spanStatus maps a generation's call error to a span status.
func spanStatus(gen sigil.Generation) otlp.StatusCode {
	if gen.CallError != "" {
		return otlp.StatusError
	}
	return otlp.StatusUnset
}

// outputToolCalls collects the ToolCall parts across a generation's output messages, in order.
func outputToolCalls(gen sigil.Generation) []*sigil.ToolCall {
	var out []*sigil.ToolCall
	for _, msg := range gen.Output {
		for i := range msg.Parts {
			if msg.Parts[i].ToolCall != nil {
				out = append(out, msg.Parts[i].ToolCall)
			}
		}
	}
	return out
}

// toolResultFor finds the tool_result content matching a tool call id within the generation's input
// (the tool-result messages live on the following input). Returns "" if not found.
func toolResultFor(gen sigil.Generation, callID string) string {
	for _, msg := range gen.Input {
		for _, p := range msg.Parts {
			if p.ToolResult != nil && p.ToolResult.ToolCallID == callID {
				return p.ToolResult.Content
			}
		}
	}
	return ""
}

// firstUserText returns the first user-message text part of a generation (for agent.user_prompt).
func firstUserText(gen sigil.Generation) string {
	for _, msg := range gen.Input {
		if msg.Role == sigil.RoleUser {
			for _, p := range msg.Parts {
				if p.Text != "" {
					return p.Text
				}
			}
		}
	}
	return ""
}

// shortHash returns a short stable hex of a string (for the coding user.id email local part).
func shortHash(s string) string {
	h := seedHash(s, "user")
	return uuidLikeShort(h)
}

func uuidLikeShort(h uint64) string {
	const hexd = "0123456789abcdef"
	b := make([]byte, 8)
	for i := 0; i < 8; i++ {
		b[i] = hexd[(h>>(uint(i)*4))&0xf]
	}
	return string(b)
}
