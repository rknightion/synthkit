// SPDX-License-Identifier: AGPL-3.0-only

package fleetmgmt

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "fleet_management",
		Doc:       "fake Fleet Management collector roster (Alloy self-metrics)",
		Scope:     core.ScopeSubstrate,
		Group:     core.GroupFeature, // Grafana Cloud product (features: section)
		NewConfig: NewConfig,
		Build:     Build,
	}
}
