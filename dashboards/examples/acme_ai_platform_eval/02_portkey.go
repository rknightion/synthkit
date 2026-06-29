// SPDX-License-Identifier: AGPL-3.0-only

// PortkeyGateway is the acme_ai_platform_eval dashboard for the Portkey LLM Gateway — the
// SHARED/MANAGED gateway view. The Portkey gateway runs in the eval service's accounts/VPC (multi-tenant,
// NOT Acme AI's). The eval service SHARES the gateway's per-tenant telemetry with Acme AI.
// Acme AI owns NO gateway clusters and does NOT receive gateway host/node infra.
//
// Shared telemetry families (connected-gateway unlock, scraped from the gateway /metrics in AI Evaluation's estate):
//
//   - request_count — true Counter → RateExpr / rate()
//   - llm_request_duration_milliseconds — classic histogram (ms) → ClassicHistogramQuantile
//   - portkey_request_duration_milliseconds — classic histogram (ms) → ClassicHistogramQuantile
//   - http_request_duration_seconds — classic histogram (s) → ClassicHistogramQuantile (×1000 → ms for overlay)
//   - llm_token_sum, llm_cost_sum — GAUGES (cumulative; reset on restart) → delta(), NEVER rate()
//   - portkey_processing_time_excluding_last_byte_ms — classic histogram
//
// Connected gateway span (Path-B trace reuse, metrics-generator derived):
//
//   - traces_spanmetrics_calls_total{service=~"portkey.*"} — span call rate
//   - traces_spanmetrics_latency{service=~"portkey.*"} — native histogram → NativeHistogramQuantile
//
// ALL shared gateway families are SUBSTRATE-SCOPED (no blueprint label) → IntSel (env=~"$env").
// The predecessor's {scenario="acme_ai_platform_eval"} filter is DROPPED on every native gateway query.
//
// Predecessor source: acme-portkey-eval.json. Ported 2026-06-16. Reworked 2026-06-16 to reflect
// AI Evaluation-managed architecture: node_* host infra removed (AI Evaluation does not share host metrics);
// connected-gateway-span spanmetrics section added.
//
// KNOWN GAPS (panels wired faithfully; will be EMPTY until the fill lands):
//   - panel "Retries & fallbacks": llm_retries_total and llm_fallbacks_total are Loki
//     recording-rule-derived counters (loki.process pipeline from source=portkey logs). They
//     are NOT emitted by synthkit's promrw path. Panel is wired as RateExpr against IntSel;
//     it will populate once the recording-rule fill lands (see signals/portkey.md [slug: portkey-derived]).
//   - portkey_request_duration_milliseconds: buckets are undocumented (SK-38); emitted as
//     assumed (v: assumed in signals/portkey.md). The panel is wired; it may return no data.
//
// No Infinity endpoints in this dashboard (native scrape, no API-pull tables).
//
// Tab layout:
//
//	Native KPIs | Latency (histograms) | Cost & tokens | Connected gateway span | Real-time logs
package acme_ai_platform_eval

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"

	dashboardv2 "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// pkSel scopes a native Portkey gateway family (substrate; carries app/env/provider/code/model/
// cacheStatus/metadata_use_case labels; NO blueprint label) by env + optional extra matchers.
// extra is an already-formatted matcher list (no leading comma), e.g.
// `provider=~"$provider",metadata_use_case=~"$use_case"`.
//
// This is a thin local helper that delegates env-scoping to acme.IntSel to avoid duplicating
// the IntSel join logic. Named pkSel (not intSel) to avoid any collision with other files in
// the package.
func pkSel(extra string) string {
	return acme.IntSel(extra)
}

