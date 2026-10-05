// SPDX-License-Identifier: AGPL-3.0-only

// Package netobs holds the network-observability dashboards for the network_topology integration
// (the SNMP topology-discovery exporter, signals/nettopo.md). It imports only the dashboard builder
// library. Registered in cmd/synthkit-dash/catalog.go against netobs-global, the federation hub
// blueprint, but every panel keys on the exporter's own `instance` so one dashboard set covers
// every exporter on the stack (netobs-enterprise, netobs-global and netobs-spoke together).
//
// Scope: every network_topology_* family is substrate-scoped (no blueprint label) and identified
// by (job, instance). Per-device signals exist only on device_info, device_uptime_seconds,
// edge_info and boundary_observation_info; change, module and discovery-health families are
// exporter-scoped, so no panel colours a device by health it cannot know.
package netobs

import (
	"github.com/grafana/grafana-foundation-sdk/go/dashboardv2"

	"github.com/rknightion/synthkit/dashboard"
)

const job = "integrations/network-topology-exporter"

// Templates returns the network-observability dashboard set.
func Templates() []dashboard.Template {
	return []dashboard.Template{Overview, Topology, Discovery, Federation}
}

// sel scopes a network_topology_* family to the exporter job and the $instance variable. extra is
// an already-formatted matcher list (no leading comma).
func sel(extra string) string {
	s := `job="` + job + `",instance=~"$instance"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// logSel scopes the exporter's topology change and conflict Loki streams.
func logSel(extra string) string {
	s := `source="network-topology-exporter",instance=~"$instance"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// instanceVar is the multi-select exporter picker shared by the fleet-level dashboards.
func instanceVar(d *dashboard.Dashboard) {
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"instance", "Exporter",
		`label_values(network_topology_graph_devices_total{job="`+job+`"}, instance)`))
}

var (
	goodZero = []dashboard.Threshold{{Value: 0, Color: "green"}, {Value: 1, Color: "red"}}
	warnZero = []dashboard.Threshold{{Value: 0, Color: "green"}, {Value: 1, Color: "orange"}}
)

func neutral(d *dashboard.Dashboard, id, title, unit, expr string) {
	dashboard.AddPanel(d, id, dashboard.StatTile(title, unit, dashboard.PromTarget(expr, "")))
}

func health(d *dashboard.Dashboard, id, title, unit, expr string, th []dashboard.Threshold) {
	dashboard.AddPanel(d, id, dashboard.StatTile(title, unit, dashboard.PromTarget(expr, ""), th...))
}

func series(d *dashboard.Dashboard, id, title, unit, expr, legend string) {
	dashboard.AddPanel(d, id, dashboard.TimeseriesPanel(title, unit, dashboard.PromTarget(expr, legend)))
}

func table(d *dashboard.Dashboard, id, title, expr string, organize dashboard.OrganizeOptions) {
	dashboard.AddPanel(d, id, dashboard.MergeTablePanel(title,
		[]*dashboardv2.TargetBuilder{dashboard.PromTableTarget(expr, "A")}, organize))
}
