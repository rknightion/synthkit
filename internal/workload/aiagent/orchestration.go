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
// (distinct agent_name, parented to the orchestrator gen), the orchestrator turn's envelope span id
// it nests under (parentEnv), and the Lane-C metric observation it produces. The fan-out is the
// difference between a linear turn CHAIN and a true orchestration TREE (R-orch1).
type subAgentGen struct {
	gen       sigil.Generation
	parentEnv string // the orchestrator turn's agents.base envelope span id (Lane B parent)
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
				out = append(out, makeSubAgent(agent, r, og, art, peer, fmt.Sprintf("sub-%d-%d", ti, pi)))
			}
		case seedUnit(og.ID, "delegate") < delegationProb:
			// Subsequent delegation: one seeded peer.
			peer := agent.Subagents[seedHash(og.ID, "subpick")%uint64(len(agent.Subagents))]
			out = append(out, makeSubAgent(agent, r, og, art, peer, fmt.Sprintf("sub-%d", ti)))
		}
	}
	return out
}

// makeSubAgent builds one sub-agent generation + its metric observation for a delegation by the
// orchestrator turn `og`. The sub-agent shares the conversation + the orchestrator turn's trace_id
// (the delegation is a child call within that turn's trace) but gets its OWN root span id; its
// agent_name is the peer name, it carries NO agent_version, and parent_generation_ids points back to
// the orchestrator generation. Token shape is the modest general form.
func makeSubAgent(agent AgentDecl, r *ledger.Request, og sigil.Generation, art turnArtifacts, peer, salt string) subAgentGen {
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
			{Text: "Delegated subtask for " + peer, ProviderType: "text"},
		}}},
		Output: []sigil.Message{{Role: sigil.RoleAssistant, Parts: []sigil.Part{
			{Text: "Completed delegated subtask.", ProviderType: "text"},
		}}},
		Usage:               usage,
		StopReason:          "end_turn",
		StartedAt:           start,
		EndedAt:             end,
		Tags:                agent.Tags,
		AgentName:           peer,
		AgentVersion:        "", // sub-agents are framework-internal: no declared version
		ParentGenerationIDs: []string{og.ID},
		EffectiveVersion:    sigil.EffectiveVersion(og.SystemPrompt),
		Metadata:            baseMetadata(agent),
	}

	obs := metricObs{
		agentName:    peer,
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

	return subAgentGen{gen: gen, parentEnv: art.envSpanID, obs: obs}
}

// subAgentResources builds the Lane B spans for the fanned-out sub-agent generations, grouped by the
// sub-agent's scope (sigil.<peer>). Each is a generateText CLIENT span on the SAME chatservice/k8s
// resource as the orchestrator, nested under the orchestrator turn's agents.base envelope span, and
// carries the parent_generation_ids attr so Tempo + the aio11y catalog render the call tree.
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
