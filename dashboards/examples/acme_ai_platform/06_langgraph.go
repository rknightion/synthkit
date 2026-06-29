// SPDX-License-Identifier: AGPL-3.0-only

// LangGraph is the Acme AI Agentic Workflow (LangGraph) dashboard (acme_ai_platform blueprint,
// predecessor 06-langgraph).
//
// Three tabs mirror the predecessor layout:
//   - Overview: agentic workflow KPI stats (agent rate, error %, p95 latency, tool call rate,
//     workflow count) + agent RED summary table (3-target merge: Calls/s, Error rate, p95) +
//     service graph (client→server) table.
//   - Per-agent / workflow: per-agent call rate + error % + p95 latency timeseries;
//     span latency distribution heatmap (native histogram); workflow throughput; plan/replan
//     rate & latency; tool calls by tool; retrieval (AOSS).
//   - Live traces: TraceQL trace table for invoke_agent spans.
//
// Scope notes (load-bearing):
//   - traces_spanmetrics_* and traces_service_graph_* are Tempo-metrics-generator-derived;
//     they carry NO blueprint label. The predecessor's scenario="$scenario" filter is DROPPED —
//     selectors are keyed on $deployment_environment and $service_name template vars.
//   - traces_spanmetrics_latency is a NATIVE histogram (no _bucket, no le). NativeHistogramQuantile
//     is used even though the predecessor JSON uses the classic histogram_quantile form — the synthkit
//     metrics-generator emits native exponential histograms, so the classic form would return
//     no data (see native-histogram-panel-direction memory + 07_apm_rum.go precedent).
//   - The agent RED summary table (predecessor panel-17) uses MergeTablePanel with 3 PromTableTarget
//     instant queries (A=call rate, B=error rate, C=p95 latency) merged by span_name. The
//     service-graph edges table (panel-18) wires one target (rate only — single series family).
//
// Variables: both QueryVariables query traces_spanmetrics_calls_total with NO blueprint/scenario
// filter — metrics-generator-derived family, substrate-scoped.
package acme_ai_platform

