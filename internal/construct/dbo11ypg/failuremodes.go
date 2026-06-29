// SPDX-License-Identifier: AGPL-3.0-only

package dbo11ypg

import "github.com/rknightion/synthkit/internal/failuremode"

// FailureModes are the modes the postgres dbo11y construct responds to. All four are implemented
// in dbo11ypg.go via shape.Eval/Active/FailFactor scoped to db.Name (the database-axis identity):
// connection_saturation (FailFactor magAt1=5), replication_lag (FailFactor magAt1=120 + Active),
// lock_contention (FailFactor magAt1=5), slow_query_storm (FailFactor magAt1=8). Declaring all four
// is load-bearing: the resolver validates incident mode references against the construct
// vocabulary, so omitting any would make it REJECT blueprints that fire that mode today.
var FailureModes = []failuremode.Mode{
	{Name: "connection_saturation", Axis: failuremode.AxisDatabase, Help: "active connections climb toward max"},
	{Name: "replication_lag", Axis: failuremode.AxisDatabase, Help: "replica falls behind primary"},
	{Name: "lock_contention", Axis: failuremode.AxisDatabase, Help: "lock waits climb"},
	{Name: "slow_query_storm", Axis: failuremode.AxisDatabase, Help: "query latency right-tail spikes"},
}
