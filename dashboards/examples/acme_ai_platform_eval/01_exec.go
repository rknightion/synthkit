// SPDX-License-Identifier: AGPL-3.0-only

// Exec is the acme_ai_platform_eval Executive Summary dashboard (uid acme-ws2-exec).
// Ported from predecessor acme-exec-eval.json (2026-06-16).
//
// Architectural difference vs acme_ai_platform:
// The gateway is "unlocked" — Portkey exposes a native scrape endpoint so KPIs arrive via
// the connected scrape family (request_count, llm_cost_sum, llm_request_duration_milliseconds_*)
// rather than the 5-min API-poller (portkey_api_*). The Confidential/General split therefore comes
// directly from request_count{env=...} rather than APM spanmetrics, and latency is a real-time
// histogram_quantile on llm_request_duration_milliseconds_bucket rather than a pre-computed
// portkey_api_latency_seconds GAUGE.
//
// Scope rules applied (load-bearing):
//   - request_count, llm_cost_sum, llm_request_duration_milliseconds_*:
//     SUBSTRATE (IntSel) — native gateway scrape, NO blueprint label on the example stack.
//     Predecessor's {scenario="acme_ai_platform_eval"} filter is DROPPED for these families.
//   - aws_bedrock_*, aws_bedrock_agentcore_*:
//     SUBSTRATE (IntSel) — CW metric-stream, NO blueprint label on the example stack.
//   - langsmith_eval_*:
//     SUBSTRATE, acme_ai_platform_eval-scoped via acme.LangsmithSel(false, ...) → project=~".+-gw".
//   - synthkit_content_leak_test (= acme_content_leak_test):
//     APP-scoped (AppSel) — carries blueprint label.
//     real Acme AI stack: acme_content_leak_test{scenario="acme_ai_platform_eval"}
//
// GAPs:
//   - $provider variable: predecessor queries filter by provider=~"$provider" but the predecessor JSON
//     does not declare a provider QueryVariable. Added as a LabelValuesVar on request_count.
//   - Pipeline collector health (p-pipe): predecessor uses up{pipeline=...}; synthkit does not emit
//     a `pipeline` label on `up` — panel included faithfully via IntSel, returns 0 until added.
//   - llm_request_duration_milliseconds_bucket: predecessor uses classic histogram_quantile with `le`
//     bucketed series; builder ClassicHistogramQuantile emits the `_bucket` suffix automatically.
//     The predecessor references the base name without suffix — handled by hand-writing the PromQL
//     since ClassicHistogramQuantile expects a base name (no suffix) and appends _bucket.
//
// Tabs: Overview · Attribution
package acme_ai_platform_eval

