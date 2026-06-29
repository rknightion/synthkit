// SPDX-License-Identifier: AGPL-3.0-only

package docdb

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:         "docdb",
		Doc:          "aws_docdb_* CloudWatch metrics for one DocumentDB cluster (WRITER/READER role split)",
		Scope:        core.ScopeBlueprint,
		NewConfig:    func() any { return &Config{} },
		Build:        Build,
		FailureModes: FailureModes,
	}
}
