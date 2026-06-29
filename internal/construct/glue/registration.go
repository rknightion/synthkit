// SPDX-License-Identifier: AGPL-3.0-only

package glue

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "glue",
		Doc:       "aws_glue_* CloudWatch metrics for AWS Glue ETL jobs (namespace: Glue, no AWS/ prefix)",
		Scope:     core.ScopeBlueprint,
		NewConfig: func() any { return &Config{} },
		Build:     Build,
	}
}
