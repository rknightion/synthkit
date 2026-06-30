// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import "time"

// Export is the per-flush sink-input envelope for the sigil native-ingest lane. ConvKey
// (= conversation_id) is the delivery-queue shard key so a conversation's generations stay
// ordered. These are synthkit-NATIVE structs (NOT the vendored proto types); the sink maps them
// to sigilv1 proto messages and protojson-POSTs them — mirroring how otlp.Span maps to ResourceSpans.
// This struct set is a FROZEN seam: every lane codes against these names/types.
type Export struct {
	Generations   []Generation
	WorkflowSteps []WorkflowStep
	Scores        []Score
	ConvKey       string // conversation_id of the batch (shard key)
}

// Generation mirrors sigilv1.Generation (generation_ingest.proto). Pointers mark proto `optional`
// scalars (omitted when nil). Token counts are int64 (the proto wire is int64 → protojson string).
type Generation struct {
	ID                  string
	ConversationID      string
	OperationName       string // generateText | streamText (Op* constants)
	Mode                string // SYNC | STREAM (Mode* constants; mapped to the proto enum by the sink)
	TraceID, SpanID     string
	Provider, Model     string // → ModelRef{provider,name}
	ResponseID          string
	ResponseModel       string
	SystemPrompt        string
	Input, Output       []Message
	Tools               []ToolDef
	Usage               Usage
	StopReason          string
	StartedAt, EndedAt  time.Time
	Tags                map[string]string
	Metadata            map[string]any
	AgentName           string
	AgentVersion        string
	MaxTokens           *int64
	Temperature, TopP   *float64
	ToolChoice          *string
	ThinkingEnabled     *bool
	ParentGenerationIDs []string
	EffectiveVersion    string // sha256:<64hex> (see version.go)
	CallError           string
}

// Message is one turn-message in a generation's input/output.
type Message struct {
	Role  string // RoleUser | RoleAssistant | RoleTool
	Name  string
	Parts []Part
}

// Part is a tagged union: exactly one of Text/Thinking/ToolCall/ToolResult is set. ProviderType
// maps to the proto Part.metadata.provider_type (e.g. "tool_call", "thinking", "tool_use").
type Part struct {
	ProviderType string
	Text         string
	Thinking     string
	ToolCall     *ToolCall
	ToolResult   *ToolResult
}

// ToolCall is a Part payload (assistant requests a tool). InputJSON is raw JSON (proto field is
// bytes → protojson base64s it on the wire).
type ToolCall struct {
	ID        string
	Name      string
	InputJSON []byte
}

// ToolResult is a Part payload (tool output, under a MESSAGE_ROLE_TOOL message).
type ToolResult struct {
	ToolCallID  string
	Name        string
	Content     string
	ContentJSON []byte
	IsError     bool
}

// ToolDef mirrors sigilv1.ToolDefinition (the agent's declared tool surface).
type ToolDef struct {
	Name            string
	Description     string
	Type            string // e.g. "function"
	InputSchemaJSON []byte
}

// Usage mirrors sigilv1.TokenUsage. CacheWrite maps the proto cache_write_input_tokens.
type Usage struct {
	Input, Output, Total  int64
	CacheRead, CacheWrite int64
	Reasoning             int64
}

// WorkflowStep mirrors sigilv1.WorkflowStep (non-LLM agentic nodes; general framework agents).
type WorkflowStep struct {
	ID, ConversationID, StepName, Framework string
	StartedAt, EndedAt                      time.Time
	InputState, OutputState                 map[string]any // → google.protobuf.Struct
	Error                                   string
	Tags                                    map[string]string
	LinkedGenerationIDs, ParentStepIDs      []string
	AgentName, AgentVersion                 string
	TraceID, SpanID                         string
	Metadata                                map[string]any
}

// Score mirrors sigilv1.ScoreItem (evaluation_ingest.proto) — field-faithful (review B3). The
// proto has NO agent_name / effective_version. HasPassed (field 12) is distinct from Passed
// (field 13). Number/Bool/String back the ScoreValue oneof (exactly one set).
type Score struct {
	ScoreID, GenerationID, ConversationID                   string
	TraceID, SpanID                                         string
	EvaluatorID, EvaluatorVersion                           string
	RuleID, ExperimentID, ScoreKey                          string
	Number                                                  *float64
	Bool                                                    *bool
	String                                                  string
	HasPassed                                               bool
	Passed                                                  bool
	Explanation                                             string
	Metadata                                                map[string]any
	CreatedAt                                               time.Time
	Source                                                  ScoreSource
	TrialID, TestCaseID                                     string
	GraderConversationID, GraderGenerationID, GraderTraceID string
}

// ScoreSource mirrors sigilv1.ScoreSource (proto field 17).
type ScoreSource struct {
	Kind string
	ID   string
}
