// SPDX-License-Identifier: AGPL-3.0-only

package cloudflare

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "cloudflare",
		Doc:       "Cloudflare zone + tunnel metrics (lablabs exporter shape)",
		Scope:     core.ScopeBlueprint,
		Group:     core.GroupIntegration, // external source GC ingests (integrations: section)
		NewConfig: func() any { return &Config{} },
		Build:     Build,
	}
}
