// SPDX-License-Identifier: AGPL-3.0-only

package k8sprofiling

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:         "k8s_profiling",
		Doc:          "k8s-monitoring continuous-profiling (Alloy eBPF process_cpu per pod)",
		Scope:        core.ScopeSubstrate,
		NewConfig:    func() any { return &Config{} },
		Build:        New,
		FailureModes: FailureModes,
	}
}
