// SPDX-License-Identifier: AGPL-3.0-only

package netobs

import "github.com/rknightion/synthkit/dashboard"

// Federation covers multi-site deployments: hub-side spoke liveness, rejected graph pushes and
// inter-domain boundaries, and spoke-side push health. uid: netobs-federation. Standalone
// exporters emit none of these families, so the panels show only hubs and spokes.
func Federation(_ *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("netobs-federation", "Network Observability - Federation")
	if err != nil {
		return dashboard.Dashboard{}, err
	}
	instanceVar(&d)

	neutral(&d, "spokes", "Spokes reporting", "short",
		`sum(network_topology_federation_spoke_up`+sel("")+`)`)
	health(&d, "spokes-down", "Spokes down", "short",
		`count(network_topology_federation_spoke_up`+sel("")+` == 0) or vector(0)`, goodZero)
	health(&d, "push-age", "Oldest spoke push", "s",
		`max(time() - network_topology_federation_spoke_last_push_timestamp_seconds`+sel("")+`)`,
		[]dashboard.Threshold{{Value: 0, Color: "green"}, {Value: 300, Color: "orange"}, {Value: 900, Color: "red"}})
	health(&d, "rejected", "Rejected graph updates (1h)", "short",
		`sum(increase(network_topology_graph_updates_rejected_total`+sel("")+`[1h])) or vector(0)`, warnZero)
	neutral(&d, "boundaries", "Inter-domain boundaries", "short",
		`count(network_topology_boundary_observation_info`+sel("")+`) or vector(0)`)

	dashboard.AddPanel(&d, "liveness", dashboard.StateTimelinePanel("Spoke liveness (hub view)",
		dashboard.PromTarget(`max by (spoke_id) (network_topology_federation_spoke_up`+sel("")+`)`, "{{spoke_id}}")))
	series(&d, "spoke-age", "Seconds since last spoke push", "s",
		`time() - max by (spoke_id) (network_topology_federation_spoke_last_push_timestamp_seconds`+sel("")+`)`, "{{spoke_id}}")
	series(&d, "rejections", "Rejected graph updates by reason", "short",
		`sum by (reason) (increase(network_topology_graph_updates_rejected_total`+sel("")+`[$__rate_interval]))`, "{{reason}}")
	series(&d, "oos", "Unmatched out-of-scope edges from spokes", "short",
		`sum by (instance) (increase(network_topology_hub_oos_unmatched_total`+sel("")+`[$__rate_interval]))`, "{{instance}}")
	table(&d, "boundary-table", "Inter-domain boundary observations",
		`network_topology_boundary_observation_info`+sel(""),
		dashboard.OrganizeOptions{
			Exclude: []string{"Time", "job", "__name__", "Value"},
			Order:   []string{"instance", "reporting_device", "src_port", "peer_a", "peer_b", "proto"},
		})

	series(&d, "push-fail", "Spoke push failures", "ops",
		`sum by (instance) (rate(network_topology_federation_spoke_push_failures_total`+sel("")+`[$__rate_interval]))`, "{{instance}}")
	series(&d, "push-success", "Seconds since last successful push (spoke view)", "s",
		`time() - max by (instance) (network_topology_federation_spoke_push_last_success_unix`+sel("")+`)`, "{{instance}}")
	series(&d, "push-drops", "Spoke push drops", "ops",
		`sum by (instance, reason) (rate(network_topology_federation_spoke_push_drops_total`+sel("")+`[$__rate_interval]))`, "{{instance}} {{reason}}")
	series(&d, "push-queue", "Spoke push queue depth", "short",
		`sum by (instance) (network_topology_federation_spoke_push_queue_depth`+sel("")+`)`, "{{instance}}")
	series(&d, "otlp", "OTLP edge-event pushes", "ops",
		`sum by (instance, status, reason) (rate(network_topology_otlp_push_total`+sel("")+`[$__rate_interval]))`, "{{instance}} {{status}} {{reason}}")

	dashboard.WithRows(&d,
		dashboard.Section("Summary",
			dashboard.At("spokes", 5, 5), dashboard.At("spokes-down", 5, 5), dashboard.At("push-age", 5, 5),
			dashboard.At("rejected", 5, 5), dashboard.At("boundaries", 4, 5)),
		dashboard.Section("Hub",
			dashboard.Full("liveness"), dashboard.Half("spoke-age"), dashboard.Half("rejections"),
			dashboard.Half("oos"), dashboard.Half("boundary-table")),
		dashboard.Section("Spokes",
			dashboard.Half("push-fail"), dashboard.Half("push-success"),
			dashboard.Half("push-drops"), dashboard.Half("push-queue")),
		dashboard.Section("OTLP export", dashboard.Full("otlp")),
	)
	return d, nil
}
