// SPDX-License-Identifier: AGPL-3.0-only

// PipelineHealth — Acme AI Telemetry Pipeline Health (acme-ai-platform-eval blueprint).
//
// This dashboard is the telemetry-path trust anchor for the connected-gateway scenario:
// Alloy/up pipeline freshness, OTLP span throughput, native gateway scrape health
// (the Portkey gateway is a CONNECTED native scrape here, not API-polled), LangSmith
// platform-component health, content-strip integrity, and k8s cluster sizing.
//
// Difference from acme_ai_platform: the gateway is a CONNECTED native scrape, so the poller
// self-telemetry rows (acme_poller_*) are ABSENT — the predecessor description explicitly
// states "NO poller self-telemetry row (acme_poller_* is empty under the unlock)".
// Instead acme_ai_platform_eval adds: gateway scrape-target health (up{instance=~".*portkey.*"}),
// request_count freshness (Portkey native counter), node-exporter target count,
// and LangSmith platform-health scrape (up/redis_up/pg_up/http_requests_total).
//
// ALL families are substrate-scoped (no blueprint label). The predecessor's
// {scenario="acme_ai_platform_eval"} filter is DROPPED on every substrate query; scope comes
// from env/job/cluster/instance labels instead.
//
// Rename gap: content families use acme.MetricContentLeakTest / MetricContentDropped
// (de-Rochified). Real Acme AI stack: acme_content_leak_test / acme_content_dropped_total.
//
// Tabs: Pipelines & collector · Gateway scrape health · LangSmith platform health · Content & infra.
package acme_ai_platform_eval

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// ws2PipeSel returns a substrate selector for otelcol_* / alloy_* / up pipeline families
// scoped by the $env label (substrate, no blueprint). pipeline filter is optional.
func ws2PipeSel(extra string) string {
	s := `env=~"$env"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// ws2GwSel returns a substrate selector for the Portkey gateway native-scrape families
// (up, request_count) scoped to portkey instances. Portkey carries no blueprint label.
func ws2GwSel(extra string) string {
	s := `env=~"$env",instance=~".*portkey.*"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// ws2LangSmithSel returns a substrate selector for LangSmith platform-health families
// (up, redis_up, pg_up, http_requests_total) scoped to langsmith jobs.
func ws2LangSmithSel(extra string) string {
	s := `env=~"$env",job=~"langsmith-.*"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// ws2K8sSel returns a substrate selector for kube_* / container_* families scoped by $cluster.
func ws2K8sSel(extra string) string {
	s := `cluster=~"$cluster"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// PipelineHealth is the Acme AI Telemetry Pipeline Health dashboard for the acme-ai-platform-eval blueprint.
// uid: acme-ws2-pipeline-health.
// Four tabs reproducing the predecessor's acme-pipeline-health-eval layout for the unlock world.
func PipelineHealth(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard(
		"acme-ws2-pipeline-health",
		"Acme AI-ws2 — Pipeline Health",
	)
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ───────────────────────────────────────────────────────────────────────────
	// scenario: ConstVar — substrate families drop it from selectors, but the seam helpers
	// need it defined so any mixed panels keep working (real Acme AI stack: blueprint→scenario).
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform_eval"))

	// env: manifest-seeded multi-value custom var (substrate scope, env label).
	d.Builder.CustomVariable(dashboard.EnvVar(m))

	// pipeline: label_values from up — substrate, drop predecessor's scenario= filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"pipeline", "Pipeline",
		`label_values(up{env=~"$env", pipeline=~".+"}, pipeline)`))

	// cluster: label_values from kube_node_info — substrate, no scenario filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"cluster", "Cluster",
		`label_values(kube_node_info, cluster)`))

	// ── TAB 1: Pipelines & collector ────────────────────────────────────────────────────────

	// KPI tile strip (predecessor row "Collector KPIs", 4-across):
	//   k-pipe: Pipelines healthy (high-is-good: red→yellow→green)
	dashboard.AddPanel(&d, "pc-pipes-up", dashboard.StatTile(
		"Pipelines healthy", "short",
		dashboard.PromTarget(
			`count(count by (pipeline)(up`+ws2PipeSel(`pipeline=~".+"`)+` == 1)) or vector(0)`,
			""),
		dashboard.Threshold{Value: 0, Color: "red"},
		dashboard.Threshold{Value: 3, Color: "yellow"},
		dashboard.Threshold{Value: 5, Color: "green"}))

	//   k-spans: OTLP spans exported (neutral — higher is expected)
	dashboard.AddPanel(&d, "pc-spans-exported", dashboard.StatTile(
		"OTLP spans exported", "short",
		dashboard.PromTarget(
			`sum(rate(otelcol_exporter_sent_spans_total`+ws2PipeSel("")+`[$__rate_interval]))`,
			"")))

	//   k-leak: Content leak test (0=green, any>0=red sentinel)
	//   real Acme AI stack: acme_content_leak_test
	dashboard.AddPanel(&d, "pc-leak-kpi", dashboard.StatTile(
		"Content leak test", "short", // real Acme AI stack: acme_content_leak_test
		dashboard.PromTarget(
			`max(`+acme.MetricContentLeakTest+`)`,
			""),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 0.001, Color: "red"}))

	//   k-build: Alloy build infos (neutral count)
	dashboard.AddPanel(&d, "pc-alloy-build-kpi", dashboard.StatTile(
		"Alloy build infos", "short",
		dashboard.PromTarget(
			`count(count by (version)(alloy_build_info`+ws2PipeSel("")+`))`,
			"")))

	// Pipeline UP status row (predecessor p-up — state-timeline, full-width):
	dashboard.AddPanel(&d, "pc-pipeline-up", dashboard.StateTimelinePanel(
		"Pipeline UP status",
		dashboard.PromTarget(
			`max by (pipeline)(up`+ws2PipeSel(`pipeline=~"$pipeline"`)+`)`,
			"{{pipeline}}")))

	// Span throughput row (predecessor p-throughput — sent vs failed, full-width):
	dashboard.AddPanel(&d, "pc-span-throughput", dashboard.TimeseriesPanel(
		"Span throughput (exported vs failed)", "short",
		dashboard.PromTarget(
			`sum(rate(otelcol_exporter_sent_spans_total`+ws2PipeSel("")+`[$__rate_interval]))`,
			"sent").RefId("A"),
		dashboard.PromTarget(
			`sum(rate(otelcol_exporter_send_failed_spans_total`+ws2PipeSel("")+`[$__rate_interval]))`,
			"failed").RefId("B")))

	// ── TAB 2: Gateway scrape health (unlock) ────────────────────────────────────────────

	// KPI strip — gateway scrape target health (high-is-good: red→green):
	// predecessor g-up: min(up{instance=~".*portkey.*"}) — native-scrape UP sentinel
	dashboard.AddPanel(&d, "gw-up", dashboard.StatTile(
		"Gateway scrape target", "short",
		dashboard.PromTarget(
			`min(min by (instance)(up`+ws2GwSel("")+`))`,
			""),
		dashboard.Threshold{Value: 0, Color: "red"},
		dashboard.Threshold{Value: 1, Color: "green"}))

	// predecessor g-rate: request_count rate — native scrape "data present = scrape live".
	// request_count is a Portkey native counter (substrate/IntSel, no blueprint label).
	dashboard.AddPanel(&d, "gw-req-rate", dashboard.TimeseriesPanel(
		"Gateway scrape — request_count freshness (rate present = scrape live)", "reqps",
		dashboard.PromTarget(
			`sum(rate(request_count`+acme.IntSel("")+`[$__rate_interval]))`,
			"req/s scraped")))

	// predecessor g-node: node-exporter targets reporting (node_* is fully substrate-scoped,
	// no scenario and no env filter — bare metric, scope by instance count only).
	dashboard.AddPanel(&d, "gw-node-exporters", dashboard.TimeseriesPanel(
		"Node-exporter targets reporting (node_* runtime)", "short",
		dashboard.PromTarget(
			`count(count by (instance)(node_cpu_seconds_total))`,
			"node exporters")))

	// ── TAB 3: LangSmith platform health (unlock) ─────────────────────────────────────────

	// predecessor l-up: LangSmith component UP per job (timeseries, half-width).
	// up{job=~"langsmith-.*"} — substrate, env-scoped; drop scenario= predecessor filter.
	dashboard.AddPanel(&d, "ls-components-up", dashboard.TimeseriesPanel(
		"LangSmith platform components UP", "short",
		dashboard.PromTarget(
			`min by (job)(up`+ws2LangSmithSel("")+`)`,
			"{{job}}")))

	// predecessor l-stores: redis_up / pg_up (timeseries, half-width).
	// Both are langsmith platform-health substrate exporters (no blueprint).
	dashboard.AddPanel(&d, "ls-datastores", dashboard.TimeseriesPanel(
		"Datastore up (redis / pg)", "short",
		dashboard.PromTarget(
			`max(redis_up`+acme.IntSel("")+`)`,
			"redis_up").RefId("A"),
		dashboard.PromTarget(
			`max(pg_up`+acme.IntSel("")+`)`,
			"pg_up").RefId("B")))

	// predecessor l-http: LangSmith platform HTTP 5xx rate (timeseries, full-width).
	// http_requests_total{job=~"langsmith-.*", status_code="500"} — substrate.
	dashboard.AddPanel(&d, "ls-http5xx", dashboard.TimeseriesPanel(
		"LangSmith platform HTTP 5xx rate", "reqps",
		dashboard.PromTarget(
			`sum(rate(http_requests_total`+ws2LangSmithSel(`status_code="500"`)+`[$__rate_interval]))`,
			"5xx/s")))

	// ── TAB 4: Content & infra ────────────────────────────────────────────────────────────

	// KPI strip — content-integrity and delivery:
	//   Content leak must-be-0 (green→red sentinel)
	//   real Acme AI stack: acme_content_leak_test
	dashboard.AddPanel(&d, "ci-leak-kpi", dashboard.StatTile(
		"CONTENT LEAK TEST", "short", // real Acme AI stack: acme_content_leak_test
		dashboard.PromTarget(
			`max(`+acme.MetricContentLeakTest+`)`,
			""),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 0.001, Color: "red"}))

	// Content-leak invariant timeseries (predecessor c-leak, half-width):
	// real Acme AI stack: acme_content_leak_test
	dashboard.AddPanel(&d, "ci-leak-ts", dashboard.TimeseriesPanel(
		"Content-leak invariant (= 0 healthy)", "short", // real Acme AI stack: acme_content_leak_test
		dashboard.PromTarget(
			`max(`+acme.MetricContentLeakTest+`)`,
			"leak")))

	// Content dropped strip events (predecessor c-dropped, half-width):
	// real Acme AI stack: acme_content_dropped_total
	dashboard.AddPanel(&d, "ci-dropped-ts", dashboard.TimeseriesPanel(
		"Content dropped (strip events)", "short", // real Acme AI stack: acme_content_dropped_total
		dashboard.PromTarget(
			`sum(rate(`+acme.MetricContentDropped+`[$__rate_interval]))`,
			"dropped/s")))

	// Cluster sizing — deployment replicas (predecessor i-replicas, full-width):
	// kube_* is substrate (no scenario); scope by $cluster only (drop scenario= predecessor filter).
	dashboard.AddPanel(&d, "ci-replicas-ts", dashboard.TimeseriesPanel(
		"Deployment replicas available (per cluster)", "short",
		dashboard.PromTarget(
			`sum by (cluster)(kube_deployment_status_replicas_available`+ws2K8sSel("")+`)`,
			"{{cluster}}")))

	// ── Layout ────────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("Pipelines & collector",
			dashboard.Section("Collector KPIs",
				dashboard.Tile("pc-pipes-up"),
				dashboard.Tile("pc-spans-exported"),
				dashboard.Tile("pc-leak-kpi"),
				dashboard.Tile("pc-alloy-build-kpi")),
			dashboard.Section("Pipeline UP status",
				dashboard.Full("pc-pipeline-up")),
			dashboard.Section("Span throughput",
				dashboard.Full("pc-span-throughput")),
		),
		dashboard.Tabbed("Gateway scrape health (unlock)",
			dashboard.Section("Native scrape target",
				dashboard.Stat("gw-up"),
				dashboard.TwoThirds("gw-req-rate")),
			dashboard.Section("Node exporters",
				dashboard.Full("gw-node-exporters")),
		),
		dashboard.Tabbed("LangSmith platform health (unlock)",
			dashboard.Section("Components & datastores",
				dashboard.Half("ls-components-up"),
				dashboard.Half("ls-datastores")),
			dashboard.Section("HTTP errors",
				dashboard.Full("ls-http5xx")),
		),
		dashboard.Tabbed("Content & infra",
			dashboard.Section("Content integrity KPI",
				dashboard.Tile("ci-leak-kpi")),
			dashboard.Section("Content invariant",
				dashboard.Half("ci-leak-ts"),
				dashboard.Half("ci-dropped-ts")),
			dashboard.Section("Cluster sizing",
				dashboard.Full("ci-replicas-ts")),
		),
	)
	return d, nil
}
