// SPDX-License-Identifier: AGPL-3.0-only

// LangSmithEvals is the acme_ai_platform dashboard for LangSmith eval-derived metrics:
// gate health, drift, retrieval quality, per-project scores, run-level evidence (Infinity tables),
// and AAEF executive KPIs. Ported from predecessor 05-langsmith.json (2026-06-15).
//
// Metric scope: langsmith_eval_* is SUBSTRATE-scoped (no blueprint label) — scope via
// acme.LangsmithSel(excludeGW=true, extra) for acme_ai_platform (project!~".+-gw" keeps acme_ai_platform projects only).
// Variable queries DROP the {scenario=} filter for the same reason.
//
// Infinity tables: wired as GET against ${infinity_base}<path>. The predecessor's
// /api/v1/runs/query panel uses POST (with a JSON body select list); our InfinityTarget is
// GET-only — noted in the report. The panel will return data only if the Infinity host
// serves the route as GET.
package acme_ai_platform

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// LangSmithEvals builds the Acme AI — LangSmith Evals dashboard (uid acme-ws1-langsmith).
// Four tabs mirror the predecessor's tab layout:
//
//   - Eval gates     — per-gate gauge + trend pairs (faithfulness, completeness, env consistency,
//     schema validity, passthrough exactness), breach indicators
//   - Quality KPIs   — stat panels for eval gate values, retrieval metrics, HITL + retry rates
//   - Trends         — time-series drift panels (faithfulness/completeness, retrieval, LLM-as-judge)
//   - Scorecard & runs — four Infinity tables (eval_scorecard, runs/query, sessions, aaef_kpis)
func LangSmithEvals(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws1-langsmith", "Acme AI — LangSmith Evals")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ────────────────────────────────────────────────────────────────────────────────

	// scenario const var (hidden) so AppSel references work consistently if mixed panels are added.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform"))

	// Infinity tables use RELATIVE paths — the Infinity datasource's base URL (the host FQDN,
	// served via tailscale serve) supplies the host. Do NOT embed an absolute base here.

	// env / project / use_case: langsmith_eval_* is substrate-scoped (no blueprint label) →
	// DROP the {scenario=} filter; keep project!~".+-gw" to isolate acme_ai_platform data.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("env", "Environment",
		`label_values(langsmith_eval_faithfulness_ratio{project!~".+-gw"}, env)`))
	d.Builder.QueryVariable(dashboard.LabelValuesVar("project", "Project",
		`label_values(langsmith_eval_faithfulness_ratio{project!~".+-gw"}, project)`))
	d.Builder.QueryVariable(dashboard.LabelValuesVar("use_case", "Use case",
		`label_values(langsmith_eval_faithfulness_ratio{project!~".+-gw"}, use_case)`))

	// ── TAB: Eval gates ──────────────────────────────────────────────────────────────────────────
	// Regression header row: breach indicator + breach count stat + judge score timeseries.
	dashboard.AddPanel(&d, "eg-regress", dashboard.StatTile(
		"⚠ Faithfulness below gate (active)", "short",
		dashboard.PromTarget(
			"min(langsmith_eval_faithfulness_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+" ) < 0.85",
			"⚠ Faithfulness below gate (active)"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 0.001, Color: "red"}))

	dashboard.AddPanel(&d, "eg-breach-count", dashboard.StatTile(
		"Projects breaching faithfulness gate", "short",
		dashboard.PromTarget(
			"count(langsmith_eval_faithfulness_ratio"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+" < 0.85) or vector(0)",
			"breaching"),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "yellow"}, dashboard.Threshold{Value: 3, Color: "red"}))

	dashboard.AddPanel(&d, "eg-judge-scores", dashboard.TimeseriesPanel(
		"LLM-as-judge eval scores by evaluator", "percentunit",
		dashboard.PromTarget(
			"avg by (evaluator, run_outcome) (langsmith_eval_score"+
				acme.LangsmithSel(true, `project=~"$project",run_outcome="success"`)+")",
			"{{evaluator}} / {{run_outcome}}")))

	// Faithfulness row: gauge + trend.
	dashboard.AddPanel(&d, "eg-g-faithfulness", dashboard.GaugePanel(
		"Faithfulness (gate ≥0.85)", "percentunit",
		dashboard.PromTarget(
			"min(langsmith_eval_faithfulness_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			"Faithfulness")))

	dashboard.AddPanel(&d, "eg-t-faithfulness", dashboard.TimeseriesPanel(
		"Faithfulness — trend (avg)", "percentunit",
		dashboard.PromTarget(
			"avg(langsmith_eval_faithfulness_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			"Faithfulness")))

	// Completeness row.
	dashboard.AddPanel(&d, "eg-g-completeness", dashboard.GaugePanel(
		"Completeness (gate ≥0.995)", "percentunit",
		dashboard.PromTarget(
			"min(langsmith_eval_completeness_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			"Completeness")))

	dashboard.AddPanel(&d, "eg-t-completeness", dashboard.TimeseriesPanel(
		"Completeness — trend (avg)", "percentunit",
		dashboard.PromTarget(
			"avg(langsmith_eval_completeness_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			"Completeness")))

	// Env consistency row.
	dashboard.AddPanel(&d, "eg-g-envcons", dashboard.GaugePanel(
		"Env consistency (gate ≥1.0)", "percentunit",
		dashboard.PromTarget(
			"min(langsmith_eval_env_consistency_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			"Env consistency")))

	dashboard.AddPanel(&d, "eg-t-envcons", dashboard.TimeseriesPanel(
		"Env consistency — trend (avg)", "percentunit",
		dashboard.PromTarget(
			"avg(langsmith_eval_env_consistency_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			"Env consistency")))

	// Schema validity row.
	dashboard.AddPanel(&d, "eg-g-schema", dashboard.GaugePanel(
		"Schema validity (gate ≥0.995)", "percentunit",
		dashboard.PromTarget(
			"min(langsmith_eval_schema_validity_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			"Schema validity")))

	dashboard.AddPanel(&d, "eg-t-schema", dashboard.TimeseriesPanel(
		"Schema validity — trend (avg)", "percentunit",
		dashboard.PromTarget(
			"avg(langsmith_eval_schema_validity_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			"Schema validity")))

	// Passthrough exactness row.
	dashboard.AddPanel(&d, "eg-g-passthrough", dashboard.GaugePanel(
		"Passthrough exactness (gate ≥0.999)", "percentunit",
		dashboard.PromTarget(
			"min(langsmith_eval_passthrough_exactness_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			"Passthrough exactness")))

	dashboard.AddPanel(&d, "eg-t-passthrough", dashboard.TimeseriesPanel(
		"Passthrough exactness — trend (avg)", "percentunit",
		dashboard.PromTarget(
			"avg(langsmith_eval_passthrough_exactness_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			"Passthrough exactness")))

	// ── TAB: Quality KPIs ────────────────────────────────────────────────────────────────────────
	// Confidential gate stats (min across all selected dims, matching predecessor panel-1..6).
	// High-is-good ratios: red base → yellow → green (thresholds match each gate floor).
	dashboard.AddPanel(&d, "kpi-faithfulness", dashboard.StatTile(
		"Faithfulness (≥0.85)", "percentunit",
		dashboard.PromTarget(
			"min(langsmith_eval_faithfulness_ratio"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			""),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 0.75, Color: "yellow"}, dashboard.Threshold{Value: 0.85, Color: "green"}))

	dashboard.AddPanel(&d, "kpi-completeness", dashboard.StatTile(
		"Completeness (≥0.995)", "percentunit",
		dashboard.PromTarget(
			"min(langsmith_eval_completeness_ratio"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			""),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 0.98, Color: "yellow"}, dashboard.Threshold{Value: 0.995, Color: "green"}))

	dashboard.AddPanel(&d, "kpi-envcons", dashboard.StatTile(
		"Env consistency (==1.0)", "percentunit",
		dashboard.PromTarget(
			"min(langsmith_eval_env_consistency_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			""),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 0.99, Color: "yellow"}, dashboard.Threshold{Value: 1.0, Color: "green"}))

	dashboard.AddPanel(&d, "kpi-schema", dashboard.StatTile(
		"Schema validity (≥0.995)", "percentunit",
		dashboard.PromTarget(
			"min(langsmith_eval_schema_validity_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			""),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 0.98, Color: "yellow"}, dashboard.Threshold{Value: 0.995, Color: "green"}))

	dashboard.AddPanel(&d, "kpi-passthrough", dashboard.StatTile(
		"Passthrough exactness (≥0.999)", "percentunit",
		dashboard.PromTarget(
			"min(langsmith_eval_passthrough_exactness_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			""),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 0.99, Color: "yellow"}, dashboard.Threshold{Value: 0.999, Color: "green"}))

	// Breach count: low-is-good → green=0, yellow=1, red=3+.
	dashboard.AddPanel(&d, "kpi-breach", dashboard.StatTile(
		"Projects breaching faithfulness gate", "short",
		dashboard.PromTarget(
			"count(langsmith_eval_faithfulness_ratio"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+" < 0.85) or vector(0)",
			""),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 1, Color: "yellow"}, dashboard.Threshold{Value: 3, Color: "red"}))

	// Retrieval & HITL stats (predecessor panel-20..25).
	// High-is-good quality ratios: red → yellow → green.
	dashboard.AddPanel(&d, "kpi-recall", dashboard.StatTile(
		"Recall@K (mean)", "percentunit",
		dashboard.PromTarget(
			"avg(langsmith_eval_recall_at_k"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			""),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 0.5, Color: "yellow"}, dashboard.Threshold{Value: 0.75, Color: "green"}))

	dashboard.AddPanel(&d, "kpi-precision", dashboard.StatTile(
		"Precision@K (mean)", "percentunit",
		dashboard.PromTarget(
			"avg(langsmith_eval_precision_at_k"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			""),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 0.5, Color: "yellow"}, dashboard.Threshold{Value: 0.75, Color: "green"}))

	dashboard.AddPanel(&d, "kpi-mrr", dashboard.StatTile(
		"MRR (mean)", "percentunit",
		dashboard.PromTarget(
			"avg(langsmith_eval_mrr"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			""),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 0.4, Color: "yellow"}, dashboard.Threshold{Value: 0.65, Color: "green"}))

	dashboard.AddPanel(&d, "kpi-ndcg", dashboard.StatTile(
		"nDCG (mean)", "percentunit",
		dashboard.PromTarget(
			"avg(langsmith_eval_ndcg"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			""),
		dashboard.Threshold{Value: 0, Color: "red"}, dashboard.Threshold{Value: 0.5, Color: "yellow"}, dashboard.Threshold{Value: 0.75, Color: "green"}))

	// HITL rate: low-is-good (high human-in-loop = quality concern) → green → yellow → red.
	dashboard.AddPanel(&d, "kpi-hitl", dashboard.StatTile(
		"HITL rate (mean)", "percentunit",
		dashboard.PromTarget(
			"avg(langsmith_eval_hitl_rate"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			""),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 0.1, Color: "yellow"}, dashboard.Threshold{Value: 0.25, Color: "red"}))

	// Retry rate: low-is-good → green → yellow → red.
	dashboard.AddPanel(&d, "kpi-retry", dashboard.StatTile(
		"Retry rate (mean)", "percentunit",
		dashboard.PromTarget(
			"avg(langsmith_eval_retry_rate"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			""),
		dashboard.Threshold{Value: 0, Color: "green"}, dashboard.Threshold{Value: 0.05, Color: "yellow"}, dashboard.Threshold{Value: 0.15, Color: "red"}))

	// ── TAB: Trends ──────────────────────────────────────────────────────────────────────────────
	// Faithfulness drift (top 10 projects by variance) and completeness+schema composite.
	dashboard.AddPanel(&d, "tr-faith-proj", dashboard.TimeseriesPanel(
		"Faithfulness per project (top 10 by variance — threshold 0.85)", "percentunit",
		dashboard.PromTarget(
			"topk(10, avg by (project) (langsmith_eval_faithfulness_ratio"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+"))",
			"{{project}}")))

	dashboard.AddPanel(&d, "tr-completeness-drift", dashboard.TimeseriesPanel(
		"Completeness & schema validity drift", "percentunit",
		dashboard.PromTarget(
			"avg(langsmith_eval_completeness_ratio"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			"completeness (avg, gate ≥0.995)").RefId("A"),
		dashboard.PromTarget(
			"avg(langsmith_eval_schema_validity_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			"schema validity (avg, gate ≥0.995)").RefId("B"),
		dashboard.PromTarget(
			"avg(langsmith_eval_passthrough_exactness_ratio"+
				acme.LangsmithSel(true, `project=~"$project"`)+")",
			"passthrough exactness (avg, gate ≥0.999)").RefId("C")))

	// Retrieval quality over time (all four metrics combined + by-k breakdown).
	dashboard.AddPanel(&d, "tr-retrieval", dashboard.TimeseriesPanel(
		"Retrieval quality over time (Recall@K / MRR / nDCG / Precision@K — aggregated)", "percentunit",
		dashboard.PromTarget(
			"avg(langsmith_eval_recall_at_k"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			"Recall@K").RefId("A"),
		dashboard.PromTarget(
			"avg(langsmith_eval_mrr"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			"MRR").RefId("B"),
		dashboard.PromTarget(
			"avg(langsmith_eval_ndcg"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			"nDCG").RefId("C"),
		dashboard.PromTarget(
			"avg(langsmith_eval_precision_at_k"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			"Precision@K").RefId("D")))

	dashboard.AddPanel(&d, "tr-recall-by-k", dashboard.TimeseriesPanel(
		"Retrieval quality by k value (Recall@K)", "percentunit",
		dashboard.PromTarget(
			"avg by (k) (langsmith_eval_recall_at_k"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			"Recall@{{k}}")))

	// LLM-as-judge scores + retry/fallback rates.
	dashboard.AddPanel(&d, "tr-judge", dashboard.TimeseriesPanel(
		"LLM-as-judge eval scores by evaluator", "percentunit",
		dashboard.PromTarget(
			"avg by (evaluator, run_outcome) (langsmith_eval_score"+
				acme.LangsmithSel(true, `project=~"$project",run_outcome="success"`)+")",
			"{{evaluator}} / {{run_outcome}}")))

	dashboard.AddPanel(&d, "tr-retry-fallback", dashboard.TimeseriesPanel(
		"Retry & fallback rates", "percentunit",
		dashboard.PromTarget(
			"avg by (use_case) (langsmith_eval_retry_rate"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			"retry · {{use_case}}").RefId("A"),
		dashboard.PromTarget(
			"avg by (use_case) (langsmith_eval_fallback_rate"+
				acme.LangsmithSel(true, `project=~"$project",use_case=~"$use_case"`)+")",
			"fallback · {{use_case}}").RefId("B")))

	// ── TAB: Scorecard & runs (Infinity tables) ───────────────────────────────────────────────────

	// Eval gate scorecard — /acme/eval_scorecard (GET).
	// Root selector "scorecard"; columns: project, env, use_case, value, op, threshold, gate, status, metric.
	dashboard.AddPanel(&d, "inf-scorecard", dashboard.InfinityTablePanel(
		"Eval gate scorecard — per project × gate (pass=\U0001f7e2 / fail=\U0001f534)",
		"A", "/acme/eval_scorecard", "synthkit (Infinity)", "scorecard",
		dashboard.Col("project", "Project", "string"),
		dashboard.Col("env", "Env", "string"),
		dashboard.Col("use_case", "Use Case", "string"),
		dashboard.Col("value", "Value", "number").WithUnit("percentunit").WithDecimals(3),
		dashboard.Col("op", "Op", "string"),
		dashboard.Col("threshold", "Threshold", "number").WithUnit("percentunit").WithDecimals(3),
		dashboard.Col("gate", "Gate", "string"),
		dashboard.Col("status", "Status", "string").WithColorMode("color-background"),
		dashboard.Col("metric", "Metric", "string"),
	))

	// Eval runs table — /api/v1/runs/query (POST with JSON select-list body).
	// Mirrors the predecessor panel and the eval-runs panel in 09_request_correlation.go.
	// Root selector "runs"; columns mirror the predecessor's dotted-path selectors.
	dashboard.AddPanel(&d, "inf-runs",
		dashboard.TablePanel(
			"Eval runs — run-level table (click otel_trace_id → Tempo, correlation_id → Loki)",
			dashboard.InfinityTargetPOST("A", "/api/v1/runs/query",
				"synthkit (Infinity)", "runs",
				`{"select":["id","name","run_type","status","start_time","total_tokens","total_cost","extra","feedback_stats"]}`,
				dashboard.Col("name", "name", "string"),
				dashboard.Col("run_type", "run_type", "string"),
				dashboard.Col("status", "status", "string"),
				dashboard.Col("start_time", "start_time", "string"),
				dashboard.Col("total_tokens", "total_tokens", "number").WithUnit("short"),
				dashboard.Col("extra.metadata.use_case", "use_case", "string"),
				dashboard.Col("feedback_stats.faithfulness.avg", "faithfulness", "number").WithUnit("percentunit").WithDecimals(3),
				dashboard.Col("feedback_stats.completeness.avg", "completeness", "number").WithUnit("percentunit").WithDecimals(3),
				dashboard.Col("extra.metadata.otel_trace_id", "otel_trace_id", "string"),
				dashboard.Col("extra.metadata.correlation_id", "correlation_id", "string"),
				dashboard.Col("extra.metadata.portkey_trace_id", "portkey_trace_id", "string"),
			)))

	// LangSmith projects / sessions table — /api/v1/sessions?include_stats=true (GET).
	// Root selector "sessions".
	dashboard.AddPanel(&d, "inf-sessions", dashboard.InfinityTablePanel(
		"LangSmith projects — runs / latency / error-rate / tokens / eval facets (per project)",
		"A", "/api/v1/sessions", "synthkit (Infinity)", "sessions",
		dashboard.Col("name", "project", "string"),
		dashboard.Col("stats.run_count", "runs", "number"),
		dashboard.Col("stats.latency_p50", "p50 ms", "number"),
		dashboard.Col("stats.latency_p99", "p99 ms", "number"),
		dashboard.Col("stats.error_rate", "error rate", "number").WithUnit("percentunit").WithDecimals(2).WithColorMode("color-background"),
		dashboard.Col("stats.total_tokens", "tokens", "number").WithUnit("short"),
		dashboard.Col("stats.total_cost", "cost $", "string"),
		dashboard.Col("stats.feedback_stats.faithfulness.avg", "faithfulness", "number").WithUnit("percentunit").WithDecimals(3),
	))

	// AAEF KPIs — /acme/aaef_kpis (GET).
	// Root selector "kpis"; columns: context, period, tue_score, mcr_score, spi_score, css_score.
	dashboard.AddPanel(&d, "inf-aaef-kpis", dashboard.InfinityTablePanel(
		"AAEF KPIs by context × period (TUE / MCR / SPI / CSS)",
		"A", "/acme/aaef_kpis", "synthkit (Infinity)", "kpis",
		dashboard.Col("context", "Context", "string"),
		dashboard.Col("period", "Period", "string"),
		dashboard.Col("tue_score", "TUE Score", "number").WithUnit("percentunit").WithDecimals(3).WithColorMode("color-text"),
		dashboard.Col("mcr_score", "MCR Score", "number").WithUnit("percentunit").WithDecimals(3).WithColorMode("color-text"),
		dashboard.Col("spi_score", "SPI Score", "number").WithUnit("percentunit").WithDecimals(3).WithColorMode("color-text"),
		dashboard.Col("css_score", "CSS Score", "number").WithUnit("percentunit").WithDecimals(3).WithColorMode("color-text"),
	))

	// ── Layout ───────────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		// ── Tab: Eval gates ──────────────────────────────────────────────────────────────────────
		// KPI breach tiles first, then one Section per gate (gauge Third + trend Half).
		dashboard.Tabbed("Eval gates",
			dashboard.Section("Gate breach indicators",
				dashboard.Tile("eg-regress"), dashboard.Tile("eg-breach-count"),
				dashboard.TwoThirds("eg-judge-scores")),
			dashboard.Section("Faithfulness (gate ≥0.85)",
				dashboard.Third("eg-g-faithfulness"), dashboard.Half("eg-t-faithfulness")),
			dashboard.Section("Completeness (gate ≥0.995)",
				dashboard.Third("eg-g-completeness"), dashboard.Half("eg-t-completeness")),
			dashboard.Section("Env consistency (gate ≥1.0)",
				dashboard.Third("eg-g-envcons"), dashboard.Half("eg-t-envcons")),
			dashboard.Section("Schema validity (gate ≥0.995)",
				dashboard.Third("eg-g-schema"), dashboard.Half("eg-t-schema")),
			dashboard.Section("Passthrough exactness (gate ≥0.999)",
				dashboard.Third("eg-g-passthrough"), dashboard.Half("eg-t-passthrough")),
		),
		// ── Tab: Quality KPIs ────────────────────────────────────────────────────────────────────
		// Two KPI tile strips: eval gate values then retrieval & HITL metrics.
		dashboard.Tabbed("Quality KPIs",
			dashboard.Section("Eval gate values (min across selected projects)",
				dashboard.Tile("kpi-faithfulness"), dashboard.Tile("kpi-completeness"),
				dashboard.Tile("kpi-envcons"), dashboard.Tile("kpi-schema"),
				dashboard.Tile("kpi-passthrough"), dashboard.Tile("kpi-breach")),
			dashboard.Section("Retrieval & HITL quality",
				dashboard.Tile("kpi-recall"), dashboard.Tile("kpi-precision"),
				dashboard.Tile("kpi-mrr"), dashboard.Tile("kpi-ndcg"),
				dashboard.Tile("kpi-hitl"), dashboard.Tile("kpi-retry")),
		),
		// ── Tab: Trends ───────────────────────────────────────────────────────────────────────────
		// Half-width pairs grouped into two sections.
		dashboard.Tabbed("Trends",
			dashboard.Section("Quality drift",
				dashboard.Half("tr-faith-proj"), dashboard.Half("tr-completeness-drift"),
				dashboard.Half("tr-retrieval"), dashboard.Half("tr-recall-by-k")),
			dashboard.Section("LLM-as-judge & retry",
				dashboard.Half("tr-judge"), dashboard.Half("tr-retry-fallback")),
		),
		// ── Tab: Scorecard & runs (Infinity) ─────────────────────────────────────────────────────
		// Full-width scorecard, then tall run-level table, then two half-width summary tables.
		dashboard.Tabbed("Scorecard & runs",
			dashboard.Section("Eval gate scorecard",
				dashboard.Tall("inf-scorecard")),
			dashboard.Section("Run-level evidence",
				dashboard.Tall("inf-runs")),
			dashboard.Section("Projects & AAEF KPIs",
				dashboard.Half("inf-sessions"), dashboard.Half("inf-aaef-kpis")),
		),
	)
	return d, nil
}
