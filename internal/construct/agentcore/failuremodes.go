// SPDX-License-Identifier: AGPL-3.0-only

package agentcore

import "github.com/rknightion/synthkit/internal/failuremode"

// FailureModes are the AxisCloud modes the AgentCore construct responds to.
// Scoped to cloud.Region so a fired incident moves the CloudWatch lane coherently
// across all AgentCore series in the affected account/region.
var FailureModes = []failuremode.Mode{
	{
		Name: "agentcore_throttle",
		Axis: failuremode.AxisCloud,
		Help: "AgentCore request throttles + system_errors spike (region-scoped capacity constraint)",
	},
}
