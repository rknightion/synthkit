// SPDX-License-Identifier: AGPL-3.0-only

package karpenter

import "github.com/rknightion/synthkit/internal/core"

// Registration returns the ConstructReg for this construct.
// It is a single-owner wiring shim — added to the catalog by the runner wiring pass.
// DO NOT call this from init() or register it in a global registry.
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "karpenter",
		Doc:       "Karpenter node autoscaler metrics (karpenter_* + go_* + process_* + controller_runtime_*)",
		Scope:     core.ScopeSubstrate,
		NewConfig: func() any { return &Config{} },
		Build:     New,
	}
}
