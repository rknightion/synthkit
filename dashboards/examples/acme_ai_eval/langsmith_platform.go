// SPDX-License-Identifier: AGPL-3.0-only

// LangSmithPlatform is the AI Evaluation (acme-ai-eval blueprint) dashboard for the
// self-hosted LangSmith platform infrastructure exporters: Python process/runtime,
// ClickHouse, Redis, Postgres, nginx, and platform-internal traces.
//
// Ported from predecessor acme-eval-langsmith-platform.json (2026-06-16).
//
// Metric scope: ALL families here are SUBSTRATE-SCOPED (no blueprint label). The
// predecessor's {scenario="acme-ai-eval"} filter is DROPPED on every query; substrate
// disambiguation uses job=~"langsmith-.*" (set at scrape time) + env-scoped via IntSel.
//
// acme-ai-eval specifics: this dashboard covers platform-exporter health ONLY — there is no
// langsmith_eval_* or gen_ai_* content here (those are Acme AI-tenant signals, absent in
// the eval_standalone world). No use_case/project/workspace business filters.
//
// KNOWN GAPS — wired faithfully from predecessor; not yet emitted by synthkit:
//   - pg_stat_activity_count (predecessor kpi-pg + pg panels): signals/langsmith.md has
//     pg_stat_database_numbackends instead; pg_stat_activity_count is not listed.
//     Panels wired with the predecessor's metric name; will be empty on the example stack until emitted.
//   - ClickHouseProfileEvents_SelectedRows / ClickHouseProfileEvents_InsertedRows
//     (predecessor ch-rows): signals/langsmith.md has ClickHouseProfileEvents_SelectQuery
//     (not SelectedRows) and no InsertedRows entry. Wired with predecessor names; will be
//     empty until the construct emits these ProfileEvent families.
//
// Tabs: Overview · Datastores · Traces
package acme_ai_eval

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// lsSel returns a substrate selector scoped to langsmith-* jobs filtered by the $job
// variable within the $env filter. extra is an already-formatted matcher list (no
// leading comma). Used for per-job panels that follow the $job variable.
func lsSel(extra string) string {
	base := `env=~"$env",job=~"$job"`
	if extra != "" {
		base += "," + extra
	}
	return "{" + base + "}"
}

// lsAllSel returns a substrate selector scoped to all langsmith-* jobs (ignores $job),
// used for fleet-wide KPI tiles and global aggregate panels.
func lsAllSel(extra string) string {
	base := `env=~"$env",job=~"langsmith-.*"`
	if extra != "" {
		base += "," + extra
	}
	return "{" + base + "}"
}

