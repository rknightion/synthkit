// SPDX-License-Identifier: AGPL-3.0-only

package langsmitheval

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:         "langsmith_eval",
		Doc:          "LangSmith feedback API poll → langsmith_eval_* quality gauges (+ token counter)",
		Scope:        core.ScopeSubstrate,
		Group:        core.GroupIntegration,
		NewConfig:    func() any { return &Config{} },
		Build:        Build,
		FailureModes: FailureModes,
	}
}
