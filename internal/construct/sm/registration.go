// SPDX-License-Identifier: AGPL-3.0-only

package sm

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "synthetic_monitoring",
		Doc:       "fake Synthetic Monitoring checks (metrics + logs)",
		Scope:     core.ScopeSubstrate, // §5: SM disambiguates by check name, not blueprint label
		Group:     core.GroupFeature,   // Grafana Cloud product (features: section)
		NewConfig: NewConfig,
		Build:     Build,
	}
}