// LangSmithPlatform builds the AI Evaluation — LangSmith Platform dashboard.
// uid: acme-eval-langsmith-platform.
// Three tabs reproducing the predecessor's layout:
//
//   - Overview     — KPI tiles (HTTP rate, 5xx %, RSS, CH running queries, Redis clients,
//     pg active conns) + API services (HTTP by job, p95 latency) +
//     process health (CPU, RSS, FDs) + Python GC full-width.
//   - Datastores   — ClickHouse row (query rate, rows read/written KNOWN GAP, TCP conns)
//   - Redis / Postgres / nginx row.
//   - Traces       — platform-internal trace table (service.name=langsmith-*).
func LangSmithPlatform(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ai-eval-langsmith-platform", "Acme AI Eval — LangSmith Platform")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ────────────────────────────────────────────────────────────────────────────────

	// scenario: hidden const — acme-ai-eval blueprint; keeps selector helpers consistent.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme-ai-eval"))

	// env: manifest-seeded multi-select (all platform families are substrate-scoped by env).
	d.Builder.CustomVariable(dashboard.EnvVar(m))

	// job: label_values from a confirmed-emitted substrate metric — no blueprint filter; env-scoped.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("job", "Service (job)",
		`label_values(process_resident_memory_bytes{env=~"$env",job=~"langsmith-.*"}, job)`))

	// ── TAB 1: Overview — KPI tiles ──────────────────────────────────────────────────────────────

	// kpi-http: total HTTP request rate across all langsmith-* services.
	// http_requests_total — v:assumed; substrate, job-scoped (no blueprint).
	dashboard.AddPanel(&d, "kpi-http", dashboard.StatTile(
		"HTTP request rate", "reqps",
		dashboard.PromTarget(
			`sum(rate(http_requests_total`+lsAllSel("")+`[$__rate_interval]))`,
			"req/s")))

	// kpi-5xx: HTTP 5xx response rate as % of total — low-is-good: green → yellow → red.
	// Predecessor expr: 100 * 5xx / clamp_min(total, 0.001).
	dashboard.AddPanel(&d, "kpi-5xx", dashboard.StatTile(
		"HTTP 5xx rate", "percent",
		dashboard.PromTarget(
			`100 * sum(rate(http_requests_total`+lsAllSel(`status_code=~"5.."`)+`[$__rate_interval])) / clamp_min(sum(rate(http_requests_total`+lsAllSel("")+`[$__rate_interval])), 0.001)`,
			"5xx %"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "yellow"},
		dashboard.Threshold{Value: 5, Color: "red"}))

	// kpi-mem: total process RSS across all langsmith-* services.
	// process_resident_memory_bytes — v:ok.
	dashboard.AddPanel(&d, "kpi-mem", dashboard.StatTile(
		"Total process RSS", "bytes",
		dashboard.PromTarget(
			`sum(process_resident_memory_bytes`+lsAllSel("")+`)`,
			"RSS")))

	// kpi-ch-q: ClickHouseMetrics_Query — current executing queries (Gauge).
	// v:ok (signals/langsmith.md). No job= filter — CH has its own job label.
	dashboard.AddPanel(&d, "kpi-ch-q", dashboard.StatTile(
		"ClickHouse running queries", "short",
		dashboard.PromTarget(
			`sum(ClickHouseMetrics_Query`+acme.IntSel("")+`)`,
			"running queries")))

	// kpi-redis: redis_connected_clients — Gauge; v:ok.
	dashboard.AddPanel(&d, "kpi-redis", dashboard.StatTile(
		"Redis connected clients", "short",
		dashboard.PromTarget(
			`sum(redis_connected_clients`+acme.IntSel("")+`)`,
			"clients")))

	// kpi-pg: pg_stat_activity_count{state="active"} — KNOWN GAP.
	// signals/langsmith.md has pg_stat_database_numbackends (not pg_stat_activity_count).
	// Wired with the predecessor's metric name; panel will be empty on the example stack until the construct emits it.
	dashboard.AddPanel(&d, "kpi-pg", dashboard.StatTile(
		"Postgres active connections (KNOWN GAP — pg_stat_activity_count not yet emitted)", "short",
		dashboard.PromTarget(
			`sum(pg_stat_activity_count`+acme.IntSel(`state="active"`)+`)`,
			"active conns")))

	// ── TAB 1: Overview — API services (Python) section ──────────────────────────────────────────

	// http-svc: HTTP request rate by service (job), filtered by $job variable.
	dashboard.AddPanel(&d, "http-svc", dashboard.TimeseriesPanel(
		"HTTP rate by service", "reqps",
		dashboard.PromTarget(
			`sum by (job) (rate(http_requests_total`+lsSel("")+`[$__rate_interval]))`,
			"{{job}}")))

	// http-p95: HTTP p95 latency per service via classic histogram.
	// http_request_duration_seconds — v:assumed; substrate.
	dashboard.AddPanel(&d, "http-p95", dashboard.TimeseriesPanel(
		"HTTP p95 latency by service", "s",
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.95, "http_request_duration_seconds", lsSel(""), []string{"job"}),
			"{{job}}")))

	// ── TAB 1: Overview — Process health section ─────────────────────────────────────────────────

	// proc-cpu: process_cpu_seconds_total rate (CPU cores) — v:ok.
	dashboard.AddPanel(&d, "proc-cpu", dashboard.TimeseriesPanel(
		"Process CPU rate by service", "short",
		dashboard.PromTarget(
			`sum by (job) (rate(process_cpu_seconds_total`+lsSel("")+`[$__rate_interval]))`,
			"{{job}}")))

	// proc-mem: process_resident_memory_bytes per service — v:ok.
	dashboard.AddPanel(&d, "proc-mem", dashboard.TimeseriesPanel(
		"Process RSS by service", "bytes",
		dashboard.PromTarget(
			`sum by (job) (process_resident_memory_bytes`+lsSel("")+`)`,
			"{{job}}")))

	// proc-fds: process_open_fds per service — v:ok.
	dashboard.AddPanel(&d, "proc-fds", dashboard.TimeseriesPanel(
		"Open file descriptors by service", "short",
		dashboard.PromTarget(
			`sum by (job) (process_open_fds`+lsSel("")+`)`,
			"{{job}}")))

	// ── TAB 1: Overview — GC section ─────────────────────────────────────────────────────────────

	// py-gc: python_gc_collections_total rate by job — v:ok.
	// Predecessor scopes by job; we follow the same pattern for the $job variable.
	dashboard.AddPanel(&d, "py-gc", dashboard.TimeseriesPanel(
		"Python GC collections rate", "short",
		dashboard.PromTarget(
			`sum by (job) (rate(python_gc_collections_total`+lsSel("")+`[$__rate_interval]))`,
			"{{job}}")))

	// ── TAB 2: Datastores — ClickHouse section ───────────────────────────────────────────────────

	// ch-prof: ClickHouseProfileEvents_Query (Counter) rate — v:ok.
	dashboard.AddPanel(&d, "ch-prof", dashboard.TimeseriesPanel(
		"ClickHouse query rate", "short",
		dashboard.PromTarget(
			`sum(rate(ClickHouseProfileEvents_Query`+acme.IntSel("")+`[$__rate_interval]))`,
			"queries/s")))

	// ch-rows: ClickHouseProfileEvents_SelectedRows / InsertedRows — KNOWN GAP.
	// signals/langsmith.md has ClickHouseProfileEvents_SelectQuery (not SelectedRows) and
	// no InsertedRows entry. Wired with predecessor's exact names; panels will be empty until
	// the langsmith_platform construct emits these ProfileEvent families.
	dashboard.AddPanel(&d, "ch-rows", dashboard.TimeseriesPanel(
		"ClickHouse rows read/written (KNOWN GAP — SelectedRows/InsertedRows not yet emitted)", "short",
		dashboard.PromTarget(
			`sum(rate(ClickHouseProfileEvents_SelectedRows`+acme.IntSel("")+`[$__rate_interval]))`,
			"selected/s").RefId("A"),
		dashboard.PromTarget(
			`sum(rate(ClickHouseProfileEvents_InsertedRows`+acme.IntSel("")+`[$__rate_interval]))`,
			"inserted/s").RefId("B")))

	// ch-conn: ClickHouseMetrics_TCPConnection (Gauge) — v:ok.
	dashboard.AddPanel(&d, "ch-conn", dashboard.TimeseriesPanel(
		"ClickHouse TCP connections", "short",
		dashboard.PromTarget(
			`sum(ClickHouseMetrics_TCPConnection`+acme.IntSel("")+`)`,
			"connections")))

	// ── TAB 2: Datastores — Redis / Postgres / nginx section ────────────────────────────────────

	// redis: redis_commands_processed_total + redis_keyspace_hits_total — v:ok.
	dashboard.AddPanel(&d, "redis", dashboard.TimeseriesPanel(
		"Redis commands & keyspace hits", "short",
		dashboard.PromTarget(
			`sum(rate(redis_commands_processed_total`+acme.IntSel("")+`[$__rate_interval]))`,
			"commands/s").RefId("A"),
		dashboard.PromTarget(
			`sum(rate(redis_keyspace_hits_total`+acme.IntSel("")+`[$__rate_interval]))`,
			"keyspace hits/s").RefId("B")))

	// pg: pg_stat_activity_count by state — KNOWN GAP.
	// signals/langsmith.md does not include pg_stat_activity_count (has pg_stat_database_numbackends).
	// Wired faithfully from predecessor; panel will be empty on the example stack until emitted.
	dashboard.AddPanel(&d, "pg", dashboard.TimeseriesPanel(
		"Postgres connections by state (KNOWN GAP — pg_stat_activity_count not yet emitted)", "short",
		dashboard.PromTarget(
			`sum by (state) (pg_stat_activity_count`+acme.IntSel("")+`)`,
			"{{state}}")))

	// pg-xact: pg_stat_database_xact_commit rate by datname — v:ok.
	dashboard.AddPanel(&d, "pg-xact", dashboard.TimeseriesPanel(
		"Postgres commit rate", "short",
		dashboard.PromTarget(
			`sum by (datname) (rate(pg_stat_database_xact_commit`+acme.IntSel("")+`[$__rate_interval]))`,
			"{{datname}}")))

	// nginx: nginx_http_requests_total rate + nginx_connections_active gauge — v:ok.
	// Predecessor combines both series in one panel; we follow the same approach.
	dashboard.AddPanel(&d, "nginx", dashboard.TimeseriesPanel(
		"nginx request rate & active connections", "short",
		dashboard.PromTarget(
			`sum(rate(nginx_http_requests_total`+acme.IntSel("")+`[$__rate_interval]))`,
			"requests/s").RefId("A"),
		dashboard.PromTarget(
			`sum(nginx_connections_active`+acme.IntSel("")+`)`,
			"active conns").RefId("B")))

	// ── TAB 3: Traces ────────────────────────────────────────────────────────────────────────────

	// Platform-internal traces — langsmith-* services (NOT Acme AI-request-correlated).
	// These are platform-operational spans: ingest.process / api.handle / queue.work.
	// Predecessor uses service.name =~ "langsmith.*" (no dash) — reproduced exactly.
	dashboard.AddPanel(&d, "traces", dashboard.TraceTablePanel(
		`Platform-health traces (service.name=langsmith-*, env=ls_self_hosted — not request-correlated)`,
		acme.DSTempo,
		dashboard.TempoTableTarget(`{ resource.service.name =~ "langsmith.*" }`, 20)))

	// ── Layout ───────────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		// ── Tab 1: Overview ──────────────────────────────────────────────────────────────────────
		// KPI strip (6 tiles) → API services half/half → process health thirds → GC full-width.
		dashboard.Tabbed("Overview",
			dashboard.Section("Platform KPIs",
				dashboard.Tile("kpi-http"), dashboard.Tile("kpi-5xx"), dashboard.Tile("kpi-mem"),
				dashboard.Tile("kpi-ch-q"), dashboard.Tile("kpi-redis"), dashboard.Tile("kpi-pg")),
			dashboard.Section("API services (Python)",
				dashboard.Half("http-svc"), dashboard.Half("http-p95")),
			dashboard.Section("Process health",
				dashboard.Third("proc-cpu"), dashboard.Third("proc-mem"), dashboard.Third("proc-fds")),
			dashboard.Section("Python GC",
				dashboard.Full("py-gc")),
		),
		// ── Tab 2: Datastores ────────────────────────────────────────────────────────────────────
		// ClickHouse thirds row → Redis/Postgres/nginx quarters row.
		dashboard.Tabbed("Datastores",
			dashboard.Section("ClickHouse",
				dashboard.Third("ch-prof"), dashboard.Third("ch-rows"), dashboard.Third("ch-conn")),
			dashboard.Section("Redis / Postgres / nginx",
				dashboard.Stat("redis"), dashboard.Stat("pg"),
				dashboard.Stat("pg-xact"), dashboard.Stat("nginx")),
		),
		// ── Tab 3: Traces ────────────────────────────────────────────────────────────────────────
		dashboard.Tabbed("Traces",
			dashboard.Section("Platform-internal traces",
				dashboard.Tall("traces")),
		),
	)
	return d, nil
}
