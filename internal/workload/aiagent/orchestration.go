// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"fmt"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// delegationProb is the per-(non-first)-turn probability that the orchestrator delegates to a
// sub-agent. Turn 0 ALWAYS fans out to every declared peer (the opening dispatch), so a multi-turn
// orchestration always renders a visible call tree. Deterministic per generation id (no global rand).
const delegationProb = 0.5

// subAgentGen is one fanned-out sub-agent contribution: the sub-agent's own sigil.Generation
// (distinct agent_name, parented to the parent turn's gen), the Lane B parent span id it nests
// under (parentEnv — the agents.base envelope for general_orchestration; the turn's own root
// generateText/streamText span for coding_claude_code, which has no envelope), and the Lane-C
// metric observation it produces. The fan-out is the difference between a linear turn CHAIN and a
// true call TREE (R-orch1; extended to coding sub-agents by SKT-0001 AC#2).
type subAgentGen struct {
	gen       sigil.Generation
	parentEnv string // Lane B parent span id to nest the sub-agent span under
	obs       metricObs
}

// buildOrchestrationFanout produces the sub-agent generations for a general_orchestration
// conversation. The declared orchestrator agent keeps its agent_name on the turn generations; each
// DELEGATION spawns a sub-agent generation under one of the agent's declared Subagents (peer agents),
// with parent_generation_ids → the orchestrator turn's generation. Turn 0 dispatches to every
// declared peer (the fan-out); later turns delegate to one seeded peer with delegationProb. Returns
// nil for any archetype other than general_orchestration, or when no peers are declared.
func buildOrchestrationFanout(agent AgentDecl, r *ledger.Request, orchGens []sigil.Generation, arts []turnArtifacts) []subAgentGen {
	if agent.Archetype != archGeneralOrchestration || len(agent.Subagents) == 0 || len(orchGens) == 0 {
		return nil
	}
	var out []subAgentGen
	for ti := range orchGens {
		og := orchGens[ti]
		art := arts[ti]
		switch {
		case ti == 0:
			// Opening fan-out: the orchestrator dispatches to EVERY declared peer.
			for pi, peer := range agent.Subagents {
				out = append(out, makeSubAgent(agent, r, og, art, peer, art.envSpanID, fmt.Sprintf("sub-%d-%d", ti, pi)))
			}
		case seedUnit(og.ID, "delegate") < delegationProb:
			// Subsequent delegation: one seeded peer.
			peer := agent.Subagents[seedHash(og.ID, "subpick")%uint64(len(agent.Subagents))]
			out = append(out, makeSubAgent(agent, r, og, art, peer, art.envSpanID, fmt.Sprintf("sub-%d", ti)))
		}
	}
	return out
}

// buildCodingSubagentFanout produces per-subagent generations for a coding_claude_code
// conversation whose agent declares Subagents. It mirrors buildOrchestrationFanout's fan-out shape
// exactly (turn 0 dispatches to every declared peer; later turns delegate to one seeded peer with
// delegationProb) so claude-code's sub-agent activity becomes attributable by agent_name the same
// way general_orchestration's already is (SKT-0001 AC#2). The two differences from the general
// case, both structural: the agent_name carries the documented `claude-code/<subagent>` form
// (signals/sigil.md), and the sub-agent's Lane B span nests under the coding turn's OWN root span
// (art.rootSpanID) rather than an agents.base envelope, because coding's 2-level span tree has no
// envelope to nest under. Returns nil for any archetype other than coding_claude_code, or when no
// peers are declared.
func buildCodingSubagentFanout(agent AgentDecl, r *ledger.Request, gens []sigil.Generation, arts []turnArtifacts) []subAgentGen {
	if agent.Archetype != archCodingClaudeCode || len(agent.Subagents) == 0 || len(gens) == 0 {
		return nil
	}
	var out []subAgentGen
	for ti := range gens {
		og := gens[ti]
		art := arts[ti]
		switch {
		case ti == 0:
			// Opening fan-out: the coding agent dispatches to EVERY declared subagent type.
			for pi, peer := range agent.Subagents {
				out = append(out, makeSubAgent(agent, r, og, art, agent.Name+"/"+peer, art.rootSpanID, fmt.Sprintf("csub-%d-%d", ti, pi)))
			}
		case seedUnit(og.ID, "codingDelegate") < delegationProb:
			// Subsequent delegation: one seeded subagent type.
			peer := agent.Subagents[seedHash(og.ID, "codingSubpick")%uint64(len(agent.Subagents))]
			out = append(out, makeSubAgent(agent, r, og, art, agent.Name+"/"+peer, art.rootSpanID, fmt.Sprintf("csub-%d", ti)))
		}
	}
	return out
}

