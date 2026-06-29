// SPDX-License-Identifier: AGPL-3.0-only

package coredns

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "core_dns",
		Doc:       "CoreDNS server/cache/forward metrics",
		Scope:     core.ScopeSubstrate,
		NewConfig: func() any { return &Config{} },
		Build:     New,
	}
}
