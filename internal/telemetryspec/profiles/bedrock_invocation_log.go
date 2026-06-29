// SPDX-License-Identifier: AGPL-3.0-only

package profiles

// bedrock_invocation_log: AWS Bedrock invocation log profile — source=bedrock_invocation.
//
// Body fields extracted verbatim from internal/workload/webservice/logs.go bedrockInvocationBody.
// Signals source: signals/bedrock.md [slug: bedrock-logs].
//
// ⚠ JSON keys are verbatim from the struct tags:
//   "operation"              — always "InvokeModel" for the current workload
//   "modelId"                — camelCase (AWS CW export schema; NOT model_id)
//   "input_inputTokenCount"  — nested CW schema key
//   "output_outputTokenCount"— nested CW schema key
//
// High-card refs (trace_id, portkey_trace_id) → structured metadata via IsHighCardRef.
// Source string is "bedrock_invocation" (verified in logs.go aiLogStreamLabels call).

import "github.com/rknightion/synthkit/internal/telemetryspec"

// biStr is a file-local string pointer helper (unique prefix "bi" for this file).
func biStr(s string) *string { return &s }

func init() {
	register(telemetryspec.Profile{
		Name: "bedrock_invocation_log",
		Logs: []telemetryspec.LogSpec{
			{
				// source=bedrock_invocation — the Bedrock model invocation log.
				// signals/bedrock.md [slug: bedrock-logs]: "source=bedrock_invocation".
				// Verified in logs.go: streamFor("bedrock_invocation", level).
				Source: "bedrock_invocation",
				// Stream labels (env/service_name/cluster/namespace/job/source/level) are auto-stamped
				// by the app workload from the node identity + request outcome — not author-declared.
				Body: map[string]telemetryspec.ValueModel{
					// --- high-card correlation refs (→ structured metadata via IsHighCardRef) ---

					// trace_id: request trace ID — high-card ref → structured metadata.
					// signals/bedrock.md [slug: bedrock-logs]: "high-card (requestId,correlation_id) → structured metadata".
					"trace_id": {Ref: "trace_id"},

					// portkey_trace_id: Portkey golden-thread join key — high-card ref → structured metadata.
					// internal/highcard.go: portkey_trace_id is in the canonical high-card set.
					"portkey_trace_id": {Ref: "portkey_trace_id"},

					// --- body fields from bedrockInvocationBody (logs.go) ---

					// operation: Bedrock API operation name.
					// signals/bedrock.md [slug: bedrock-logs]: "operation" body field.
					// Verified in logs.go: Operation: "InvokeModel" (the only operation the workload emits).
					"operation": {ConstStr: biStr("InvokeModel")},

					// modelId: Bedrock model identifier — camelCase per AWS CloudWatch export schema.
					// signals/bedrock.md [slug: bedrock-logs]: "modelId" body field.
					// ⚠ camelCase JSON key "modelId" — verbatim from bedrockInvocationBody struct tag.
					"modelId": {Enum: []telemetryspec.EnumEntry{
						{Value: "amazon.titan-text-premier-v1:0", Weight: 0.25},
						{Value: "anthropic.claude-3-5-sonnet-20241022-v2:0", Weight: 0.30},
						{Value: "anthropic.claude-3-7-sonnet-20250219-v1:0", Weight: 0.20},
						{Value: "meta.llama3-70b-instruct-v1:0", Weight: 0.15},
						{Value: "amazon.nova-pro-v1:0", Weight: 0.10},
					}},

					// input_inputTokenCount: input token count from the Bedrock invocation.
					// signals/bedrock.md [slug: bedrock-logs]: "input_inputTokenCount" body field.
					// ⚠ nested schema key — verbatim from bedrockInvocationBody struct tag.
					"input_inputTokenCount": {IntRange: &telemetryspec.IntRange{Min: 50, Max: 4000}},

					// output_outputTokenCount: output token count from the Bedrock invocation.
					// signals/bedrock.md [slug: bedrock-logs]: "output_outputTokenCount" body field.
					// ⚠ nested schema key — verbatim from bedrockInvocationBody struct tag.
					// Output ≈ 1/5–1/3 of input tokens.
					"output_outputTokenCount": {IntRange: &telemetryspec.IntRange{Min: 20, Max: 800}},
				},
			},
		},
	})
}
