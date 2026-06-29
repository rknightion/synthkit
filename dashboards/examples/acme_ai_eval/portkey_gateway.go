// SPDX-License-Identifier: AGPL-3.0-only

// PortkeyGateway is the eval_standalone (acme-ai-eval) dashboard for the Portkey LLM Gateway.
// This is the PLATFORM view — the Acme AI Eval team's gateway-health dashboard, reading the
// native /metrics scrape families TENANT-STRIPPED (no use_case/workspace filters):
//
//   - request_count — true Counter → rate()
//   - llm_request_duration_milliseconds — classic histogram (ms) → ClassicHistogramQuantile / heatmap
//   - http_request_duration_seconds — classic histogram (s) → ClassicHistogramQuantile
//   - portkey_request_duration_milliseconds — classic histogram (ms) → ClassicHistogramQuantile
//   - portkey_processing_time_excluding_last_byte_ms — classic histogram (ms) → ClassicHistogramQuantile
//   - llm_token_sum, llm_cost_sum — GAUGES (cumulative; reset on restart) → delta(), NEVER rate()
//   - node_process_cpu_user_seconds_total — Node.js process counter → rate()
//   - node_process_resident_memory_bytes — Node.js process gauge
//   - node_eventloop_lag_seconds — Node.js event-loop lag gauge
//
// ALL native gateway families are SUBSTRATE-SCOPED (no blueprint label) → gwSel (env=~"$env").
// The predecessor's {scenario="acme-ai-eval"} filter is DROPPED on every native gateway query.
//
// Scope by env / provider / model only. Providers are CROSS-CLOUD: azure-openai, gcp-vertex,
// bedrock (note multi-cloud routing — broader provider set than acme_ai_platform_eval).
//
// Predecessor source: acme-eval-portkey-gateway.json. Ported 2026-06-16.
//
// KNOWN GAPS (panels wired faithfully; will be EMPTY until the fill lands):
//   - panel "Retries & fallbacks": llm_retries_total and llm_fallbacks_total are
//     Loki recording-rule-derived counters (loki.process pipeline from source=portkey logs).
//     They are NOT emitted by synthkit's promrw path. Wired as RateExpr against gwSel;
//     will populate once the Spec 4 recording-rule fill lands
//     (see signals/portkey.md [slug: portkey-derived]).
//   - portkey_request_duration_milliseconds: buckets are undocumented (SK-38 v: assumed);
//     panel wired; may return no data.
//
// No Infinity endpoints in this dashboard (native scrape, no API-pull tables).
//
// Tab layout:
//
//	Overview | Latency (histograms) | Cost & cache | Node.js runtime | Gateway logs
package acme_ai_eval

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"

	dashboardv2 "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// gwSel scopes a native Portkey gateway family (substrate; carries app/env/provider/code/model/
// cacheStatus labels; NO blueprint label) by env + optional extra matchers.
// extra is an already-formatted matcher list (no leading comma), e.g.
// `provider=~"$provider",model=~"$model"`.
//
// Thin local helper delegating env-scoping to acme.IntSel. Named gwSel to avoid collision
// with any other file in the package.
func gwSel(extra string) string {
	return acme.IntSel(extra)
}

