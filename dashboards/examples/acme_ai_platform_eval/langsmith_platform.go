// SPDX-License-Identifier: AGPL-3.0-only

// LangSmithPlatform is the Acme AI-ws2 (acme-ai-platform-eval blueprint) dashboard for the
// self-hosted LangSmith platform infrastructure exporters: process/python runtime,
// ClickHouse, Redis, Postgres, nginx, and internal platform traces.
//
// Ported from predecessor acme-langsmith-platform-eval.json (2026-06-16).
//
// Metric scope: ALL families here are SUBSTRATE-SCOPED (no blueprint label). The
// predecessor's {scenario="acme_ai_platform_eval"} filter is DROPPED on every query; substrate
// disambiguation uses job=~"langsmith-.*" (set at scrape time; env-scoped via IntSel).
//
// KNOWN GAPS — wired faithfully; not yet emitted by synthkit:
//   - pg_stat_activity_count (predecessor: pg-conn panel): signals/ has pg_stat_database_numbackends
//     instead. Panel wired to pg_stat_activity_count as in the predecessor; will be empty on the example stack
//     until the construct emits it.
//   - pg_replication_lag (predecessor: pg-lag panel): no matching entry in signals/langsmith.md.
//     Wired faithfully; will be empty until added to the construct.
//   - ClickHouseProfileEvents_SelectedRows / ClickHouseProfileEvents_InsertedRows (predecessor:
//     ch-rows panel): signals/langsmith.md has ClickHouseProfileEvents_SelectQuery (not SelectedRows)
//     and no InsertedRows entry. Wired with the predecessor names; will be empty until emitted.
//
// Tabs: App services · ClickHouse · Redis & Postgres · nginx & Traces
package acme_ai_platform_eval

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// lsSel returns a substrate selector scoped to langsmith-* jobs within the $env filter.
// extra is an already-formatted matcher list (no leading comma).
func lsSel(extra string) string {
	base := `env=~"$env",job=~"langsmith-.*"`
	if extra != "" {
		base += "," + extra
	}
	return "{" + base + "}"
}

