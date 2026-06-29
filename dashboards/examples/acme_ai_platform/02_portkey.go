// SPDX-License-Identifier: AGPL-3.0-only

// PortkeyGateway is the acme_ai_platform dashboard for the Portkey LLM Gateway — the analytics
// API-pull lane. portkey_api_* metrics are SUBSTRATE-scoped gauges emitted by the in-cluster
// analytics poller (no blueprint label) → scope via intSel (env=~"$env"), never AppSel.
// Variables drop the {scenario=} filter for the same reason.
//
// Predecessor source: 02-portkey.json (acme-portkey). Ported 2026-06-16.
//
// KNOWN GAPS (panels wired faithfully; will be EMPTY until the fill lands):
//   - panel "Retries & fallbacks": llm_retries_total and llm_fallbacks_total are Loki
//     recording-rule-derived counters (loki.process pipeline, source=portkey logs). They are NOT
//     yet emitted by synthkit's promrw path. Panel is wired as RateExpr against IntSel; it will
//     populate once the recording-rule fill lands (see cantfind.md).
//
// portkey_api_latency_seconds (pre-computed quantile GAUGE with {quantile="0.5/0.9/0.99"} label)
// is EMITTED — latency panels populate. No fill pending.
//
// Infinity endpoints wired (all GET via ${infinity_base}):
//   - /v1/analytics/groups/metadata   (root: "data", by use_case)
//   - /v1/analytics/groups/ai-models  (root: "data", by model)
//   - /v1/configs                     (root: "providers", provider catalog)
//   - /v1/prompts                     (root: "data", prompt registry)
//
// Tab layout:
//
//	Overview | By use case | Attribution | Logs
//
// NOT reproducible with the current builder:
//   - "By use case" tab: the predecessor uses a row-level repeat (repeat by $use_case variable).
//     The Go builder now supports this via RepeatSection — the per-use-case stat row repeats
//     once per value of $use_case (mode=variable), scoping each clone to a single use_case.
//
// Previously not reproducible, now implemented:
//   - Fallback storm conditional row: implemented via ConditionalSection("Fallback storm") in the
//     "By use case" tab. The trigger panel queries portkey_api_rescued_requests_total > 5 and
//     portkey_api_error_rate > 0.05; Grafana hides the row when both return no data (healthy).
//   - Column-level unit overrides: Attribution tables use InfinityTablePanel with per-column
//     formatting (cost→currencyUSD, error_rate/cache_hit_rate→percentunit, latency→s, tokens/requests→short,
//     status→color-background).
package acme_ai_platform

import (
	"github.com/rknightion/synthkit/dashboard"
	acme "github.com/rknightion/synthkit/dashboards/examples/acme_ai"
)

