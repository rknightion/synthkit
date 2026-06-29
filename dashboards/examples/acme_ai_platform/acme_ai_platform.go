// SPDX-License-Identifier: AGPL-3.0-only

// Package acme_ai_platform holds the customer dashboards for the acme_ai_platform blueprint — the
// API-poll reality of an AI-assistant estate (browser frontend → python backend doing the gen_ai
// work in-process; the LLM gateway observed via the Portkey ANALYTICS poller, NOT a connected
// span). It imports ONLY the dashboard builder library (never internal/core or constructs) — data
// composition, not emission. Registered in cmd/synthkit-dash/catalog.go.
//
// All metric NAMES + label keys are sourced from the live the example stack emit surface (verified via the
// -once -dump facade + gcx read-back, 2026-06-15). Scope discipline (load-bearing — wrong scope =
// empty panel):
//   - App per-service families (http_server_*, gen_ai_client_*) carry the synth `blueprint` label
//     → scope by blueprint + service + the $env regex.
//   - Integration families (portkey_api_*, langsmith_eval_*) are substrate-scoped (NO blueprint
//     label) → scope by `env` (+ workspace/project/use_case), NEVER blueprint.
//   - Faro RUM streams are collector-owned: NO blueprint label — scope by service_name +
//     kind + deployment_environment. The 3 fabricated Prometheus RUM gauges
//     (largest_contentful_paint / cumulative_layout_shift / interaction_to_next_paint) have been
//     removed from emit; all web-vital views use Loki unwrap queries.
//   - traces_service_graph_* are Tempo-metrics-generator-derived (no blueprint label) → scope by
//     the server/client service names.
package acme_ai_platform

import (
	"sort"
	"strings"

	"github.com/rknightion/synthkit/dashboard"
)

const blueprintName = "acme-ai-platform"

// Templates returns acme_ai_platform's dashboard templates (registered in the CLI catalog). The faithful
// per-concern set (NN_*.go) is being ported from the predecessor; Overview is the original condensed
// hub and stays until the faithful 00-observability hub lands.
func Templates() []dashboard.Template {
	return []dashboard.Template{
		Overview,
		ObservabilityHub,
		Exec,
		PortkeyGateway,
		Bedrock,
		AgentCore,
		LangSmithEvals,
		LangGraph,
		ApmRum,
		Sigil,
		RequestCorrelation,
		Database,
		PipelineHealth,
	}
}

