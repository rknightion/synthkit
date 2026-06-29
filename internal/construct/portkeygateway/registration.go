// SPDX-License-Identifier: AGPL-3.0-only

package portkeygateway

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "portkey_gateway",
		Doc:       "Portkey LLM gateway /metrics scrape (portkey_* custom metrics + node_* runtime subset)",
		Scope:     core.ScopeSubstrate,
		Group:     core.GroupIntegration,
		NewConfig: NewConfig,
		Build:     Build,
	}
}
