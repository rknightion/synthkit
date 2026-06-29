// SPDX-License-Identifier: AGPL-3.0-only

package profiles

// gateway_export_log: Portkey export log profile — source=portkey.
//
// Body fields extracted verbatim from internal/workload/webservice/logs.go portkeyExportBody.
// Signals source: signals/portkey.md [slug: portkey-logs].
//
// High-card fields (trace_id, portkey_trace_id) ride as structured metadata via Ref — the
// capability matrix routes them to Loki structured metadata rather than the JSON body (I14/I15).
// ai_model is Ref:"model" because the gateway workload populates ctx.Ref["model"] from
// call.AI.Model (the bounded model-name vocabulary, not high-card — legal label but in body here).
//
// Realism fix (over hardcoded 0/false in current emission code):
//   - retry_count: IntRange{0..3, p_zero=0.9} — occasionally non-zero retries
//   - fallback:    Bool{PTrue:0.03}           — ~3 % of requests trigger fallback routing

import "github.com/rknightion/synthkit/internal/telemetryspec"

// gwStr is a file-local helper that returns a *string.
func gwStr(s string) *string { return &s }

func init() {
	register(telemetryspec.Profile{
		Name: "gateway_export_log",
		Logs: []telemetryspec.LogSpec{
			{
				// source=portkey — the Portkey export log; stream labels are low-card only (I14).
				// signals/portkey.md [slug: portkey-logs]: stream labels {env, context, service_name, level, source}.
				Source: "portkey",
				// Stream labels (env/service_name/cluster/namespace/job/source/level) are auto-stamped
				// by the app workload from the node identity + request outcome — not author-declared.
				Body: map[string]telemetryspec.ValueModel{
					// --- high-card correlation refs (→ structured metadata via IsHighCardRef) ---

					// trace_id: request trace ID — high-card ref → structured metadata.
					// signals/portkey.md [slug: portkey-logs]: "trace_id" body field.
					"trace_id": {Ref: "trace_id"},

					// portkey_trace_id: Portkey golden-thread join key — high-card ref → structured metadata.
					// signals/portkey.md [slug: portkey-logs] + internal/highcard.
					"portkey_trace_id": {Ref: "portkey_trace_id"},

					// --- body fields from portkeyExportBody (logs.go) ---

					// ai_model: the model identifier (e.g. gpt-4o, claude-3-5-sonnet).
					// signals/portkey.md [slug: portkey-logs]: "ai_model" (⚠ NOT "model").
					// Ref:"model" pulls from EvalCtx.Ref["model"] (bounded model vocab, not high-card).
					"ai_model": {Ref: "model"},

					// ai_org: the provider/org string (e.g. openai, anthropic, bedrock).
					// signals/portkey.md [slug: portkey-logs]: "ai_org" (the provider; differs from Prometheus label).
					"ai_org": {Enum: []telemetryspec.EnumEntry{
						{Value: "openai", Weight: 0.45},
						{Value: "anthropic", Weight: 0.30},
						{Value: "bedrock", Weight: 0.15},
						{Value: "azure-openai", Weight: 0.10},
					}},

					// cost: per-request USD cost.
					// signals/portkey.md [slug: portkey-logs]: "cost" body field.
					// Range 0.0001–0.05 covers typical LLM request costs.
					"cost": {FloatRange: &telemetryspec.FloatRange{Min: 0.0001, Max: 0.05}},

					// req_units: input token count.
					// signals/portkey.md [slug: portkey-logs]: "req_units" body field.
					"req_units": {IntRange: &telemetryspec.IntRange{Min: 50, Max: 4000}},

					// res_units: output token count.
					// signals/portkey.md [slug: portkey-logs]: "res_units" body field.
					// Output ≈ 1/3–1/5 of input tokens.
					"res_units": {IntRange: &telemetryspec.IntRange{Min: 20, Max: 800}},

					// response_status_code: HTTP status from the upstream LLM provider.
					// signals/portkey.md [slug: portkey-logs]: "response_status_code" body field.
					"response_status_code": {Enum: []telemetryspec.EnumEntry{
						{Value: "200", Weight: 0.90},
						{Value: "429", Weight: 0.07},
						{Value: "500", Weight: 0.03},
					}},

					// retry_count: number of retries before success/failure.
					// signals/portkey.md [slug: portkey-logs]: "retry_count" Ⓐ (retry/fallback keys, SK-38).
					// Realism fix: was hardcoded 0 in logs.go; now varies (p_zero=0.9 → mostly 0).
					"retry_count": {IntRange: &telemetryspec.IntRange{Min: 0, Max: 3, PZero: 0.90}},

					// fallback: whether fallback routing was triggered.
					// signals/portkey.md [slug: portkey-logs]: "fallback" Ⓐ (SK-38).
					// Realism fix: was hardcoded false in logs.go; now ~3 % true.
					"fallback": {Bool: &telemetryspec.BoolModel{PTrue: 0.03}},
				},
			},
		},
	})
}
