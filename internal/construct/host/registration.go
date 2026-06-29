// SPDX-License-Identifier: AGPL-3.0-only

package host

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass in Task 4.3).
//
// Scope ScopeSubstrate: the exporter's series carry no blueprint label; instances are
// disambiguated by (job, instance). The `instance` (hostname) must be unique across
// blueprints — the load-time collision gate (Task 4.2b) rejects duplicates.
//
// Group is omitted ("") → a TOPOLOGY kind: NOT declarable under features:/integrations:.
// Hosts are emitted by a bespoke resolver pass over the top-level `hosts:` list (Task 4.2),
// the same shape as ec2/rds (also Group "").
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      Kind,
		Doc:       "traditional non-k8s host (node/windows/macos exporter + optional docker cadvisor), metrics + logs",
		Scope:     core.ScopeSubstrate,
		NewConfig: func() any { return &Config{} },
		Build:     Build,
	}
}
