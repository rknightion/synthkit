// SPDX-License-Identifier: AGPL-3.0-only

package envoygateway

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "envoy_gateway",
		Doc:       "Envoy Gateway control-plane (xds_*/watchable_*/controller_runtime_*) and data-plane (envoy_*) metrics",
		Scope:     core.ScopeSubstrate,
		NewConfig: func() any { return &Config{} },
		Build:     New,
	}
}
