// SPDX-License-Identifier: AGPL-3.0-only

// GoldenPath is the Acme AI Golden Path dashboard (acme-ai-platform-eval blueprint,
// predecessor A3-golden-path.json). It shows the signals that are EXCLUSIVE to the
// connected-gateway scenario — the "unlock delta" vs the analytics-poller deployment —
// structured as a guided walkthrough of the happy-path (reference user journey).
//
// Architecture: the Portkey gateway runs in the eval service's accounts/VPC (multi-tenant, NOT Acme AI's).
// In the connected-gateway scenario, the eval service SHARES the gateway's per-tenant telemetry with
// Acme AI: the gateway /metrics app-level slice (request_count, llm_*, cost/token families, processing
// histograms) PLUS the connected Path-B gateway span in Acme AI's trace. Acme AI owns NO gateway
// clusters and does NOT receive the gateway's host/node infra.
//
// acme_ai_platform_eval (connected-gateway) vs acme_ai_platform (analytics-poller) distinction:
//
//   - In acme_ai_platform_eval the AI Evaluation gateway is CONNECTED: it exports OTel spans with the
//     propagated W3C trace-id (Path-B trace reuse), so the Portkey gateway
//     SERVER span (service.name=portkey, route POST /v1/chat/completions) sits
//     in the SAME Tempo trace tree as the app spans. No log-join stitch needed.
//   - AI Evaluation shares the gateway /metrics in real-time (request_count, llm_*,
//     portkey_request_*, portkey_processing_*) — not available under acme_ai_platform,
//     which only has the 5-min portkey_api_* analytics poll.
//   - A real-time Portkey gateway log stream ({source="portkey"}) is present,
//     carrying correlation_id + trace_id as structured metadata.
//   - llm_* gauge families (llm_token_sum, llm_cost_sum) and the classic
//     histogram llm_request_duration_milliseconds_bucket are present.
//
// Tabs (matching the predecessor A3-golden-path.json tab order):
//
//	Why this matters         — intro text describing the unlock delta
//	Gateway native scrape    — KPI tiles + native request/latency/cache/cost panels
//	Real-time logs & prompts — prompt-version usage (S12) + real-time gateway logs
//	Gateway traces           — backend→gateway service-graph edge + span-metrics +
//	                           gateway span p95 latency + TraceQL gateway trace table +
//	                           LangSmith-note text
//
// Scope notes (load-bearing):
//
//   - request_count / portkey_request_* / llm_* / portkey_processing_* are
//     SUBSTRATE/integration families (native scrape) — use acme.IntSel().
//     The predecessor used scenario="acme_ai_platform_eval" on these — DROPPED for synthkit
//     (no blueprint label on substrate families).
//   - traces_spanmetrics_* / traces_service_graph_* are metrics-generator-derived;
//     they carry NO blueprint label → hand-written selectors (service= / client= /
//     server= edge matchers). The predecessor's scenario= filter on these is DROPPED.
//   - Portkey real-time log stream ({source="portkey"}) is substrate-scoped;
//     predecessor had scenario= on this stream — DROPPED.
//   - llm_cost_sum / llm_token_sum are GAUGE (cumulative) — use delta(), never rate().
//   - llm_request_duration_milliseconds is a classic histogram → histogram_quantile
//     over the _bucket suffix. portkey_processing_time_excluding_last_byte_ms and
//     llm_last_byte_diff_duration_milliseconds are also classic histograms (_bucket).
//
// No Infinity endpoints are used in this dashboard (A3 is purely metric/trace/log).
//
// "golden thread" / "Golden Thread" strings are BANNED in dashboard copy
// (use "Request Correlation" / "End-to-End Trace" instead).
// "Golden Path" (A3) is a different, kept term meaning the happy-path reference journey.
package acme_ai_platform_eval

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"

	dashboardv2 "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// gwSel returns a label-matcher selector for gateway-native SUBSTRATE families
