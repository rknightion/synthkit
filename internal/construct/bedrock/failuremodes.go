// SPDX-License-Identifier: AGPL-3.0-only

package bedrock

import "github.com/rknightion/synthkit/internal/failuremode"

// FailureModes are the AxisCloud modes the Bedrock construct responds to.
// Scoped to cloud.AccountID so a fired incident amplifies throttles coherently
// across all models in the affected account.
var FailureModes = []failuremode.Mode{
	{Name: "bedrock_throttle", Axis: failuremode.AxisCloud, Help: "Bedrock invocation throttling climbs"},
}
