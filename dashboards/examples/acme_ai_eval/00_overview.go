// SPDX-License-Identifier: AGPL-3.0-only

// Package eval_standalone holds the customer dashboards for the eval_standalone (acme-ai-eval)
// blueprint — the AI-gateway PLATFORM OPERATOR's view across an 8-cell estate (4 AWS account
// roles × 2 regions: acme-{develop,devint,test,prod}-{usw2,euw3}).
//
// This is the landing page for the Acme AI Eval platform team: gateway health, LangSmith platform
// API health, per-cell EKS/k8s inventory, and the per-account AWS estate (EC2 + ALB).
//
// TENANT-STRIPPED: no use_case, workspace, correlation, or tenant business identity appears
// anywhere in this dashboard. The platform view is the gateway itself + the substrate estate.
//
// Scope discipline (load-bearing — wrong scope = empty panel):
//
//   - Portkey NATIVE gateway scrape families (request_count, llm_request_duration_milliseconds,
//     portkey_request_*): SUBSTRATE-scoped (no blueprint label) → gwSel (declared in
//     portkey_gateway.go; package-wide).  env label = acme-eval-<role>-<region> cell name.
//
//   - LangSmith platform exporter families (http_requests_total, job=langsmith-*): SUBSTRATE-
//     scoped → lsAllSel (declared in langsmith_platform.go; package-wide; all langsmith-* jobs).
//
//   - k8s families (kube_node_info, kube_node_status_condition, kube_pod_status_phase):
//     SUBSTRATE-scoped (never carry blueprint); disambiguated by cluster=~"acme-eks.*"
//     (the real AI Evaluation EKS cluster name pattern). No env filter needed — cluster covers it.
//
//   - CW metric-stream families (aws_eks_info, aws_ec2_cpuutilization_average,
//     aws_applicationelb_*): BLUEPRINT-scoped (carry blueprint label) → cwSel (this file).
//     The predecessor's {scenario="$scenario"} = synthkit's {blueprint="acme-ai-eval"}.
//
// Tab list: Overview (single tab — the predecessor has only one tab).
package acme_ai_eval

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// evalClusters is the cluster name pattern for all 8 AI Evaluation EKS cells.
// k8s families are substrate-scoped; we filter by cluster name, not blueprint.
const evalClusters = `acme-eks.*`

