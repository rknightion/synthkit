// SPDX-License-Identifier: AGPL-3.0-only

package qualificationpipeline

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "qualification_pipeline",
		Doc:       "GitLab CI qualification/validation pipeline — gitlab_ci_pipeline_* exporter families + coined qualification_* suite signals",
		Scope:     core.ScopeSubstrate,
		Group:     core.GroupIntegration,
		NewConfig: NewConfig,
		Build:     Build,
	}
}
