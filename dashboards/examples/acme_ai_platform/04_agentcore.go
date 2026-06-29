// SPDX-License-Identifier: AGPL-3.0-only

// AgentCore — Acme AI AgentCore Runtime (acme-ai-platform blueprint).
//
// AWS Bedrock AgentCore vended CloudWatch metric-stream families (aws_bedrock_agentcore_*)
// are SUBSTRATE-SCOPED (no blueprint label) — the predecessor's {scenario="$scenario"} filter is
// DROPPED for every CW metric query (CW metric-stream series carry no blueprint label on
// the example stack). Per-period CW _sum/_average statistics are GAUGES — /60 gives per-second
// representation; never rate(). _average statistics use avg().
//
// The predecessor's "By Environment" tab uses a row-repeat-by-variable pattern; this is reproduced
// via RepeatSection("env") so Grafana clones the summary row once per selected $env value,
// scoping each clone to account_id="$env" alongside the multi-env Overview tab.
//
// Loki log gaps (panels are wired faithfully but will be EMPTY until a fill lands):
//   - {source="agentcore_app"}   — APPLICATION_LOGS — documented in signals/agentcore.md,
//     NOT yet emitted by any construct. Wire: LogsPanel, label as GAP.
//   - {source="agentcore_usage"} — USAGE_LOGS (session_id, cpu, memory) — same status.
//
// {source="agentcore_spans"} (service-plane span logs) is DEFERRED — omitted entirely per
// the porting instructions (the span table + latency + error panels from the predecessor are not
// reproduced here; they require the agentcore_spans Loki lane which is deferred).
//
// Tabs: Overview · By Environment (repeat-per-$env) · Logs Attribution.
package acme_ai_platform

import "github.com/rknightion/synthkit/dashboard"

