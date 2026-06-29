// SPDX-License-Identifier: AGPL-3.0-only

package ec2

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "ec2",
		Doc:       "aws_ec2_* CloudWatch metrics correlated to the cluster nodes",
		Scope:     core.ScopeBlueprint,
		NewConfig: func() any { return &Config{} },
		Build:     New,
	}
}
