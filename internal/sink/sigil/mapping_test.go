// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	nativesigil "github.com/rknightion/synthkit/internal/sigil"
	sigilv1 "github.com/rknightion/synthkit/internal/sink/sigil/v1"
)

func TestToProtoGenerations_FieldByField(t *testing.T) {
	maxTok := int64(4096)
	temp := 0.7
	topP := 0.95
	choice := "auto"
	think := true

	started := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	ended := time.Date(2026, 6, 30, 10, 0, 5, 0, time.UTC)

	inputJSON := []byte(`{"path":"/tmp/foo.go"}`)

	gen := nativesigil.Generation{
		ID:             "gen-001",
		ConversationID: "conv-abc",
		OperationName:  "generateText",
		Mode:           "SYNC",
		TraceID:        "trace-xyz",
		SpanID:         "span-001",
		Provider:       "anthropic",
		Model:          "claude-opus-4-5",
		ResponseID:     "resp-001",
		ResponseModel:  "claude-opus-4-5-20260630",
		SystemPrompt:   "you are helpful",
		Input: []nativesigil.Message{
			{
				Role: "user",
				Parts: []nativesigil.Part{
					{Text: "hello world"},
				},
			},
		},
		Output: []nativesigil.Message{
			{
				Role: "assistant",
				Parts: []nativesigil.Part{
					{Thinking: "let me think", ProviderType: "thinking"},
					{Text: "here is the answer"},
					{
						ProviderType: "tool_call",
						ToolCall: &nativesigil.ToolCall{
							ID:        "toolu_001",
							Name:      "read_file",
							InputJSON: inputJSON,
						},
					},
				},
			},
			{
				Role: "tool",
				Parts: []nativesigil.Part{
					{
						ToolResult: &nativesigil.ToolResult{
							ToolCallID:  "toolu_001",
							Name:        "read_file",
							Content:     "file contents",
							ContentJSON: []byte(`{"lines":10}`),
							IsError:     false,
						},
					},
				},
			},
		},
		Tools: []nativesigil.ToolDef{
			{Name: "read_file", Description: "reads a file", Type: "function", InputSchemaJSON: []byte(`{}`)},
		},
		Usage: nativesigil.Usage{
			Input:      100,
			Output:     200,
			Total:      300,
			CacheRead:  50,
			CacheWrite: 25,
			Reasoning:  15,
		},
		StopReason:          "tool_use",
		StartedAt:           started,
		EndedAt:             ended,
		Tags:                map[string]string{"entrypoint": "claude-code", "version": "1.0"},
		Metadata:            map[string]any{"sdk": "sdk-go", "capture_mode": "default"},
		AgentName:           "coding-agent",
		AgentVersion:        "sha256:abc123",
		MaxTokens:           &maxTok,
		Temperature:         &temp,
		TopP:                &topP,
		ToolChoice:          &choice,
		ThinkingEnabled:     &think,
		ParentGenerationIDs: []string{"gen-000"},
		EffectiveVersion:    "sha256:" + strings.Repeat("a", 64),
	}

	protos := toProtoGenerations([]nativesigil.Generation{gen})
	if len(protos) != 1 {
		t.Fatalf("expected 1 proto generation, got %d", len(protos))
	}
	pg := protos[0]

	// Basic string fields
	assertStr(t, "id", "gen-001", pg.GetId())
	assertStr(t, "conversation_id", "conv-abc", pg.GetConversationId())
	assertStr(t, "operation_name", "generateText", pg.GetOperationName())
	assertStr(t, "trace_id", "trace-xyz", pg.GetTraceId())
	assertStr(t, "span_id", "span-001", pg.GetSpanId())
	assertStr(t, "response_id", "resp-001", pg.GetResponseId())
	assertStr(t, "response_model", "claude-opus-4-5-20260630", pg.GetResponseModel())
	assertStr(t, "system_prompt", "you are helpful", pg.GetSystemPrompt())
	assertStr(t, "stop_reason", "tool_use", pg.GetStopReason())
	assertStr(t, "agent_name", "coding-agent", pg.GetAgentName())

	// Mode enum
	if pg.GetMode() != sigilv1.GenerationMode_GENERATION_MODE_SYNC {
		t.Errorf("mode: want GENERATION_MODE_SYNC, got %v", pg.GetMode())
	}

	// ModelRef
	if pg.GetModel() == nil {
		t.Fatal("model is nil")
	}
	assertStr(t, "model.provider", "anthropic", pg.GetModel().GetProvider())
	assertStr(t, "model.name", "claude-opus-4-5", pg.GetModel().GetName())

	// Input messages
	if len(pg.GetInput()) != 1 {
		t.Fatalf("input len: want 1, got %d", len(pg.GetInput()))
	}
	inputMsg := pg.GetInput()[0]
	if inputMsg.GetRole() != sigilv1.MessageRole_MESSAGE_ROLE_USER {
		t.Errorf("input[0].role: want USER, got %v", inputMsg.GetRole())
	}
	if len(inputMsg.GetParts()) != 1 {
		t.Fatalf("input[0] parts len: want 1, got %d", len(inputMsg.GetParts()))
	}
	if inputMsg.GetParts()[0].GetText() != "hello world" {
		t.Errorf("input[0].parts[0].text: want 'hello world', got %q", inputMsg.GetParts()[0].GetText())
	}

	// Output messages
	if len(pg.GetOutput()) != 2 {
		t.Fatalf("output len: want 2, got %d", len(pg.GetOutput()))
	}
	assistantMsg := pg.GetOutput()[0]
	if assistantMsg.GetRole() != sigilv1.MessageRole_MESSAGE_ROLE_ASSISTANT {
		t.Errorf("output[0].role: want ASSISTANT, got %v", assistantMsg.GetRole())
	}
	if len(assistantMsg.GetParts()) != 3 {
		t.Fatalf("output[0] parts len: want 3, got %d", len(assistantMsg.GetParts()))
	}

	// Part 0: thinking
	if assistantMsg.GetParts()[0].GetThinking() != "let me think" {
		t.Errorf("output[0].parts[0].thinking: want 'let me think', got %q", assistantMsg.GetParts()[0].GetThinking())
	}
	if assistantMsg.GetParts()[0].GetMetadata().GetProviderType() != "thinking" {
		t.Errorf("output[0].parts[0].metadata.provider_type: want 'thinking', got %q",
			assistantMsg.GetParts()[0].GetMetadata().GetProviderType())
	}

	// Part 1: text
	if assistantMsg.GetParts()[1].GetText() != "here is the answer" {
		t.Errorf("output[0].parts[1].text: want 'here is the answer', got %q", assistantMsg.GetParts()[1].GetText())
	}

	// Part 2: tool_call with InputJson bytes
	toolCallPart := assistantMsg.GetParts()[2]
	if toolCallPart.GetToolCall() == nil {
		t.Fatal("output[0].parts[2].tool_call is nil")
	}
	tc := toolCallPart.GetToolCall()
	assertStr(t, "tool_call.id", "toolu_001", tc.GetId())
	assertStr(t, "tool_call.name", "read_file", tc.GetName())
	if string(tc.GetInputJson()) != string(inputJSON) {
		t.Errorf("tool_call.input_json: want %q, got %q", inputJSON, tc.GetInputJson())
	}

	// Tool message
	toolMsg := pg.GetOutput()[1]
	if toolMsg.GetRole() != sigilv1.MessageRole_MESSAGE_ROLE_TOOL {
		t.Errorf("output[1].role: want TOOL, got %v", toolMsg.GetRole())
	}
	if len(toolMsg.GetParts()) != 1 {
		t.Fatalf("output[1] parts len: want 1, got %d", len(toolMsg.GetParts()))
	}
	tr := toolMsg.GetParts()[0].GetToolResult()
	if tr == nil {
		t.Fatal("output[1].parts[0].tool_result is nil")
	}
	assertStr(t, "tool_result.tool_call_id", "toolu_001", tr.GetToolCallId())
	assertStr(t, "tool_result.content", "file contents", tr.GetContent())

	// Usage
	u := pg.GetUsage()
	if u == nil {
		t.Fatal("usage is nil")
	}
	assertInt64(t, "usage.input_tokens", 100, u.GetInputTokens())
	assertInt64(t, "usage.output_tokens", 200, u.GetOutputTokens())
	assertInt64(t, "usage.total_tokens", 300, u.GetTotalTokens())
	assertInt64(t, "usage.cache_read_input_tokens", 50, u.GetCacheReadInputTokens())
	assertInt64(t, "usage.cache_write_input_tokens", 25, u.GetCacheWriteInputTokens())
	assertInt64(t, "usage.reasoning_tokens", 15, u.GetReasoningTokens())

	// Timestamps
	if pg.GetStartedAt() == nil {
		t.Fatal("started_at is nil")
	}
	if !pg.GetStartedAt().AsTime().Equal(started) {
		t.Errorf("started_at mismatch: want %v, got %v", started, pg.GetStartedAt().AsTime())
	}
	if pg.GetCompletedAt() == nil {
		t.Fatal("completed_at is nil")
	}
	if !pg.GetCompletedAt().AsTime().Equal(ended) {
		t.Errorf("completed_at mismatch: want %v, got %v", ended, pg.GetCompletedAt().AsTime())
	}

	// Tags
	if pg.GetTags()["entrypoint"] != "claude-code" {
		t.Errorf("tags[entrypoint]: want 'claude-code', got %q", pg.GetTags()["entrypoint"])
	}

	// Metadata structpb
	if pg.GetMetadata() == nil {
		t.Fatal("metadata is nil")
	}
	if pg.GetMetadata().GetFields()["sdk"].GetStringValue() != "sdk-go" {
		t.Errorf("metadata.sdk: want 'sdk-go', got %q",
			pg.GetMetadata().GetFields()["sdk"].GetStringValue())
	}

	// Optional scalars
	if pg.MaxTokens == nil || *pg.MaxTokens != 4096 {
		t.Errorf("max_tokens: want 4096, got %v", pg.MaxTokens)
	}
	if pg.Temperature == nil || *pg.Temperature != 0.7 {
		t.Errorf("temperature: want 0.7, got %v", pg.Temperature)
	}
	if pg.TopP == nil || *pg.TopP != 0.95 {
		t.Errorf("top_p: want 0.95, got %v", pg.TopP)
	}
	if pg.ToolChoice == nil || *pg.ToolChoice != "auto" {
		t.Errorf("tool_choice: want 'auto', got %v", pg.ToolChoice)
	}
	if pg.ThinkingEnabled == nil || *pg.ThinkingEnabled != true {
		t.Errorf("thinking_enabled: want true, got %v", pg.ThinkingEnabled)
	}

	// ParentGenerationIds
	if len(pg.GetParentGenerationIds()) != 1 || pg.GetParentGenerationIds()[0] != "gen-000" {
		t.Errorf("parent_generation_ids: want [gen-000], got %v", pg.GetParentGenerationIds())
	}

	// EffectiveVersion
	wantEV := "sha256:" + strings.Repeat("a", 64)
	if pg.EffectiveVersion == nil || *pg.EffectiveVersion != wantEV {
		t.Errorf("effective_version: want %q, got %v", wantEV, pg.EffectiveVersion)
	}
}

