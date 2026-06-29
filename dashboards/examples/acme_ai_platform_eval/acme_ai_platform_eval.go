// SPDX-License-Identifier: AGPL-3.0-only

// Package acme_ai_platform_eval holds the customer dashboards for the acme_ai_platform_eval blueprint —
// the gateway-NATIVE reality of the same AI-assistant estate as acme-ai-platform, but with the AI Evaluation
// AI gateway exposed so the LLM call is observed as a CONNECTED trace (a real Path-B gateway SERVER
// span, service.name=portkey) plus a native gateway scrape (request_count/portkey_request_*/llm_*/
// node_*) and the LangSmith platform-health scrape. It imports ONLY the dashboard builder library
// and the shared acme seam (never internal/core or constructs). Registered in
// cmd/synthkit-dash/catalog.go under the blueprint key "acme_ai_platform_eval".
//
// Scope discipline (load-bearing — wrong scope = empty panel), via the shared acme seam:
//   - APP families (http_server_*, gen_ai_client_*, RUM web-vitals) → acme.AppSel (blueprint pinned
//     to $scenario = "acme_ai_platform_eval").
//   - SUBSTRATE families (native gateway request_count/portkey_request_*/llm_*/node_*, aws_bedrock_*,
//     langsmith platform exporters, kube_*/container_*, otelcol_*) → acme.IntSel (env-scoped, NO
//     blueprint label); the predecessor's {scenario=} filter is DROPPED.
//   - langsmith_eval_* → acme.LangsmithSel(false, …): acme_ai_platform_eval uses the "-gw" projects.
//   - Metrics-generator families (traces_spanmetrics_*, traces_service_graph_*) carry NO blueprint
//     label → hand-written selectors.
package acme_ai_platform_eval

import (
	"sort"
	"strings"

	"github.com/rknightion/synthkit/dashboard"
)

const blueprintName = "acme-ai-platform-eval"

// Templates returns acme_ai_platform_eval's dashboard templates. The 6 structural clones of the
// acme_ai_platform per-construct dashboards (bedrock/agentcore/langsmith-evals/langgraph/apm-rum/sigil)
// are intentionally NOT re-authored here — they differ from acme_ai_platform only by the scenario
// selector. acme_ai_platform_eval ships the 7 NET-NEW dashboards whose content genuinely differs
// under the gateway-native unlock.
func Templates() []dashboard.Template {
	return []dashboard.Template{
		ObservabilityHub,
		Exec,
		PortkeyGateway,
		LangSmithPlatform,
		PipelineHealth,
		RequestCorrelation,
		ConnectedGateway,
	}
}

// Rules returns the acme_ai_platform_eval recording rule groups — the 2 Loki recording rules that derive
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
