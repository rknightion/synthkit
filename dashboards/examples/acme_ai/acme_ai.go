// SPDX-License-Identifier: AGPL-3.0-only

// Package acme_ai is the SHARED seam for the Acme AI example dashboards (WS1 acme_ai_platform,
// WS2 acme_ai_platform_eval, WS3 acme_ai_eval). It is a thin, frozen helper layer over the generic
// dashboard/ builder: datasource UIDs, the Acme AI service.name values, the family-name
// reconciliation map, and the scope-correct PromQL selector helpers every example dashboard reuses.
//
// Scope discipline (load-bearing — wrong scope = empty panel):
//   - APP families (http_server_*, gen_ai_client_*, http_server_active_requests, RUM web-vitals) carry
//     blueprint + deployment_environment + service → AppSel (blueprint pinned to $scenario).
//   - SUBSTRATE/integration families (portkey_api_*, portkey native llm_*/portkey_request_*/request_count/
//     node_*, aws_bedrock_*, aws_bedrock_agentcore_*, langsmith platform exporters, aws_* infra, kube_*/
//     container_*, otelcol_*) carry NO blueprint label → IntSel (env-scoped) / family-specific selectors.
//   - langsmith_eval_* is substrate (env-scoped); WS1/WS2 share env names so WS2 suffixes its projects
//     "-gw" → LangsmithSel disambiguates.
//   - Metrics-generator families (traces_spanmetrics_*, traces_service_graph_*) carry NO blueprint and
//     are scoped by service / client+server edge (hand-write those selectors; they're not app-scoped).
//
// Service-name reconciliation: dashboards surface the Acme AI identity (env names DEV1..PRD,
// use_cases, projects, service.names, the "scenario" concept) because synthkit emits those as label
// VALUES. The "scenario" concept rides a ConstVar referenced as blueprint="$scenario". Where a metric
// NAME carries a synthetic prefix (synthkit_*), the Metric* constants document the mapping.
package acme_ai

// Datasource UIDs — Grafana Cloud convention (example stack). Panels resolve their
// datasource by query group, so these are needed only where a uid must be embedded (e.g. the
// trace-table Explore deep link).
const (
	DSProm  = "grafanacloud-prom"
	DSLoki  = "grafanacloud-logs"
	DSTempo = "grafanacloud-traces"
)

// Acme AI service.name values (emitted as label VALUES).
const (
	SvcFrontend     = "acme-frontend"
	SvcBackend      = "acme-backend"
	SvcAgentRuntime = "acme-agent-runtime"
	SvcPortkey      = "portkey"
)

// Family-name reconciliation. synthkit emits the LHS name; the comments document the real-stack
// equivalent. Dashboards query the LHS so panels populate against synthkit-emitted data.
const (
	MetricContentLeakTest = "synthkit_content_leak_test"            // real: acme_content_leak_test
	MetricContentDropped  = "synthkit_content_dropped_total"        // real: acme_content_dropped_total
	MetricPollerLastOK    = "poller_last_success_timestamp_seconds" // real: acme_poller_last_success_timestamp_seconds
	MetricPollerErrors    = "poller_api_errors_total"               // real: acme_poller_api_errors_total
	MetricPollerWindowLag = "poller_window_lag_seconds"             // real: acme_poller_window_lag_seconds
)

// join folds an optional already-formatted matcher list onto a base, returning a braced selector.
func join(base, extra string) string {
	if extra != "" {
		base += "," + extra
	}
	return "{" + base + "}"
}

// AppSel scopes an APP-emitted promrw family (carries blueprint + deployment_environment_name +
// service): blueprint pinned to the $scenario const var, deployment_environment_name filtered by the
// $env multi var. The env label is the OTEL semconv `_name` form (clean cutover — synthkit emits
// deployment_environment_name on its native lanes). extra is an already-formatted matcher list (no
// leading comma), e.g. `service="acme-backend"`. Requires the dashboard to define the `scenario`
// (ConstVar) + `env` (LabelValuesVar) variables. Real stack: swap blueprint→scenario.
func AppSel(extra string) string {
	return join(`blueprint="$scenario",deployment_environment_name=~"$env"`, extra)
}

// IntSel scopes a SUBSTRATE/integration family (NO blueprint label) by the substrate `env` label and
// the $env multi var. extra appends family-specific matchers (workspace/use_case/model/account/…).
func IntSel(extra string) string {
	return join(`env=~"$env"`, extra)
}

// LangsmithSel scopes langsmith_eval_* (substrate, env-scoped) with WS1/WS2 project disambiguation.
// WS1 (acme_ai_platform) and WS2 (acme_ai_platform_eval) share env names; WS2 suffixes its projects
// "-gw". excludeGW=true → WS1 view (project!~".+-gw"); false → WS2 view (project=~".+-gw").
func LangsmithSel(excludeGW bool, extra string) string {
	proj := `project=~".+-gw"`
	if excludeGW {
		proj = `project!~".+-gw"`
	}
	base := `env=~"$env",` + proj
	return join(base, extra)
}
