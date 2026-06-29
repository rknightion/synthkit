// SPDX-License-Identifier: AGPL-3.0-only

package neptune

import "github.com/rknightion/synthkit/internal/failuremode"

// FailureModes are the AxisDatabase modes the Neptune construct responds to.
var FailureModes = []failuremode.Mode{
	{Name: "connection_saturation", Axis: failuremode.AxisDatabase, Help: "active connections climb toward max"},
	{Name: "slow_query_storm", Axis: failuremode.AxisDatabase, Help: "query latency right-tail spikes"},
}
