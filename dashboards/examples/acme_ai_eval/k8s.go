// SPDX-License-Identifier: AGPL-3.0-only

// K8s — AI Evaluation Kubernetes (acme-ai-eval blueprint).
//
// Pod inventory and resource usage for the Acme AI Eval platform's 8-cell EKS estate
// (acme-eks-{develop,devint,test,prod}-{usw2,euw3}). All families here are
// SUBSTRATE-SCOPED (no blueprint label) — kube-state-metrics, cAdvisor, and
// kubelet/node-exporter carry only cluster/namespace/node identity.
//
// Scope: cluster=~"$cluster", namespace=~"$namespace". The predecessor's {scenario=}
// filter is DROPPED throughout (k8s families carry no blueprint label).
//
// TENANT-STRIPPED: no use_case, no workspace — this is the platform operator's
// view, scoped by cluster and namespace only.
//
// Tabs: Inventory · Resources · Nodes.
package acme_ai_eval

import "github.com/rknightion/synthkit/dashboard"

// k8sSel returns a raw label-matcher selector for substrate k8s families
// (kube_*, container_*, node_*, kubelet_*). Scopes by $cluster and $namespace.
// extra is an already-formatted matcher list (no leading comma),
// e.g. `phase="Running"` or `node=~"$node"`.
func k8sSel(extra string) string {
	s := `cluster=~"$cluster",namespace=~"$namespace"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// k8sClusterSel returns a selector scoped only to cluster (no namespace filter),
// for metrics that do not carry a namespace label (node-level series).
func k8sClusterSel(extra string) string {
	s := `cluster=~"$cluster"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// K8s is the AI Evaluation Kubernetes dashboard for the acme-ai-eval blueprint.
// uid: acme-eval-k8s. Three tabs reproducing the predecessor's acme-eval-k8s layout.
func K8s(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ai-eval-k8s", "Acme AI Eval — Kubernetes")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ───────────────────────────────────────────────────────────────────────────
	// scenario: constant so any shared seam helpers referencing $scenario keep working.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme-ai-eval"))
	// cluster: manifest-seeded custom var — the 8 acme-eks-* clusters (substrate scope, no blueprint).
	d.Builder.CustomVariable(dashboard.ClusterVar(m))
	// namespace: label_values from kube_pod_info — substrate, DROP scenario= (no blueprint label).
	d.Builder.QueryVariable(dashboard.LabelValuesVar("namespace", "Namespace",
		`label_values(kube_pod_info{cluster=~"$cluster"}, namespace)`))

	// ── Tab 1: Inventory ──────────────────────────────────────────────────────────────────

	// KPI strip: pods running, total CPU, total memory.
	dashboard.AddPanel(&d, "kpi-pods", dashboard.StatTile("Pods (Running)", "short",
		dashboard.PromTarget(
			`sum(kube_pod_status_phase`+k8sSel(`phase="Running"`)+`)`,
			"Running pods"),
		dashboard.Threshold{Value: 0, Color: "green"}))

	dashboard.AddPanel(&d, "kpi-cpu", dashboard.StatTile("Total CPU usage", "short",
		dashboard.PromTarget(
			`sum(rate(container_cpu_usage_seconds_total`+k8sSel("")+`[$__rate_interval]))`,
			"CPU cores"),
		dashboard.Threshold{Value: 0, Color: "blue"}))

	dashboard.AddPanel(&d, "kpi-mem", dashboard.StatTile("Total working-set memory", "bytes",
		dashboard.PromTarget(
			`sum(container_memory_working_set_bytes`+k8sSel("")+`)`,
			"working-set bytes"),
		dashboard.Threshold{Value: 0, Color: "purple"}))

	// Pod inventory table: kube_pod_info (instant, table format) — cluster/namespace/pod/node.
	dashboard.AddPanel(&d, "pod-inv", dashboard.TablePanel("Pod inventory",
		dashboard.PromTarget(
			`kube_pod_info`+k8sSel(""),
			"")))

	// Running pods by namespace + cluster (stacked timeseries).
	dashboard.AddPanel(&d, "phase-ns", dashboard.TimeseriesPanel("Pods by namespace", "short",
		dashboard.PromTarget(
			`sum by (namespace, cluster) (kube_pod_status_phase`+k8sClusterSel(`phase="Running"`)+`)`,
			"{{cluster}}/{{namespace}}")))

	// ── Tab 2: Resources ──────────────────────────────────────────────────────────────────

	// CPU by pod (rate of counter).
	dashboard.AddPanel(&d, "cpu-pod", dashboard.TimeseriesPanel("CPU by pod", "short",
		dashboard.PromTarget(
			`sum by (pod, cluster) (rate(container_cpu_usage_seconds_total`+k8sSel("")+`[$__rate_interval]))`,
			"{{cluster}}/{{pod}}")))

	// Working-set memory by pod.
	dashboard.AddPanel(&d, "mem-pod", dashboard.TimeseriesPanel("Working-set memory by pod", "bytes",
		dashboard.PromTarget(
			`sum by (pod, cluster) (container_memory_working_set_bytes`+k8sSel("")+`)`,
			"{{cluster}}/{{pod}}")))

	// RSS memory by pod.
	dashboard.AddPanel(&d, "rss-pod", dashboard.TimeseriesPanel("RSS memory by pod", "bytes",
		dashboard.PromTarget(
			`sum by (pod, cluster) (container_memory_rss`+k8sSel("")+`)`,
			"{{cluster}}/{{pod}}")))

	// CPU throttling (cAdvisor): throttled_periods / total_periods.
	dashboard.AddPanel(&d, "cpu-throttle", dashboard.TimeseriesPanel("CPU throttling by pod", "percentunit",
		dashboard.PromTarget(
			`sum by (pod, cluster) (rate(container_cpu_cfs_throttled_periods_total`+k8sSel("")+`[$__rate_interval])) /`+
				` sum by (pod, cluster) (rate(container_cpu_cfs_periods_total`+k8sSel("")+`[$__rate_interval]))`,
			"{{cluster}}/{{pod}}")))

	// Container resource requests vs limits — cpu.
	dashboard.AddPanel(&d, "res-req-cpu", dashboard.TimeseriesPanel("CPU requests vs limits (cluster total)", "short",
		dashboard.PromTarget(
			`sum(kube_pod_container_resource_requests`+k8sSel(`resource="cpu"`)+`)`,
			"requests").RefId("A"),
		dashboard.PromTarget(
			`sum(kube_pod_container_resource_limits`+k8sSel(`resource="cpu"`)+`)`,
			"limits").RefId("B")))

	// Container resource requests vs limits — memory.
	dashboard.AddPanel(&d, "res-req-mem", dashboard.TimeseriesPanel("Memory requests vs limits (cluster total)", "bytes",
		dashboard.PromTarget(
			`sum(kube_pod_container_resource_requests`+k8sSel(`resource="memory"`)+`)`,
			"requests").RefId("A"),
		dashboard.PromTarget(
			`sum(kube_pod_container_resource_limits`+k8sSel(`resource="memory"`)+`)`,
			"limits").RefId("B")))

	// ── Tab 3: Nodes ──────────────────────────────────────────────────────────────────────

	// Node count KPI (kube_node_status_condition Ready=true, cluster scope only).
	dashboard.AddPanel(&d, "kpi-nodes", dashboard.StatTile("Ready nodes", "short",
		dashboard.PromTarget(
			`sum(kube_node_status_condition`+k8sClusterSel(`condition="Ready",status="true"`)+`)`,
			"ready nodes"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "yellow"},
	))

	// Node CPU usage (node-exporter — node_cpu_seconds_total idle complement).
	dashboard.AddPanel(&d, "node-cpu-usage", dashboard.TimeseriesPanel("Node CPU usage", "percentunit",
		dashboard.PromTarget(
			`1 - avg by (node, cluster) (`+
				`rate(node_cpu_seconds_total`+k8sClusterSel(`mode="idle"`)+`[$__rate_interval]))`,
			"{{cluster}}/{{node}}")))

	// Node memory available (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes).
	dashboard.AddPanel(&d, "node-mem-avail", dashboard.TimeseriesPanel("Node memory available", "percentunit",
		dashboard.PromTarget(
			`avg by (node, cluster) (`+
				`node_memory_MemAvailable_bytes`+k8sClusterSel("")+`) /`+
				` avg by (node, cluster) (node_memory_MemTotal_bytes`+k8sClusterSel("")+`)`,
			"{{cluster}}/{{node}}")))

	// Node-level pod count (kubelet_running_pods).
	dashboard.AddPanel(&d, "node-pods", dashboard.TimeseriesPanel("Running pods per node", "short",
		dashboard.PromTarget(
			`kubelet_running_pods`+k8sClusterSel(""),
			"{{cluster}}/{{node}}")))

	// Node disk I/O (node_disk_read/write bytes, representative).
	dashboard.AddPanel(&d, "node-disk-io", dashboard.TimeseriesPanel("Node disk I/O", "Bps",
		dashboard.PromTarget(
			`sum by (node, cluster) (rate(node_disk_read_bytes_total`+k8sClusterSel("")+`[$__rate_interval]))`,
			"read {{cluster}}/{{node}}").RefId("A"),
		dashboard.PromTarget(
			`sum by (node, cluster) (rate(node_disk_written_bytes_total`+k8sClusterSel("")+`[$__rate_interval]))`,
			"write {{cluster}}/{{node}}").RefId("B")))

	// Node network I/O.
	dashboard.AddPanel(&d, "node-net-io", dashboard.TimeseriesPanel("Node network I/O", "Bps",
		dashboard.PromTarget(
			`sum by (node, cluster) (rate(node_network_receive_bytes_total`+k8sClusterSel("")+`[$__rate_interval]))`,
			"rx {{cluster}}/{{node}}").RefId("A"),
		dashboard.PromTarget(
			`sum by (node, cluster) (rate(node_network_transmit_bytes_total`+k8sClusterSel("")+`[$__rate_interval]))`,
			"tx {{cluster}}/{{node}}").RefId("B")))

	// Node condition timeline — MemoryPressure.
	dashboard.AddPanel(&d, "node-mem-pressure", dashboard.StateTimelinePanel("Node MemoryPressure",
		dashboard.PromTarget(
			`kube_node_status_condition`+k8sClusterSel(`condition="MemoryPressure",status="true"`),
			"{{node}}")))

	// ── Layout ──────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("Inventory",
			dashboard.Section("Summary",
				dashboard.Tile("kpi-pods"), dashboard.Tile("kpi-cpu"), dashboard.Tile("kpi-mem")),
			dashboard.Section("Pods",
				dashboard.TwoThirds("pod-inv"), dashboard.Third("phase-ns")),
		),
		dashboard.Tabbed("Resources",
			dashboard.Section("CPU",
				dashboard.Full("cpu-pod"),
				dashboard.Half("cpu-throttle"), dashboard.Half("res-req-cpu")),
			dashboard.Section("Memory",
				dashboard.Half("mem-pod"), dashboard.Half("rss-pod"),
				dashboard.Full("res-req-mem")),
		),
		dashboard.Tabbed("Nodes",
			dashboard.Section("Node KPIs",
				dashboard.Tile("kpi-nodes")),
			dashboard.Section("CPU & memory",
				dashboard.Half("node-cpu-usage"), dashboard.Half("node-mem-avail")),
			dashboard.Section("Workload & I/O",
				dashboard.Half("node-pods"), dashboard.Half("node-disk-io"),
				dashboard.Full("node-net-io")),
			dashboard.Section("Pressure",
				dashboard.Full("node-mem-pressure")),
		),
	)
	return d, nil
}
