// SPDX-License-Identifier: AGPL-3.0-only

package docdb

import "github.com/rknightion/synthkit/internal/failuremode"

// FailureModes are the AxisDatabase modes the DocumentDB construct responds to.
// Scoped to db.Name so a fired incident moves the CloudWatch lane coherently.
var FailureModes = []failuremode.Mode{
	{Name: "connection_saturation", Axis: failuremode.AxisDatabase, Help: "active connections climb toward max"},
	{Name: "slow_query_storm", Axis: failuremode.AxisDatabase, Help: "query latency right-tail spikes"},
}
