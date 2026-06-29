// SPDX-License-Identifier: AGPL-3.0-only

package aoss

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "aoss",
		Doc:       "aws_aoss_* CloudWatch metrics for Amazon OpenSearch Serverless (collection-scoped + OCU)",
		Scope:     core.ScopeBlueprint,
		NewConfig: func() any { return &Config{} },
		Build:     Build,
	}
}
