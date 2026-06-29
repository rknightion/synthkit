// SPDX-License-Identifier: AGPL-3.0-only

package cspgcp

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "csp_gcp",
		Doc:       "GCP Cloud Monitoring (stackdriver_*) metrics + logs",
		Scope:     core.ScopeSubstrate,
		Group:     core.GroupIntegration, // external source GC ingests (integrations: section)
		NewConfig: NewConfig,
		Build:     Build,
	}
}
