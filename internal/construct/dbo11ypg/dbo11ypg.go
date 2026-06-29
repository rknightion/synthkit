// SPDX-License-Identifier: AGPL-3.0-only

// Package dbo11ypg is the synthkit construct for the Grafana Database Observability
// (dbo11y) PostgreSQL integration.
//
// Kind:     "dbo11y_postgres"
// Scope:    ScopeSubstrate  (no blueprint label — vendor-app conformance)
// Signals:  [Metrics, Logs]
// Interval: 60s
// Config:   empty struct (all identity comes from fixture.DB)
//
// Build requires fx.DB with Engine=="postgres"; returns an error for other engines.
//
// Signal fidelity:
//   - database_observability_connection_info with ALL SIX metric labels (engine="postgres").
//   - pg_stat_statements_* per fx.DB.Queries; queryid = Query.ID (decimal int64 string).
//   - NO database_observability_wait_event_seconds_total (MySQL-only; absence is contract).
//   - health_status with Alloy version "v1.16.3"; six Postgres health checks.
//   - Loki ops: query_sample (ts = now−query_time), wait_event/wait_event_v2 (Postgres
//     wait events as LOGS only), query_association (queryid= field), explain_plan_output
//     (schema=/digest= keys), table_detection/create_statement schema ops.
//   - Duration fields use Go time.Duration.String() format ("150ms", "2m0s").
//   - Counters cumulative via state.Add; info/gauges via state.Set.
//
// References: signals/dbo11y.md [slug: dbo11ypg]; predecessor
// generator/internal/emit/dbo11y_postgres.go (READ-ONLY).
package dbo11ypg

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/state"
)

const kind = "dbo11y_postgres"

// Config is the per-instance configuration struct. All identity derives from fixture.DB;
// this struct carries no fields in v1 (the database declaration in the blueprint YAML
// provides engine/version/name/observability mode).
type Config struct{}

// Reg is the ConstructReg entry for dbo11y_postgres.
var Reg = core.ConstructReg{
	Kind:      kind,
	Doc:       "Grafana Database Observability for PostgreSQL (pg_stat_statements metrics + dbo11y log ops)",
	Scope:     core.ScopeSubstrate,
	NewConfig: func() any { return &Config{} },
	Build:     build,
}

func build(cfg any, fx *fixture.Set) (core.Construct, error) {
	db := fx.DB
	if db == nil {
		return nil, fmt.Errorf("%s: fixture.Set.DB is nil", kind)
	}
	if db.Engine != "postgres" {
		return nil, fmt.Errorf("%s: requires engine=postgres, got %q", kind, db.Engine)
	}
	return &construct{db: db, st: state.NewState()}, nil
}

// construct is the per-instance renderer.
type construct struct {
	db *fixture.DB
	st *state.State
}

func (c *construct) Kind() string                { return kind }
func (c *construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics, core.Logs} }
func (c *construct) Interval() time.Duration     { return 60 * time.Second }

func (c *construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	c.buildMetrics(now, w)
	batch := c.st.Collect(now)
	if err := w.Metrics.Write(ctx, batch); err != nil {
		return err
	}
	streams := c.buildStreams(now)
	if len(streams) == 0 {
		return nil
	}
	return w.Logs.Write(ctx, streams)
}

// ── metric groups ─────────────────────────────────────────────────────────────

