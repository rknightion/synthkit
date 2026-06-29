// SPDX-License-Identifier: AGPL-3.0-only

package dbo11ymysql

import "github.com/rknightion/synthkit/internal/failuremode"

// FailureModes is the MySQL dbo11y construct's mode vocabulary. All four are WIRED to emission
// in dbo11ymysql.go via shape.Active/FailFactor scoped to db.Name (parity with the Postgres
// dbo11y construct):
//   - connection_saturation (FailFactor magAt1=5 → mysql_global_status_threads_connected/_running)
//   - replication_lag (FailFactor magAt1=120 + Active → mysql_slave_status_seconds_behind_source)
//   - lock_contention (FailFactor magAt1=5 → mysql_perf_schema_events_statements_lock_time_seconds_total
//     AND Active gates the query_data_locks log op)
//   - slow_query_storm (FailFactor magAt1=8 → slow-query events_statements_seconds_total,
//     mysql_global_status_slow_queries, database_observability_wait_event_seconds_total)
//
// Declaring all four is also load-bearing for validation: the resolver checks incident mode
// references against this set, so omitting any would make it REJECT blueprints that fire it.
var FailureModes = []failuremode.Mode{
	// Help text is the SHARED, construct-agnostic mode description: it MUST match the wording the
	// dbo11ypg + rds constructs use for the same (name, axis) so the blueprint-schema reference
	// collapses them to one canonical row per mode (it dedups by name+axis+help). Construct-specific
	// effects (e.g. this construct gating the query_data_locks op) are documented in the comment
	// above, not in Help.
	{Name: "connection_saturation", Axis: failuremode.AxisDatabase, Help: "active connections climb toward max"},
	{Name: "replication_lag", Axis: failuremode.AxisDatabase, Help: "replica falls behind primary"},
	{Name: "lock_contention", Axis: failuremode.AxisDatabase, Help: "lock waits climb"},
	{Name: "slow_query_storm", Axis: failuremode.AxisDatabase, Help: "query latency right-tail spikes"},
}
