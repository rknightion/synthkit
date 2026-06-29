// SPDX-License-Identifier: AGPL-3.0-only

package bedrock

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:         "bedrock",
		Doc:          "aws_bedrock_* CloudWatch metrics for Bedrock model invocations, agents, and guardrails",
		Scope:        core.ScopeBlueprint,
		NewConfig:    func() any { return &Config{} },
		Build:        Build,
		FailureModes: FailureModes,
	}
}
