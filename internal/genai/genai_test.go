// SPDX-License-Identifier: AGPL-3.0-only

package genai

import "testing"

func TestSpanName(t *testing.T) {
	cases := []struct{ op, subject, want string }{
		{OpExecuteTool, "search", "execute_tool search"},
		{OpChat, "gpt-4o", "chat gpt-4o"},
		{OpInvokeAgent, "planner", "invoke_agent planner"},
		{OpInvokeWorkflow, "rag-pipeline", "invoke_workflow rag-pipeline"},
		{OpRetrieval, "kb-index", "retrieval kb-index"},
		{OpChat, "", "chat"}, // subject unavailable → operation only
	}
	for _, c := range cases {
		if got := SpanName(c.op, c.subject); got != c.want {
			t.Errorf("SpanName(%q,%q)=%q want %q", c.op, c.subject, got, c.want)
		}
	}
}

// TestOperationValues locks the operation spellings against accidental edits (names are law).
func TestOperationValues(t *testing.T) {
	want := map[string]string{
		"chat": OpChat, "text_completion": OpTextCompletion, "embeddings": OpEmbeddings,
		"generate_content": OpGenerateContent, "create_agent": OpCreateAgent,
		"invoke_agent": OpInvokeAgent, "invoke_workflow": OpInvokeWorkflow,
		"execute_tool": OpExecuteTool, "retrieval": OpRetrieval,
	}
	for lit, got := range want {
		if got != lit {
			t.Errorf("operation const = %q want %q", got, lit)
		}
	}
}

// TestMetricNames locks the OTLP→Prom translated metric names.
func TestMetricNames(t *testing.T) {
	want := map[string]string{
		"gen_ai_client_token_usage":                             MetricClientTokenUsage,
		"gen_ai_client_operation_duration_seconds":              MetricClientOpDuration,
		"gen_ai_client_operation_time_to_first_chunk_seconds":   MetricClientTTFC,
		"gen_ai_client_operation_time_per_output_chunk_seconds": MetricClientTimePerOutputChunk,
		"gen_ai_server_request_duration_seconds":                MetricServerRequestDuration,
		"gen_ai_server_time_to_first_token_seconds":             MetricServerTimeToFirstToken,
		"gen_ai_server_time_per_output_token_seconds":           MetricServerTimePerOutputToken,
	}
	for lit, got := range want {
		if got != lit {
			t.Errorf("metric const = %q want %q", got, lit)
		}
	}
}

// TestBucketsMonotonic guards the advisory bucket arrays are non-empty + strictly increasing.
func TestBucketsMonotonic(t *testing.T) {
	for name, b := range map[string][]float64{"token": TokenUsageBuckets, "opduration": OpDurationBuckets} {
		if len(b) == 0 {
			t.Fatalf("%s buckets empty", name)
		}
		for i := 1; i < len(b); i++ {
			if b[i] <= b[i-1] {
				t.Errorf("%s buckets not increasing at %d: %v <= %v", name, i, b[i], b[i-1])
			}
		}
	}
}

// TestContentStripList: every strip-list entry is a content key, and NONE of the
// attribute-key constants the lib blesses for emission overlaps the content set (so a
// builder using the Attr* constants can never leak content).
func TestContentStripList(t *testing.T) {
	for _, k := range ContentStripList {
		if !IsContentKey(k) {
			t.Errorf("strip-list entry %q not reported as content key", k)
		}
	}
	emittable := []string{
		AttrOperationName, AttrProviderName, AttrRequestModel, AttrResponseModel,
		AttrInputTokens, AttrOutputTokens, AttrReasoningOutputTokens, AttrCacheReadTokens, AttrCacheCreationTokens,
		AttrConversationID, AttrAgentName, AttrToolName, AttrToolCallID, AttrToolType,
		AttrWorkflowName, AttrDataSourceID, AttrOutputType, AttrErrorType,
	}
	for _, k := range emittable {
		if IsContentKey(k) {
			t.Errorf("emittable attribute %q is in the content strip-list — must be disjoint", k)
		}
	}
	if IsContentKey("gen_ai.usage.input_tokens") {
		t.Error("token-usage key must not be content")
	}
	if !IsContentKey("gen_ai.input.messages") {
		t.Error("gen_ai.input.messages must be content")
	}
}
