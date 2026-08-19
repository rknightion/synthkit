// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/sigil"
)

func generalOrchAgent() AgentDecl {
	return AgentDecl{
		Name:        "product_agent",
		Archetype:   "general_orchestration",
		SDK:         "sdk-python",
		Provider:    "openai",
		Models:      []string{"gpt-4.1-nano"},
		Tools:       []string{"search_products", "get_price"},
		Subagents:   []string{"pricing_agent", "inventory_agent"},
		CaptureMode: "full",
		Streaming:   true,
		Version:     "1.4.0",
		Activity:    Activity{SessionsPerMin: 30, TurnsP50: 3, TurnsP95: 6},
	}
}

func codexAgent() AgentDecl {
	return AgentDecl{
		Name:      "codex",
		Archetype: "coding_codex",
		SDK:       "sdk-go",
		Provider:  "openai",
		Models:    []string{"gpt-5.5"},
		Tools:     []string{"apply_patch", "shell"},
		Activity:  Activity{SessionsPerMin: 20, TurnsP50: 4, TurnsP95: 10},
		// no Version (codex carries none)
	}
}

func autonomousAgent() AgentDecl {
	return AgentDecl{
		Name:      "soc-analyst",
		Archetype: "general_autonomous_loop",
		SDK:       "sdk-python",
		Provider:  "bedrock",
		Models:    []string{"us.anthropic.claude-3-haiku"},
		Tools:     []string{"query_logs", "lookup_ioc", "submit_verdict"},
		Activity:  Activity{SessionsPerMin: 10, TurnsP50: 4, TurnsP95: 8},
	}
}

func singleShotAgent() AgentDecl {
	return AgentDecl{
		Name:      "title_generator",
		Archetype: "general_single_shot",
		SDK:       "sdk-python",
		Provider:  "openai",
		Models:    []string{"gpt-4o-mini"},
		Activity:  Activity{SessionsPerMin: 50, TurnsP50: 1, TurnsP95: 1},
	}
}

// TestCodexArchetype: numeric codex.* metadata keys + empty AgentVersion.
func TestCodexArchetype(t *testing.T) {
	agent := codexAgent()
	r := fixedReq("conv-codex-1", 60*time.Second)
	r.Route = agent.Name
	r.Provider = agent.Provider
	r.Model = agent.Models[0]
	gens, _, _, _ := buildConversation(ResourceID{}, agent, r)
	if len(gens) == 0 {
		t.Fatal("no generations")
	}
	for i, g := range gens {
		if g.AgentVersion != "" {
			t.Fatalf("turn %d: codex AgentVersion=%q, want empty", i, g.AgentVersion)
		}
		for _, k := range []string{"codex.sandbox_level", "codex.approval_policy", "codex.reasoning_effort"} {
			v, ok := g.Metadata[k]
			if !ok {
				t.Fatalf("turn %d: missing codex metadata key %q", i, k)
			}
			if _, isFloat := v.(float64); !isFloat {
				t.Fatalf("turn %d: codex metadata %q = %T, want numeric (float64)", i, k, v)
			}
		}
	}
}

