// SPDX-License-Identifier: AGPL-3.0-only

package elasticache

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "elasticache",
		Doc:       "aws_elasticache_* CloudWatch metrics for one cache cluster",
		Scope:     core.ScopeBlueprint,
		NewConfig: func() any { return &Config{} },
		Build:     build,
	}
}
