// SPDX-License-Identifier: AGPL-3.0-only

// ApmRum is the Acme AI APM & RUM dashboard (acme_ai_platform blueprint, predecessor 07-apm-rum).
//
// Three tabs:
//   - Backend (APM): spanmetrics RED + latency quantiles + service-graph edge table + RED summary
//     table + Knowledge Layer & datastore dependency panels (service map, client-span call rates,
//     client-span p95 latency, knowledge-service endpoint RED table).
//   - Frontend (RUM): Faro web-vitals KPI stats + p75 timeseries (LCP/INP/CLS/TTFB/FCP) + rating
//     distribution + event rate + exceptions table + live frontend trace list.
//   - Sessions & Navigation: multi-page session analytics — page-view rates, top pages, navigation
//     flow (from→to view), top user actions, HTTP error rate, per-page web vitals, session event
//     stream.
//
// Scope notes (load-bearing):
//   - traces_spanmetrics_* and traces_service_graph_* are Tempo-metrics-generator-derived; they carry
//     NO blueprint label and are scoped by deployment_environment + service (hand-written selectors,
//     keyed on $deployment_environment and $service_name template vars).
//   - Faro RUM streams are emitted via the real Grafana Faro Collector, which owns the Loki labels.
//     Collector streams carry ONLY the 6 collector labels (app_id, app_key, deployment_environment,
//     kind, service_name, service_namespace) — NO blueprint label. Selectors scope by service_name +
//     kind + deployment_environment only.
//   - The 3 fabricated Prometheus gauges (largest_contentful_paint / cumulative_layout_shift /
//     interaction_to_next_paint) have been removed from emit — all web-vital views use Loki unwrap.
//
// traces_spanmetrics_latency: treated as a NATIVE histogram (no _bucket suffix, no le grouping) —
// the predecessor's panel description and query form both confirm native (histogram_quantile over the bare
// series). Uses NativeHistogramQuantile accordingly. Requires the blueprint to be added to
// span_metrics_blueprints via the control plane (CONFIG-GATED, not a construct gap).
//
// Variables rewritten per scope rule: both QueryVariables query traces_spanmetrics_calls_total with
// NO blueprint/scenario filter (metrics-generator-derived).
package acme_ai_platform