func (c *construct) buildMetrics(now time.Time, w *core.World) {
	db := c.db
	bf := w.Shape.BusinessFactor(now) * w.Shape.Noise(0.1)
	tgt := targetLabels(db)

	// core: connection_info + pg_up + pg_stat_database_numbackends + replication + locks + bgwriter
	{
		// database_observability_connection_info gauge = 1.
		// ALL SIX metric labels required (T1 / §4.1.1).
		ciLabels := mergeMaps(tgt, map[string]string{
			"db_instance_identifier": db.Name,
			"engine":                 "postgres",
			"engine_version":         db.EngineVersion,
		})
		c.st.Set("database_observability_connection_info", ciLabels, 1)

		// pg_up — standard postgres_exporter liveness gauge.
		c.st.Set("pg_up", tgt, 1)

		// pg_stat_database_numbackends.
		connFactor := w.Shape.FailFactor(now, "connection_saturation", db.Name, 5)
		c.st.Set("pg_stat_database_numbackends",
			mergeMaps(tgt, map[string]string{"datname": db.Databases[0]}),
			math.Round(15*bf*connFactor))

		// pg_replication_* gauges.
		lagFactor := w.Shape.FailFactor(now, "replication_lag", db.Name, 120)
		lagBytes := 0.0
		if w.Shape.Active(now, "replication_lag", db.Name) {
			lagBytes = 1_000_000 * lagFactor
		}
		c.st.Set("pg_replication_is_replica", tgt, 0)
		c.st.Set("pg_replication_lag", tgt, lagBytes)

		// pg_locks_count.
		lockFactor := w.Shape.FailFactor(now, "lock_contention", db.Name, 5)
		c.st.Set("pg_locks_count",
			mergeMaps(tgt, map[string]string{"mode": "ExclusiveLock", "datname": db.Databases[0]}),
			math.Round(2*bf*lockFactor))

		// pg_stat_bgwriter counters.
		c.st.Add("pg_stat_bgwriter_buffers_clean", tgt, math.Round(10*bf))
		c.st.Add("pg_stat_bgwriter_buffers_alloc", tgt, math.Round(100*bf))

		// pg_postmaster_start_time_seconds — Unix timestamp 48h ago.
		c.st.Set("pg_postmaster_start_time_seconds", tgt, float64(now.Add(-48*time.Hour).Unix()))
	}

	// errors: database_observability_pg_errors_total + pg_error_log_parse_failures_total.
	{
		type pgErr struct{ severity, sqlstate, sqlstateClass string }
		errs := []pgErr{
			{"ERROR", "42P01", "42"}, // undefined_table
			{"ERROR", "23505", "23"}, // unique_violation
			{"FATAL", "53300", "53"}, // too_many_connections
		}
		for _, er := range errs {
			el := mergeMaps(tgt, map[string]string{
				"severity":       er.severity,
				"sqlstate":       er.sqlstate,
				"sqlstate_class": er.sqlstateClass,
				"datname":        db.Databases[0],
				"user":           "app_user",
			})
			// Per-error-class variation so peer series (different sqlstate) emit distinct values.
			errV := c.seriesVar(w, now, "err|"+er.sqlstate, 0.30)
			// Do NOT round: math.Round(0.5*bf*errV) collapses every sqlstate to 0 (the term is
			// <1 for all classes), reintroducing the lockstep this variation removes. Counters are
			// float64, so fractional per-tick increments are fine and keep peer classes distinct.
			var delta float64
			if er.severity == "FATAL" && w.Shape.Active(now, "connection_saturation", db.Name) {
				delta = 5 * bf * errV
			} else {
				delta = 0.5 * bf * errV
			}
			c.st.Add("database_observability_pg_errors_total", el, delta)
		}
		c.st.Add("database_observability_pg_error_log_parse_failures_total", tgt, 0)
	}

	// pg_stat_statements: three counter families per query.
	// NOTE: do NOT emit database_observability_wait_event_seconds_total (MySQL-only §4.1.10 / T3).
	{
		slowFactor := w.Shape.FailFactor(now, "slow_query_storm", db.Name, 8)
		for _, q := range db.Queries {
			// Labels: {tgt…, datname, queryid="<decimal string>"} — NO digest_text (§4.1.10).
			ql := mergeMaps(tgt, map[string]string{
				"datname": db.Databases[0],
				"queryid": q.ID, // decimal int64 string per §1.3 / T4
			})
			// Per-series variation: keyed on metric name + queryid so each peer query gets a
			// distinct, stable-but-drifting multiplier (amp=0.18 for volume, 0.30 for rates).
			callsV := c.seriesVar(w, now, "calls|"+q.ID, 0.30)
			rowsV := c.seriesVar(w, now, "rows|"+q.ID, 0.30)
			// Do not round here — fractional values preserve per-series distinctness across
			// queries (rounding collapses nearby floats to the same integer when the base is small).
			execDelta := math.Max(0.5, 10*bf*callsV)
			c.st.Add("pg_stat_statements_calls_total", ql, execDelta)
			var secDelta float64
			if q.Slow {
				secDelta = 0.5 * bf * slowFactor * c.seriesVar(w, now, "sec_slow|"+q.ID, 0.30)
			} else {
				secDelta = 0.01 * bf * c.seriesVar(w, now, "sec_fast|"+q.ID, 0.30)
			}
			c.st.Add("pg_stat_statements_seconds_total", ql, secDelta)
			rowsDelta := math.Max(1, 100*bf*rowsV)
			c.st.Add("pg_stat_statements_rows_total", ql, rowsDelta)
		}
	}
}

