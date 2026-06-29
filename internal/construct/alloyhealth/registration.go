// SPDX-License-Identifier: AGPL-3.0-only

package alloyhealth

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "alloy_health",
		Doc:       "Alloy pipeline meta-health (otelcol_*) + content-strip sentinel",
		Scope:     core.ScopeSubstrate,
		NewConfig: NewConfig,
		Build:     Build,
	}
}