// PortkeyGateway builds the Acme AI — Portkey Gateway dashboard
// (uid acme-ws1-portkey).
//
// portkey_api_* is the analytics-poller lane (substrate, env+workspace+use_case+ai_model scoped,
// all GAUGES) → IntSel + extra matchers (workspace/metadata_use_case/ai_model).
// Use GaugeExpr for GAUGE families; RateExpr only for the log-derived _total counters.
func PortkeyGateway(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws1-portkey", "Acme AI — Portkey Gateway")
	if err != nil {
		return dashboard.Dashboard{}, err
	}

	// ── Variables ────────────────────────────────────────────────────────────────────────────────

	// scenario: hidden const var — keeps AppSel-using panels coherent if mixed panels are added.
	d.Builder.ConstantVariable(dashboard.ConstVar("scenario", "acme_ai_platform"))

	// Infinity tables use RELATIVE paths — the Infinity datasource's base URL (the host FQDN,
	// served via tailscale serve) supplies the host. Do NOT embed an absolute base here.

	// env: substrate-scoped filter — IntSel emits {env=~"$env",...}; without this variable the
	// selector resolves to the literal string "$env" and every IntSel-based panel returns no data.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("env", "Environment",
		`label_values(portkey_api_requests_total, env)`))

	// portkey_api_* is substrate-scoped (no blueprint label) → DROP {scenario=} from label_values.
	// workspace / use_case / ai_model are all IntSel dimensions.
	d.Builder.QueryVariable(dashboard.LabelValuesVar("workspace", "Workspace",
		`label_values(portkey_api_requests_total, workspace)`))
	d.Builder.QueryVariable(dashboard.LabelValuesVar("use_case", "Use case",
		`label_values(portkey_api_requests_total, metadata_use_case)`))
	d.Builder.QueryVariable(dashboard.LabelValuesVar("ai_model", "Model",
		`label_values(portkey_api_requests_total, ai_model)`))

	// ── TAB: Overview ────────────────────────────────────────────────────────────────────────────

	// Data-contract text note (predecessor panel-100).
	dashboard.AddPanel(&d, "ov-contract", dashboard.TextPanel(
		"API-pull view — data contract",
		"**acme_ai_platform API-pull view** — Portkey telemetry arrives via the `acme-api-poller` (in-cluster). "+
			"Analytics gauges refresh on **~5-min boundaries** (values step; expected). "+
			"Logs export batch every **~15 min**; capped at **50k logs/job** per Portkey export API. "+
			"Percentiles (p50/p90/p99) are **pre-computed gauges** — no histogram_quantile, no heatmaps. "+
			"Gateway `/metrics` scrape is available in the acme_ai_platform_eval scenario. "+
			"Poller freshness shown below."))

	// KPI summary row (predecessor panel-1..6).
	// portkey_api_requests_total is a windowed-count GAUGE — do NOT rate() it.
	dashboard.AddPanel(&d, "ov-total-req", dashboard.StatTile(
		"Total requests (window)", "short",
		dashboard.PromTarget(
			"sum(portkey_api_requests_total"+
				acme.IntSel(`workspace=~"$workspace",metadata_use_case=~"$use_case"`)+
				")",
			"")))

	// portkey_api_error_rate GAUGE 0–1 (pre-computed by poller from analytics API).
	// Low-is-good: green base → yellow → red.
	dashboard.AddPanel(&d, "ov-error-rate", dashboard.StatTile(
		"Error rate", "percentunit",
		dashboard.PromTarget(
			"max(portkey_api_error_rate"+
				acme.IntSel(`workspace=~"$workspace",metadata_use_case=~"$use_case"`)+
				")",
			""),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 0.02, Color: "yellow"},
		dashboard.Threshold{Value: 0.05, Color: "red"}))

	// portkey_api_cache_hit_rate GAUGE 0–1.
	// High-is-good: red base → yellow → green.
	dashboard.AddPanel(&d, "ov-cache-hit", dashboard.StatTile(
		"Cache-hit rate", "percentunit",
		dashboard.PromTarget(
			"avg(portkey_api_cache_hit_rate"+
				acme.IntSel(`workspace=~"$workspace",metadata_use_case=~"$use_case"`)+
				")",
			""),
		dashboard.Threshold{Value: 0, Color: "red"},
		dashboard.Threshold{Value: 0.2, Color: "yellow"},
		dashboard.Threshold{Value: 0.5, Color: "green"}))

	// portkey_api_cost_usd GAUGE — current window total.
	dashboard.AddPanel(&d, "ov-cost", dashboard.StatTile(
		"Total cost (window, USD)", "currencyUSD",
		dashboard.PromTarget(
			"sum(portkey_api_cost_usd"+
				acme.IntSel(`workspace=~"$workspace",metadata_use_case=~"$use_case",ai_model=~"$ai_model"`)+
				")",
			"")))

	// portkey_api_tokens_total GAUGE — current window total.
	dashboard.AddPanel(&d, "ov-tokens", dashboard.StatTile(
		"Total tokens (window)", "short",
		dashboard.PromTarget(
			"sum(portkey_api_tokens_total"+
				acme.IntSel(`workspace=~"$workspace",metadata_use_case=~"$use_case",ai_model=~"$ai_model"`)+
				")",
			"")))

	// Analytics freshness: time() - max(poller_last_success_timestamp_seconds{api="portkey-analytics"}).
	// Uses acme.MetricPollerLastOK (de-Rochified synthkit name = poller_last_success_timestamp_seconds).
	// real Acme AI stack: acme_poller_last_success_timestamp_seconds
	// NOTE: the emitted api label value is "portkey-analytics" (hyphen) — matching reality, not "_".
	// Low-is-good (staleness in seconds): green → yellow → red.
	dashboard.AddPanel(&d, "ov-freshness", dashboard.StatTile(
		"Analytics freshness (staleness)", "s",
		dashboard.PromTarget(
			`time() - max(`+acme.MetricPollerLastOK+`{api="portkey-analytics"}`+`)`,
			""),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 300, Color: "yellow"},
		dashboard.Threshold{Value: 600, Color: "red"}))

	// Rescued requests (retry+fallback rescues per analytics window) — by use case, timeseries.
	// portkey_api_rescued_requests_total GAUGE.
	dashboard.AddPanel(&d, "ov-rescued", dashboard.TimeseriesPanel(
		"Rescued requests (retry/fallback visibility) — featured", "short",
		dashboard.PromTarget(
			dashboard.GaugeExpr("portkey_api_rescued_requests_total",
				acme.IntSel(`workspace=~"$workspace",metadata_use_case=~"$use_case"`),
				[]string{"metadata_use_case"}),
			"rescued · {{metadata_use_case}}")))

	// Requests by status class (portkey_api_requests_total GAUGE split by status_class).
	dashboard.AddPanel(&d, "ov-req-status", dashboard.TimeseriesPanel(
		"Requests by status class", "short",
		dashboard.PromTarget(
			dashboard.GaugeExpr("portkey_api_requests_total",
				acme.IntSel(`workspace=~"$workspace",metadata_use_case=~"$use_case"`),
				[]string{"status_class"}),
			"{{status_class}}")))

	// LLM latency pre-computed quantile GAUGEs.
	// portkey_api_latency_seconds GAUGE with quantile label (0.5 / 0.9 / 0.99) — emitted; panels populate.
	dashboard.AddPanel(&d, "ov-latency-quantiles", dashboard.TimeseriesPanel(
		"LLM latency (p50 / p90 / p99) — pre-computed quantiles", "s",
		dashboard.PromTarget(
			`max by (quantile) (portkey_api_latency_seconds`+
				acme.IntSel(`workspace=~"$workspace",quantile="0.5"`)+`)`,
			"p50").RefId("A"),
		dashboard.PromTarget(
			`max by (quantile) (portkey_api_latency_seconds`+
				acme.IntSel(`workspace=~"$workspace",quantile="0.9"`)+`)`,
			"p90").RefId("B"),
		dashboard.PromTarget(
			`max by (quantile) (portkey_api_latency_seconds`+
				acme.IntSel(`workspace=~"$workspace",quantile="0.99"`)+`)`,
			"p99").RefId("C")))

	// Error rate & cache-hit rate over time.
	dashboard.AddPanel(&d, "ov-err-cache-ts", dashboard.TimeseriesPanel(
		"Error rate & cache-hit rate over time", "percentunit",
		dashboard.PromTarget(
			`max(portkey_api_error_rate`+
				acme.IntSel(`workspace=~"$workspace",metadata_use_case=~"$use_case"`)+`)`,
			"error rate").RefId("A"),
		dashboard.PromTarget(
			`avg(portkey_api_cache_hit_rate`+
				acme.IntSel(`workspace=~"$workspace",metadata_use_case=~"$use_case"`)+`)`,
			"cache-hit rate").RefId("B")))

	// ── TAB: By use case ─────────────────────────────────────────────────────────────────────────
	// The predecessor repeats this row once per $use_case value (row-level repeat). The stat row below
	// uses RepeatSection so Grafana clones it per $use_case value (mode=variable), matching the
	// predecessor's per-use-case repeat behaviour.

	// Stat row: requests / cost / p99 / cache-hit / rescued (predecessor panel-uc-*).
	dashboard.AddPanel(&d, "uc-req", dashboard.StatTile(
		"Requests (window)", "short",
		dashboard.PromTarget(
			"sum(portkey_api_requests_total"+
				acme.IntSel(`metadata_use_case=~"$use_case"`)+
				")",
			"")))

	dashboard.AddPanel(&d, "uc-cost", dashboard.StatTile(
		"Cost (window, USD)", "currencyUSD",
		dashboard.PromTarget(
			"sum(portkey_api_cost_usd"+
				acme.IntSel(`metadata_use_case=~"$use_case"`)+
				")",
			"")))

	// portkey_api_latency_seconds GAUGE p99 — emitted; panel populates.
	// Low-is-good: green → yellow → red.
	dashboard.AddPanel(&d, "uc-p99", dashboard.StatTile(
		"Latency p99 (s)", "s",
		dashboard.PromTarget(
			`max(portkey_api_latency_seconds`+
				acme.IntSel(`quantile="0.99",metadata_use_case=~"$use_case"`)+`)`,
			""),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 3, Color: "yellow"},
		dashboard.Threshold{Value: 10, Color: "red"}))

	// High-is-good: red → yellow → green.
	dashboard.AddPanel(&d, "uc-cache", dashboard.StatTile(
		"Cache-hit rate", "percentunit",
		dashboard.PromTarget(
			"avg(portkey_api_cache_hit_rate"+
				acme.IntSel(`metadata_use_case=~"$use_case"`)+
				")",
			""),
		dashboard.Threshold{Value: 0, Color: "red"},
		dashboard.Threshold{Value: 0.2, Color: "yellow"},
		dashboard.Threshold{Value: 0.5, Color: "green"}))

	dashboard.AddPanel(&d, "uc-rescued", dashboard.StatTile(
		"Rescued requests (retry/fallback)", "short",
		dashboard.PromTarget(
			"sum(portkey_api_rescued_requests_total"+
				acme.IntSel(`metadata_use_case=~"$use_case"`)+
				")",
			"")))

	// Degradation row: fallback storm indicator + rescued over time + error rate over time.
	// Wired as ConditionalSection: Grafana hides the row when both trigger panels return no data.
	// Trigger panels use threshold-filtered queries (> 5 / > 0.05) — they return empty series when
	// metrics are below threshold (healthy), causing the ConditionalSection row to auto-hide.
	// Red when non-zero (storm active); green when 0.
	//
	// uc-fallback-trigger: threshold gate for rescued_requests > 5; empty when healthy.
	dashboard.AddPanel(&d, "uc-fallback-trigger", dashboard.TimeseriesPanel(
		"Fallback storm trigger (rescued > 5)", "short",
		dashboard.PromTarget(
			`max(portkey_api_rescued_requests_total`+acme.IntSel("")+`) > 5`,
			"rescued > 5")))

	// uc-error-trigger: threshold gate for error_rate > 0.05; empty when healthy.
	dashboard.AddPanel(&d, "uc-error-trigger", dashboard.TimeseriesPanel(
		"Error storm trigger (error_rate > 5%)", "percentunit",
		dashboard.PromTarget(
			`max(portkey_api_error_rate`+acme.IntSel("")+`) > 0.05`,
			"error_rate > 0.05")))

	dashboard.AddPanel(&d, "uc-degrade-stat", dashboard.StatTile(
		"⚠ Fallback storm indicator", "short",
		dashboard.PromTarget(
			`sum(portkey_api_rescued_requests_total`+acme.IntSel("")+` > 5) `+
				`OR sum(portkey_api_error_rate`+acme.IntSel("")+` > 0.05)`,
			"degraded"),
		dashboard.Threshold{Value: 0, Color: "green"},
		dashboard.Threshold{Value: 1, Color: "red"}))

	dashboard.AddPanel(&d, "uc-degrade-rescued", dashboard.TimeseriesPanel(
		"Rescued requests over time (all use_cases)", "short",
		dashboard.PromTarget(
			dashboard.GaugeExpr("portkey_api_rescued_requests_total",
				acme.IntSel(""),
				[]string{"metadata_use_case"}),
			"{{metadata_use_case}}")))

	dashboard.AddPanel(&d, "uc-degrade-error", dashboard.TimeseriesPanel(
		"Error rate over time (degradation)", "percentunit",
		dashboard.PromTarget(
			`max by (metadata_use_case) (portkey_api_error_rate`+acme.IntSel("")+`)`,
			"error · {{metadata_use_case}}")))

	// Cost and tokens by model over time (predecessor panel-30, 31).
	dashboard.AddPanel(&d, "uc-cost-model", dashboard.TimeseriesPanel(
		"Cost by model over time (USD)", "currencyUSD",
		dashboard.PromTarget(
			dashboard.GaugeExpr("portkey_api_cost_usd",
				acme.IntSel(`workspace=~"$workspace",ai_model=~"$ai_model",metadata_use_case=~"$use_case"`),
				[]string{"ai_model"}),
			"{{ai_model}}")))

	dashboard.AddPanel(&d, "uc-tokens-model", dashboard.TimeseriesPanel(
		"Tokens by model over time", "short",
		dashboard.PromTarget(
			dashboard.GaugeExpr("portkey_api_tokens_total",
				acme.IntSel(`workspace=~"$workspace",ai_model=~"$ai_model",metadata_use_case=~"$use_case"`),
				[]string{"ai_model"}),
			"{{ai_model}}")))

	// Retries & fallbacks (recording-rule-derived from Portkey logs — predecessor panel-40).
	// llm_retries_total and llm_fallbacks_total are Loki recording-rule-derived series
	// (count_over_time([5m]) → instant GAUGE evaluated per scrape, NOT a cumulative counter).
	// Query the recorded series DIRECTLY — rate() is WRONG for this family.
	// The recording rule groups by (env, provider); scope by env=~"$env" here.
	// KNOWN GAP: NOT yet emitted by synthkit's promrw path; panels will be empty until fill lands.
	dashboard.AddPanel(&d, "uc-retries-fallbacks", dashboard.TimeseriesPanel(
		"Retries & fallbacks (recording-rule-derived from Portkey logs)", "short",
		dashboard.PromTarget(
			`sum by (provider) (llm_retries_total`+acme.IntSel("")+`)`,
			"retries · {{provider}}").RefId("A"),
		dashboard.PromTarget(
			`sum by (provider) (llm_fallbacks_total`+acme.IntSel("")+`)`,
			"fallbacks · {{provider}}").RefId("B")))

	// Latency by use case (p99, pre-computed GAUGE — predecessor panel-41) — emitted; panel populates.
	dashboard.AddPanel(&d, "uc-latency-usecase", dashboard.TimeseriesPanel(
		"Latency by use case (p99)", "s",
		dashboard.PromTarget(
			`max by (metadata_use_case) (portkey_api_latency_seconds`+
				acme.IntSel(`workspace=~"$workspace",quantile="0.99",metadata_use_case=~"$use_case"`)+`)`,
			"p99 · {{metadata_use_case}}")))

	// ── TAB: Attribution (Infinity tables) ───────────────────────────────────────────────────────

	// Section header note (predecessor panel-inf-text).
	dashboard.AddPanel(&d, "att-header", dashboard.TextPanel(
		"Attribution — Portkey analytics groups (API-pull, tabular)",
		"### Portkey analytics groups (API-pull)\n"+
			"Bedrock cannot dimension by use_case → Portkey is the metric-level attribution source. "+
			"Tables degrade with the live failure plane (provider_outage → error_rate↑, cache_collapse → hit-rate→0). "+
			"Requires the Infinity datasource base URL reachable from browser (or PDC)."))

	// By use_case: GET /v1/analytics/groups/metadata (predecessor panel-inf-usecase).
	// Root selector "data"; columns: group_key, requests, cost, total_tokens, latency_p99,
	// error_rate, cache_hit_rate, rescued_requests.
	dashboard.AddPanel(&d, "att-usecase", dashboard.InfinityTablePanel(
		`By use_case — cost / tokens / p99 / errors / cache / rescued (GET /v1/analytics/groups/metadata)`,
		"A", "/v1/analytics/groups/metadata", "synthkit (Infinity)", "data",
		dashboard.Col("group_key", "use_case", "string"),
		dashboard.Col("requests", "requests", "number").WithUnit("short"),
		dashboard.Col("cost", "cost $", "number").WithUnit("currencyUSD").WithDecimals(2),
		dashboard.Col("total_tokens", "tokens", "number").WithUnit("short"),
		dashboard.Col("latency_p99", "p99 ms", "number").WithUnit("s"),
		dashboard.Col("error_rate", "error_rate", "number").WithUnit("percentunit").WithDecimals(3).WithColorMode("color-text"),
		dashboard.Col("cache_hit_rate", "cache_hit_rate", "number").WithUnit("percentunit").WithDecimals(3).WithColorMode("color-text"),
		dashboard.Col("rescued_requests", "rescued", "number").WithUnit("short"),
	))

	// By model: GET /v1/analytics/groups/ai-models (predecessor panel-inf-model).
	// Root selector "data"; same columns with group_key = model name.
	dashboard.AddPanel(&d, "att-model", dashboard.InfinityTablePanel(
		`By model — cost / tokens / p99 / errors / cache / rescued (GET /v1/analytics/groups/ai-models)`,
		"A", "/v1/analytics/groups/ai-models", "synthkit (Infinity)", "data",
		dashboard.Col("group_key", "model", "string"),
		dashboard.Col("requests", "requests", "number").WithUnit("short"),
		dashboard.Col("cost", "cost $", "number").WithUnit("currencyUSD").WithDecimals(2),
		dashboard.Col("total_tokens", "tokens", "number").WithUnit("short"),
		dashboard.Col("latency_p99", "p99 ms", "number").WithUnit("s"),
		dashboard.Col("error_rate", "error_rate", "number").WithUnit("percentunit").WithDecimals(3).WithColorMode("color-text"),
		dashboard.Col("cache_hit_rate", "cache_hit_rate", "number").WithUnit("percentunit").WithDecimals(3).WithColorMode("color-text"),
		dashboard.Col("rescued_requests", "rescued", "number").WithUnit("short"),
	))

	// Provider/model catalog: GET /v1/configs (predecessor panel-inf-configs).
	// Root selector "providers"; status flips to "degraded" on provider_outage.
	dashboard.AddPanel(&d, "att-configs", dashboard.InfinityTablePanel(
		`Provider / model catalog (GET /v1/configs) — status flips to "degraded" on provider_outage`,
		"A", "/v1/configs", "synthkit (Infinity)", "providers",
		dashboard.Col("slug", "provider_slug", "string"),
		dashboard.Col("provider", "provider", "string"),
		dashboard.Col("region", "region", "string"),
		dashboard.Col("status", "status", "string").WithColorMode("color-background"),
	))

	// Prompt registry: GET /v1/prompts (predecessor panel-inf-prompts).
	// Root selector "data"; columns: slug, version, status, env_label, last_approved.
	dashboard.AddPanel(&d, "att-prompts", dashboard.InfinityTablePanel(
		`Prompt registry (GET /v1/prompts) — slug / version / status / env`,
		"A", "/v1/prompts", "synthkit (Infinity)", "data",
		dashboard.Col("slug", "slug", "string"),
		dashboard.Col("version", "version", "number").WithUnit("short"),
		dashboard.Col("status", "status", "string").WithColorMode("color-background"),
		dashboard.Col("env_label", "env", "string"),
		dashboard.Col("last_approved", "last_approved", "string"),
	))

	// ── TAB: Logs ────────────────────────────────────────────────────────────────────────────────

	// Portkey 2b export log stream (predecessor panel-50).
	// Stream selector: {source="portkey"} (substrate — no blueprint label).
	// The predecessor also filters service_name="portkey-gateway" and scenario="$scenario"; on synthkit
	// the Loki stream carries {source="portkey"} only (no blueprint/scenario stream label for the
	// substrate lane).
	dashboard.AddPanel(&d, "logs-portkey", dashboard.LogsPanel(
		`Portkey 2b export logs (body-excluded — correlation_id / portkey_trace_id in structured metadata)`,
		dashboard.LokiTarget(
			`{source="portkey"} | json`,
			"")))

	// ── Layout ───────────────────────────────────────────────────────────────────────────────────
	dashboard.WithTabs(&d,
		dashboard.Tabbed("Overview",
			dashboard.Section("Gateway KPIs",
				dashboard.Tile("ov-total-req"), dashboard.Tile("ov-error-rate"),
				dashboard.Tile("ov-cache-hit"), dashboard.Tile("ov-cost"),
				dashboard.Tile("ov-tokens"), dashboard.Tile("ov-freshness")),
			dashboard.Section("Data contract",
				dashboard.Full("ov-contract")),
			dashboard.Section("Traffic & reliability",
				dashboard.Half("ov-rescued"), dashboard.Half("ov-req-status")),
			dashboard.Section("Latency & error trends",
				dashboard.Half("ov-latency-quantiles"), dashboard.Half("ov-err-cache-ts")),
		),
		dashboard.Tabbed("By use case",
			dashboard.RepeatSection("Per-use-case summary", "use_case",
				dashboard.Tile("uc-req"), dashboard.Tile("uc-cost"),
				dashboard.Tile("uc-p99"), dashboard.Tile("uc-cache"),
				dashboard.Tile("uc-rescued")),
			dashboard.ConditionalSection("Fallback storm",
				dashboard.Half("uc-fallback-trigger"), dashboard.Half("uc-error-trigger"),
				dashboard.Stat("uc-degrade-stat"),
				dashboard.Half("uc-degrade-rescued"), dashboard.Half("uc-degrade-error")),
			dashboard.Section("Cost & tokens by model",
				dashboard.Half("uc-cost-model"), dashboard.Half("uc-tokens-model")),
			dashboard.Section("Retries, fallbacks & latency",
				dashboard.Half("uc-retries-fallbacks"), dashboard.Half("uc-latency-usecase")),
		),
		dashboard.Tabbed("Attribution",
			dashboard.Section("Attribution note",
				dashboard.Full("att-header")),
			dashboard.Section("By dimension (Portkey analytics API-pull)",
				dashboard.Full("att-usecase"), dashboard.Full("att-model")),
			dashboard.Section("Config & prompt registry",
				dashboard.Half("att-configs"), dashboard.Half("att-prompts")),
		),
		dashboard.Tabbed("Logs",
			dashboard.Section("",
				dashboard.Tall("logs-portkey")),
		),
	)
	return d, nil
}
