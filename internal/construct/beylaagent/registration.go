// SPDX-License-Identifier: AGPL-3.0-only

package beylaagent

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:         Kind,
		Doc:          "Grafana Beyla agent self/internal metrics (beyla_internal_* / beyla_bpf_* from /internal/metrics; mode-aware, substrate-scoped)",
		Scope:        core.ScopeSubstrate,
		Group:        core.GroupIntegration,
		NewConfig:    NewConfig,
		Build:        Build,
		FailureModes: FailureModes,
	}
}
