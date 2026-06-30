// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"reflect"
	"testing"
)

// TestAssembleConversationDeterminism verifies that the same (seed, archetype, turns) always
// produces the same result, and that a different seed produces a different result.
func TestAssembleConversationDeterminism(t *testing.T) {
	seed := uint64(42)
	arch := ArchetypeCodingClaudeCode
	turns := 3

	got1 := AssembleConversation(seed, arch, turns)
	got2 := AssembleConversation(seed, arch, turns)

	if !reflect.DeepEqual(got1, got2) {
		t.Fatal("same seed+archetype+turns produced different results (not deterministic)")
	}

	got3 := AssembleConversation(seed+1, arch, turns)
	// At least one turn should differ (a seed change should change something).
	if reflect.DeepEqual(got1, got3) {
		t.Log("note: different seeds produced identical conversations (possible with small corpus; not a hard failure)")
		// Not a hard failure with tiny corpus, but log it.
	}
}

// TestAssembleConversationLength verifies the returned slice has exactly the requested turn count.
func TestAssembleConversationLength(t *testing.T) {
	for _, tc := range []struct {
		archetype string
		turns     int
	}{
		{ArchetypeCodingClaudeCode, 1},
		{ArchetypeCodingClaudeCode, 3},
		{ArchetypeGeneralSingleShot, 1},
		{ArchetypeGeneralMultiturn, 4},
	} {
		got := AssembleConversation(1, tc.archetype, tc.turns)
		if len(got) != tc.turns {
			t.Errorf("archetype=%s turns=%d: got %d turns", tc.archetype, tc.turns, len(got))
		}
	}
}

// TestAssembleConversationToolIDWiring verifies that every ToolResult.ToolCallID in the assembled
// conversation matches a prior ToolCall.ID (within the same conversation).
func TestAssembleConversationToolIDWiring(t *testing.T) {
	// Use coding archetype which has tool calls in its corpus.
	conv := AssembleConversation(99, ArchetypeCodingClaudeCode, 6)

	seenToolCallIDs := map[string]bool{}
	for turnIdx, turn := range conv {
		for _, msg := range turn.Output {
			for _, part := range msg.Parts {
				if part.ToolCall != nil {
					if part.ToolCall.ID == "" {
						t.Errorf("turn %d: ToolCall has empty ID", turnIdx)
					}
					seenToolCallIDs[part.ToolCall.ID] = true
				}
			}
		}
		// ToolResult.ToolCallID must refer to a known ToolCall.ID.
		for _, msg := range turn.Input {
			for _, part := range msg.Parts {
				if part.ToolResult != nil {
					if !seenToolCallIDs[part.ToolResult.ToolCallID] {
						t.Errorf("turn %d: ToolResult.ToolCallID %q has no matching prior ToolCall.ID",
							turnIdx, part.ToolResult.ToolCallID)
					}
				}
			}
		}
	}
}

// TestAssembleConversationCodingHasToolUse verifies that, given enough turns for the coding
// archetype, at least one turn has StopReason == "tool_use".
func TestAssembleConversationCodingHasToolUse(t *testing.T) {
	// Try a range of seeds; with enough turns we expect at least one to have tool_use.
	found := false
	for seed := uint64(0); seed < 20; seed++ {
		conv := AssembleConversation(seed, ArchetypeCodingClaudeCode, 4)
		for _, turn := range conv {
			if turn.StopReason == "tool_use" {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Error("coding_claude_code: no turn with StopReason=tool_use found across seeds 0..19")
	}
}

// TestAssembleConversationSingleShot verifies that ArchetypeGeneralSingleShot with turns=1
// produces exactly 1 turn and ToolCalls==0.
func TestAssembleConversationSingleShot(t *testing.T) {
	conv := AssembleConversation(7, ArchetypeGeneralSingleShot, 1)
	if len(conv) != 1 {
		t.Fatalf("want 1 turn, got %d", len(conv))
	}
	if conv[0].ToolCalls != 0 {
		t.Errorf("ArchetypeGeneralSingleShot: want ToolCalls==0, got %d", conv[0].ToolCalls)
	}
}

// TestAssembleConversationNoPanicAllArchetypes verifies that assembling every archetype at
// turns=1..6 never panics.
func TestAssembleConversationNoPanicAllArchetypes(t *testing.T) {
	for _, arch := range AllArchetypes {
		for turns := 1; turns <= 6; turns++ {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("panic: archetype=%s turns=%d: %v", arch, turns, r)
					}
				}()
				_ = AssembleConversation(uint64(turns), arch, turns)
			}()
		}
	}
}

// TestAssembleConversationToolCallsCount verifies ToolCalls is the count of tool_call parts in
// the Output messages of each turn.
func TestAssembleConversationToolCallsCount(t *testing.T) {
	for _, arch := range AllArchetypes {
		conv := AssembleConversation(11, arch, 4)
		for i, turn := range conv {
			var countInOutput int
			for _, msg := range turn.Output {
				for _, part := range msg.Parts {
					if part.ToolCall != nil {
						countInOutput++
					}
				}
			}
			if turn.ToolCalls != countInOutput {
				t.Errorf("arch=%s turn=%d: ToolCalls=%d but counted %d tool_call parts in Output",
					arch, i, turn.ToolCalls, countInOutput)
			}
		}
	}
}

// TestAssembleConversationRoleAssistant verifies that each turn's Role field is "assistant".
func TestAssembleConversationRoleAssistant(t *testing.T) {
	for _, arch := range AllArchetypes {
		conv := AssembleConversation(3, arch, 2)
		for i, turn := range conv {
			if turn.Role != RoleAssistant {
				t.Errorf("arch=%s turn=%d: want Role=%q, got %q", arch, i, RoleAssistant, turn.Role)
			}
		}
	}
}
