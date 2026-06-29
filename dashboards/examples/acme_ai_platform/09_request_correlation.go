// SPDX-License-Identifier: AGPL-3.0-only

// RequestCorrelation is the Acme AI Request Correlation dashboard (acme_ai_platform blueprint,
// predecessor 09-golden-thread). It follows ONE request across every telemetry tier — metrics,
// distributed traces, structured logs, and eval evidence — joined on correlation_id +
// portkey_trace_id + trace_id. Ported from predecessor 09-golden-thread.json (2026-06-16).
//
// The predecessor dashboard is a single flat scroll organised by section-header text panels.
// We reproduce the same content grouped into six tabs that match the section boundaries:
//
//   - Overview          — intro text + entry-point tables (latest correlated request)
//   - End-to-End Path   — 10-hop journey table + Trace tier (TraceQL) + Logs tier
//   - Eval Tier         — LangSmith eval-runs Infinity table + correlation join logs
//   - Topology          — service-graph edge table + request-rate timeseries
//   - Metrics           — Portkey latency, gen_ai token spend, content integrity, pipeline stats
//   - RUM → Trace → DB  — browser RUM beacon → assist trace → Knowledge-Layer/DB spans + DB health
//
// Scope notes (load-bearing):
//   - traces_spanmetrics_* and traces_service_graph_* are metrics-generator-derived; they carry
//     NO blueprint label → hand-written selectors (no AppSel / IntSel).
//   - portkey_api_* (portkey_api_latency_seconds, portkey_api_requests_total) is substrate-scoped
//     → IntSel (env=~"$env"); the predecessor used scenario= which does not exist on the synthkit path.
//   - gen_ai_client_token_usage_* IS app-scoped → acme.AppSel().
//   - otelcol_exporter_sent_spans_total is substrate → bare selector.
//   - acme_content_leak_test / acme_content_dropped_total → synthkit de-Roch names via
//     acme.MetricContentLeakTest / acme.MetricContentDropped. App-scoped → AppSel.
//   - Loki streams: use blueprint="acme-ai-platform" (synthkit label) where predecessor used scenario=.
//     The correlation_id structured-metadata filter uses blueprint= stream selector for app streams;
//     portkey export-log (source="portkey") and langsmith-runindex streams use their own labels
//     (service_name=) and are substrate-scoped (no blueprint label).
//
// Variables:
//   - correlation_id: the predecessor uses a Tempo tag-values QueryVariable (span.app.correlation_id).
//     The builder DSL has no Tempo-backed QueryVariable builder, so we fall back to TextVar.
//     This is a known builder gap — noted in the report.
//
// Infinity panels:
//   - /acme/golden_thread root_selector=request       (panel-2: request context)
//   - /acme/golden_thread root_selector=correlation_keys (panel-3: key-set — empty cols in predecessor)
//   - /acme/golden_thread root_selector=hops          (panel-4: 10-hop journey)
//   - /api/v1/runs/query                               (panel-7: eval runs — POST with JSON select-list body)
//
// "golden thread" / "Golden Thread" strings are BANNED. All references renamed to
// "Request Correlation" or "End-to-End Trace".
package acme_ai_platform

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// RequestCorrelation builds the Acme AI Request Correlation dashboard for the acme_ai_platform blueprint.
func RequestCorrelation(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard(
		"acme-ws1-request-correlation",
		"Acme AI — Request Correlation")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ────────────────────────────────────────────────────────────────────────────────

	// scenario: hidden const var → app selectors resolve via acme.AppSel($scenario).
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform"))

	// env: multi-select filter seeded from manifest envs — AppSel emits
	// {deployment_environment=~"$env",...}; without this variable AppSel-based panels (gen_ai
	// token usage, content-leak, content-dropped) resolve "$env" literally and return no data.
	// Matches the pattern in 00_observability.go (dashboard.EnvVar(m)).
	d.Builder.CustomVariable(dashboard.EnvVar(m))

	// Infinity tables use RELATIVE paths — the Infinity datasource's base URL (the host FQDN,
	// served via tailscale serve) supplies the host. Do NOT embed an absolute base here.

	// correlation_id: predecessor uses a Tempo tag-values QueryVariable (span.app.correlation_id).
	// The builder DSL has no Tempo-backed QueryVariable builder → fall back to TextVar.
	// GAP: Tempo tag-values variable is not supported; a TextVar allows manual paste/filter.
	d.Builder.TextVariable(dashboard.TextVar("correlation_id", "Correlation ID", ""))

	// trace_id: optional W3C trace-id paste box.
	d.Builder.TextVariable(dashboard.TextVar("trace_id", "Trace ID (optional)", ""))

	// portkey_trace_id: optional portkey trace-id pivot box.
	d.Builder.TextVariable(dashboard.TextVar("portkey_trace_id", "Portkey Trace ID (optional)", ""))

	// db_instance: scopes the side-by-side dbo11y/RDS DB-health panels in the RUM → Trace → DB tab.
	// There is NO trace_id on dbo11y/RDS — join is by instance name + time window only.
	d.Builder.TextVariable(dashboard.TextVar("db_instance", "DB instance (acme-pg-<env>)", "acme-pg-prd"))

	// ── TAB: Overview ────────────────────────────────────────────────────────────────────────────

	// Intro text panel (predecessor panel-1, de-Rochified).
	// Backtick characters in markdown are spelled out via string concatenation to avoid raw-literal nesting.
	bt := "`"
	dashboard.AddPanel(&d, "ov-intro",
		dashboard.TextPanel("Request Correlation — One Request, Every Tier",
			"## Request Correlation — One Request, Every Tier\n\n"+
				"This dashboard follows a **single Acme AI request** end-to-end using the **universal "+bt+"correlation_id"+bt+"** "+
				"that is stamped on every signal: Portkey API-pull metrics · OpenTelemetry distributed traces · Loki structured logs · LangSmith eval runs.\n\n"+
				"**How to use:** paste a "+bt+"correlation_id"+bt+" into the **Correlation ID** text box above — the trace and log panels filter to that single request. "+
				"Leave it empty to browse recent correlated activity. You can copy an id straight from the Entry Point / Eval tables below.\n\n"+
				"> **acme_ai_platform join chain (API-only):** the Portkey gateway span is **suppressed** in the "+bt+"acme_ai_platform"+bt+" scenario (API-only architecture — no real-time gateway stream). "+
				"The correlation join hops are: **Tempo trace** (backend/langgraph spans) → **(e) 2b-export Loki line** "+bt+`{service_name="portkey-gateway"}`+bt+" (body-excluded export, ~15 min cadence) → "+
				"**(f-ii) run-index Loki line** "+bt+`{service_name="langsmith-runindex"}`+bt+" (content-free run index, ~5 min cadence) → **Infinity eval row**. "+
				"Both export and run-index streams carry "+bt+"portkey_trace_id"+bt+" as structured metadata — the stitch key. "+
				bt+"correlation_id"+bt+" travels end-to-end via span attribute, structured metadata, and LangSmith run tag.",
		))

	// Entry Point section header (predecessor panel-100).
	dashboard.AddPanel(&d, "ov-entry-hdr",
		dashboard.TextPanel("", "## Entry Point — Latest Correlated Request"))

	// Latest correlated request context — /acme/golden_thread root=request (predecessor panel-2).
	dashboard.AddPanel(&d, "ov-request",
		dashboard.TablePanel(
			"Latest correlated request — context + universal key-set",
			dashboard.InfinityTarget("A", "/acme/golden_thread",
				"synthkit (Infinity)", "request",
				dashboard.Col("use_case", "Use Case", "string"),
				dashboard.Col("context", "Context", "string"),
				dashboard.Col("env", "Env", "string"),
				dashboard.Col("model", "Model", "string"),
				dashboard.Col("provider", "Provider", "string"),
				dashboard.Col("started_at", "Started At", "string"),
			)))

	// Universal key-set — /acme/golden_thread root=correlation_keys (predecessor panel-3).
	// The predecessor passes columns:[] (auto-infer from JSON). InfinityTarget requires explicit columns;
	// we list the known key fields from the predecessor description. GAP: auto-infer not supported.
	dashboard.AddPanel(&d, "ov-corrkeys",
		dashboard.TablePanel(
			"Universal key-set — Correlation IDs (copy any id to filter panels below)",
			dashboard.InfinityTarget("A", "/acme/golden_thread",
				"synthkit (Infinity)", "correlation_keys",
				dashboard.Col("correlation_id", "correlation_id", "string"),
				dashboard.Col("trace_id", "trace_id", "string"),
				dashboard.Col("portkey_trace_id", "portkey_trace_id", "string"),
				dashboard.Col("span_id", "span_id", "string"),
			)))

	// ── TAB: End-to-End Path ──────────────────────────────────────────────────────────────────────

	// Section header: 10-hop journey (predecessor panel-101).
	dashboard.AddPanel(&d, "path-hops-hdr",
		dashboard.TextPanel("", "## The End-to-End Path — browser → backend → workflow → agent → gateway → Bedrock → logs → eval"))

	// 10-hop journey table — /acme/golden_thread root=hops (predecessor panel-4).
	dashboard.AddPanel(&d, "path-hops",
		dashboard.TablePanel(
			"End-to-end path — service hop table for the latest sampled request",
			dashboard.InfinityTarget("A", "/acme/golden_thread",
				"synthkit (Infinity)", "hops",
				dashboard.Col("hop", "Hop #", "number"),
				dashboard.Col("service", "Service", "string"),
				dashboard.Col("span_name", "Span / Event", "string"),
				dashboard.Col("signal", "Signal", "string"),
				dashboard.Col("span_id", "Span ID", "string"),
				dashboard.Col("parent_span_id", "Parent Span", "string"),
				dashboard.Col("keys", "Correlation Keys", "string"),
			)))

	// Section header: Trace tier (predecessor panel-102).
	dashboard.AddPanel(&d, "path-trace-hdr",
		dashboard.TextPanel("", "## Trace Tier — Distributed Trace filtered by correlation_id"))

	// Correlated traces — Tempo TraceQL (predecessor panel-5).
	// Scope: Tempo search, no blueprint label needed — TraceQL attribute filter is sufficient.
	// span.app.correlation_id is the OTEL semconv aligned attr (was span.app.correlation_id).
	dashboard.AddPanel(&d, "path-traces",
		dashboard.TraceTablePanel(
			"Traces matching correlation_id (TraceQL — click traceID to open waterfall)",
			acme.DSTempo,
			dashboard.TempoTableTarget(
				`{ span.app.correlation_id =~ "$correlation_id" }`, 20)))

	// Section header: Logs tier (predecessor panel-103).
	dashboard.AddPanel(&d, "path-logs-hdr",
		dashboard.TextPanel("", "## Logs Tier — Log lines from every source, filtered by correlation_id\n\n"+
			"> **acme_ai_platform:** the real-time Portkey gateway stream ("+bt+`service_name="portkey"`+bt+") is **suppressed** in this scenario — it emits no log lines here. "+
			"The gateway signal is accessible via the **(e) 2b-export stream** and **(f-ii) run-index stream** panels in the Correlation Join section."))

	// All correlated logs — non-gateway sources (predecessor panel-6).
	// Predecessor: {scenario="acme_ai_platform"} | correlation_id=~"$correlation_id"
	// Scope fix: synthkit emits blueprint= (not scenario=) on app streams.
	// correlation_id is Loki structured metadata → filter after stream selector.
	dashboard.AddPanel(&d, "path-logs",
		dashboard.LogsPanel(
			`All logs for this request — non-gateway sources (bedrock_invocation / app / mrhub_etl)`,
			dashboard.LokiTarget(
				`{blueprint="acme-ai-platform"} | correlation_id=~"$correlation_id"`,
				"")))

	// ── TAB: Eval Tier ────────────────────────────────────────────────────────────────────────────

	// Section header: Eval tier (predecessor panel-104).
	dashboard.AddPanel(&d, "eval-hdr",
		dashboard.TextPanel("", "## Eval Tier — LangSmith evaluation evidence (Infinity eval_runs — with trace_id → Tempo + correlation_id → Loki links)"))

	// LangSmith eval runs — /api/v1/runs/query (predecessor panel-7).
	// Predecessor issues POST with a JSON select-list body; switched from GET to POST for fidelity.
	// If the host does not yet handle POST on this route, that is a host-side fill, not our concern.
	dashboard.AddPanel(&d, "eval-runs",
		dashboard.TablePanel(
			"LangSmith eval runs — quality evidence for this request (click otel_trace_id → Tempo, correlation_id → Loki)",
			dashboard.InfinityTargetPOST("A", "/api/v1/runs/query",
				"synthkit (Infinity)", "runs",
				`{"select":["id","name","run_type","status","start_time","total_tokens","total_cost","extra","feedback_stats"]}`,
				dashboard.Col("id", "id", "string"),
				dashboard.Col("name", "name", "string"),
				dashboard.Col("run_type", "run_type", "string"),
				dashboard.Col("status", "status", "string"),
				dashboard.Col("start_time", "start_time", "string"),
				dashboard.Col("total_tokens", "total_tokens", "number").WithUnit("short"),
				dashboard.Col("total_cost", "total_cost", "string"),
				dashboard.Col("extra.metadata.otel_trace_id", "otel_trace_id", "string"),
				dashboard.Col("extra.metadata.correlation_id", "correlation_id", "string"),
				dashboard.Col("extra.metadata.portkey_trace_id", "portkey_trace_id", "string"),
				dashboard.Col("extra.metadata.use_case", "use_case", "string"),
				dashboard.Col("feedback_stats.faithfulness.avg", "Faithfulness", "number").WithUnit("percentunit").WithDecimals(3),
				dashboard.Col("feedback_stats.completeness.avg", "Completeness", "number").WithUnit("percentunit").WithDecimals(3),
			)))

	// Section header: Correlation join — acme_ai_platform API-only path (predecessor panel-108).
	dashboard.AddPanel(&d, "eval-join-hdr",
		dashboard.TextPanel("Correlation Join — acme_ai_platform API-only path",
			`## Correlation Join — acme_ai_platform API-only path: Trace → (e) Export Log → (f-ii) Run-Index → Eval

Under `+"`"+`acme_ai_platform`+"`"+` the gateway span is absent. The join chain is: **Tempo trace** (backend/langgraph spans) → **(e) 2b-export log** `+"`"+`{service_name="portkey-gateway"}`+"`"+` (~15 min batch; `+"`"+`portkey_trace_id`+"`"+` structured metadata) → **(f-ii) run-index log** `+"`"+`{service_name="langsmith-runindex"}`+"`"+` (~5 min poll; same `+"`"+`portkey_trace_id`+"`"+`) → **Infinity eval row**. Filter both panels by `+"`"+`portkey_trace_id`+"`"+` — copy the value from the Universal Key-Set table above or from the eval table below.`))

	// (e) Portkey 2b-export log — body-excluded gateway records (predecessor panel-16).
	// Substrate stream (source="portkey", service_name="portkey-gateway") — no blueprint label.
	// Predecessor: {source="portkey", service_name="portkey-gateway", scenario="acme_ai_platform"} | portkey_trace_id=~"${portkey_trace_id:raw}"
	// Scope fix: drop scenario= (not a synthkit label on this stream); keep source+service_name.
	dashboard.AddPanel(&d, "eval-export-log",
		dashboard.LogsPanel(
			"(e) Portkey 2b-export log — body-excluded gateway records (service_name=portkey-gateway)",
			dashboard.LokiTarget(
				`{source="portkey",service_name="portkey-gateway"} | portkey_trace_id=~"${portkey_trace_id:raw}"`,
				"")))

	// (f-ii) LangSmith run-index log — content-free run metadata (predecessor panel-17).
	// Substrate stream (source="langsmith-runs", service_name="langsmith-runindex") — no blueprint label.
	dashboard.AddPanel(&d, "eval-runindex-log",
		dashboard.LogsPanel(
			"(f-ii) LangSmith run-index log — content-free run metadata (service_name=langsmith-runindex)",
			dashboard.LokiTarget(
				`{source="langsmith-runs",service_name="langsmith-runindex"} | portkey_trace_id=~"${portkey_trace_id:raw}"`,
				"")))

	// ── TAB: Topology ─────────────────────────────────────────────────────────────────────────────

	// Section header: Topology (predecessor panel-105).
	dashboard.AddPanel(&d, "topo-hdr",
		dashboard.TextPanel("", "## Topology — Service-graph (which services this request traverses)"))

	// Service-graph edges table (predecessor panel-8).
	// Substrate family — no blueprint label; predecessor used scenario= which we drop.
	dashboard.AddPanel(&d, "topo-svcgraph",
		dashboard.TablePanel(
			"Service-graph edges (traces_service_graph_request_total — client→server request rates)",
			dashboard.PromTarget(
				`sum by (client, server)(rate(traces_service_graph_request_total{}[$__rate_interval]))`,
				"")))

	// Request rate by service (predecessor panel-9).
	// Substrate family — no blueprint label.
	dashboard.AddPanel(&d, "topo-rate-svc",
		dashboard.TimeseriesPanel(
			"Request rate by service (spanmetrics — platform real-time pulse)", "reqps",
			dashboard.PromTarget(
				`sum by (service)(rate(traces_spanmetrics_calls_total{}[$__rate_interval]))`,
				"{{service}}")))

	// ── TAB: Metrics ──────────────────────────────────────────────────────────────────────────────

	// Section header: Metrics tier (predecessor panel-106).
	dashboard.AddPanel(&d, "met-hdr",
		dashboard.TextPanel("", "## Metrics Tier — Gateway latency + token spend anchored to this request's model/use-case"))

	// Portkey latency quantiles (predecessor panel-10).
	// portkey_api_latency_seconds is substrate/integration-scoped (no blueprint label).
	// Predecessor used scenario="acme_ai_platform" — drop (not a synthkit label on this family).
	// Pre-computed quantile gauges from the Analytics API: no histogram_quantile needed.
	dashboard.AddPanel(&d, "met-portkey-lat",
		dashboard.TimeseriesPanel(
			"Portkey gateway latency — API-pull pre-computed quantiles", "s",
			dashboard.PromTarget(
				`max by (quantile)(portkey_api_latency_seconds{quantile="0.5"})`,
				"p50").RefId("A"),
			dashboard.PromTarget(
				`max by (quantile)(portkey_api_latency_seconds{quantile="0.9"})`,
				"p90").RefId("B"),
			dashboard.PromTarget(
				`max by (quantile)(portkey_api_latency_seconds{quantile="0.99"})`,
				"p99").RefId("C")))

	// gen_ai token usage (predecessor panel-11).
	// gen_ai_client_token_usage_* is APP-scoped → AppSel.
	dashboard.AddPanel(&d, "met-genai-tokens",
		dashboard.TimeseriesPanel(
			"gen_ai token usage — input vs output", "short",
			dashboard.PromTarget(
				`sum by (gen_ai_token_type)(rate(gen_ai_client_token_usage_sum`+
					acme.AppSel("")+`[$__rate_interval]))`,
				"{{gen_ai_token_type}}")))

	// Section header: Content integrity (predecessor panel-107).
	dashboard.AddPanel(&d, "met-content-hdr",
		dashboard.TextPanel("", "## Content Integrity — confirming no prompt/response content crosses to Grafana"))

	// Content leak test (predecessor panel-12).
	// acme_content_leak_test → acme.MetricContentLeakTest (de-Roch). App-scoped → AppSel.
	// real Acme AI stack: acme_content_leak_test
	dashboard.AddPanel(&d, "met-leak-test",
		dashboard.StatTile(
			"Content leak test (must be 0 → CLEAN)", "short",
			dashboard.PromTarget(
				`max(`+acme.MetricContentLeakTest+acme.AppSel("")+`)`,
				"leak test"),
			dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "red"}))

	// Content dropped counter (predecessor panel-13).
	// acme_content_dropped_total → acme.MetricContentDropped. App-scoped → AppSel.
	// real Acme AI stack: acme_content_dropped_total
	dashboard.AddPanel(&d, "met-dropped",
		dashboard.StatTile(
			"Content dropped (strip events — rising = healthy)", "short",
			dashboard.PromTarget(
				`sum(`+acme.MetricContentDropped+acme.AppSel("")+`)`,
				"dropped")))

	// OTLP spans exported (predecessor panel-14).
	// otelcol_exporter_sent_spans_total is substrate (Alloy/OTel collector) — no blueprint label.
	dashboard.AddPanel(&d, "met-spans-exported",
		dashboard.StatTile(
			"OTLP spans exported (pipeline healthy)", "short",
			dashboard.PromTarget(
				`sum(otelcol_exporter_sent_spans_total{})`,
				"spans exported"),
			dashboard.Threshold{Value: 0, Color: "yellow"}, dashboard.Threshold{Value: 1, Color: "green"}))

	// Portkey API requests total (predecessor panel-15).
	// portkey_api_requests_total is substrate/integration-scoped — no blueprint label.
	dashboard.AddPanel(&d, "met-portkey-reqs",
		dashboard.StatTile(
			"Portkey API requests", "short",
			dashboard.PromTarget(
				`sum(portkey_api_requests_total{})`,
				"requests")))

	// ── TAB: RUM → Trace → DB ────────────────────────────────────────────────────────────────────

	// Explainer header: walk from RUM beacon → assist trace → Knowledge-Layer spans → datastores.
	// DB-tier (dbo11y/RDS) has no trace_id — correlation is by instance name + time window.
	dashboard.AddPanel(&d, "rdb-hdr",
		dashboard.TextPanel("",
			"## RUM → Trace → Knowledge Layer → Database\n\n"+
				"Follow one assist request from the browser RUM beacon, through the backend trace and its Knowledge-Layer-API fan-out "+
				"("+bt+"call rds-acl"+bt+" / "+bt+"call documentdb-store"+bt+" / "+bt+"call opensearch-vectors"+bt+"), "+
				"to the datastores. "+
				"**DB-tier telemetry (dbo11y / RDS) has NO "+bt+"trace_id"+bt+"** — correlate it by "+
				"**instance name + time window** using the **DB instance** variable, not a trace join."))

	// RUM assist beacons — Faro fetch-trace events filtered by trace_id.
	// RUM streams are collector-owned (no blueprint label); scope by service_name + deployment_environment.
	// Empty $trace_id ⇒ regex matches all — allows browsing recent beacons without a specific trace.
	dashboard.AddPanel(&d, "rdb-rum",
		dashboard.LogsPanel(
			"RUM assist beacons (filter by Trace ID / correlation)",
			dashboard.LokiTarget(
				`{service_name="acme-frontend",kind="event",deployment_environment=~"$env"} | logfmt | event_name="faro.tracing.fetch" | traceID=~".*$trace_id.*"`,
				"")))

	// Assist traces that hit the Knowledge Layer — these carry the DB fan-out spans (call rds-acl /
	// call documentdb-store / call opensearch-vectors). Browsable by default (recent knowledge-service traces);
	// click a traceID to open the waterfall and see the datastore hops. For a single-request filter,
	// paste a correlation_id on the End-to-End Path tab's trace panel.
	dashboard.AddPanel(&d, "rdb-trace",
		dashboard.TraceTablePanel(
			"Knowledge-Layer traces with DB fan-out (TraceQL — click traceID for the waterfall)",
			acme.DSTempo,
			dashboard.TempoTableTarget(
				`{ resource.service.name = "knowledge-layer-api" }`, 20)))

	// DB-call rate by span name (spanmetrics, substrate — no blueprint label).
	// Covers the three Knowledge-Layer egress spans that hit a datastore.
	dashboard.AddPanel(&d, "rdb-dbspans",
		dashboard.TimeseriesPanel(
			"Knowledge-Layer DB-call rate by target (spanmetrics)", "reqps",
			dashboard.PromTarget(
				`sum by (span_name)(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"$env",span_kind="SPAN_KIND_CLIENT",span_name=~"call rds-acl|call documentdb-store|call opensearch-vectors"}[$__rate_interval]))`,
				"{{span_name}}")))

	// RDS CPU utilisation — CloudWatch gauge per-period (NEVER rate()).
	// dimension_DBInstanceIdentifier scoped by $db_instance variable.
	dashboard.AddPanel(&d, "rdb-pg-cpu",
		dashboard.TimeseriesPanel(
			"RDS CPU / connections — same instance + window", "percent",
			dashboard.PromTarget(
				`max(aws_rds_cpuutilization_maximum{dimension_DBInstanceIdentifier=~"$db_instance"})`,
				"CPU% max")))

	// RDS database connections — average gauge; same instance scoping as CPU panel.
	dashboard.AddPanel(&d, "rdb-pg-conn",
		dashboard.TimeseriesPanel(
			"RDS database connections (avg) — same instance", "short",
			dashboard.PromTarget(
				`avg(aws_rds_database_connections_average{dimension_DBInstanceIdentifier=~"$db_instance"})`,
				"connections")))

	// dbo11y query digests — Loki stream, no trace_id, correlate by instance + time window.
	// instance label format: postgresql://<host>... — match via prefix glob on $db_instance.
	dashboard.AddPanel(&d, "rdb-dbo-logs",
		dashboard.LogsPanel(
			"dbo11y query digests for this instance (Loki — correlate by time)",
			dashboard.LokiTarget(
				`{job="integrations/db-o11y", instance=~"postgresql://$db_instance.*"}`,
				"")))

	// ── Layout (tabs — rich Tabbed/Section layout) ───────────────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("Overview",
			dashboard.Section("",
				dashboard.Full("ov-intro")),
			dashboard.Section("Entry Point",
				dashboard.Full("ov-entry-hdr"),
				dashboard.Tall("ov-request"),
				dashboard.Tall("ov-corrkeys")),
		),
		dashboard.Tabbed("End-to-End Path",
			dashboard.Section("Hop Journey",
				dashboard.Full("path-hops-hdr"),
				dashboard.Tall("path-hops")),
			dashboard.Section("Trace Tier",
				dashboard.Full("path-trace-hdr"),
				dashboard.Tall("path-traces")),
			dashboard.Section("Logs Tier",
				dashboard.Full("path-logs-hdr"),
				dashboard.Tall("path-logs")),
		),
		dashboard.Tabbed("RUM → Trace → DB",
			dashboard.Section("",
				dashboard.Full("rdb-hdr")),
			dashboard.Section("Browser → Trace",
				dashboard.Tall("rdb-rum"),
				dashboard.Tall("rdb-trace")),
			dashboard.Section("Knowledge Layer → Datastores",
				dashboard.Half("rdb-dbspans"), dashboard.Half("rdb-pg-cpu")),
			dashboard.Section("Database health (instance + time-window correlation — no trace_id)",
				dashboard.Half("rdb-pg-conn"), dashboard.Tall("rdb-dbo-logs")),
		),
		dashboard.Tabbed("Eval Tier",
			dashboard.Section("LangSmith Eval Runs",
				dashboard.Full("eval-hdr"),
				dashboard.Tall("eval-runs")),
			dashboard.Section("Correlation Join",
				dashboard.Full("eval-join-hdr"),
				dashboard.Tall("eval-export-log"),
				dashboard.Tall("eval-runindex-log")),
		),
		dashboard.Tabbed("Topology",
			dashboard.Section("Service Graph",
				dashboard.Full("topo-hdr"),
				dashboard.Tall("topo-svcgraph")),
			dashboard.Section("Request Rate",
				dashboard.Full("topo-rate-svc")),
		),
		dashboard.Tabbed("Metrics",
			dashboard.Section("Gateway & Token",
				dashboard.Full("met-hdr"),
				dashboard.Half("met-portkey-lat"), dashboard.Half("met-genai-tokens")),
			dashboard.Section("Content Integrity",
				dashboard.Full("met-content-hdr")),
			dashboard.Section("Pipeline Health",
				dashboard.Tile("met-leak-test"), dashboard.Tile("met-dropped"),
				dashboard.Tile("met-spans-exported"), dashboard.Tile("met-portkey-reqs")),
		),
	)
	return d, nil
}
