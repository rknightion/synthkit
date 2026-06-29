// SPDX-License-Identifier: AGPL-3.0-only

// Database is the Acme AI Database Health dashboard (acme_ai_platform blueprint).
//
// Two tabs:
//   - PostgreSQL (RDS + dbo11y): RDS CloudWatch gauges for capacity/IOPS/latency + dbo11y
//     internals (backends, locks by mode, bgwriter buffers, pg_stat_statements call/time
//     rates, query-sample logs).
//   - DocumentDB: DocDB CloudWatch gauges for CPU/connections/buffer-cache/document-ops/
//     opcounters/latency.
//
// Scope notes (load-bearing):
//   - aws_rds_* are CloudWatch metric-stream gauges (no blueprint label, per-period GAUGES —
//     NEVER rate()). Scoped by dimension_DBInstanceIdentifier via the $pg template var.
//   - aws_docdb_* are CloudWatch metric-stream gauges (no blueprint label). Scoped by
//     dimension_DBClusterIdentifier via the $docdb template var.
//   - dbo11y metrics (pg_stat_database_numbackends, pg_locks_count, pg_stat_bgwriter_*,
//     pg_stat_statements_*) are Prometheus counters/gauges scraped by the dbo11y integration.
//     They carry an `instance` URL label (e.g. "postgresql://acme-pg-dev.…") NOT
//     db_instance_identifier — scoped via pgInst. pg_stat_database_numbackends and
//     connection_info carry db_instance_identifier — scoped via pgId.
//   - dbo11y and RDS have NO trace_id: correlate by instance + time only.
//
// All metric/label names verified live on the example stack.
package acme_ai_platform

import (
	"github.com/rknightion/synthkit/dashboard"
)

