// SPDX-License-Identifier: AGPL-3.0-only

// RequestCorrelation is the Acme AI Request Correlation dashboard for the acme_ai_platform_eval
// blueprint. It follows ONE request across every telemetry tier — the CONNECTED-SPAN version
// where the Portkey gateway span is in the SAME Tempo trace as the app spans (Path-B trace
// reuse). Ported from the predecessor connected-trace request-correlation dashboard (2026-06-16).
//
// acme_ai_platform_eval (connected-gateway) vs acme_ai_platform (analytics-poller) distinction:
//   - In acme_ai_platform_eval the AI Evaluation gateway is CONNECTED: Path-B reuses the caller's
//     trace_id, so the gateway span (service.name=portkey) appears in the same Tempo trace tree
//     as the app spans.
//   - The log-join chain through (e) 2b-export + (f-ii) run-index Loki streams is NOT needed here
//     because the join is trace-native: one trace_id covers both app and gateway hops.
//   - A real-time gateway log stream IS present ({source="portkey",service_name="portkey"})
//     and carries trace_id as structured metadata → pivot is trivial.
//
// Tab structure:
//
//	Overview      — intro text + connected-trace table (TraceQL by correlation_id)
//	Gateway       — gateway spans table (TraceQL by service.name=portkey) +
//	                backend→gateway service-graph edge timeseries +
//	                real-time gateway log (joined by trace_id)
//	Logs          — all logs for this request (every source, joined by correlation_id)
//	Eval Tier     — LangSmith eval runs Infinity table (otel_trace_id IS the connected trace_id)
//
// Scope notes (load-bearing):
//   - traces_spanmetrics_* and traces_service_graph_* are metrics-generator-derived; they carry
//     NO blueprint label → hand-written selectors only (no AppSel / IntSel).
//   - Real-time Portkey gateway log: {source="portkey",service_name="portkey"} is substrate-scoped;
//     predecessor had scenario="acme_ai_platform_eval" on this stream — dropped (not a synthkit label).
//   - All-logs panel: predecessor used {scenario="acme_ai_platform_eval"} → corrected to
//     {blueprint="acme-ai-platform-eval"} (the synthkit stream-selector label for app streams).
//   - gen_ai_client_token_usage_* is APP-scoped → acme.AppSel().
//   - acme_content_leak_test / acme_content_dropped_total → de-Roch names via
//     acme.MetricContentLeakTest / acme.MetricContentDropped (APP-scoped → AppSel).
//
// Variables:
//   - correlation_id: predecessor uses a Tempo tag-values QueryVariable (span.app.correlation_id).
//     The builder DSL has no Tempo-backed QueryVariable builder → fall back to TextVar.
//     This is a known builder gap — noted in the report.
//   - No portkey_trace_id variable needed: the join is by trace_id (not the 2b-export
//     portkey_trace_id stitch key used in acme_ai_platform).
//
// Infinity panels:
//   - /api/v1/runs/query  (predecessor uses POST with JSON body; wired as GET — note in report)
//
// Naming: this dashboard uses "Request Correlation" / "End-to-End Trace" / "connected trace" throughout.
package acme_ai_platform_eval

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// RequestCorrelation builds the Acme AI Request Correlation dashboard for the acme_ai_platform_eval (WS2)
// blueprint. uid: acme-ws2-request-correlation.
func RequestCorrelation(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard(
		"acme-ws2-request-correlation",
		"Acme AI-ws2 — Request Correlation")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ────────────────────────────────────────────────────────────────────────────────

	// scenario: hidden ConstVar → app selectors resolve via acme.AppSel($scenario).
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform_eval"))

	// env: deployment_environment_name multi-select (app families carry this label).
	d.Builder.CustomVariable(dashboard.EnvVar(m))

	// Infinity tables use RELATIVE paths — the Infinity datasource's base URL (the host FQDN,
	// served via tailscale serve) supplies the host. Do NOT embed an absolute base here.

	// correlation_id: predecessor uses a Tempo tag-values QueryVariable (span.app.correlation_id).
	// GAP: the builder DSL has no Tempo-backed QueryVariable builder → TextVar for manual paste.
	d.Builder.TextVariable(dashboard.TextVar("correlation_id", "Correlation ID", ""))

	// trace_id: optional W3C trace-id paste box (pivot the gateway log panel to one trace).
	// Predecessor description: "paste a W3C trace_id to pin the gateway log panel to one connected trace."
	d.Builder.TextVariable(dashboard.TextVar("trace_id", "Trace ID (optional)", ""))

	// ── TAB: Overview ────────────────────────────────────────────────────────────────────────────

	bt := "`"

	// Intro text panel (predecessor p-intro — de-Rochified, terminology ban applied).
	dashboard.AddPanel(&d, "ov-intro",
		dashboard.TextPanel("Request Correlation — Connected Span (With AI Evaluation)",
			"## Request Correlation — Connected Span (With AI Evaluation)\n\n"+
				"In the **acme_ai_platform_eval** scenario the **Portkey gateway span is CONNECTED in the same "+
				"Tempo trace** as the app spans — Path-B reuses the caller's "+bt+"trace_id"+bt+", so the "+
				"app → gateway → Bedrock journey is a single connected span tree (no log-join needed).\n\n"+
				"Contrast with the analytics-poller deployment, where the gateway is a black box and the join hops "+
				"through the 2b-export + run-index Loki streams.\n\n"+
				"**How to use:** pick a "+bt+"correlation_id"+bt+" (paste a value from a recent trace) — "+
				"the connected-trace table, the real-time gateway log line, and the LangSmith eval row all "+
				"pivot to that one request. The trace now contains both the app spans AND the gateway span "+
				"on the same "+bt+"trace_id"+bt+"."))

	// Connected trace table — TraceQL by correlation_id (predecessor p-trace).
	// Scope: Tempo search by span attribute — no blueprint label needed in TraceQL.
	dashboard.AddPanel(&d, "ov-trace",
		dashboard.TraceTablePanel(
			"Connected trace (app + gateway, same trace_id) — TraceQL by correlation_id",
			acme.DSTempo,
			dashboard.TempoTableTarget(
				`{ span.app.correlation_id =~ "$correlation_id" }`, 20)))

	// ── TAB: Gateway ─────────────────────────────────────────────────────────────────────────────

	// Section header: Gateway spans.
	dashboard.AddPanel(&d, "gw-spans-hdr",
		dashboard.TextPanel("",
			"## Gateway Spans — Path-B Trace Reuse\n\n"+
				"The gateway span appears in the same Tempo trace as the backend spans because Path-B "+
				"propagates the caller's "+bt+"trace_id"+bt+" into the gateway. Edge present ONLY in "+
				"acme_ai_platform_eval — absent under the analytics-poller deployment."))

	// Gateway spans table — TraceQL by service.name=portkey (predecessor p-gwspans).
	// Scope: Tempo search by resource attribute — no blueprint label needed.
	dashboard.AddPanel(&d, "gw-spans",
		dashboard.TraceTablePanel(
			`Gateway spans (Path-B trace reuse) — TraceQL { resource.service.name =~ "portkey.*" }`,
			acme.DSTempo,
			dashboard.TempoTableTarget(
				`{ resource.service.name =~ "portkey.*" }`, 20)))

	// Backend → gateway service-graph edge (predecessor p-edge).
	// traces_service_graph_* is metrics-generator-derived — NO blueprint label.
	// Predecessor had scenario="acme_ai_platform_eval" on this family — DROPPED (not a metrics-generator label).
	// Scoped by server=~".*portkey.*" to isolate the gateway edge that only exists in WS2.
	dashboard.AddPanel(&d, "gw-edge",
		dashboard.TimeseriesPanel(
			"Backend → gateway service-graph edge (proof of connection — absent in WS1)", "reqps",
			dashboard.PromTarget(
				`sum by (client, server)(rate(traces_service_graph_request_total{server=~".*portkey.*"}[$__rate_interval]))`,
				"{{client}} → {{server}}")))

	// Real-time Portkey gateway log — joined by trace_id (predecessor p-gwlog).
	// Substrate stream: source="portkey", service_name="portkey" — NO blueprint label.
	// Predecessor had scenario="acme_ai_platform_eval" on this stream — DROPPED (not a synthkit substrate label).
	// Filter by trace_id structured metadata (paste from the trace table above or the trace_id var).
	dashboard.AddPanel(&d, "gw-log",
		dashboard.LogsPanel(
			`Real-time Portkey gateway log — joined by trace_id (source=portkey, service_name=portkey)`,
			dashboard.LokiTarget(
				`{source="portkey",service_name="portkey"} | trace_id=~"${trace_id:raw}"`,
				"")))

	// ── TAB: Logs ────────────────────────────────────────────────────────────────────────────────

	// All logs for this request — every source, joined by correlation_id (predecessor p-alllog).
	// Predecessor used {scenario="acme_ai_platform_eval"} — corrected to blueprint= (synthkit app stream label).
	// In WS2 this includes the live gateway stream (not suppressed as in WS1).
	dashboard.AddPanel(&d, "log-all-hdr",
		dashboard.TextPanel("",
			"## End-to-End Logs — Every Source, Joined on correlation_id\n\n"+
				"In **acme_ai_platform_eval** (WS2) the real-time Portkey gateway stream is **present** and "+
				"included here — unlike WS1 where the gateway stream is suppressed. All tiers: "+
				"app backend · langgraph agent · portkey gateway · bedrock invocation."))

	dashboard.AddPanel(&d, "log-all",
		dashboard.LogsPanel(
			"All logs for this request — every source (correlation_id)",
			dashboard.LokiTarget(
				`{blueprint="acme-ai-platform-eval"} | correlation_id=~"$correlation_id"`,
				"")))

	// ── TAB: Eval Tier ────────────────────────────────────────────────────────────────────────────

	// Section header: LangSmith eval tier.
	dashboard.AddPanel(&d, "eval-hdr",
		dashboard.TextPanel("LangSmith Eval — Connected Trace Join",
			"## LangSmith Eval Runs — Quality Evidence\n\n"+
				"The LangSmith eval run's "+bt+"otel_trace_id"+bt+" IS the connected "+bt+"trace_id"+bt+
				" — one click opens the full app+gateway waterfall in Tempo. "+
				"In WS2 the eval→trace join is direct (no portkey_trace_id stitch needed)."))

	// LangSmith eval runs — /api/v1/runs/query (predecessor p-eval).
	// Predecessor issues POST with a JSON select-list body; mirrored here via InfinityTargetPOST
	// (same pattern as WS1 09_request_correlation.go panel-7).
	// real Acme AI stack: predecessor endpoint is the real LangSmith POST /api/v1/runs/query.
	dashboard.AddPanel(&d, "eval-runs",
		dashboard.TablePanel(
			"LangSmith eval runs — quality evidence (otel_trace_id IS the connected trace_id → click → Tempo)",
			dashboard.InfinityTargetPOST("A", "/api/v1/runs/query",
				"synthkit (Infinity)", "runs",
				`{"select":["id","name","run_type","status","start_time","total_tokens","total_cost","extra","feedback_stats"]}`,
				dashboard.Col("id", "Run ID", "string"),
				dashboard.Col("name", "Name", "string"),
				dashboard.Col("status", "Status", "string"),
				dashboard.Col("start_time", "start_time", "string"),
				dashboard.Col("total_tokens", "Tokens", "number"),
				dashboard.Col("extra.metadata.otel_trace_id", "otel_trace_id", "string"),
				dashboard.Col("extra.metadata.correlation_id", "correlation_id", "string"),
				dashboard.Col("extra.metadata.portkey_trace_id", "portkey_trace_id", "string"),
				dashboard.Col("extra.metadata.use_case", "Use Case", "string"),
				dashboard.Col("feedback_stats.faithfulness.avg", "Faithfulness", "number"),
				dashboard.Col("feedback_stats.completeness.avg", "Completeness", "number"),
			)))

	// Additional WS2 Metrics: gen_ai token usage (APP-scoped → AppSel).
	// Not in the predecessor WS2 dashboard but useful WS2 context panel (same family, different blueprint).
	dashboard.AddPanel(&d, "met-genai-hdr",
		dashboard.TextPanel("",
			"## Metrics — Token Spend & Content Integrity"))

	// gen_ai token usage by type (APP-scoped → AppSel).
	dashboard.AddPanel(&d, "met-genai-tokens",
		dashboard.TimeseriesPanel(
			"gen_ai token usage — input vs output (acme_ai_platform_eval)", "short",
			dashboard.PromTarget(
				`sum by (gen_ai_token_type)(rate(gen_ai_client_token_usage_sum`+
					acme.AppSel("")+`[$__rate_interval]))`,
				"{{gen_ai_token_type}}")))

	// Content leak test — must be 0 (APP-scoped → AppSel).
	// real Acme AI stack: acme_content_leak_test
	dashboard.AddPanel(&d, "met-leak",
		dashboard.StatTile(
			"Content leak test (must be 0 → CLEAN)", "short",
			dashboard.PromTarget(
				`max(`+acme.MetricContentLeakTest+acme.AppSel("")+`)`,
				"leak test"),
			dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "red"}))

	// Content dropped counter (APP-scoped → AppSel).
	// real Acme AI stack: acme_content_dropped_total
	dashboard.AddPanel(&d, "met-dropped",
		dashboard.StatTile(
			"Content dropped (strip events — rising = healthy)", "short",
			dashboard.PromTarget(
				`sum(`+acme.MetricContentDropped+acme.AppSel("")+`)`,
				"dropped")))

	// ── Layout (tabs — rich Tabbed/Section layout) ───────────────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("Overview",
			dashboard.Section("",
				dashboard.Full("ov-intro")),
			dashboard.Section("Connected Trace",
				dashboard.Tall("ov-trace")),
		),
		dashboard.Tabbed("Gateway",
			dashboard.Section("Gateway Spans",
				dashboard.Full("gw-spans-hdr")),
			dashboard.Section("",
				dashboard.Half("gw-spans"), dashboard.Half("gw-edge")),
			dashboard.Section("Real-time Gateway Log",
				dashboard.Tall("gw-log")),
		),
		dashboard.Tabbed("Logs",
			dashboard.Section("",
				dashboard.Full("log-all-hdr"),
				dashboard.Tall("log-all")),
		),
		dashboard.Tabbed("Eval Tier",
			dashboard.Section("LangSmith Evals",
				dashboard.Full("eval-hdr"),
				dashboard.Tall("eval-runs")),
			dashboard.Section("Metrics",
				dashboard.Full("met-genai-hdr")),
			dashboard.Section("",
				dashboard.Half("met-genai-tokens"),
				dashboard.Tile("met-leak"),
				dashboard.Tile("met-dropped")),
		),
	)

	return d, nil
}
