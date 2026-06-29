// SPDX-License-Identifier: AGPL-3.0-only

// Bedrock — Acme AI Bedrock Usage (acme-ai-platform blueprint).
//
// AWS Bedrock via CloudWatch metric streams → Mimir. All aws_bedrock_* families are
// SUBSTRATE-SCOPED (no blueprint label) — scope is by account_id + dimension_ModelId
// using raw label matchers. The predecessor's {scenario="$scenario"} filter is DROPPED for
// every aws_bedrock_* query (CW metric-stream series carry no blueprint label on the example stack).
// Per-period CW _sum statistics are GAUGES — /60 gives per-second representation; never
// rate(). _average statistics use avg().
//
// Loki log lane ({source="bedrock_invocation"}) is similarly substrate-scoped: no blueprint
// label on the stream. The predecessor's scenario= stream filter is dropped.
//
// Tabs: Overview · By model · Tokens & Guardrails · Attribution (logs) · Fleet (per account).
package acme_ai_platform

import "github.com/rknightion/synthkit/dashboard"

// bedSel returns a raw label-matcher selector for aws_bedrock_* families (substrate-scoped,
// no blueprint label). Bedrock CW metric streams carry account_id and dimension_ModelId.
// extra is an already-formatted matcher list (no leading comma), e.g.
// `dimension_GuardrailPolicyType=~".*"`.
func bedSel(extra string) string {
	s := `account_id=~"$account",dimension_ModelId=~"$model"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// bedSelNoModel returns a selector scoped only to account_id (for guardrail metrics which
// carry GuardrailArn/GuardrailVersion/GuardrailPolicyType dimensions, not ModelId).
func bedSelNoModel(extra string) string {
	s := `account_id=~"$account"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// bedSelLogDelivery scopes the CW log-delivery families which carry dimension "ModelId" with
// value "Across all model IDs" and no GuardrailArn dimensions — still no blueprint label.
func bedSelLogDelivery() string {
	return `{account_id=~"$account"}`
}

// Bedrock is the Acme AI Bedrock Usage dashboard for the acme-ai-platform blueprint.
// uid: acme-ws1-bedrock. Five tabs reproducing the predecessor's 03-bedrock layout.
func Bedrock(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws1-bedrock", "Acme AI — Bedrock Usage")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ───────────────────────────────────────────────────────────────────────────
	// scenario: ConstVar so the seam-level AppSel helpers keep working for any mixed panel.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform"))
	// account: label_values from aws_bedrock_invocations_sum — substrate, no {scenario=} filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("account", "AWS Account (env)",
		"label_values(aws_bedrock_invocations_sum, account_id)"))
	// model: label_values from aws_bedrock_invocations_sum — substrate, no {scenario=} filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("model", "Model",
		"label_values(aws_bedrock_invocations_sum, dimension_ModelId)"))

	// ── Tab 1: Overview ────────────────────────────────────────────────────────────────────
	// Row: Bedrock KPIs (colored health tiles)
	dashboard.AddPanel(&d, "ov-invocrate", dashboard.StatTile("Invocation rate (avg)", "reqps",
		dashboard.PromTarget(
			"sum(aws_bedrock_invocations_sum"+bedSel("")+") / 60",
			"Invocations/s")))

	dashboard.AddPanel(&d, "ov-errpct", dashboard.StatTile("Error rate (client + server)", "percent",
		dashboard.PromTarget(
			"100 * sum(aws_bedrock_invocation_client_errors_sum"+bedSel("")+
				" + aws_bedrock_invocation_server_errors_sum"+bedSel("")+") / clamp_min(sum(aws_bedrock_invocations_sum"+bedSel("")+"), 1)",
			"error %"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 2, Color: "yellow"}, dashboard.Threshold{Value: 5, Color: "red"}))

	dashboard.AddPanel(&d, "ov-lat", dashboard.StatTile("Avg invocation latency", "ms",
		dashboard.PromTarget(
			"avg(aws_bedrock_invocation_latency_average"+bedSel("")+")",
			"latency (ms)"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 3000, Color: "yellow"}, dashboard.Threshold{Value: 8000, Color: "red"}))

	dashboard.AddPanel(&d, "ov-ttft", dashboard.StatTile("Avg TTFT (streaming)", "ms",
		dashboard.PromTarget(
			"avg(aws_bedrock_time_to_first_token_average"+bedSel("")+")",
			"TTFT (ms)"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 800, Color: "yellow"}, dashboard.Threshold{Value: 2000, Color: "red"}))

	dashboard.AddPanel(&d, "ov-throttle", dashboard.StatTile("Throttle rate", "short",
		dashboard.PromTarget(
			"sum(aws_bedrock_invocation_throttles_sum"+bedSel("")+") / 60",
			"throttles/s"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 0.05, Color: "yellow"}, dashboard.Threshold{Value: 0.5, Color: "red"}))

	dashboard.AddPanel(&d, "ov-guardrate", dashboard.StatTile("Guardrail intervention rate", "percent",
		dashboard.PromTarget(
			"100 * sum(aws_bedrock_guardrails_invocations_intervened_sum"+bedSelNoModel("")+") / clamp_min(sum(aws_bedrock_guardrails_invocations_sum"+bedSelNoModel("")+"), 1)",
			"intervention %")))

	// Row: TPM throttling
	dashboard.AddPanel(&d, "ov-throttle-cond", dashboard.StatTile("Throttled invocations (active)", "short",
		dashboard.PromTarget(
			"sum(aws_bedrock_invocation_throttles_sum"+bedSel("")+") / 60 > 0",
			"throttled invoc/s"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 0.01, Color: "red"}))

	dashboard.AddPanel(&d, "ov-tpmquota", dashboard.TimeseriesPanel("Estimated TPM quota usage", "short",
		dashboard.PromTarget(
			"avg by (dimension_ModelId) (aws_bedrock_estimated_tpmquota_usage_average"+bedSel("")+")",
			"{{dimension_ModelId}}")))

	// Row: Latency, errors & throughput
	dashboard.AddPanel(&d, "ov-invoc-by-model", dashboard.TimeseriesPanel("Invocations by model", "reqps",
		dashboard.PromTarget(
			"sum by (dimension_ModelId) (aws_bedrock_invocations_sum"+bedSel("")+") / 60",
			"{{dimension_ModelId}}")))

	dashboard.AddPanel(&d, "ov-errors-by-model", dashboard.TimeseriesPanel("Client errors & server errors by model", "reqps",
		dashboard.PromTarget(
			"sum by (dimension_ModelId) (aws_bedrock_invocation_client_errors_sum"+bedSel("")+") / 60",
			"client · {{dimension_ModelId}}").RefId("A"),
		dashboard.PromTarget(
			"sum by (dimension_ModelId) (aws_bedrock_invocation_server_errors_sum"+bedSel("")+") / 60",
			"server · {{dimension_ModelId}}").RefId("B")))

	dashboard.AddPanel(&d, "ov-lat-by-model", dashboard.TimeseriesPanel("Invocation latency by model (avg, ms)", "ms",
		dashboard.PromTarget(
			"avg by (dimension_ModelId) (aws_bedrock_invocation_latency_average"+bedSel("")+")",
			"{{dimension_ModelId}}")))

	dashboard.AddPanel(&d, "ov-ttft-by-model", dashboard.TimeseriesPanel("Time-to-First-Token by model (avg, ms) — streaming", "ms",
		dashboard.PromTarget(
			"avg by (dimension_ModelId) (aws_bedrock_time_to_first_token_average"+bedSel("")+")",
			"{{dimension_ModelId}}")))

	// ── Tab 2: By model (repeating stats per $model value) ────────────────────────────────
	// RepeatSection clones this row once per $model value (Grafana row-repeat on dimension_ModelId).
	dashboard.AddPanel(&d, "bm-invoc", dashboard.StatTile("Invocations/s", "reqps",
		dashboard.PromTarget(
			"sum(aws_bedrock_invocations_sum"+bedSel("")+") / 60",
			"Invocations/s")))

	dashboard.AddPanel(&d, "bm-lat", dashboard.StatTile("Invocation latency", "ms",
		dashboard.PromTarget(
			"avg(aws_bedrock_invocation_latency_average"+bedSel("")+")",
			"Invocation latency"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 3000, Color: "yellow"}, dashboard.Threshold{Value: 8000, Color: "red"}))

	dashboard.AddPanel(&d, "bm-ttft", dashboard.StatTile("TTFT (streaming)", "ms",
		dashboard.PromTarget(
			"avg(aws_bedrock_time_to_first_token_average"+bedSel("")+")",
			"TTFT (streaming)"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 800, Color: "yellow"}, dashboard.Threshold{Value: 2000, Color: "red"}))

	dashboard.AddPanel(&d, "bm-guard", dashboard.StatTile("Guardrail interv./s", "short",
		dashboard.PromTarget(
			"sum(aws_bedrock_guardrails_invocations_intervened_sum"+bedSelNoModel("")+") / 60",
			"Guardrail interv./s")))

	// ── Tab 3: Tokens & Guardrails ─────────────────────────────────────────────────────────
	dashboard.AddPanel(&d, "tk-in-out", dashboard.TimeseriesPanel("Input & output tokens by model", "short",
		dashboard.PromTarget(
			"sum by (dimension_ModelId) (aws_bedrock_input_token_count_sum"+bedSel("")+") / 60",
			"in · {{dimension_ModelId}}").RefId("A"),
		dashboard.PromTarget(
			"sum by (dimension_ModelId) (aws_bedrock_output_token_count_sum"+bedSel("")+") / 60",
			"out · {{dimension_ModelId}}").RefId("B")))

	dashboard.AddPanel(&d, "tk-cache", dashboard.TimeseriesPanel("Cache tokens (read & write)", "short",
		dashboard.PromTarget(
			"sum(aws_bedrock_cache_read_input_tokens_sum"+bedSel("")+") / 60",
			"cache read").RefId("A"),
		dashboard.PromTarget(
			"sum(aws_bedrock_cache_write_input_tokens_sum"+bedSel("")+") / 60",
			"cache write").RefId("B")))

	dashboard.AddPanel(&d, "gd-invoc-vs-intervened", dashboard.TimeseriesPanel("Guardrail invocations vs interventions", "reqps",
		dashboard.PromTarget(
			"sum(aws_bedrock_guardrails_invocations_sum"+bedSelNoModel("")+") / 60",
			"guardrail calls").RefId("A"),
		dashboard.PromTarget(
			"sum(aws_bedrock_guardrails_invocations_intervened_sum"+bedSelNoModel("")+") / 60",
			"interventions (blocked/modified)").RefId("B")))

	dashboard.AddPanel(&d, "gd-by-policy", dashboard.TimeseriesPanel("Guardrail interventions by policy type", "reqps",
		dashboard.PromTarget(
			"sum by (dimension_GuardrailPolicyType, dimension_GuardrailContentSource) (aws_bedrock_guardrails_invocations_intervened_sum"+bedSelNoModel("")+") / 60",
			"{{dimension_GuardrailPolicyType}} / {{dimension_GuardrailContentSource}}")))

	dashboard.AddPanel(&d, "gd-lat-textunits", dashboard.TimeseriesPanel("Guardrail latency (avg) + text unit throughput", "ms",
		dashboard.PromTarget(
			"avg(aws_bedrock_guardrails_invocation_latency_average"+bedSelNoModel("")+")",
			"guardrail latency (ms)").RefId("A"),
		dashboard.PromTarget(
			"sum(aws_bedrock_guardrails_text_unit_count_sum"+bedSelNoModel("")+") / 60",
			"text units/s").RefId("B")))

	// ── Tab 4: Attribution (logs) ──────────────────────────────────────────────────────────
	// bedrock_invocation logs are substrate-scoped (no blueprint label). Drop scenario= filter.
	dashboard.AddPanel(&d, "attr-by-usecase", dashboard.TimeseriesPanel("Invocations by use case (from logs)", "short",
		dashboard.LokiTarget(
			`sum by (requestMetadata_use_case) (count_over_time({source="bedrock_invocation"} | json | requestMetadata_use_case!="" [$__auto]))`,
			"{{requestMetadata_use_case}}")))

	dashboard.AddPanel(&d, "attr-by-model", dashboard.TimeseriesPanel("Invocations by model (from logs)", "short",
		dashboard.LokiTarget(
			`sum by (modelId) (count_over_time({source="bedrock_invocation"} | json | __error__="" [$__auto]))`,
			"{{modelId}}")))

	dashboard.AddPanel(&d, "attr-usecase-model-table", dashboard.TablePanel("Use case x model invocation breakdown (range)",
		dashboard.LokiTarget(
			`sum by (requestMetadata_use_case, modelId) (count_over_time({source="bedrock_invocation"} | json | requestMetadata_use_case!="" [$__range]))`,
			"{{requestMetadata_use_case}} x {{modelId}}")))

	dashboard.AddPanel(&d, "attr-logs", dashboard.LogsPanel("Bedrock invocation logs (structured metadata: correlation_id, trace_id)",
		dashboard.LokiTarget(`{source="bedrock_invocation"} | json`, "")))

	// ── Tab 5: Fleet (per account) ─────────────────────────────────────────────────────────
	dashboard.AddPanel(&d, "fl-invoc-by-acct", dashboard.TimeseriesPanel("Invocation rate by account (env)", "reqps",
		dashboard.PromTarget(
			"sum by (account_id) (aws_bedrock_invocations_sum"+bedSel("")+") / 60",
			"{{account_id}}")))

	dashboard.AddPanel(&d, "fl-err-by-acct", dashboard.TimeseriesPanel("Error rate by account (env)", "reqps",
		dashboard.PromTarget(
			"sum by (account_id) (aws_bedrock_invocation_client_errors_sum"+bedSel("")+
				" + aws_bedrock_invocation_server_errors_sum"+bedSel("")+") / 60",
			"{{account_id}}")))

	// Record-path log delivery stat panels (Confidential / Tier-1 audit path)
	dashboard.AddPanel(&d, "fl-cw-delivery-health", dashboard.StatTile("Record-path health: CW delivery", "percent",
		dashboard.PromTarget(
			"100 * sum(aws_bedrock_model_invocation_logs_cloud_watch_delivery_success_sum"+bedSelLogDelivery()+") / clamp_min(sum(aws_bedrock_model_invocation_logs_cloud_watch_delivery_success_sum"+bedSelLogDelivery()+")+sum(aws_bedrock_model_invocation_logs_cloud_watch_delivery_failure_sum"+bedSelLogDelivery()+"), 0.001)",
			"CW delivery %"),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 99, Color: "yellow"}, dashboard.Threshold{Value: 99.9, Color: "green"}))

	dashboard.AddPanel(&d, "fl-s3-delivery-health", dashboard.StatTile("Record-path health: S3 delivery", "percent",
		dashboard.PromTarget(
			"100 * sum(aws_bedrock_model_invocation_logs_s3_delivery_success_sum"+bedSelLogDelivery()+
				" + aws_bedrock_model_invocation_large_data_s3_delivery_success_sum"+bedSelLogDelivery()+") / clamp_min(sum(aws_bedrock_model_invocation_logs_s3_delivery_success_sum"+bedSelLogDelivery()+")+sum(aws_bedrock_model_invocation_logs_s3_delivery_failure_sum"+bedSelLogDelivery()+")+sum(aws_bedrock_model_invocation_large_data_s3_delivery_success_sum"+bedSelLogDelivery()+")+sum(aws_bedrock_model_invocation_large_data_s3_delivery_failure_sum"+bedSelLogDelivery()+"), 0.001)",
			"S3 delivery %"),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 99, Color: "yellow"}, dashboard.Threshold{Value: 99.9, Color: "green"}))

	dashboard.AddPanel(&d, "fl-cw-delivery-ts", dashboard.TimeseriesPanel("CloudWatch log delivery: success vs failure", "short",
		dashboard.PromTarget(
			"sum(aws_bedrock_model_invocation_logs_cloud_watch_delivery_success_sum"+bedSelLogDelivery()+") / 60",
			"CW delivery success").RefId("A"),
		dashboard.PromTarget(
			"sum(aws_bedrock_model_invocation_logs_cloud_watch_delivery_failure_sum"+bedSelLogDelivery()+") / 60",
			"CW delivery FAILURE").RefId("B")))

	dashboard.AddPanel(&d, "fl-s3-delivery-ts", dashboard.TimeseriesPanel("S3 log delivery: success vs failure (incl. large-data)", "short",
		dashboard.PromTarget(
			"sum(aws_bedrock_model_invocation_logs_s3_delivery_success_sum"+bedSelLogDelivery()+") / 60",
			"S3 success").RefId("A"),
		dashboard.PromTarget(
			"sum(aws_bedrock_model_invocation_logs_s3_delivery_failure_sum"+bedSelLogDelivery()+") / 60",
			"S3 FAILURE").RefId("B"),
		dashboard.PromTarget(
			"sum(aws_bedrock_model_invocation_large_data_s3_delivery_success_sum"+bedSelLogDelivery()+") / 60",
			"large-data S3 success").RefId("C"),
		dashboard.PromTarget(
			"sum(aws_bedrock_model_invocation_large_data_s3_delivery_failure_sum"+bedSelLogDelivery()+") / 60",
			"large-data S3 FAILURE").RefId("D")))

	dashboard.WithTabs(&d,
		dashboard.Tabbed("Overview",
			dashboard.Section("Service-level KPIs",
				dashboard.Tile("ov-invocrate"), dashboard.Tile("ov-errpct"), dashboard.Tile("ov-lat"),
				dashboard.Tile("ov-ttft"), dashboard.Tile("ov-throttle"), dashboard.Tile("ov-guardrate")),
			dashboard.Section("Throughput & latency by model",
				dashboard.Half("ov-invoc-by-model"), dashboard.Half("ov-errors-by-model"),
				dashboard.Half("ov-lat-by-model"), dashboard.Half("ov-ttft-by-model")),
			dashboard.Section("Capacity",
				dashboard.Stat("ov-throttle-cond"), dashboard.TwoThirds("ov-tpmquota")),
		),
		dashboard.Tabbed("By model",
			dashboard.RepeatSection("Per-model KPIs — $model", "model",
				dashboard.Tile("bm-invoc"), dashboard.Tile("bm-lat"), dashboard.Tile("bm-ttft"), dashboard.Tile("bm-guard")),
		),
		dashboard.Tabbed("Tokens & Guardrails",
			dashboard.Section("Tokens",
				dashboard.Half("tk-in-out"), dashboard.Half("tk-cache")),
			dashboard.Section("Guardrails",
				dashboard.Half("gd-invoc-vs-intervened"), dashboard.Half("gd-by-policy"),
				dashboard.Full("gd-lat-textunits")),
		),
		dashboard.Tabbed("Attribution (logs)",
			dashboard.Section("Invocation attribution",
				dashboard.Half("attr-by-usecase"), dashboard.Half("attr-by-model"),
				dashboard.Full("attr-usecase-model-table")),
			dashboard.Section("Raw invocation log (correlation_id · trace_id)",
				dashboard.Tall("attr-logs")),
		),
		dashboard.Tabbed("Fleet (per account)",
			dashboard.Section("Record-path delivery health",
				dashboard.Stat("fl-cw-delivery-health"), dashboard.Stat("fl-s3-delivery-health")),
			dashboard.Section("Per-account fleet",
				dashboard.Half("fl-invoc-by-acct"), dashboard.Half("fl-err-by-acct"),
				dashboard.Half("fl-cw-delivery-ts"), dashboard.Half("fl-s3-delivery-ts")),
		),
	)
	return d, nil
}