import (
	"fmt"

	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// ws2IntSel scopes native gateway scrape families (request_count, llm_*) and substrate CW
// families. These carry NO blueprint label on the example stack — env-scoped only.
// extra is an already-formatted matcher list (no leading comma).
func ws2IntSel(extra string) string {
	if extra != "" {
		return fmt.Sprintf("{env=~\"$env\",%s}", extra)
	}
	return `{env=~"$env"}`
}

// ws2BedrockSel scopes aws_bedrock_* (substrate CW, no blueprint label) by account_id.
func ws2BedrockSel(extra string) string {
	s := `account_id=~"$account"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// Exec builds the Acme AI — Executive (connected-gateway) dashboard (uid acme-ws2-exec).
// Two tabs reproducing the predecessor's layout:
//
//   - Overview     — headline KPI tile strip + Confidential/General split timeseries + tier-health stats
//   - Attribution  — spend + request-rate by use case + latency envelope
func Exec(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws2-exec", "Acme AI — Executive (connected-gateway)")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ────────────────────────────────────────────────────────────────────────────────

	// scenario const var (hidden): blueprint name; app-scoped selectors pin blueprint="$scenario".
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform_eval"))

	// env: label_values from request_count — native gateway scrape, substrate (no blueprint label).
	// Predecessor used request_count{scenario="acme_ai_platform_eval"} — on the example stack drop the scenario filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("env", "Environment",
		`label_values(request_count, env)`))

	// use_case: label_values from request_count — substrate-scoped, no blueprint label.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("use_case", "Use case",
		`label_values(request_count, metadata_use_case)`))

	// provider: label_values from request_count — substrate-scoped. Predecessor queries filter by
	// provider=~"$provider" but the predecessor JSON omits this variable declaration; added here.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("provider", "Provider",
		`label_values(request_count, provider)`))

	// account: label_values from aws_bedrock_invocations_sum — substrate CW, no blueprint label.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("account", "AWS Account (CW)",
		`label_values(aws_bedrock_invocations_sum, account_id)`))

	// ── TAB: Overview — KPI strip ────────────────────────────────────────────────────────────────

	// panel k-req: LLM request rate — native request_count counter (substrate scrape, IntSel).
	// Blue: neutral volume tile — high volume is observed, not alarming.
	dashboard.AddPanel(&d, "kw-req", dashboard.StatTile(
		"LLM request rate", "reqps",
		dashboard.PromTarget(
			fmt.Sprintf(`sum(rate(request_count%s[$__rate_interval]))`,
				ws2IntSel(`provider=~"$provider",metadata_use_case=~"$use_case"`)),
			"req/s")))

	// panel k-err: Gateway error rate (non-2xx) — request_count{code!~"2.."} ratio. Low-is-good.
	dashboard.AddPanel(&d, "kw-err", dashboard.StatTile(
		"Gateway error rate (non-2xx)", "percent",
		dashboard.PromTarget(
			fmt.Sprintf(`100 * sum(rate(request_count%s[$__rate_interval])) / clamp_min(sum(rate(request_count%s[$__rate_interval])), 0.001)`,
				ws2IntSel(`provider=~"$provider",metadata_use_case=~"$use_case",code!~"2.."`),
				ws2IntSel(`provider=~"$provider",metadata_use_case=~"$use_case"`)),
			"error %"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "yellow"},
		dashboard.Threshold{Value: 5, Color: "red"}))

	// panel k-p95: p95 LLM latency (classic histogram_quantile on llm_request_duration_milliseconds_bucket).
	// Low-is-good. Predecessor description: "real-time, no 5-min poll delay" (vs acme_ai_platform's pre-computed GAUGE).
	dashboard.AddPanel(&d, "kw-p95", dashboard.StatTile(
		"p95 LLM latency (histogram)", "ms",
		dashboard.PromTarget(
			fmt.Sprintf(`histogram_quantile(0.95, sum by (le) (rate(llm_request_duration_milliseconds_bucket%s[$__rate_interval])))`,
				ws2IntSel(`provider=~"$provider"`)),
			"p95 ms"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 3000, Color: "yellow"},
		dashboard.Threshold{Value: 8000, Color: "red"}))

	// panel k-spend: Spend in range window — llm_cost_sum gauge delta (cumulative gauge → delta for range).
	// Neutral (budget-dependent threshold). Predecessor: clamp_min(delta(llm_cost_sum[$__range]), 0).
	dashboard.AddPanel(&d, "kw-spend", dashboard.StatTile(
		"Spend (range, gauge delta)", "currencyUSD",
		dashboard.PromTarget(
			fmt.Sprintf(`sum(clamp_min(delta(llm_cost_sum%s[$__range]), 0))`,
				ws2IntSel(`provider=~"$provider"`)),
			"USD")))

	// panel k-faith: Min faithfulness — langsmith_eval_faithfulness_ratio (acme_ai_platform_eval: project=~".+-gw").
	// High-is-good: red→yellow→green (gate >=0.85).
	dashboard.AddPanel(&d, "kw-faith", dashboard.StatTile(
		"Min faithfulness (gate >=0.85)", "percentunit",
		dashboard.PromTarget(
			fmt.Sprintf("min(langsmith_eval_faithfulness_ratio%s)", acme.LangsmithSel(false, `env=~"$env"`)),
			"faithfulness"),
		dashboard.Threshold{Value: 0, Color: "red"},
		dashboard.Threshold{Value: 0.80, Color: "yellow"},
		dashboard.Threshold{Value: 0.85, Color: "green"}))

	// panel k-seal: Content sealed — synthkit_content_leak_test (de-Rochified; AppSel blueprint-scoped).
	// real Acme AI stack: acme_content_leak_test{scenario="acme_ai_platform_eval"}.
	// 0=sealed (green), >=1=breach (red).
	dashboard.AddPanel(&d, "kw-seal", dashboard.StatTile(
		"CONTENT SEALED", "short",
		dashboard.PromTarget(
			fmt.Sprintf("max(%s%s)", acme.MetricContentLeakTest, acme.AppSel("")),
			"leak"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "red"}))

	// ── TAB: Overview — Confidential/General split row ───────────────────────────────────────────

	// panel p-split: Gateway request rate — Confidential vs General, sourced from native request_count{env=...}.
	// The scrape carries the env label directly; env dimension discriminates Confidential vs General.
	// Substrate — no blueprint label.
	dashboard.AddPanel(&d, "conf-split", dashboard.TimeseriesPanel(
		"Gateway request rate — Confidential vs General (native env split)", "reqps",
		dashboard.PromTarget(
			`sum(rate(request_count{env=~"(PRD|TST1|TST2|TRN)"}[$__rate_interval]))`,
			"Confidential (PRD/TST1/TST2/TRN)").RefId("A"),
		dashboard.PromTarget(
			`sum(rate(request_count{env=~"(DEV1|DEV2|BVE)"}[$__rate_interval]))`,
			"General (DEV1/DEV2/BVE)").RefId("B")))

	// panel p-conf-err: Confidential gateway error rate (request_count, non-2xx ratio). Low-is-good.
	dashboard.AddPanel(&d, "conf-err", dashboard.StatTile(
		"Confidential gateway error rate", "percent",
		dashboard.PromTarget(
			`100 * sum(rate(request_count{env=~"(PRD|TST1|TST2|TRN)",code!~"2.."}[$__rate_interval])) / clamp_min(sum(rate(request_count{env=~"(PRD|TST1|TST2|TRN)"}[$__rate_interval])), 0.001)`,
			""),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 0.5, Color: "yellow"},
		dashboard.Threshold{Value: 2, Color: "red"}))

	// panel p-gen-err: General gateway error rate. Low-is-good (relaxed thresholds vs Confidential).
	dashboard.AddPanel(&d, "gen-err", dashboard.StatTile(
		"General gateway error rate", "percent",
		dashboard.PromTarget(
			`100 * sum(rate(request_count{env=~"(DEV1|DEV2|BVE)",code!~"2.."}[$__rate_interval])) / clamp_min(sum(rate(request_count{env=~"(DEV1|DEV2|BVE)"}[$__rate_interval])), 0.001)`,
			""),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 5, Color: "yellow"},
		dashboard.Threshold{Value: 15, Color: "red"}))

	// ── TAB: Overview — Tier health strip ────────────────────────────────────────────────────────

	// panel p-bedrock: Bedrock invocation rate — aws_bedrock_invocations_sum CW _sum gauge ÷ 60.
	// Neutral rate tile (volume, no absolute alarm threshold).
	dashboard.AddPanel(&d, "ct-bedrock", dashboard.StatTile(
		"Bedrock invocation rate", "reqps",
		dashboard.PromTarget(
			fmt.Sprintf("sum(aws_bedrock_invocations_sum%s) / 60", ws2BedrockSel("")),
			"req/s")))

	// panel p-agent: AgentCore sessions — aws_bedrock_agentcore_session_count_sum. Neutral.
	dashboard.AddPanel(&d, "ct-agentcore", dashboard.StatTile(
		"AgentCore sessions", "short",
		dashboard.PromTarget(
			fmt.Sprintf("sum(aws_bedrock_agentcore_session_count_sum%s)", ws2BedrockSel("")),
			"sessions")))

	// panel p-faith-breach: Projects breaching faithfulness gate (acme_ai_platform_eval GW projects). Low-is-good.
	dashboard.AddPanel(&d, "ct-faith-breach", dashboard.StatTile(
		"Projects breaching faithfulness gate", "short",
		dashboard.PromTarget(
			fmt.Sprintf("count(langsmith_eval_faithfulness_ratio%s < 0.85) or vector(0)",
				acme.LangsmithSel(false, `env=~"$env"`)),
			"breaching"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "yellow"},
		dashboard.Threshold{Value: 3, Color: "red"}))

	// panel p-pipe: Pipeline collector health — up{pipeline=...}. High-is-good.
	// GAP: synthkit does not emit a `pipeline` label on `up`; panel included faithfully via IntSel.
	// Will return 0 until a pipeline label is added to synthkit fm/alloy telemetry.
	dashboard.AddPanel(&d, "ct-pipe", dashboard.StatTile(
		"Pipeline collector health", "short",
		dashboard.PromTarget(
			fmt.Sprintf("count(count by (pipeline)(up%s == 1)) or vector(0)", acme.IntSel("")),
			"pipelines up"),
		dashboard.Threshold{Value: 0, Color: "red"},
		dashboard.Threshold{Value: 3, Color: "yellow"},
		dashboard.Threshold{Value: 5, Color: "green"}))

	// ── TAB: Attribution ─────────────────────────────────────────────────────────────────────────

	// panel p-uc-cost: Spend by use case — llm_cost_sum gauge delta per $__rate_interval, top 5.
	// Substrate (IntSel). Predecessor: clamp_min(delta(llm_cost_sum[...]), 0) to suppress negative deltas.
	dashboard.AddPanel(&d, "attr-cost-uc", dashboard.TimeseriesPanel(
		"Spend by use case (gauge delta)", "currencyUSD",
		dashboard.PromTarget(
			fmt.Sprintf(`topk(5, sum by (metadata_use_case) (clamp_min(delta(llm_cost_sum%s[$__rate_interval]), 0)))`,
				ws2IntSel(`provider=~"$provider",metadata_use_case=~"$use_case"`)),
			"{{metadata_use_case}}")))

	// panel p-uc-req: Request rate by use case — native request_count counter, top 5.
	// Substrate (IntSel). Connected-gateway: real-time from scrape (vs acme_ai_platform's 5-min poller window count).
	dashboard.AddPanel(&d, "attr-req-uc", dashboard.TimeseriesPanel(
		"Request rate by use case (native)", "reqps",
		dashboard.PromTarget(
			fmt.Sprintf(`topk(5, sum by (metadata_use_case) (rate(request_count%s[$__rate_interval])))`,
				ws2IntSel(`provider=~"$provider",metadata_use_case=~"$use_case"`)),
			"{{metadata_use_case}}")))

	// panel p-lat-trend: LLM latency trend (p50/p90/p99) — classic histogram_quantile on
	// llm_request_duration_milliseconds_bucket (substrate, IntSel). Predecessor uses _bucket suffix
	// with histogram_quantile(q, sum by (le) (rate(..._bucket[...]))).
	dashboard.AddPanel(&d, "attr-lat-trend", dashboard.TimeseriesPanel(
		"LLM latency trend (p50/p90/p99) — native histogram", "ms",
		dashboard.PromTarget(
			fmt.Sprintf(`histogram_quantile(0.50, sum by (le) (rate(llm_request_duration_milliseconds_bucket%s[$__rate_interval])))`,
				ws2IntSel(`provider=~"$provider"`)),
			"p50").RefId("A"),
		dashboard.PromTarget(
			fmt.Sprintf(`histogram_quantile(0.90, sum by (le) (rate(llm_request_duration_milliseconds_bucket%s[$__rate_interval])))`,
				ws2IntSel(`provider=~"$provider"`)),
			"p90").RefId("B"),
		dashboard.PromTarget(
			fmt.Sprintf(`histogram_quantile(0.99, sum by (le) (rate(llm_request_duration_milliseconds_bucket%s[$__rate_interval])))`,
				ws2IntSel(`provider=~"$provider"`)),
			"p99").RefId("C")))

	// ── Layout (rich: Tabbed/Section/sized-Cells) ────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		// Overview: KPI tile strip → Confidential/General split timeseries + error stats → tier health tiles.
		dashboard.Tabbed("Overview",
			dashboard.Section("Platform KPIs (gateway-native scrape)",
				dashboard.Tile("kw-req"), dashboard.Tile("kw-err"), dashboard.Tile("kw-p95"),
				dashboard.Tile("kw-spend"), dashboard.Tile("kw-faith"), dashboard.Tile("kw-seal"),
			),
			dashboard.Section("Confidential vs General (native env split)",
				// TwoThirds for the split timeseries; two narrow stat tiles for error rates.
				dashboard.TwoThirds("conf-split"),
				dashboard.Stat("conf-err"), dashboard.Stat("gen-err"),
			),
			dashboard.Section("Tier health — click a tile to drill",
				dashboard.Stat("ct-bedrock"), dashboard.Stat("ct-agentcore"),
				dashboard.Stat("ct-faith-breach"), dashboard.Stat("ct-pipe"),
			),
		),
		// Attribution: side-by-side spend + request-rate by use case, then full-width latency trend.
		dashboard.Tabbed("Attribution",
			dashboard.Section("By use case (native + gauge delta)",
				dashboard.Half("attr-cost-uc"), dashboard.Half("attr-req-uc"),
			),
			dashboard.Section("Latency envelope",
				dashboard.Full("attr-lat-trend"),
			),
		),
	)
	return d, nil
}