// PortkeyGateway builds the AI Evaluation — Portkey Gateway dashboard (uid acme-eval-portkey-gateway).
//
// Native scrape families: request_count (Counter), llm_request_duration_milliseconds (histogram),
// http_request_duration_seconds (histogram), portkey_request_duration_milliseconds (histogram),
// portkey_processing_time_excluding_last_byte_ms (histogram), llm_token_sum (GAUGE),
// llm_cost_sum (GAUGE), node_process_* runtime.
//
// All native families: gwSel (substrate; no blueprint). delta() for GAUGE families.
func PortkeyGateway(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-eval-portkey-gateway", "AI Evaluation — Portkey Gateway")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ────────────────────────────────────────────────────────────────────────────────

	// scenario: hidden const var (blueprint name for eval_standalone).
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme-ai-eval"))

	// env: substrate-scoped; source from request_count (DROP scenario= — substrate family).
	// The 8 AI Evaluation env cells: acme-eval-dev-eus/weu/sea/aue, acme-eval-prd-eus/weu/sea/aue.
	d.Builder.CustomVariable(dashboard.EnvVar(m))

	// provider: label on all native gateway families → substrate, no {scenario=} filter.
	// Cross-cloud: azure-openai, gcp-vertex, bedrock (multi-cloud routing).
	d.Builder.QueryVariable(dashboard.LabelValuesVar("provider", "Provider",
		`label_values(request_count, provider)`))

	// model: label on request_count and llm_* families → substrate, no {scenario=} filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("model", "Model",
		`label_values(request_count, model)`))

	// ── TAB: Overview ────────────────────────────────────────────────────────────────────────────

	// KPI tile: total request rate (predecessor panel kpi-rate). request_count Counter → rate().
	dashboard.AddPanel(&d, "kpi-rate", dashboard.StatTile(
		"Request rate", "reqps",
		dashboard.PromTarget(
			`sum(rate(request_count`+
				gwSel(`provider=~"$provider",model=~"$model"`)+
				`[$__rate_interval]))`,
			"req/s")))

	// KPI tile: error rate (non-2xx). Low-is-good → green → yellow → red.
	// (predecessor panel kpi-err)
	dashboard.AddPanel(&d, "kpi-err", dashboard.StatTile(
		"Error rate (non-2xx)", "percent",
		dashboard.PromTarget(
			`100 * sum(rate(request_count`+
				gwSel(`provider=~"$provider",model=~"$model",code!~"2.."`)+
				`[$__rate_interval])) / clamp_min(sum(rate(request_count`+
				gwSel(`provider=~"$provider",model=~"$model"`)+
				`[$__rate_interval])), 0.001)`,
			"error %"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "yellow"},
		dashboard.Threshold{Value: 5, Color: "red"}))

	// KPI tile: p99 LLM round-trip (predecessor panel kpi-p99 — acme-ai-eval uses p99, not p95 like acme_ai_platform_eval).
	// Low-is-good → green → yellow → red.
	dashboard.AddPanel(&d, "kpi-p99", dashboard.StatTile(
		"p99 LLM round-trip (ms)", "ms",
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.99, "llm_request_duration_milliseconds",
				gwSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p99 ms"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 3000, Color: "yellow"},
		dashboard.Threshold{Value: 8000, Color: "red"}))

	// KPI tile: cache-hit ratio. High-is-good → red → yellow → green.
	// (predecessor panel kpi-cache)
	dashboard.AddPanel(&d, "kpi-cache", dashboard.StatTile(
		"Cache-hit ratio", "percent",
		dashboard.PromTarget(
			`100 * sum(rate(request_count`+
				gwSel(`provider=~"$provider",cacheStatus="hit"`)+
				`[$__rate_interval])) / clamp_min(sum(rate(request_count`+
				gwSel(`provider=~"$provider"`)+
				`[$__rate_interval])), 0.001)`,
			"cache hit %"),
		dashboard.Threshold{Value: 0, Color: "red"},
		dashboard.Threshold{Value: 20, Color: "yellow"},
		dashboard.Threshold{Value: 50, Color: "green"}))

	// KPI tile: token throughput over range (delta of cumulative GAUGE llm_token_sum — never rate()).
	// (predecessor panel kpi-tokens)
	dashboard.AddPanel(&d, "kpi-tokens", dashboard.StatTile(
		"Token throughput (range, gauge delta)", "short",
		dashboard.PromTarget(
			`sum(clamp_min(delta(llm_token_sum`+
				gwSel(`provider=~"$provider"`)+
				`[$__range]), 0))`,
			"tokens")))

	// KPI tile: spend over range (delta of cumulative GAUGE llm_cost_sum — never rate()).
	// (predecessor panel kpi-cost)
	dashboard.AddPanel(&d, "kpi-cost", dashboard.StatTile(
		"Spend (range, gauge delta)", "currencyUSD",
		dashboard.PromTarget(
			`sum(clamp_min(delta(llm_cost_sum`+
				gwSel(`provider=~"$provider"`)+
				`[$__range]), 0))`,
			"USD")))

	// Timeseries: request rate by HTTP code (predecessor panel ts-rate-code; stacked).
	dashboard.AddPanel(&d, "ts-rate-code", dashboard.TimeseriesPanel(
		"Request rate by HTTP status code", "reqps",
		dashboard.PromTarget(
			`sum by (code) (rate(request_count`+
				gwSel(`provider=~"$provider",model=~"$model"`)+
				`[$__rate_interval]))`,
			"{{code}}")))

	// Timeseries: request rate by provider (predecessor panel ts-rate-provider; stacked).
	// Cross-cloud providers: azure-openai, gcp-vertex, bedrock.
	dashboard.AddPanel(&d, "ts-rate-provider", dashboard.TimeseriesPanel(
		"Request rate by provider (azure-openai · gcp-vertex · bedrock)", "reqps",
		dashboard.PromTarget(
			`sum by (provider) (rate(request_count`+
				gwSel(`model=~"$model"`)+
				`[$__rate_interval]))`,
			"{{provider}}")))

	// Timeseries: p50/p95/p99 LLM latency overview (predecessor panel ts-lat — Overview row).
	dashboard.AddPanel(&d, "ts-lat-overview", dashboard.TimeseriesPanel(
		"LLM round-trip latency — p50/p95/p99 (ms)", "ms",
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.50, "llm_request_duration_milliseconds",
				gwSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p50").RefId("A"),
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.95, "llm_request_duration_milliseconds",
				gwSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p95").RefId("B"),
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.99, "llm_request_duration_milliseconds",
				gwSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p99").RefId("C")))

	// Timeseries: gateway HTTP cycle p95 in seconds (predecessor panel ts-http).
	// http_request_duration_seconds is a classic histogram, unit = seconds.
	dashboard.AddPanel(&d, "ts-http", dashboard.TimeseriesPanel(
		"Gateway HTTP cycle p95 (seconds)", "s",
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.95, "http_request_duration_seconds",
				gwSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p95")))

	// Timeseries: full gateway processing p95 (predecessor panel ts-portkey-dur).
	// portkey_request_duration_milliseconds (ms). KNOWN GAP: buckets undocumented (SK-38 v: assumed).
	dashboard.AddPanel(&d, "ts-portkey-dur", dashboard.TimeseriesPanel(
		"Gateway processing p95 — portkey_request_duration_milliseconds (ms, KNOWN GAP: assumed buckets)", "ms",
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.95, "portkey_request_duration_milliseconds",
				gwSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p95")))

	// ── TAB: Latency (histograms) ─────────────────────────────────────────────────────────────────

	// Timeseries: p50/p95/p99 LLM round-trip (dedicated Latency tab panel).
	dashboard.AddPanel(&d, "lat-quantiles", dashboard.TimeseriesPanel(
		"LLM round-trip latency — histogram_quantile p50/p95/p99 (ms)", "ms",
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.50, "llm_request_duration_milliseconds",
				gwSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p50").RefId("A"),
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.95, "llm_request_duration_milliseconds",
				gwSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p95").RefId("B"),
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.99, "llm_request_duration_milliseconds",
				gwSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p99").RefId("C")))

	// Heatmap: LLM round-trip latency distribution.
	// Source: llm_request_duration_milliseconds classic histogram → pre-bucketed.
	dashboard.AddPanel(&d, "lat-heat", dashboard.HeatmapPanel(
		"LLM round-trip latency heatmap (ms)", "ms",
		dashboard.PromTarget(
			`sum by (le) (rate(llm_request_duration_milliseconds_bucket`+
				gwSel(`provider=~"$provider"`)+
				`[$__rate_interval]))`,
			"{{le}}")))

	// Timeseries: gateway overhead — portkey_processing_time_excluding_last_byte_ms p95.
	// Confirmed in signals/portkey.md (v: ok) — gateway overhead excl. streaming final byte.
	dashboard.AddPanel(&d, "lat-overhead-excl", dashboard.TimeseriesPanel(
		"Gateway overhead excl. last byte — portkey_processing_time_excluding_last_byte_ms p95 (ms)", "ms",
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.95, "portkey_processing_time_excluding_last_byte_ms",
				gwSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p95 (excl. last byte)")))

	// Timeseries: provider latency breakdown p95 (by provider — cross-cloud routing view).
	dashboard.AddPanel(&d, "lat-by-provider", dashboard.TimeseriesPanel(
		"LLM round-trip p95 by provider (azure-openai · gcp-vertex · bedrock)", "ms",
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.95, "llm_request_duration_milliseconds",
				gwSel(`provider=~"$provider"`),
				[]string{"provider", "le"}),
			"p95 · {{provider}}")))

	// ── TAB: Cost & cache ─────────────────────────────────────────────────────────────────────────

	// Timeseries: token throughput by provider (predecessor panel ts-tokens).
	// llm_token_sum is a cumulative GAUGE → delta(), never rate().
	dashboard.AddPanel(&d, "cc-tokens", dashboard.TimeseriesPanel(
		"Token throughput by provider (gauge delta)", "short",
		dashboard.PromTarget(
			`sum by (provider) (clamp_min(delta(llm_token_sum`+
				gwSel(`provider=~"$provider"`)+
				`[$__rate_interval]), 0))`,
			"{{provider}}")))

	// Timeseries: cost by provider (predecessor panel ts-cost).
	// llm_cost_sum is a cumulative GAUGE → delta(), never rate().
	dashboard.AddPanel(&d, "cc-cost", dashboard.TimeseriesPanel(
		"Cost by provider (gauge delta)", "currencyUSD",
		dashboard.PromTarget(
			`sum by (provider) (clamp_min(delta(llm_cost_sum`+
				gwSel(`provider=~"$provider"`)+
				`[$__rate_interval]), 0))`,
			"{{provider}}")))

	// Table: tokens + cost by model over range (2-leg merge; delta over range gives totals per model).
	// llm_token_sum and llm_cost_sum are cumulative GAUGEs → delta(), never rate().
	dashboard.AddPanel(&d, "cc-cost-model", dashboard.MergeTablePanel(
		"Tokens & cost by model (range, gauge delta)",
		[]*dashboardv2.TargetBuilder{
			dashboard.PromTableTarget(
				`sum by (model) (clamp_min(delta(llm_token_sum`+
					gwSel(`provider=~"$provider",model=~"$model"`)+
					`[$__range]), 0))`,
				"A"),
			dashboard.PromTableTarget(
				`sum by (model) (clamp_min(delta(llm_cost_sum`+
					gwSel(`provider=~"$provider",model=~"$model"`)+
					`[$__range]), 0))`,
				"B"),
		},
		dashboard.OrganizeOptions{
			Exclude: []string{"Time"},
			Rename: map[string]string{
				"Value #A": "Tokens",
				"Value #B": "Cost (USD)",
			},
		},
	))

	// Timeseries: cache status breakdown (predecessor panel ts-cache).
	dashboard.AddPanel(&d, "cc-cache", dashboard.TimeseriesPanel(
		"Cache status breakdown (cacheStatus ∈ hit/miss/disabled/error)", "reqps",
		dashboard.PromTarget(
			`sum by (cacheStatus) (rate(request_count`+
				gwSel(`provider=~"$provider"`)+
				`[$__rate_interval]))`,
			"{{cacheStatus}}")))

	// Timeseries: retries & fallbacks (predecessor panel ts-retries).
	// llm_retries_total and llm_fallbacks_total are Loki recording-rule-derived series
	// (count_over_time([5m]) → instant GAUGE evaluated per scrape, NOT a cumulative counter).
	// Query the recorded series DIRECTLY — rate() is WRONG for this family.
	// KNOWN GAP: NOT emitted by synthkit's promrw path; will be empty until fill lands.
	// (see signals/portkey.md [slug: portkey-derived])
	dashboard.AddPanel(&d, "cc-retries", dashboard.TimeseriesPanel(
		"Retries & fallbacks (recording-rule-derived from Portkey logs — KNOWN GAP)", "short",
		dashboard.PromTarget(
			`sum by (provider) (llm_retries_total`+gwSel(`provider=~"$provider"`)+`)`,
			"retries · {{provider}}").RefId("A"),
		dashboard.PromTarget(
			`sum by (provider) (llm_fallbacks_total`+gwSel(`provider=~"$provider"`)+`)`,
			"fallbacks · {{provider}}").RefId("B")))

	// ── TAB: Node.js runtime ─────────────────────────────────────────────────────────────────────

	// Timeseries: Node.js CPU user-time rate by pod (predecessor panel node-cpu).
	// node_process_cpu_user_seconds_total — Node.js process counter (NOT node-exporter).
	// The predecessor uses this metric per gateway pod (instance label); no env filter on predecessor
	// but IntSel keeps it scoped to env so it stays consistent with the rest of the dashboard.
	dashboard.AddPanel(&d, "node-cpu", dashboard.TimeseriesPanel(
		"Node.js CPU user-time rate by pod (node_process_cpu_user_seconds_total)", "short",
		dashboard.PromTarget(
			`sum by (instance) (rate(node_process_cpu_user_seconds_total`+
				gwSel(``)+
				`[$__rate_interval]))`,
			"{{instance}}")))

	// Timeseries: Node.js resident memory by pod (predecessor panel node-mem).
	// node_process_resident_memory_bytes — Node.js process gauge (NOT node-exporter).
	dashboard.AddPanel(&d, "node-mem", dashboard.TimeseriesPanel(
		"Node.js resident memory by pod (node_process_resident_memory_bytes)", "bytes",
		dashboard.PromTarget(
			`sum by (instance) (node_process_resident_memory_bytes`+
				gwSel(``)+
				`)`,
			"{{instance}}")))

	// Timeseries: event-loop lag by pod (predecessor panel node-lag).
	// node_eventloop_lag_seconds — spikes indicate gateway overload (Node.js process gauge).
	dashboard.AddPanel(&d, "node-lag", dashboard.TimeseriesPanel(
		"Event-loop lag by pod — spikes indicate overload (node_eventloop_lag_seconds)", "s",
		dashboard.PromTarget(
			`max by (instance) (node_eventloop_lag_seconds`+
				gwSel(``)+
				`)`,
			"{{instance}}")))

	// ── TAB: Gateway logs ─────────────────────────────────────────────────────────────────────────

	// Text: data contract and context (platform team view).
	dashboard.AddPanel(&d, "logs-intro", dashboard.TextPanel(
		"Gateway platform view — data contract",
		"## AI Evaluation — Portkey Gateway (eval_standalone)\n\n"+
			"The Portkey self-hosted gateway **native `/metrics` scrape**, tenant-stripped (no use_case/workspace filters). "+
			"Scope by **env** (acme-eval-dev/prd-eus/weu/sea/aue), **provider** (azure-openai · gcp-vertex · bedrock), and **model**.\n\n"+
			"**Metric type discipline:** `request_count` is a true **Counter** → `rate()`. "+
			"`llm_token_sum`/`llm_cost_sum` are cumulative **GAUGES** → `delta()`, never `rate()`. "+
			"Latency families (`llm_request_duration_milliseconds`, `http_request_duration_seconds`, "+
			"`portkey_request_duration_milliseconds`, `portkey_processing_time_excluding_last_byte_ms`) "+
			"are **classic histograms** → `histogram_quantile()` + heatmaps.\n\n"+
			"**KNOWN GAPS:** `llm_retries_total`/`llm_fallbacks_total` are Loki recording-rule-derived "+
			"(not emitted by synthkit promrw). `portkey_request_duration_milliseconds` buckets are "+
			"undocumented (SK-38 v: assumed)."))

	// Logs: Portkey gateway log stream (substrate; no blueprint/scenario stream label).
	// Drop predecessor's scenario= filter — Loki substrate stream carries no scenario stream label.
	dashboard.AddPanel(&d, "logs-portkey", dashboard.LogsPanel(
		`Portkey gateway logs — real-time stream (source=portkey, service_name=portkey)`,
		dashboard.LokiTarget(
			`{source="portkey",service_name="portkey"} | json`,
			"")))

	// ── Layout ───────────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("Overview",
			dashboard.Section("Service-level KPIs",
				dashboard.Tile("kpi-rate"), dashboard.Tile("kpi-err"), dashboard.Tile("kpi-p99"),
				dashboard.Tile("kpi-cache"), dashboard.Tile("kpi-tokens"), dashboard.Tile("kpi-cost")),
			dashboard.Section("Traffic by code & provider",
				dashboard.Half("ts-rate-code"), dashboard.Half("ts-rate-provider")),
			dashboard.Section("Latency overview",
				dashboard.Half("ts-lat-overview"), dashboard.Third("ts-http"), dashboard.Third("ts-portkey-dur")),
		),
		dashboard.Tabbed("Latency (histograms)",
			dashboard.Section("LLM round-trip latency",
				dashboard.Half("lat-quantiles"), dashboard.Half("lat-heat")),
			dashboard.Section("Gateway overhead & provider breakdown",
				dashboard.Half("lat-overhead-excl"), dashboard.Half("lat-by-provider")),
		),
		dashboard.Tabbed("Cost & cache",
			dashboard.Section("Token throughput & spend (gauge delta)",
				dashboard.Half("cc-tokens"), dashboard.Half("cc-cost"),
				dashboard.Full("cc-cost-model")),
			dashboard.Section("Cache & resilience",
				dashboard.Half("cc-cache"), dashboard.Half("cc-retries")),
		),
		dashboard.Tabbed("Node.js runtime",
			dashboard.Section("Gateway process — Node.js runtime (node_process_* + node_eventloop_lag_seconds)",
				dashboard.Third("node-cpu"), dashboard.Third("node-mem"), dashboard.Third("node-lag")),
		),
		dashboard.Tabbed("Gateway logs",
			dashboard.Section("",
				dashboard.Full("logs-intro"),
				dashboard.Tall("logs-portkey")),
		),
	)
	return d, nil
}
