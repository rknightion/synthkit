// SPDX-License-Identifier: AGPL-3.0-only

package rds

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:         "rds",
		Doc:          "aws_rds_* CloudWatch metrics for one database instance",
		Scope:        core.ScopeBlueprint,
		NewConfig:    func() any { return &Config{} },
		Build:        Build,
		FailureModes: FailureModes,
	}
}
