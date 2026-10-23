// SPDX-License-Identifier: AGPL-3.0-only

package solace

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim - added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      Kind,
		Doc:       "Solace event-broker telemetry (solace_prometheus_exporter v1.21.0: per-queue and per-VPN spool, flow, rate, discard and connection series)",
		Scope:     core.ScopeSubstrate,
		Group:     core.GroupIntegration,
		NewConfig: func() any { return &Config{} },
		Build:     Build,
	}
}
