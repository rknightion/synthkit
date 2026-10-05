// SPDX-License-Identifier: AGPL-3.0-only

package netobs

import (
	"github.com/grafana/grafana-foundation-sdk/go/dashboardv2"

	"github.com/rknightion/synthkit/dashboard"
)

// Overview is the NOC landing board: estate size, graph freshness, churn and the exporter roster.
// uid: netobs-overview.
func Overview(_ *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("netobs-overview", "Network Observability - Overview")
	if err != nil {
		return dashboard.Dashboard{}, err
	}
	instanceVar(&d)

	neutral(&d, "kpi-exporters", "Topology exporters", "short",
		`count(count by (instance) (network_topology_graph_devices_total`+sel("")+`))`)
	neutral(&d, "kpi-devices", "Devices discovered", "short",
		`sum(network_topology_graph_devices_total`+sel("")+`)`)
	neutral(&d, "kpi-links", "Links discovered", "short",
		`sum(network_topology_graph_edges_total`+sel("")+`)`)
	health(&d, "kpi-stale", "Stale graphs", "short",
		`sum(network_topology_graph_stale`+sel("")+`)`, goodZero)
	health(&d, "kpi-unreachable", "Devices failing discovery", "short",
		`sum(network_topology_discovery_devices_total`+sel(`status!="success"`)+`) or vector(0)`, goodZero)
	health(&d, "kpi-modules", "Degraded discovery modules", "short",
		`count(network_topology_module_last_status`+sel("")+` > 0) or vector(0)`, warnZero)
	neutral(&d, "kpi-changes", "Link changes (1h)", "short",
		`sum(increase(network_topology_change_total`+sel("")+`[1h])) or vector(0)`)
	neutral(&d, "kpi-oos", "Out-of-scope neighbours", "short",
		`sum(network_topology_out_of_scope_neighbours_total`+sel("")+`)`)

	dashboard.AddPanel(&d, "roster", dashboard.MergeTablePanel("Exporter roster",
		[]*dashboardv2.TargetBuilder{
			dashboard.PromTableTarget(`sum by (instance) (network_topology_graph_devices_total`+sel("")+`)`, "A"),
			dashboard.PromTableTarget(`sum by (instance) (network_topology_graph_edges_total`+sel("")+`)`, "B"),
			dashboard.PromTableTarget(`max by (instance) (network_topology_graph_stale`+sel("")+`)`, "C"),
			dashboard.PromTableTarget(dashboard.ClassicHistogramQuantile(0.95,
				"network_topology_discovery_cycle_duration_seconds", sel(""), []string{"instance"}), "D"),
			dashboard.PromTableTarget(`sum by (instance) (increase(network_topology_change_total`+sel("")+`[1h]))`, "E"),
		},
		dashboard.OrganizeOptions{
			Exclude: []string{"Time"},
			Rename: map[string]string{
				"Value #A": "Devices", "Value #B": "Links", "Value #C": "Stale",
				"Value #D": "Cycle p95 (s)", "Value #E": "Link changes (1h)",
			},
		}))

	series(&d, "vendors", "Devices by vendor", "short",
		`count by (vendor) (network_topology_device_info`+sel("")+`)`, "{{vendor}}")
	series(&d, "protos", "Links by discovery protocol", "short",
		`count by (discovery_proto) (network_topology_edge_info`+sel("")+`)`, "{{discovery_proto}}")
	dashboard.AddPanel(&d, "churn", dashboard.TimeseriesPanel("Link changes", "short",
		dashboard.PromTarget(`sum by (change_kind, discovery_proto) (increase(network_topology_change_total`+sel("")+`[$__rate_interval]))`,
			"{{change_kind}} {{discovery_proto}}")))
	series(&d, "cycle", "Discovery cycle duration p95", "s",
		dashboard.ClassicHistogramQuantile(0.95, "network_topology_discovery_cycle_duration_seconds", sel(""), []string{"instance"}),
		"{{instance}}")
	dashboard.AddPanel(&d, "change-logs", dashboard.LogsPanel("Topology change and conflict events",
		dashboard.LokiTarget(logSel(""), "")))

	dashboard.WithRows(&d,
		dashboard.Section("Estate",
			dashboard.Stat("kpi-exporters"), dashboard.Stat("kpi-devices"), dashboard.Stat("kpi-links"),
			dashboard.Stat("kpi-stale"), dashboard.Stat("kpi-unreachable"), dashboard.Stat("kpi-modules"),
			dashboard.Stat("kpi-changes"), dashboard.Stat("kpi-oos")),
		dashboard.Section("Exporters", dashboard.Full("roster")),
		dashboard.Section("Inventory and churn",
			dashboard.Half("vendors"), dashboard.Half("protos"),
			dashboard.Half("churn"), dashboard.Half("cycle")),
		dashboard.Section("Events", dashboard.At("change-logs", 24, 12)),
	)
	return d, nil
}