// appSel builds a selector for an app-emitted (blueprint+service scoped) family, filtered by the
// $env multi-value template variable. extra is an already-formatted matcher list (no leading comma).
func appSel(extra string) string {
	s := `blueprint="` + blueprintName + `",deployment_environment_name=~"$env"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// intSel builds a selector for an integration (env-scoped, no blueprint label) family.
func intSel(extra string) string {
	s := `env=~"$env"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// lsSel scopes a langsmith_eval_* family to acme_ai_platform: env-filtered AND excluding
// acme_ai_platform_eval's "-gw" projects. langsmith_eval is substrate-scoped (no blueprint label)
// and acme_ai_platform + acme_ai_platform_eval share env names — acme_ai_platform_eval
// disambiguates by suffixing its projects "-gw", so the project!~".+-gw" matcher keeps
// this dashboard's LangSmith panels to acme_ai_platform only.
func lsSel(extra string) string {
	s := `env=~"$env",project!~".+-gw"`
	if extra != "" {
		s += "," + extra
	}
	return "{" + s + "}"
}

// Overview is the acme_ai_platform flagship dashboard: the AI-assistant estate end-to-end across seven
// tabs — Overview, Service RED, GenAI, Frontend (RUM), Gateway (Portkey poller), Eval (LangSmith),
// and Request Correlation (one request followed end-to-end across correlated traces + logs).
func Overview(m *dashboard.Manifest) (dashboard.Dashboard, error) {
	d, err := dashboard.NewDashboard("acme-ws1-overview", "Acme AI — AI Assistant Estate")
	if err != nil {
		return dashboard.Dashboard{}, err
	}
	d.Builder.CustomVariable(dashboard.EnvVar(m))

	// ── Overview ────────────────────────────────────────────────────────────────────────────
	dashboard.AddPanel(&d, "ov-rps", dashboard.StatPanel("Request rate",
		dashboard.PromTarget("sum (rate(http_server_request_duration_seconds_count"+appSel("")+"[$__rate_interval]))", "req/s")))
	dashboard.AddPanel(&d, "ov-errpct", dashboard.StatPanel("Error rate %",
		dashboard.PromTarget(
			"100 * sum (rate(http_server_request_duration_seconds_count"+appSel(`http_response_status_code=~"5.."`)+"[$__rate_interval]))"+
				" / sum (rate(http_server_request_duration_seconds_count"+appSel("")+"[$__rate_interval]))", "5xx %")))
	dashboard.AddPanel(&d, "ov-p95", dashboard.StatPanel("Backend p95 latency",
		dashboard.PromTarget(dashboard.ClassicHistogramQuantile(0.95, "http_server_request_duration_seconds", appSel(""), nil), "p95")))
	dashboard.AddPanel(&d, "ov-tokens", dashboard.StatPanel("GenAI tokens/min",
		dashboard.PromTarget("60 * sum (rate(gen_ai_client_token_usage_count"+appSel("")+"[$__rate_interval]))", "tokens/min")))
	dashboard.AddPanel(&d, "ov-lcp", dashboard.StatPanel("Frontend LCP p75 (s)",
		dashboard.LokiTarget(
			`quantile_over_time(0.75, {service_name="acme-frontend",kind="measurement",deployment_environment=~"$env"} | logfmt | lcp > 0 | unwrap lcp [5m]) by () / 1000`,
			"LCP p75 (s)")))
	dashboard.AddPanel(&d, "ov-rate-by-svc", dashboard.TimeseriesPanel("Request rate by service", "reqps",
		dashboard.PromTarget(dashboard.RateExpr("http_server_request_duration_seconds_count", appSel(""), []string{"service"}), "{{service}}")))

	// ── Service RED ─────────────────────────────────────────────────────────────────────────
	dashboard.AddPanel(&d, "red-rate", dashboard.TimeseriesPanel("Request rate by status", "reqps",
		dashboard.PromTarget(dashboard.RateExpr("http_server_request_duration_seconds_count", appSel(""), []string{"http_response_status_code"}), "{{http_response_status_code}}")))
	dashboard.AddPanel(&d, "red-latency", dashboard.TimeseriesPanel("Backend latency quantiles", "s",
		dashboard.PromTarget(dashboard.ClassicHistogramQuantile(0.50, "http_server_request_duration_seconds", appSel(""), nil), "p50").RefId("A"),
		dashboard.PromTarget(dashboard.ClassicHistogramQuantile(0.95, "http_server_request_duration_seconds", appSel(""), nil), "p95").RefId("B"),
		dashboard.PromTarget(dashboard.ClassicHistogramQuantile(0.99, "http_server_request_duration_seconds", appSel(""), nil), "p99").RefId("C")))
	dashboard.AddPanel(&d, "red-method", dashboard.TimeseriesPanel("Request rate by method", "reqps",
		dashboard.PromTarget(dashboard.RateExpr("http_server_request_duration_seconds_count", appSel(""), []string{"http_request_method"}), "{{http_request_method}}")))
	dashboard.AddPanel(&d, "red-active", dashboard.TimeseriesPanel("Active requests (in-flight)", "short",
		dashboard.PromTarget(dashboard.GaugeExpr("http_server_active_requests", appSel(""), []string{"service"}), "{{service}}")))

	// ── GenAI ───────────────────────────────────────────────────────────────────────────────
	dashboard.AddPanel(&d, "ai-tokens-type", dashboard.TimeseriesPanel("Token throughput by type", "short",
		dashboard.PromTarget(dashboard.RateExpr("gen_ai_client_token_usage_count", appSel(""), []string{"gen_ai_token_type"}), "{{gen_ai_token_type}}")))
	dashboard.AddPanel(&d, "ai-tokens-provider", dashboard.TimeseriesPanel("Token throughput by provider", "short",
		dashboard.PromTarget(dashboard.RateExpr("gen_ai_client_token_usage_count", appSel(""), []string{"gen_ai_provider_name"}), "{{gen_ai_provider_name}}")))
	dashboard.AddPanel(&d, "ai-opdur", dashboard.TimeseriesPanel("LLM operation duration p95 by operation", "s",
		dashboard.PromTarget(dashboard.ClassicHistogramQuantile(0.95, "gen_ai_client_operation_duration_seconds", appSel(""), []string{"gen_ai_operation_name"}), "{{gen_ai_operation_name}}")))
	dashboard.AddPanel(&d, "ai-callrate", dashboard.TimeseriesPanel("LLM call rate by provider", "reqps",
		dashboard.PromTarget(dashboard.RateExpr("gen_ai_client_operation_duration_seconds_count", appSel(""), []string{"gen_ai_provider_name"}), "{{gen_ai_provider_name}}")))

	// ── Gateway (Portkey analytics poller; portkey_api_*, env+workspace+use_case scoped) ──────
	dashboard.AddPanel(&d, "gw-req", dashboard.TimeseriesPanel("Gateway requests by status class", "reqps",
		dashboard.PromTarget(dashboard.RateExpr("portkey_api_requests_total", intSel(""), []string{"status_class"}), "{{status_class}}")))
	dashboard.AddPanel(&d, "gw-tokens", dashboard.TimeseriesPanel("Gateway tokens by model", "short",
		dashboard.PromTarget(dashboard.RateExpr("portkey_api_tokens_total", intSel(""), []string{"ai_model"}), "{{ai_model}}")))
	dashboard.AddPanel(&d, "gw-cost", dashboard.TimeseriesPanel("Gateway cost (USD) by model", "currencyUSD",
		dashboard.PromTarget(dashboard.GaugeExpr("portkey_api_cost_usd", intSel(""), []string{"ai_model"}), "{{ai_model}}")))
	dashboard.AddPanel(&d, "gw-cache", dashboard.TimeseriesPanel("Gateway cache-hit rate by use case", "percentunit",
		dashboard.PromTarget(dashboard.GaugeExpr("portkey_api_cache_hit_rate", intSel(""), []string{"metadata_use_case"}), "{{metadata_use_case}}")))

	// ── Eval (LangSmith eval bridge; langsmith_eval_*, env+project+use_case scoped) ───────────
	dashboard.AddPanel(&d, "ev-score", dashboard.TimeseriesPanel("Eval score by evaluator", "short",
		dashboard.PromTarget(dashboard.GaugeExpr("langsmith_eval_score", lsSel(""), []string{"evaluator"}), "{{evaluator}}")))
	dashboard.AddPanel(&d, "ev-faith", dashboard.TimeseriesPanel("Faithfulness ratio by project", "percentunit",
		dashboard.PromTarget(dashboard.GaugeExpr("langsmith_eval_faithfulness_ratio", lsSel(""), []string{"project"}), "{{project}}")))
	dashboard.AddPanel(&d, "ev-latency", dashboard.TimeseriesPanel("Eval latency by use case", "s",
		dashboard.PromTarget(dashboard.GaugeExpr("langsmith_eval_latency_seconds", lsSel(""), []string{"use_case"}), "{{use_case}}")))
	dashboard.AddPanel(&d, "ev-fallback", dashboard.TimeseriesPanel("Retry / fallback rate by use case", "percentunit",
		dashboard.PromTarget(dashboard.GaugeExpr("langsmith_eval_retry_rate", lsSel(""), []string{"use_case"}), "retry {{use_case}}").RefId("A"),
		dashboard.PromTarget(dashboard.GaugeExpr("langsmith_eval_fallback_rate", lsSel(""), []string{"use_case"}), "fallback {{use_case}}").RefId("B")))

	// ── Request Correlation (one request end-to-end across correlated traces + logs) ───────────
	dashboard.AddPanel(&d, "gt-traces", dashboard.TablePanel("Backend assist traces (Tempo)",
		dashboard.TempoTarget(`{resource.service.name="acme-backend" && resource.blueprint="acme-ai-platform"}`)))
	dashboard.AddPanel(&d, "gt-applog", dashboard.TimeseriesPanel("App log volume by level", "short",
		dashboard.LokiTarget(`sum by (level) (count_over_time({source="app", blueprint="acme-ai-platform"} [$__auto]))`, "{{level}}")))
	dashboard.AddPanel(&d, "gt-portkeylog", dashboard.TablePanel("Portkey export logs (correlated)",
		dashboard.LokiTarget(`{source="portkey", blueprint="acme-ai-platform"}`, "")))

	dashboard.WithTabs(&d,
		dashboard.Tab("Overview", "ov-rps", "ov-errpct", "ov-p95", "ov-tokens", "ov-lcp", "ov-rate-by-svc"),
		dashboard.Tab("Service RED", "red-rate", "red-latency", "red-method", "red-active"),
		dashboard.Tab("GenAI", "ai-tokens-type", "ai-tokens-provider", "ai-opdur", "ai-callrate"),
		dashboard.Tab("Gateway (Portkey)", "gw-req", "gw-tokens", "gw-cost", "gw-cache"),
		dashboard.Tab("Eval (LangSmith)", "ev-score", "ev-faith", "ev-latency", "ev-fallback"),
		dashboard.Tab("Request Correlation", "gt-traces", "gt-applog", "gt-portkeylog"),
	)
	return d, nil
}

// Rules returns the acme_ai_platform recording rule groups — the 2 Loki recording rules that derive
// llm_retries_total and llm_fallbacks_total from the source=portkey export log stream.
// The portkey log stream carries no blueprint label (stream labels: env, service_name, level, source,
// cluster, job) → scope by env=~ using the scenario's disjoint env names.
func Rules(m *dashboard.Manifest) []dashboard.RuleGroup {
	envRegex := buildEnvRegex(m)
	var retrySel, fallbackSel string
	if envRegex != "" {
		retrySel = `{source="portkey",env=~"` + envRegex + `"} | json | retry_count > 0 [5m]`
		fallbackSel = `{source="portkey",env=~"` + envRegex + `"} | json | fallback=` + "`true`" + ` [5m]`
	} else {
		retrySel = `{source="portkey"} | json | retry_count > 0 [5m]`
		fallbackSel = `{source="portkey"} | json | fallback=` + "`true`" + ` [5m]`
	}
	return []dashboard.RuleGroup{{
		Name:      blueprintName + "-portkey-derived",
		FolderUID: blueprintName,
		Recordings: []dashboard.RecordingRule{
			{
				UID:         blueprintName + "-llm-retries-total",
				Record:      "llm_retries_total",
				Datasource:  "loki",
				Expr:        `sum by (env, provider) (count_over_time(` + retrySel + `))`,
				IntervalSec: 300,
			},
			{
				UID:         blueprintName + "-llm-fallbacks-total",
				Record:      "llm_fallbacks_total",
				Datasource:  "loki",
				Expr:        `sum by (env, provider) (count_over_time(` + fallbackSel + `))`,
				IntervalSec: 300,
			},
		},
	}}
}

// buildEnvRegex builds a |-joined regex of env names from the manifest for use in Loki
// stream selectors. Returns "" if m.Environments is empty (caller omits the env= matcher).
func buildEnvRegex(m *dashboard.Manifest) string {
	if len(m.Environments) == 0 {
		return ""
	}
	parts := make([]string, len(m.Environments))
	for i, e := range m.Environments {
		parts[i] = e.Name
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}
