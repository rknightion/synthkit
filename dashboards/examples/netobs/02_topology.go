// SPDX-License-Identifier: AGPL-3.0-only

package netobs

import "github.com/rknightion/synthkit/dashboard"

// Topology draws one exporter's discovered graph and its device and link inventory.
// uid: netobs-topology.
//
// The map is the built-in nodeGraph. It needs string id/title/source/target columns, so they are
// built as labels with label_join over device_info (nodes) and edge_info (edges); node mainstat is
// device uptime in days. The exporter picker is single-select because device ids are only unique
// within one exporter's graph.
func Topology(_ *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("netobs-topology", "Network Observability - Topology map")
	if err != nil {
		return dashboard.Dashboard{}, err
	}
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"instance", "Exporter",
		`label_values(network_topology_graph_devices_total{job="`+job+`"}, instance)`).
		Multi(false).IncludeAll(false))

	devices := `network_topology_device_uptime_seconds` + sel("") +
		` * on (instance, device_id) group_left (vendor, os_version, site) network_topology_device_info` + sel("")
	nodes := `label_join(label_join(label_join(label_join(label_join(` + devices + ` / 86400,` +
		` "id", "", "device_id"), "title", "", "device_id"), "subtitle", "", "vendor"),` +
		` "detail__os_version", "", "os_version"), "detail__site", "", "site")`
	edges := `label_join(label_join(label_join(label_join(network_topology_edge_info` + sel("") + `,` +
		` "id", "/", "src_device", "src_port", "dst_device", "discovery_proto"),` +
		` "source", "", "src_device"), "target", "", "dst_device"), "mainstat", "", "discovery_proto")`
	dashboard.AddPanel(&d, "map", dashboard.NodeGraphTablesPanel("Discovered topology ($instance)",
		dashboard.PromTableTarget(nodes, "A"), dashboard.PromTableTarget(edges, "B"),
		dashboard.OrganizeOptions{
			Exclude: []string{"Time", "Value #B"},
			Rename:  map[string]string{"Value #A": "mainstat"},
		}))

	neutral(&d, "devices", "Devices", "short", `sum(network_topology_graph_devices_total`+sel("")+`)`)
	neutral(&d, "links", "Links", "short", `sum(network_topology_graph_edges_total`+sel("")+`)`)
	health(&d, "rebooted", "Devices rebooted (24h)", "short",
		`count(network_topology_device_uptime_seconds`+sel("")+` < 86400) or vector(0)`, warnZero)
	neutral(&d, "versions", "Distinct OS versions", "short",
		`count(count by (vendor, os_version) (network_topology_device_info`+sel("")+`))`)

	table(&d, "inventory", "Device inventory", devices, dashboard.OrganizeOptions{
		Exclude: []string{"Time", "job", "instance"},
		Rename:  map[string]string{"Value": "Uptime (s)", "device_id": "Device", "os_version": "OS version"},
		Order:   []string{"device_id", "vendor", "os_version", "site", "Value"},
	})
	table(&d, "adjacency", "Link adjacency", `network_topology_edge_info`+sel(""), dashboard.OrganizeOptions{
		Exclude: []string{"Time", "job", "instance", "__name__", "Value"},
		Order:   []string{"src_device", "src_port", "dst_device", "dst_port", "discovery_proto", "link_kind", "direction"},
	})
	table(&d, "software", "Software versions in the estate",
		`count by (vendor, os_version) (network_topology_device_info`+sel("")+`)`,
		dashboard.OrganizeOptions{Exclude: []string{"Time"}, Rename: map[string]string{"Value": "Devices"}})
	table(&d, "recent-reboots", "Devices rebooted in the last 24h",
		`network_topology_device_uptime_seconds`+sel("")+` < 86400`,
		dashboard.OrganizeOptions{
			Exclude: []string{"Time", "job", "__name__"},
			Rename:  map[string]string{"Value": "Uptime (s)"},
		})

	dashboard.WithRows(&d,
		dashboard.Section("Summary",
			dashboard.Stat("devices"), dashboard.Stat("links"), dashboard.Stat("rebooted"), dashboard.Stat("versions")),
		dashboard.Section("Map", dashboard.At("map", 24, 32)),
		dashboard.Section("Inventory",
			dashboard.At("inventory", 12, 12), dashboard.At("adjacency", 12, 12),
			dashboard.Half("software"), dashboard.Half("recent-reboots")),
	)
	return d, nil
}
