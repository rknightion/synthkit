// SPDX-License-Identifier: AGPL-3.0-only

// Exec is the acme_ai_platform Executive Summary dashboard (uid acme-ws1-exec).
// Ported from predecessor 01-exec.json (2026-06-16).
//
// Scope rules applied (load-bearing):
//   - portkey_api_*: SUBSTRATE (IntSel) — no blueprint label; env optional on these gauges.
//   - aws_bedrock_*, aws_bedrock_agentcore_*: BLUEPRINT (AppSel-like with acme.AppSel) — blueprint-scoped CW.
//   - langsmith_eval_*: SUBSTRATE, acme_ai_platform-scoped via acme.LangsmithSel(true, extra).
//   - traces_spanmetrics_*, traces_service_graph_*: metrics-generator, NO blueprint — hand-written selectors.
//   - synthkit_content_leak_test (= acme_content_leak_test): SUBSTRATE, app-emitted; seam constant used.
//
// Panel-35 (pipeline collector health): counts distinct Alloy pipeline lanes via
// up{job="integrations/alloy",pipeline=...} — emitted by the alloyhealth construct,
// substrate-scoped (no blueprint label), scoped by cluster/k8s_cluster_name.
//
// panel-40/41 (fallback rate): predecessor sources llm_fallbacks_total (loki.process-derived counter).
// GAP: synthkit does not emit llm_fallbacks_total. Panel is included with IntSel; will render
// zero until the metric is added. Use portkey_api_rescued_requests_total as a proxy where needed.
package acme_ai_platform

