// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:         "k8s_cluster",
		Doc:          "k8s-monitoring substrate (KSM/cAdvisor/kubelet/node-exporter + conformance + events)",
		Scope:        core.ScopeSubstrate,
		NewConfig:    func() any { return &Config{} },
		Build:        New,
		FailureModes: FailureModes,
	}
}
