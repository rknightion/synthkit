// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/state"
)

// Lane-C metric names. The first two reuse internal/genai (verbatim-matching names); the latter two
// are sigil-lane-specific names sourced VERBATIM from signals/sigil.md [slug: sigil-gen-ai-metrics]
// (live-confirmed m7kni + emea-cloud-demokit 2026-06-30) — never invented.
const (
	metricTokenUsage  = genai.MetricClientTokenUsage // gen_ai_client_token_usage
	metricOpDuration  = genai.MetricClientOpDuration // gen_ai_client_operation_duration_seconds
	metricToolCalls   = "gen_ai_client_tool_calls_per_operation_count"
	metricTimeToFirst = "gen_ai_client_time_to_first_token_seconds"
)

// Lane-C metric label keys. gen_ai.* keys reuse internal/genai (OTLP→Prom mangled); agent_name /
// agent_version are sigil-extensions (signals/sigil.md).
const (
	labelOperationName = genai.LabelOperationName // gen_ai_operation_name
	labelProviderName  = genai.LabelProviderName  // gen_ai_provider_name
	labelRequestModel  = genai.LabelRequestModel  // gen_ai_request_model
	labelTokenType     = genai.LabelTokenType     // gen_ai_token_type
	labelAgentName     = "agent_name"
	labelAgentVersion  = "agent_version"
	labelGenAIToolName = "gen_ai_tool_name"
	labelErrorType     = "error_type"     // operation_duration on failure only (signals/sigil.md)
	labelErrorCategory = "error_category" // operation_duration on failure only (sigil-extended)
)

// toolCallsBuckets / ttfbBuckets are advisory (v: assumed) buckets for the sigil-lane histograms.
// tool-calls-per-operation is a small integer count; TTFT reuses the genai op-duration second
// buckets (matching the internal/genai advisory base-2 second buckets).
var (
	toolCallsBuckets = []float64{0, 1, 2, 4, 8, 16, 32, 64}
	ttfbBuckets      = genai.OpDurationBuckets
)

// accumulate folds one turn's metric observation into the workload-owned state.State (R-M4:
// ProjectBatch accumulates; Tick flushes). Histograms are cumulative across ticks (I3). It observes
// one token_usage point per token type present, the operation duration, the tool-calls-per-op count,
// per-execute_tool durations (with gen_ai_tool_name), and (streaming) the time-to-first-token.
func accumulate(st *state.State, o metricObs) {
	idLabels := map[string]string{
		labelOperationName: o.operation,
		labelProviderName:  o.provider,
		labelRequestModel:  o.model,
		labelAgentName:     o.agentName,
	}
	if o.agentVersion != "" {
		idLabels[labelAgentVersion] = o.agentVersion
	}

	// token_usage: one Observe per token type present (R-M3 token-type values).
	observeToken(st, idLabels, sigil.TokenInput, o.inputTokens)
	observeToken(st, idLabels, sigil.TokenOutput, o.outputTokens)
	observeToken(st, idLabels, sigil.TokenCacheRead, o.cacheReadTokens)
	observeToken(st, idLabels, sigil.TokenCacheWrite, o.cacheWriteTok)
	observeToken(st, idLabels, sigil.TokenReasoning, o.reasoningTokens)

	// operation duration. On a provider call error the series carries error_type/error_category
	// (a DISTINCT series from the success path); absent on success (I13 — never empty-valued labels).
	opLabels := idLabels
	if o.errorType != "" {
		opLabels = cloneLabels(idLabels)
		opLabels[labelErrorType] = o.errorType
		opLabels[labelErrorCategory] = o.errorCategory
	}
	st.Observe(metricOpDuration, opLabels, genai.OpDurationBuckets, state.LEDotZero, o.opDurationSec)

	// tool-calls-per-operation count.
	st.Observe(metricToolCalls, idLabels, toolCallsBuckets, state.LEDotZero, float64(o.toolCalls))

	// per-execute_tool operation duration (+ gen_ai_tool_name label).
	for _, td := range o.toolDurations {
		toolLabels := cloneLabels(idLabels)
		toolLabels[labelOperationName] = sigil.OpExecuteTool
		toolLabels[labelGenAIToolName] = td.name
		st.Observe(metricOpDuration, toolLabels, genai.OpDurationBuckets, state.LEDotZero, td.sec)
	}

	// time-to-first-token (streaming only).
	if o.streaming && o.ttfbSec > 0 {
		st.Observe(metricTimeToFirst, idLabels, ttfbBuckets, state.LEDotZero, o.ttfbSec)
	}
}

// observeToken records one token_usage observation for a token type when the count is > 0 (an absent
// token category is OMITTED, not a zero-valued series — I13). The value is the token COUNT.
func observeToken(st *state.State, idLabels map[string]string, tokenType string, count int64) {
	if count <= 0 {
		return
	}
	labels := cloneLabels(idLabels)
	labels[labelTokenType] = tokenType
	st.Observe(metricTokenUsage, labels, genai.TokenUsageBuckets, state.LEDotZero, float64(count))
}

// cloneLabels returns a shallow copy of a label map (so per-token-type label additions never mutate
// the shared identity map).
func cloneLabels(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}
