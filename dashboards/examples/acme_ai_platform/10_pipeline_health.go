// SPDX-License-Identifier: AGPL-3.0-only

// PipelineHealth — Acme AI Telemetry Pipeline Health (acme-ai-platform blueprint).
//
// This dashboard is the operator's trust anchor: it surfaces collector self-telemetry
// (otelcol_* / alloy_*), Firehose delivery health (aws_firehose_*, CWGauge), EKS/k8s
// infra (kube_*, container_*), content-strip integrity (MetricContentLeakTest /
// MetricContentDropped), and poller freshness (MetricPollerLastOK / MetricPollerErrors /
// MetricPollerWindowLag).
//
// ALL families are substrate-scoped (no blueprint label). The predecessor's
// {scenario="$scenario"} filter is DROPPED on every substrate query; scope comes from
// job/cluster/namespace/account_id/pipeline labels instead.
//
// Alertlist gap: the predecessor has one `alertlist` panel (panel-95, "Acme AI Mock Alerts")
// sourced from a Grafana alert folder (uid ) that is not provisioned on
// the example stack and the builder has no alertlist panel type. The panel is REPLACED with a
// TextPanel documenting this and pointing to the poller-staleness panels nearby as the
// nearest functional substitute.
//
// Rename gap: the predecessor queries acme_* metric names (content_leak_test,
// content_dropped_total, poller_*). Synthkit emits de-Rochified names — use the seam
// constants (acme.MetricContentLeakTest / MetricContentDropped / MetricPollerLastOK /
// MetricPollerErrors / MetricPollerWindowLag) so panels populate on the example stack. Comments
// mark the real Acme AI stack swap.
//
// Tabs: Pipelines & collector · Content & record-path · Infra (7 clusters / accounts) · Poller (API-pull).
package acme_ai_platform

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// pipeSel returns a substrate selector scoped to the Alloy job and optional pipeline filter.
// otelcol_* and alloy_* families carry job="integrations/alloy" and pipeline=<name>.
func pipeSel(extra string) string {
	s := `job="integrations/alloy",pipeline=~"$pipeline"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// fireSel returns a substrate selector for aws_firehose_* (CW gauge, no blueprint label).
func fireSel(extra string) string {
	s := `account_id=~"$account"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// k8sSel returns a substrate selector scoped by $cluster (kube_*/container_* families).
func k8sSel(extra string) string {
	s := `cluster=~"$cluster"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// ec2Sel returns a substrate CW selector for aws_ec2_* scoped by account_id.
func ec2Sel(extra string) string {
	s := `account_id=~"$account"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// PipelineHealth is the Acme AI Telemetry Pipeline Health dashboard for the acme-ai-platform blueprint.
// uid: acme-ws1-pipeline-health.
// Four tabs reproducing the predecessor's 10-pipeline-health layout (minus the unprovisionable alertlist).
func PipelineHealth(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard(
		"acme-ws1-pipeline-health",
		"Acme AI — Telemetry Pipeline Health",
	)
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ───────────────────────────────────────────────────────────────────────────
	// scenario: ConstVar — substrate families drop it from selectors, but the seam helpers
	// need it defined for any mixed panels that share the AppSel helper.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform"))

	// pipeline: label_values from up{job="integrations/alloy"} — substrate, no scenario filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"pipeline", "Pipeline",
		`label_values(up{job="integrations/alloy", pipeline=~".+"}, pipeline)`))

	// cluster: label_values from kube_node_info — substrate.
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"cluster", "Cluster (EKS)",
		`label_values(kube_node_info, cluster)`))

	// account: label_values from aws_firehose — substrate, no scenario filter (drop predecessor's scenario= filter).
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"account", "Account (CW)",
		`label_values(aws_firehose_delivery_to_http_endpoint_success_average, account_id)`))

	// ── TAB 1: Pipelines & collector ────────────────────────────────────────────────────────

	// KPI tile strip — high-is-good: pipelines UP (red→yellow→green); low-is-good: refused/failed/queue (green→yellow→red).

	// Predecessor panel-2: Pipelines UP / 5 (high-is-good: red→yellow→green)
	dashboard.AddPanel(&d, "pc-pipelines-up", dashboard.StatTile(
		"Pipelines UP / 5", "short",
		dashboard.PromTarget(
			`count(count by (pipeline) (up{job="integrations/alloy", pipeline=~".+"} == 1))`,
			""),
		dashboard.Threshold{Value: 0, Color: "red"},
		dashboard.Threshold{Value: 3, Color: "yellow"},
		dashboard.Threshold{Value: 5, Color: "green"}))

	// Predecessor panel-3: Span ingest rate (neutral — higher is expected, no bad direction)
	dashboard.AddPanel(&d, "pc-span-ingest", dashboard.StatTile(
		"Span ingest rate", "short",
		dashboard.PromTarget(
			`sum(rate(otelcol_receiver_accepted_spans_total`+pipeSel("")+`[$__rate_interval]))`,
			"")))

	// Predecessor panel-4: Refused spans rate (low-is-good: green→yellow→red)
	dashboard.AddPanel(&d, "pc-refused", dashboard.StatTile(
		"Refused spans rate", "short",
		dashboard.PromTarget(
			`sum(rate(otelcol_receiver_refused_spans_total`+pipeSel("")+`[$__rate_interval]))`,
			""),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 0.5, Color: "yellow"},
		dashboard.Threshold{Value: 2, Color: "red"}))

	// Predecessor panel-5: Export failed rate (low-is-good: green→yellow→red)
	dashboard.AddPanel(&d, "pc-export-failed", dashboard.StatTile(
		"Export failed rate", "short",
		dashboard.PromTarget(
			`sum(rate(otelcol_exporter_send_failed_spans_total`+pipeSel("")+`[$__rate_interval]))`,
			""),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 0.1, Color: "yellow"},
		dashboard.Threshold{Value: 1, Color: "red"}))

	// Predecessor panel-6: Exporter queue size (low-is-good: green→yellow→red)
	dashboard.AddPanel(&d, "pc-queue-stat", dashboard.StatTile(
		"Exporter queue size", "short",
		dashboard.PromTarget(
			`sum(otelcol_exporter_queue_size`+pipeSel("")+`)`,
			""),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 500, Color: "yellow"},
		dashboard.Threshold{Value: 1000, Color: "red"}))

	// Row: Pipeline UP status
	// Predecessor panel-11: Per-pipeline UP status (table — builder uses TablePanel)
	dashboard.AddPanel(&d, "pc-pipeline-up-table", dashboard.TablePanel(
		"Per-pipeline UP status",
		dashboard.PromTarget(
			`max by (pipeline) (up{job="integrations/alloy", pipeline=~"$pipeline"})`,
			"{{pipeline}}")))

	// Predecessor panel-12: Per-pipeline up status (timeline)
	dashboard.AddPanel(&d, "pc-pipeline-up-ts", dashboard.TimeseriesPanel(
		"Per-pipeline up status (timeline)", "short",
		dashboard.PromTarget(
			`min by (pipeline) (up{job="integrations/alloy", pipeline=~"$pipeline"})`,
			"{{pipeline}}")))

	// Row: Span throughput & queue
	// Predecessor panel-21: Span throughput accepted/sent/refused/failed
	dashboard.AddPanel(&d, "pc-span-throughput", dashboard.TimeseriesPanel(
		"Span throughput: accepted vs sent vs refused vs failed", "short",
		dashboard.PromTarget(
			`sum(rate(otelcol_receiver_accepted_spans_total`+pipeSel("")+`[$__rate_interval]))`,
			"accepted").RefId("A"),
		dashboard.PromTarget(
			`sum(rate(otelcol_exporter_sent_spans_total`+pipeSel("")+`[$__rate_interval]))`,
			"sent").RefId("B"),
		dashboard.PromTarget(
			`sum(rate(otelcol_receiver_refused_spans_total`+pipeSel("")+`[$__rate_interval]))`,
			"refused").RefId("C"),
		dashboard.PromTarget(
			`sum(rate(otelcol_exporter_send_failed_spans_total`+pipeSel("")+`[$__rate_interval]))`,
			"failed").RefId("D")))

	// Predecessor panel-22: Exporter queue size (timeseries)
	dashboard.AddPanel(&d, "pc-queue-ts", dashboard.TimeseriesPanel(
		"Exporter queue size", "short",
		dashboard.PromTarget(
			`sum by (exporter) (otelcol_exporter_queue_size`+pipeSel("")+`)`,
			"queue · {{exporter}}")))

	// ── TAB 2: Content & record-path ────────────────────────────────────────────────────────

	// KPI tile strip

	// Predecessor panel-7: CONTENT LEAK TEST (0=green any>0=red — content-leak sentinel)
	// real Acme AI stack: acme_content_leak_test
	dashboard.AddPanel(&d, "cr-leak-test", dashboard.StatTile(
		"CONTENT LEAK TEST", "short", // real Acme AI stack: acme_content_leak_test
		dashboard.PromTarget(
			`max(`+acme.MetricContentLeakTest+`)`,
			""),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "red"}))

	// Predecessor panel-41: Firehose delivery success (high-is-good: red→yellow→green)
	dashboard.AddPanel(&d, "cr-fire-success-stat", dashboard.StatTile(
		"Firehose delivery success", "percentunit",
		dashboard.PromTarget(
			`avg(aws_firehose_delivery_to_http_endpoint_success_average`+fireSel("")+`)`,
			""),
		dashboard.Threshold{Value: 0, Color: "red"},
		dashboard.Threshold{Value: 0.95, Color: "yellow"},
		dashboard.Threshold{Value: 0.99, Color: "green"}))

	// Predecessor panel-43: Firehose data freshness (low-is-good: green→yellow→red — staleness in seconds)
	dashboard.AddPanel(&d, "cr-fire-freshness", dashboard.StatTile(
		"Firehose data freshness (avg)", "s",
		dashboard.PromTarget(
			`avg(aws_firehose_delivery_to_http_endpoint_data_freshness_average`+fireSel("")+`)`,
			""),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 60, Color: "yellow"},
		dashboard.Threshold{Value: 300, Color: "red"}))

	// Predecessor panel-31: Content leak test — per pipeline (stat)
	// real Acme AI stack: acme_content_leak_test
	dashboard.AddPanel(&d, "cr-leak-per-pipeline", dashboard.TablePanel(
		"Content leak test — per pipeline", // real Acme AI stack: acme_content_leak_test
		dashboard.PromTarget(
			`max by (pipeline) (`+acme.MetricContentLeakTest+`)`,
			"{{pipeline}}")))

	// Predecessor panel-32: Content fields stripped rate
	// real Acme AI stack: acme_content_dropped_total
	dashboard.AddPanel(&d, "cr-stripped-rate", dashboard.TimeseriesPanel(
		"Content fields stripped rate", "short", // real Acme AI stack: acme_content_dropped_total
		dashboard.PromTarget(
			`sum by (pipeline) (rate(`+acme.MetricContentDropped+`[$__rate_interval]))`,
			"stripped · {{pipeline}}")))

	// Predecessor panel-42: Firehose delivery success per account (timeline)
	dashboard.AddPanel(&d, "cr-fire-success-ts", dashboard.TimeseriesPanel(
		"Firehose delivery success per account (timeline)", "percentunit",
		dashboard.PromTarget(
			`avg by (account_id) (aws_firehose_delivery_to_http_endpoint_success_average`+fireSel("")+`)`,
			"account {{account_id}}")))

	// ── TAB 3: Collector / Series (renamed from "Infra (7 clusters / accounts)") ─────────────

	// KPI tile strip — cluster sizing stats
	// Predecessor panel-61: Node count per cluster (stat tile)
	dashboard.AddPanel(&d, "inf-nodes-stat", dashboard.StatTile(
		"Node count per cluster", "short",
		dashboard.PromTarget(
			`count by (cluster) (count by (cluster, node) (kube_node_info`+k8sSel("")+`))`,
			"{{cluster}}")))

	// Predecessor panel-81: k8s series per cluster (stat tile)
	dashboard.AddPanel(&d, "inf-k8s-series-stat", dashboard.StatTile(
		"k8s series per cluster (kube_node_info)", "short",
		dashboard.PromTarget(
			`count by (cluster) (kube_node_info`+k8sSel("")+`)`,
			"{{cluster}}")))

	// Predecessor panel-62: Node count per cluster (timeline)
	dashboard.AddPanel(&d, "inf-nodes-ts", dashboard.TimeseriesPanel(
		"Node count per cluster (timeline)", "short",
		dashboard.PromTarget(
			`count by (cluster) (count by (cluster, node) (kube_node_info`+k8sSel("")+`))`,
			"{{cluster}}")))

	// Row: Replicas & resource use
	// Predecessor panel-63: Deployment available replicas per cluster (table)
	dashboard.AddPanel(&d, "inf-replicas-table", dashboard.TablePanel(
		"Deployment available replicas per cluster",
		dashboard.PromTarget(
			`kube_deployment_status_replicas_available`+k8sSel("")+``,
			"")))

	// Predecessor panel-64: Container CPU usage by cluster
	dashboard.AddPanel(&d, "inf-cpu-ts", dashboard.TimeseriesPanel(
		"Container CPU usage by cluster", "cores",
		dashboard.PromTarget(
			`sum by (cluster) (rate(container_cpu_usage_seconds_total`+k8sSel(`container!=""`)+`[$__rate_interval]))`,
			"{{cluster}}")))

	// Predecessor panel-65: Container memory (working set) by cluster
	dashboard.AddPanel(&d, "inf-mem-ts", dashboard.TimeseriesPanel(
		"Container memory (working set) by cluster", "bytes",
		dashboard.PromTarget(
			`sum by (cluster) (container_memory_working_set_bytes`+k8sSel(`container!=""`)+`)`,
			"{{cluster}}")))

	// Row: AWS infra (per account)
	// Predecessor panel-71: EC2 CPU utilisation by account (CWGauge)
	dashboard.AddPanel(&d, "inf-ec2-cpu", dashboard.TimeseriesPanel(
		"EC2 CPU utilisation by account", "percent",
		dashboard.PromTarget(
			`avg by (account_id) (aws_ec2_cpuutilization_average`+ec2Sel("")+`)`,
			"account {{account_id}}")))

	// Predecessor panel-72: NAT gateway active connections by account (CWGauge _sum, gauge)
	dashboard.AddPanel(&d, "inf-nat-conn", dashboard.TimeseriesPanel(
		"NAT gateway active connections by account", "short",
		dashboard.PromTarget(
			`sum by (account_id) (aws_natgateway_active_connection_count_sum`+ec2Sel("")+`)`,
			"account {{account_id}}")))

	// Row: Alloy build & total series
	// Predecessor panel-51: Alloy build info (table)
	dashboard.AddPanel(&d, "inf-alloy-build", dashboard.TablePanel(
		"Alloy build info",
		dashboard.PromTarget(
			`count by (version, revision, goversion) (alloy_build_info{job="integrations/alloy"})`,
			"")))

	// Predecessor panel-82: k8s-monitoring total series over time (all clusters)
	dashboard.AddPanel(&d, "inf-k8s-series-ts", dashboard.TimeseriesPanel(
		"k8s-monitoring total series over time (all clusters)", "short",
		dashboard.PromTarget(
			`count(kube_node_info)`,
			"total kube_node_info series (all clusters)")))

	// ── TAB 4: Infrastructure (EKS/AWS) ─────────────────────────────────────────────────────

	// Row A — Cluster health (StatTiles)

	// infra-nodes: total node count per cluster
	dashboard.AddPanel(&d, "infra-nodes", dashboard.StatTile(
		"Nodes", "short",
		dashboard.PromTarget(
			`count by (cluster) (count by (cluster, node) (kube_node_info{cluster=~"$cluster"}))`,
			"{{cluster}}")))

	// infra-notready: nodes NOT ready (red > 0)
	dashboard.AddPanel(&d, "infra-notready", dashboard.StatTile(
		"Nodes NotReady", "short",
		dashboard.PromTarget(
			`count by (cluster) (kube_node_status_condition{cluster=~"$cluster",condition="Ready",status="false"}) or vector(0)`,
			"{{cluster}}"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "red"}))

	// infra-pods-nr: pods not ready per cluster (red > 0)
	dashboard.AddPanel(&d, "infra-pods-nr", dashboard.StatTile(
		"Pods not Ready", "short",
		dashboard.PromTarget(
			`sum by (cluster) (kube_pod_status_ready{cluster=~"$cluster",condition="false"}) or vector(0)`,
			"{{cluster}}"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "red"}))

	// infra-restarts: container restart rate per cluster
	dashboard.AddPanel(&d, "infra-restarts", dashboard.StatTile(
		"Container restarts/s", "short",
		dashboard.PromTarget(
			`sum by (cluster) (rate(kube_pod_container_status_restarts_total{cluster=~"$cluster"}[$__rate_interval]))`,
			"{{cluster}}")))

	// infra-running: running pod count per cluster
	dashboard.AddPanel(&d, "infra-running", dashboard.StatTile(
		"Running pods", "short",
		dashboard.PromTarget(
			`sum by (cluster) (kube_pod_status_phase{cluster=~"$cluster",phase="Running"})`,
			"{{cluster}}")))

	// Row B — Node utilisation (TimeseriesPanels)

	// infra-cpu: node CPU utilisation % by cluster + node
	dashboard.AddPanel(&d, "infra-cpu", dashboard.TimeseriesPanel(
		"Node CPU %", "percent",
		dashboard.PromTarget(
			`100 * (1 - avg by (cluster,node) (rate(node_cpu_seconds_total{cluster=~"$cluster",mode="idle"}[$__rate_interval])))`,
			"{{cluster}} / {{node}}")))

	// infra-mem: node memory available % by cluster + node
	dashboard.AddPanel(&d, "infra-mem", dashboard.TimeseriesPanel(
		"Node memory available %", "percent",
		dashboard.PromTarget(
			`100 * avg by (cluster,node) (node_memory_MemAvailable_bytes{cluster=~"$cluster"} / node_memory_MemTotal_bytes{cluster=~"$cluster"})`,
			"{{cluster}} / {{node}}")))

	// infra-cpureq: pod CPU requests vs allocatable (0–1 ratio)
	dashboard.AddPanel(&d, "infra-cpureq", dashboard.TimeseriesPanel(
		"Pod CPU requests vs allocatable", "percentunit",
		dashboard.PromTarget(
			`sum by (cluster)(kube_pod_container_resource_requests{cluster=~"$cluster",resource="cpu"}) / sum by (cluster)(kube_node_status_allocatable{cluster=~"$cluster",resource="cpu"})`,
			"{{cluster}}")))

	// Row C — EKS control plane (CloudWatch CW gauges — never rate()/increase())

	// infra-api5xx: EKS API server 5xx errors (CW _sum gauge)
	dashboard.AddPanel(&d, "infra-api5xx", dashboard.TimeseriesPanel(
		"API server 5xx", "short",
		dashboard.PromTarget(
			`sum by (dimension_ClusterName)(aws_eks_apiserver_request_total_5_xx_sum{account_id=~"$account"})`,
			"{{dimension_ClusterName}}")))

	// infra-apireq: EKS API server total requests (CW _sum gauge)
	dashboard.AddPanel(&d, "infra-apireq", dashboard.TimeseriesPanel(
		"API server requests", "short",
		dashboard.PromTarget(
			`sum by (dimension_ClusterName)(aws_eks_apiserver_request_total_sum{account_id=~"$account"})`,
			"{{dimension_ClusterName}}")))

	// infra-pending: scheduler pending pods (CW _maximum gauge — red > 0)
	dashboard.AddPanel(&d, "infra-pending", dashboard.StatTile(
		"Scheduler pending pods", "short",
		dashboard.PromTarget(
			`max by (dimension_ClusterName)(aws_eks_scheduler_pending_pods_maximum{account_id=~"$account"}) or vector(0)`,
			"{{dimension_ClusterName}}"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "red"}))

	// infra-etcd: etcd DB size (CW _average gauge)
	dashboard.AddPanel(&d, "infra-etcd", dashboard.TimeseriesPanel(
		"etcd DB size", "bytes",
		dashboard.PromTarget(
			`avg by (dimension_ClusterName)(aws_eks_etcd_mvcc_db_total_size_in_bytes_average{account_id=~"$account"})`,
			"{{dimension_ClusterName}}")))

	// Row D — Incidents (blueprint pod_crashloop / node_not_ready / oom_kill)

	// infra-oom: OOMKilled rate (spikes only during oom_kill incidents)
	dashboard.AddPanel(&d, "infra-oom", dashboard.TimeseriesPanel(
		"OOMKilled /s", "short",
		dashboard.PromTarget(
			`sum by (cluster)(rate(kube_pod_container_status_last_terminated_reason{cluster=~"$cluster",reason="OOMKilled"}[$__rate_interval]))`,
			"{{cluster}}")))

	// infra-crashloop: CrashLoopBackOff containers (red > 0)
	dashboard.AddPanel(&d, "infra-crashloop", dashboard.StatTile(
		"CrashLoopBackOff containers", "short",
		dashboard.PromTarget(
			`sum by (cluster)(kube_pod_container_status_waiting_reason{cluster=~"$cluster",reason="CrashLoopBackOff"}) or vector(0)`,
			"{{cluster}}"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "red"}))

	// Row E — AWS networking (CloudWatch CW gauges — _sum is per-period gauge, divide by 60 for rate)

	// infra-alb-rate: ALB request rate (requests per second — _sum gauge ÷ 60 s period)
	dashboard.AddPanel(&d, "infra-alb-rate", dashboard.TimeseriesPanel(
		"ALB request rate", "reqps",
		dashboard.PromTarget(
			`sum by (account_id)(aws_applicationelb_request_count_sum{account_id=~"$account"}) / 60`,
			"{{account_id}}")))

	// infra-alb-5xx: ALB 5xx error %
	dashboard.AddPanel(&d, "infra-alb-5xx", dashboard.TimeseriesPanel(
		"ALB 5xx %", "percent",
		dashboard.PromTarget(
			`100 * sum by (account_id)(aws_applicationelb_httpcode_target_5_xx_count_sum{account_id=~"$account"}) / (sum by (account_id)(aws_applicationelb_request_count_sum{account_id=~"$account"}) + 1)`,
			"{{account_id}}")))

	// infra-alb-unhealthy: ALB unhealthy targets (red > 0)
	dashboard.AddPanel(&d, "infra-alb-unhealthy", dashboard.StatTile(
		"ALB unhealthy targets", "short",
		dashboard.PromTarget(
			`max by (account_id)(aws_applicationelb_un_healthy_host_count_maximum{account_id=~"$account"}) or vector(0)`,
			"{{account_id}}"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "red"}))

	// infra-nat: NAT bytes out to destination (CW _sum gauge)
	dashboard.AddPanel(&d, "infra-nat", dashboard.TimeseriesPanel(
		"NAT bytes out", "bytes",
		dashboard.PromTarget(
			`sum by (account_id)(aws_natgateway_bytes_out_to_destination_sum{account_id=~"$account"})`,
			"{{account_id}}")))

	// ── TAB 5: Poller (API-pull self-telemetry) ─────────────────────────────────────────────

	// KPI tile strip — poller health

	// Predecessor panel-91: Poller staleness per API (low-is-good: green→yellow→red, seconds)
	// real Acme AI stack: acme_poller_last_success_timestamp_seconds
	dashboard.AddPanel(&d, "po-staleness-stat", dashboard.StatTile(
		"Poller staleness per API (s)", "s", // real Acme AI stack: acme_poller_last_success_timestamp_seconds
		dashboard.PromTarget(
			`time() - `+acme.MetricPollerLastOK,
			"{{api}}"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 300, Color: "yellow"},
		dashboard.Threshold{Value: 900, Color: "red"}))

	// Predecessor panel-92: Poller staleness trend by API (timeseries)
	// real Acme AI stack: acme_poller_last_success_timestamp_seconds
	dashboard.AddPanel(&d, "po-staleness-ts", dashboard.TimeseriesPanel(
		"Poller staleness trend by API (s)", "s", // real Acme AI stack: acme_poller_last_success_timestamp_seconds
		dashboard.PromTarget(
			`time() - `+acme.MetricPollerLastOK,
			"staleness · {{api}}")))

	// Row: Window lag & API errors
	// Predecessor panel-93: Poller window lag by API
	// real Acme AI stack: acme_poller_window_lag_seconds
	dashboard.AddPanel(&d, "po-window-lag", dashboard.TimeseriesPanel(
		"Poller window lag by API (s)", "s", // real Acme AI stack: acme_poller_window_lag_seconds
		dashboard.PromTarget(
			acme.MetricPollerWindowLag,
			"window-lag · {{api}}")))

	// Predecessor panel-94: Poller API error rate
	// real Acme AI stack: acme_poller_api_errors_total
	dashboard.AddPanel(&d, "po-errors", dashboard.TimeseriesPanel(
		"Poller API error rate (per api)", "short", // real Acme AI stack: acme_poller_api_errors_total
		dashboard.PromTarget(
			`rate(`+acme.MetricPollerErrors+`[$__rate_interval])`,
			"errors · {{api}}")))

	// Predecessor panel-95 (alertlist → SUBSTITUTE):
	// The predecessor has one alertlist panel sourcing "Acme AI Mock Alerts" folder (uid ).
	// The builder has no alertlist panel type, and that alert folder is not provisioned on the example stack.
	// SUBSTITUTE: a TextPanel noting the gap and pointing to poller-staleness panels as proxy signal.
	dashboard.AddPanel(&d, "po-alerts-stub", dashboard.TextPanel(
		"Alerts",
		"## Alert rules — separate provisioning workstream\n\nThe original dashboard includes a live alertlist panel sourced from the **Acme AI Mock Alerts** folder (Grafana folder uid ``). That folder is not provisioned on the example stack and the builder DSL has no alertlist panel type.\n\n**Proxy signal while alert rules are pending:** use the *Poller staleness per API* stat panel above (green < 5 min, red > 15 min) and the *Poller API error rate* timeseries as the nearest equivalent gate on poller-driven signal freshness. Firehose delivery success on the **Content & record-path** tab covers the metric-stream path.\n\nAlert rules are tracked as a separate provisioning workstream."))

	// ── Layout ────────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("Pipelines & collector",
			dashboard.Section("Collector KPIs",
				dashboard.Tile("pc-pipelines-up"), dashboard.Tile("pc-span-ingest"),
				dashboard.Tile("pc-refused"), dashboard.Tile("pc-export-failed"),
				dashboard.Tile("pc-queue-stat")),
			dashboard.Section("Pipeline UP status",
				dashboard.Tall("pc-pipeline-up-table"), dashboard.Full("pc-pipeline-up-ts")),
			dashboard.Section("Span throughput & queue",
				dashboard.Full("pc-span-throughput"), dashboard.Half("pc-queue-ts")),
		),
		dashboard.Tabbed("Content & record-path",
			dashboard.Section("Content & delivery KPIs",
				dashboard.Tile("cr-leak-test"), dashboard.Tile("cr-fire-success-stat"),
				dashboard.Tile("cr-fire-freshness")),
			dashboard.Section("Content-leak invariant",
				dashboard.Tall("cr-leak-per-pipeline"), dashboard.Half("cr-stripped-rate")),
			dashboard.Section("Firehose delivery",
				dashboard.Full("cr-fire-success-ts")),
		),
		dashboard.Tabbed("Collector / Series",
			dashboard.Section("Cluster sizing",
				dashboard.Tile("inf-nodes-stat"), dashboard.Tile("inf-k8s-series-stat"),
				dashboard.Full("inf-nodes-ts")),
			dashboard.Section("Replicas & resource use",
				dashboard.Tall("inf-replicas-table"),
				dashboard.Half("inf-cpu-ts"), dashboard.Half("inf-mem-ts")),
			dashboard.Section("AWS infra (per account)",
				dashboard.Half("inf-ec2-cpu"), dashboard.Half("inf-nat-conn")),
			dashboard.Section("Alloy build & total series",
				dashboard.Tall("inf-alloy-build"), dashboard.Full("inf-k8s-series-ts")),
		),
		dashboard.Tabbed("Infrastructure (EKS/AWS)",
			dashboard.Section("Cluster health",
				dashboard.Tile("infra-nodes"), dashboard.Tile("infra-notready"),
				dashboard.Tile("infra-pods-nr"), dashboard.Tile("infra-restarts"),
				dashboard.Tile("infra-running")),
			dashboard.Section("Node utilisation",
				dashboard.Half("infra-cpu"), dashboard.Half("infra-mem"),
				dashboard.Full("infra-cpureq")),
			dashboard.Section("EKS control plane (CloudWatch)",
				dashboard.Half("infra-api5xx"), dashboard.Half("infra-apireq"),
				dashboard.Tile("infra-pending"), dashboard.Half("infra-etcd")),
			dashboard.Section("Incidents",
				dashboard.Half("infra-oom"), dashboard.Tile("infra-crashloop")),
			dashboard.Section("AWS networking (CloudWatch)",
				dashboard.Half("infra-alb-rate"), dashboard.Half("infra-alb-5xx"),
				dashboard.Tile("infra-alb-unhealthy"), dashboard.Half("infra-nat")),
		),
		dashboard.Tabbed("Poller (API-pull)",
			dashboard.Section("Poller health KPIs",
				dashboard.Tile("po-staleness-stat")),
			dashboard.Section("Staleness & window lag",
				dashboard.Half("po-staleness-ts"), dashboard.Half("po-window-lag")),
			dashboard.Section("API errors & alerts",
				dashboard.Half("po-errors"), dashboard.Half("po-alerts-stub")),
		),
	)
	return d, nil
}
