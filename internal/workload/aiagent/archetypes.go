// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"fmt"

	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sigil"
)

// Archetype names (mirror sigil.Archetype* — frozen seam). Re-declared here as package locals so the
// workload's archetype switch reads cleanly without leaking the sigil constants into call sites.
const (
	archCodingClaudeCode      = sigil.ArchetypeCodingClaudeCode
	archCodingCodex           = sigil.ArchetypeCodingCodex
	archGeneralOrchestration  = sigil.ArchetypeGeneralOrchestration
	archGeneralAutonomousLoop = sigil.ArchetypeGeneralAutonomousLoop
	archGeneralMultiturn      = sigil.ArchetypeGeneralMultiturn
	archGeneralSingleShot     = sigil.ArchetypeGeneralSingleShot
)

// verdictTool / verdictArg are the autonomous-loop terminal verdict tool name + enum-taxonomy arg.
const (
	verdictTool = "submit_verdict"
	verdictArg  = "verdict"
)

var verdictValues = []string{"benign", "suspicious", "malicious", "inconclusive"}

// applyArchetype post-processes the built generations in place for per-archetype behaviour, and
// returns any WorkflowSteps the archetype emits. It runs AFTER buildGeneration so it can decorate
// the full conversation (e.g. wire parent_generation_ids across turns).
func applyArchetype(agent AgentDecl, r *ledger.Request, gens []sigil.Generation, arts []turnArtifacts) []sigil.WorkflowStep {
	switch agent.Archetype {
	case archCodingCodex:
		applyCodex(gens)
		return nil
	case archGeneralOrchestration:
		return applyOrchestration(agent, r, gens, arts)
	case archGeneralAutonomousLoop:
		applyAutonomousLoop(agent, gens)
		return nil
	case archGeneralSingleShot:
		applySingleShot(gens)
		return nil
	default:
		return nil
	}
}

// applyCodex stamps numeric codex.* metadata keys and clears the agent version (codex emits none).
func applyCodex(gens []sigil.Generation) {
	for i := range gens {
		gens[i].AgentVersion = "" // codex carries no agent_version (captured reality)
		if gens[i].Metadata == nil {
			gens[i].Metadata = map[string]any{}
		}
		// Numeric codex.* metadata (sandbox/approval/effort levels are numeric in the codex SDK).
		gens[i].Metadata["codex.sandbox_level"] = float64(seedHash(gens[i].ID, "codex-sandbox") % 3)
		gens[i].Metadata["codex.approval_policy"] = float64(seedHash(gens[i].ID, "codex-approval") % 2)
		gens[i].Metadata["codex.reasoning_effort"] = float64(seedHash(gens[i].ID, "codex-effort") % 3)
	}
}

// applyOrchestration sets ParentGenerationIDs on sub-agent (non-first) generations so the multi-turn
// fan-out forms a call tree (turn N parents to turn N-1), and emits one WorkflowStep modelling the
// framework orchestration node.
func applyOrchestration(agent AgentDecl, r *ledger.Request, gens []sigil.Generation, arts []turnArtifacts) []sigil.WorkflowStep {
	for i := 1; i < len(gens); i++ {
		gens[i].ParentGenerationIDs = []string{gens[i-1].ID}
	}
	if len(gens) == 0 {
		return nil
	}
	first := gens[0]
	last := gens[len(gens)-1]
	step := sigil.WorkflowStep{
		ID:                  uuidLike(r.SessionID, "wfstep-0"),
		ConversationID:      r.SessionID,
		StepName:            "orchestrate",
		Framework:           "agents.base",
		StartedAt:           arts[0].start,
		EndedAt:             arts[len(arts)-1].end,
		AgentName:           agent.Name,
		AgentVersion:        agent.Version,
		TraceID:             arts[0].traceID,
		SpanID:              arts[0].rootSpanID,
		LinkedGenerationIDs: linkedGenIDs(gens),
		Metadata: map[string]any{
			"first_generation_id": first.ID,
			"last_generation_id":  last.ID,
		},
	}
	return []sigil.WorkflowStep{step}
}

// applyAutonomousLoop ensures the conversation terminates with EXACTLY one terminal verdict tool
// call carrying an enum-taxonomy arg. It appends the verdict tool_call to the LAST generation's
// output (replacing any pre-existing verdict so there is exactly one) and sets its stop reason.
func applyAutonomousLoop(agent AgentDecl, gens []sigil.Generation) {
	if len(gens) == 0 {
		return
	}
	// Strip any verdict tool calls from all turns first (idempotent — exactly one in the end).
	for i := range gens {
		gens[i].Output = stripVerdict(gens[i].Output)
	}
	last := &gens[len(gens)-1]
	verdict := verdictValues[seedHash(last.ID, "verdict")%uint64(len(verdictValues))]
	inputJSON := []byte(fmt.Sprintf(`{%q:%q}`, verdictArg, verdict))
	callID := "call_" + uuidLikeShort(seedHash(last.ID, "verdict-id"))
	tcPart := sigil.Part{
		ProviderType: "tool_call",
		ToolCall:     &sigil.ToolCall{ID: callID, Name: verdictTool, InputJSON: inputJSON},
	}
	if len(last.Output) == 0 {
		last.Output = []sigil.Message{{Role: sigil.RoleAssistant, Parts: []sigil.Part{tcPart}}}
	} else {
		li := len(last.Output) - 1
		last.Output[li].Parts = append(last.Output[li].Parts, tcPart)
	}
	last.StopReason = "tool_use"
}

// applySingleShot enforces the single-shot invariant: exactly one turn, zero tools. (TurnCount
// already returns 1 for single_shot, and the assembler emits no tool calls; this strips any tools
// defensively and clears the declared tool surface.)
func applySingleShot(gens []sigil.Generation) {
	for i := range gens {
		gens[i].Output = stripAllToolCalls(gens[i].Output)
		gens[i].Tools = nil
	}
}

// stripVerdict removes any verdict tool_call parts from a message slice.
func stripVerdict(msgs []sigil.Message) []sigil.Message {
	for mi := range msgs {
		kept := msgs[mi].Parts[:0]
		for _, p := range msgs[mi].Parts {
			if p.ToolCall != nil && p.ToolCall.Name == verdictTool {
				continue
			}
			kept = append(kept, p)
		}
		msgs[mi].Parts = kept
	}
	return msgs
}

// stripAllToolCalls removes every tool_call part from a message slice (single-shot).
func stripAllToolCalls(msgs []sigil.Message) []sigil.Message {
	for mi := range msgs {
		kept := msgs[mi].Parts[:0]
		for _, p := range msgs[mi].Parts {
			if p.ToolCall != nil {
				continue
			}
			kept = append(kept, p)
		}
		msgs[mi].Parts = kept
	}
	return msgs
}

// linkedGenIDs returns the generation ids of a conversation (workflow-step linkage).
func linkedGenIDs(gens []sigil.Generation) []string {
	out := make([]string, len(gens))
	for i := range gens {
		out[i] = gens[i].ID
	}
	return out
}
