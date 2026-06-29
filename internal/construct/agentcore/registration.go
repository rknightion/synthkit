// SPDX-License-Identifier: AGPL-3.0-only

package agentcore

import "github.com/rknightion/synthkit/internal/core"

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim — added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:         "agentcore",
		Doc:          "aws_bedrock_agentcore_* CloudWatch metrics for Bedrock AgentCore (invocation-class + resource-usage families)",
		Scope:        core.ScopeBlueprint,
		NewConfig:    func() any { return &Config{} },
		Build:        Build,
		FailureModes: FailureModes,
	}
}