import (
	dashboardv2 "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"

	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// ApmRum builds the Acme AI APM & RUM dashboard for the acme_ai_platform blueprint.
func ApmRum(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws1-apm-rum", "Acme AI — APM & RUM")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ───────────────────────────────────────────────────────────────────────────────
	// scenario: hidden const var so acme.AppSel($scenario) resolves.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform"))
	// deployment_environment: metrics-generator family carries NO blueprint → drop scenario filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"deployment_environment", "Environment",
		"label_values(traces_spanmetrics_calls_total, deployment_environment_name)"))
	// service_name: same — drop scenario filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"service_name", "Service",
		"label_values(traces_spanmetrics_calls_total, service)"))

	// smSel builds a selector for traces_spanmetrics_* (NO blueprint label) filtered by the two
	// template vars and an optional server-span kind restriction. extra is appended (no leading comma).
	smSel := func(serverOnly bool, extra string) string {
		base := `deployment_environment_name=~"$deployment_environment",service=~"$service_name"`
		if serverOnly {
			base += `,span_kind="SPAN_KIND_SERVER"`
		}
		if extra != "" {
			base += "," + extra
		}
		return "{" + base + "}"
	}

	// sgSel builds a selector for traces_service_graph_* (NO blueprint label).
	sgSel := func() string {
		return `{deployment_environment_name=~"$deployment_environment"}`
	}

	// ── Backend (APM) tab ────────────────────────────────────────────────────────────────────────

	// KPI stats — StatTile for colored-background health tiles
	dashboard.AddPanel(&d, "apm-stat-rate",
		dashboard.StatTile("Request rate (server spans)", "reqps",
			dashboard.PromTarget(
				`sum(rate(traces_spanmetrics_calls_total`+smSel(true, "")+`[$__rate_interval]))`,
				"req/s")))

	dashboard.AddPanel(&d, "apm-stat-err",
		dashboard.StatTile("Error rate %", "percent",
			dashboard.PromTarget(
				`100 * sum(rate(traces_spanmetrics_calls_total`+
					smSel(true, `status_code="STATUS_CODE_ERROR"`)+
					`[$__rate_interval])) / clamp_min(sum(rate(traces_spanmetrics_calls_total`+
					smSel(true, "")+`[$__rate_interval])), 0.001)`,
				"Error %"),
			dashboard.Threshold{Value: 0, Color: "green"},
			dashboard.Threshold{Value: 1, Color: "yellow"},
			dashboard.Threshold{Value: 5, Color: "red"}))

	dashboard.AddPanel(&d, "apm-stat-p95",
		dashboard.StatTile("p95 latency (all services)", "s",
			dashboard.PromTarget(
				// native histogram: histogram_quantile over bare series (no _bucket, no le)
				dashboard.NativeHistogramQuantile(0.95, "traces_spanmetrics_latency", smSel(true, ""), nil),
				"p95"),
			dashboard.Threshold{Value: 0, Color: "green"},
			dashboard.Threshold{Value: 0.5, Color: "yellow"},
			dashboard.Threshold{Value: 2, Color: "red"}))

	// Per-service RED timeseries
	dashboard.AddPanel(&d, "apm-rate-svc",
		dashboard.TimeseriesPanel("Request rate by service (server spans)", "reqps",
			dashboard.PromTarget(
				dashboard.RateExpr("traces_spanmetrics_calls_total", smSel(true, ""), []string{"service"}),
				"{{service}}")))

	dashboard.AddPanel(&d, "apm-err-svc",
		dashboard.TimeseriesPanel("Error rate % by service", "percent",
			dashboard.PromTarget(
				`100 * sum by (service)(rate(traces_spanmetrics_calls_total`+
					smSel(true, `status_code="STATUS_CODE_ERROR"`)+
					`[$__rate_interval])) / clamp_min(sum by (service)(rate(traces_spanmetrics_calls_total`+
					smSel(true, "")+`[$__rate_interval])), 0.001)`,
				"{{service}}")))

	// Latency quantiles by service (native histogram)
	dashboard.AddPanel(&d, "apm-lat-svc",
		dashboard.TimeseriesPanel("p50 / p95 / p99 latency by service (s)", "s",
			dashboard.PromTarget(
				dashboard.NativeHistogramQuantile(0.50, "traces_spanmetrics_latency", smSel(true, ""), []string{"service"}),
				"p50 · {{service}}").RefId("A"),
			dashboard.PromTarget(
				dashboard.NativeHistogramQuantile(0.95, "traces_spanmetrics_latency", smSel(true, ""), []string{"service"}),
				"p95 · {{service}}").RefId("B"),
			dashboard.PromTarget(
				dashboard.NativeHistogramQuantile(0.99, "traces_spanmetrics_latency", smSel(true, ""), []string{"service"}),
				"p99 · {{service}}").RefId("C")))

	// Latency distribution heatmap (native histogram — full shape over time)
	dashboard.AddPanel(&d, "apm-lat-heatmap",
		dashboard.NativeHistogramHeatmap(
			"Request latency distribution (native histogram)", "s",
			dashboard.PromTarget(
				dashboard.NativeHistogramRate("traces_spanmetrics_latency", smSel(true, "")),
				"")))

	// Service-graph edges table (client → server, rate + failed rate)
	dashboard.AddPanel(&d, "apm-svcgraph",
		dashboard.TablePanel("Service graph edges (client → server)",
			dashboard.PromTarget(
				dashboard.RateExpr("traces_service_graph_request_total", sgSel(), []string{"client", "server"}),
				"{{client}} → {{server}}")))

	// RED summary (per-service instant snapshot) — 3-target merge table: rate (A), error % (B), p95 (C).
	// Grouped by service. spanmetrics status_code="STATUS_CODE_ERROR" is the error discriminator;
	// latency uses NativeHistogramQuantile (native exponential histogram, no _bucket/le).
	dashboard.AddPanel(&d, "apm-red-table",
		dashboard.MergeTablePanel("Service RED summary",
			[]*dashboardv2.TargetBuilder{
				dashboard.PromTableTarget(
					`sum by (service)(rate(traces_spanmetrics_calls_total`+smSel(true, "")+`[$__rate_interval]))`,
					"A"),
				dashboard.PromTableTarget(
					`100 * sum by (service)(rate(traces_spanmetrics_calls_total`+
						smSel(true, `status_code="STATUS_CODE_ERROR"`)+
						`[$__rate_interval])) / (sum by (service)(rate(traces_spanmetrics_calls_total`+
						smSel(true, "")+`[$__rate_interval])) + 1)`,
					"B"),
				dashboard.PromTableTarget(
					dashboard.NativeHistogramQuantile(0.95, "traces_spanmetrics_latency", smSel(true, ""), []string{"service"}),
					"C"),
			},
			dashboard.OrganizeOptions{
				Rename:  map[string]string{"Value #A": "Rate (rps)", "Value #B": "Error %", "Value #C": "p95 (ms)"},
				Exclude: []string{"Time"},
				Order:   []string{"service", "Rate (rps)", "Error %", "p95 (ms)"},
			},
		))

	// ── Dependency panels (Backend APM tab — Knowledge Layer & datastore section) ────────────────

	// Service map — Tempo service graph rendered as a node-graph panel.
	dashboard.AddPanel(&d, "dep-nodegraph",
		dashboard.NodeGraphPanel("Service map — estate topology (Tempo service graph)",
			dashboard.ServiceMapTarget()))

	// Client-span call rate: surfaces "call rds-acl", "call documentdb-store",
	// "call opensearch-vectors", "call knowledge-layer-api", "call acme-backend", etc.
	dashboard.AddPanel(&d, "dep-call-rate",
		dashboard.TimeseriesPanel("Dependency call rate (client spans) by target", "reqps",
			dashboard.PromTarget(
				`sum by (span_name)(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"$deployment_environment",span_kind="SPAN_KIND_CLIENT",span_name=~"call .+"}[$__rate_interval]))`,
				"{{span_name}}")))

	// Client-span p95 latency for the leaf DB hops (rds-acl, documentdb-store, opensearch-vectors).
	// Uses NativeHistogramQuantile — no _bucket/le (native exponential histogram).
	dashboard.AddPanel(&d, "dep-call-lat",
		dashboard.TimeseriesPanel("Dependency call p95 latency (client spans) by target", "s",
			dashboard.PromTarget(
				dashboard.NativeHistogramQuantile(0.95, "traces_spanmetrics_latency",
					`{deployment_environment_name=~"$deployment_environment",span_kind="SPAN_KIND_CLIENT",span_name=~"call rds-acl|call documentdb-store|call opensearch-vectors"}`,
					[]string{"span_name"}),
				"{{span_name}}")))

	// Knowledge-service endpoint RED (3-target merge table: rate A, error% B, p95 C, grouped by span_name).
	// kbSel is the base selector for knowledge-service server spans — factored out for the three targets below.
	kbSel := `{deployment_environment_name=~"$deployment_environment",service="knowledge-layer-api",span_kind="SPAN_KIND_SERVER"}`
	dashboard.AddPanel(&d, "dep-kb-red",
		dashboard.MergeTablePanel("Knowledge-service endpoint RED",
			[]*dashboardv2.TargetBuilder{
				dashboard.PromTableTarget(
					`sum by (span_name)(rate(traces_spanmetrics_calls_total`+kbSel+`[$__rate_interval]))`,
					"A"),
				dashboard.PromTableTarget(
					`100 * sum by (span_name)(rate(traces_spanmetrics_calls_total`+
						`{deployment_environment_name=~"$deployment_environment",service="knowledge-layer-api",span_kind="SPAN_KIND_SERVER",status_code="STATUS_CODE_ERROR"}`+
						`[$__rate_interval])) / (sum by (span_name)(rate(traces_spanmetrics_calls_total`+kbSel+`[$__rate_interval])) + 1)`,
					"B"),
				dashboard.PromTableTarget(
					dashboard.NativeHistogramQuantile(0.95, "traces_spanmetrics_latency", kbSel, []string{"span_name"}),
					"C"),
			},
			dashboard.OrganizeOptions{
				Rename:  map[string]string{"Value #A": "Rate (rps)", "Value #B": "Error %", "Value #C": "p95 (s)"},
				Exclude: []string{"Time"},
				Order:   []string{"span_name", "Rate (rps)", "Error %", "p95 (s)"},
			},
		))

	// ── Frontend (RUM) tab ───────────────────────────────────────────────────────────────────────

	// faroMeasurement: Loki stream selector for Faro kind=measurement.
	// Collector-owned streams: no blueprint label — scope by service_name + kind + deployment_environment.
	faroMeas := `{service_name="acme-frontend",kind="measurement",deployment_environment=~"$deployment_environment"}`

	// KPI stats (from Faro Loki streams — p75 over 5m window as in predecessor) — StatTile with web-vitals thresholds
	// LCP: good < 2500 ms (2.5 s), needs-improvement < 4000 ms (4 s), poor ≥ 4000 ms — low is good.
	dashboard.AddPanel(&d, "rum-stat-lcp",
		dashboard.StatTile("p75 LCP (RUM)", "s",
			dashboard.LokiTarget(
				`quantile_over_time(0.75, `+faroMeas+` | logfmt | lcp > 0 | unwrap lcp [5m]) by () / 1000`,
				"LCP p75 (s)"),
			dashboard.Threshold{Value: 0, Color: "green"},
			dashboard.Threshold{Value: 2.5, Color: "yellow"},
			dashboard.Threshold{Value: 4, Color: "red"}))

	// INP: good < 200 ms, needs-improvement < 500 ms, poor ≥ 500 ms — low is good.
	dashboard.AddPanel(&d, "rum-stat-inp",
		dashboard.StatTile("p75 INP (RUM)", "ms",
			dashboard.LokiTarget(
				`quantile_over_time(0.75, `+faroMeas+` | logfmt | inp > 0 | unwrap inp [5m]) by ()`,
				"INP p75 (ms)"),
			dashboard.Threshold{Value: 0, Color: "green"},
			dashboard.Threshold{Value: 200, Color: "yellow"},
			dashboard.Threshold{Value: 500, Color: "red"}))

	dashboard.AddPanel(&d, "rum-stat-exc",
		dashboard.StatTile("RUM exception rate", "reqps",
			dashboard.LokiTarget(
				`sum(rate({service_name="acme-frontend",kind="exception",deployment_environment=~"$deployment_environment"}[5m]))`,
				"exc/s"),
			dashboard.Threshold{Value: 0, Color: "green"},
			dashboard.Threshold{Value: 0.1, Color: "yellow"},
			dashboard.Threshold{Value: 1, Color: "red"}))

	// Web-vitals timeseries (p75 via Loki unwrap, $__auto window)
	dashboard.AddPanel(&d, "rum-lcp-ts",
		dashboard.TimeseriesPanel("LCP p75 (s) — Largest Contentful Paint", "s",
			dashboard.LokiTarget(
				`quantile_over_time(0.75, `+faroMeas+` | logfmt | lcp > 0 | unwrap lcp [$__auto]) by () / 1000`,
				"LCP p75")))

	dashboard.AddPanel(&d, "rum-inp-ts",
		dashboard.TimeseriesPanel("INP p75 (ms) — Interaction to Next Paint", "ms",
			dashboard.LokiTarget(
				`quantile_over_time(0.75, `+faroMeas+` | logfmt | inp > 0 | unwrap inp [$__auto]) by ()`,
				"INP p75")))

	dashboard.AddPanel(&d, "rum-cls-ts",
		dashboard.TimeseriesPanel("CLS p75 — Cumulative Layout Shift", "short",
			dashboard.LokiTarget(
				`quantile_over_time(0.75, `+faroMeas+` | logfmt | cls > 0 | unwrap cls [$__auto]) by ()`,
				"CLS p75")))

	dashboard.AddPanel(&d, "rum-ttfb-ts",
		dashboard.TimeseriesPanel("TTFB p75 (ms) — Time to First Byte", "ms",
			dashboard.LokiTarget(
				`quantile_over_time(0.75, `+faroMeas+` | logfmt | ttfb > 0 | unwrap ttfb [$__auto]) by ()`,
				"TTFB p75")))

	dashboard.AddPanel(&d, "rum-fcp-ts",
		dashboard.TimeseriesPanel("FCP p75 (ms) — First Contentful Paint", "ms",
			dashboard.LokiTarget(
				`quantile_over_time(0.75, `+faroMeas+` | logfmt | fcp > 0 | unwrap fcp [$__auto]) by ()`,
				"FCP p75")))

	// Web Vitals rating distribution (count by context_rating — good/needs-improvement/poor)
	dashboard.AddPanel(&d, "rum-rating",
		dashboard.TimeseriesPanel("Web Vitals rating distribution", "short",
			dashboard.LokiTarget(
				`sum by (context_rating)(count_over_time(`+faroMeas+` | logfmt | context_rating != "" [$__auto]))`,
				"{{context_rating}}")))

	// Faro event rate by event_name
	dashboard.AddPanel(&d, "rum-events",
		dashboard.TimeseriesPanel("RUM event rate by event_name", "short",
			dashboard.LokiTarget(
				`sum by (event_name)(count_over_time({service_name="acme-frontend",kind="event",deployment_environment=~"$deployment_environment"} | logfmt | event_name != "" [$__auto]))`,
				"{{event_name}}")))

	// Frontend exceptions table (type × env, count over range)
	dashboard.AddPanel(&d, "rum-exc-table",
		dashboard.TablePanel("Frontend exceptions — type × env",
			dashboard.LokiTarget(
				`sum by (type, deployment_environment)(count_over_time({service_name="acme-frontend",kind="exception",deployment_environment=~"$deployment_environment"} | logfmt | type != "" [$__range]))`,
				"")))

	// Live frontend traces via Tempo TraceQL
	dashboard.AddPanel(&d, "rum-traces",
		dashboard.TraceTablePanel(
			`Live traces — acme-frontend (TraceQL)`,
			acme.DSTempo,
			dashboard.TempoTableTarget(`{ resource.service.name = "acme-frontend" }`, 20)))

	// Faro RUM raw logs panel
	dashboard.AddPanel(&d, "rum-logs",
		dashboard.LogsPanel("Faro RUM logs (acme-frontend)",
			dashboard.LokiTarget(
				`{service_name="acme-frontend"}`,
				"")))

	// ── Sessions & Navigation tab ────────────────────────────────────────────────────────────────

	// faroEvent: Loki stream selector for Faro kind=event (session/nav/action/fetch events).
	// Collector-owned streams — no blueprint label.
	faroEvent := `{service_name="acme-frontend",kind="event",deployment_environment=~"$deployment_environment"}`

	// Page-view rate timeseries — top 8 views by view_changed events.
	dashboard.AddPanel(&d, "nav-pageviews-ts",
		dashboard.TimeseriesPanel("Page-view rate by page", "short",
			dashboard.LokiTarget(
				`topk(8, sum by (view_name)(count_over_time(`+faroEvent+` | logfmt | event_name="view_changed" [$__auto])))`,
				"{{view_name}}")))

	// Top pages by page-view volume (page_id + view_name, over the full selected range).
	dashboard.AddPanel(&d, "nav-top-pages",
		dashboard.TablePanel("Top pages by page-view (page_id / view_name)",
			dashboard.LokiInstantTarget(
				`sum by (view_name, page_id)(count_over_time(`+faroEvent+` | logfmt | event_name=~"view_changed|session_start" [$__range]))`,
				"")))

	// Navigation flow: from→to view transitions, aggregated over the range.
	dashboard.AddPanel(&d, "nav-flow",
		dashboard.TablePanel("Session navigation flow (from → to view)",
			dashboard.LokiInstantTarget(
				`sum by (event_data_fromView, event_data_toView)(count_over_time(`+faroEvent+` | logfmt | event_name="view_changed" [$__range]))`,
				"")))

	// Top user actions (action_name from faro.user.action events, top 20 by count).
	dashboard.AddPanel(&d, "nav-top-actions",
		dashboard.TablePanel("Top user actions",
			dashboard.LokiInstantTarget(
				`topk(20, sum by (action_name)(count_over_time(`+faroEvent+` | logfmt | event_name="faro.user.action" [$__range])))`,
				"")))

	// User-action HTTP error rate: fraction of faro.tracing.fetch events with 4xx/5xx status.
	dashboard.AddPanel(&d, "nav-action-errrate",
		dashboard.StatTile("User-action HTTP error rate", "percent",
			dashboard.LokiInstantTarget(
				`100 * sum(count_over_time(`+faroEvent+` | logfmt | event_name="faro.tracing.fetch" | event_data_http_status_code=~"4..|5.." [$__range])) / sum(count_over_time(`+faroEvent+` | logfmt | event_name="faro.tracing.fetch" [$__range]))`,
				"err %"),
			dashboard.Threshold{Value: 0, Color: "green"},
			dashboard.Threshold{Value: 1, Color: "yellow"},
			dashboard.Threshold{Value: 5, Color: "red"}))

	// Per-page LCP p75 — split by view_name, uses faroMeas (kind=measurement).
	dashboard.AddPanel(&d, "nav-vitals-lcp-page",
		dashboard.TimeseriesPanel("Per-page LCP p75 (s)", "s",
			dashboard.LokiTarget(
				`quantile_over_time(0.75, `+faroMeas+` | logfmt | lcp > 0 | unwrap lcp [$__auto]) by (view_name) / 1000`,
				"{{view_name}}")))

	// Per-page INP p75 — split by view_name, uses faroMeas (kind=measurement).
	dashboard.AddPanel(&d, "nav-vitals-inp-page",
		dashboard.TimeseriesPanel("Per-page INP p75 (ms)", "ms",
			dashboard.LokiTarget(
				`quantile_over_time(0.75, `+faroMeas+` | logfmt | inp > 0 | unwrap inp [$__auto]) by (view_name)`,
				"{{view_name}}")))

	// Session navigation event stream — session_start + view_changed events as a live log panel.
	dashboard.AddPanel(&d, "nav-sessions-logs",
		dashboard.LogsPanel("Session navigation events (session_start / view_changed)",
			dashboard.LokiTarget(
				`{service_name="acme-frontend",kind="event",deployment_environment=~"$deployment_environment"} | logfmt | event_name=~"session_start|view_changed"`,
				"")))

	dashboard.WithTabs(&d,
		dashboard.Tabbed("Backend (APM)",
			dashboard.Section("Service-level KPIs",
				dashboard.Tile("apm-stat-rate"),
				dashboard.Tile("apm-stat-err"),
				dashboard.Tile("apm-stat-p95")),
			dashboard.Section("RED — rate & errors by service",
				dashboard.Half("apm-rate-svc"),
				dashboard.Half("apm-err-svc")),
			dashboard.Section("Latency",
				dashboard.Full("apm-lat-svc"),
				dashboard.Full("apm-lat-heatmap")),
			dashboard.Section("Service graph & RED summary",
				dashboard.Half("apm-svcgraph"),
				dashboard.Half("apm-red-table")),
			dashboard.Section("Knowledge Layer & datastore dependencies",
				dashboard.Full("dep-nodegraph"),
				dashboard.Half("dep-call-rate"),
				dashboard.Half("dep-call-lat"),
				dashboard.Full("dep-kb-red")),
		),
		dashboard.Tabbed("Frontend (RUM)",
			dashboard.Section("Web Vitals KPIs",
				dashboard.Tile("rum-stat-lcp"),
				dashboard.Tile("rum-stat-inp"),
				dashboard.Tile("rum-stat-exc")),
			dashboard.Section("Core Web Vitals — p75 timeseries",
				dashboard.Half("rum-lcp-ts"),
				dashboard.Half("rum-inp-ts"),
				dashboard.Half("rum-cls-ts"),
				dashboard.Half("rum-ttfb-ts"),
				dashboard.Half("rum-fcp-ts"),
				dashboard.Half("rum-rating")),
			dashboard.Section("Events & exceptions",
				dashboard.Half("rum-events"),
				dashboard.Full("rum-exc-table")),
			dashboard.Section("Traces & logs",
				dashboard.Tall("rum-traces"),
				dashboard.Tall("rum-logs")),
		),
		dashboard.Tabbed("Sessions & Navigation",
			dashboard.Section("Navigation overview",
				dashboard.Half("nav-pageviews-ts"),
				dashboard.Stat("nav-action-errrate")),
			dashboard.Section("Pages & flow",
				dashboard.Half("nav-top-pages"),
				dashboard.Half("nav-flow")),
			dashboard.Section("User actions",
				dashboard.Full("nav-top-actions")),
			dashboard.Section("Per-page Web Vitals",
				dashboard.Half("nav-vitals-lcp-page"),
				dashboard.Half("nav-vitals-inp-page")),
			dashboard.Section("Session event stream",
				dashboard.Tall("nav-sessions-logs")),
		),
	)
	return d, nil
}
