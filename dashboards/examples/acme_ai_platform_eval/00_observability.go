// SPDX-License-Identifier: AGPL-3.0-only

// ObservabilityHub — Acme AI Observability Hub (acme-ai-platform-eval blueprint).
//
// This is the cross-cutting observability hub / signal-coverage overview for the connected-gateway
// scenario ("with AI Evaluation"). The Portkey gateway runs in the eval service's accounts/VPC (multi-tenant,
// NOT Acme AI's). In the connected-gateway unlock, the eval service SHARES the gateway's per-tenant
// Prometheus/OTel telemetry with Acme AI: the gateway /metrics app-level slice (request_count, llm_*,
// cost, tokens) PLUS a connected Path-B gateway span in Acme AI's trace. Acme AI owns NO gateway
// clusters and does NOT receive the gateway's host/node infra.
//
// The key difference vs acme_ai_platform: the gateway is CONNECTED — AI Evaluation shares the gateway's
// per-tenant native scrape (request_count / llm_request_duration_milliseconds / llm_cost) PLUS a
// connected span that produces a real service-graph edge (backend → portkey) and gateway-scoped
// spanmetrics. acme_ai_platform shows a Portkey analytics-poller lane and notes the gateway is a
// black box; acme_ai_platform_eval shows the live native scrape + connected-span topology instead.
//
// Tabs (4, matching predecessor acme-observability-eval.json):
//
//	Overview · Gateway (native) · LangSmith platform · Estate
//
// Scope discipline (load-bearing — wrong scope = empty panel):
//
//	request_count, llm_request_duration_milliseconds_*, llm_cost_*
//	  SUBSTRATE (native gateway scrape, no blueprint label) → intSel2().
//	traces_service_graph_request_total, traces_spanmetrics_calls_total
//	  metrics-generator-derived (no blueprint label) → hand-written selectors.
//	langsmith_eval_faithfulness_ratio, langsmith_eval_completeness_ratio
//	  SUBSTRATE (env-scoped); acme_ai_platform_eval → include "-gw"-suffixed projects → lsSel2(extra).
//	up{job=~"langsmith-.*"}, http_requests_total{job=~"langsmith-.*"},
//	ClickHouseProfileEvents_Query, redis_connected_clients
//	  SUBSTRATE (LangSmith platform exporters, no blueprint label) → intSel2().
//	aws_bedrock_invocations_sum
//	  SUBSTRATE CW metric-stream (no blueprint label) → plain label matchers.
//	acme_content_leak_test → acme.MetricContentLeakTest (RENAME: de-Rochification).
//	  SUBSTRATE-scoped (no blueprint label) → intSel2().
//	  real Acme AI stack: acme_content_leak_test
//
// Ported from the reference dashboard set (2026-06-16).
// The predecessor uses scenario="acme_ai_platform_eval" on every query; that filter is DROPPED here for all
// substrate/metrics-generator families (they carry no blueprint label) and replaced with the
// correct scope helpers.
//
// Variables declared in predecessor JSON: only `scenario` (ConstVar). The predecessor also references $env,
// $provider, $use_case in panel queries but omits their variable declarations (an omission in the
// predecessor source). We add them here so the variables panel is usable: EnvVar + LabelValuesVar for
// provider and use_case (both substrate-scoped, no blueprint filter).
package acme_ai_platform_eval

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// intSel2 scopes a SUBSTRATE family (no blueprint label) for acme_ai_platform_eval by the substrate `env` label.
// extra is an already-formatted matcher list (no leading comma).
func intSel2(extra string) string {
	s := `env=~"$env"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// lsSel2 scopes langsmith_eval_* for acme_ai_platform_eval (env-scoped; includes "-gw"-suffixed
// projects because acme_ai_platform_eval disambiguates its LangSmith projects with a "-gw" suffix).
// extra appends additional matchers.
func lsSel2(extra string) string {
	// acme_ai_platform_eval: excludeGW=false → include "-gw" projects
	return acme.LangsmithSel(false, extra)
}

// ObservabilityHub is the Acme AI Observability Hub (mission control) for the acme_ai_platform_eval
// blueprint — the connected-gateway reality.
// uid: acme-ws2-observability. Four tabs reproducing the predecessor's acme-observability-eval layout.
func ObservabilityHub(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws2-observability", "Acme AI — Observability (connected-gateway)")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ─────────────────────────────────────────────────────────────────────────────────
	// scenario: ConstVar — the "scenario" concept on the real Acme AI stack; synthkit emits blueprint=.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform_eval"))
	// env: multi-select filter for substrate families.
	d.Builder.CustomVariable(dashboard.EnvVar(m))
	// provider: label_values from request_count — substrate, no blueprint filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("provider", "Provider",
		"label_values(request_count, provider)"))
	// use_case: label_values from request_count — substrate, no blueprint filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("use_case", "Use case",
		"label_values(request_count, metadata_use_case)"))

	// ── Tab 1: Overview ───────────────────────────────────────────────────────────────────────────
	// Row: Platform KPIs (7 colored stat tiles — gateway scrape + platform health).

	// k-rate: request_count native gateway scrape — SUBSTRATE (no blueprint label).
	dashboard.AddPanel(&d, "k-rate", dashboard.StatTile("Gateway req rate", "reqps",
		dashboard.PromTarget(
			`sum(rate(request_count`+intSel2(`provider=~"$provider",metadata_use_case=~"$use_case"`)+`[$__rate_interval]))`,
			"req/s")))

	// k-err: gateway error % — SUBSTRATE (no blueprint label).
	// Non-2xx codes from request_count native scrape.
	dashboard.AddPanel(&d, "k-err", dashboard.StatTile("Gateway error %", "percent",
		dashboard.PromTarget(
			`100 * sum(rate(request_count`+intSel2(`provider=~"$provider",metadata_use_case=~"$use_case",code!~"2.."`)+`[$__rate_interval])) / clamp_min(sum(rate(request_count`+intSel2(`provider=~"$provider",metadata_use_case=~"$use_case"`)+`[$__rate_interval])), 0.001)`,
			"error %"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "yellow"},
		dashboard.Threshold{Value: 5, Color: "red"}))

	// k-p95: p95 latency from native llm_request_duration_milliseconds histogram — SUBSTRATE.
	dashboard.AddPanel(&d, "k-p95", dashboard.StatTile("Gateway p95 (ms)", "ms",
		dashboard.PromTarget(
			`histogram_quantile(0.95, sum by (le) (rate(llm_request_duration_milliseconds_bucket`+intSel2(`provider=~"$provider"`)+`[$__rate_interval])))`,
			"p95 ms"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 3000, Color: "yellow"},
		dashboard.Threshold{Value: 8000, Color: "red"}))

	// k-spend: cumulative spend over the window from llm_cost_sum — SUBSTRATE native gauge.
	// The predecessor uses delta() over $__range; that pattern is preserved here.
	dashboard.AddPanel(&d, "k-spend", dashboard.StatTile("Spend (range)", "currencyUSD",
		dashboard.PromTarget(
			`sum(clamp_min(delta(llm_cost_sum`+intSel2(`provider=~"$provider"`)+`[$__range]), 0))`,
			"spend USD")))

	// k-faith: min faithfulness ratio — SUBSTRATE (acme_ai_platform_eval: include "-gw" projects).
	dashboard.AddPanel(&d, "k-faith", dashboard.StatTile("Min faithfulness", "percentunit",
		dashboard.PromTarget(
			`min(langsmith_eval_faithfulness_ratio`+lsSel2("")+`)`,
			"min faithfulness"),
		dashboard.Threshold{Value: 0, Color: "red"},
		dashboard.Threshold{Value: 0.8, Color: "yellow"},
		dashboard.Threshold{Value: 0.85, Color: "green"}))

	// k-ls-up: LangSmith platform UP — SUBSTRATE; min across all langsmith-* jobs.
	// 1=UP (green), 0=DOWN (red). Colored threshold tile.
	dashboard.AddPanel(&d, "k-ls-up", dashboard.StatTile("LangSmith platform UP", "short",
		dashboard.PromTarget(
			`min(min by (job)(up`+intSel2(`job=~"langsmith-.*"`)+`))`,
			"platform UP"),
		dashboard.Threshold{Value: 0, Color: "red"},
		dashboard.Threshold{Value: 1, Color: "green"}))

	// k-seal: content sealed (acme_content_leak_test → MetricContentLeakTest RENAME).
	// SUBSTRATE (no blueprint label); scope by env only.
	// real Acme AI stack: acme_content_leak_test
	dashboard.AddPanel(&d, "k-seal", dashboard.StatTile("Content sealed", "short",
		dashboard.PromTarget(
			`max(`+acme.MetricContentLeakTest+intSel2("")+`)`,
			"leak test"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "red"}))

	// Row: Needs attention — faithfulness gate breach (< 0.80).
	// a-faith: langsmith_eval_faithfulness_ratio < 0.80 — SUBSTRATE (acme_ai_platform_eval lsSel2).
	dashboard.AddPanel(&d, "a-faith", dashboard.StatTile("Faithfulness gate breach (<0.80)", "percentunit",
		dashboard.PromTarget(
			`langsmith_eval_faithfulness_ratio`+lsSel2("")+` < 0.8`,
			"{{project}} / {{env}}"),
		dashboard.Threshold{Value: 0, Color: "red"},
		dashboard.Threshold{Value: 0.75, Color: "yellow"}))

	// ── Tab 2: Gateway (native) ────────────────────────────────────────────────────────────────────
	// The connected-gateway unlock: AI Evaluation shares the gateway's per-tenant Prometheus/OTel telemetry
	// with Acme AI. The gateway runs in AI Evaluation's estate; Acme AI owns no gateway infra. Shared
	// telemetry includes the native scrape (request_count / llm_*) PLUS a connected span that surfaces a
	// service-graph edge (backend → portkey) and span-metrics.

	// Row: Native scrape — request rate by HTTP code + latency percentiles.
	// g-rate-code: request_count by code — SUBSTRATE (native gateway, no blueprint label).
	dashboard.AddPanel(&d, "g-rate-code", dashboard.TimeseriesPanel("Request rate by code", "reqps",
		dashboard.PromTarget(
			`sum by (code) (rate(request_count`+intSel2(`provider=~"$provider",metadata_use_case=~"$use_case"`)+`[$__rate_interval]))`,
			"{{code}}")))

	// g-lat: latency p50/p95/p99 from classic histogram — SUBSTRATE (native gateway).
	dashboard.AddPanel(&d, "g-lat", dashboard.TimeseriesPanel("Latency p50/p95/p99 (ms)", "ms",
		dashboard.PromTarget(
			`histogram_quantile(0.50, sum by (le) (rate(llm_request_duration_milliseconds_bucket`+intSel2(`provider=~"$provider"`)+`[$__rate_interval])))`,
			"p50").RefId("A"),
		dashboard.PromTarget(
			`histogram_quantile(0.95, sum by (le) (rate(llm_request_duration_milliseconds_bucket`+intSel2(`provider=~"$provider"`)+`[$__rate_interval])))`,
			"p95").RefId("B"),
		dashboard.PromTarget(
			`histogram_quantile(0.99, sum by (le) (rate(llm_request_duration_milliseconds_bucket`+intSel2(`provider=~"$provider"`)+`[$__rate_interval])))`,
			"p99").RefId("C")))

	// Row: Connected gateway spans — the topology that is ABSENT in the analytics-poller scenario (black-box gateway).
	// g-edge: backend → gateway service-graph edge produced by the connected gateway span.
	// traces_service_graph_request_total — metrics-generator-derived (no blueprint label).
	// Scope: server=~".*portkey.*" to isolate the backend→portkey edge.
	dashboard.AddPanel(&d, "g-edge", dashboard.TimeseriesPanel("Backend → gateway service-graph edge (the unlock)", "reqps",
		dashboard.PromTarget(
			`sum by (client, server)(rate(traces_service_graph_request_total{server=~".*portkey.*"}[$__rate_interval]))`,
			"{{client}} → {{server}}")))

	// g-gwspan: gateway span rate by span_name from spanmetrics — metrics-generator-derived.
	// CONFIG-GATED: requires blueprint in span_metrics_blueprints opt-in.
	dashboard.AddPanel(&d, "g-gwspan", dashboard.TimeseriesPanel("Gateway span rate (span-metrics)", "reqps",
		dashboard.PromTarget(
			`sum by (span_name)(rate(traces_spanmetrics_calls_total{service=~"portkey.*",deployment_environment_name=~"$env"}[$__rate_interval]))`,
			"{{span_name}}")))

	// ── Tab 3: LangSmith platform ──────────────────────────────────────────────────────────────────
	// The connected-gateway unlock: native platform-health scrape of LangSmith infrastructure (previously only
	// eval-bridge metrics were available; acme_ai_platform_eval includes up/http/ClickHouse/redis from the LS deployment).

	// Row: Platform health (scrape unlock).
	// lh-up: up by job — SUBSTRATE (LangSmith platform exporter, no blueprint label).
	dashboard.AddPanel(&d, "lh-up", dashboard.TimeseriesPanel("LangSmith platform components UP", "short",
		dashboard.PromTarget(
			`min by (job)(up`+intSel2(`job=~"langsmith-.*"`)+`)`,
			"{{job}}")))

	// lh-http: HTTP request rate by status_code — SUBSTRATE (LangSmith http exporter).
	dashboard.AddPanel(&d, "lh-http", dashboard.TimeseriesPanel("LangSmith HTTP request rate by status", "reqps",
		dashboard.PromTarget(
			`sum by (status_code)(rate(http_requests_total`+intSel2(`job=~"langsmith-.*"`)+`[$__rate_interval]))`,
			"{{status_code}}")))

	// Row: Datastores — ClickHouse + Redis.
	// lh-ch: ClickHouseProfileEvents_Query — SUBSTRATE (ClickHouse exporter, LangSmith platform).
	dashboard.AddPanel(&d, "lh-ch", dashboard.TimeseriesPanel("ClickHouse query rate", "short",
		dashboard.PromTarget(
			`sum(rate(ClickHouseProfileEvents_Query`+intSel2("")+`[$__rate_interval]))`,
			"queries/s")))

	// lh-redis: redis_connected_clients — SUBSTRATE (Redis exporter, LangSmith platform).
	dashboard.AddPanel(&d, "lh-redis", dashboard.TimeseriesPanel("Redis connected clients", "short",
		dashboard.PromTarget(
			`max(redis_connected_clients`+intSel2("")+`)`,
			"clients")))

	// ── Tab 4: Estate ──────────────────────────────────────────────────────────────────────────────
	// Estate-level health: Bedrock + spanmetrics service coverage + eval gate timeseries.

	// Row: Bedrock & services.
	// e-bedrock: aws_bedrock_invocations_sum — SUBSTRATE CW metric-stream (no blueprint label).
	// Per-period CW _sum is a gauge; /60 gives per-second representation.
	dashboard.AddPanel(&d, "e-bedrock", dashboard.TimeseriesPanel("Bedrock invocation rate", "reqps",
		dashboard.PromTarget(
			`sum(aws_bedrock_invocations_sum) / 60`,
			"invocations/s")))

	// e-spanrate: request rate by service from spanmetrics — metrics-generator-derived (no blueprint).
	// CONFIG-GATED: requires blueprint in span_metrics_blueprints opt-in.
	dashboard.AddPanel(&d, "e-spanrate", dashboard.TimeseriesPanel("Request rate by service (spanmetrics)", "reqps",
		dashboard.PromTarget(
			`sum by (service)(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"$env"}[$__rate_interval]))`,
			"{{service}}")))

	// Row: Eval — faithfulness + completeness gate timeseries.
	// e-eval: langsmith_eval_faithfulness_ratio + completeness — SUBSTRATE (acme_ai_platform_eval lsSel2).
	dashboard.AddPanel(&d, "e-eval", dashboard.TimeseriesPanel("Eval gates (faithfulness/completeness)", "percentunit",
		dashboard.PromTarget(
			`min(langsmith_eval_faithfulness_ratio`+lsSel2("")+`)`,
			"faithfulness").RefId("A"),
		dashboard.PromTarget(
			`min(langsmith_eval_completeness_ratio`+lsSel2("")+`)`,
			"completeness").RefId("B")))

	// ── Layout ─────────────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("Overview",
			dashboard.Section("Platform KPIs (gateway scrape + platform health)",
				dashboard.Tile("k-rate"), dashboard.Tile("k-err"), dashboard.Tile("k-p95"),
				dashboard.Tile("k-spend"), dashboard.Tile("k-faith"), dashboard.Tile("k-ls-up"),
				dashboard.Tile("k-seal")),
			dashboard.ConditionalSection("Needs attention",
				dashboard.Full("a-faith")),
		),
		dashboard.Tabbed("Gateway (native)",
			dashboard.Section("Native scrape",
				dashboard.Half("g-rate-code"), dashboard.Half("g-lat")),
			dashboard.Section("Connected gateway spans",
				dashboard.Half("g-edge"), dashboard.Half("g-gwspan")),
		),
		dashboard.Tabbed("LangSmith platform",
			dashboard.Section("Platform health (scrape unlock)",
				dashboard.Half("lh-up"), dashboard.Half("lh-http")),
			dashboard.Section("Datastores",
				dashboard.Half("lh-ch"), dashboard.Half("lh-redis")),
		),
		dashboard.Tabbed("Estate",
			dashboard.Section("Bedrock & services",
				dashboard.Half("e-bedrock"), dashboard.Half("e-spanrate")),
			dashboard.Section("Eval",
				dashboard.Full("e-eval")),
		),
	)
	return d, nil
}
