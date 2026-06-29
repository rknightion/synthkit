// SPDX-License-Identifier: AGPL-3.0-only

// Package dbo11ymysql renders the Grafana Database Observability MySQL signal set.
//
// Kind: "dbo11y_mysql"
// Scope: core.ScopeSubstrate (no blueprint label — dbo11y aggregates across fleets)
// Signals: Metrics + Logs
// Interval: 60 s
// Config: empty struct (all identity from fixture.Set.DB)
//
// Signal fidelity references: signals/dbo11y.md [slug: dbo11ymysql] (MySQL lane).
// Predecessor ground-truth: generator/internal/emit/dbo11y_mysql.go + dbo11y_shared.go.
package dbo11ymysql

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// Config is the (empty) per-instance configuration struct. All identity is resolved
// from the fixture.Set handed at Build; no blueprint-specific config is needed.
type Config struct{}

// construct is the live instance produced by Build.
type construct struct {
	db *fixture.DB
	st *state.State
}

// Build validates the fixture set and returns a ready construct instance. Returns an
// error if the fixture DB engine is not "mysql" — this is a loud, not-silent, error
// (CLAUDE.md: "loud, not silent").
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx == nil || fx.DB == nil {
		return nil, fmt.Errorf("dbo11y_mysql: fixture.Set.DB is required")
	}
	if !strings.EqualFold(fx.DB.Engine, "mysql") {
		return nil, fmt.Errorf("dbo11y_mysql: requires engine=mysql, got %q", fx.DB.Engine)
	}
	return &construct{db: fx.DB, st: state.NewState()}, nil
}

// ── per-series variation helpers ─────────────────────────────────────────────

// seriesVar returns a stable-but-living per-series multiplier ≈ 1: a deterministic
// baseline offset (shape.Engine.Spread — peer series sharing a formula get distinct, stable
// values) times a slow per-series drift (Wander — value is not frozen). amp sets the
// magnitude; volume/rows/bytes metrics use ≈0.18, rates ≈0.30.
func (c *construct) seriesVar(w *core.World, now time.Time, key string, amp float64) float64 {
	if w == nil || w.Shape == nil {
		return 1.0
	}
	return w.Shape.Spread(key, amp) * w.Shape.Wander(key, now, amp*0.4)
}

// ── core.Construct interface ─────────────────────────────────────────────────

func (c *construct) Kind() string                { return "dbo11y_mysql" }
func (c *construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics, core.Logs} }
func (c *construct) Interval() time.Duration     { return 60 * time.Second }

func (c *construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	if err := w.Metrics.Write(ctx, c.buildMetrics(now, w)); err != nil {
		return err
	}
	return w.Logs.Write(ctx, c.buildStreams(now, w))
}

// ── metrics ──────────────────────────────────────────────────────────────────

