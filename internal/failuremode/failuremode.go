// SPDX-License-Identifier: AGPL-3.0-only

// Package failuremode defines the failure-mode vocabulary types shared across constructs,
// workloads, the blueprint layer, and the control plane. It is a leaf: it imports nothing from
// other synthkit tiers (same tier as fixture/shape/state), so constructs may import it without
// violating the three-tier rule.
package failuremode

// Axis is the kind of fixture a mode attaches to. Validation and axis-wildcard expansion key off
// it; runtime dispatch does NOT (a construct simply calls shape.Eval for the modes it implements,
// passing its own instance identity as the scope).
type Axis string

const (
	AxisWorkload Axis = "workload"
	AxisCluster  Axis = "cluster" // k8s pods + the EC2 nodes sharing the cluster identity
	AxisDatabase Axis = "database"
	AxisCache    Axis = "cache"
	AxisCloud    Axis = "cloud"    // region/account-scoped CloudWatch infra families
	AxisService  Axis = "service"  // an individual service-graph NODE of an `app` workload (Spec 5)
	AxisNetwork  Axis = "network"  // network-topology-exporter instance (SNMP topology discovery)
	AxisFunction Axis = "function" // one declared serverless function (today: a cloud.lambda function name)
	AxisQueue    Axis = "queue"    // one declared message queue (today: a Solace queue name)
	AxisTable    Axis = "table"    // one declared provisioned table (today: a cloud.dynamodb table name)
)

// Target is one addressable failure target a construct declaration owns. A construct Config
// reports its targets through a FailureTargets() []Target method (duck-typed by the blueprint
// resolver), so the declaration stays the single source of the inventory.
type Target struct {
	Name string
	Axis Axis
}

// Mode is one declared failure mode. Name is the human label used in scenarios, the control
// plane, and the owning construct's shape.Eval(now, Name, scope) call. (Name, Axis) is the key:
// the same Name may exist on multiple axes (e.g. latency_spike on workload AND database).
type Mode struct {
	Name string
	Axis Axis
	Help string
}

// MultiAxis reports the set of mode names that appear under more than one Axis across modes.
// The blueprint resolver uses it to reject an empty ("all") target on an ambiguous mode.
func MultiAxis(modes []Mode) map[string]bool {
	seen := map[string]map[Axis]bool{}
	for _, m := range modes {
		if seen[m.Name] == nil {
			seen[m.Name] = map[Axis]bool{}
		}
		seen[m.Name][m.Axis] = true
	}
	out := map[string]bool{}
	for name, axes := range seen {
		if len(axes) > 1 {
			out[name] = true
		}
	}
	return out
}
