// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
)

// AssembledTurn is one fully-assembled conversation turn, ready for the aiagent workload to
// populate Generation / span / metric fields. It is the output of AssembleConversation.
//
// Input holds the user (and tool-result) messages that precede this generation.
// Output holds the assistant messages this generation produced.
// ToolCalls is the count of tool_call parts across all Output messages (used for the
// gen_ai_client_tool_calls_per_operation_count metric).
// Role is always RoleAssistant (the generating role).
type AssembledTurn struct {
	Input        []Message // user + tool-result messages feeding this generation
	Output       []Message // assistant messages this generation produced
	Tools        []ToolDef // declared tool surface for this turn
	SystemPrompt string    // effective system prompt (from the corpus system_prompt record)
	StopReason   string    // e.g. "tool_use", "end_turn", "stop"
	ToolCalls    int       // count of ToolCall parts in Output
	Role         string    // always RoleAssistant
}

// AssembleConversation deterministically assembles a conversation of `turns` AssembledTurns from
// the embedded corpus for the given archetype. Seeding from `seed` (typically derived from a
// per-run conversation id) ensures intra-conversation coherence while content varies across runs
// (corrected per review B1 — the SHAPE is deterministic, not individual conversation content).
//
// Constraints:
//   - Uses a seeded *rand.Rand (never global math/rand; I12).
//   - stdlib only (no OTel SDK, no grpc).
//   - NO "scenario" literal anywhere (TestCatalogIsDeCustomerized).
func AssembleConversation(seed uint64, archetype string, turns int) []AssembledTurn {
	rng := rand.New(rand.NewSource(int64(seed))) //nolint:gosec // seeded, not crypto
	recs, err := LoadCorpus(archetype)
	if err != nil || len(recs) == 0 {
		// Corpus load failure → return minimal assembled turns so callers never get nil.
		return makeFallbackConversation(turns)
	}

	// Bucket records by kind.
	var (
		systemPromptRecs []CorpusRecord
		turnRecs         []CorpusRecord
	)
	for _, r := range recs {
		switch r.Kind {
		case "system_prompt":
			systemPromptRecs = append(systemPromptRecs, r)
		case "turn":
			turnRecs = append(turnRecs, r)
		}
	}

	systemPrompt := ""
	if len(systemPromptRecs) > 0 {
		systemPrompt = systemPromptRecs[rng.Intn(len(systemPromptRecs))].Text
	}

	isSingleShot := archetype == ArchetypeGeneralSingleShot

	result := make([]AssembledTurn, 0, turns)
	for i := 0; i < turns; i++ {
		turn := assembleTurn(rng, archetype, turnRecs, systemPrompt, isSingleShot)
		result = append(result, turn)
	}
	return result
}

// assembleTurn builds one AssembledTurn by sampling corpus turn records and wiring tool ids.
func assembleTurn(rng *rand.Rand, archetype string, turnRecs []CorpusRecord, systemPrompt string, singleShot bool) AssembledTurn {
	if len(turnRecs) == 0 {
		return AssembledTurn{
			SystemPrompt: systemPrompt,
			StopReason:   "end_turn",
			Role:         RoleAssistant,
		}
	}

	// Pick a user-role turn record for the input.
	var userRecs, assistantRecs []CorpusRecord
	for _, r := range turnRecs {
		switch r.Role {
		case "user":
			userRecs = append(userRecs, r)
		case "assistant":
			assistantRecs = append(assistantRecs, r)
		}
	}

	var inputMsgs []Message
	if len(userRecs) > 0 {
		rec := userRecs[rng.Intn(len(userRecs))]
		inputMsgs = append(inputMsgs, corpusRecordToMessage(rec, RoleUser))
	}

	// For single-shot: no tool calls; pick a simple assistant response.
	if singleShot {
		var outputMsgs []Message
		stopReason := "end_turn"
		if len(assistantRecs) > 0 {
			rec := assistantRecs[rng.Intn(len(assistantRecs))]
			// Force no tool calls for single-shot.
			msg := Message{Role: RoleAssistant}
			for _, p := range rec.Parts {
				if p.ToolCall == nil && p.ToolResult == nil {
					if p.Text != "" {
						msg.Parts = append(msg.Parts, Part{Text: p.Text, ProviderType: "text"})
					}
					if p.Thinking != "" {
						msg.Parts = append(msg.Parts, Part{Thinking: p.Thinking, ProviderType: "thinking"})
					}
				}
			}
			if rec.StopReason != "" {
				stopReason = rec.StopReason
			}
			if len(msg.Parts) == 0 {
				msg.Parts = []Part{{Text: "Here is the answer.", ProviderType: "text"}}
			}
			outputMsgs = append(outputMsgs, msg)
		}
		return AssembledTurn{
			Input:        inputMsgs,
			Output:       outputMsgs,
			SystemPrompt: systemPrompt,
			StopReason:   stopReason,
			ToolCalls:    0,
			Role:         RoleAssistant,
		}
	}

	// For non-single-shot: pick an assistant record, wire tool_call → tool_result ids.
	var outputMsgs []Message
	stopReason := "end_turn"
	toolCallCount := 0

	if len(assistantRecs) > 0 {
		rec := assistantRecs[rng.Intn(len(assistantRecs))]
		if rec.StopReason != "" {
			stopReason = rec.StopReason
		}
		msg, toolIDs := buildAssistantMessage(rng, rec, archetype)
		outputMsgs = append(outputMsgs, msg)
		toolCallCount = countToolCalls(msg)

		// If there are tool calls, add a tool-result message to the next input.
		if len(toolIDs) > 0 {
			// Build tool-result messages using the seeded rng (may use corpus tool_result records
			// but we build them inline to keep id wiring simple and deterministic).
			toolResultMsg := buildToolResultMessage(rng, toolIDs, rec)
			// Tool result goes into input (the follow-on user+tool_results messages).
			inputMsgs = append(inputMsgs, toolResultMsg)
		}
	}

	return AssembledTurn{
		Input:        inputMsgs,
		Output:       outputMsgs,
		SystemPrompt: systemPrompt,
		StopReason:   stopReason,
		ToolCalls:    toolCallCount,
		Role:         RoleAssistant,
	}
}

