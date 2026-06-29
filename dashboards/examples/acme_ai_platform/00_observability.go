// SPDX-License-Identifier: AGPL-3.0-only

// ObservabilityHub — Acme AI Observability Hub (acme-ai-platform blueprint).
//
// This is the cross-signal mission-control dashboard: one entry point that summarises every tier
// (Gateway, Models, Agents, Evals, Traces, Frontend, GenAI, Pipeline) via stat/timeseries headline
// panels, with each tier on its own tab linking to the dedicated drill-in dashboard.
//
// Scope discipline (load-bearing — wrong scope = empty panel):
//   - kpi-* / att-* KPI/attention panels: portkey_api_* + langsmith_eval_* are SUBSTRATE-scoped
//     (no blueprint label) → use IntSel / lsSel.
//   - h-traces, conf-split, tr-rate: traces_spanmetrics_* are metrics-generator-derived (no blueprint
//     label) → hand-written selectors scoped by deployment_environment / service / span_name.
//     CONFIG-GATED: these panels only populate once the blueprint is added to span_metrics_blueprints
//     via POST /control/spanmetrics.
//   - h-genai, ga-tok: gen_ai_client_* are APP-scoped (blueprint+deployment_environment+service) →
//     appSel().
//   - aws_bedrock_* and aws_bedrock_agentcore_*: SUBSTRATE CW metric-stream (no blueprint label) →
//     plain label matchers (no blueprint/scenario filter; consistent with 03_bedrock.go).
//   - up{job="integrations/alloy"}: substrate; scenario label absent on synthkit emit → plain IntSel.
//   - fe-vitals: Loki {service_name="acme-frontend",kind="event"} — substrate stream (no blueprint
//     stream label on Faro RUM logs). Consistent with predecessor which uses service_name, not scenario.
//   - gt-clean / gt-req: acme_content_leak_test → acme.MetricContentLeakTest (RENAME);
//     acme_poller_last_success_timestamp_seconds → acme.MetricPollerLastOK (RENAME).
//     Both are SUBSTRATE-scoped (no blueprint label) → use IntSel.
//   - pl-stale: acme_poller_last_success_timestamp_seconds → acme.MetricPollerLastOK (RENAME);
//     substrate-scoped → use IntSel, scope by env (no blueprint label).
//
// Predecessor tab list (9 tabs): Overview · Gateway · Models · Agents · Evals · Traces · Frontend ·
// GenAI · Request Correlation · Pipeline.
//
// The Request Correlation tab here shows only stat headline panels that link to the dedicated
// Request Correlation dashboard (09_request_correlation.go).
package acme_ai_platform

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// ObservabilityHub is the Acme AI Observability Hub (mission control) for the acme-ai-platform blueprint.
// uid: acme-ws1-observability. Nine tabs reproducing the predecessor's 00-observability layout.
func ObservabilityHub(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws1-observability", "Acme AI — Observability Hub")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ───────────────────────────────────────────────────────────────────────────────
	// scenario: ConstVar — the "scenario" concept on the real Acme AI stack; synthkit emits blueprint=.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform"))
	// env: multi-select filter for substrate families (IntSel / lsSel).
	d.Builder.CustomVariable(dashboard.EnvVar(m))
	// cluster: substrate k8s cluster filter for EKS Health row.
	// kube_node_info is substrate-scoped (no blueprint label) — label_values populates from the
	// k8s-monitoring scrape. Multi-select with All = ".*".
	d.Builder.QueryVariable(dashboard.LabelValuesVar("cluster", "Cluster",
		`label_values(kube_node_info, cluster)`))

	// ── Tab 1: Overview ─────────────────────────────────────────────────────────────────────────
	// Row: Platform KPIs (6 stat panels)

	// portkey_api_requests_total is substrate-scoped (no blueprint label). Drop scenario= filter.
	dashboard.AddPanel(&d, "kpi-req", dashboard.StatTile("Requests (window)", "short",
		dashboard.PromTarget(
			"sum(portkey_api_requests_total"+intSel("")+")",
			"Requests (window)")))

	// portkey_api_error_rate is substrate-scoped (no blueprint label). Drop scenario= filter.
	dashboard.AddPanel(&d, "kpi-err", dashboard.StatTile("Error rate", "percent",
		dashboard.PromTarget(
			"100*max(portkey_api_error_rate"+intSel("")+")",
			"Error rate"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 2, Color: "yellow"}, dashboard.Threshold{Value: 5, Color: "red"}))

	// portkey_api_latency_seconds{quantile="0.9"} is substrate-scoped.
	dashboard.AddPanel(&d, "kpi-lat", dashboard.StatTile("p90 latency", "ms",
		dashboard.PromTarget(
			`1000*max(portkey_api_latency_seconds`+intSel(`quantile="0.9"`)+`)`,
			"p90 latency"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1000, Color: "yellow"}, dashboard.Threshold{Value: 3000, Color: "red"}))

	// portkey_api_cost_usd is substrate-scoped.
	dashboard.AddPanel(&d, "kpi-spend", dashboard.StatTile("Spend (window)", "currencyUSD",
		dashboard.PromTarget(
			"sum(portkey_api_cost_usd"+intSel("")+")",
			"Spend (window)")))

	// langsmith_eval_faithfulness_ratio is substrate-scoped (acme_ai_platform: exclude -gw projects).
	dashboard.AddPanel(&d, "kpi-faith", dashboard.StatTile("Min faithfulness", "percentunit",
		dashboard.PromTarget(
			"min(langsmith_eval_faithfulness_ratio"+lsSel("")+")",
			"Min faithfulness"),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 0.7, Color: "yellow"}, dashboard.Threshold{Value: 0.85, Color: "green"}))

	// portkey_api_cache_hit_rate is substrate-scoped.
	dashboard.AddPanel(&d, "kpi-cache", dashboard.StatTile("Cache-hit rate", "percent",
		dashboard.PromTarget(
			"100*avg(portkey_api_cache_hit_rate"+intSel("")+")",
			"Cache-hit rate"),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 20, Color: "yellow"}, dashboard.Threshold{Value: 40, Color: "green"}))

	// Row: Needs attention (conditional — panels only show data when condition is breached)

	// Faithfulness below gate (< 0.85): substrate-scoped. Red when value returned (condition fires).
	dashboard.AddPanel(&d, "att-faith", dashboard.StatTile("Faithfulness below gate", "percentunit",
		dashboard.PromTarget(
			"min(langsmith_eval_faithfulness_ratio"+lsSel("")+") < 0.85",
			"Faithfulness below gate"),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 0.85, Color: "green"}))

	// Content leak detected: shows the live value; thresholds colour healthy (0→green) vs firing (≥1→red).
	// Dropping the ">0" comparison so Grafana always has a value to render instead of empty-vector "No data".
	// acme_content_leak_test → acme.MetricContentLeakTest (RENAME: de-Rochification SK-38).
	// Substrate-scoped: the poller emits no blueprint label; scope by env only via IntSel.
	// real Acme AI stack: acme_content_leak_test
	dashboard.AddPanel(&d, "att-leak", dashboard.StatTile("Content leak detected", "short",
		dashboard.PromTarget(
			"max("+acme.MetricContentLeakTest+intSel("")+")",
			"Content leak detected"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "red"}))

	// Gateway error spike: shows the live error-rate %; turns red ≥5%.
	// portkey_api_error_rate is a 0–1 ratio; *100 converts to percent (unit="percent").
	// Dropping the ">0.05" comparison so Grafana always has a value to render instead of empty-vector
	// "No data". Threshold updated from 0.05 to 5 to match the *100 percent scale.
	// Substrate-scoped. Red when spike fires.
	dashboard.AddPanel(&d, "att-err", dashboard.StatTile("Gateway error spike", "percent",
		dashboard.PromTarget(
			"100*max(portkey_api_error_rate"+intSel("")+")",
			"Gateway error spike"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 5, Color: "red"}))

	// Row: Tier health tiles — one stat per tier (click-to-drill), substrate or mixed scope.

	// h-gw: portkey_api_requests_total — substrate.
	dashboard.AddPanel(&d, "h-gw", dashboard.StatTile("Gateway", "short",
		dashboard.PromTarget(
			"sum(portkey_api_requests_total"+intSel("")+")",
			"Gateway")))

	// h-models: aws_bedrock_invocations_sum — substrate CW metric-stream (no blueprint label).
	dashboard.AddPanel(&d, "h-models", dashboard.StatTile("Models", "reqps",
		dashboard.PromTarget(
			"sum(aws_bedrock_invocations_sum)/60",
			"Models")))

	// h-agents: aws_bedrock_agentcore_session_count_sum — substrate CW (no blueprint label).
	dashboard.AddPanel(&d, "h-agents", dashboard.StatTile("Agents", "short",
		dashboard.PromTarget(
			"sum(aws_bedrock_agentcore_session_count_sum)/60",
			"Agents")))

	// h-evals: langsmith_eval_faithfulness_ratio — substrate (acme_ai_platform: exclude -gw).
	dashboard.AddPanel(&d, "h-evals", dashboard.StatTile("Evals", "percentunit",
		dashboard.PromTarget(
			"min(langsmith_eval_faithfulness_ratio"+lsSel("")+")",
			"Evals"),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 0.7, Color: "yellow"}, dashboard.Threshold{Value: 0.85, Color: "green"}))

	// h-traces: traces_spanmetrics_calls_total — metrics-generator-derived (no blueprint label).
	// CONFIG-GATED: requires blueprint in span_metrics_blueprints. Scoped by service (all services).
	dashboard.AddPanel(&d, "h-traces", dashboard.StatTile("Traces", "reqps",
		dashboard.PromTarget(
			`sum(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"$env"}[$__rate_interval]))`,
			"Traces")))

	// h-frontend: traces_spanmetrics_calls_total scoped to acme-frontend service.
	// CONFIG-GATED: same opt-in requirement as h-traces.
	dashboard.AddPanel(&d, "h-frontend", dashboard.StatTile("Frontend", "reqps",
		dashboard.PromTarget(
			`sum(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"$env",service="`+acme.SvcFrontend+`"}[$__rate_interval]))`,
			"Frontend")))

	// h-genai: gen_ai_client_token_usage_sum — APP-scoped (blueprint+deployment_environment+service).
	dashboard.AddPanel(&d, "h-genai", dashboard.StatTile("GenAI", "short",
		dashboard.PromTarget(
			"sum(rate(gen_ai_client_token_usage_sum"+appSel("")+"[$__rate_interval]))",
			"GenAI")))

	// h-pipe: up{job="integrations/alloy"} — substrate (no blueprint label; cluster-scoped).
	// up carries no env label (labels: cluster, k8s_cluster_name, pipeline, namespace, instance,
	// asserts_env, job) — scope by k8s_cluster_name instead of env.
	// Counts distinct pipeline label values that are currently UP (== 1).
	dashboard.AddPanel(&d, "h-pipe", dashboard.StatTile("Pipeline", "short",
		dashboard.PromTarget(
			`count(count by (pipeline)(up{job="integrations/alloy",k8s_cluster_name=~"acme-eks-.*"}==1))`,
			"Pipeline")))

	// Row: Confidential vs General traffic split.
	// traces_spanmetrics_calls_total — metrics-generator-derived (no blueprint label).
	// CONFIG-GATED: requires span_metrics_blueprints opt-in.
	dashboard.AddPanel(&d, "conf-split", dashboard.TimeseriesPanel("App request rate — Confidential vs General", "reqps",
		dashboard.PromTarget(
			`sum(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"(PRD|TST1|TST2|TRN)"}[$__rate_interval]))`,
			"Confidential (PRD/TST1/TST2/TRN)").RefId("A"),
		dashboard.PromTarget(
			`sum(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"(DEV1|DEV2|BVE)"}[$__rate_interval]))`,
			"General (DEV1/DEV2/BVE)").RefId("B")))

	// Row: EKS Health at-a-glance (5 compact stat tiles, substrate-scoped, no blueprint label).
	// Signals sourced from signals/k8s.md. Scoped by cluster=~"$cluster".

	// ek-nodes: total node count across selected clusters.
	dashboard.AddPanel(&d, "ek-nodes", dashboard.StatTile("Nodes", "short",
		dashboard.PromTarget(
			`count(count by (cluster,node) (kube_node_info{cluster=~"$cluster"}))`,
			"Nodes")))

	// ek-notready: nodes in NotReady state. Red >0; "or vector(0)" keeps it data-present when healthy.
	dashboard.AddPanel(&d, "ek-notready", dashboard.StatTile("Nodes NotReady", "short",
		dashboard.PromTarget(
			`count(kube_node_status_condition{cluster=~"$cluster",condition="Ready",status="false"}) or vector(0)`,
			"Nodes NotReady"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "red"}))

	// ek-pods: pod-level not-ready count. Red >0.
	dashboard.AddPanel(&d, "ek-pods", dashboard.StatTile("Pods not Ready", "short",
		dashboard.PromTarget(
			`sum(kube_pod_status_ready{cluster=~"$cluster",condition="false"}) or vector(0)`,
			"Pods not Ready"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "red"}))

	// ek-restarts: container restart rate (restarts/s) across selected clusters.
	dashboard.AddPanel(&d, "ek-restarts", dashboard.StatTile("Container restarts/s", "short",
		dashboard.PromTarget(
			`sum(rate(kube_pod_container_status_restarts_total{cluster=~"$cluster"}[$__rate_interval]))`,
			"Container restarts/s")))

	// ek-5xx: EKS API server 5xx errors from CloudWatch metric stream (GAUGE — never rate()).
	// aws_eks_apiserver_request_total_5_xx_sum is substrate CW metric-stream (no blueprint label).
	// No $account var on this dashboard — use unfiltered aggregate; red >0.
	dashboard.AddPanel(&d, "ek-5xx", dashboard.StatTile("EKS apiserver 5xx", "short",
		dashboard.PromTarget(
			`sum(aws_eks_apiserver_request_total_5_xx_sum) or vector(0)`,
			"EKS apiserver 5xx"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "red"}))

	// ── Tab 2: Gateway ─────────────────────────────────────────────────────────────────────────
	// Portkey headline stats + spend timeseries + text drill-in link.

	// gw-req: portkey_api_requests_total — substrate.
	dashboard.AddPanel(&d, "gw-req", dashboard.StatTile("Requests (window)", "short",
		dashboard.PromTarget(
			"sum(portkey_api_requests_total"+intSel("")+")",
			"Requests (window)")))

	// gw-err: portkey_api_error_rate — substrate.
	dashboard.AddPanel(&d, "gw-err", dashboard.StatTile("Error rate", "percent",
		dashboard.PromTarget(
			"100*max(portkey_api_error_rate"+intSel("")+")",
			"Error rate"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 2, Color: "yellow"}, dashboard.Threshold{Value: 5, Color: "red"}))

	// gw-cost: portkey_api_cost_usd by use case — substrate.
	dashboard.AddPanel(&d, "gw-cost", dashboard.TimeseriesPanel("Spend by use-case", "currencyUSD",
		dashboard.PromTarget(
			"sum by (metadata_use_case)(portkey_api_cost_usd"+intSel("")+")",
			"{{metadata_use_case}}")))

	// gw-open: text panel with link to full Gateway dashboard.
	dashboard.AddPanel(&d, "gw-open", dashboard.TextPanel("Portkey LLM Gateway",
		"### Portkey LLM Gateway\n\n**[Open the full Portkey LLM Gateway dashboard →](/d/acme-ws1-portkey)**\n\nHeadline signals only on this tab — drill in for repeat-rows, conditional alerts, traces and tables."))

	// ── Tab 3: Models ──────────────────────────────────────────────────────────────────────────
	// AWS Bedrock headline: invocations/s stat + invocations-by-model timeseries + text link.
	// aws_bedrock_* are substrate CW metric-stream (no blueprint label); consistent with 03_bedrock.go.

	// md-invoc: aws_bedrock_invocations_sum — substrate CW gauge (/60 → per-second).
	dashboard.AddPanel(&d, "md-invoc", dashboard.StatTile("Invocations/s", "reqps",
		dashboard.PromTarget(
			"sum(aws_bedrock_invocations_sum)/60",
			"Invocations/s")))

	// md-by: invocations by model — substrate CW gauge.
	dashboard.AddPanel(&d, "md-by", dashboard.TimeseriesPanel("Invocations by model", "reqps",
		dashboard.PromTarget(
			"sum by (dimension_ModelId)(aws_bedrock_invocations_sum)/60",
			"{{dimension_ModelId}}")))

	// md-open: text panel with link to full Bedrock dashboard.
	dashboard.AddPanel(&d, "md-open", dashboard.TextPanel("Bedrock / Model layer",
		"### Bedrock / Model layer\n\n**[Open the full Bedrock / Model layer dashboard →](/d/acme-ws1-bedrock)**\n\nHeadline signals only on this tab — drill in for repeat-rows, conditional alerts, traces and tables."))

	// ── Tab 4: Agents ──────────────────────────────────────────────────────────────────────────
	// AgentCore: headline text + sessions-by-account timeseries.
	// aws_bedrock_agentcore_* are substrate CW metric-stream (no blueprint label).

	// ag-open: text panel with link to full AgentCore dashboard.
	dashboard.AddPanel(&d, "ag-open", dashboard.TextPanel("AgentCore Runtime",
		"### AgentCore Runtime\n\n**[Open the full AgentCore Runtime dashboard →](/d/acme-ws1-agentcore)**\n\nHeadline signals only on this tab — drill in for repeat-rows, conditional alerts, traces and tables."))

	// ag-sess: aws_bedrock_agentcore_session_count_sum by account — substrate CW gauge.
	dashboard.AddPanel(&d, "ag-sess", dashboard.TimeseriesPanel("AgentCore sessions/s by account", "short",
		dashboard.PromTarget(
			"sum by (account_id)(aws_bedrock_agentcore_session_count_sum)/60",
			"{{account_id}}")))

	// ── Tab 5: Evals ───────────────────────────────────────────────────────────────────────────
	// LangSmith eval gates timeseries + text link.
	// langsmith_eval_* are substrate-scoped (acme_ai_platform: exclude -gw projects).

	// ev-gates: faithfulness/completeness/env_consistency ratios — substrate (acme_ai_platform lsSel).
	dashboard.AddPanel(&d, "ev-gates", dashboard.TimeseriesPanel("Eval gates (avg)", "percentunit",
		dashboard.PromTarget(
			"avg(langsmith_eval_faithfulness_ratio"+lsSel("")+")",
			"faithfulness").RefId("A"),
		dashboard.PromTarget(
			"avg(langsmith_eval_completeness_ratio"+lsSel("")+")",
			"completeness").RefId("B"),
		dashboard.PromTarget(
			"avg(langsmith_eval_env_consistency_ratio"+lsSel("")+")",
			"env_consistency").RefId("C")))

	// ev-open: text panel with link to full LangSmith dashboard.
	dashboard.AddPanel(&d, "ev-open", dashboard.TextPanel("LangSmith Evals & Quality",
		"### LangSmith Evals & Quality\n\n**[Open the full LangSmith Evals & Quality dashboard →](/d/acme-ws1-langsmith)**\n\nHeadline signals only on this tab — drill in for repeat-rows, conditional alerts, traces and tables."))

	// ── Tab 6: Traces ──────────────────────────────────────────────────────────────────────────
	// traces_spanmetrics_calls_total — metrics-generator-derived (no blueprint label).
	// CONFIG-GATED: requires span_metrics_blueprints opt-in.
	// tr-rate: span rate by span_name.
	dashboard.AddPanel(&d, "tr-rate", dashboard.TimeseriesPanel("Agent span rate by span_name", "reqps",
		dashboard.PromTarget(
			`sum by (span_name)(rate(traces_spanmetrics_calls_total{deployment_environment_name=~"$env"}[$__rate_interval]))`,
			"{{span_name}}")))

	// tr-open: text panel with link to full LangGraph dashboard.
	dashboard.AddPanel(&d, "tr-open", dashboard.TextPanel("Agentic Workflow (LangGraph)",
		"### Agentic Workflow (LangGraph)\n\n**[Open the full Agentic Workflow (LangGraph) dashboard →](/d/acme-ws1-langgraph)**\n\nHeadline signals only on this tab — drill in for repeat-rows, conditional alerts, traces and tables."))

	// ── Tab 7: Frontend ────────────────────────────────────────────────────────────────────────
	// fe-vitals: Faro RUM logs — event rate by event_name. Substrate Loki stream (no blueprint
	// stream label on Faro RUM logs; predecessor uses service_name, not scenario).
	dashboard.AddPanel(&d, "fe-vitals", dashboard.TimeseriesPanel("RUM event rate by event_name", "short",
		dashboard.LokiTarget(
			`sum by (event_name)(count_over_time({service_name="`+acme.SvcFrontend+`",kind="event"}[$__rate_interval]))`,
			"{{event_name}}")))

	// fe-open: text panel with link to full APM+RUM dashboard.
	dashboard.AddPanel(&d, "fe-open", dashboard.TextPanel("App APM + RUM",
		"### App APM + RUM\n\n**[Open the full App APM + RUM dashboard →](/d/acme-ws1-apm-rum)**\n\nHeadline signals only on this tab — drill in for repeat-rows, conditional alerts, traces and tables."))

	// ── Tab 8: GenAI ───────────────────────────────────────────────────────────────────────────
	// gen_ai_client_token_usage_sum — APP-scoped (blueprint+deployment_environment+service).
	// ga-tok: token throughput by token type.
	dashboard.AddPanel(&d, "ga-tok", dashboard.TimeseriesPanel("Token throughput by type", "short",
		dashboard.PromTarget(
			"sum by (gen_ai_token_type)(rate(gen_ai_client_token_usage_sum"+appSel("")+"[$__rate_interval]))",
			"{{gen_ai_token_type}}")))

	// ga-open: text panel with link to full GenAI Semconv dashboard.
	dashboard.AddPanel(&d, "ga-open", dashboard.TextPanel("GenAI Semconv (Sigil)",
		"### GenAI Semconv (Sigil)\n\n**[Open the full GenAI Semconv (Sigil) dashboard →](/d/acme-ws1-sigil)**\n\nHeadline signals only on this tab — drill in for repeat-rows, conditional alerts, traces and tables."))

	// ── Tab 9: Request Correlation ───────────────────────────────────────────────────────────────────
	// Headline: content-seal stat + correlated-requests stat + text link.
	// gt-clean: acme_content_leak_test → acme.MetricContentLeakTest (RENAME).
	// Substrate-scoped: the poller emits no blueprint label; scope by env only via IntSel.
	// real Acme AI stack: acme_content_leak_test
	dashboard.AddPanel(&d, "gt-clean", dashboard.StatTile("Content sealed (0 = CLEAN)", "short",
		dashboard.PromTarget(
			"max("+acme.MetricContentLeakTest+intSel("")+")",
			"Content sealed"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "red"}))

	// gt-req: correlated requests count — portkey_api_requests_total substrate.
	dashboard.AddPanel(&d, "gt-req", dashboard.StatTile("Correlated requests (window)", "short",
		dashboard.PromTarget(
			"sum(portkey_api_requests_total"+intSel("")+")",
			"Correlated requests (window)")))

	// gt-open: text panel with link to full Request Correlation dashboard.
	dashboard.AddPanel(&d, "gt-open", dashboard.TextPanel("Request Correlation",
		"### Request Correlation\n\n**[Open the full Request Correlation dashboard →](/d/acme-ws1-request-correlation)**\n\nHeadline signals only on this tab — drill in for repeat-rows, conditional alerts, traces and tables."))

	// ── Tab 10: Pipeline ───────────────────────────────────────────────────────────────────────
	// Pipeline health: pipelines-UP stat + poller staleness timeseries + text link.

	// pl-up: count of distinct Alloy pipelines UP. up{job="integrations/alloy"} — substrate.
	// up carries no env label — scope by k8s_cluster_name instead of env.
	dashboard.AddPanel(&d, "pl-up", dashboard.StatTile("Pipelines UP", "short",
		dashboard.PromTarget(
			`count(count by (pipeline)(up{job="integrations/alloy",k8s_cluster_name=~"acme-eks-.*"}==1))`,
			"Pipelines UP"),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 1, Color: "yellow"}, dashboard.Threshold{Value: 3, Color: "green"}))

	// pl-stale: poller staleness by API (seconds since last successful poll).
	// acme_poller_last_success_timestamp_seconds → acme.MetricPollerLastOK (RENAME).
	// Substrate-scoped (no blueprint label); scope by env only via IntSel.
	// real Acme AI stack: acme_poller_last_success_timestamp_seconds
	dashboard.AddPanel(&d, "pl-stale", dashboard.TimeseriesPanel("Poller staleness by API (s)", "s",
		dashboard.PromTarget(
			"time() - "+acme.MetricPollerLastOK+intSel(""),
			"{{api}}")))

	// pl-open: text panel with link to full Pipeline Health dashboard.
	dashboard.AddPanel(&d, "pl-open", dashboard.TextPanel("Pipeline & Alloy Health",
		"### Pipeline & Alloy Health\n\n**[Open the full Pipeline & Alloy Health dashboard →](/d/acme-ws1-pipeline-health)**\n\nHeadline signals only on this tab — drill in for repeat-rows, conditional alerts, traces and tables."))

	// ── Layout ──────────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("Overview",
			dashboard.Section("Platform KPIs",
				dashboard.Tile("kpi-req"), dashboard.Tile("kpi-err"), dashboard.Tile("kpi-lat"),
				dashboard.Tile("kpi-spend"), dashboard.Tile("kpi-faith"), dashboard.Tile("kpi-cache")),
			dashboard.ConditionalSection("Needs attention",
				dashboard.Stat("att-faith"), dashboard.Stat("att-leak"), dashboard.Stat("att-err")),
			dashboard.Section("Tier health",
				dashboard.Tile("h-gw"), dashboard.Tile("h-models"), dashboard.Tile("h-agents"),
				dashboard.Tile("h-evals"), dashboard.Tile("h-traces"), dashboard.Tile("h-frontend"),
				dashboard.Tile("h-genai"), dashboard.Tile("h-pipe")),
			dashboard.Section("Confidential vs General",
				dashboard.Full("conf-split")),
			dashboard.Section("EKS Health",
				dashboard.Tile("ek-nodes"), dashboard.Tile("ek-notready"), dashboard.Tile("ek-pods"),
				dashboard.Tile("ek-restarts"), dashboard.Tile("ek-5xx")),
		),
		dashboard.Tabbed("Gateway",
			dashboard.Section("Gateway KPIs",
				dashboard.Tile("gw-req"), dashboard.Tile("gw-err")),
			dashboard.Section("Spend & drill-in",
				dashboard.TwoThirds("gw-cost"), dashboard.Third("gw-open")),
		),
		dashboard.Tabbed("Models",
			dashboard.Section("Bedrock headline",
				dashboard.Tile("md-invoc")),
			dashboard.Section("Invocations & drill-in",
				dashboard.TwoThirds("md-by"), dashboard.Third("md-open")),
		),
		dashboard.Tabbed("Agents",
			dashboard.Section("AgentCore",
				dashboard.Third("ag-open"), dashboard.TwoThirds("ag-sess")),
		),
		dashboard.Tabbed("Evals",
			dashboard.Section("Eval gates & drill-in",
				dashboard.TwoThirds("ev-gates"), dashboard.Third("ev-open")),
		),
		dashboard.Tabbed("Traces",
			dashboard.Section("Span rate & drill-in",
				dashboard.TwoThirds("tr-rate"), dashboard.Third("tr-open")),
		),
		dashboard.Tabbed("Frontend",
			dashboard.Section("RUM events & drill-in",
				dashboard.TwoThirds("fe-vitals"), dashboard.Third("fe-open")),
		),
		dashboard.Tabbed("GenAI",
			dashboard.Section("Token throughput & drill-in",
				dashboard.TwoThirds("ga-tok"), dashboard.Third("ga-open")),
		),
		dashboard.Tabbed("Request Correlation",
			dashboard.Section("Correlation KPIs",
				dashboard.Stat("gt-clean"), dashboard.Stat("gt-req")),
			dashboard.Section("",
				dashboard.Full("gt-open")),
		),
		dashboard.Tabbed("Pipeline",
			dashboard.Section("Pipeline health",
				dashboard.Stat("pl-up")),
			dashboard.Section("Poller staleness & drill-in",
				dashboard.TwoThirds("pl-stale"), dashboard.Third("pl-open")),
		),
	)
	return d, nil
}