// TestOrchestrationArchetype: TRUE fan-out — orchestrator turns carry NO parent_generation_ids;
// sub-agent generations exist under DISTINCT agent_names drawn from the declared peers, each
// parented to an orchestrator generation; and a WorkflowStep is emitted (R-orch1).
func TestOrchestrationArchetype(t *testing.T) {
	agent := generalOrchAgent()
	agent.Activity.TurnsP50 = 4 // ensure >1 turn
	r := fixedReq("conv-orch-1", 90*time.Second)
	r.Route = agent.Name
	r.Provider = agent.Provider
	r.Model = agent.Models[0]
	gens, steps, _, _ := buildConversation(ResourceID{ServiceName: "chatservice"}, agent, r)
	if len(gens) < 2 {
		t.Fatalf("only %d generations; expected orchestrator turns + sub-agent fan-out", len(gens))
	}

	// Partition into orchestrator gens (agent_name == the declared orchestrator) and sub-agent gens.
	peers := map[string]bool{}
	for _, p := range agent.Subagents {
		peers[p] = true
	}
	orchByID := map[string]bool{}
	var subAgentCount int
	for i := range gens {
		switch {
		case gens[i].AgentName == agent.Name:
			orchByID[gens[i].ID] = true
			// Orchestrator turns are tree ROOTS: no parent ids (no linear chain).
			if len(gens[i].ParentGenerationIDs) != 0 {
				t.Fatalf("orchestrator gen %s: unexpected ParentGenerationIDs %v (should be a tree root)",
					gens[i].ID, gens[i].ParentGenerationIDs)
			}
		case peers[gens[i].AgentName]:
			subAgentCount++
			if gens[i].AgentVersion != "" {
				t.Fatalf("sub-agent %s: AgentVersion=%q, want empty", gens[i].AgentName, gens[i].AgentVersion)
			}
			if len(gens[i].ParentGenerationIDs) != 1 {
				t.Fatalf("sub-agent %s: ParentGenerationIDs=%v, want exactly one parent",
					gens[i].AgentName, gens[i].ParentGenerationIDs)
			}
		default:
			t.Fatalf("gen %s has unexpected agent_name %q (not orchestrator nor a declared peer)",
				gens[i].ID, gens[i].AgentName)
		}
	}
	if subAgentCount < len(agent.Subagents) {
		t.Fatalf("got %d sub-agent generations, want ≥%d (turn-0 fan-out to every peer)",
			subAgentCount, len(agent.Subagents))
	}
	// Every sub-agent parent must reference a real orchestrator generation.
	for i := range gens {
		if peers[gens[i].AgentName] {
			if !orchByID[gens[i].ParentGenerationIDs[0]] {
				t.Fatalf("sub-agent %s parent %s is not an orchestrator generation",
					gens[i].AgentName, gens[i].ParentGenerationIDs[0])
			}
		}
	}

	if len(steps) == 0 {
		t.Fatal("orchestration: expected a WorkflowStep")
	}
	if steps[0].ConversationID != r.SessionID {
		t.Fatalf("WorkflowStep ConversationID=%q, want %q", steps[0].ConversationID, r.SessionID)
	}
}

// TestCodingClaudeCodeSubagentFanout verifies the coding_claude_code archetype attributes its
// declared Subagents' activity under distinct `claude-code/<subagent>` agent_names, parented to the
// coding turn that delegates to them — the same fan-out shape general_orchestration already uses
// (SKT-0001 AC#2).
func TestCodingClaudeCodeSubagentFanout(t *testing.T) {
	agent := codingAgent()
	agent.Subagents = []string{"general-purpose", "code-reviewer"}
	agent.Activity.TurnsP50 = 4 // ensure >1 turn
	r := fixedReq("conv-coding-subagent-1", 90*time.Second)
	r.Route = agent.Name
	r.Provider = agent.Provider
	r.Model = agent.Models[0]
	gens, _, spans, _ := buildConversation(ResourceID{}, agent, r)
	if len(gens) < 2 {
		t.Fatalf("only %d generations; expected coding turns + subagent fan-out", len(gens))
	}

	wantSubName := map[string]bool{}
	for _, p := range agent.Subagents {
		wantSubName[agent.Name+"/"+p] = true
	}
	mainByID := map[string]bool{}
	var subAgentCount int
	for i := range gens {
		switch {
		case gens[i].AgentName == agent.Name:
			mainByID[gens[i].ID] = true
			if len(gens[i].ParentGenerationIDs) != 0 {
				t.Fatalf("coding turn gen %s: unexpected ParentGenerationIDs %v", gens[i].ID, gens[i].ParentGenerationIDs)
			}
		case wantSubName[gens[i].AgentName]:
			subAgentCount++
			if gens[i].AgentVersion != "" {
				t.Fatalf("subagent %s: AgentVersion=%q, want empty", gens[i].AgentName, gens[i].AgentVersion)
			}
			if len(gens[i].ParentGenerationIDs) != 1 {
				t.Fatalf("subagent %s: ParentGenerationIDs=%v, want exactly one parent",
					gens[i].AgentName, gens[i].ParentGenerationIDs)
			}
			if !mainByID[gens[i].ParentGenerationIDs[0]] {
				// Parent must resolve to a real coding-turn generation. Deferred check below once
				// all main-turn ids are collected (order of iteration is not guaranteed).
				continue
			}
		default:
			t.Fatalf("gen %s has unexpected agent_name %q (not the coding agent nor a declared subagent)",
				gens[i].ID, gens[i].AgentName)
		}
	}
	if subAgentCount < len(agent.Subagents) {
		t.Fatalf("got %d subagent generations, want ≥%d (turn-0 fan-out to every declared subagent)",
			subAgentCount, len(agent.Subagents))
	}
	// Every subagent parent must reference a real coding-turn generation (checked here, now that
	// every main-turn id has been collected).
	for i := range gens {
		if wantSubName[gens[i].AgentName] {
			if !mainByID[gens[i].ParentGenerationIDs[0]] {
				t.Fatalf("subagent %s parent %s is not a coding-turn generation",
					gens[i].AgentName, gens[i].ParentGenerationIDs[0])
			}
		}
	}

	// The subagent scope must carry the sigil.<agent_name> convention too.
	found := false
	for _, res := range spans {
		if res.Scope.Name == "sigil."+agent.Name+"/general-purpose" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no Lane B resource scoped sigil.%s/general-purpose found", agent.Name)
	}
}