import (
	dashboardv2 "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"

	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// LangGraph builds the Acme AI Agentic Workflow (LangGraph) dashboard for the acme_ai_platform blueprint.
func LangGraph(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws1-langgraph", "Acme AI — LangGraph Traces")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ─────────────────────────────────────────────────────────────────────────────────
	// scenario: hidden const var so acme.AppSel($scenario) resolves if referenced elsewhere.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform"))
	// deployment_environment: metrics-generator family carries NO blueprint → drop scenario filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"deployment_environment", "Environment",
		"label_values(traces_spanmetrics_calls_total, deployment_environment_name)"))
	// service_name: same — drop scenario filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar(
		"service_name", "Service",
		"label_values(traces_spanmetrics_calls_total, service)"))

	// smSel builds a selector for traces_spanmetrics_* (NO blueprint label) filtered by the two
	// template vars plus an optional span_name pattern and extra matchers.
	// The predecessor's scenario="$scenario" is intentionally omitted — metrics-generator-derived.
	smSel := func(spanNamePattern, extra string) string {
		base := `deployment_environment_name=~"$deployment_environment",service=~"$service_name"`
		if spanNamePattern != "" {
			base += `,span_name=~"` + spanNamePattern + `"`
		}
		if extra != "" {
			base += "," + extra
		}
		return "{" + base + "}"
	}

	// smSelNoSvc builds a selector that omits service= (for cross-service plan/replan and retrieval spans
	// that may cross service boundaries in the predecessor queries).
	smSelNoSvc := func(spanNamePattern, extra string) string {
		base := `deployment_environment_name=~"$deployment_environment"`
		if spanNamePattern != "" {
			base += `,span_name=~"` + spanNamePattern + `"`
		}
		if extra != "" {
			base += "," + extra
		}
		return "{" + base + "}"
	}

	// sgSel builds a selector for traces_service_graph_* (NO blueprint label).
	sgSel := func() string {
		return `{deployment_environment_name=~"$deployment_environment"}`
	}

	// ── Overview tab ─────────────────────────────────────────────────────────────────────────────

	// KPI stat tiles — lead section for at-a-glance health strip.
	dashboard.AddPanel(&d, "lg-stat-agent-rate",
		dashboard.StatTile("Agent span rate", "reqps",
			dashboard.PromTarget(
				`sum(rate(traces_spanmetrics_calls_total`+smSel(`invoke_agent.*`, "")+`[$__rate_interval]))`,
				"req/s")))

	dashboard.AddPanel(&d, "lg-stat-agent-err",
		dashboard.StatTile("Agent error rate %", "percent",
			dashboard.PromTarget(
				`100 * sum(rate(traces_spanmetrics_calls_total`+
					smSel(`invoke_agent.*`, `status_code="STATUS_CODE_ERROR"`)+
					`[$__rate_interval])) / clamp_min(sum(rate(traces_spanmetrics_calls_total`+
					smSel(`invoke_agent.*`, "")+`[$__rate_interval])), 0.001)`,
				"Error %"),
			dashboard.Threshold{Value: 0, Color: "green"},
			dashboard.Threshold{Value: 1, Color: "yellow"},
			dashboard.Threshold{Value: 5, Color: "red"}))

	dashboard.AddPanel(&d, "lg-stat-agent-p95",
		dashboard.StatTile("p95 agent latency", "s",
			dashboard.PromTarget(
				// native histogram — no _bucket/le (synthkit metrics-generator emits native)
				dashboard.NativeHistogramQuantile(0.95, "traces_spanmetrics_latency", smSel(`invoke_agent.*`, ""), nil),
				"p95 (s)"),
			dashboard.Threshold{Value: 0, Color: "green"},
			dashboard.Threshold{Value: 2, Color: "yellow"},
			dashboard.Threshold{Value: 10, Color: "red"}))

	dashboard.AddPanel(&d, "lg-stat-tool-rate",
		dashboard.StatTile("Tool call rate", "reqps",
			dashboard.PromTarget(
				`sum(rate(traces_spanmetrics_calls_total`+smSel(`execute_tool.*`, "")+`[$__rate_interval]))`,
				"req/s")))

	dashboard.AddPanel(&d, "lg-stat-workflow-count",
		dashboard.StatTile("Workflow count (unique span_names)", "short",
			dashboard.PromTarget(
				`count(sum by (span_name)(rate(traces_spanmetrics_calls_total`+smSelNoSvc(`invoke_workflow.*`, "")+`[$__rate_interval])))`,
				"count")))

	// Agent RED summary table — 3-target merge (predecessor panel-17): call rate (A), error rate (B),
	// p95 latency (C) per span_name. MergeTablePanel joins the three instant queries on their shared
	// span_name label, one row per span with all three KPI columns.
	// Span scope: real in-process gen_ai spans (invoke_agent planner/retriever/summarization/passthrough/inference) emitted by
	// the acme app workload and generated into spanmetrics by the GC metrics-generator
	// (SPAN_KIND_INTERNAL). span_name=~"invoke_agent.*" gives one row per agent variant.
	dashboard.AddPanel(&d, "lg-agent-red-table",
		dashboard.MergeTablePanel("Agent RED summary",
			[]*dashboardv2.TargetBuilder{
				dashboard.PromTableTarget(
					`sum by (span_name)(rate(traces_spanmetrics_calls_total`+smSel(`invoke_agent.*`, "")+`[$__rate_interval]))`,
					"A"),
				dashboard.PromTableTarget(
					`sum by (span_name)(rate(traces_spanmetrics_calls_total`+
						smSel(`invoke_agent.*`, `status_code="STATUS_CODE_ERROR"`)+
						`[$__rate_interval]))`,
					"B"),
				dashboard.PromTableTarget(
					dashboard.NativeHistogramQuantile(0.95, "traces_spanmetrics_latency", smSel(`invoke_agent.*`, ""), []string{"span_name"}),
					"C"),
			},
			dashboard.OrganizeOptions{
				Exclude: []string{"Time"},
				Rename: map[string]string{
					"Value #A": "Calls/s",
					"Value #B": "Error rate",
					"Value #C": "p95 (s)",
				},
				Order: []string{"span_name", "Calls/s", "Error rate", "p95 (s)"},
			},
		))

	// Service graph edges table (client→server) — 3-leg RED merge table.
	// Error leg uses traces_service_graph_request_failed_total (separate counter — no status_code label
	// on service-graph metrics). Latency uses traces_service_graph_request_server_seconds (native histogram).
	// Grouped by client + server.
	dashboard.AddPanel(&d, "lg-svcgraph-table",
		dashboard.MergeTablePanel("Service graph (client → server edges)",
			[]*dashboardv2.TargetBuilder{
				dashboard.PromTableTarget(
					`sum by (client, server)(rate(traces_service_graph_request_total`+sgSel()+`[$__rate_interval]))`,
					"A"),
				dashboard.PromTableTarget(
					`100 * sum by (client, server)(rate(traces_service_graph_request_failed_total`+sgSel()+`[$__rate_interval])) / (sum by (client, server)(rate(traces_service_graph_request_total`+sgSel()+`[$__rate_interval])) + 1)`,
					"B"),
				dashboard.PromTableTarget(
					dashboard.NativeHistogramQuantile(0.95, "traces_service_graph_request_server_seconds", sgSel(), []string{"client", "server"}),
					"C"),
			},
			dashboard.OrganizeOptions{
				Rename:  map[string]string{"Value #A": "Rate (rps)", "Value #B": "Error %", "Value #C": "p95 (ms)"},
				Exclude: []string{"Time"},
				Order:   []string{"client", "server", "Rate (rps)", "Error %", "p95 (ms)"},
			},
		))

	// ── Per-agent / workflow tab ──────────────────────────────────────────────────────────────────

	// Per-agent call rate timeseries (stacked by span_name).
	dashboard.AddPanel(&d, "lg-agent-rate-ts",
		dashboard.TimeseriesPanel("Per-agent call rate", "reqps",
			dashboard.PromTarget(
				`sum by (span_name)(rate(traces_spanmetrics_calls_total`+smSel(`invoke_agent.*`, "")+`[$__rate_interval]))`,
				"{{span_name}}")))

	// Per-agent error rate % timeseries.
	dashboard.AddPanel(&d, "lg-agent-err-ts",
		dashboard.TimeseriesPanel("Per-agent error rate %", "percent",
			dashboard.PromTarget(
				`100 * sum by (span_name)(rate(traces_spanmetrics_calls_total`+
					smSel(`invoke_agent.*`, `status_code="STATUS_CODE_ERROR"`)+
					`[$__rate_interval])) / clamp_min(sum by (span_name)(rate(traces_spanmetrics_calls_total`+
					smSel(`invoke_agent.*`, "")+`[$__rate_interval])), 0.001)`,
				"{{span_name}}")))

	// Per-agent p95 latency timeseries (native histogram).
	dashboard.AddPanel(&d, "lg-agent-lat-ts",
		dashboard.TimeseriesPanel("Per-agent p95 latency (s)", "s",
			dashboard.PromTarget(
				dashboard.NativeHistogramQuantile(0.95, "traces_spanmetrics_latency", smSel(`invoke_agent.*`, ""), []string{"span_name"}),
				"{{span_name}}")))

	// Span latency distribution heatmap (native histogram) — agent span latency distribution.
	// Scoped to invoke_agent.* spans (planner/retriever/summarization/passthrough/inference): real in-process gen_ai spans
	// generated into spanmetrics by the GC metrics-generator (SPAN_KIND_INTERNAL).
	// NativeHistogramRate emits sum(rate(metric selector [$__rate_interval])). NativeHistogramHeatmap
	// sets Calculate(false) so Grafana auto-detects the exponential bucket layout without re-bucketing.
	dashboard.AddPanel(&d, "lg-span-lat-heatmap",
		dashboard.NativeHistogramHeatmap(
			"Span latency distribution (native)",
			"s",
			dashboard.PromTarget(
				dashboard.NativeHistogramRate("traces_spanmetrics_latency", smSel(`invoke_agent.*`, "")),
				"")))

	// Workflow throughput by workflow name (stacked timeseries).
	dashboard.AddPanel(&d, "lg-workflow-rate-ts",
		dashboard.TimeseriesPanel("Workflow throughput by workflow", "reqps",
			dashboard.PromTarget(
				`sum by (span_name)(rate(traces_spanmetrics_calls_total`+smSel(`invoke_workflow.*`, "")+`[$__rate_interval]))`,
				"{{span_name}}")))

	// Plan & replan rate & latency — the planner's plan-orchestration loop (Acme AI ContentGen
	// plan-and-execute: dynamic plan generation + automatic replanning on validation
	// failure). Scoped to the planner's plan_generate / replan tool spans. Two targets (rate +
	// native p95); spans may cross services so smSelNoSvc covers both.
	dashboard.AddPanel(&d, "lg-plan-replan-ts",
		dashboard.TimeseriesPanel("Plan & replan rate & latency", "reqps",
			dashboard.PromTarget(
				`sum by (span_name)(rate(traces_spanmetrics_calls_total`+
					smSelNoSvc(`execute_tool (plan_generate|replan|plan_lint)`, "")+`[$__rate_interval]))`,
				"rate · {{span_name}}").RefId("A"),
			dashboard.PromTarget(
				dashboard.NativeHistogramQuantile(0.95, "traces_spanmetrics_latency", smSelNoSvc(`execute_tool (plan_generate|replan|plan_lint)`, ""), []string{"span_name"}),
				"p95 · {{span_name}}").RefId("B")))

	// Tool calls by tool name (stacked timeseries).
	dashboard.AddPanel(&d, "lg-tool-calls-ts",
		dashboard.TimeseriesPanel("Tool calls by tool", "reqps",
			dashboard.PromTarget(
				`sum by (span_name)(rate(traces_spanmetrics_calls_total`+smSel(`execute_tool.*`, "")+`[$__rate_interval]))`,
				"{{span_name}}")))

	// Retrieval span rate (AOSS) — rate + native p95 latency.
	// predecessor description: "retrieval aoss-acme-kb span rate — vector search boundary latency."
	dashboard.AddPanel(&d, "lg-retrieval-ts",
		dashboard.TimeseriesPanel("Retrieval span rate (AOSS)", "reqps",
			dashboard.PromTarget(
				`sum(rate(traces_spanmetrics_calls_total`+smSelNoSvc(`retrieval.*`, "")+`[$__rate_interval]))`,
				"retrieval rate").RefId("A"),
			dashboard.PromTarget(
				dashboard.NativeHistogramQuantile(0.95, "traces_spanmetrics_latency", smSelNoSvc(`retrieval.*`, ""), nil),
				"retrieval p95 (s)").RefId("B")))

	// ── Live traces tab ───────────────────────────────────────────────────────────────────────────

	// Trace table for invoke_agent spans via Tempo TraceQL (gen_ai.operation.name attribute).
	// The predecessor uses: { span.gen_ai.operation.name = "invoke_agent" } with tableType:traces.
	dashboard.AddPanel(&d, "lg-traces-invoke-agent",
		dashboard.TraceTablePanel(
			`Live traces — invoke_agent spans (TraceQL, tableType:traces)`,
			acme.DSTempo,
			dashboard.TempoTableTarget(`{ span.gen_ai.operation.name = "invoke_agent" }`, 20)))

	// Graph node traversal trace table — spans emitted by the LangGraph executor on each node hop.
	// These carry span.gen_ai.operation.name = "graph_node" in the agent-runtime service.
	dashboard.AddPanel(&d, "lg-traces-graph-node",
		dashboard.TraceTablePanel(
			`Live traces — graph node traversal (TraceQL, tableType:traces)`,
			acme.DSTempo,
			dashboard.TempoTableTarget(`{ resource.service.name = "`+acme.SvcAgentRuntime+`" }`, 20)))

	// ── Tab layout (rich) ─────────────────────────────────────────────────────────────────────────

	dashboard.WithTabs(&d,
		dashboard.Tabbed("Overview",
			dashboard.Section("Agent workflow KPIs",
				dashboard.Tile("lg-stat-agent-rate"),
				dashboard.Tile("lg-stat-agent-err"),
				dashboard.Tile("lg-stat-agent-p95"),
				dashboard.Tile("lg-stat-tool-rate"),
				dashboard.Tile("lg-stat-workflow-count")),
			dashboard.Section("Summary tables",
				dashboard.Half("lg-agent-red-table"),
				dashboard.Half("lg-svcgraph-table")),
		),
		dashboard.Tabbed("Per-agent / workflow",
			dashboard.Section("Agent RED",
				dashboard.Half("lg-agent-rate-ts"),
				dashboard.Half("lg-agent-err-ts"),
				dashboard.Full("lg-agent-lat-ts"),
				dashboard.Full("lg-span-lat-heatmap")),
			dashboard.Section("Workflow & tools",
				dashboard.Half("lg-workflow-rate-ts"),
				dashboard.Half("lg-tool-calls-ts")),
			dashboard.Section("Planning & retrieval",
				dashboard.Half("lg-plan-replan-ts"),
				dashboard.Half("lg-retrieval-ts")),
		),
		dashboard.Tabbed("Live traces",
			dashboard.Section("",
				dashboard.Tall("lg-traces-invoke-agent"),
				dashboard.Tall("lg-traces-graph-node")),
		),
	)
	return d, nil
}
