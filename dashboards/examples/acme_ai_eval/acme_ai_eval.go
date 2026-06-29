// SPDX-License-Identifier: AGPL-3.0-only

// Package eval_standalone holds the customer dashboards for the eval_standalone (acme-ai-eval)
// blueprint — the AI-gateway PLATFORM OPERATOR's view across an 8-cell estate (4 AWS account roles
// × 2 regions). This is what the platform team itself observes: gateway health, LangSmith platform
// health, the per-cell AWS estate, the multi-cloud LLM endpoints, edge, and the qualification
// pipeline — NOT any single tenant's app traces. It imports ONLY the dashboard builder library and
// the shared acme seam. Registered in cmd/synthkit-dash/catalog.go under "acme-ai-eval".
//
// TENANT-STRIPPED: no use_case / workspace / correlation business identity exists in acme-ai-eval — the
// platform view scopes by env (the 8 cells), cluster, account_id, region, and provider only.
//
// Scope discipline (load-bearing): the Portkey gateway IS the entry (service.name=portkey) so its
// gen_ai_client_*/http_server_* families are app-scoped → acme.AppSel ($scenario=eval_standalone);
// the native gateway scrape (request_count/portkey_request_*/llm_*/node_*), LangSmith platform
// exporters, AWS CloudWatch (aws_*), k8s (kube_*/container_*), multi-cloud (csp_azure/csp_gcp),
// qualification pipeline, and Cloudflare edge are all SUBSTRATE (NO blueprint label) → env/cluster/
// account/region selectors with the predecessor's {scenario=} filter DROPPED.
package acme_ai_eval

import (
	"sort"
	"strings"

	"github.com/rknightion/synthkit/dashboard"
)

// Templates returns eval_standalone's dashboard templates: the platform-operator suite.
func Templates() []dashboard.Template {
	return []dashboard.Template{
		Overview,
		PortkeyGateway,
		LangSmithPlatform,
		K8s,
		AWS,
	}
}

// Rules returns the eval_standalone recording rule groups — the 2 Loki recording rules that derive
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