// acSel returns a raw label-matcher selector for aws_bedrock_agentcore_* families
// (substrate-scoped, no blueprint label). These CW metric-stream families carry account_id
// and dimension_Name. The predecessor's scenario= filter is dropped (CW substrate, no blueprint).
// extra is an already-formatted matcher list (no leading comma).
func acSel(extra string) string {
	s := `account_id=~"$env",dimension_Name=~"$dimension_Name"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// acEnvSel scopes to a single environment (account_id="$env") — used for the By Environment
// tab panels. RepeatSection clones the row once per $env value; each clone resolves $env to
// one account_id so these panels correctly scope to that environment's data.
func acEnvSel(extra string) string {
	s := `account_id=~"$env",dimension_Name=~"$dimension_Name"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// AgentCore is the Acme AI AgentCore Runtime dashboard for the acme-ai-platform blueprint.
// uid: acme-ws1-agentcore. Three tabs reproducing the predecessor's agentcore layout
// (agentcore_spans panels deferred; agentcore_app/usage log panels wired but empty).
func AgentCore(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws1-agentcore", "Acme AI — AgentCore Runtime")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ─────────────────────────────────────────────────────────────────────────────
	// scenario: ConstVar — keeps AppSel helpers working; hidden from picker.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform"))

	// env: label_values from aws_bedrock_agentcore_invocations_sum — substrate, no scenario= filter.
	// The predecessor var is named "env" and filters by account_id. The _sum series is per-period gauge;
	// the var just enumerates which account_ids are present.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("env", "Environment (account)",
		"label_values(aws_bedrock_agentcore_invocations_sum, account_id)"))

	// dimension_Name: label_values for agent-name filtering — substrate, no scenario= filter.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("dimension_Name", "Agent Name",
		"label_values(aws_bedrock_agentcore_invocations_sum, dimension_Name)"))

	// ── Tab 1: Overview ───────────────────────────────────────────────────────────────────────
	// Row: KPIs — all envs (6 stat tiles matching predecessor panel-kpi-*)

	// Invocation rate — invocations_sum is a CW per-period gauge; /60 = inv/s.
	dashboard.AddPanel(&d, "ov-invocrate", dashboard.StatTile("Invocation rate", "reqps",
		dashboard.PromTarget(
			"sum(aws_bedrock_agentcore_invocations_sum"+acSel("")+") / 60",
			"inv/s")))

	// Total error rate — ratio of total_errors_sum to invocations_sum (rate-free gauge ratio).
	// predecessor description: "Total errors (system + user) as a percentage of invocations, from
	// vended CloudWatch per-period gauges (ratio of sums — rate-free)."
	dashboard.AddPanel(&d, "ov-errpct", dashboard.StatTile("Total error rate", "percent",
		dashboard.PromTarget(
			"100 * sum(aws_bedrock_agentcore_total_errors_sum"+acSel("")+") / clamp_min(sum(aws_bedrock_agentcore_invocations_sum"+acSel("")+"), 1)",
			"error %"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 2, Color: "yellow"}, dashboard.Threshold{Value: 5, Color: "red"}))

	// Session count — session_count_sum is a CW per-period gauge count.
	dashboard.AddPanel(&d, "ov-sessions", dashboard.StatTile("Session count", "short",
		dashboard.PromTarget(
			"sum(aws_bedrock_agentcore_session_count_sum"+acSel("")+")",
			"sessions")))

	// Active streaming connections — _average statistic.
	dashboard.AddPanel(&d, "ov-streaming", dashboard.StatTile("Active streaming connections", "short",
		dashboard.PromTarget(
			"sum(aws_bedrock_agentcore_active_streaming_connections_average"+acSel("")+")",
			"connections")))

	// CPU-hours used — cpu_used_v_cpu_hours_sum / 60 = vCPU-h/s. ≤60-min reporting lag.
	dashboard.AddPanel(&d, "ov-cpu", dashboard.StatTile("CPU-hours used", "short",
		dashboard.PromTarget(
			"sum(aws_bedrock_agentcore_cpu_used_v_cpu_hours_sum"+acSel("")+") / 60",
			"vCPU-h/s")))

	// Memory-hours used — memory_used_gb_hours_sum / 60 = GB-h/s. ≤60-min reporting lag.
	dashboard.AddPanel(&d, "ov-mem", dashboard.StatTile("Memory-hours used", "short",
		dashboard.PromTarget(
			"sum(aws_bedrock_agentcore_memory_used_gb_hours_sum"+acSel("")+") / 60",
			"GB-h/s")))

	// Row: Invocations & latency timeseries
	// Predecessor panel-overview-invocations: 4-series (invocations, system errors, user errors, throttles).
	dashboard.AddPanel(&d, "ov-invoc-ts", dashboard.TimeseriesPanel("Invocations & errors over time", "reqps",
		dashboard.PromTarget(
			"sum(aws_bedrock_agentcore_invocations_sum"+acSel("")+") / 60",
			"invocations").RefId("A"),
		dashboard.PromTarget(
			"sum(aws_bedrock_agentcore_system_errors_sum"+acSel("")+") / 60",
			"system errors").RefId("B"),
		dashboard.PromTarget(
			"sum(aws_bedrock_agentcore_user_errors_sum"+acSel("")+") / 60",
			"user errors").RefId("C"),
		dashboard.PromTarget(
			"sum(aws_bedrock_agentcore_throttles_sum"+acSel("")+") / 60",
			"throttles").RefId("D")))

	// Predecessor panel-overview-latency: avg by (dimension_Name) of _average stat.
	// predecessor description: "Does NOT include in-runtime LLM time."
	dashboard.AddPanel(&d, "ov-latency-ts", dashboard.TimeseriesPanel("Latency (average, ms)", "ms",
		dashboard.PromTarget(
			"avg by (dimension_Name) (aws_bedrock_agentcore_latency_average"+acSel("")+")",
			"{{dimension_Name}}")))

	// ── Tab 2: By Environment (RepeatSection per $env) ────────────────────────────────────────
	// Reproduces the predecessor's row-repeat-by-variable pattern via RepeatSection("env").
	// Grafana clones this row once per $env value; each clone pins $env to one account_id.

	// Sessions per env (predecessor panel-env-sessions)
	dashboard.AddPanel(&d, "env-sessions", dashboard.StatTile("Sessions ($env)", "short",
		dashboard.PromTarget(
			"sum(aws_bedrock_agentcore_session_count_sum"+acEnvSel("")+") / 60",
			"sessions/s")))

	// Invocations per env (predecessor panel-env-invocations)
	dashboard.AddPanel(&d, "env-invocations", dashboard.StatTile("Invocations ($env)", "reqps",
		dashboard.PromTarget(
			"sum(aws_bedrock_agentcore_invocations_sum"+acEnvSel("")+") / 60",
			"inv/s")))

	// CPU-hours per env (predecessor panel-env-cpu)
	// predecessor description: "vCPU-hours/s (CW per-period gauge / 60). ≤60-min reporting lag."
	dashboard.AddPanel(&d, "env-cpu", dashboard.StatTile("CPU-hours ($env)", "short",
		dashboard.PromTarget(
			"sum(aws_bedrock_agentcore_cpu_used_v_cpu_hours_sum"+acEnvSel("")+") / 60",
			"vCPU-h/s")))

	// Memory-hours per env (predecessor panel-env-mem)
	// predecessor description: "GB-hours/s (CW per-period gauge / 60). ≤60-min reporting lag."
	dashboard.AddPanel(&d, "env-mem", dashboard.StatTile("Memory-hours ($env)", "short",
		dashboard.PromTarget(
			"sum(aws_bedrock_agentcore_memory_used_gb_hours_sum"+acEnvSel("")+") / 60",
			"GB-h/s")))

	// Error rate per env (predecessor panel-env-errors)
	// predecessor description: "Total errors / invocations for this environment (rate-free ratio)."
	dashboard.AddPanel(&d, "env-errors", dashboard.StatTile("Error rate ($env)", "percent",
		dashboard.PromTarget(
			"100 * sum(aws_bedrock_agentcore_total_errors_sum"+acEnvSel("")+") / clamp_min(sum(aws_bedrock_agentcore_invocations_sum"+acEnvSel("")+"), 1)",
			"error%"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 2, Color: "yellow"}, dashboard.Threshold{Value: 5, Color: "red"}))

	// Latency avg per env (predecessor panel-env-latency)
	dashboard.AddPanel(&d, "env-latency", dashboard.StatTile("Latency avg ms ($env)", "ms",
		dashboard.PromTarget(
			"avg(aws_bedrock_agentcore_latency_average"+acEnvSel("")+")",
			"avg ms"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 3000, Color: "yellow"}, dashboard.Threshold{Value: 8000, Color: "red"}))

	// ── Tab 3: Logs Attribution ────────────────────────────────────────────────────────────────
	// agentcore_spans panels (panel-spans-table, panel-spans-latency, panel-spans-errors) are
	// DEFERRED — the {source="agentcore_spans"} Loki lane is not yet emitted; omitted entirely.
	//
	// agentcore_app and agentcore_usage: wired faithfully; will be EMPTY until the log-fill
	// lands (signals/agentcore.md documents both lanes; no construct emits them yet).

	// GAP: agentcore_app — APPLICATION_LOGS from the AgentCore runtime (content-stripped).
	// Agent step lifecycle, status transitions, PII-redaction events. trace_id + correlation_id.
	// signals/agentcore.md §APPLICATION_LOGS. NOT yet emitted — panel wired, will be empty.
	dashboard.AddPanel(&d, "log-app", dashboard.LogsPanel(
		"AgentCore app logs (agentcore_app) — GAP: lane not yet emitted",
		dashboard.LokiTarget(
			// Substrate-scoped: no blueprint/scenario label on agentcore_app stream (CW feed).
			// The predecessor uses {source="agentcore_app", scenario="$scenario"} — drop scenario= per scope rule.
			`{source="agentcore_app"} | json`,
			"")))

	// GAP: agentcore_usage — USAGE_LOGS: session_id, cpu (fraction), memory (bytes), 1-second cadence.
	// Fine-grained resource usage per session; complements the ≤60-min-lag vended CW resource metrics.
	// signals/agentcore.md §USAGE_LOGS. NOT yet emitted — panel wired, will be empty.
	dashboard.AddPanel(&d, "log-usage", dashboard.LogsPanel(
		"AgentCore usage logs (agentcore_usage) — GAP: lane not yet emitted",
		dashboard.LokiTarget(
			// Substrate-scoped: drop scenario= filter per scope rule.
			`{source="agentcore_usage"} | json`,
			"")))

	// ── Tab layout (rich: Tabbed / Section / sized-Cells) ────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("Overview",
			dashboard.Section("Service-level KPIs",
				dashboard.Tile("ov-invocrate"), dashboard.Tile("ov-errpct"), dashboard.Tile("ov-sessions"),
				dashboard.Tile("ov-streaming"), dashboard.Tile("ov-cpu"), dashboard.Tile("ov-mem")),
			dashboard.Section("Invocations & Latency",
				dashboard.Half("ov-invoc-ts"), dashboard.Half("ov-latency-ts")),
		),
		dashboard.Tabbed("By Environment",
			dashboard.RepeatSection("Per-environment summary", "env",
				dashboard.Tile("env-sessions"), dashboard.Tile("env-invocations"), dashboard.Tile("env-cpu"),
				dashboard.Tile("env-mem"), dashboard.Tile("env-errors"), dashboard.Tile("env-latency")),
		),
		dashboard.Tabbed("Logs Attribution",
			dashboard.Section("",
				dashboard.Tall("log-app"), dashboard.Tall("log-usage")),
		),
	)
	return d, nil
}
