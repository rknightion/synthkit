// SPDX-License-Identifier: AGPL-3.0-only

package mwaa

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "mwaa",
		Doc:       "aws_mwaa_* + aws_amazonmwaa_* CloudWatch metrics for Amazon MWAA (Managed Workflows for Apache Airflow)",
		Scope:     core.ScopeBlueprint,
		NewConfig: func() any { return &Config{} },
		Build:     Build,
	}
}