import (
	"fmt"

	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// Exec builds the Acme AI — Executive Summary dashboard (uid acme-ws1-exec).
// Seven tabs mirror the predecessor's row structure:
//
//   - KPI Wall        — six headline stats (requests, error rate, latency, spend, faithfulness, content seal)
//   - Confidential split — Confidential vs General traffic (spanmetrics) + attention panels
//   - Attribution     — per-use-case requests and spend (portkey_api_* gauges)
//   - Cross-Tier      — Bedrock / AgentCore / APM / eval health stats + Bedrock-by-env timeseries
//   - Incidents       — fallback + throttle trend and sparklines, LLM latency quantile trend
//   - AAEF KPIs       — Infinity table: GET ${infinity_base}/acme/aaef_kpis
//   - Service graph   — teaching note + service-graph edge request rates
func Exec(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws1-exec", "Acme AI — Executive Summary")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ────────────────────────────────────────────────────────────────────────────────

	// scenario const var (hidden): app-scoped selectors pin blueprint="$scenario".
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform"))

	// Infinity tables use RELATIVE paths — the Infinity datasource's base URL (the host FQDN,
	// served via tailscale serve) supplies the host. Do NOT embed an absolute base here.

	// env: spanmetrics-derived (substrate, NO blueprint label → drop {scenario=}).
	d.Builder.QueryVariable(dashboard.LabelValuesVar("env", "Environment",
		`label_values(traces_spanmetrics_calls_total, deployment_environment_name)`))

	// use_case: portkey_api_* is substrate-scoped (no blueprint label).
	d.Builder.QueryVariable(dashboard.LabelValuesVar("use_case", "Use case",
		`label_values(portkey_api_requests_total, metadata_use_case)`))

	// account: aws_bedrock_* is blueprint-scoped; drop scenario pin (same blueprint only).
	d.Builder.QueryVariable(dashboard.LabelValuesVar("account", "AWS Account (CW)",
		`label_values(aws_bedrock_invocations_sum, account_id)`))

	// ── TAB: KPI Wall (headline stats) ──────────────────────────────────────────────────────────

	// panel-1: LLM requests (portkey_api_requests_total GAUGE — windowed count, NOT rate())
	// IntSel: substrate (no blueprint label). real Acme AI: portkey_api_requests_total{scenario="$scenario",...}
	// neutral: request count — no thresholds (high volume is simply observed, not alarming by itself).
	dashboard.AddPanel(&d, "kw-requests", dashboard.StatTile(
		"LLM requests (window)", "short",
		dashboard.PromTarget(
			fmt.Sprintf("sum(portkey_api_requests_total%s)", acme.IntSel(`metadata_use_case=~"$use_case"`)),
			"requests")))

	// panel-2: Gateway error rate (portkey_api_error_rate GAUGE 0-1 × 100 → %)
	// IntSel: substrate. Low-is-good: green→yellow→red.
	dashboard.AddPanel(&d, "kw-errpct", dashboard.StatTile(
		"Error rate (non-2xx)", "percent",
		dashboard.PromTarget(
			fmt.Sprintf("100 * max(portkey_api_error_rate%s)", acme.IntSel(`metadata_use_case=~"$use_case"`)),
			"error %"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 2, Color: "yellow"}, dashboard.Threshold{Value: 5, Color: "red"}))

	// panel-3: p90 LLM latency — portkey_api_latency_seconds pre-computed GAUGE (×1000 → ms)
	// IntSel: substrate. No histogram_quantile — this IS the quantile. Low-is-good: green→yellow→red.
	dashboard.AddPanel(&d, "kw-p90lat", dashboard.StatTile(
		`p90 LLM latency (API-pull)`, "ms",
		dashboard.PromTarget(
			fmt.Sprintf(`1000 * max(portkey_api_latency_seconds%s)`, acme.IntSel(`quantile="0.9"`)),
			"p90 ms"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 2000, Color: "yellow"}, dashboard.Threshold{Value: 5000, Color: "red"}))

	// panel-4: Spend (portkey_api_cost_usd GAUGE — windowed)
	// IntSel: substrate. Neutral rate metric — thresholds are budget-dependent; omit for now.
	dashboard.AddPanel(&d, "kw-spend", dashboard.StatTile(
		"Spend (window)", "currencyUSD",
		dashboard.PromTarget(
			fmt.Sprintf("sum(portkey_api_cost_usd%s)", acme.IntSel(`metadata_use_case=~"$use_case"`)),
			"USD")))

	// panel-5: Min faithfulness (langsmith_eval_faithfulness_ratio — substrate, acme_ai_platform scoped)
	// LangsmithSel excludeGW=true keeps acme_ai_platform projects. High-is-good: red→yellow→green (gate >=0.85).
	dashboard.AddPanel(&d, "kw-faithfulness", dashboard.StatTile(
		"Min faithfulness (gate >=0.85)", "percentunit",
		dashboard.PromTarget(
			fmt.Sprintf("min(langsmith_eval_faithfulness_ratio%s)", acme.LangsmithSel(true, `env=~"$env"`)),
			"faithfulness"),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 0.70, Color: "yellow"}, dashboard.Threshold{Value: 0.85, Color: "green"}))

	// panel-6: CONTENT SEALED — synthkit_content_leak_test (de-Rochified seam constant)
	// real Acme AI: acme_content_leak_test. Blueprint-scoped via AppSel (no extra filter).
	// real Acme AI stack: acme_content_leak_test{scenario="$scenario"}
	// 0=sealed (green), >0=breach (red).
	dashboard.AddPanel(&d, "kw-seal", dashboard.StatTile(
		"CONTENT SEALED", "short",
		dashboard.PromTarget(
			fmt.Sprintf("max(%s%s)", acme.MetricContentLeakTest, acme.AppSel("")),
			"leak"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "red"}))

	// ── TAB: Needs attention (conditional panels — alert-level) ─────────────────────────────────

	// panel-attention-faithful: faithfulness breach indicator (substrate, acme_ai_platform)
	// 0=no breach (green), any value=breach (red — low-is-good: breach count).
	dashboard.AddPanel(&d, "attn-faithful", dashboard.StatTile(
		"⚠ Faithfulness gate breach — min ratio below 0.80", "short",
		dashboard.PromTarget(
			fmt.Sprintf("langsmith_eval_faithfulness_ratio%s < 0.8", acme.LangsmithSel(true, `env=~"$env"`)),
			"{{project}} / {{env}}"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 0.001, Color: "red"}))

	// panel-attention-content: content leak breach indicator (blueprint-scoped)
	// real Acme AI: acme_content_leak_test{scenario="$scenario"} > 0
	// 0=clear (green), any positive=breach (red).
	dashboard.AddPanel(&d, "attn-content", dashboard.StatTile(
		"⚠ Content leak BREACH — check pipeline immediately", "short",
		dashboard.PromTarget(
			fmt.Sprintf("%s%s > 0", acme.MetricContentLeakTest, acme.AppSel("")),
			"{{pipeline}}"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "red"}))

	// ── TAB: Confidential split ─────────────────────────────────────────────────────────────────
	// traces_spanmetrics_calls_total: metrics-generator, NO blueprint label.
	// Scope by deployment_environment regex + $env multi-var.

	// panel-10: Confidential vs General combined timeseries
	dashboard.AddPanel(&d, "conf-split", dashboard.TimeseriesPanel(
		`App request rate — Confidential (PRD/TST1/TST2/TRN) vs General (DEV1/DEV2/BVE)`, "reqps",
		dashboard.PromTarget(
			`sum(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"(PRD|TST1|TST2|TRN)"}[$__rate_interval]))`,
			"Confidential (PRD/TST1/TST2/TRN)").RefId("A"),
		dashboard.PromTarget(
			`sum(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"(DEV1|DEV2|BVE)"}[$__rate_interval]))`,
			"General (DEV1/DEV2/BVE)").RefId("B")))

	// panel-11: Confidential request rate stat — neutral (high is good but no absolute threshold).
	dashboard.AddPanel(&d, "conf-rate", dashboard.StatTile(
		"Confidential request rate", "reqps",
		dashboard.PromTarget(
			`sum(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"(PRD|TST1|TST2|TRN)"}[$__rate_interval]))`,
			"")))

	// panel-12: General request rate stat — neutral.
	dashboard.AddPanel(&d, "gen-rate", dashboard.StatTile(
		"General request rate", "reqps",
		dashboard.PromTarget(
			`sum(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"(DEV1|DEV2|BVE)"}[$__rate_interval]))`,
			"")))

	// panel-13: Confidential error rate — STATUS_CODE_ERROR from spanmetrics. Low-is-good.
	dashboard.AddPanel(&d, "conf-errpct", dashboard.StatTile(
		"Confidential error rate", "percent",
		dashboard.PromTarget(
			`100 * sum(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"(PRD|TST1|TST2|TRN)",status_code="STATUS_CODE_ERROR"}[$__rate_interval])) / clamp_min(sum(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"(PRD|TST1|TST2|TRN)"}[$__rate_interval])), 0.001)`,
			""),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "yellow"}, dashboard.Threshold{Value: 5, Color: "red"}))

	// panel-14: General error rate. Low-is-good.
	dashboard.AddPanel(&d, "gen-errpct", dashboard.StatTile(
		"General error rate", "percent",
		dashboard.PromTarget(
			`100 * sum(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"(DEV1|DEV2|BVE)",status_code="STATUS_CODE_ERROR"}[$__rate_interval])) / clamp_min(sum(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"(DEV1|DEV2|BVE)"}[$__rate_interval])), 0.001)`,
			""),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "yellow"}, dashboard.Threshold{Value: 5, Color: "red"}))

	// ── TAB: Attribution ────────────────────────────────────────────────────────────────────────
	// portkey_api_*: substrate (IntSel). These are GAUGES — no rate(), no histogram.

	// panel-20: Requests by use case (portkey_api_requests_total GAUGE timeseries)
	dashboard.AddPanel(&d, "attr-req-uc", dashboard.TimeseriesPanel(
		"Requests by use case (window)", "short",
		dashboard.PromTarget(
			fmt.Sprintf("topk(5, sum by (metadata_use_case) (portkey_api_requests_total%s))",
				acme.IntSel(`metadata_use_case=~"$use_case"`)),
			"{{metadata_use_case}}")))

	// panel-21: Spend by use case (portkey_api_cost_usd GAUGE timeseries)
	dashboard.AddPanel(&d, "attr-spend-uc", dashboard.TimeseriesPanel(
		"Spend by use case (window)", "currencyUSD",
		dashboard.PromTarget(
			fmt.Sprintf("topk(5, sum by (metadata_use_case) (portkey_api_cost_usd%s))",
				acme.IntSel(`metadata_use_case=~"$use_case"`)),
			"{{metadata_use_case}}")))

	// ── TAB: Cross-Tier Health ───────────────────────────────────────────────────────────────────
	// aws_bedrock_*: blueprint-scoped (AppSel). CW _sum is per-period count (60 s) → divide by 60 → req/s.
	// aws_bedrock_agentcore_*: blueprint-scoped.
	// langsmith_eval_*: substrate, acme_ai_platform via LangsmithSel.
	// traces_spanmetrics_calls_total: metrics-generator, no blueprint.
	// up: substrate/IntSel.

	// panel-30: Bedrock invocation rate (aws_bedrock_invocations_sum ÷ 60) — neutral rate.
	dashboard.AddPanel(&d, "ct-bedrock-rate", dashboard.StatTile(
		"Bedrock invocation rate", "reqps",
		dashboard.PromTarget(
			fmt.Sprintf("sum(aws_bedrock_invocations_sum%s) / 60",
				acme.AppSel(`account_id=~"$account"`)),
			"req/s")))

	// panel-31: AgentCore sessions (aws_bedrock_agentcore_session_count_sum — blueprint-scoped) — neutral.
	dashboard.AddPanel(&d, "ct-agentcore-sessions", dashboard.StatTile(
		"AgentCore sessions", "short",
		dashboard.PromTarget(
			fmt.Sprintf("sum(aws_bedrock_agentcore_session_count_sum%s)",
				acme.AppSel(`account_id=~"$account"`)),
			"sessions")))

	// panel-32: Guardrail interventions/s — low-is-good (spikes mean content risk).
	dashboard.AddPanel(&d, "ct-guardrail", dashboard.StatTile(
		"Guardrail interventions/s", "reqps",
		dashboard.PromTarget(
			fmt.Sprintf("sum(aws_bedrock_guardrails_invocations_intervened_sum%s) / 60",
				acme.AppSel(`account_id=~"$account"`)),
			"interventions/s"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 0.1, Color: "yellow"}, dashboard.Threshold{Value: 1, Color: "red"}))

	// panel-33: APM error rate (traces_spanmetrics — metrics-generator, no blueprint). Low-is-good.
	dashboard.AddPanel(&d, "ct-apm-err", dashboard.StatTile(
		"APM error rate", "percent",
		dashboard.PromTarget(
			`100 * sum(rate(traces_spanmetrics_calls_total{status_code="STATUS_CODE_ERROR"}[$__rate_interval])) / clamp_min(sum(rate(traces_spanmetrics_calls_total{status_code!="STATUS_CODE_UNSET"}[$__rate_interval])), 0.001)`,
			""),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "yellow"}, dashboard.Threshold{Value: 5, Color: "red"}))

	// panel-34: Projects breaching faithfulness gate. Low-is-good (0 breaches=green).
	dashboard.AddPanel(&d, "ct-eval-breach", dashboard.StatTile(
		"Projects breaching faithfulness gate", "short",
		dashboard.PromTarget(
			fmt.Sprintf("count(langsmith_eval_faithfulness_ratio%s < 0.85) or vector(0)",
				acme.LangsmithSel(true, `env=~"$env"`)),
			"breaching"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "yellow"}, dashboard.Threshold{Value: 3, Color: "red"}))

	// panel-35: Pipeline collector health — counts distinct Alloy pipeline lanes that are up.
	// Counts Alloy pipeline lanes up (substrate-scoped, no blueprint label).
	// up{job="integrations/alloy",pipeline=...} is emitted by the alloyhealth construct,
	// scoped by cluster/k8s_cluster_name (no env label). High-is-good (more pipelines up=better).
	dashboard.AddPanel(&d, "ct-pipeline-health", dashboard.StatTile(
		"Pipeline collector health", "short",
		dashboard.PromTarget(
			`count(count by (pipeline)(up{job="integrations/alloy"} == 1)) or vector(0)`,
			"pipelines up"),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 1, Color: "yellow"}, dashboard.Threshold{Value: 3, Color: "green"}))

	// panel-60: Bedrock invocations by environment (account_id) timeseries (blueprint-scoped)
	dashboard.AddPanel(&d, "ct-bedrock-env", dashboard.TimeseriesPanel(
		"Bedrock invocations by environment (7-env fan-out)", "reqps",
		dashboard.PromTarget(
			fmt.Sprintf("sum by (account_id) (aws_bedrock_invocations_sum%s) / 60",
				acme.AppSel(`account_id=~"$account"`)),
			"{{account_id}}")))

	// ── TAB: Incidents ──────────────────────────────────────────────────────────────────────────
	// llm_fallbacks_total: GAP — not emitted by synthkit (loki.process-derived in predecessor).
	// Panel ported faithfully with IntSel; renders zero until the metric is added.
	// aws_bedrock_invocation_throttles_sum: blueprint-scoped (AppSel).
	// portkey_api_latency_seconds: substrate GAUGE (IntSel), pre-computed quantile.

	// panel-40: Fallback + Throttle combined timeseries
	dashboard.AddPanel(&d, "inc-fallback-throttle", dashboard.TimeseriesPanel(
		"Fallback rate (log-derived) + Throttle rate (Bedrock)", "reqps",
		// llm_fallbacks_total: GAP — IntSel; substrate env-scoped
		dashboard.PromTarget(
			fmt.Sprintf("sum(rate(llm_fallbacks_total%s[$__rate_interval]))", acme.IntSel(`env=~"$env"`)),
			"fallbacks/s").RefId("A"),
		// aws_bedrock_invocation_throttles_sum: blueprint-scoped
		dashboard.PromTarget(
			fmt.Sprintf("sum(aws_bedrock_invocation_throttles_sum%s) / 60",
				acme.AppSel(`account_id=~"$account"`)),
			"throttles/s").RefId("B")))

	// panel-41: Fallback rate now (GAP metric — see note above). Low-is-good.
	dashboard.AddPanel(&d, "inc-fallback-now", dashboard.StatTile(
		"Fallback rate now", "reqps",
		dashboard.PromTarget(
			fmt.Sprintf("sum(rate(llm_fallbacks_total%s[$__rate_interval]))", acme.IntSel(`env=~"$env"`)),
			""),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 0.01, Color: "yellow"}, dashboard.Threshold{Value: 0.1, Color: "red"}))

	// panel-42: Throttle rate now (aws_bedrock_invocation_throttles_sum ÷ 60). Low-is-good.
	dashboard.AddPanel(&d, "inc-throttle-now", dashboard.StatTile(
		"Throttle rate now", "reqps",
		dashboard.PromTarget(
			fmt.Sprintf("sum(aws_bedrock_invocation_throttles_sum%s) / 60",
				acme.AppSel(`account_id=~"$account"`)),
			""),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 0.05, Color: "yellow"}, dashboard.Threshold{Value: 0.5, Color: "red"}))

	// panel-43: LLM latency trend (portkey_api_latency_seconds GAUGE × 1000 → ms)
	// IntSel: substrate. Pre-computed quantile gauges — NOT histogram_quantile.
	dashboard.AddPanel(&d, "inc-latency-trend", dashboard.TimeseriesPanel(
		"LLM latency trend (p50 / p90 / p99) — latency_spike incident", "ms",
		dashboard.PromTarget(
			fmt.Sprintf("1000 * max(portkey_api_latency_seconds%s)", acme.IntSel(`quantile="0.5"`)),
			"p50").RefId("A"),
		dashboard.PromTarget(
			fmt.Sprintf("1000 * max(portkey_api_latency_seconds%s)", acme.IntSel(`quantile="0.9"`)),
			"p90").RefId("B"),
		dashboard.PromTarget(
			fmt.Sprintf("1000 * max(portkey_api_latency_seconds%s)", acme.IntSel(`quantile="0.99"`)),
			"p99").RefId("C")))

	// ── TAB: AAEF KPIs ──────────────────────────────────────────────────────────────────────────
	// panel-50: AAEF KPIs — Infinity GET ${infinity_base}/acme/aaef_kpis, root_selector=kpis.
	// Columns: context, period, tue_score, mcr_score, spi_score, css_score.
	// Score columns formatted as percentunit (0–1 ratios), 3 dp, colour-text for at-a-glance health.
	dashboard.AddPanel(&d, "aaef-kpis", dashboard.InfinityTablePanel(
		"AAEF KPIs by context × period (TUE / MCR / SPI / CSS) — green >=0.85, yellow >=0.70",
		"A", "/acme/aaef_kpis", "synthkit (Infinity)", "kpis",
		dashboard.Col("context", "Context", "string"),
		dashboard.Col("period", "Period", "string"),
		dashboard.Col("tue_score", "TUE Score", "number").WithUnit("percentunit").WithDecimals(3).WithColorMode("color-text"),
		dashboard.Col("mcr_score", "MCR Score", "number").WithUnit("percentunit").WithDecimals(3).WithColorMode("color-text"),
		dashboard.Col("spi_score", "SPI Score", "number").WithUnit("percentunit").WithDecimals(3).WithColorMode("color-text"),
		dashboard.Col("css_score", "CSS Score", "number").WithUnit("percentunit").WithDecimals(3).WithColorMode("color-text"),
	))

	// ── TAB: Service graph ───────────────────────────────────────────────────────────────────────

	// panel-55: Teaching note text panel (no target)
	dashboard.AddPanel(&d, "sg-note", dashboard.TextPanel(
		"Teaching note: backend→portkey edge is EMPTY under acme_ai_platform — this is CORRECT",
		"### Teaching note: `acme-backend → portkey` edge is EMPTY under `acme_ai_platform` — this is CORRECT\n\n"+
			"Under **`acme_ai_platform`** (API-only architecture), the Portkey gateway does **not** emit a span into "+
			"the OTLP pipeline. It is a **black box bracketed by the app's client span**: the app's backend opens "+
			"a span, calls the Portkey gateway, receives the model response, and closes the span — the gateway hop "+
			"is invisible to the trace.\n\n"+
			"**This is by design, not a gap.** Gateway KPIs (requests/cost/tokens/latency quantiles/error-rate/"+
			"rescued-requests) reach Grafana via **Pipeline 2 — the API poller** pulling the Portkey Analytics API "+
			"every ~5 min. See the 02-Portkey dashboard.\n\n"+
			"**Contrast:** switch to the `acme_ai_platform_eval` scenario and the `acme-backend → portkey` edge "+
			"re-appears — that is the unlock world where Portkey scrape/OTel is granted."))

	// panel-56: Service-graph edge request rates (traces_service_graph_request_total — no blueprint)
	dashboard.AddPanel(&d, "sg-edges", dashboard.TimeseriesPanel(
		"Service-graph edge request rates (all services)", "reqps",
		dashboard.PromTarget(
			`topk(10, sum by (client, server) (rate(traces_service_graph_request_total[$__rate_interval])))`,
			"{{client}} → {{server}}")))

	// ── Layout (rich: Tabbed/Section/sized-Cells) ────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		// KPI Wall: six headline stat tiles (colored background) — at-a-glance executive health strip.
		dashboard.Tabbed("KPI Wall",
			dashboard.Section("Headline KPIs",
				dashboard.Tile("kw-requests"), dashboard.Tile("kw-errpct"), dashboard.Tile("kw-p90lat"),
				dashboard.Tile("kw-spend"), dashboard.Tile("kw-faithfulness"), dashboard.Tile("kw-seal"),
			),
		),
		// Confidential split: attention tiles at top, then the Confidential vs General timeseries hero, then per-env stats.
		dashboard.Tabbed("Confidential split",
			dashboard.Section("Needs attention (alert-level indicators)",
				dashboard.Stat("attn-faithful"), dashboard.Stat("attn-content"),
			),
			dashboard.Section("Confidential vs General traffic",
				dashboard.Full("conf-split"),
			),
			dashboard.Section("Per-tier rates & errors",
				dashboard.Stat("conf-rate"), dashboard.Stat("gen-rate"),
				dashboard.Stat("conf-errpct"), dashboard.Stat("gen-errpct"),
			),
		),
		// Attribution: side-by-side half-width timeseries for requests and spend by use case.
		dashboard.Tabbed("Attribution",
			dashboard.Section("Use-case attribution",
				dashboard.Half("attr-req-uc"), dashboard.Half("attr-spend-uc"),
			),
		),
		// Cross-Tier Health: tile strip of six cross-tier stats, then Bedrock-by-env timeseries.
		dashboard.Tabbed("Cross-Tier Health",
			dashboard.Section("Cross-tier health snapshot",
				dashboard.Tile("ct-bedrock-rate"), dashboard.Tile("ct-agentcore-sessions"), dashboard.Tile("ct-guardrail"),
				dashboard.Tile("ct-apm-err"), dashboard.Tile("ct-eval-breach"), dashboard.Tile("ct-pipeline-health"),
			),
			dashboard.Section("Bedrock by environment",
				dashboard.Full("ct-bedrock-env"),
			),
		),
		// Incidents: fallback+throttle combined timeseries hero, then sparkline stats, then latency trend.
		dashboard.Tabbed("Incidents",
			dashboard.Section("Fallback & throttle trend",
				dashboard.Full("inc-fallback-throttle"),
			),
			dashboard.Section("Now",
				dashboard.Stat("inc-fallback-now"), dashboard.Stat("inc-throttle-now"),
			),
			dashboard.Section("Latency trend",
				dashboard.Full("inc-latency-trend"),
			),
		),
		// AAEF KPIs: Infinity table — tall for comfortable row density.
		dashboard.Tabbed("AAEF KPIs",
			dashboard.Section("",
				dashboard.Tall("aaef-kpis"),
			),
		),
		// Service graph: teaching note then edge rate timeseries.
		dashboard.Tabbed("Service graph",
			dashboard.Section("",
				dashboard.Full("sg-note"), dashboard.Full("sg-edges"),
			),
		),
	)
	return d, nil
}