// buildAssistantMessage builds a Message from an assistant corpus record, minting fresh
// tool_call ids (toolu_<12hex> for coding, call_<24alnum> for general/codex) from the seeded rng.
// Returns the message and a slice of tool call IDs that were minted.
func buildAssistantMessage(rng *rand.Rand, rec CorpusRecord, archetype string) (Message, []string) {
	msg := Message{Role: RoleAssistant}
	var toolIDs []string

	for _, cp := range rec.Parts {
		switch {
		case cp.Thinking != "":
			msg.Parts = append(msg.Parts, Part{Thinking: cp.Thinking, ProviderType: "thinking"})
		case cp.Text != "":
			msg.Parts = append(msg.Parts, Part{Text: cp.Text, ProviderType: "text"})
		case cp.ToolCall != nil:
			id := mintToolCallID(rng, archetype)
			inputJSON, _ := json.Marshal(cp.ToolCall.Arguments)
			msg.Parts = append(msg.Parts, Part{
				ProviderType: "tool_call",
				ToolCall: &ToolCall{
					ID:        id,
					Name:      cp.ToolCall.Name,
					InputJSON: inputJSON,
				},
			})
			toolIDs = append(toolIDs, id)
		}
	}

	// If no parts were built (empty record), emit a minimal text part.
	if len(msg.Parts) == 0 {
		msg.Parts = []Part{{Text: "I'll help you with that.", ProviderType: "text"}}
	}

	return msg, toolIDs
}

// buildToolResultMessage builds a MESSAGE_ROLE_TOOL message containing ToolResult parts for each
// minted tool call ID, reusing tool names from the corpus record where available.
func buildToolResultMessage(rng *rand.Rand, toolIDs []string, assistantRec CorpusRecord) Message {
	msg := Message{Role: RoleTool}

	// Collect tool call names from the record.
	var toolNames []string
	for _, cp := range assistantRec.Parts {
		if cp.ToolCall != nil {
			toolNames = append(toolNames, cp.ToolCall.Name)
		}
	}

	for i, id := range toolIDs {
		name := "tool"
		if i < len(toolNames) {
			name = toolNames[i]
		}
		// Generate deterministic but varied content.
		content := fmt.Sprintf("result_%d", rng.Int63n(1000))
		msg.Parts = append(msg.Parts, Part{
			ProviderType: "tool_result",
			ToolResult: &ToolResult{
				ToolCallID: id,
				Name:       name,
				Content:    content,
			},
		})
	}
	return msg
}

// mintToolCallID generates a fresh tool call id using the seeded rng.
// Coding (sdk-go) uses "toolu_<12 hex chars>"; general/codex uses "call_<24 alnum chars>".
func mintToolCallID(rng *rand.Rand, archetype string) string {
	if IsCoding(archetype) {
		return "toolu_" + randomHex(rng, 12)
	}
	return "call_" + randomAlnum(rng, 24)
}

// randomHex returns n random hex characters using the seeded rng.
func randomHex(rng *rand.Rand, n int) string {
	b := make([]byte, (n+1)/2)
	for i := range b {
		b[i] = byte(rng.Intn(256))
	}
	return hex.EncodeToString(b)[:n]
}

// randomAlnum returns n random alphanumeric characters using the seeded rng.
func randomAlnum(rng *rand.Rand, n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rng.Intn(len(charset))]
	}
	return string(b)
}

// corpusRecordToMessage converts a CorpusRecord (user role) to a sigil.Message.
func corpusRecordToMessage(rec CorpusRecord, role string) Message {
	msg := Message{Role: role}
	for _, cp := range rec.Parts {
		if cp.Text != "" {
			msg.Parts = append(msg.Parts, Part{Text: cp.Text, ProviderType: "text"})
		}
	}
	if rec.Text != "" {
		msg.Parts = append(msg.Parts, Part{Text: rec.Text, ProviderType: "text"})
	}
	return msg
}

// countToolCalls counts the number of ToolCall parts in a message.
func countToolCalls(msg Message) int {
	n := 0
	for _, p := range msg.Parts {
		if p.ToolCall != nil {
			n++
		}
	}
	return n
}

// makeFallbackConversation returns minimal turns when corpus load fails, ensuring callers
// never receive nil.
func makeFallbackConversation(turns int) []AssembledTurn {
	result := make([]AssembledTurn, turns)
	for i := range result {
		result[i] = AssembledTurn{
			Output: []Message{
				{Role: RoleAssistant, Parts: []Part{{Text: "Hello.", ProviderType: "text"}}},
			},
			StopReason: "end_turn",
			Role:       RoleAssistant,
		}
	}
	return result
}
