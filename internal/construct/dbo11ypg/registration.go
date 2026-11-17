// SPDX-License-Identifier: AGPL-3.0-only

package dbo11ypg

import (
	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
)

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:         "dbo11y_postgres",
		Doc:          "Database Observability Postgres lane (metrics + log ops)",
		Scope:        core.ScopeSubstrate,
		NewConfig:    func() any { return &Config{} },
		Build:        build,
		FailureModes: append(append([]failuremode.Mode(nil), FailureModes...), causal.FailureModes...),
	}
}
