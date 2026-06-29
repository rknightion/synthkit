// SPDX-License-Identifier: AGPL-3.0-only

// Package genai is the gen_ai semantic-conventions VOCABULARY mechanic lib — the shared
// home of the OpenTelemetry gen_ai attribute keys, operation values, metric names,
// advisory histogram buckets, metric label keys, and the content strip-list. It is a peer
// to internal/cw (a mechanic lib, not a construct): the workload-AI lane builds gen_ai
// trace-span attributes and gen_ai_client_*/gen_ai_server_* metrics FROM these constants
// and emits them via the existing hand-encoded OTLP + promrw seams. This package emits
// NOTHING itself and imports stdlib only — it is NOT an OTel SDK (the synthetic-path SDK
// ban is unchanged; see ARCHITECTURE §6.1).
//
// Names are LAW. The attribute keys, operation values, and span-name patterns are sourced
// VERBATIM from the OpenTelemetry semantic conventions for generative AI (validated
// 2026-06-15 against open-telemetry/semantic-conventions; the gen_ai families have "Moved"
// to open-telemetry/semantic-conventions-genai, stability "development"). Metric NAMES are
// the deterministic OTLP→Prometheus translation of the gen_ai.client.*/gen_ai.server.*
// instruments. Histogram bucket boundaries are ADVISORY in the spec (not mandated): the
// arrays below are the spec's advisory values, so consumers mark those families v: assumed.
package genai

// Span attribute KEYS (gen_ai semconv). Constants, not values. Content-bearing keys are
// NOT here — they live in ContentStripList (synthkit never emits content).
const (
	AttrOperationName         = "gen_ai.operation.name"
	AttrProviderName          = "gen_ai.provider.name" // current spelling (replaced gen_ai.system pre-v1.37)
	AttrRequestModel          = "gen_ai.request.model"
	AttrResponseModel         = "gen_ai.response.model"
	AttrInputTokens           = "gen_ai.usage.input_tokens"
	AttrOutputTokens          = "gen_ai.usage.output_tokens"
	AttrCacheReadTokens       = "gen_ai.usage.cache_read.input_tokens"
	AttrCacheCreationTokens   = "gen_ai.usage.cache_creation.input_tokens"
	AttrReasoningOutputTokens = "gen_ai.usage.reasoning.output_tokens" // extended-thinking models (§6.1)
	AttrConversationID        = "gen_ai.conversation.id"
	AttrAgentName             = "gen_ai.agent.name"
	AttrToolName              = "gen_ai.tool.name"
	AttrToolCallID            = "gen_ai.tool.call.id"
	AttrToolType              = "gen_ai.tool.type"
	AttrWorkflowName          = "gen_ai.workflow.name" // assumed: invoke_workflow subject carrier (spec thin on workflow attrs)
	AttrDataSourceID          = "gen_ai.data_source.id"
	AttrOutputType            = "gen_ai.output.type"
	AttrErrorType             = "error.type"
)

// Operation-name VALUES for AttrOperationName (validated against the gen_ai.operation.name
// well-known-values enum: chat, create_agent, embeddings, execute_tool, generate_content,
// invoke_agent, invoke_workflow, retrieval, text_completion).
const (
	OpChat            = "chat"
	OpTextCompletion  = "text_completion"
	OpEmbeddings      = "embeddings"
	OpGenerateContent = "generate_content"
	OpCreateAgent     = "create_agent"
	OpInvokeAgent     = "invoke_agent"
	OpInvokeWorkflow  = "invoke_workflow"
	OpExecuteTool     = "execute_tool"
	OpRetrieval       = "retrieval"
)

// SpanName builds the gen_ai span name "{op} {subject}" (e.g. "chat gpt-4o",
// "invoke_agent planner", "execute_tool search"). With an empty subject it is just the
// operation (the spec's fallback when the subject is unavailable).
func SpanName(op, subject string) string {
	if subject == "" {
		return op
	}
	return op + " " + subject
}

// Final-form Mimir metric names (OTLP→Prom translated). Emitted via promrw — NOT the OTel
// metrics SDK (the synthetic-path ban holds).
const (
	// Client instruments (confirmed gen_ai client metrics).
	MetricClientTokenUsage = "gen_ai_client_token_usage"                // histogram, unit {token}
	MetricClientOpDuration = "gen_ai_client_operation_duration_seconds" // histogram, unit s
	// Streaming client histograms (added gen_ai semconv v1.40.0). ASSUMED/advisory —
	// streaming-only, advisory buckets; consumers mark them v: assumed + cantfind. Kept for
	// the streaming-gateway (Portkey) scenario.
	MetricClientTTFC               = "gen_ai_client_operation_time_to_first_chunk_seconds"
	MetricClientTimePerOutputChunk = "gen_ai_client_operation_time_per_output_chunk_seconds"

	// Server instruments (experimental in-spec; OTLP→Prom translated).
	MetricServerRequestDuration    = "gen_ai_server_request_duration_seconds"
	MetricServerTimeToFirstToken   = "gen_ai_server_time_to_first_token_seconds"
	MetricServerTimePerOutputToken = "gen_ai_server_time_per_output_token_seconds"
)

// Advisory histogram buckets (the spec's advisory values; not mandated → v: assumed).
var (
	// TokenUsageBuckets is the advisory bucket set for gen_ai.client.token.usage.
	TokenUsageBuckets = []float64{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864}
	// OpDurationBuckets is the advisory bucket set for gen_ai.client.operation.duration (seconds).
	OpDurationBuckets = []float64{0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92}
)

// Metric label KEYS (the gen_ai.* attribute keys, OTLP→Prom mangled to underscores).
const (
	LabelOperationName = "gen_ai_operation_name"
	LabelProviderName  = "gen_ai_provider_name"
	LabelRequestModel  = "gen_ai_request_model"
	LabelResponseModel = "gen_ai_response_model"
	LabelTokenType     = "gen_ai_token_type" // "input" | "output"
	LabelErrorType     = "error_type"
)

// Token-type label VALUES for LabelTokenType (the gen_ai.client.token.usage dimension).
const (
	TokenTypeInput  = "input"
	TokenTypeOutput = "output"
)

// ContentStripList is every gen_ai attribute key that carries request/response CONTENT.
// synthkit never has content, so these are NEVER emitted; the list documents the ban and
// backs IsContentKey so a builder can assert it never produces one.
var ContentStripList = []string{
	"gen_ai.prompt",     // legacy/event content
	"gen_ai.completion", // legacy/event content
	"gen_ai.input.messages",
	"gen_ai.output.messages",
	"gen_ai.system_instructions",
	"gen_ai.tool.call.arguments",
	"gen_ai.tool.call.result",
	"gen_ai.tool.definitions",
	"gen_ai.retrieval.documents",
	"gen_ai.retrieval.query.text",
	"input.value",  // OpenInference content mirror
	"output.value", // OpenInference content mirror
}

// contentKeys is the ContentStripList as a set for O(1) lookup.
var contentKeys = func() map[string]struct{} {
	m := make(map[string]struct{}, len(ContentStripList))
	for _, k := range ContentStripList {
		m[k] = struct{}{}
	}
	return m
}()

// IsContentKey reports whether key carries request/response content and must never be
// emitted. Emitters guard every attribute through this before stamping.
func IsContentKey(key string) bool {
	_, ok := contentKeys[key]
	return ok
}
