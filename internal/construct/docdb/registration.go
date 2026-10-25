// SPDX-License-Identifier: AGPL-3.0-only

package docdb

import (
	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
)

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	// The resource vocabulary rides along so a bound cluster can be driven by a resource-axis
	// incident; an unbound cluster ignores it.
	modes := append(append([]failuremode.Mode(nil), FailureModes...), causal.FailureModes...)
	return core.ConstructReg{
		Kind:         "docdb",
		Doc:          "aws_docdb_* CloudWatch metrics for one DocumentDB cluster (WRITER/READER role split)",
		Scope:        core.ScopeBlueprint,
		NewConfig:    func() any { return &Config{} },
		Build:        Build,
		FailureModes: modes,
	}
}