func TestToProtoGenerations_ProtoJSONRoundTrip(t *testing.T) {
	gen := nativesigil.Generation{
		ID:             "gen-rt-001",
		ConversationID: "conv-rt-abc",
		OperationName:  "generateText",
		Mode:           "SYNC",
		Usage: nativesigil.Usage{
			Input:  1000,
			Output: 500,
		},
	}

	protos := toProtoGenerations([]nativesigil.Generation{gen})
	req := &sigilv1.ExportGenerationsRequest{Generations: protos}

	marshaler := protojson.MarshalOptions{UseProtoNames: true}
	jsonBytes, err := marshaler.Marshal(req)
	if err != nil {
		t.Fatalf("protojson.Marshal: %v", err)
	}

	jsonStr := string(jsonBytes)

	// Must contain snake_case proto names
	if !strings.Contains(jsonStr, `"operation_name"`) {
		t.Errorf("expected 'operation_name' in JSON output, got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"conversation_id"`) {
		t.Errorf("expected 'conversation_id' in JSON output, got: %s", jsonStr)
	}

	// int64 fields must be JSON strings per proto3 JSON spec
	var rawMap map[string]any
	if err := json.Unmarshal(jsonBytes, &rawMap); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	gens, ok := rawMap["generations"].([]any)
	if !ok || len(gens) == 0 {
		t.Fatal("no generations in JSON")
	}
	genObj, ok := gens[0].(map[string]any)
	if !ok {
		t.Fatal("generation is not an object")
	}
	usageObj, ok := genObj["usage"].(map[string]any)
	if !ok {
		t.Fatal("usage is not an object")
	}
	// int64 fields should be serialized as strings in protojson
	if _, isStr := usageObj["input_tokens"].(string); !isStr {
		t.Errorf("usage.input_tokens should be a JSON string (int64), got: %T %v",
			usageObj["input_tokens"], usageObj["input_tokens"])
	}

	// Enum must be the string name, not the integer
	if !strings.Contains(jsonStr, `"GENERATION_MODE_SYNC"`) {
		t.Errorf("expected enum as string 'GENERATION_MODE_SYNC' in JSON, got: %s", jsonStr)
	}
}

func TestToProtoScores_FieldByField(t *testing.T) {
	numVal := 0.92
	createdAt := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)

	sc := nativesigil.Score{
		ScoreID:              "score-001",
		GenerationID:         "gen-001",
		ConversationID:       "conv-abc",
		TraceID:              "trace-xyz",
		SpanID:               "span-001",
		EvaluatorID:          "eval-001",
		EvaluatorVersion:     "1.0.0",
		RuleID:               "rule-001",
		ExperimentID:         "exp-001",
		ScoreKey:             "quality",
		Number:               &numVal,
		HasPassed:            true,
		Passed:               true,
		Explanation:          "looks good",
		Metadata:             map[string]any{"judge_model": "claude-opus-4"},
		CreatedAt:            createdAt,
		Source:               nativesigil.ScoreSource{Kind: "online", ID: "rule-001"},
		TrialID:              "trial-001",
		TestCaseID:           "tc-001",
		GraderConversationID: "grader-conv-001",
		GraderGenerationID:   "grader-gen-001",
		GraderTraceID:        "grader-trace-001",
	}

	protos := toProtoScores([]nativesigil.Score{sc})
	if len(protos) != 1 {
		t.Fatalf("expected 1 proto score, got %d", len(protos))
	}
	ps := protos[0]

	assertStr(t, "score_id", "score-001", ps.GetScoreId())
	assertStr(t, "generation_id", "gen-001", ps.GetGenerationId())
	assertStr(t, "conversation_id", "conv-abc", ps.GetConversationId())
	assertStr(t, "evaluator_id", "eval-001", ps.GetEvaluatorId())
	assertStr(t, "rule_id", "rule-001", ps.GetRuleId())
	assertStr(t, "score_key", "quality", ps.GetScoreKey())
	assertStr(t, "explanation", "looks good", ps.GetExplanation())

	if !ps.GetHasPassed() {
		t.Error("has_passed: want true")
	}
	if !ps.GetPassed() {
		t.Error("passed: want true")
	}

	// ScoreValue Number oneof
	if ps.GetValue() == nil {
		t.Fatal("value is nil")
	}
	if ps.GetValue().GetNumber() != 0.92 {
		t.Errorf("value.number: want 0.92, got %v", ps.GetValue().GetNumber())
	}

	// Source
	if ps.GetSource() == nil {
		t.Fatal("source is nil")
	}
	assertStr(t, "source.kind", "online", ps.GetSource().GetKind())
	assertStr(t, "source.id", "rule-001", ps.GetSource().GetId())

	// Timestamps
	if ps.GetCreatedAt() == nil {
		t.Fatal("created_at is nil")
	}
	if !ps.GetCreatedAt().AsTime().Equal(createdAt) {
		t.Errorf("created_at mismatch: want %v, got %v", createdAt, ps.GetCreatedAt().AsTime())
	}

	// Grader fields
	assertStr(t, "grader_conversation_id", "grader-conv-001", ps.GetGraderConversationId())
	assertStr(t, "grader_generation_id", "grader-gen-001", ps.GetGraderGenerationId())
	assertStr(t, "grader_trace_id", "grader-trace-001", ps.GetGraderTraceId())

	// Metadata structpb
	if ps.GetMetadata() == nil {
		t.Fatal("metadata is nil")
	}
	if ps.GetMetadata().GetFields()["judge_model"].GetStringValue() != "claude-opus-4" {
		t.Errorf("metadata.judge_model: want 'claude-opus-4', got %q",
			ps.GetMetadata().GetFields()["judge_model"].GetStringValue())
	}
}