// PortkeyGateway builds the Acme AI-ws2 — Portkey Gateway (AI Evaluation-managed) dashboard (uid acme-ws2-portkey).
//
// The gateway runs in AI Evaluation's estate; AI Evaluation shares per-tenant telemetry with Acme AI.
// Acme AI owns no gateway clusters; node/host infra is NOT shared.
//
// Shared families: request_count (Counter), llm_request_duration_milliseconds (histogram),
// portkey_request_duration_milliseconds (histogram), http_request_duration_seconds (histogram),
// llm_token_sum (GAUGE), llm_cost_sum (GAUGE).
// Connected span (metrics-generator): traces_spanmetrics_calls_total, traces_spanmetrics_latency.
//
// All substrate families: IntSel (no blueprint). delta() for GAUGE families.
func PortkeyGateway(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws2-portkey", "Acme AI-ws2 — Portkey Gateway (AI Evaluation-managed)")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ────────────────────────────────────────────────────────────────────────────────

	// scenario: hidden const var (blueprint name). Keeps AppSel-using panels coherent.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform_eval"))

	// env: substrate-scoped (no blueprint); source from request_count (DROP scenario= — substrate).
	d.Builder.CustomVariable(dashboard.EnvVar(m))

	// provider: label on all native gateway families → substrate, no {scenario=} filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("provider", "Provider",
		`label_values(request_count, provider)`))

	// use_case: metadata_use_case on request_count → substrate, no {scenario=} filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("use_case", "Use case",
		`label_values(request_count, metadata_use_case)`))

	// ── TAB: Native KPIs ─────────────────────────────────────────────────────────────────────────

	// KPI tile: request rate (predecessor panel k-rate). request_count is a true Counter → rate().
	// Low thresholds intentionally absent (plain colored tile — blue in predecessor).
	dashboard.AddPanel(&d, "kpi-rate", dashboard.StatTile(
		"Request rate", "reqps",
		dashboard.PromTarget(
			`sum(rate(request_count`+
				pkSel(`provider=~"$provider",metadata_use_case=~"$use_case"`)+
				`[$__rate_interval]))`,
			"req/s")))

	// KPI tile: error rate (non-2xx). Low-is-good → green → yellow → red.
	// (predecessor panel k-err)
	dashboard.AddPanel(&d, "kpi-err", dashboard.StatTile(
		"Error rate (non-2xx)", "percent",
		dashboard.PromTarget(
			`100 * sum(rate(request_count`+
				pkSel(`provider=~"$provider",metadata_use_case=~"$use_case",code!~"2.."`)+
				`[$__rate_interval])) / clamp_min(sum(rate(request_count`+
				pkSel(`provider=~"$provider",metadata_use_case=~"$use_case"`)+
				`[$__rate_interval])), 0.001)`,
			"error %"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "yellow"},
		dashboard.Threshold{Value: 5, Color: "red"}))

	// KPI tile: p95 LLM round-trip (classic histogram). Low-is-good → green → yellow → red.
	// (predecessor panel k-p95; llm_request_duration_milliseconds is in ms)
	dashboard.AddPanel(&d, "kpi-p95", dashboard.StatTile(
		"p95 LLM round-trip (ms)", "ms",
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.95, "llm_request_duration_milliseconds",
				pkSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p95 ms"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 3000, Color: "yellow"},
		dashboard.Threshold{Value: 8000, Color: "red"}))

	// KPI tile: cache-hit ratio. High-is-good → red → yellow → green.
	// cacheStatus∈{hit,miss,disabled,error}; no simple_hit/semantic_hit.
	// (predecessor panel k-cache)
	dashboard.AddPanel(&d, "kpi-cache", dashboard.StatTile(
		"Cache-hit ratio", "percent",
		dashboard.PromTarget(
			`100 * sum(rate(request_count`+
				pkSel(`provider=~"$provider",cacheStatus="hit"`)+
				`[$__rate_interval])) / clamp_min(sum(rate(request_count`+
				pkSel(`provider=~"$provider"`)+
				`[$__rate_interval])), 0.001)`,
			"cache hit %"),
		dashboard.Threshold{Value: 0, Color: "red"},
		dashboard.Threshold{Value: 20, Color: "yellow"},
		dashboard.Threshold{Value: 50, Color: "green"}))

	// KPI tile: spend over range (delta of cumulative GAUGE llm_cost_sum — never rate()).
	// (predecessor panel k-spend)
	dashboard.AddPanel(&d, "kpi-spend", dashboard.StatTile(
		"Spend (range, gauge delta)", "currencyUSD",
		dashboard.PromTarget(
			`sum(clamp_min(delta(llm_cost_sum`+
				pkSel(`provider=~"$provider"`)+
				`[$__range]), 0))`,
			"USD")))

	// Timeseries: request rate by status code (stacked, predecessor panel p-rate-code).
	dashboard.AddPanel(&d, "ts-rate-code", dashboard.TimeseriesPanel(
		"Request rate by status code", "reqps",
		dashboard.PromTarget(
			`sum by (code) (rate(request_count`+
				pkSel(`provider=~"$provider",metadata_use_case=~"$use_case"`)+
				`[$__rate_interval]))`,
			"{{code}}")))

	// Timeseries: request rate by model (stacked, predecessor panel p-rate-model).
	dashboard.AddPanel(&d, "ts-rate-model", dashboard.TimeseriesPanel(
		"Request rate by model", "reqps",
		dashboard.PromTarget(
			`sum by (model) (rate(request_count`+
				pkSel(`provider=~"$provider",metadata_use_case=~"$use_case"`)+
				`[$__rate_interval]))`,
			"{{model}}")))

	// Timeseries: cache status breakdown (predecessor panel p-cache-break).
	dashboard.AddPanel(&d, "ts-cache", dashboard.TimeseriesPanel(
		"Cache status breakdown (cacheStatus∈{hit,miss,disabled,error})", "reqps",
		dashboard.PromTarget(
			`sum by (cacheStatus) (rate(request_count`+
				pkSel(`provider=~"$provider"`)+
				`[$__rate_interval]))`,
			"{{cacheStatus}}")))

	// Timeseries: p50/p95/p99 latency overview (for the KPI tab — predecessor panel p-lat-q).
	// Also used in the Latency tab below (same panel id reused is NOT allowed — separate ids).
	dashboard.AddPanel(&d, "ts-lat-overview", dashboard.TimeseriesPanel(
		"LLM round-trip latency — p50/p95/p99 overview", "ms",
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.50, "llm_request_duration_milliseconds",
				pkSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p50").RefId("A"),
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.95, "llm_request_duration_milliseconds",
				pkSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p95").RefId("B"),
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.99, "llm_request_duration_milliseconds",
				pkSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p99").RefId("C")))

	// ── TAB: Latency (histograms) ─────────────────────────────────────────────────────────────────

	// Timeseries: p50/p95/p99 (predecessor panel p-lat-q; reproduced with unique id for the Latency tab).
	dashboard.AddPanel(&d, "lat-quantiles", dashboard.TimeseriesPanel(
		"LLM round-trip latency — histogram_quantile (p50/p95/p99)", "ms",
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.50, "llm_request_duration_milliseconds",
				pkSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p50").RefId("A"),
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.95, "llm_request_duration_milliseconds",
				pkSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p95").RefId("B"),
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.99, "llm_request_duration_milliseconds",
				pkSel(`provider=~"$provider"`),
				[]string{"le"}),
			"p99").RefId("C")))

	// Heatmap: LLM round-trip latency distribution (predecessor panel p-lat-heat).
	// Source: llm_request_duration_milliseconds classic histogram → pre-bucketed.
	dashboard.AddPanel(&d, "lat-heat", dashboard.HeatmapPanel(
		"LLM round-trip latency heatmap", "ms",
		dashboard.PromTarget(
			`sum by (le) (rate(llm_request_duration_milliseconds_bucket`+
				pkSel(`provider=~"$provider"`)+
				`[$__rate_interval]))`,
			"{{le}}")))

	// Timeseries: gateway overhead p95 — portkey vs http (predecessor panel p-gw-overhead).
	// portkey_request_duration_milliseconds (ms) vs http_request_duration_seconds (s→ms).
	// KNOWN GAP: portkey_request_duration_milliseconds has undocumented buckets (SK-38 v: assumed);
	// this panel may return no data until the construct emits real bucket series.
	dashboard.AddPanel(&d, "lat-overhead", dashboard.TimeseriesPanel(
		"Gateway overhead p95 — portkey_request_duration vs http_request_duration", "ms",
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.95, "portkey_request_duration_milliseconds",
				pkSel(`provider=~"$provider"`),
				[]string{"le"}),
			"portkey gateway p95 (ms)").RefId("A"),
		dashboard.PromTarget(
			`1000 * `+dashboard.ClassicHistogramQuantile(0.95, "http_request_duration_seconds",
				pkSel(`provider=~"$provider"`),
				[]string{"le"}),
			"http server p95 (ms)").RefId("B")))

	// ── TAB: Cost & tokens ───────────────────────────────────────────────────────────────────────

	// Timeseries: token throughput by provider (predecessor panel p-tok).
	// llm_token_sum is a cumulative GAUGE → delta(), never rate().
	dashboard.AddPanel(&d, "ct-tokens", dashboard.TimeseriesPanel(
		"Token throughput by provider (gauge delta)", "short",
		dashboard.PromTarget(
			`sum by (provider) (clamp_min(delta(llm_token_sum`+
				pkSel(`provider=~"$provider"`)+
				`[$__rate_interval]), 0))`,
			"{{provider}}")))

	// Timeseries: cost by provider (predecessor panel p-cost-prov).
	// llm_cost_sum is a cumulative GAUGE → delta(), never rate().
	dashboard.AddPanel(&d, "ct-cost-prov", dashboard.TimeseriesPanel(
		"Cost by provider (gauge delta)", "currencyUSD",
		dashboard.PromTarget(
			`sum by (provider) (clamp_min(delta(llm_cost_sum`+
				pkSel(`provider=~"$provider"`)+
				`[$__rate_interval]), 0))`,
			"{{provider}}")))

	// Table: tokens + cost by model over range (predecessor panel p-cost-model; 2-leg merge table).
	// Leg A: llm_token_sum (cumulative GAUGE) → delta() over $__range gives total tokens per model.
	// Leg B: llm_cost_sum (cumulative GAUGE) → delta() over $__range gives total spend per model.
	// Both gauge families use the `model` label. delta(), never rate().
	dashboard.AddPanel(&d, "ct-cost-model", dashboard.MergeTablePanel(
		"Tokens & cost by model (range, gauge delta)",
		[]*dashboardv2.TargetBuilder{
			dashboard.PromTableTarget(
				`sum by (model) (clamp_min(delta(llm_token_sum`+pkSel(`provider=~"$provider"`)+`[$__range]), 0))`,
				"A"),
			dashboard.PromTableTarget(
				`sum by (model) (clamp_min(delta(llm_cost_sum`+pkSel(`provider=~"$provider"`)+`[$__range]), 0))`,
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

	// Timeseries: request rate by use case (predecessor panel p-uc).
	dashboard.AddPanel(&d, "ct-uc", dashboard.TimeseriesPanel(
		"Request rate by use case", "reqps",
		dashboard.PromTarget(
			`sum by (metadata_use_case) (rate(request_count`+
				pkSel(`provider=~"$provider",metadata_use_case=~"$use_case"`)+
				`[$__rate_interval]))`,
			"{{metadata_use_case}}")))

	// Timeseries: retries & fallbacks (predecessor panel p-retry).
	// llm_retries_total and llm_fallbacks_total are Loki recording-rule-derived series
	// (count_over_time([5m]) → instant GAUGE evaluated per scrape, NOT a cumulative counter).
	// Query the recorded series DIRECTLY — rate() is WRONG for this family.
	// KNOWN GAP: NOT emitted by synthkit's promrw path; will be empty until fill lands.
	// (see signals/portkey.md [slug: portkey-derived])
	dashboard.AddPanel(&d, "ct-retry", dashboard.TimeseriesPanel(
		"Retries & fallbacks (recording-rule-derived from Portkey logs — KNOWN GAP)", "short",
		dashboard.PromTarget(
			`sum by (provider) (llm_retries_total`+pkSel(`provider=~"$provider"`)+`)`,
			"retries · {{provider}}").RefId("A"),
		dashboard.PromTarget(
			`sum by (provider) (llm_fallbacks_total`+pkSel(`provider=~"$provider"`)+`)`,
			"fallbacks · {{provider}}").RefId("B")))

	// ── TAB: Connected gateway span ──────────────────────────────────────────────────────────────
	// The gateway runs in AI Evaluation's estate. AI Evaluation shares the gateway's per-tenant
	// Prometheus/OTel telemetry with Acme AI — the gateway appears as a connected span in Acme AI's
	// trace (Path-B trace reuse). These panels surface the tenant-side view via metrics-generator
	// spanmetrics. Acme AI owns no gateway clusters; AI Evaluation does NOT share host/node infra.
	// metrics-generator-derived families: NO blueprint label — hand-written selectors.

	// Timeseries: gateway span call rate (traces_spanmetrics_calls_total, service=~"portkey.*").
	// This is the tenant-side connected-span call rate — the Path-B gateway hop as Acme AI sees it.
	dashboard.AddPanel(&d, "cgspan-rate", dashboard.TimeseriesPanel(
		"Connected gateway span call rate (AI Evaluation-managed, tenant view)", "reqps",
		dashboard.PromTarget(
			`sum by (span_name)(rate(traces_spanmetrics_calls_total{service=~"portkey.*",deployment_environment_name=~"$env"}[$__rate_interval]))`,
			"{{span_name}}")))

	// Timeseries: gateway span p50/p95/p99 latency (traces_spanmetrics_latency, native histogram).
	// NativeHistogramQuantile: metrics-generator emits native histograms for spanmetrics latency.
	// service=~"portkey.*" — no blueprint label.
	dashboard.AddPanel(&d, "cgspan-lat", dashboard.TimeseriesPanel(
		"Connected gateway span latency p50/p95/p99 (AI Evaluation-managed, native histogram)", "s",
		dashboard.PromTarget(
			dashboard.NativeHistogramQuantile(0.50,
				"traces_spanmetrics_latency",
				`{service=~"portkey.*",deployment_environment_name=~"$env"}`,
				[]string{"service"}),
			"p50").RefId("A"),
		dashboard.PromTarget(
			dashboard.NativeHistogramQuantile(0.95,
				"traces_spanmetrics_latency",
				`{service=~"portkey.*",deployment_environment_name=~"$env"}`,
				[]string{"service"}),
			"p95").RefId("B"),
		dashboard.PromTarget(
			dashboard.NativeHistogramQuantile(0.99,
				"traces_spanmetrics_latency",
				`{service=~"portkey.*",deployment_environment_name=~"$env"}`,
				[]string{"service"}),
			"p99").RefId("C")))

	// Text: connected span context note.
	dashboard.AddPanel(&d, "cgspan-note", dashboard.TextPanel(
		"Connected gateway span — AI Evaluation-managed architecture",
		"## Connected gateway span (connected-gateway unlock)\n\n"+
			"AI Evaluation shares the gateway's per-tenant Prometheus/OTel telemetry with Acme AI; "+
			"**the gateway runs in AI Evaluation's estate — Acme AI owns no gateway infrastructure**.\n\n"+
			"The gateway hop appears as a **connected span** in Acme AI's trace (Path-B W3C trace-id propagation). "+
			"These panels show the tenant-side view derived by the metrics-generator from that connected span:\n\n"+
			"- `traces_spanmetrics_calls_total{service=~\"portkey.*\"}` — call rate per span operation\n"+
			"- `traces_spanmetrics_latency{service=~\"portkey.*\"}` — native-histogram latency (p50/p95/p99)\n\n"+
			"_Host/node infra (`node_*`) is NOT shared — AI Evaluation does not expose gateway compute metrics to tenants._"))

	// ── TAB: Real-time logs ──────────────────────────────────────────────────────────────────────

	// Text: intro / data contract (predecessor panel t-intro, placed in the Real-time logs tab).
	dashboard.AddPanel(&d, "logs-intro", dashboard.TextPanel(
		"AI Evaluation-managed gateway — shared telemetry data contract",
		"## Portkey Gateway — AI Evaluation-managed (connected-gateway unlock)\n\n"+
			"AI Evaluation shares the gateway's per-tenant Prometheus/OTel telemetry with Acme AI; "+
			"**the Portkey gateway runs in AI Evaluation's estate — Acme AI owns no gateway infrastructure**.\n\n"+
			"Shared telemetry families: real-time `request_count` counter, "+
			"`llm_token_sum`/`llm_cost_sum` cumulative **gauges** (read via `delta()`, never `rate()`), "+
			"native latency **histograms** (`llm_request_duration_milliseconds`, ms) with "+
			"`histogram_quantile` + heatmaps, native `cacheStatus`. "+
			"This is the connected-gateway counterpart to the API-pull Portkey dashboard (no `portkey_api_*` here).\n\n"+
			"_Host/node infra (`node_*`) is NOT shared — AI Evaluation does not expose gateway compute metrics to Acme AI tenants._"))

	// Logs: Portkey gateway log stream (predecessor panel p-logs).
	// Stream: {source="portkey", service_name="portkey"} — substrate (no blueprint stream label).
	// The predecessor also filters scenario="acme_ai_platform_eval"; on synthkit the Loki substrate stream
	// carries no blueprint/scenario stream label → drop scenario= filter.
	dashboard.AddPanel(&d, "logs-portkey", dashboard.LogsPanel(
		`Portkey gateway logs — real-time stream (source=portkey, service_name=portkey)`,
		dashboard.LokiTarget(
			`{source="portkey",service_name="portkey"} | json`,
			"")))

	// ── Layout ───────────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("Native KPIs",
			dashboard.Section("Real-time /metrics — gateway-native (connected-gateway only)",
				dashboard.Tile("kpi-rate"), dashboard.Tile("kpi-err"), dashboard.Tile("kpi-p95"),
				dashboard.Tile("kpi-cache"), dashboard.Tile("kpi-spend")),
			dashboard.Section("Request rate & status",
				dashboard.Half("ts-rate-code"), dashboard.Half("ts-rate-model")),
			dashboard.Section("Cache & latency",
				dashboard.Half("ts-cache"), dashboard.Half("ts-lat-overview")),
		),
		dashboard.Tabbed("Latency (histograms)",
			dashboard.Section("Round-trip latency",
				dashboard.Half("lat-quantiles"), dashboard.Half("lat-heat")),
			dashboard.Section("Gateway overhead",
				dashboard.Full("lat-overhead")),
		),
		dashboard.Tabbed("Cost & tokens",
			dashboard.Section("Throughput & spend (gauge delta)",
				dashboard.Third("ct-tokens"), dashboard.Third("ct-cost-prov"), dashboard.Third("ct-cost-model")),
			dashboard.Section("By use case & resilience",
				dashboard.Half("ct-uc"), dashboard.Half("ct-retry")),
		),
		dashboard.Tabbed("Connected gateway span",
			dashboard.Section("Tenant-side connected span (AI Evaluation-managed — Acme AI owns no gateway infra)",
				dashboard.Half("cgspan-rate"), dashboard.Half("cgspan-lat")),
			dashboard.Section("",
				dashboard.Full("cgspan-note")),
		),
		dashboard.Tabbed("Real-time logs",
			dashboard.Section("",
				dashboard.Full("logs-intro"),
				dashboard.Tall("logs-portkey")),
		),
	)
	return d, nil
}