// cwSel returns a selector for AWS CloudWatch metric-stream families (BLUEPRINT-scoped).
// The predecessor's {scenario="$scenario"} = synthkit's {blueprint="acme-ai-eval"}.
// extra is an already-formatted matcher list (no leading comma).
func cwSel(extra string) string {
	s := `blueprint="` + blueprintName + `"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

const blueprintName = "acme-ai-eval"

// Overview is the AI Evaluation Platform Overview dashboard for the acme-ai-eval blueprint.
// uid: acme-eval-overview. Single tab reproducing the predecessor's acme-eval-overview layout.
//
// Panel scope notes:
//   - kpi-clusters: aws_eks_info — CW blueprint-scoped → cwSel. (predecessor: scenario="$scenario")
//   - kpi-nodes, kpi-failed, kpi-pods, ts-pods-cluster: kube_* — k8s substrate, cluster-filtered.
//   - kpi-gw-rate, kpi-gw-err, ts-gw-rate-env: request_count — portkey native substrate → gwSel.
//   - kpi-gw-p99, ts-gw-lat: llm_request_duration_milliseconds — portkey native substrate → gwSel
//     (same env-only scope; _bucket suffix is part of the metric name, not the selector).
//   - kpi-ls-http, ts-ls-http: http_requests_total{job=~"langsmith-.*"} — substrate → lsAllSel.
//   - ts-ec2-cpu: aws_ec2_cpuutilization_average — CW blueprint-scoped → cwSel.
//   - ts-elb-5xx: aws_applicationelb_httpcode_target_5_xx_count_sum — CW blueprint-scoped → cwSel.
//     _sum CW metrics are per-period(60s) GAUGES → /60, never rate().
func Overview(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ai-eval-overview", "Acme AI Eval — Overview")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ───────────────────────────────────────────────────────────────────────────
	// scenario: ConstVar pinned to "acme-ai-eval" — used by the shared acme.AppSel seam
	// for any gen_ai_client_* / http_server_* panels added in the future (unused here since
	// this is the PLATFORM view; included per the shared seam contract).
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme-ai-eval"))
	// env: multi-select across the 8 AI Evaluation cells (acme-{develop,devint,test,prod}-{usw2,euw3}).
	// Substrate families (portkey native + LangSmith) use env=~"$env".
	d.Builder.CustomVariable(dashboard.EnvVar(m))

	// ── Tab 1: Overview ─────────────────────────────────────────────────────────────────────

	// ── Section: Platform health KPIs ────────────────────────────────────────────────────────
	// 8 KPI tiles in a dense strip — estate inventory + gateway health at a glance.

	// kpi-clusters: AI Evaluation EKS clusters visible via CW (aws_eks_info is blueprint-scoped).
	// predecessor used {scenario="$scenario"} — synthkit emits blueprint= on CW; swap here.
	dashboard.AddPanel(&d, "kpi-clusters", dashboard.StatTile("Acme AI Eval EKS clusters", "short",
		dashboard.PromTarget(
			"count(aws_eks_info"+cwSel("")+")",
			"EKS clusters")))

	// kpi-nodes: Total worker nodes across all 8 AI Evaluation cells (k8s substrate, cluster-filtered).
	// predecessor note: "k8s node inventory is scenario-LESS — filtered by cluster, not scenario".
	dashboard.AddPanel(&d, "kpi-nodes", dashboard.StatTile("Worker nodes", "short",
		dashboard.PromTarget(
			`count(kube_node_info{cluster=~"`+evalClusters+`"})`,
			"Nodes")))

	// kpi-failed: Nodes in NotReady condition — 0 = healthy, >0 = attention needed.
	dashboard.AddPanel(&d, "kpi-failed", dashboard.StatTile("Failed nodes", "short",
		dashboard.PromTarget(
			`sum(kube_node_status_condition{cluster=~"`+evalClusters+`",condition="Ready",status="false"})`,
			"Failed nodes"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "red"}))

	// kpi-gw-rate: Portkey gateway request rate (request_count is a Counter on the native scrape;
	// substrate-scoped by env label = the 8 AI Evaluation cells). DROP scenario=.
	dashboard.AddPanel(&d, "kpi-gw-rate", dashboard.StatTile("Gateway request rate", "reqps",
		dashboard.PromTarget(
			"sum(rate(request_count"+gwSel("")+"[$__rate_interval]))",
			"Requests/s")))

	// kpi-gw-err: Non-2xx gateway requests as % of total (request_count code label).
	// substrate-scoped. DROP scenario=.
	dashboard.AddPanel(&d, "kpi-gw-err", dashboard.StatTile("Gateway error rate", "percent",
		dashboard.PromTarget(
			`100 * sum(rate(request_count`+gwSel(`code!~"2.."`)+"[$__rate_interval])) / clamp_min(sum(rate(request_count"+gwSel("")+"[$__rate_interval])), 0.001)",
			"Error %"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "yellow"}, dashboard.Threshold{Value: 5, Color: "red"}))

	// kpi-gw-p99: p99 LLM round-trip from llm_request_duration_milliseconds histogram (ms).
	// SUBSTRATE (portkey native scrape). Classic histogram (_bucket suffix). DROP scenario=.
	dashboard.AddPanel(&d, "kpi-gw-p99", dashboard.StatTile("Gateway p99 latency", "ms",
		dashboard.PromTarget(
			"histogram_quantile(0.99, sum by (le) (rate(llm_request_duration_milliseconds_bucket"+gwSel("")+"[$__rate_interval])))",
			"p99 (ms)"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 3000, Color: "yellow"}, dashboard.Threshold{Value: 8000, Color: "red"}))

	// kpi-pods: Running Portkey/LangSmith component pods across AI Evaluation clusters.
	// k8s substrate (cluster-filtered). predecessor note: "k8s pod inventory is scenario-LESS".
	dashboard.AddPanel(&d, "kpi-pods", dashboard.StatTile("Component pods (Running)", "short",
		dashboard.PromTarget(
			`sum(kube_pod_status_phase{cluster=~"`+evalClusters+`",phase="Running"})`,
			"Pods running")))

	// kpi-ls-http: LangSmith platform HTTP request rate (http_requests_total, job=langsmith-*).
	// SUBSTRATE. DROP scenario=; scope by env + job prefix.
	// lsAllSel declared in langsmith_platform.go (package-wide): env=~"$env",job=~"langsmith-.*".
	dashboard.AddPanel(&d, "kpi-ls-http", dashboard.StatTile("LangSmith API req rate", "reqps",
		dashboard.PromTarget(
			"sum(rate(http_requests_total"+lsAllSel("")+"[$__rate_interval]))",
			"LangSmith req/s")))

	// ── Section: Gateway ─────────────────────────────────────────────────────────────────────
	// request rate by cell + latency quantile fan — the gateway health at cell resolution.

	// ts-gw-rate-env: Request rate split by env (= the 8 AI Evaluation cells). Substrate-scoped.
	dashboard.AddPanel(&d, "ts-gw-rate-env", dashboard.TimeseriesPanel("Gateway request rate by cell", "reqps",
		dashboard.PromTarget(
			"sum by (env) (rate(request_count"+gwSel("")+"[$__rate_interval]))",
			"{{env}}")))

	// ts-gw-lat: LLM round-trip p50/p95/p99 from the native histogram. Substrate-scoped.
	dashboard.AddPanel(&d, "ts-gw-lat", dashboard.TimeseriesPanel("Gateway latency (p50/p95/p99)", "ms",
		dashboard.PromTarget(
			"histogram_quantile(0.50, sum by (le) (rate(llm_request_duration_milliseconds_bucket"+gwSel("")+"[$__rate_interval])))",
			"p50").RefId("A"),
		dashboard.PromTarget(
			"histogram_quantile(0.95, sum by (le) (rate(llm_request_duration_milliseconds_bucket"+gwSel("")+"[$__rate_interval])))",
			"p95").RefId("B"),
		dashboard.PromTarget(
			"histogram_quantile(0.99, sum by (le) (rate(llm_request_duration_milliseconds_bucket"+gwSel("")+"[$__rate_interval])))",
			"p99").RefId("C")))

	// ── Section: Kubernetes & LangSmith ──────────────────────────────────────────────────────
	// pod inventory by cluster + LangSmith service breakdown side-by-side.

	// ts-pods-cluster: Running component pods by AI Evaluation cluster. k8s substrate.
	dashboard.AddPanel(&d, "ts-pods-cluster", dashboard.TimeseriesPanel("Running pods by cluster", "short",
		dashboard.PromTarget(
			`sum by (cluster) (kube_pod_status_phase{cluster=~"`+evalClusters+`",phase="Running"})`,
			"{{cluster}}")))

	// ts-ls-http: LangSmith HTTP rate per service (job=langsmith-*). Substrate-scoped.
	// lsAllSel declared in langsmith_platform.go (package-wide): env=~"$env",job=~"langsmith-.*".
	dashboard.AddPanel(&d, "ts-ls-http", dashboard.TimeseriesPanel("LangSmith HTTP rate by service", "reqps",
		dashboard.PromTarget(
			"sum by (job) (rate(http_requests_total"+lsAllSel("")+"[$__rate_interval]))",
			"{{job}}")))

	// ── Section: AWS estate ───────────────────────────────────────────────────────────────────
	// EC2 CPU + ALB 5xx by account — the per-account AWS footprint view.
	// CW metric-stream: blueprint-scoped (blueprint="acme-ai-eval"). /60 because _sum and
	// _average CW series are per-period(60s) gauges, NOT rate-able.

	// ts-ec2-cpu: Average EC2 CPU utilisation per account. CW _average → avg().
	// predecessor: avg by (account_id) (aws_ec2_cpuutilization_average{scenario="$scenario"})
	// → synthkit CW carries blueprint=, not scenario=; swap here.
	dashboard.AddPanel(&d, "ts-ec2-cpu", dashboard.TimeseriesPanel("EC2 CPU by account", "percent",
		dashboard.PromTarget(
			"avg by (account_id) (aws_ec2_cpuutilization_average"+cwSel("")+")",
			"{{account_id}}")))

	// ts-elb-5xx: ALB target 5xx per second per account. CW _sum → /60 (per-period GAUGE).
	// predecessor: sum by (account_id) (aws_applicationelb_httpcode_target_5_xx_count_sum{scenario=}) / 60
	// → swap scenario= for blueprint=.
	dashboard.AddPanel(&d, "ts-elb-5xx", dashboard.TimeseriesPanel("ALB target 5xx rate by account", "reqps",
		dashboard.PromTarget(
			"sum by (account_id) (aws_applicationelb_httpcode_target_5_xx_count_sum"+cwSel("")+") / 60",
			"{{account_id}}")))

	// ── Layout ────────────────────────────────────────────────────────────────────────────────
	// Single tab with four named sections: KPI strip → Gateway pair → K8s+LangSmith pair → AWS pair.
	// Uses the full Cell vocabulary: Tile (4w) for the KPI strip, Half (12w) for all chart pairs.
	dashboard.WithTabs(&d,
		dashboard.Tabbed("Overview",
			dashboard.Section("Platform health KPIs",
				dashboard.Tile("kpi-clusters"), dashboard.Tile("kpi-nodes"), dashboard.Tile("kpi-failed"),
				dashboard.Tile("kpi-gw-rate"), dashboard.Tile("kpi-gw-err"), dashboard.Tile("kpi-gw-p99"),
				dashboard.Tile("kpi-pods"), dashboard.Tile("kpi-ls-http")),
			dashboard.Section("Gateway",
				dashboard.Half("ts-gw-rate-env"), dashboard.Half("ts-gw-lat")),
			dashboard.Section("Kubernetes & LangSmith",
				dashboard.Half("ts-pods-cluster"), dashboard.Half("ts-ls-http")),
			dashboard.Section("AWS estate",
				dashboard.Half("ts-ec2-cpu"), dashboard.Half("ts-elb-5xx")),
		),
	)

	_ = acme.DSTempo // import used for the shared seam; no Tempo panels in this overview

	return d, nil
}
