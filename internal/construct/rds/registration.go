// SPDX-License-Identifier: AGPL-3.0-only

package rds

import (
	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
)

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	// The resource vocabulary rides along so a bound instance can be driven by a resource-axis
	// incident; an unbound instance ignores it.
	modes := append(append([]failuremode.Mode(nil), FailureModes...), causal.FailureModes...)
	return core.ConstructReg{
		Kind:         "rds",
		Doc:          "aws_rds_* CloudWatch metrics for one database instance",
		Scope:        core.ScopeBlueprint,
		NewConfig:    func() any { return &Config{} },
		Build:        Build,
		FailureModes: modes,
	}
}
