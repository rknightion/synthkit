// SPDX-License-Identifier: AGPL-3.0-only

// AWS — AI Evaluation AWS Estate (acme-ai-eval blueprint).
//
// Visualises the platform operator's 8-cell AWS estate (4 account roles × 2 regions):
// EKS control-plane health, EC2 compute, ALB load-balancing, NAT/EBS networking + storage,
// and the Firehose metric-stream pipeline watching itself.
//
// ALL aws_* CloudWatch families are SUBSTRATE-SCOPED (no blueprint label) on the
// acme-ai-eval blueprint. The predecessor's {scenario="$scenario"} filter is DROPPED
// for every CW query. Scope is instead by account_id=~"$account" and region=~"$region".
//
// CW law (never rate() a _sum; _sum is a per-period GAUGE → /60 for per-second):
//   - _sum  → gauge, divide by 60 for per-second rate
//   - _average → avg()
//   - _maximum → max() (not used in panels; available for alerting)
//
// kube_* families (EKS node count, node conditions) are k8s-monitoring sourced — NO
// blueprint label and NOT account-scoped (scope by cluster name instead).
//
// Tabs: EKS · EC2 / Compute · Load Balancing · Networking & Storage · Pipeline Health
package acme_ai_eval

import (
	"github.com/rknightion/synthkit/dashboard"

	dashboardv2 "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// awsSel returns a raw label-matcher selector for aws_* CloudWatch infra families in the
// acme-ai-eval estate, scoped by account_id + region (substrate-scoped: no blueprint
// label here — the CW infra lane drops the predecessor's {scenario=} filter per the acme-ai-eval spec).
// extra is an already-formatted matcher list (no leading comma), e.g.
// `dimension_ClusterName=~"acme-eks.*"`.
func awsSel(extra string) string {
	s := `account_id=~"$account",region=~"$region"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// AWS is the AI Evaluation AWS Estate dashboard for the acme-ai-eval blueprint.
// uid: acme-eval-aws. Five tabs: EKS · EC2 / Compute · Load Balancing ·
// Networking & Storage · Pipeline Health.
func AWS(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ai-eval-aws", "Acme AI Eval — AWS")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ─────────────────────────────────────────────────────────────────────────
	// scenario: constant pin (acme-ai-eval blueprint); referenced by app-scoped helpers
	// in mixed dashboards — kept here for seam consistency even though CW panels drop it.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme-ai-eval"))

	// account: manifest-seeded custom var (4 account IDs from the blueprint estate).
	// Seeded from acme-ai-eval accounts: 200000000001 / 200000000002 / 200000000003 / 200000000004.
	d.Builder.CustomVariable(dashboard.AccountVar(m))

	// region: label_values from aws_eks_apiserver_request_total — substrate, no {scenario=} filter.
	// Scoped to the eval estate accounts so it returns only the two real regions (us-west-2, eu-west-3).
	d.Builder.QueryVariable(dashboard.LabelValuesVar("region", "Region",
		`label_values(aws_eks_apiserver_request_total{account_id=~"$account"}, region)`))

	// ── Tab 1: EKS ────────────────────────────────────────────────────────────────────────

	// KPI tiles — EKS control-plane health at a glance.
	dashboard.AddPanel(&d, "eks-api-rps", dashboard.StatTile("API server RPS", "reqps",
		dashboard.PromTarget(
			`sum(aws_eks_apiserver_request_total_sum`+awsSel(`dimension_ClusterName=~"acme-eks.*"`)+`) / 60`,
			"API req/s")))

	dashboard.AddPanel(&d, "eks-api-4xx", dashboard.StatTile("API server 4xx rate", "reqps",
		dashboard.PromTarget(
			`sum(aws_eks_apiserver_request_total_4_xx_sum`+awsSel(`dimension_ClusterName=~"acme-eks.*"`)+`) / 60`,
			"4xx/s"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 0.5, Color: "yellow"},
		dashboard.Threshold{Value: 2, Color: "red"}))

	dashboard.AddPanel(&d, "eks-api-5xx", dashboard.StatTile("API server 5xx rate", "reqps",
		dashboard.PromTarget(
			`sum(aws_eks_apiserver_request_total_5_xx_sum`+awsSel(`dimension_ClusterName=~"acme-eks.*"`)+`) / 60`,
			"5xx/s"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 0.1, Color: "yellow"},
		dashboard.Threshold{Value: 0.5, Color: "red"}))

	dashboard.AddPanel(&d, "eks-pending-pods", dashboard.StatTile("Pending pods", "short",
		dashboard.PromTarget(
			`sum(aws_eks_scheduler_pending_pods_average`+awsSel(`dimension_ClusterName=~"acme-eks.*"`)+`)`,
			"pending pods"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 5, Color: "yellow"},
		dashboard.Threshold{Value: 20, Color: "red"}))

	dashboard.AddPanel(&d, "eks-etcd-size", dashboard.StatTile("etcd DB size", "bytes",
		dashboard.PromTarget(
			`avg(aws_eks_etcd_mvcc_db_total_size_in_bytes_average`+awsSel(`dimension_ClusterName=~"acme-eks.*"`)+`)`,
			"etcd size")))

	// kube_node_info panels — k8s-monitoring sourced (NO blueprint label, scope by cluster pattern).
	// KNOWN GAP: predecessor filters by `cluster=~"acme-eks.*"` (hardcoded k8s cluster names), not
	// account/region. This is intentional: kube_* carry no account_id/region CW labels.
	dashboard.AddPanel(&d, "eks-node-count", dashboard.TimeseriesPanel("EKS node count by cluster", "short",
		dashboard.PromTarget(
			`count by (cluster) (kube_node_info{cluster=~"acme-eks.*"})`,
			"{{cluster}}")))

	dashboard.AddPanel(&d, "eks-failed-nodes", dashboard.TimeseriesPanel("Unhealthy nodes (Ready=false)", "short",
		dashboard.PromTarget(
			`sum by (cluster) (kube_node_status_condition{cluster=~"acme-eks.*",condition="Ready",status="false"})`,
			"{{cluster}}")))

	// API server request rate and error rate by cluster — time series.
	dashboard.AddPanel(&d, "eks-api-rate-ts", dashboard.TimeseriesPanel("API server request rate by cluster", "reqps",
		dashboard.PromTarget(
			`sum by (dimension_ClusterName) (aws_eks_apiserver_request_total_sum`+awsSel(`dimension_ClusterName=~"acme-eks.*"`)+`) / 60`,
			"{{dimension_ClusterName}}")))

	dashboard.AddPanel(&d, "eks-api-latency-ts", dashboard.TimeseriesPanel("API server GET p99 latency by cluster", "s",
		dashboard.PromTarget(
			`avg by (dimension_ClusterName) (aws_eks_apiserver_request_duration_seconds_get_p99_average`+awsSel(`dimension_ClusterName=~"acme-eks.*"`)+`)`,
			"{{dimension_ClusterName}}")))

	dashboard.AddPanel(&d, "eks-etcd-size-ts", dashboard.TimeseriesPanel("etcd DB size by cluster", "bytes",
		dashboard.PromTarget(
			`avg by (dimension_ClusterName) (aws_eks_etcd_mvcc_db_total_size_in_bytes_average`+awsSel(`dimension_ClusterName=~"acme-eks.*"`)+`)`,
			"{{dimension_ClusterName}}")))

	// Account inventory table — aws_eks_info (gauge=1, no stat suffix).
	// Single-leg MergeTablePanel so OrganizeOptions can rename the raw label columns.
	dashboard.AddPanel(&d, "acct-table", dashboard.MergeTablePanel(
		"Account inventory (aws_eks_info)",
		[]*dashboardv2.TargetBuilder{
			dashboard.PromTableTarget(
				`aws_eks_info`+awsSel(`dimension_ClusterName=~"acme-eks.*"`),
				"A"),
		},
		dashboard.OrganizeOptions{
			Exclude: []string{"Time"},
			Rename: map[string]string{
				"account_id":      "Account",
				"tag_Environment": "Environment",
				"tag_VpcId":       "VPC",
				"tag_Owner":       "Owner",
				"tag_Purpose":     "Purpose",
				"Value #A":        "Info",
			},
		},
	))

	// ── Tab 2: EC2 / Compute ──────────────────────────────────────────────────────────────

	// KPI tiles.
	dashboard.AddPanel(&d, "ec2-avg-cpu", dashboard.StatTile("EC2 avg CPU utilisation", "percent",
		dashboard.PromTarget(
			`avg(aws_ec2_cpuutilization_average`+awsSel(`dimension_InstanceId!=""`)+`)`,
			"avg CPU %"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 60, Color: "yellow"},
		dashboard.Threshold{Value: 85, Color: "red"}))

	dashboard.AddPanel(&d, "ec2-max-cpu", dashboard.StatTile("EC2 peak CPU (any instance)", "percent",
		dashboard.PromTarget(
			`max(aws_ec2_cpuutilization_average`+awsSel(`dimension_InstanceId!=""`)+`)`,
			"peak CPU %"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 75, Color: "yellow"},
		dashboard.Threshold{Value: 95, Color: "red"}))

	dashboard.AddPanel(&d, "ec2-status-fail", dashboard.StatTile("Status check failures", "short",
		dashboard.PromTarget(
			`sum(aws_ec2_status_check_failed_sum`+awsSel(`dimension_InstanceId!=""`)+`)`,
			"failures"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "red"}))

	// Time series — per-instance CPU utilisation.
	dashboard.AddPanel(&d, "ec2-cpu-ts", dashboard.TimeseriesPanel("EC2 CPU utilisation by instance", "percent",
		dashboard.PromTarget(
			`avg by (dimension_InstanceId) (aws_ec2_cpuutilization_average`+awsSel(`dimension_InstanceId!=""`)+`)`,
			"{{dimension_InstanceId}}")))

	// Network in/out (ASG-level to avoid double-counting); _sum is a per-period gauge → /60.
	dashboard.AddPanel(&d, "ec2-net-ts", dashboard.TimeseriesPanel("EC2 network in / out (ASG-level, bytes/s)", "Bps",
		dashboard.PromTarget(
			`sum(aws_ec2_network_in_sum`+awsSel(`dimension_AutoScalingGroupName!=""`)+`) / 60`,
			"in").RefId("A"),
		dashboard.PromTarget(
			`sum(aws_ec2_network_out_sum`+awsSel(`dimension_AutoScalingGroupName!=""`)+`) / 60`,
			"out").RefId("B")))

	// EBS I/O at the instance level (aggregate view; per-volume detail is in the Storage tab).
	dashboard.AddPanel(&d, "ec2-ebs-rw", dashboard.TimeseriesPanel("EC2 EBS read / write bytes/s (instance-aggregate)", "Bps",
		dashboard.PromTarget(
			`sum by (account_id) (aws_ec2_ebsread_bytes_sum`+awsSel(`dimension_InstanceId!=""`)+`) / 60`,
			"read · {{account_id}}").RefId("A"),
		dashboard.PromTarget(
			`sum by (account_id) (aws_ec2_ebswrite_bytes_sum`+awsSel(`dimension_InstanceId!=""`)+`) / 60`,
			"write · {{account_id}}").RefId("B")))

	// ── Tab 3: Load Balancing ─────────────────────────────────────────────────────────────

	// KPI tiles — ALB fleet health.
	dashboard.AddPanel(&d, "alb-rps", dashboard.StatTile("ALB request rate", "reqps",
		dashboard.PromTarget(
			`sum(aws_applicationelb_request_count_sum`+awsSel("")+`) / 60`,
			"req/s")))

	dashboard.AddPanel(&d, "alb-5xx-rate", dashboard.StatTile("ALB target 5xx rate", "reqps",
		dashboard.PromTarget(
			`sum(aws_applicationelb_httpcode_target_5_xx_count_sum`+awsSel("")+`) / 60`,
			"5xx/s"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 0.1, Color: "yellow"},
		dashboard.Threshold{Value: 1, Color: "red"}))

	dashboard.AddPanel(&d, "alb-err-pct", dashboard.StatTile("ALB 5xx error %", "percent",
		dashboard.PromTarget(
			`100 * sum(aws_applicationelb_httpcode_target_5_xx_count_sum`+awsSel("")+`) / clamp_min(sum(aws_applicationelb_request_count_sum`+awsSel("")+`), 1)`,
			"error %"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "yellow"},
		dashboard.Threshold{Value: 5, Color: "red"}))

	dashboard.AddPanel(&d, "alb-unhealthy", dashboard.StatTile("ALB unhealthy hosts", "short",
		dashboard.PromTarget(
			`sum(aws_applicationelb_un_healthy_host_count_average`+awsSel("")+`)`,
			"unhealthy"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "red"}))

	dashboard.AddPanel(&d, "alb-healthy", dashboard.StatTile("ALB healthy hosts", "short",
		dashboard.PromTarget(
			`sum(aws_applicationelb_healthy_host_count_average`+awsSel("")+`)`,
			"healthy")))

	// Time series — ALB request rate and errors by account.
	dashboard.AddPanel(&d, "alb-rps-ts", dashboard.TimeseriesPanel("ALB request rate by account", "reqps",
		dashboard.PromTarget(
			`sum by (account_id) (aws_applicationelb_request_count_sum`+awsSel("")+`) / 60`,
			"{{account_id}}")))

	dashboard.AddPanel(&d, "alb-5xx-ts", dashboard.TimeseriesPanel("ALB target 5xx count/s by account", "reqps",
		dashboard.PromTarget(
			`sum by (account_id) (aws_applicationelb_httpcode_target_5_xx_count_sum`+awsSel("")+`) / 60`,
			"{{account_id}}")))

	dashboard.AddPanel(&d, "alb-rt-ts", dashboard.TimeseriesPanel("ALB target response time (avg, s) by account", "s",
		dashboard.PromTarget(
			`avg by (account_id) (aws_applicationelb_target_response_time_average`+awsSel("")+`)`,
			"{{account_id}}")))

	// Active connections and processed bytes — deeper capacity view.
	dashboard.AddPanel(&d, "alb-conn-ts", dashboard.TimeseriesPanel("ALB active connections by account", "short",
		dashboard.PromTarget(
			`sum by (account_id) (aws_applicationelb_active_connection_count_sum`+awsSel("")+`) / 60`,
			"{{account_id}}")))

	dashboard.AddPanel(&d, "alb-bytes-ts", dashboard.TimeseriesPanel("ALB processed bytes/s by account", "Bps",
		dashboard.PromTarget(
			`sum by (account_id) (aws_applicationelb_processed_bytes_sum`+awsSel("")+`) / 60`,
			"{{account_id}}")))

	// ── Tab 4: Networking & Storage ───────────────────────────────────────────────────────

	// KPI tiles — NAT gateway egress and drop pressure.
	dashboard.AddPanel(&d, "nat-egress-tile", dashboard.StatTile("NAT egress bytes/s (total)", "Bps",
		dashboard.PromTarget(
			`sum(aws_natgateway_bytes_out_to_destination_sum`+awsSel("")+`) / 60`,
			"egress bytes/s")))

	dashboard.AddPanel(&d, "nat-drops-tile", dashboard.StatTile("NAT packet drops", "short",
		dashboard.PromTarget(
			`sum(aws_natgateway_packets_drop_count_sum`+awsSel("")+`)`,
			"drops"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 10, Color: "yellow"},
		dashboard.Threshold{Value: 100, Color: "red"}))

	dashboard.AddPanel(&d, "nat-port-err-tile", dashboard.StatTile("NAT port-allocation errors", "short",
		dashboard.PromTarget(
			`sum(aws_natgateway_error_port_allocation_sum`+awsSel("")+`)`,
			"port errors"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "red"}))

	dashboard.AddPanel(&d, "ebs-queue-tile", dashboard.StatTile("EBS queue length (avg)", "short",
		dashboard.PromTarget(
			`avg(aws_ebs_volume_queue_length_average`+awsSel("")+`)`,
			"queue depth"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "yellow"},
		dashboard.Threshold{Value: 5, Color: "red"}))

	// NAT gateway time series.
	dashboard.AddPanel(&d, "nat-bytes-ts", dashboard.TimeseriesPanel("NAT gateway egress bytes/s by account", "Bps",
		dashboard.PromTarget(
			`sum by (account_id) (aws_natgateway_bytes_out_to_destination_sum`+awsSel("")+`) / 60`,
			"{{account_id}}")))

	dashboard.AddPanel(&d, "nat-conns-ts", dashboard.TimeseriesPanel("NAT active connections by account", "short",
		dashboard.PromTarget(
			`sum by (account_id) (aws_natgateway_active_connection_count_average`+awsSel("")+`)`,
			"{{account_id}}")))

	// EBS volume I/O — per-volume throughput and queue length.
	dashboard.AddPanel(&d, "ebs-rw-ts", dashboard.TimeseriesPanel("EBS read / write bytes/s by account", "Bps",
		dashboard.PromTarget(
			`sum by (account_id) (aws_ebs_volume_read_bytes_sum`+awsSel("")+`) / 60`,
			"read · {{account_id}}").RefId("A"),
		dashboard.PromTarget(
			`sum by (account_id) (aws_ebs_volume_write_bytes_sum`+awsSel("")+`) / 60`,
			"write · {{account_id}}").RefId("B")))

	dashboard.AddPanel(&d, "ebs-queue-ts", dashboard.TimeseriesPanel("EBS volume queue length by account", "short",
		dashboard.PromTarget(
			`avg by (account_id) (aws_ebs_volume_queue_length_average`+awsSel("")+`)`,
			"{{account_id}}")))

	// EBS latency (Nitro-only; _average stat applied to the latency value; units = ms per signals/cw.md).
	dashboard.AddPanel(&d, "ebs-lat-ts", dashboard.TimeseriesPanel("EBS avg read / write latency by account (Nitro)", "ms",
		dashboard.PromTarget(
			`avg by (account_id) (aws_ebs_volume_avg_read_latency_average`+awsSel("")+`)`,
			"read · {{account_id}}").RefId("A"),
		dashboard.PromTarget(
			`avg by (account_id) (aws_ebs_volume_avg_write_latency_average`+awsSel("")+`)`,
			"write · {{account_id}}").RefId("B")))

	// ── Tab 5: Pipeline Health ────────────────────────────────────────────────────────────
	// Firehose is the metric-stream delivery pipeline (AWS → Firehose → Mimir).
	// delivery_to_http_endpoint_success is a fraction 0–1 (≈1.0 steady).
	// delivery_to_http_endpoint_data_freshness is lag in seconds (<60s steady).

	dashboard.AddPanel(&d, "firehose-success-tile", dashboard.StatTile("Firehose delivery success (fraction)", "percentunit",
		dashboard.PromTarget(
			`avg(aws_firehose_delivery_to_http_endpoint_success_average`+awsSel("")+`)`,
			"delivery success"),
		dashboard.Threshold{Value: 0, Color: "red"},
		dashboard.Threshold{Value: 0.99, Color: "yellow"},
		dashboard.Threshold{Value: 0.999, Color: "green"}))

	dashboard.AddPanel(&d, "firehose-lag-tile", dashboard.StatTile("Firehose data freshness (lag, s)", "s",
		dashboard.PromTarget(
			`avg(aws_firehose_delivery_to_http_endpoint_data_freshness_average`+awsSel("")+`)`,
			"lag (s)"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 30, Color: "yellow"},
		dashboard.Threshold{Value: 60, Color: "red"}))

	dashboard.AddPanel(&d, "firehose-success-ts", dashboard.TimeseriesPanel("Firehose delivery success by stream", "percentunit",
		dashboard.PromTarget(
			`avg by (dimension_DeliveryStreamName) (aws_firehose_delivery_to_http_endpoint_success_average`+awsSel("")+`)`,
			"{{dimension_DeliveryStreamName}}")))

	dashboard.AddPanel(&d, "firehose-lag-ts", dashboard.TimeseriesPanel("Firehose data freshness by stream", "s",
		dashboard.PromTarget(
			`avg by (dimension_DeliveryStreamName) (aws_firehose_delivery_to_http_endpoint_data_freshness_average`+awsSel("")+`)`,
			"{{dimension_DeliveryStreamName}}")))

	// ── Layout ────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("EKS",
			dashboard.Section("Control-plane KPIs",
				dashboard.Tile("eks-api-rps"), dashboard.Tile("eks-api-4xx"), dashboard.Tile("eks-api-5xx"),
				dashboard.Tile("eks-pending-pods"), dashboard.Tile("eks-etcd-size")),
			dashboard.Section("Node health",
				dashboard.Half("eks-node-count"), dashboard.Half("eks-failed-nodes")),
			dashboard.Section("API server & etcd",
				dashboard.Third("eks-api-rate-ts"), dashboard.Third("eks-api-latency-ts"), dashboard.Third("eks-etcd-size-ts")),
			dashboard.Section("Account inventory",
				dashboard.Tall("acct-table")),
		),
		dashboard.Tabbed("EC2 / Compute",
			dashboard.Section("Compute KPIs",
				dashboard.Tile("ec2-avg-cpu"), dashboard.Tile("ec2-max-cpu"), dashboard.Tile("ec2-status-fail")),
			dashboard.Section("Workers",
				dashboard.Half("ec2-cpu-ts"), dashboard.Half("ec2-net-ts"),
				dashboard.Full("ec2-ebs-rw")),
		),
		dashboard.Tabbed("Load Balancing",
			dashboard.Section("ALB KPIs",
				dashboard.Tile("alb-rps"), dashboard.Tile("alb-5xx-rate"), dashboard.Tile("alb-err-pct"),
				dashboard.Tile("alb-unhealthy"), dashboard.Tile("alb-healthy")),
			dashboard.Section("Request & error rates",
				dashboard.Half("alb-rps-ts"), dashboard.Half("alb-5xx-ts"),
				dashboard.Half("alb-rt-ts"), dashboard.Half("alb-conn-ts")),
			dashboard.Section("Capacity",
				dashboard.Full("alb-bytes-ts")),
		),
		dashboard.Tabbed("Networking & Storage",
			dashboard.Section("NAT & EBS KPIs",
				dashboard.Tile("nat-egress-tile"), dashboard.Tile("nat-drops-tile"),
				dashboard.Tile("nat-port-err-tile"), dashboard.Tile("ebs-queue-tile")),
			dashboard.Section("NAT gateway",
				dashboard.Half("nat-bytes-ts"), dashboard.Half("nat-conns-ts")),
			dashboard.Section("EBS volumes",
				dashboard.Half("ebs-rw-ts"), dashboard.Half("ebs-queue-ts"),
				dashboard.Full("ebs-lat-ts")),
		),
		dashboard.Tabbed("Pipeline Health",
			dashboard.Section("Firehose metric-stream pipeline",
				dashboard.Stat("firehose-success-tile"), dashboard.Stat("firehose-lag-tile")),
			dashboard.Section("Per-stream detail",
				dashboard.Half("firehose-success-ts"), dashboard.Half("firehose-lag-ts")),
		),
	)
	return d, nil
}
