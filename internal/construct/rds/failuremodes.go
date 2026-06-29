// SPDX-License-Identifier: AGPL-3.0-only

package rds

import "github.com/rknightion/synthkit/internal/failuremode"

// FailureModes are the AxisDatabase modes the RDS CloudWatch construct responds to, all scoped to
// db.Name — the SAME scope key the dbo11y constructs use, so a single fired DB incident moves the
// CloudWatch lane and the dbo11y lane coherently for the same database identity. Wired in rds.go
// via shape.Active/FailFactor:
//   - connection_saturation (FailFactor magAt1=5 → aws_rds_database_connections + aws_rds_cpuutilization)
//   - lock_contention (FailFactor magAt1=5 → aws_rds_disk_queue_depth)
//   - slow_query_storm (FailFactor magAt1=4 → aws_rds_read_latency + aws_rds_write_latency)
//
// replication_lag is registered below for vocabulary parity (keeps RDS-only DBs reactive) but drives
// NO RDS series: a standalone primary emits no ReplicaLag (live-reference-confirmed replica-only) — only the
// dbo11y lane surfaces it (seconds_behind_source).
//
// This vocabulary is intentionally identical to the dbo11y constructs' (same names + AxisDatabase).
// The registry unions modes across constructs and the resolver validates incident references against
// that union, so declaring the same set keeps an RDS-only database (cloudwatch:true, dbo11y:false)
// reactive to the very incidents the operator can already fire — without it, an RDS-only DB would be
// inert to DB incidents.
//
// Help text is the SHARED, construct-agnostic mode description: it MUST match the wording the dbo11y
// constructs use for the same (name, axis) so the blueprint-schema reference collapses them to one
// canonical row per mode (it dedups by name+axis+help). Construct-specific effects (e.g. this
// construct backing up the I/O queue) are documented in the comment above, not in Help.
var FailureModes = []failuremode.Mode{
	{Name: "connection_saturation", Axis: failuremode.AxisDatabase, Help: "active connections climb toward max"},
	{Name: "replication_lag", Axis: failuremode.AxisDatabase, Help: "replica falls behind primary"},
	{Name: "lock_contention", Axis: failuremode.AxisDatabase, Help: "lock waits climb"},
	{Name: "slow_query_storm", Axis: failuremode.AxisDatabase, Help: "query latency right-tail spikes"},
}
