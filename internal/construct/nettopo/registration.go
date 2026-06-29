// SPDX-License-Identifier: AGPL-3.0-only

package nettopo

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
//
// Scope ScopeSubstrate: the exporter's series carry no blueprint label; instances are
// disambiguated by (job, instance). The `instance` (exporter endpoint) must be unique
// across blueprints — two declarations sharing an instance silently collide.
// Group GroupIntegration: an external system Grafana Cloud ingests (declared under `integrations:`).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:         Kind,
		Doc:          "fake network-topology-exporter (SNMP topology discovery: devices/edges + discovery health, metrics + logs)",
		Scope:        core.ScopeSubstrate,
		Group:        core.GroupIntegration,
		NewConfig:    NewConfig,
		Build:        Build,
		FailureModes: FailureModes,
	}
}