// TestAutonomousLoopArchetype: exactly one terminal verdict tool call with an enum-taxonomy arg.
func TestAutonomousLoopArchetype(t *testing.T) {
	agent := autonomousAgent()
	r := fixedReq("conv-auto-1", 90*time.Second)
	r.Route = agent.Name
	r.Provider = agent.Provider
	r.Model = agent.Models[0]
	gens, _, _, _ := buildConversation(ResourceID{ServiceName: "chatservice"}, agent, r)
	if len(gens) == 0 {
		t.Fatal("no generations")
	}
	verdictCount := 0
	var verdictVal string
	for _, g := range gens {
		for _, msg := range g.Output {
			for _, p := range msg.Parts {
				if p.ToolCall != nil && p.ToolCall.Name == verdictTool {
					verdictCount++
					verdictVal = string(p.ToolCall.InputJSON)
				}
			}
		}
	}
	if verdictCount != 1 {
		t.Fatalf("expected exactly 1 terminal verdict tool call, got %d", verdictCount)
	}
	// enum-taxonomy arg present.
	found := false
	for _, v := range verdictValues {
		if containsStr(verdictVal, v) {
			found = true
		}
	}
	if !found {
		t.Fatalf("verdict arg %q has no enum-taxonomy value", verdictVal)
	}
	// last turn stop reason is tool_use.
	if gens[len(gens)-1].StopReason != "tool_use" {
		t.Fatalf("last turn StopReason=%q, want tool_use", gens[len(gens)-1].StopReason)
	}
}

// TestSingleShotArchetype: 1 turn, 0 tools.
func TestSingleShotArchetype(t *testing.T) {
	agent := singleShotAgent()
	r := fixedReq("conv-single-1", 30*time.Second)
	r.Route = agent.Name
	r.Provider = agent.Provider
	r.Model = agent.Models[0]
	gens, _, _, _ := buildConversation(ResourceID{ServiceName: "chatservice"}, agent, r)
	if len(gens) != 1 {
		t.Fatalf("single_shot: %d turns, want 1", len(gens))
	}
	if n := countOutputToolCalls(gens[0]); n != 0 {
		t.Fatalf("single_shot: %d tool calls, want 0", n)
	}
	if len(gens[0].Tools) != 0 {
		t.Fatalf("single_shot: %d declared tools, want 0", len(gens[0].Tools))
	}
}

func containsStr(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

var _ = sigil.OpGenerateText // keep sigil import used in case helpers shrink