func (c *construct) buildMetrics(now time.Time, w *core.World) []promrw.Series {
	db := c.db
	bf := w.Shape.BusinessFactor(now) * w.Shape.Noise(0.1)
	tgt := dboTargetLabels(db)

	// Failure-mode amplifiers (parity with the Postgres dbo11y construct). Each returns 1.0
	// when its mode is inactive, so emission is identical to baseline unless the control plane
	// fires the incident. All scope to db.Name (the database-axis identity).
	connFactor := w.Shape.FailFactor(now, "connection_saturation", db.Name, 5)
	lockFactor := w.Shape.FailFactor(now, "lock_contention", db.Name, 5)
	slowFactor := w.Shape.FailFactor(now, "slow_query_storm", db.Name, 8)

	// ── core: connection_info ────────────────────────────────────────────────
	// ALL SIX metric labels required (03-dbo11y §3.1.1 / T1).
	ciLabels := mergeMaps(tgt, map[string]string{
		"db_instance_identifier": db.Name,
		"engine":                 "mysql",
		"engine_version":         db.EngineVersion,
	})
	c.st.Set("database_observability_connection_info", ciLabels, 1)

	// setup_consumers (§3.1.2) — five consumer names.
	for _, cname := range []string{
		"events_statements_cpu", "events_waits_current", "events_waits_history",
		"statements_digest", "global_instrumentation",
	} {
		c.st.Set("database_observability_setup_consumers_enabled",
			mergeMaps(tgt, map[string]string{"consumer_name": cname}), 1)
	}

	// global_variables (§3.1.3).
	c.st.Set("mysql_global_variables_performance_schema", tgt, 1)
	c.st.Set("mysql_global_variables_performance_schema_max_digest_length", tgt, 1024)
	c.st.Set("mysql_global_variables_performance_schema_max_sql_text_length", tgt, 1024)
	c.st.Set("mysql_global_variables_max_digest_length", tgt, 4096)
	c.st.Set("mysql_global_variables_max_connections", tgt, 512)

	// global_status (§3.1.4).
	// connection_saturation: connections climb toward max_connections (512). Base 20×bf×5 ≈
	// ≤100 at peak — well within the ceiling (asserted by TestConnectionSaturationAmplifies).
	c.st.Set("mysql_global_status_threads_connected", tgt, math.Round(20*bf*connFactor))
	c.st.Set("mysql_global_status_threads_running", tgt, math.Round(5*bf*connFactor))
	c.st.Add("mysql_global_status_questions", tgt, math.Round(500*bf))
	// slow_query_storm: the global slow-query counter accelerates.
	c.st.Add("mysql_global_status_slow_queries", tgt, math.Round(2*bf*slowFactor))
	c.st.Add("mysql_global_status_bytes_received", tgt, math.Round(1_000_000*bf))
	c.st.Add("mysql_global_status_bytes_sent", tgt, math.Round(2_000_000*bf))
	c.st.Add("mysql_global_status_uptime", tgt, 60)
	c.st.Set("mysql_global_status_innodb_buffer_pool_read_requests", tgt, math.Round(50_000*bf))
	c.st.Set("mysql_global_status_innodb_buffer_pool_reads", tgt, math.Round(200*bf))

	// ── perf_statements: six counters per (schema, digest) (§3.1.5, T2: NO digest_text). ──
	// seriesVar keys include the digest so each peer series gets a distinct stable offset
	// (amplitude 0.18 for volumes/rows, 0.30 for rates) layered on top of bf.
	if len(db.Databases) > 0 {
		schema := db.Databases[0]
		for _, q := range db.Queries {
			ql := mergeMaps(tgt, map[string]string{"schema": schema, "digest": q.ID})
			svVol := c.seriesVar(w, now, "stmt_total|"+schema+"|"+q.ID, 0.18)
			svRow := c.seriesVar(w, now, "rows_sent|"+schema+"|"+q.ID, 0.18)
			svExam := c.seriesVar(w, now, "rows_exam|"+schema+"|"+q.ID, 0.18)
			c.st.Add("mysql_perf_schema_events_statements_total", ql, 10*bf*svVol)
			var secDelta float64
			if q.Slow {
				// slow_query_storm: the slow-query right-tail latency stretches further.
				svRate := c.seriesVar(w, now, "stmt_sec|"+schema+"|"+q.ID, 0.30)
				secDelta = 0.5 * bf * slowFactor * svRate
			} else {
				svRate := c.seriesVar(w, now, "stmt_sec|"+schema+"|"+q.ID, 0.30)
				secDelta = 0.01 * bf * svRate
			}
			c.st.Add("mysql_perf_schema_events_statements_seconds_total", ql, secDelta)
			c.st.Add("mysql_perf_schema_events_statements_rows_sent_total", ql, 100*bf*svRow)
			c.st.Add("mysql_perf_schema_events_statements_rows_examined_total", ql, 500*bf*svExam)
			c.st.Add("mysql_perf_schema_events_statements_errors_total", ql, 0)
			var lockDelta float64
			if q.Slow {
				svLock := c.seriesVar(w, now, "lock_sec|"+schema+"|"+q.ID, 0.30)
				lockDelta = 0.1 * bf * svLock
			} else {
				svLock := c.seriesVar(w, now, "lock_sec|"+schema+"|"+q.ID, 0.30)
				lockDelta = 0.001 * bf * svLock
			}
			// lock_contention: lock-wait time accrues faster across every digest.
			c.st.Add("mysql_perf_schema_events_statements_lock_time_seconds_total", ql, lockDelta*lockFactor)
		}
	}

	// ── wait_event_seconds_total — MySQL-only (§3.1.6, T3). ──────────────────
	// Emitted only for slow queries.
	if len(db.Databases) > 0 {
		schema := db.Databases[0]
		for _, q := range db.Queries {
			if !q.Slow {
				continue
			}
			wl := mergeMaps(tgt, map[string]string{"digest": q.ID, "schema": schema})
			svWait := c.seriesVar(w, now, "wait_sec|"+schema+"|"+q.ID, 0.18)
			// slow_query_storm: time spent in waits grows with the latency right-tail.
			c.st.Add("database_observability_wait_event_seconds_total", wl, 0.2*bf*slowFactor*svWait)
		}
	}

	// ── replication / slave status (§3.1.7 / SK-13). ────────────────────────
	// MySQL 8.4 uses source/replica naming (SHOW REPLICA STATUS).
	// Labels: master_host, master_uuid (+ target labels). NO channel_name for default channel.
	rl := mergeMaps(tgt, map[string]string{
		"master_host": masterHost(db.ServerID),
		"master_uuid": masterUUID(db.ServerID),
	})
	var secondsBehind float64
	if w.Shape.Active(now, "replication_lag", db.Name) {
		secondsBehind = math.Round(w.Shape.FailFactor(now, "replication_lag", db.Name, 120))
	}
	c.st.Set("mysql_slave_status_seconds_behind_source", rl, secondsBehind)
	c.st.Set("mysql_slave_status_replica_io_running", rl, 1)
	c.st.Set("mysql_slave_status_replica_sql_running", rl, 1)
	c.st.Set("mysql_slave_status_source_port", rl, 3306)
	c.st.Set("mysql_slave_status_source_retry_count", rl, 86400)
	c.st.Set("mysql_slave_status_source_ssl_allowed", rl, 0)
	c.st.Set("mysql_slave_status_relay_log_pos", rl, 4)
	c.st.Set("mysql_slave_status_relay_log_space", rl, 154)
	c.st.Set("mysql_slave_status_exec_source_log_pos", rl, 4)
	c.st.Set("mysql_slave_status_skip_counter", rl, 0)
	c.st.Set("mysql_slave_status_last_errno", rl, 0)
	c.st.Set("mysql_slave_status_last_sql_errno", rl, 0)
	c.st.Set("mysql_slave_status_get_source_public_key", rl, 0)

	return c.st.Collect(now)
}

