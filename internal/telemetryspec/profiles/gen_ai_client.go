// SPDX-License-Identifier: AGPL-3.0-only

package profiles

// gen_ai_client: OpenTelemetry gen_ai semantic-conventions profile.
//
// Covers the two live-reference-empirically-confirmed core client metrics plus a SpanSpec for the
// gen_ai CLIENT span (hand-encoded OTLP seam — the OTel SDK is NOT used on the synthetic path).
//
// Signals sources:
//   - signals/genai.md [slug: genai-metrics]: metric names, label keys, histogram buckets.
//   - signals/genai.md [slug: genai-spans]: span attribute keys + operation values.
//   - internal/genai/genai.go: EXACT constant values for names, label keys, buckets.
//
// Histogram note (SK-35): the two core client metrics are v: ok (live-reference-empirical). Bucket
// arrays come from internal/genai.TokenUsageBuckets / OpDurationBuckets — these are the
// spec's ADVISORY boundaries, not mandated; marked v: assumed per SK-35.
//
// Label key naming: OTLP→Prom mangling maps gen_ai.operation.name → gen_ai_operation_name,
// gen_ai.provider.name → gen_ai_provider_name, gen_ai.token_type → gen_ai_token_type.
// gen_ai.system (old spelling) is NOT emitted — current spelling is gen_ai.provider.name
// (replaced pre-v1.37; see internal/genai AttrProviderName comment).

import "github.com/rknightion/synthkit/internal/telemetryspec"

// genaiF64 is a file-local helper that returns a *float64.
func genaiF64(f float64) *float64 { return &f }

// gaiStr is a file-local helper that returns a *string (for ConstStr value models).
func gaiStr(s string) *string { return &s }