// makeSubAgent builds one sub-agent generation + its metric observation for a delegation by the
// parent turn `og`. The sub-agent shares the conversation + the parent turn's trace_id (the
// delegation is a child call within that turn's trace) but gets its OWN root span id; its
// agent_name is `agentName` (the peer name verbatim for general_orchestration; `<agent.Name>/<peer>`
// for coding_claude_code — SKT-0001 AC#2), it carries NO agent_version, and parent_generation_ids
// points back to the parent generation. parentSpanID is the Lane B span this sub-agent's own span
// nests under (see subAgentGen). Token shape is the modest general form.
func makeSubAgent(agent AgentDecl, r *ledger.Request, og sigil.Generation, art turnArtifacts, agentName, parentSpanID, salt string) subAgentGen {
	sgID := uuidLike(og.ID, "subgen-"+salt)
	spanID := ledger.NewSpanID()

	// Window: the delegated call occupies the middle of the orchestrator turn window.
	dur := art.end.Sub(art.start)
	start := art.start.Add(dur / 4)
	end := art.start.Add(dur * 3 / 4)
	if !end.After(start) {
		end = start.Add(time.Millisecond)
	}

	input := int64(200 + int(seedUnit(sgID, "gin")*1500))
	output := int64(50 + int(seedUnit(sgID, "gout")*800))
	usage := sigil.Usage{Input: input, Output: output, Total: input + output}

	gen := sigil.Generation{
		ID:             sgID,
		ConversationID: r.SessionID,
		OperationName:  sigil.OpGenerateText, // sub-agent calls are SYNC sub-invocations
		Mode:           sigil.ModeSync,
		TraceID:        art.traceID, // same trace as the orchestrator turn (child call)
		SpanID:         spanID,
		Provider:       og.Provider,
		Model:          og.Model,
		ResponseID:     uuidLike(sgID, "resp"),
		ResponseModel:  og.Model,
		SystemPrompt:   og.SystemPrompt, // shares the framework system prompt
		Input: []sigil.Message{{Role: sigil.RoleUser, Parts: []sigil.Part{
			{Text: "Delegated subtask for " + agentName, ProviderType: "text"},
		}}},
		Output: []sigil.Message{{Role: sigil.RoleAssistant, Parts: []sigil.Part{
			{Text: "Completed delegated subtask.", ProviderType: "text"},
		}}},
		Usage:               usage,
		StopReason:          "end_turn",
		StartedAt:           start,
		EndedAt:             end,
		Tags:                agent.Tags,
		AgentName:           agentName,
		AgentVersion:        "", // sub-agents are framework-internal: no declared version
		ParentGenerationIDs: []string{og.ID},
		EffectiveVersion:    sigil.EffectiveVersion(og.SystemPrompt),
		Metadata:            baseMetadata(agent),
	}

	obs := metricObs{
		agentName:    agentName,
		operation:    sigil.OpGenerateText,
		provider:     og.Provider,
		model:        og.Model,
		inputTokens:  usage.Input,
		outputTokens: usage.Output,
		opDurationSec: func() float64 {
			s := end.Sub(start).Seconds()
			if s <= 0 {
				return 0.001
			}
			return s
		}(),
	}

	return subAgentGen{gen: gen, parentEnv: parentSpanID, obs: obs}
}

// subAgentResources builds the Lane B spans for the fanned-out sub-agent generations, grouped by the
// sub-agent's scope (sigil.<peer>, e.g. sigil.claude-code/<subagent> for coding). Each is a
// generateText CLIENT span on the SAME resource as the parent (chatservice/k8s for
// general_orchestration; sigil/sigil for coding_claude_code), nested under each sub-agent's own
// subAgentGen.parentEnv (the parent's agents.base envelope span for general, its turn's own root
// span for coding), and carries the parent_generation_ids attr so Tempo + the aio11y catalog render
// the call tree.
func subAgentResources(res ResourceID, agent AgentDecl, r *ledger.Request, subs []subAgentGen) []otlp.Resource {
	if len(subs) == 0 {
		return nil
	}
	resAttrs := resourceAttrs(res, agent, r)
	byScope := map[string][]otlp.Span{}
	var order []string // preserve first-seen scope order for deterministic output
	for i := range subs {
		s := subs[i]
		attrs := rootSpanAttrs(agent, r, s.gen)
		attrs[sigil.AttrParentGenerationIDs] = strings.Join(s.gen.ParentGenerationIDs, ",")
		span := otlp.Span{
			Name:     spanName(s.gen.OperationName, s.gen.Model),
			TraceID:  s.gen.TraceID,
			SpanID:   s.gen.SpanID,
			ParentID: s.parentEnv,
			Kind:     otlp.KindClient,
			Start:    s.gen.StartedAt,
			End:      s.gen.EndedAt,
			Status:   spanStatus(s.gen),
			Attrs:    attrs,
		}
		scope := sigil.ScopeName(s.gen.AgentName)
		if _, ok := byScope[scope]; !ok {
			order = append(order, scope)
		}
		byScope[scope] = append(byScope[scope], span)
	}
	out := make([]otlp.Resource, 0, len(order))
	for _, scope := range order {
		out = append(out, otlp.Resource{Attrs: resAttrs, Scope: otlp.Scope{Name: scope}, Spans: byScope[scope]})
	}
	return out
}
