// SPDX-License-Identifier: AGPL-3.0-only

package sigil

// Operation-name VALUES for gen_ai.operation.name (sigil lane). These are the sigil-specific
// operation names observed in live coding-agent and general-agent captures (signals/sigil.md).
// They are distinct from, and used alongside, internal/genai Op* constants.
const (
	OpGenerateText = "generateText" // primary coding + general sync generation
	OpStreamText   = "streamText"   // streaming generation
	OpExecuteTool  = "execute_tool" // tool invocation span (shared with genai.OpExecuteTool)
	OpChat         = "chat"         // openai_v2 provider span (opentelemetry.instrumentation.openai_v2)
	OpEmbeddings   = "embeddings"   // documented for completeness; unused in v1 (R-n3)
)

// Mode VALUES for Generation.Mode (the proto Mode enum).
const (
	ModeSync   = "SYNC"
	ModeStream = "STREAM"
)

// Message-role VALUES for Message.Role (the proto MessageRole enum).
const (
	RoleUser      = "MESSAGE_ROLE_USER"
	RoleAssistant = "MESSAGE_ROLE_ASSISTANT"
	RoleTool      = "MESSAGE_ROLE_TOOL"
)

// Token-type metric label VALUES for gen_ai_token_type (R-m3 pin: exactly these five values,
// NOT the _input_tokens suffix form). Used as the gen_ai_token_type label on
// gen_ai_client_token_usage observations.
const (
	TokenInput      = "input"
	TokenOutput     = "output"
	TokenCacheRead  = "cache_read"
	TokenCacheWrite = "cache_write"
	TokenReasoning  = "reasoning"
)

// Content-capture-mode VALUES for the sigil content_capture_mode metadata key /
// AgentDecl.CaptureMode.
const (
	CaptureFull                  = "full"
	CaptureNoToolContent         = "no_tool_content"
	CaptureMetadataOnly          = "metadata_only"
	CaptureFullWithMetadataSpans = "full_with_metadata_spans"
)

// SDK-name VALUES for the sigil.sdk.name span attribute / metadata key.
const (
	SDKGo     = "sdk-go"
	SDKPython = "sdk-python"
)

// Span attribute KEYS for sigil.* and agent.* attributes (captured from live m7kni + emea stacks,
// 2026-06-30). Every key is verbatim from signals/sigil.md — never invent.
const (
	AttrGenerationID        = "sigil.generation.id"
	AttrSDKName             = "sigil.sdk.name"
	AttrConversationTitle   = "sigil.conversation.title"
	AttrThinkingEnabled     = "sigil.gen_ai.request.thinking.enabled"
	AttrParentGenerationIDs = "sigil.generation.parent_generation_ids"
	AttrAgentAvailableTools = "agent.available_tools"
	AttrAgentPeerAgents     = "agent.peer_agents"
	AttrAgentSystem         = "agent.system"
	AttrAgentUserPrompt     = "agent.user_prompt"
	AttrUserID              = "user.id"

	// TagPrefix is prepended to every entry in Generation.Tags to form a span attribute key,
	// e.g. TagPrefix+"cwd" → "sigil.tag.cwd".
	TagPrefix = "sigil.tag."
)

// sigil-LOCAL cache-token span attribute keys (UNDERSCORE form). These are the keys observed on
// SIGIL SPANS in the live m7kni capture. They deliberately differ from internal/genai's dotted
// keys (genai.AttrCacheReadTokens = "gen_ai.usage.cache_read.input_tokens"). Do NOT reuse the
// genai constants for sigil spans — the two spaces have distinct wire spellings.
const (
	AttrCacheReadInputTokens  = "gen_ai.usage.cache_read_input_tokens"  // sigil span attr (underscore)
	AttrCacheWriteInputTokens = "gen_ai.usage.cache_write_input_tokens" // sigil span attr (underscore)
	AttrReasoningTokens       = "gen_ai.usage.reasoning_tokens"         // sigil span attr (underscore)
)

// ScopeName returns the OTel instrumentation-scope name for a sigil agent span
// (e.g. ScopeName("claude-code") → "sigil.claude-code").
func ScopeName(agent string) string { return "sigil." + agent }