func init() {
	register(telemetryspec.Profile{
		Name: "gen_ai_client",
		Metrics: []telemetryspec.MetricSpec{
			{
				// gen_ai_client_token_usage — histogram, unit {token}.
				// signals/genai.md [slug: genai-metrics]: v: ok (live-reference-empirical); buckets advisory (SK-35).
				// internal/genai: MetricClientTokenUsage, TokenUsageBuckets, LabelOperationName,
				//   LabelProviderName, LabelRequestModel, LabelTokenType.
				Name:       "gen_ai_client_token_usage",
				Instrument: "histogram",
				Unit:       "token",
				Labels: map[string]telemetryspec.ValueModel{
					// gen_ai_operation_name: the gen_ai operation type (OTLP→Prom mangled).
					// signals/genai.md: gen_ai_operation_name ∈ {chat, text_completion, embeddings,
					//   invoke_agent, invoke_workflow, execute_tool, retrieval}.
					// internal/genai: LabelOperationName, Op* constants.
					"gen_ai_operation_name": {Enum: []telemetryspec.EnumEntry{
						{Value: "chat", Weight: 0.55},
						{Value: "text_completion", Weight: 0.15},
						{Value: "embeddings", Weight: 0.15},
						{Value: "invoke_agent", Weight: 0.08},
						{Value: "invoke_workflow", Weight: 0.04},
						{Value: "execute_tool", Weight: 0.02},
						{Value: "retrieval", Weight: 0.01},
					}},
					// gen_ai_provider_name: the LLM provider (CURRENT spelling; NOT gen_ai_system).
					// signals/genai.md: gen_ai_provider_name e.g. azure-openai, gcp-vertex, bedrock.
					// internal/genai: LabelProviderName, AttrProviderName.
					"gen_ai_provider_name": {Enum: []telemetryspec.EnumEntry{
						{Value: "openai", Weight: 0.40},
						{Value: "anthropic", Weight: 0.25},
						{Value: "azure-openai", Weight: 0.20},
						{Value: "bedrock", Weight: 0.10},
						{Value: "gcp-vertex", Weight: 0.05},
					}},
					// gen_ai_token_type: input|output (token_usage dimension only; no "cache" value —
					// cache tokens are span attrs, not a separate token_type).
					// signals/genai.md: gen_ai_token_type: input|output.
					// internal/genai: LabelTokenType, TokenTypeInput, TokenTypeOutput.
					"gen_ai_token_type": {Enum: []telemetryspec.EnumEntry{
						{Value: "input", Weight: 0.60},
						{Value: "output", Weight: 0.40},
					}},
					// gen_ai_request_model: the requested model identifier (OTLP→Prom mangled).
					// §6.1 requires model on gen_ai_client_token_usage (G9 gap — verified live-absent).
					// Bounded to ≈6 representative IDs to keep cross-product cardinality tractable.
					// Source: internal/genai/models.go (exact platform-native IDs, LAW).
					// signals/genai.md [slug: genai-metrics]: gen_ai_request_model label.
					"gen_ai_request_model": {Enum: []telemetryspec.EnumEntry{
						{Value: "gpt-4o", Weight: 0.30},
						{Value: "gpt-4o-mini", Weight: 0.25},
						{Value: "anthropic.claude-sonnet-4-6", Weight: 0.20},
						{Value: "anthropic.claude-haiku-4-5-20251001-v1:0", Weight: 0.12},
						{Value: "text-embedding-3-large", Weight: 0.08},
						{Value: "amazon.nova-lite-v1:0", Weight: 0.05},
					}},
					// NOTE: gen_ai.response.model is OPT-IN on the token-usage metric (OTel gen_ai semconv)
					// and no dashboard groups by it as a metric label; adding it here would cross-product
					// 6× with gen_ai_request_model (mostly impossible request≠response combos) and explode
					// histogram cardinality. §6.1 fallback visibility (request vs response model) lives on
					// the gen_ai CLIENT SPAN below (which carries BOTH request.model and response.model).
				},
				// Token count value: realistic range covering small (embedding/tool) to large
				// (long-context chat) requests; input 50–4000, output 20–800.
				// signals/genai.md: advisory buckets from internal/genai.TokenUsageBuckets.
				Value: telemetryspec.ValueModel{IntRange: &telemetryspec.IntRange{Min: 20, Max: 4096}},
				// Buckets: advisory spec values from internal/genai.TokenUsageBuckets (SK-35).
				// signals/genai.md: buckets advisory→assumed (SK-35).
				Buckets: []float64{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864},
				// LEStyleDotZero: OTLP→Prometheus histogram le label uses "0.0" suffix style.
				LEStyle: "dotzero",
			},
			{
				// gen_ai_client_operation_duration_seconds — histogram, unit seconds.
				// signals/genai.md [slug: genai-metrics]: v: ok (live-reference-empirical); buckets advisory (SK-35).
				// internal/genai: MetricClientOpDuration, OpDurationBuckets.
				Name:       "gen_ai_client_operation_duration_seconds",
				Instrument: "histogram",
				Unit:       "seconds",
				Labels: map[string]telemetryspec.ValueModel{
					// gen_ai_operation_name: operation type.
					// signals/genai.md: gen_ai_operation_name enum (same as token_usage).
					"gen_ai_operation_name": {Enum: []telemetryspec.EnumEntry{
						{Value: "chat", Weight: 0.55},
						{Value: "text_completion", Weight: 0.15},
						{Value: "embeddings", Weight: 0.15},
						{Value: "invoke_agent", Weight: 0.08},
						{Value: "invoke_workflow", Weight: 0.04},
						{Value: "execute_tool", Weight: 0.02},
						{Value: "retrieval", Weight: 0.01},
					}},
					// gen_ai_provider_name: LLM provider.
					// signals/genai.md: gen_ai_provider_name enum.
					"gen_ai_provider_name": {Enum: []telemetryspec.EnumEntry{
						{Value: "openai", Weight: 0.40},
						{Value: "anthropic", Weight: 0.25},
						{Value: "azure-openai", Weight: 0.20},
						{Value: "bedrock", Weight: 0.10},
						{Value: "gcp-vertex", Weight: 0.05},
					}},
					// error_type: present on failures only — modelled with a high p_zero so it rarely
					// appears; when non-zero it's a realistic LLM provider error class.
					// signals/genai.md: error_type (operation_duration on failure ONLY).
					// internal/genai: LabelErrorType, AttrErrorType.
					// ⚠ enum (not Ref) — required for label determinism (-dump); error_type is a
					// bounded class string, not high-card.
					"error_type": {Enum: []telemetryspec.EnumEntry{
						{Value: "timeout", Weight: 0.40},
						{Value: "rate_limit_exceeded", Weight: 0.35},
						{Value: "invalid_request", Weight: 0.15},
						{Value: "server_error", Weight: 0.10},
					}},
					// gen_ai_request_model: requested model identifier — duration is sliced by model
					// in sigil/exec dashboard panels (§6.1, G9). Same bounded set as token_usage.
					// Source: internal/genai/models.go (exact platform-native IDs, LAW).
					// signals/genai.md [slug: genai-metrics]: gen_ai_request_model label.
					"gen_ai_request_model": {Enum: []telemetryspec.EnumEntry{
						{Value: "gpt-4o", Weight: 0.30},
						{Value: "gpt-4o-mini", Weight: 0.25},
						{Value: "anthropic.claude-sonnet-4-6", Weight: 0.20},
						{Value: "anthropic.claude-haiku-4-5-20251001-v1:0", Weight: 0.12},
						{Value: "text-embedding-3-large", Weight: 0.08},
						{Value: "amazon.nova-lite-v1:0", Weight: 0.05},
					}},
					// (gen_ai.response.model omitted on the metric — see token_usage note above.)
				},
				// Duration value: realistic range 0.1–30s covering embedding (fast) to long chat.
				// signals/genai.md: advisory buckets from internal/genai.OpDurationBuckets.
				Value: telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 2.5, Stddev: 1.8}},
				// Buckets: advisory spec values from internal/genai.OpDurationBuckets (SK-35).
				Buckets: []float64{0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92},
				LEStyle: "dotzero",
			},
			{
				// gen_ai_client_operation_time_to_first_chunk_seconds — streaming TTFC histogram.
				// signals/genai.md [slug: genai-metrics]: streaming-only (semconv v1.40.0); v: assumed (SK-35).
				// internal/genai: MetricClientTTFC. Emitted by the gen_ai_client node (the app workload's
				// SDK lane) so the WS1/WS2/WS3 sigil streaming panels populate.
				Name:       "gen_ai_client_operation_time_to_first_chunk_seconds",
				Instrument: "histogram",
				Unit:       "seconds",
				Labels: map[string]telemetryspec.ValueModel{
					"gen_ai_operation_name": {Enum: []telemetryspec.EnumEntry{
						{Value: "chat", Weight: 0.80},
						{Value: "text_completion", Weight: 0.20},
					}},
					"gen_ai_provider_name": {Enum: []telemetryspec.EnumEntry{
						{Value: "openai", Weight: 0.40},
						{Value: "anthropic", Weight: 0.25},
						{Value: "azure-openai", Weight: 0.20},
						{Value: "bedrock", Weight: 0.10},
						{Value: "gcp-vertex", Weight: 0.05},
					}},
				},
				// TTFC: time to the first streamed chunk — sub-second to a few seconds.
				Value:   telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 0.4, Stddev: 0.3}},
				Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
				LEStyle: "dotzero",
			},
			{
				// gen_ai_client_operation_time_per_output_chunk_seconds — inter-chunk latency histogram.
				// signals/genai.md [slug: genai-metrics]: streaming-only (semconv v1.40.0); v: assumed (SK-35).
				// internal/genai: MetricClientTimePerOutputChunk.
				Name:       "gen_ai_client_operation_time_per_output_chunk_seconds",
				Instrument: "histogram",
				Unit:       "seconds",
				Labels: map[string]telemetryspec.ValueModel{
					"gen_ai_operation_name": {Enum: []telemetryspec.EnumEntry{
						{Value: "chat", Weight: 0.80},
						{Value: "text_completion", Weight: 0.20},
					}},
					"gen_ai_provider_name": {Enum: []telemetryspec.EnumEntry{
						{Value: "openai", Weight: 0.40},
						{Value: "anthropic", Weight: 0.25},
						{Value: "azure-openai", Weight: 0.20},
						{Value: "bedrock", Weight: 0.10},
						{Value: "gcp-vertex", Weight: 0.05},
					}},
				},
				// Per-output-chunk: inter-token/chunk latency — tens of milliseconds.
				Value:   telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 0.03, Stddev: 0.02}},
				Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
				LEStyle: "dotzero",
			},
		},
		Spans: []telemetryspec.SpanSpec{
			{
				// gen_ai CLIENT span — the AI hop span emitted by the workload.
				// signals/genai.md [slug: genai-spans]: span name = "{operation} {subject}".
				// internal/genai: SpanName("{op}", "{subject}") pattern; Kind="client" (CLIENT span).
				// internal/workload/webservice/rum.go: browser spans use Kind="client"; AI hops
				//   likewise emit CLIENT spans (the workload is the client of the LLM provider).
				NameTemplate: "{{gen_ai.operation.name}} {{gen_ai.request.model}}",
				Kind:         "client",
				Attributes: map[string]telemetryspec.ValueModel{
					// gen_ai.operation.name: CONST "chat" — the gen_ai CLIENT span is an LLM INFERENCE
					// call, and real inference-call spans are uniformly "chat {model}". The other
					// operation values are NOT inference-call spans: embeddings/text_completion appear
					// only in the METRIC lane (the gen_ai_client_* metric labels above keep the full
					// weighted enum, ~5% embeddings — faithful to the aggregate), and the agent-framework
					// operations (invoke_agent/execute_tool/retrieval/…) are emitted as their OWN spans
					// whose SUBJECT is the agent/tool/datasource name, not the model. Drawing the operation
					// independently of the model ref here produced impossible span names like
					// "embeddings {chat-model}". Source: predecessor tracetree/genai.go — gateway CLIENT spans
					// are always operation="chat"; embeddings is metric-only, never a span name.
					// signals/genai.md [slug: genai-spans].
					"gen_ai.operation.name": {ConstStr: gaiStr("chat")},
					// gen_ai.provider.name: the LLM provider (CURRENT spelling — replaced gen_ai.system).
					// signals/genai.md [slug: genai-spans]: "gen_ai.provider.name (⚠ CURRENT spelling)".
					// internal/genai: AttrProviderName = "gen_ai.provider.name".
					"gen_ai.provider.name": {Ref: "provider"},

					// gen_ai.request.model: the requested model identifier (bounded vocab, not high-card).
					// signals/genai.md [slug: genai-spans]: gen_ai.request.model attr.
					// internal/genai: AttrRequestModel = "gen_ai.request.model".
					"gen_ai.request.model": {Ref: "model"},

					// gen_ai.response.model: the model that actually responded (may differ from request).
					// signals/genai.md [slug: genai-spans]: gen_ai.response.model attr.
					// internal/genai: AttrResponseModel = "gen_ai.response.model".
					"gen_ai.response.model": {Ref: "model"},

					// gen_ai.usage.input_tokens: prompt/input token count.
					// signals/genai.md [slug: genai-spans]: gen_ai.usage.input_tokens attr.
					// internal/genai: AttrInputTokens = "gen_ai.usage.input_tokens".
					"gen_ai.usage.input_tokens": {IntRange: &telemetryspec.IntRange{Min: 50, Max: 4000}},

					// gen_ai.usage.output_tokens: completion/output token count.
					// signals/genai.md [slug: genai-spans]: gen_ai.usage.output_tokens attr.
					// internal/genai: AttrOutputTokens = "gen_ai.usage.output_tokens".
					"gen_ai.usage.output_tokens": {IntRange: &telemetryspec.IntRange{Min: 20, Max: 800}},

					// gen_ai.conversation.id: the conversation / thread ID (high-card ref).
					// signals/genai.md [slug: genai-spans]: gen_ai.conversation.id attr.
					// internal/genai: AttrConversationID = "gen_ai.conversation.id".
					// Not in internal/highcard (not a synthkit canonical correlation field) — use Ref
					// that maps to the session_id correlation slot (session == conversation).
					"gen_ai.conversation.id": {Ref: "session_id"},
				},
			},
		},
	})
}