func TestToProtoScores_BoolValue(t *testing.T) {
	boolVal := true
	sc := nativesigil.Score{
		ScoreID: "score-bool",
		Bool:    &boolVal,
	}
	protos := toProtoScores([]nativesigil.Score{sc})
	if protos[0].GetValue().GetBool() != true {
		t.Errorf("value.bool: want true, got false")
	}
}

func TestToProtoScores_StringValue(t *testing.T) {
	sc := nativesigil.Score{
		ScoreID: "score-str",
		String:  "pass",
	}
	protos := toProtoScores([]nativesigil.Score{sc})
	if protos[0].GetValue().GetString_() != "pass" {
		t.Errorf("value.string: want 'pass', got %q", protos[0].GetValue().GetString_())
	}
}

func TestToProtoWorkflowSteps_FieldByField(t *testing.T) {
	started := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	ended := time.Date(2026, 6, 30, 10, 0, 2, 0, time.UTC)

	step := nativesigil.WorkflowStep{
		ID:                  "step-001",
		ConversationID:      "conv-abc",
		StepName:            "route_request",
		Framework:           "langgraph",
		StartedAt:           started,
		EndedAt:             ended,
		InputState:          map[string]any{"query": "hello"},
		OutputState:         map[string]any{"routed_to": "coding"},
		Error:               "",
		Tags:                map[string]string{"env": "prod"},
		LinkedGenerationIDs: []string{"gen-001"},
		ParentStepIDs:       []string{"step-000"},
		AgentName:           "orchestrator",
		AgentVersion:        "2.0",
		TraceID:             "trace-xyz",
		SpanID:              "span-step-001",
		Metadata:            map[string]any{"framework_version": "0.2.0"},
	}

	protos := toProtoWorkflowSteps([]nativesigil.WorkflowStep{step})
	if len(protos) != 1 {
		t.Fatalf("expected 1 proto workflow step, got %d", len(protos))
	}
	ps := protos[0]

	assertStr(t, "id", "step-001", ps.GetId())
	assertStr(t, "conversation_id", "conv-abc", ps.GetConversationId())
	assertStr(t, "step_name", "route_request", ps.GetStepName())
	assertStr(t, "framework", "langgraph", ps.GetFramework())
	assertStr(t, "agent_name", "orchestrator", ps.GetAgentName())
	assertStr(t, "trace_id", "trace-xyz", ps.GetTraceId())
	assertStr(t, "span_id", "span-step-001", ps.GetSpanId())

	if ps.GetStartedAt() == nil || !ps.GetStartedAt().AsTime().Equal(started) {
		t.Errorf("started_at mismatch")
	}
	if ps.GetCompletedAt() == nil || !ps.GetCompletedAt().AsTime().Equal(ended) {
		t.Errorf("completed_at mismatch")
	}

	if ps.GetInputState() == nil {
		t.Fatal("input_state is nil")
	}
	if ps.GetInputState().GetFields()["query"].GetStringValue() != "hello" {
		t.Errorf("input_state.query: want 'hello'")
	}

	if ps.GetTags()["env"] != "prod" {
		t.Errorf("tags[env]: want 'prod', got %q", ps.GetTags()["env"])
	}
	if len(ps.GetLinkedGenerationIds()) != 1 || ps.GetLinkedGenerationIds()[0] != "gen-001" {
		t.Errorf("linked_generation_ids: want [gen-001], got %v", ps.GetLinkedGenerationIds())
	}
	if len(ps.GetParentStepIds()) != 1 || ps.GetParentStepIds()[0] != "step-000" {
		t.Errorf("parent_step_ids: want [step-000], got %v", ps.GetParentStepIds())
	}
}

// helpers

func assertStr(t *testing.T, field, want, got string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %q, got %q", field, want, got)
	}
}

func assertInt64(t *testing.T, field string, want, got int64) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %d, got %d", field, want, got)
	}
}