// LangSmithPlatform builds the Acme AI-ws2 — LangSmith Platform dashboard.
// uid: acme-ws2-langsmith-platform.
// Four tabs reproducing the predecessor's layout:
//
//   - App services  — KPI health tiles + HTTP + process/python runtime panels
//   - ClickHouse    — query rate, rows inserted/selected (KNOWN GAP), TCP connections
//   - Redis & Postgres — connected clients, memory, commands/hits; pg connections (KNOWN GAP),
//     commit rate, replication lag (KNOWN GAP)
//   - nginx & Traces — request rate, active connections, internal platform trace table
func LangSmithPlatform(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws2-langsmith-platform", "Acme AI-ws2 — LangSmith Platform")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ────────────────────────────────────────────────────────────────────────────────

	// scenario const var (hidden) — WS2 blueprint name; keeps app-selector helpers consistent.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform_eval"))

	// env: manifest-seeded multi-select (all platform families are substrate-scoped by env).
	d.Builder.CustomVariable(dashboard.EnvVar(m))

	// job: label_values from a confirmed-emitted substrate metric — no blueprint filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("job", "Component",
		`label_values(process_resident_memory_bytes{job=~"langsmith-.*"}, job)`))

	// ── TAB 1: App services ──────────────────────────────────────────────────────────────────────

	// KPI tiles: components up, HTTP rate, 5xx rate, p95 latency.
	// up{job=~"langsmith-.*"} — substrate, IntSel adds env scope.
	dashboard.AddPanel(&d, "k-up", dashboard.StatTile(
		"Components UP", "short",
		dashboard.PromTarget(
			`sum(min by (job) (up`+acme.IntSel(`job=~"langsmith-.*"`)+`))`,
			"components up")))

	// http_requests_total — v:assumed; substrate (job-scoped, no blueprint).
	dashboard.AddPanel(&d, "k-http", dashboard.StatTile(
		"HTTP request rate", "reqps",
		dashboard.PromTarget(
			`sum(rate(http_requests_total`+lsSel("")+`[$__rate_interval]))`,
			"req/s")))

	// HTTP 5xx rate — low-is-good: green → yellow → red.
	dashboard.AddPanel(&d, "k-5xx", dashboard.StatTile(
		"HTTP 5xx rate", "reqps",
		dashboard.PromTarget(
			`sum(rate(http_requests_total`+lsSel(`status_code="500"`)+`[$__rate_interval]))`,
			"5xx/s"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 0.05, Color: "yellow"},
		dashboard.Threshold{Value: 0.5, Color: "red"}))

	// HTTP p95 latency via classic histogram — low-is-good: green → yellow → red.
	// http_request_duration_seconds — v:assumed; substrate.
	dashboard.AddPanel(&d, "k-httplat", dashboard.StatTile(
		"HTTP p95 latency", "s",
		dashboard.PromTarget(
			dashboard.ClassicHistogramQuantile(0.95, "http_request_duration_seconds", lsSel(""), nil),
			"p95 latency"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "yellow"},
		dashboard.Threshold{Value: 3, Color: "red"}))

	// HTTP section: by status_code + by handler.
	dashboard.AddPanel(&d, "p-http-status", dashboard.TimeseriesPanel(
		"HTTP requests by status code", "reqps",
		dashboard.PromTarget(
			`sum by (status_code) (rate(http_requests_total`+lsSel("")+`[$__rate_interval]))`,
			"{{status_code}}")))

	dashboard.AddPanel(&d, "p-http-handler", dashboard.TimeseriesPanel(
		"HTTP requests by handler (top 8)", "reqps",
		dashboard.PromTarget(
			`topk(8, sum by (handler) (rate(http_requests_total`+lsSel("")+`[$__rate_interval])))`,
			"{{handler}}")))

	// Process & runtime section.
	// process_resident_memory_bytes — v:ok.
	dashboard.AddPanel(&d, "p-rss", dashboard.TimeseriesPanel(
		"Process RSS by component", "bytes",
		dashboard.PromTarget(
			`max by (job) (process_resident_memory_bytes`+lsSel("")+`)`,
			"{{job}}")))

	// process_cpu_seconds_total — v:ok.
	dashboard.AddPanel(&d, "p-cpu", dashboard.TimeseriesPanel(
		"Process CPU by component", "short",
		dashboard.PromTarget(
			`sum by (job) (rate(process_cpu_seconds_total`+lsSel("")+`[$__rate_interval]))`,
			"{{job}}")))

	// process_open_fds — v:ok.
	dashboard.AddPanel(&d, "p-fds", dashboard.TimeseriesPanel(
		"Open file descriptors", "short",
		dashboard.PromTarget(
			`max by (job) (process_open_fds`+lsSel("")+`)`,
			"{{job}}")))

	// python_gc_collections_total — v:ok.
	dashboard.AddPanel(&d, "p-gc", dashboard.TimeseriesPanel(
		"Python GC collections rate", "short",
		dashboard.PromTarget(
			`sum by (generation) (rate(python_gc_collections_total`+acme.IntSel("")+`[$__rate_interval]))`,
			"gen {{generation}}")))

	// ── TAB 2: ClickHouse ────────────────────────────────────────────────────────────────────────

	// ClickHouseProfileEvents_Query — v:ok (signals has ClickHouseProfileEvents_Query counter).
	dashboard.AddPanel(&d, "ch-query", dashboard.TimeseriesPanel(
		"ClickHouse query rate", "short",
		dashboard.PromTarget(
			`sum(rate(ClickHouseProfileEvents_Query`+acme.IntSel("")+`[$__rate_interval]))`,
			"queries/s")))

	// ClickHouseProfileEvents_SelectedRows / InsertedRows — KNOWN GAP:
	// signals/langsmith.md has ClickHouseProfileEvents_SelectQuery (not SelectedRows) and no
	// InsertedRows entry. Wired with the predecessor's exact names; panels will be empty until
	// the langsmith_platform construct emits these ProfileEvents families.
	dashboard.AddPanel(&d, "ch-rows", dashboard.TimeseriesPanel(
		"ClickHouse rows selected vs inserted (KNOWN GAP — not yet emitted)", "short",
		dashboard.PromTarget(
			`sum(rate(ClickHouseProfileEvents_SelectedRows`+acme.IntSel("")+`[$__rate_interval]))`,
			"selected/s").RefId("A"),
		dashboard.PromTarget(
			`sum(rate(ClickHouseProfileEvents_InsertedRows`+acme.IntSel("")+`[$__rate_interval]))`,
			"inserted/s").RefId("B")))

	// ClickHouseMetrics_TCPConnection — v:ok (gauge).
	dashboard.AddPanel(&d, "ch-conn", dashboard.TimeseriesPanel(
		"ClickHouse TCP connections", "short",
		dashboard.PromTarget(
			`max(ClickHouseMetrics_TCPConnection`+acme.IntSel("")+`)`,
			"tcp conns")))

	// ── TAB 3: Redis & Postgres ──────────────────────────────────────────────────────────────────

	// redis_connected_clients — v:ok (gauge).
	dashboard.AddPanel(&d, "r-clients", dashboard.TimeseriesPanel(
		"Redis connected clients", "short",
		dashboard.PromTarget(
			`max(redis_connected_clients`+acme.IntSel("")+`)`,
			"clients")))

	// redis_memory_used_bytes — v:ok (gauge).
	dashboard.AddPanel(&d, "r-mem", dashboard.TimeseriesPanel(
		"Redis memory used", "bytes",
		dashboard.PromTarget(
			`max(redis_memory_used_bytes`+acme.IntSel("")+`)`,
			"mem")))

	// redis_commands_processed_total + redis_keyspace_hits_total — v:ok.
	dashboard.AddPanel(&d, "r-ops", dashboard.TimeseriesPanel(
		"Redis commands & keyspace hits", "short",
		dashboard.PromTarget(
			`sum(rate(redis_commands_processed_total`+acme.IntSel("")+`[$__rate_interval]))`,
			"commands/s").RefId("A"),
		dashboard.PromTarget(
			`sum(rate(redis_keyspace_hits_total`+acme.IntSel("")+`[$__rate_interval]))`,
			"keyspace hits/s").RefId("B")))

	// pg_stat_activity_count — KNOWN GAP: signals/ has pg_stat_database_numbackends instead.
	// Wired with predecessor's pg_stat_activity_count (by state); panel will be empty on the example stack
	// until the construct emits this metric family.
	dashboard.AddPanel(&d, "pg-conn", dashboard.TimeseriesPanel(
		"Postgres connections by state (KNOWN GAP — pg_stat_activity_count not yet emitted)", "short",
		dashboard.PromTarget(
			`max by (state) (pg_stat_activity_count`+acme.IntSel("")+`)`,
			"{{state}}")))

	// pg_stat_database_xact_commit — v:ok.
	dashboard.AddPanel(&d, "pg-xact", dashboard.TimeseriesPanel(
		"Postgres commit rate", "short",
		dashboard.PromTarget(
			`sum(rate(pg_stat_database_xact_commit`+acme.IntSel("")+`[$__rate_interval]))`,
			"commits/s")))

	// pg_replication_lag — KNOWN GAP: not in signals/langsmith.md.
	// Wired faithfully; panel will be empty until the construct emits this metric.
	dashboard.AddPanel(&d, "pg-lag", dashboard.TimeseriesPanel(
		"Postgres replication lag (KNOWN GAP — pg_replication_lag not yet emitted)", "s",
		dashboard.PromTarget(
			`max(pg_replication_lag`+acme.IntSel("")+`)`,
			"lag")))

	// ── TAB 4: nginx & Traces ────────────────────────────────────────────────────────────────────

	// nginx_http_requests_total — v:ok.
	dashboard.AddPanel(&d, "n-req", dashboard.TimeseriesPanel(
		"nginx request rate", "reqps",
		dashboard.PromTarget(
			`sum(rate(nginx_http_requests_total`+acme.IntSel("")+`[$__rate_interval]))`,
			"req/s")))

	// nginx_connections_active — v:ok (gauge).
	dashboard.AddPanel(&d, "n-conn", dashboard.TimeseriesPanel(
		"nginx active connections", "short",
		dashboard.PromTarget(
			`max(nginx_connections_active`+acme.IntSel("")+`)`,
			"active")))

	// Internal platform traces — langsmith-* services (NOT Acme AI-request-correlated).
	// These are platform-internal spans: ingest.process / api.handle / queue.work.
	dashboard.AddPanel(&d, "tr-health", dashboard.TraceTablePanel(
		"Platform-health traces (internal LangSmith ops — not Acme AI-request-correlated)",
		acme.DSTempo,
		dashboard.TempoTableTarget(`{ resource.service.name =~ "langsmith-.*" }`, 30)))

	// ── Layout ───────────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		// ── Tab 1: App services ──────────────────────────────────────────────────────────────────
		// KPI strip → HTTP section → process/runtime section → GC full-width.
		dashboard.Tabbed("App services",
			dashboard.Section("Platform KPIs (scrape unlock)",
				dashboard.Tile("k-up"), dashboard.Tile("k-http"),
				dashboard.Tile("k-5xx"), dashboard.Tile("k-httplat")),
			dashboard.Section("HTTP",
				dashboard.Half("p-http-status"), dashboard.Half("p-http-handler")),
			dashboard.Section("Process & runtime",
				dashboard.Third("p-rss"), dashboard.Third("p-cpu"), dashboard.Third("p-fds")),
			dashboard.Section("Python GC",
				dashboard.Full("p-gc")),
		),
		// ── Tab 2: ClickHouse ────────────────────────────────────────────────────────────────────
		dashboard.Tabbed("ClickHouse",
			dashboard.Section("ClickHouse",
				dashboard.Third("ch-query"), dashboard.Third("ch-rows"), dashboard.Third("ch-conn")),
		),
		// ── Tab 3: Redis & Postgres ──────────────────────────────────────────────────────────────
		// Redis row then Postgres row.
		dashboard.Tabbed("Redis & Postgres",
			dashboard.Section("Redis",
				dashboard.Third("r-clients"), dashboard.Third("r-mem"), dashboard.Third("r-ops")),
			dashboard.Section("Postgres",
				dashboard.Third("pg-conn"), dashboard.Third("pg-xact"), dashboard.Third("pg-lag")),
		),
		// ── Tab 4: nginx & Traces ────────────────────────────────────────────────────────────────
		// nginx half-width pair then full-width trace table.
		dashboard.Tabbed("nginx & Traces",
			dashboard.Section("nginx",
				dashboard.Half("n-req"), dashboard.Half("n-conn")),
			dashboard.Section("Platform traces",
				dashboard.Tall("tr-health")),
		),
	)
	return d, nil
}