// (request_count, llm_*, portkey_request_*, portkey_processing_*).
// These carry NO blueprint label; they are scoped by env + provider + use_case.
// extra is an already-formatted matcher list (no leading comma).
func gwSel(extra string) string {
	base := `env=~"$env",provider=~"$provider",metadata_use_case=~"$use_case"`
	if extra != "" {
		base += "," + extra
	}
	return "{" + base + "}"
}

// gwSelEnvProv returns a selector scoped only by env + provider (for latency/cost
// families that do not carry metadata_use_case).
func gwSelEnvProv(extra string) string {
	base := `env=~"$env",provider=~"$provider"`
	if extra != "" {
		base += "," + extra
	}
	return "{" + base + "}"
}

// GoldenPath builds the Acme AI Golden Path dashboard for the acme-ai-platform-eval blueprint.
// uid: acme-ws2-golden-path. Four tabs reproducing the predecessor A3-golden-path layout.
func GoldenPath(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws2-golden-path", "Acme AI — Golden Path (connected-gateway)")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ─────────────────────────────────────────────────────────────────────────────

	// scenario: hidden const — app selectors resolve via acme.AppSel($scenario).
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform_eval"))

	// env: multi-value; predecessor sourced from request_count (substrate, no blueprint label).
	d.Builder.QueryVariable(dashboard.LabelValuesVar("env", "Environment",
		`label_values(request_count{}, env)`))

	// provider: multi-value; predecessor sourced from request_count (substrate).
	d.Builder.QueryVariable(dashboard.LabelValuesVar("provider", "Provider",
		`label_values(request_count{}, provider)`))

	// use_case: multi-value; predecessor sourced from request_count (substrate).
	d.Builder.QueryVariable(dashboard.LabelValuesVar("use_case", "Use case",
		`label_values(request_count{}, metadata_use_case)`))

	// ── TAB 1: Why this matters ────────────────────────────────────────────────────────────────

	bt := "`"
	dashboard.AddPanel(&d, "gp-intro",
		dashboard.TextPanel("",
			"## The Golden Path — the *unlock delta* vs the analytics-poller deployment\n\n"+
				"Every panel here shows a signal that **does not exist under the analytics-poller deployment**. "+
				"These are what you gain when **AI Evaluation shares the gateway's per-tenant Prometheus/OTel telemetry** with Acme AI "+
				"(the connected-gateway world — acme_ai_platform_eval blueprint):\n\n"+
				"> **Architecture:** the Portkey gateway runs in **the eval service accounts/VPC** (multi-tenant), NOT Acme AI's. "+
				"AI Evaluation shares the gateway's per-tenant `/metrics` slice and OTel spans with Acme AI. "+
				"**Acme AI owns no gateway clusters and receives no gateway host/node infra.**\n\n"+
				"- **Shared gateway `/metrics`** — real-time request/latency/cache from the Portkey data-plane "+
				"(`request_count`, `llm_request_duration_milliseconds`, native `cacheStatus`). "+
				"The analytics-poller deployment only has the 5-min `portkey_api_*` poll.\n"+
				"- **The backend→gateway span + service-graph edge** — in the analytics-poller deployment the gateway "+
				"is a black box bracketed by the app's client span (that missing edge is *correct teaching* there). "+
				"Here the gateway SERVER span (`service.name=portkey`, route `POST /v1/chat/completions`) "+
				"appears in the **same Tempo trace** as the app spans (Path-B trace reuse, AI Evaluation-emitted).\n"+
				"- **Real-time gateway log stream** (`source=\"portkey\"`) vs the batched 15-min export in the analytics-poller deployment.\n"+
				"- **Gateway span-metrics** + **Path-B single-trace join** (one trace_id covers both app and gateway hops — "+
				"no metadata log-join stitch needed).\n\n"+
				"_If these panels are empty, enable the `acme_ai_platform_eval` blueprint (`ENABLED_SCENARIOS`) to populate them._\n\n"+
				"> **Scope note:** shared gateway families (`request_count`, `llm_*`, `portkey_processing_*`) "+
				"are SUBSTRATE-scoped (no blueprint label). Use the `$env`/`$provider`/`$use_case` variables above to filter. "+
				"Traces and service-graph panels filter by "+bt+"service=portkey"+bt+" — no blueprint label needed.",
		))

	// ── TAB 2: Gateway native scrape ──────────────────────────────────────────────────────────

	// KPI tiles (predecessor panel-1: request rate, panel-2: error rate, panel-3: p95 latency,
	// panel-4: cache-hit ratio, panel-5: spend).

	// panel-1: Request rate
	dashboard.AddPanel(&d, "gw-rps",
		dashboard.StatTile("Request rate", "reqps",
			dashboard.PromTarget(
				`sum(rate(request_count`+gwSel("")+`[$__rate_interval]))`,
				"reqps")))

	// panel-2: Error rate (non-2xx)
	dashboard.AddPanel(&d, "gw-errpct",
		dashboard.StatTile("Error rate (non-2xx)", "percent",
			dashboard.PromTarget(
				`100 * sum(rate(request_count`+gwSel(`code!~"2.."`)+ // non-2xx
					`[$__rate_interval])) / clamp_min(sum(rate(request_count`+gwSel("")+`[$__rate_interval])), 0.001)`,
				"error %"),
			dashboard.Threshold{Value: 0, Color: "green"},
			dashboard.Threshold{Value: 1, Color: "yellow"},
			dashboard.Threshold{Value: 5, Color: "red"}))

	// panel-3: p95 LLM round-trip (classic histogram_quantile — ms)
	dashboard.AddPanel(&d, "gw-p95lat",
		dashboard.StatTile("p95 LLM round-trip (histogram)", "ms",
			dashboard.PromTarget(
				`histogram_quantile(0.95, sum by (le) (rate(llm_request_duration_milliseconds_bucket`+
					gwSelEnvProv("")+`[$__rate_interval])))`,
				"p95"),
			dashboard.Threshold{Value: 0, Color: "green"},
			dashboard.Threshold{Value: 3000, Color: "yellow"},
			dashboard.Threshold{Value: 8000, Color: "red"}))

	// panel-4: Cache-hit ratio
	dashboard.AddPanel(&d, "gw-cachehit",
		dashboard.StatTile("Cache-hit ratio", "percent",
			dashboard.PromTarget(
				`100 * sum(rate(request_count`+gwSel(`cacheStatus="hit"`)+
					`[$__rate_interval])) / clamp_min(sum(rate(request_count`+gwSel("")+`[$__rate_interval])), 0.001)`,
				"cache-hit %")))

	// panel-5: Spend (range, gauge delta)
	// llm_cost_sum is a GAUGE (cumulative) — delta(), never rate().
	dashboard.AddPanel(&d, "gw-spend",
		dashboard.StatTile("Spend (range, gauge delta)", "currencyUSD",
			dashboard.PromTarget(
				`sum(clamp_min(delta(llm_cost_sum`+gwSelEnvProv("")+`[$__range]), 0))`,
				"spend")))

	// Native request rate & latency section (predecessor panel-10, panel-12)

	// panel-10: Request rate by provider
	dashboard.AddPanel(&d, "gw-rps-by-provider",
		dashboard.TimeseriesPanel("Request rate by provider", "reqps",
			dashboard.PromTarget(
				`sum by (provider) (rate(request_count`+gwSel("")+`[$__rate_interval]))`,
				"{{provider}}")))

	// panel-12: LLM round-trip latency — histogram_quantile (p50/p95/p99)
	// ONLY valid in the unlock world — classic histogram (ms bucket).
	dashboard.AddPanel(&d, "gw-llm-lat-histo",
		dashboard.TimeseriesPanel("LLM round-trip latency — histogram_quantile (p50/p95/p99)", "ms",
			dashboard.PromTarget(
				`histogram_quantile(0.50, sum by (le) (rate(llm_request_duration_milliseconds_bucket`+
					gwSelEnvProv("")+`[$__rate_interval])))`,
				"p50").RefId("A"),
			dashboard.PromTarget(
				`histogram_quantile(0.95, sum by (le) (rate(llm_request_duration_milliseconds_bucket`+
					gwSelEnvProv("")+`[$__rate_interval])))`,
				"p95").RefId("B"),
			dashboard.PromTarget(
				`histogram_quantile(0.99, sum by (le) (rate(llm_request_duration_milliseconds_bucket`+
					gwSelEnvProv("")+`[$__rate_interval])))`,
				"p99").RefId("C")))

	// Gateway overhead & cache section (predecessor panel-13, panel-17)

	// panel-13: Gateway overhead vs TTFT proxy (p95) — classic histograms
	// portkey_processing_time_excluding_last_byte_ms and llm_last_byte_diff_duration_milliseconds
	// are classic histograms (_bucket). Scope: IntSel (env+provider, no use_case).
	dashboard.AddPanel(&d, "gw-overhead-ttft",
		dashboard.TimeseriesPanel("Gateway overhead vs TTFT proxy (p95) — native histograms", "ms",
			dashboard.PromTarget(
				`histogram_quantile(0.95, sum by (le) (rate(portkey_processing_time_excluding_last_byte_ms_bucket`+
					acme.IntSel(`provider=~"$provider"`)+ // substrate: env+provider, no use_case
					`[$__rate_interval])))`,
				"gateway overhead p95").RefId("A"),
			dashboard.PromTarget(
				`histogram_quantile(0.95, sum by (le) (rate(llm_last_byte_diff_duration_milliseconds_bucket`+
					acme.IntSel(`provider=~"$provider"`)+
					`[$__rate_interval])))`,
				"TTFT proxy p95").RefId("B")))

	// panel-17: Cache status breakdown (native cacheStatus label)
	// cacheStatus ∈ {hit, miss, disabled, error} — see signals/portkey.md.
	dashboard.AddPanel(&d, "gw-cache-status",
		dashboard.TimeseriesPanel("Cache status breakdown (native cacheStatus label)", "reqps",
			dashboard.PromTarget(
				`sum by (cacheStatus) (rate(request_count`+gwSel("")+`[$__rate_interval]))`,
				"{{cacheStatus}}")))

	// Cost & tokens section (predecessor panel-14, panel-15, panel-16)

	// panel-14: Token throughput by provider (gauge delta — NEVER rate())
	dashboard.AddPanel(&d, "gw-tokens",
		dashboard.TimeseriesPanel("Token throughput by provider (gauge delta)", "short",
			dashboard.PromTarget(
				`sum by (provider) (clamp_min(delta(llm_token_sum`+gwSelEnvProv("")+`[$__rate_interval]), 0))`,
				"{{provider}}")))

	// panel-15: Cost by provider (gauge delta)
	dashboard.AddPanel(&d, "gw-cost-ts",
		dashboard.TimeseriesPanel("Cost by provider (gauge delta)", "currencyUSD",
			dashboard.PromTarget(
				`sum by (provider) (clamp_min(delta(llm_cost_sum`+gwSelEnvProv("")+`[$__rate_interval]), 0))`,
				"{{provider}}")))

	// panel-16: Tokens + cost by model — 2-leg merge range table.
	// Leg A: llm_token_sum (cumulative GAUGE) → delta() over $__range gives total tokens per model.
	// Leg B: llm_cost_sum (cumulative GAUGE) → delta() over $__range gives total spend per model.
	// Both gauge families carry the `model` label. delta(), never rate().
	dashboard.AddPanel(&d, "gw-cost-model-table",
		dashboard.MergeTablePanel("Tokens & cost by model (range, gauge delta)",
			[]*dashboardv2.TargetBuilder{
				dashboard.PromTableTarget(
					`sum by (model) (clamp_min(delta(llm_token_sum`+gwSelEnvProv("")+`[$__range]), 0))`,
					"A"),
				dashboard.PromTableTarget(
					`sum by (model) (clamp_min(delta(llm_cost_sum`+gwSelEnvProv("")+`[$__range]), 0))`,
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

	// By use-case & retries section (predecessor panel-11, panel-18)

	// panel-11: Request rate by use case
	dashboard.AddPanel(&d, "gw-rps-by-usecase",
		dashboard.TimeseriesPanel("Request rate by use case", "reqps",
			dashboard.PromTarget(
				`sum by (metadata_use_case) (rate(request_count`+gwSel("")+`[$__rate_interval]))`,
				"{{metadata_use_case}}")))

	// panel-18: Retries & fallbacks (log-derived counters)
	// llm_retries_total / llm_fallbacks_total — loki.process-derived counters.
	// Substrate-scoped (env+provider, no blueprint label).
	dashboard.AddPanel(&d, "gw-retries",
		dashboard.TimeseriesPanel("Retries & fallbacks (log-derived)", "short",
			dashboard.PromTarget(
				`sum by (provider) (rate(llm_retries_total`+gwSelEnvProv("")+`[$__rate_interval]))`,
				"retries · {{provider}}").RefId("A"),
			dashboard.PromTarget(
				`sum by (provider) (rate(llm_fallbacks_total`+gwSelEnvProv("")+`[$__rate_interval]))`,
				"fallbacks · {{provider}}").RefId("B")))

	// ── TAB 3: Real-time logs & prompts ────────────────────────────────────────────────────────

	// panel-19: Prompt-version usage (S12) — real-time Loki stream.
	// Portkey real-time stream is substrate-scoped: {source="portkey"}.
	// predecessor had scenario="acme_ai_platform_eval" on this stream — DROPPED (not a synthkit stream label).
	dashboard.AddPanel(&d, "rt-prompt-usage",
		dashboard.TimeseriesPanel("Prompt-version usage (S12) — real-time Loki stream", "short",
			dashboard.LokiTarget(
				`topk(10, sum by (prompt_slug) (count_over_time({source="portkey"} | prompt_slug!="" [$__auto])))`,
				"{{prompt_slug}}")))

	// panel-20: Portkey gateway logs — real-time stream.
	// Real-time portkey stream (not the batched 2b export) — available in acme_ai_platform_eval.
	// Substrate-scoped: {source="portkey"} — no blueprint label.
	dashboard.AddPanel(&d, "rt-gw-logs",
		dashboard.LogsPanel("Portkey gateway logs — real-time stream (acme_ai_platform_eval)",
			dashboard.LokiTarget(
				`{source="portkey"} | json`,
				"")))

	// ── TAB 4: Gateway traces (the missing edge) ────────────────────────────────────────────────

	// gp-edge: Backend → gateway service-graph edge (the unlock).
	// traces_service_graph_request_total is metrics-generator-derived — NO blueprint label.
	// Scope by server=~".*portkey.*" (the gateway server node).
	// predecessor had scenario= on this — DROPPED.
	dashboard.AddPanel(&d, "gp-edge",
		dashboard.TimeseriesPanel("Backend → gateway service-graph edge (the unlock)", "reqps",
			dashboard.PromTarget(
				`sum by (client, server)(rate(traces_service_graph_request_total{server=~".*portkey.*"}[$__rate_interval]))`,
				"{{client}} → {{server}}")))

	// gp-gwspan: Gateway span rate (span-metrics).
	// traces_spanmetrics_calls_total is metrics-generator-derived — NO blueprint label.
	// Scope by service=~"portkey.*".
	dashboard.AddPanel(&d, "gp-gwspan",
		dashboard.TimeseriesPanel("Gateway span rate (span-metrics)", "reqps",
			dashboard.PromTarget(
				`sum by (span_name)(rate(traces_spanmetrics_calls_total{service=~"portkey.*"}[$__rate_interval]))`,
				"{{span_name}}")))

	// gp-gwlat: Gateway span p95 latency (span-metrics classic histogram).
	// traces_spanmetrics_latency is a classic histogram → histogram_quantile over _bucket suffix.
	// Scope by service=~"portkey.*". No blueprint label.
	dashboard.AddPanel(&d, "gp-gwlat",
		dashboard.TimeseriesPanel("Gateway span p95 latency (span-metrics)", "s",
			dashboard.PromTarget(
				`histogram_quantile(0.95, sum by (le) (rate(traces_spanmetrics_latency_bucket{service=~"portkey.*"}[$__rate_interval])))`,
				"p95")))

	// gp-gwtrace: Gateway spans (TraceQL — Path-B trace reuse).
	// Gateway spans carry the propagated trace_id → single-trace join across the gateway hop.
	// TraceQL filter by resource.service.name=~"portkey.*".
	dashboard.AddPanel(&d, "gp-gwtrace",
		dashboard.TraceTablePanel(
			"Gateway spans (TraceQL — Path-B trace reuse, service.name=portkey)",
			acme.DSTempo,
			dashboard.TempoTableTarget(
				`{ resource.service.name =~ "portkey.*" }`, 20)))

	// gp-langsmith-note: LangSmith platform self-metrics (unlock context).
	dashboard.AddPanel(&d, "gp-langsmith-note",
		dashboard.TextPanel("",
			"### LangSmith platform self-metrics (unlock)\n\n"+
				"With direct scrape of the self-hosted LangSmith platform, you also gain **platform-health + poll-cadence gauges** "+
				"(queue depth, eval worker throughput, ingestion lag) that the analytics-poller deployment omits — there the eval signal comes only "+
				"from the `/api/v1/sessions` + `/runs/query` API poll. _(Metric names depend on the AI Evaluation LangSmith Helm deployment; "+
				"populated under the connected-gateway blueprint when scraped.)_"))

	// ── Layout ────────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("Why this matters",
			dashboard.Section("",
				dashboard.Full("gp-intro")),
		),
		dashboard.Tabbed("Gateway native scrape",
			dashboard.Section("Native KPIs (real-time /metrics — connected-gateway only)",
				dashboard.Tile("gw-rps"), dashboard.Tile("gw-errpct"), dashboard.Tile("gw-p95lat"),
				dashboard.Tile("gw-cachehit"), dashboard.Tile("gw-spend")),
			dashboard.Section("Native request rate & latency",
				dashboard.Half("gw-rps-by-provider"), dashboard.Half("gw-llm-lat-histo")),
			dashboard.Section("Gateway overhead & cache (native)",
				dashboard.Half("gw-overhead-ttft"), dashboard.Half("gw-cache-status")),
			dashboard.Section("Cost & tokens (gauge delta)",
				dashboard.Third("gw-tokens"), dashboard.Third("gw-cost-ts"), dashboard.Third("gw-cost-model-table")),
			dashboard.Section("By use-case & retries",
				dashboard.Half("gw-rps-by-usecase"), dashboard.Half("gw-retries")),
		),
		dashboard.Tabbed("Real-time logs & prompts",
			dashboard.Section("Prompt-version usage (real-time Loki — S12)",
				dashboard.Full("rt-prompt-usage")),
			dashboard.Section("Gateway log stream (real-time, source=portkey)",
				dashboard.Tall("rt-gw-logs")),
		),
		dashboard.Tabbed("Gateway traces (the missing edge)",
			dashboard.Section("The service-graph edge (connected-gateway only)",
				dashboard.Half("gp-edge"), dashboard.Half("gp-gwspan")),
			dashboard.Section("Gateway latency & spans",
				dashboard.Half("gp-gwlat"), dashboard.Tall("gp-gwtrace")),
			dashboard.Section("",
				dashboard.Full("gp-langsmith-note")),
		),
	)
	return d, nil
}