// ── logs ─────────────────────────────────────────────────────────────────────

func (c *construct) buildStreams(now time.Time, w *core.World) []loki.Stream {
	db := c.db
	if len(db.Databases) == 0 {
		return nil
	}
	schema := db.Databases[0]
	var out []loki.Stream

	out = append(out, c.sampleStreams(now, schema)...)
	out = append(out, c.waitStreams(now, schema)...)
	out = append(out, c.queryDetailStreams(now, schema)...)
	out = append(out, c.schemaStreams(now, schema)...)
	out = append(out, c.explainStreams(now, schema)...)
	out = append(out, c.healthStreams(now)...)
	// query_data_locks: only emitted when lock_contention is active (SK-14).
	if w.Shape.Active(now, "lock_contention", db.Name) {
		out = append(out, c.lockStreams(now)...)
	}
	return out
}

// sampleStreams emits op="query_sample" (§3.2.1).
// Timestamp rule (M8/T7): line T = now − elapsed_time (query start).
func (c *construct) sampleStreams(now time.Time, schema string) []loki.Stream {
	qsLabels := dboStreamLabels(c.db, "query_sample")
	var lines []loki.Line
	const samplesPerQuery = 4
	for qi, q := range c.db.Queries {
		var elapsedMs float64
		if q.Slow {
			elapsedMs = 500 + float64(qi)*50
		} else {
			elapsedMs = 5 + float64(qi)*0.5
		}
		elapsedStr := fmt.Sprintf("%.6fms", elapsedMs)
		for s := range samplesPerQuery {
			elapsedDur := time.Duration(elapsedMs * float64(time.Millisecond))
			sampleOffset := time.Duration(s) * 15 * time.Second
			lineTime := now.Add(-elapsedDur).Add(-sampleOffset)
			tid := fmt.Sprintf("%d", 1000+qi*samplesPerQuery+s)
			eid := fmt.Sprintf("%d", 100+s)
			endeid := fmt.Sprintf("%d", 200+s)
			body := logfmtLine(
				kv{"level", "info"},
				kv{"schema", schema},
				kv{"user", "app_user"},
				kv{"client_host", "10.0.0.10"},
				kv{"thread_id", tid},
				kv{"event_id", eid},
				kv{"end_event_id", endeid},
				kv{"digest", q.ID},
				kv{"rows_examined", "100"},
				kv{"rows_sent", "10"},
				kv{"rows_affected", "0"},
				kv{"errors", "0"},
				kv{"max_controlled_memory", "4096b"},
				kv{"max_total_memory", "8192b"},
				kv{"cpu_time", fmt.Sprintf("%.6fms", elapsedMs*0.3)},
				kv{"elapsed_time", elapsedStr},
				kv{"elapsed_time_ms", elapsedStr}, // §B1: identical to elapsed_time (T13)
			)
			lines = append(lines, loki.Line{T: lineTime, Body: body})
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return []loki.Stream{{Labels: qsLabels, Lines: lines}}
}

// waitStreams emits op="wait_event" (v1, §3.2.2) and op="wait_event_v2" (§3.2.3).
// Only emitted for slow queries. Timestamp rule same as query_sample (T7).
func (c *construct) waitStreams(now time.Time, schema string) []loki.Stream {
	wev1Labels := dboStreamLabels(c.db, "wait_event")
	wev2Labels := dboStreamLabels(c.db, "wait_event_v2")

	var wev1Lines, wev2Lines []loki.Line
	const samplesPerQuery = 4

	for qi, q := range c.db.Queries {
		if !q.Slow {
			continue
		}
		elapsedMs := 500 + float64(qi)*50
		for s := range samplesPerQuery {
			elapsedDur := time.Duration(elapsedMs * float64(time.Millisecond))
			sampleOffset := time.Duration(s) * 15 * time.Second
			lineTime := now.Add(-elapsedDur).Add(-sampleOffset)

			tid := fmt.Sprintf("%d", 1000+qi*samplesPerQuery+s)
			eid := fmt.Sprintf("%d", 100+s)
			waitName, waitObj, waitType := waitEventForQuery(qi)

			wev1Body := logfmtLine(
				kv{"level", "info"},
				kv{"schema", schema},
				kv{"user", "app_user"},
				kv{"client_host", "10.0.0.10"},
				kv{"thread_id", tid},
				kv{"digest", q.ID},
				kv{"event_id", eid},
				kv{"wait_event_id", fmt.Sprintf("%d", 300+s)},
				kv{"wait_end_event_id", fmt.Sprintf("%d", 400+s)},
				kv{"wait_event_name", waitName},
				kv{"wait_object_name", waitObj},
				kv{"wait_object_type", "TABLE"},
				kv{"wait_time", fmt.Sprintf("%.6fms", elapsedMs*0.8)},
			)
			wev1Lines = append(wev1Lines, loki.Line{T: lineTime, Body: wev1Body})

			// wait_event_v2 adds wait_event_type classifier (§3.2.3).
			wev2Body := logfmtLine(
				kv{"level", "info"},
				kv{"schema", schema},
				kv{"user", "app_user"},
				kv{"client_host", "10.0.0.10"},
				kv{"thread_id", tid},
				kv{"digest", q.ID},
				kv{"event_id", eid},
				kv{"wait_event_id", fmt.Sprintf("%d", 300+s)},
				kv{"wait_end_event_id", fmt.Sprintf("%d", 400+s)},
				kv{"wait_event_name", waitName},
				kv{"wait_event_type", waitType},
				kv{"wait_object_name", waitObj},
				kv{"wait_object_type", "TABLE"},
				kv{"wait_time", fmt.Sprintf("%.6fms", elapsedMs*0.8)},
			)
			wev2Lines = append(wev2Lines, loki.Line{T: lineTime, Body: wev2Body})
		}
	}

	var out []loki.Stream
	if len(wev1Lines) > 0 {
		out = append(out, loki.Stream{Labels: wev1Labels, Lines: wev1Lines})
	}
	if len(wev2Lines) > 0 {
		out = append(out, loki.Stream{Labels: wev2Labels, Lines: wev2Lines})
	}
	return out
}

// waitEventForQuery returns (waitName, waitObject, waitType) cycling over the 5 InnoDB
// wait event examples from §3.2.3.
func waitEventForQuery(qi int) (string, string, string) {
	type entry struct{ name, obj, wtype string }
	table := []entry{
		{"wait/io/file/innodb/innodb_data_file", "innodb_data_file", "IO Wait"},
		{"wait/io/table/sql/handler", "users", "IO Wait"},
		{"wait/lock/table/sql/handler", "orders", "Lock Wait"},
		{"wait/synch/mutex/sql/LOCK_open", "LOCK_open", "Engine Wait"},
		{"wait/io/socket/sql/client_connection", "client_connection", "Network Wait"},
	}
	e := table[qi%len(table)]
	return e.name, e.obj, e.wtype
}

// queryDetailStreams emits op="query_association" (§3.2.4) and
// op="query_parsed_table_name" (§3.2.5).
func (c *construct) queryDetailStreams(now time.Time, schema string) []loki.Stream {
	qaLabels := dboStreamLabels(c.db, "query_association")
	qptnLabels := dboStreamLabels(c.db, "query_parsed_table_name")

	var qaLines, qptnLines []loki.Line
	for _, q := range c.db.Queries {
		qaLines = append(qaLines, loki.Line{
			T: now,
			Body: logfmtLine(
				kv{"level", "info"},
				kv{"schema", schema},
				kv{"parseable", "true"},
				kv{"digest", q.ID},
				kv{"digest_text", q.Text},
			),
		})
		for _, tbl := range q.Tables {
			qptnLines = append(qptnLines, loki.Line{
				T: now,
				Body: logfmtLine(
					kv{"level", "info"},
					kv{"schema", schema},
					kv{"digest", q.ID},
					kv{"table", tbl},
				),
			})
		}
	}

	var out []loki.Stream
	if len(qaLines) > 0 {
		out = append(out, loki.Stream{Labels: qaLabels, Lines: qaLines})
	}
	if len(qptnLines) > 0 {
		out = append(out, loki.Stream{Labels: qptnLabels, Lines: qptnLines})
	}
	return out
}

// schemaStreams emits op="table_detection" (§3.2.6) and op="create_statement" (§3.2.7).
func (c *construct) schemaStreams(now time.Time, schema string) []loki.Stream {
	tables := []string{"users", "orders", "inventory", "events", "audit_log"}
	tdLabels := dboStreamLabels(c.db, "table_detection")
	csLabels := dboStreamLabels(c.db, "create_statement")

	var tdLines, csLines []loki.Line
	for _, tbl := range tables {
		tdLines = append(tdLines, loki.Line{
			T: now,
			Body: logfmtLine(
				kv{"level", "info"},
				kv{"schema", schema},
				kv{"table", tbl},
			),
		})
		ddl := fmt.Sprintf("CREATE TABLE `%s` (`id` int NOT NULL AUTO_INCREMENT, `name` varchar(255), PRIMARY KEY (`id`)) ENGINE=InnoDB", tbl)
		csLines = append(csLines, loki.Line{
			T: now,
			Body: logfmtLine(
				kv{"level", "info"},
				kv{"schema", schema},
				kv{"table", tbl},
				kv{"create_statement", b64(ddl)},
				kv{"table_spec", b64json(tableSpecJSON(tbl))},
			),
		})
	}

	var out []loki.Stream
	if len(tdLines) > 0 {
		out = append(out, loki.Stream{Labels: tdLabels, Lines: tdLines})
	}
	if len(csLines) > 0 {
		out = append(out, loki.Stream{Labels: csLabels, Lines: csLines})
	}
	return out
}

// tableSpecJSON returns the §3.2.7 table_spec JSON structure (W1.4: PENDING field types).
func tableSpecJSON(tbl string) any {
	_ = tbl
	return map[string]any{
		"columns": []map[string]any{
			{"name": "id", "type": "int", "not_null": true, "auto_increment": true, "primary_key": true, "default_value": ""},
			{"name": "name", "type": "varchar(255)", "not_null": false, "auto_increment": false, "primary_key": false, "default_value": ""},
		},
		"indexes": []map[string]any{
			{"name": "PRIMARY", "type": "BTREE", "columns": []string{"id"}, "unique": true, "nullable": false},
		},
		"foreign_keys": []map[string]any{},
	}
}

// explainStreams emits op="explain_plan_output" (§3.2.8).
func (c *construct) explainStreams(now time.Time, schema string) []loki.Stream {
	epLabels := dboStreamLabels(c.db, "explain_plan_output")
	var lines []loki.Line
	for _, q := range c.db.Queries {
		lines = append(lines, loki.Line{
			T: now,
			Body: logfmtLine(
				kv{"level", "info"},
				kv{"schema", schema},
				kv{"digest", q.ID},
				kv{"explain_plan_output", b64json(explainPlanJSON(q.ID, c.db.EngineVersion))},
			),
		})
	}
	if len(lines) == 0 {
		return nil
	}
	return []loki.Stream{{Labels: epLabels, Lines: lines}}
}

// explainPlanJSON returns the §3.2.8 explain_plan_output JSON structure.
// W1.5 PENDING: operation/accessType full enum.
func explainPlanJSON(digest, engineVersion string) any {
	return map[string]any{
		"metadata": map[string]any{
			"databaseEngine":         "MySQL",
			"databaseVersion":        engineVersion,
			"queryIdentifier":        digest,
			"generatedAt":            "2026-06-10T13:00:00Z",
			"processingResult":       "success",
			"processingResultReason": "",
		},
		"plan": map[string]any{
			"operation": "Ordering Operation",
			"details": map[string]any{
				"estimatedRows": 0,
				"estimatedCost": 135387.02,
			},
			"children": []map[string]any{
				{
					"operation": "Nested Loop Join",
					"details": map[string]any{
						"estimatedRows": 37253,
						"joinAlgorithm": "nested_loop",
					},
					"children": []map[string]any{
						{
							"operation": "Table Scan",
							"details": map[string]any{
								"estimatedRows": 9,
								"alias":         "d",
								"accessType":    "index",
								"keyUsed":       "PRIMARY",
								"estimatedCost": 1.90,
								"condition":     "(d.id = e.dept_id)",
							},
						},
					},
				},
			},
		},
	}
}

// healthStreams emits op="health_status" (§3.2.10): three checks per tick.
// Alloy version gate: "v1.16.0" (MySQL — distinct from the Postgres health value "v1.16.3").
func (c *construct) healthStreams(now time.Time) []loki.Stream {
	hsLabels := dboStreamLabels(c.db, "health_status")
	checks := []struct{ check, result, value string }{
		{"AlloyVersion", "true", "v1.16.0"},
		{"RequiredGrantsPresent", "true", ""},
		{"PerformanceSchemaHasRows", "true", ""},
	}
	var lines []loki.Line
	for _, ch := range checks {
		lines = append(lines, loki.Line{
			T: now,
			Body: logfmtLine(
				kv{"level", "info"},
				kv{"check", ch.check},
				kv{"result", ch.result},
				kv{"value", ch.value},
			),
		})
	}
	return []loki.Stream{{Labels: hsLabels, Lines: lines}}
}

// lockStreams emits op="query_data_locks" (SK-14 / §3.2.11).
// ONLY called when lock_contention failure mode is active.
// Sourced from Alloy internal/component/database_observability/mysql/collector/locks.go:
// fields are waiting_digest, waiting_digest_text, blocking_digest, blocking_digest_text,
// waiting_timer_wait (ms), waiting_lock_time (ms), blocking_timer_wait (ms), blocking_lock_time (ms).
// Guard: if <2 queries, emit nothing (need a waiting and a blocking query).
func (c *construct) lockStreams(now time.Time) []loki.Stream {
	db := c.db
	if len(db.Queries) < 2 {
		return nil
	}
	dlLabels := dboStreamLabels(db, "query_data_locks")
	var lines []loki.Line

	// Emit 1–3 logfmt lines: pairs of waiting/blocking from the query list. With exactly two
	// queries only {0,1} is valid (indices 1 and 2 require a third query) — seeding {1,2}/{2,0}
	// unconditionally would panic on a 2-query catalogue (reachable via `observability.digests: 2`).
	pairs := [][2]int{{0, 1}}
	if len(db.Queries) >= 3 {
		pairs = append(pairs, [2]int{1, 2}, [2]int{2, 0})
	}
	for _, p := range pairs {
		wq := db.Queries[p[0]]
		bq := db.Queries[p[1]]
		waitMs := fmt.Sprintf("%.3f", 1200.0+float64(p[0])*150.0)
		lockMs := fmt.Sprintf("%.3f", 800.0+float64(p[0])*100.0)
		blockMs := fmt.Sprintf("%.3f", 2500.0+float64(p[1])*200.0)
		blockLockMs := fmt.Sprintf("%.3f", 1800.0+float64(p[1])*150.0)
		lines = append(lines, loki.Line{
			T: now,
			Body: logfmtLine(
				kv{"level", "info"},
				kv{"waiting_digest", wq.ID},
				kv{"waiting_digest_text", wq.Text},
				kv{"blocking_digest", bq.ID},
				kv{"blocking_digest_text", bq.Text},
				kv{"waiting_timer_wait", waitMs},
				kv{"waiting_lock_time", lockMs},
				kv{"blocking_timer_wait", blockMs},
				kv{"blocking_lock_time", blockLockMs},
			),
		})
	}
	if len(lines) == 0 {
		return nil
	}
	return []loki.Stream{{Labels: dlLabels, Lines: lines}}
}

// ── shared label constructors (§2.1 / §2.2 of extract) ──────────────────────

const dboJob = "integrations/db-o11y"

// dboTargetLabels returns the six Prometheus target labels present on every dbo11y
// metric series (03-dbo11y §2.1).
func dboTargetLabels(db *fixture.DB) map[string]string {
	out := map[string]string{
		"job":       dboJob,
		"instance":  db.InstanceKey,
		"server_id": db.ServerID,
	}
	// Provider fields come from fixture.DB.Cloud; absent = omitted (I13).
	if db.Cloud != nil {
		out["provider_name"] = db.Cloud.Provider
		out["provider_region"] = db.Cloud.Region
		out["provider_account"] = db.Cloud.AccountID
	}
	return out
}

// dboStreamLabels returns the four Loki stream labels for every dbo11y log stream
// (03-dbo11y §2.2).
func dboStreamLabels(db *fixture.DB, op string) map[string]string {
	return map[string]string{
		"job":       dboJob,
		"instance":  db.InstanceKey,
		"server_id": db.ServerID,
		"op":        op,
	}
}

// ── logfmt helpers ────────────────────────────────────────────────────────────

type kv struct{ k, v string }

// logfmtLine renders ordered key=value pairs as logfmt. Values containing spaces or
// special characters are double-quoted. Empty values are emitted as key="" (not omitted).
func logfmtLine(pairs ...kv) string {
	var sb strings.Builder
	for i, p := range pairs {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(p.k)
		sb.WriteByte('=')
		needsQuote := strings.ContainsAny(p.v, " \t\r\n\"\\")
		if needsQuote || p.v == "" {
			sb.WriteByte('"')
			sb.WriteString(strings.ReplaceAll(p.v, `"`, `\"`))
			sb.WriteByte('"')
		} else {
			sb.WriteString(p.v)
		}
	}
	return sb.String()
}

// b64 base64-encodes a plain string (standard encoding, §3.2.7 DDL fields).
func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// b64json marshals v to JSON then base64-encodes it (§3.2.7 table_spec / §3.2.8 explain).
func b64json(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}

// mergeMaps returns a new map containing all keys from base plus any overrides.
func mergeMaps(base, overrides map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overrides))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}