// ── log groups ────────────────────────────────────────────────────────────────

func (c *construct) buildStreams(now time.Time) []loki.Stream {
	var out []loki.Stream
	db := c.db
	datname := db.Databases[0]

	out = append(out, c.sampleStreams(now, datname)...)
	out = append(out, c.waitStreams(now, datname)...)
	out = append(out, c.queryDetailStreams(now, datname)...)
	out = append(out, c.schemaStreams(now, datname)...)
	out = append(out, c.explainStreams(now, datname)...)
	out = append(out, c.healthStreams(now)...)
	return out
}

// sampleStreams emits op="query_sample" (§4.2.1).
// Timestamp rule (M8 / T7): lineTime = now − query_time.
// Duration fields use Go time.Duration.String() format (T8).
// leader_pid="" is emitted as empty string, NOT omitted (§B.5).
func (c *construct) sampleStreams(now time.Time, datname string) []loki.Stream {
	db := c.db
	labels := streamLabels(db, "query_sample")
	var lines []loki.Line
	samplesPerQuery := 4
	for qi, q := range db.Queries {
		var queryDur time.Duration
		if q.Slow {
			baseMs := float64(30_000 + qi*5_000)
			queryDur = time.Duration(baseMs) * time.Millisecond
		} else {
			queryDur = time.Duration(5+qi) * 10 * time.Millisecond
		}
		xactDur := queryDur + 50*time.Millisecond
		queryTimeStr := queryDur.String()
		xactTimeStr := xactDur.String()

		for s := 0; s < samplesPerQuery; s++ {
			sampleOffset := time.Duration(s) * 15 * time.Second
			// §4.2.1 / T7: timestamp = query_start = now − query_time.
			lineTime := now.Add(-queryDur).Add(-sampleOffset)

			pid := strconv.Itoa(1000 + qi*samplesPerQuery + s)

			fields := []kv{
				{"level", "info"},
				{"datname", datname},
				{"pid", pid},
				{"leader_pid", ""}, // §B.5: empty string, NOT omitted
				{"user", "app_user"},
				{"app", "web-api"},
				{"client", "10.0.0.10:54321"},
				{"backend_type", "client backend"},
				{"state", "active"},
				{"xid", strconv.Itoa(500 + qi)},
				{"xmin", strconv.Itoa(400 + qi)},
				{"xact_time", xactTimeStr},
				{"query_time", queryTimeStr},
				{"queryid", q.ID},                   // T5: same string as metric label
				{"query", toPostgresParams(q.Text)}, // SK-15: parameterized SQL shape (I23: no row data)
			}
			if !q.Slow {
				cpuDur := queryDur / 3
				fields = append(fields, kv{"cpu_time", cpuDur.String()})
			}
			lines = append(lines, loki.Line{T: lineTime, Body: logfmtLine(fields)})
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return []loki.Stream{{Labels: labels, Lines: lines}}
}

// waitStreams emits op="wait_event" (v1) and op="wait_event_v2" (§4.2.2 / §4.2.3).
// Only slow queries generate wait events.
// wait_event_name = "<type>:<event>" (T9 colon-joined format).
// wait_event_v2 carries pre-classified wait_event_type buckets (T17).
func (c *construct) waitStreams(now time.Time, datname string) []loki.Stream {
	db := c.db
	wev1Labels := streamLabels(db, "wait_event")
	wev2Labels := streamLabels(db, "wait_event_v2")

	var wev1Lines, wev2Lines []loki.Line
	samplesPerQuery := 4

	for qi, q := range db.Queries {
		if !q.Slow {
			continue
		}
		baseMs := float64(30_000 + qi*5_000)
		queryDur := time.Duration(baseMs) * time.Millisecond
		waitDur := queryDur * 4 / 5
		waitDurStr := waitDur.String()

		for s := 0; s < samplesPerQuery; s++ {
			sampleOffset := time.Duration(s) * 15 * time.Second
			lineTime := now.Add(-queryDur).Add(-sampleOffset)
			pidInt := 1000 + qi*samplesPerQuery + s
			pid := strconv.Itoa(pidInt)

			// Cycle entries per sample so all wait-event types appear even when
			// only one slow query exists in the fixture (SK-15).
			rawType, rawEvent, v2Type, blocked := pgWaitEventForQuery(qi*samplesPerQuery + s)
			waitEventName := rawType + ":" + rawEvent // T9: "<type>:<event>"

			// blocked_by_pids: conditional on entry's blocked flag (SK-15).
			// blocked → "[<pid+1> <pid+2>]" (Postgres array text, space-separated).
			// not blocked → "[]".
			var blockedByPids string
			if blocked {
				blockedByPids = fmt.Sprintf("[%d %d]", pidInt+1, pidInt+2)
			} else {
				blockedByPids = "[]"
			}

			baseFields := []kv{
				{"level", "info"},
				{"datname", datname},
				{"pid", pid},
				{"leader_pid", ""},
				{"user", "app_user"},
				{"app", "web-api"},            // SK-15b: identity block matching query_sample
				{"client", "10.0.0.10:54321"}, // SK-15b: identity block matching query_sample
				{"backend_type", "client backend"},
				{"state", "waiting"},
				{"xid", "0"},
				{"xmin", "0"},
				{"wait_time", waitDurStr},
				{"wait_event", rawEvent},
				{"wait_event_name", waitEventName},
				{"blocked_by_pids", blockedByPids}, // SK-15c: conditional
				{"queryid", q.ID},
			}

			// wait_event v1: raw wait_event_type.
			// Insert wait_event_type after wait_time (index 12, before wait_event).
			// baseFields order: level(0) datname(1) pid(2) leader_pid(3) user(4)
			//   app(5) client(6) backend_type(7) state(8) xid(9) xmin(10)
			//   wait_time(11) wait_event(12) ...
			wev1Fields := make([]kv, 0, len(baseFields)+1)
			wev1Fields = append(wev1Fields, baseFields[:12]...)
			wev1Fields = append(wev1Fields, kv{"wait_event_type", rawType})
			wev1Fields = append(wev1Fields, baseFields[12:]...)
			wev1Lines = append(wev1Lines, loki.Line{T: lineTime, Body: logfmtLine(wev1Fields)})

			// wait_event_v2: classified wait_event_type bucket (T17).
			wev2Fields := make([]kv, 0, len(baseFields)+1)
			wev2Fields = append(wev2Fields, baseFields[:12]...)
			wev2Fields = append(wev2Fields, kv{"wait_event_type", v2Type})
			wev2Fields = append(wev2Fields, baseFields[12:]...)
			wev2Lines = append(wev2Lines, loki.Line{T: lineTime, Body: logfmtLine(wev2Fields)})
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

// pgWaitEventForQuery returns (rawType, rawEvent, v2ClassifiedType, blocked) cycling over §4.2.3 table.
// Entry order matches live-captured Postgres 18.3 wait events (SK-15 / cantfind resolution).
// blocked=true → blocked_by_pids is non-empty (Lock:tuple); blocked=false → blocked_by_pids="[]".
func pgWaitEventForQuery(qi int) (rawType, rawEvent, v2Type string, blocked bool) {
	type entry struct {
		raw, event, v2type string
		blocked            bool
	}
	entries := []entry{
		{"Lock", "tuple", "Lock Wait", true},          // 1st: live-captured lock-on-tuple (blocked)
		{"Timeout", "PgSleep", "Timeout Wait", false}, // 2nd: live-captured PgSleep timeout (not blocked)
		{"IO", "DataFileRead", "IO Wait", false},
		{"LWLock", "BufferContent", "Engine Wait", false},
		{"Client", "ClientRead", "Network Wait", false},
	}
	e := entries[qi%len(entries)]
	return e.raw, e.event, e.v2type, e.blocked
}

// queryDetailStreams emits op="query_association" (§4.2.4) + op="query_parsed_table_name" (§4.2.5).
// Postgres uses queryid=, querytext= (%q-quoted), datname= — NOT digest=/digest_text=/schema=.
func (c *construct) queryDetailStreams(now time.Time, datname string) []loki.Stream {
	db := c.db
	qaLabels := streamLabels(db, "query_association")
	qptnLabels := streamLabels(db, "query_parsed_table_name")

	var qaLines, qptnLines []loki.Line

	for _, q := range db.Queries {
		pgText := toPostgresParams(q.Text)

		// query_association (§4.2.4): queryid=, querytext= (%q-quoted), datname=.
		qaLines = append(qaLines, loki.Line{
			T: now,
			Body: logfmtLine([]kv{
				{"level", "info"},
				{"queryid", q.ID},
				{"querytext", pgText},
				{"datname", datname},
			}),
		})

		// query_parsed_table_name (§4.2.5): queryid=, datname=, table=, validated="true".
		for _, tbl := range q.Tables {
			qptnLines = append(qptnLines, loki.Line{
				T: now,
				Body: logfmtLine([]kv{
					{"level", "info"},
					{"queryid", q.ID},
					{"datname", datname},
					{"table", tbl},
					{"validated", "true"},
				}),
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

// schemaStreams emits op="table_detection" (§4.2.6) + op="create_statement" (§4.2.7).
// Postgres create_statement uses datname= + schema= and table_spec= only (NO create_statement= DDL field; T14).
func (c *construct) schemaStreams(now time.Time, datname string) []loki.Stream {
	db := c.db
	tables := []string{"users", "orders", "inventory", "events", "audit_log"}
	schemaName := "public"

	tdLabels := streamLabels(db, "table_detection")
	csLabels := streamLabels(db, "create_statement")

	var tdLines, csLines []loki.Line
	for _, tbl := range tables {
		// table_detection: datname=, schema=, table=.
		tdLines = append(tdLines, loki.Line{
			T: now,
			Body: logfmtLine([]kv{
				{"level", "info"},
				{"datname", datname},
				{"schema", schemaName},
				{"table", tbl},
			}),
		})

		// create_statement: datname=, schema=, table=, table_spec=.
		// No create_statement= DDL field for Postgres (T14).
		tableSpec := pgTableSpecJSON(tbl)
		csLines = append(csLines, loki.Line{
			T: now,
			Body: logfmtLine([]kv{
				{"level", "info"},
				{"datname", datname},
				{"schema", schemaName},
				{"table", tbl},
				{"table_spec", b64json(tableSpec)},
			}),
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

// pgTableSpecJSON returns the §4.2.7 table_spec JSON for a Postgres table.
// Column default_value uses nextval()/sequence syntax per dossier §B.10.
func pgTableSpecJSON(tbl string) map[string]any {
	return map[string]any{
		"columns": []map[string]any{
			{
				"name":           "id",
				"type":           "bigint",
				"not_null":       true,
				"auto_increment": true,
				"primary_key":    true,
				"default_value":  fmt.Sprintf("nextval('%s_id_seq'::regclass)", tbl),
			},
			{
				"name":           "name",
				"type":           "character varying",
				"not_null":       true,
				"auto_increment": false,
				"primary_key":    false,
				"default_value":  "",
			},
		},
		"indexes": []map[string]any{
			{
				"name":     tbl + "_pkey",
				"type":     "btree",
				"columns":  []string{"id"},
				"unique":   true,
				"nullable": false,
			},
		},
	}
}

// explainStreams emits op="explain_plan_output" (§4.2.8).
// ⚠ Critical: uses schema=<datname> and digest=<queryid> — NOT datname=/queryid= (T6 / §B.8).
func (c *construct) explainStreams(now time.Time, datname string) []loki.Stream {
	db := c.db
	epLabels := streamLabels(db, "explain_plan_output")
	var lines []loki.Line

	for _, q := range db.Queries {
		plan := pgExplainPlanJSON(q.ID, db.EngineVersion, now)
		lines = append(lines, loki.Line{
			T: now,
			Body: logfmtLine([]kv{
				{"level", "info"},
				{"schema", datname}, // T6 / §B.8: schema= not datname=
				{"digest", q.ID},    // T6 / §B.8: digest= not queryid= (same value as queryid label)
				{"explain_plan_output", b64json(plan)},
			}),
		})
	}

	if len(lines) == 0 {
		return nil
	}
	return []loki.Stream{{Labels: epLabels, Lines: lines}}
}

// pgExplainPlanJSON returns the §4.2.8 explain_plan JSON for Postgres.
// metadata.databaseEngine = "PostgreSQL" (T6 / §B.8 Postgres discriminator).
func pgExplainPlanJSON(queryID, engineVersion string, now time.Time) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"databaseEngine":         "PostgreSQL",
			"databaseVersion":        engineVersion,
			"queryIdentifier":        queryID,
			"generatedAt":            now.Format(time.RFC3339),
			"processingResult":       "success",
			"processingResultReason": "",
		},
		"plan": map[string]any{
			"operation": "Gather Merge",
			"details": map[string]any{
				"estimatedRows": 141178,
				"estimatedCost": 16235.47,
				"sortKeys":      []string{"e.last_name", "e.first_name"},
			},
			"children": []map[string]any{
				{
					"operation": "Sort",
					"details": map[string]any{
						"estimatedRows": 141178,
						"estimatedCost": 353.0,
					},
					"children": []map[string]any{
						{
							"operation": "Seq Scan",
							"details": map[string]any{
								"estimatedRows": 4784,
								"estimatedCost": 0.0,
								"alias":         "e",
							},
						},
					},
				},
			},
		},
	}
}

// healthStreams emits op="health_status" with six Postgres checks (§4.2.9).
// AlloyVersion: "v1.16.3" — the running Alloy version (T16). (v1.17.0 was a stale
// pre-release placeholder; v1.16.3 is the latest real release. See signals/dbo11y.md.)
func (c *construct) healthStreams(now time.Time) []loki.Stream {
	db := c.db
	hsLabels := streamLabels(db, "health_status")
	checks := []struct{ check, result, value string }{
		{"AlloyVersion", "true", "v1.16.3"}, // T16: running Alloy version (latest real release)
		{"PgStatStatementsEnabled", "true", ""},
		{"TrackActivityQuerySize", "true", "4096"},
		{"ComputeQueryIdEnabled", "true", "auto"},
		{"MonitoringUserPrivileges", "true",
			"can_select_view=true,has_pg_monitor_role=true,has_pg_read_all_stats_role=false,sees_insufficient_privilege=false"},
		{"PgStatStatementsHasRows", "true", ""},
	}
	var lines []loki.Line
	for _, ch := range checks {
		lines = append(lines, loki.Line{
			T: now,
			Body: logfmtLine([]kv{
				{"level", "info"},
				{"check", ch.check},
				{"result", ch.result},
				{"value", ch.value},
			}),
		})
	}
	return []loki.Stream{{Labels: hsLabels, Lines: lines}}
}

// ── per-series variation helpers ──────────────────────────────────────────────

// seriesVar returns a stable-but-drifting per-series multiplier ≈ 1±amp.
// Spread gives a time-invariant hash-based baseline so peer series (different queryid,
// sqlstate, etc.) emit distinct values; Wander adds slow drift so values are not frozen.
func (c *construct) seriesVar(w *core.World, now time.Time, key string, amp float64) float64 {
	if w == nil || w.Shape == nil {
		return 1.0
	}
	return w.Shape.Spread(key, amp) * w.Shape.Wander(key, now, amp*0.4)
}

// ── label helpers ─────────────────────────────────────────────────────────────

const dboJob = "integrations/db-o11y"

// targetLabels returns the six Prometheus target labels every dbo11y metric carries (§2.1).
func targetLabels(db *fixture.DB) map[string]string {
	cloud := db.Cloud
	providerName := "aws"
	providerRegion := cloud.Region
	providerAccount := cloud.AccountID
	if cloud.Provider != "" {
		providerName = cloud.Provider
	}
	return map[string]string{
		"job":              dboJob,
		"instance":         db.InstanceKey,
		"server_id":        db.ServerID,
		"provider_name":    providerName,
		"provider_region":  providerRegion,
		"provider_account": providerAccount,
	}
}

// streamLabels returns the four Loki stream labels every dbo11y stream carries (§2.2).
func streamLabels(db *fixture.DB, op string) map[string]string {
	return map[string]string{
		"job":       dboJob,
		"instance":  db.InstanceKey,
		"server_id": db.ServerID,
		"op":        op,
	}
}

// mergeMaps returns a new map containing all keys from base + overrides.
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

// ── logfmt helpers ────────────────────────────────────────────────────────────

// kv is an ordered logfmt key/value pair. Order matters: it must match the real Alloy
// field order so the app's `| logfmt` parse + line_format works correctly.
type kv struct{ K, V string }

// logfmtLine renders `k="v" k="v"` with Go-style double-quote escaping of values
// (matches Alloy's logfmt encoder; fields are %q-quoted per dossier §B.3).
func logfmtLine(pairs []kv) string {
	var b strings.Builder
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(p.K)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(p.V))
	}
	return b.String()
}

// b64json base64-encodes the JSON of v (for table_spec / explain_plan_output).
func b64json(v any) string {
	raw, _ := json.Marshal(v)
	return base64.StdEncoding.EncodeToString(raw)
}

// toPostgresParams converts MySQL-style `?` placeholders to PostgreSQL `$N` style.
// Used for querytext in query_association (§4.2.4).
func toPostgresParams(sql string) string {
	var out []byte
	n := 1
	for i := 0; i < len(sql); i++ {
		if sql[i] == '?' {
			out = append(out, []byte(fmt.Sprintf("$%d", n))...)
			n++
		} else {
			out = append(out, sql[i])
		}
	}
	return string(out)
}