// Database builds the Acme AI Database Health dashboard for the acme_ai_platform blueprint.
// uid: acme-ws1-database-health.
func Database(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws1-database-health", "Acme AI — Database Health")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ───────────────────────────────────────────────────────────────────────────────
	// scenario: hidden const so acme blueprint selectors resolve if needed.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform"))
	// pg: PostgreSQL RDS instance filter — scoped by CloudWatch dimension label.
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"pg", "PostgreSQL instance",
		"label_values(aws_rds_cpuutilization_average, dimension_DBInstanceIdentifier)"))
	// docdb: DocumentDB cluster filter — scoped by CloudWatch dimension label.
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"docdb", "DocumentDB cluster",
		"label_values(aws_docdb_cpuutilization_average, dimension_DBClusterIdentifier)"))

	// ── Selector helpers ────────────────────────────────────────────────────────────────────────
	// pgCW returns a CW selector for aws_rds_* scoped by $pg. extra (if non-empty) is
	// appended as an additional label matcher after a leading comma.
	pgCW := func(extra string) string {
		base := `{dimension_DBInstanceIdentifier=~"$pg"`
		if extra != "" {
			return base + "," + extra + "}"
		}
		return base + "}"
	}
	// ddCW returns a CW selector for aws_docdb_* scoped by $docdb.
	ddCW := func(extra string) string {
		base := `{dimension_DBClusterIdentifier=~"$docdb"`
		if extra != "" {
			return base + "," + extra + "}"
		}
		return base + "}"
	}
	// pgInst scopes dbo11y metrics that carry an `instance` URL label
	// (pg_locks_count, pg_stat_bgwriter_*, pg_stat_statements_*).
	pgInst := `instance=~"postgresql://$pg.*"`
	// pgId scopes dbo11y metrics that carry db_instance_identifier
	// (pg_stat_database_numbackends, connection_info).
	pgId := `db_instance_identifier=~"$pg",job="integrations/db-o11y"`

	// groupByPg is the per-instance group label for CWGauge on RDS metrics.
	groupByPg := []string{"dimension_DBInstanceIdentifier"}
	// groupByDd is the per-cluster group label for DocDB CWGauge metrics.
	groupByDd := []string{"dimension_DBClusterIdentifier"}

	// ── Tab 1: PostgreSQL (RDS + dbo11y) ────────────────────────────────────────────────────────

	// db-intro: context note explaining signal provenance and correlation approach.
	dashboard.AddPanel(&d, "db-intro",
		dashboard.TextPanel("PostgreSQL — signal overview",
			"### PostgreSQL — RDS CloudWatch + dbo11y\n\n"+
				"**RDS CloudWatch** (`aws_rds_*`) metrics are per-period **gauges** emitted by the"+
				" CloudWatch metric stream — use the `_average` / `_maximum` / `_minimum` / `_sum` suffixes"+
				" directly; never wrap in `rate()`.\n\n"+
				"**dbo11y** metrics (`pg_stat_*`, `pg_locks_count`) are scraped by the dbo11y"+
				" integration (`job=\"integrations/db-o11y\"`). Counters use `rate()`. The `instance`"+
				" label carries a PostgreSQL connection URL — scoped by URL prefix, not by"+
				" `dimension_DBInstanceIdentifier`.\n\n"+
				"**Correlation:** RDS and dbo11y share no `trace_id`; correlate by instance name + time."))

	// KPI stat tiles — current health snapshot.
	dashboard.AddPanel(&d, "db-st-conn",
		dashboard.StatTile("Connections (max)", "short",
			dashboard.PromTarget(
				dashboard.CWGauge("aws_rds_database_connections", "maximum", pgCW(""), groupByPg),
				"{{dimension_DBInstanceIdentifier}}"),
			dashboard.Threshold{Value: 0, Color: "green"},
			dashboard.Threshold{Value: 80, Color: "yellow"},
			dashboard.Threshold{Value: 160, Color: "red"}))

	dashboard.AddPanel(&d, "db-st-cpu",
		dashboard.StatTile("CPU % (max)", "percent",
			dashboard.PromTarget(
				dashboard.CWGauge("aws_rds_cpuutilization", "maximum", pgCW(""), groupByPg),
				"{{dimension_DBInstanceIdentifier}}"),
			dashboard.Threshold{Value: 0, Color: "green"},
			dashboard.Threshold{Value: 70, Color: "yellow"},
			dashboard.Threshold{Value: 90, Color: "red"}))

	// Freeable memory: CWGauge does avg; write min by directly for the minimum stat.
	dashboard.AddPanel(&d, "db-st-mem",
		dashboard.StatTile("Freeable memory (min)", "bytes",
			dashboard.PromTarget(
				`min by (dimension_DBInstanceIdentifier)(aws_rds_freeable_memory_minimum`+pgCW("")+`)`,
				"{{dimension_DBInstanceIdentifier}}"),
			dashboard.Threshold{Value: 0, Color: "red"},
			dashboard.Threshold{Value: 536870912, Color: "yellow"},
			dashboard.Threshold{Value: 1073741824, Color: "green"}))

	// Burst balance: min by for the minimum stat — low balance = risk of I/O throttle.
	dashboard.AddPanel(&d, "db-st-burst",
		dashboard.StatTile("Burst balance % (min)", "percent",
			dashboard.PromTarget(
				`min by (dimension_DBInstanceIdentifier)(aws_rds_burst_balance_minimum`+pgCW("")+`)`,
				"{{dimension_DBInstanceIdentifier}}"),
			dashboard.Threshold{Value: 0, Color: "red"},
			dashboard.Threshold{Value: 40, Color: "yellow"},
			dashboard.Threshold{Value: 80, Color: "green"}))

	// RDS capacity & throughput timeseries — per-period GAUGES, no rate().

	// CPU: overlay avg + max per instance to show sustained vs burst.
	dashboard.AddPanel(&d, "db-ts-cpu",
		dashboard.TimeseriesPanel("RDS CPU utilization (avg / max)", "percent",
			dashboard.PromTarget(
				dashboard.CWGauge("aws_rds_cpuutilization", "average", pgCW(""), groupByPg),
				"{{dimension_DBInstanceIdentifier}} avg").RefId("A"),
			dashboard.PromTarget(
				`max by (dimension_DBInstanceIdentifier)(aws_rds_cpuutilization_maximum`+pgCW("")+`)`,
				"{{dimension_DBInstanceIdentifier}} max").RefId("B")))

	dashboard.AddPanel(&d, "db-ts-conn",
		dashboard.TimeseriesPanel("RDS database connections (avg)", "short",
			dashboard.PromTarget(
				dashboard.CWGauge("aws_rds_database_connections", "average", pgCW(""), groupByPg),
				"{{dimension_DBInstanceIdentifier}}")))

	dashboard.AddPanel(&d, "db-ts-mem",
		dashboard.TimeseriesPanel("RDS freeable memory (min)", "bytes",
			dashboard.PromTarget(
				`min by (dimension_DBInstanceIdentifier)(aws_rds_freeable_memory_minimum`+pgCW("")+`)`,
				"{{dimension_DBInstanceIdentifier}}")))

	dashboard.AddPanel(&d, "db-ts-iops",
		dashboard.TimeseriesPanel("RDS IOPS (read / write avg)", "iops",
			dashboard.PromTarget(
				dashboard.CWGauge("aws_rds_read_iops", "average", pgCW(""), groupByPg),
				"{{dimension_DBInstanceIdentifier}} read").RefId("A"),
			dashboard.PromTarget(
				dashboard.CWGauge("aws_rds_write_iops", "average", pgCW(""), groupByPg),
				"{{dimension_DBInstanceIdentifier}} write").RefId("B")))

	dashboard.AddPanel(&d, "db-ts-lat",
		dashboard.TimeseriesPanel("RDS latency (read / write avg)", "s",
			dashboard.PromTarget(
				dashboard.CWGauge("aws_rds_read_latency", "average", pgCW(""), groupByPg),
				"{{dimension_DBInstanceIdentifier}} read").RefId("A"),
			dashboard.PromTarget(
				dashboard.CWGauge("aws_rds_write_latency", "average", pgCW(""), groupByPg),
				"{{dimension_DBInstanceIdentifier}} write").RefId("B")))

	// dbo11y internals — Prometheus counters/gauges from the dbo11y integration.

	// Active backends (gauge, sum by instance).
	dashboard.AddPanel(&d, "db-ts-backends",
		dashboard.TimeseriesPanel("dbo11y backends (pg_stat_database_numbackends)", "short",
			dashboard.PromTarget(
				`sum by (db_instance_identifier)(pg_stat_database_numbackends{`+pgId+`})`,
				"{{db_instance_identifier}}")))

	// Locks by mode — split by mode label to show which lock types dominate.
	dashboard.AddPanel(&d, "db-ts-locks",
		dashboard.TimeseriesPanel("dbo11y locks by mode (pg_locks_count)", "short",
			dashboard.PromTarget(
				`sum by (mode)(pg_locks_count{`+pgInst+`})`,
				"{{mode}}")))

	// bgwriter buffer rates — alloc vs clean (rate of counters).
	dashboard.AddPanel(&d, "db-ts-bgwriter",
		dashboard.TimeseriesPanel("dbo11y bgwriter buffers (rate)", "Bps",
			dashboard.PromTarget(
				`sum(rate(pg_stat_bgwriter_buffers_alloc{`+pgInst+`}[$__rate_interval]))`,
				"buffers_alloc").RefId("A"),
			dashboard.PromTarget(
				`sum(rate(pg_stat_bgwriter_buffers_clean{`+pgInst+`}[$__rate_interval]))`,
				"buffers_clean").RefId("B")))

	// Top slow-query digests by total execution time rate (join queryid → logs for SQL text).
	dashboard.AddPanel(&d, "db-tbl-digests",
		dashboard.TablePanel("Top slow-query digests (by total time rate) — join queryid to logs for SQL",
			dashboard.PromTarget(
				`topk(15, sum by (queryid)(rate(pg_stat_statements_seconds_total{`+pgInst+`}[$__rate_interval])))`,
				"")))

	// Statement call rate — top 8 query IDs by calls/s.
	dashboard.AddPanel(&d, "db-ts-calls",
		dashboard.TimeseriesPanel("dbo11y statement call rate", "ops",
			dashboard.PromTarget(
				`topk(8, sum by (queryid)(rate(pg_stat_statements_calls_total{`+pgInst+`}[$__rate_interval])))`,
				"{{queryid}}")))

	// Query-sample / wait-event / explain-plan logs from dbo11y.
	// op label values: query_sample, wait_event, query_association, explain_plan_output.
	dashboard.AddPanel(&d, "db-log-samples",
		dashboard.LogsPanel("dbo11y logs — query_sample | wait_event | query_association | explain_plan_output",
			dashboard.LokiTarget(
				`{job="integrations/db-o11y", instance=~"postgresql://$pg.*"}`,
				"")))

	// ── Tab 2: DocumentDB ────────────────────────────────────────────────────────────────────────

	// dd-intro: context note for DocDB tab.
	dashboard.AddPanel(&d, "dd-intro",
		dashboard.TextPanel("DocumentDB — signal overview",
			"### DocumentDB — CloudWatch\n\n"+
				"**DocDB CloudWatch** (`aws_docdb_*`) metrics are per-period **gauges** emitted by the"+
				" CloudWatch metric stream. The `dimension_DBClusterIdentifier` label identifies the"+
				" cluster; `dimension_Role` distinguishes writer vs reader nodes.\n\n"+
				"Use the `_average` / `_maximum` / `_minimum` / `_sum` suffixes directly — **never**"+
				" wrap in `rate()`. The `_sum` suffix is a **per-period count** (documents/ops in that"+
				" period), not a cumulative counter.\n\n"+
				"**Note:** dbo11y does not support DocumentDB; PostgreSQL internals are only on tab 1."))

	// KPI stat tiles.
	dashboard.AddPanel(&d, "dd-st-conn",
		dashboard.StatTile("Connections (max)", "short",
			dashboard.PromTarget(
				`max by (dimension_DBClusterIdentifier)(aws_docdb_database_connections_maximum`+ddCW("")+`)`,
				"{{dimension_DBClusterIdentifier}}"),
			dashboard.Threshold{Value: 0, Color: "green"},
			dashboard.Threshold{Value: 80, Color: "yellow"},
			dashboard.Threshold{Value: 160, Color: "red"}))

	dashboard.AddPanel(&d, "dd-st-cpu",
		dashboard.StatTile("CPU % (max)", "percent",
			dashboard.PromTarget(
				dashboard.CWGauge("aws_docdb_cpuutilization", "maximum", ddCW(""), groupByDd),
				"{{dimension_DBClusterIdentifier}}"),
			dashboard.Threshold{Value: 0, Color: "green"},
			dashboard.Threshold{Value: 70, Color: "yellow"},
			dashboard.Threshold{Value: 90, Color: "red"}))

	// Buffer cache hit ratio: high is good; thresholds reversed.
	dashboard.AddPanel(&d, "dd-st-cache",
		dashboard.StatTile("Buffer cache hit ratio (avg)", "percent",
			dashboard.PromTarget(
				dashboard.CWGauge("aws_docdb_buffer_cache_hit_ratio", "average", ddCW(""), groupByDd),
				"{{dimension_DBClusterIdentifier}}"),
			dashboard.Threshold{Value: 0, Color: "red"},
			dashboard.Threshold{Value: 90, Color: "yellow"},
			dashboard.Threshold{Value: 98, Color: "green"}))

	// DocDB timeseries — per-period gauges, no rate().

	dashboard.AddPanel(&d, "dd-ts-cpu",
		dashboard.TimeseriesPanel("DocDB CPU utilization (avg)", "percent",
			dashboard.PromTarget(
				dashboard.CWGauge("aws_docdb_cpuutilization", "average", ddCW(""), groupByDd),
				"{{dimension_DBClusterIdentifier}}")))

	dashboard.AddPanel(&d, "dd-ts-conn",
		dashboard.TimeseriesPanel("DocDB connections (avg)", "short",
			dashboard.PromTarget(
				dashboard.CWGauge("aws_docdb_database_connections", "average", ddCW(""), groupByDd),
				"{{dimension_DBClusterIdentifier}}")))

	dashboard.AddPanel(&d, "dd-ts-cache",
		dashboard.TimeseriesPanel("DocDB buffer cache hit ratio (avg)", "percent",
			dashboard.PromTarget(
				dashboard.CWGauge("aws_docdb_buffer_cache_hit_ratio", "average", ddCW(""), groupByDd),
				"{{dimension_DBClusterIdentifier}}")))

	// Document ops/period — _sum is a per-period gauge (count in the CloudWatch period), not
	// a cumulative counter; do NOT use rate().
	dashboard.AddPanel(&d, "dd-ts-docs",
		dashboard.TimeseriesPanel("DocDB documents/period", "short",
			dashboard.PromTarget(
				`sum by (dimension_DBClusterIdentifier)(aws_docdb_documents_inserted_sum`+ddCW("")+`)`,
				"{{dimension_DBClusterIdentifier}} inserted").RefId("A"),
			dashboard.PromTarget(
				`sum by (dimension_DBClusterIdentifier)(aws_docdb_documents_updated_sum`+ddCW("")+`)`,
				"{{dimension_DBClusterIdentifier}} updated").RefId("B"),
			dashboard.PromTarget(
				`sum by (dimension_DBClusterIdentifier)(aws_docdb_documents_deleted_sum`+ddCW("")+`)`,
				"{{dimension_DBClusterIdentifier}} deleted").RefId("C"),
			dashboard.PromTarget(
				`sum by (dimension_DBClusterIdentifier)(aws_docdb_documents_returned_sum`+ddCW("")+`)`,
				"{{dimension_DBClusterIdentifier}} returned").RefId("D")))

	// Opcounters/period — per-period gauges.
	dashboard.AddPanel(&d, "dd-ts-ops",
		dashboard.TimeseriesPanel("DocDB opcounters/period", "short",
			dashboard.PromTarget(
				`sum by (dimension_DBClusterIdentifier)(aws_docdb_opcounters_query_sum`+ddCW("")+`)`,
				"{{dimension_DBClusterIdentifier}} query").RefId("A"),
			dashboard.PromTarget(
				`sum by (dimension_DBClusterIdentifier)(aws_docdb_opcounters_insert_sum`+ddCW("")+`)`,
				"{{dimension_DBClusterIdentifier}} insert").RefId("B"),
			dashboard.PromTarget(
				`sum by (dimension_DBClusterIdentifier)(aws_docdb_opcounters_update_sum`+ddCW("")+`)`,
				"{{dimension_DBClusterIdentifier}} update").RefId("C"),
			dashboard.PromTarget(
				`sum by (dimension_DBClusterIdentifier)(aws_docdb_opcounters_delete_sum`+ddCW("")+`)`,
				"{{dimension_DBClusterIdentifier}} delete").RefId("D")))

	// Read/write latency — average, in milliseconds (CW reports ms for DocDB latency).
	dashboard.AddPanel(&d, "dd-ts-lat",
		dashboard.TimeseriesPanel("DocDB read / write latency (avg)", "ms",
			dashboard.PromTarget(
				dashboard.CWGauge("aws_docdb_read_latency", "average", ddCW(""), groupByDd),
				"{{dimension_DBClusterIdentifier}} read").RefId("A"),
			dashboard.PromTarget(
				dashboard.CWGauge("aws_docdb_write_latency", "average", ddCW(""), groupByDd),
				"{{dimension_DBClusterIdentifier}} write").RefId("B")))

	// ── Layout ──────────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("PostgreSQL (RDS + dbo11y)",
			dashboard.Section("",
				dashboard.Full("db-intro")),
			dashboard.Section("Health (current)",
				dashboard.Tile("db-st-conn"),
				dashboard.Tile("db-st-cpu"),
				dashboard.Tile("db-st-mem"),
				dashboard.Tile("db-st-burst")),
			dashboard.Section("RDS CloudWatch — capacity & throughput (per-period gauges)",
				dashboard.Half("db-ts-cpu"),
				dashboard.Half("db-ts-conn"),
				dashboard.Third("db-ts-mem"),
				dashboard.Third("db-ts-iops"),
				dashboard.Third("db-ts-lat")),
			dashboard.Section("dbo11y — PostgreSQL internals (counters → rate)",
				dashboard.Third("db-ts-backends"),
				dashboard.Third("db-ts-locks"),
				dashboard.Third("db-ts-bgwriter"),
				dashboard.Half("db-tbl-digests"),
				dashboard.Half("db-ts-calls")),
			dashboard.Section("dbo11y logs",
				dashboard.Tall("db-log-samples")),
		),
		dashboard.Tabbed("DocumentDB",
			dashboard.Section("",
				dashboard.Full("dd-intro")),
			dashboard.Section("Health (current)",
				dashboard.Third("dd-st-conn"),
				dashboard.Third("dd-st-cpu"),
				dashboard.Third("dd-st-cache")),
			dashboard.Section("CloudWatch (per-period gauges)",
				dashboard.Third("dd-ts-cpu"),
				dashboard.Third("dd-ts-conn"),
				dashboard.Third("dd-ts-cache"),
				dashboard.Half("dd-ts-docs"),
				dashboard.Half("dd-ts-ops"),
				dashboard.Full("dd-ts-lat")),
		),
	)
	return d, nil
}
