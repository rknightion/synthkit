// SPDX-License-Identifier: AGPL-3.0-only

package dbo11ymysql

import (
	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
)

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	modes := append([]failuremode.Mode(nil), FailureModes...)
	modes = append(modes, causal.FailureModes...)
	return core.ConstructReg{
		Kind:         "dbo11y_mysql",
		Doc:          "Database Observability MySQL lane (metrics + log ops)",
		Scope:        core.ScopeSubstrate,
		NewConfig:    func() any { return &Config{} },
		Build:        Build,
		FailureModes: modes,
	}
}
