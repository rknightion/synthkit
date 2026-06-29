// SPDX-License-Identifier: AGPL-3.0-only

package nettopo

import "github.com/rknightion/synthkit/internal/failuremode"

// FailureModes are the modes the network_topology construct responds to. All five are
// AxisNetwork (scoped to the exporter instance, e.g. "netobs-glb-hub:9100"). Declaring
// them is load-bearing: the resolver validates incident/effect mode references against
// the construct vocabulary, so omitting any would reject blueprints that fire that mode.
//
// Runtime implementation (shape.Eval calls) is wired in the next task (Task 7 emit-side).
// Until then, declaring these modes enables blueprint authoring and resolver validation
// without any runtime effect.
var FailureModes = []failuremode.Mode{
	{
		Name: "nettopo_devices_unreachable",
		Axis: failuremode.AxisNetwork,
		Help: "SNMP polling fails for a fraction of devices (walk errors spike, device discovery drops)",
	},
	{
		Name: "nettopo_discovery_slow",
		Axis: failuremode.AxisNetwork,
		Help: "discovery cycle duration inflates (cycle_duration_seconds and module walk times rise)",
	},
	{
		Name: "nettopo_walker_degraded",
		Axis: failuremode.AxisNetwork,
		Help: "walker outcome errors climb; edge count under-reports (partial topology visibility)",
	},
	{
		Name: "nettopo_auth_failures",
		Axis: failuremode.AxisNetwork,
		Help: "SNMP credential trials fail (credential_trials_total error rate rises)",
	},
	{
		Name: "nettopo_spoke_down",
		Axis: failuremode.AxisNetwork,
		Help: "a federation spoke goes offline (spoke_status=down, hub/spoke session metrics degrade)",
	},
}
