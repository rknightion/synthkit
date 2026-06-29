// SPDX-License-Identifier: AGPL-3.0-only

package argocd

import "github.com/rknightion/synthkit/internal/core"

// Registration returns the ConstructReg for the "argocd" construct.
// This shim is NOT added to runner/catalog.go — that is a single-owner wiring
// pass done separately.
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "argocd",
		Doc:       "Argo CD application-controller / server / repo-server / applicationset metrics",
		Scope:     core.ScopeSubstrate,
		NewConfig: func() any { return &Config{} },
		Build:     New,
	}
}
