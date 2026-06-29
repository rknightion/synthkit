// SPDX-License-Identifier: AGPL-3.0-only

package forge

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// BuildPrompt produces a single self-contained prompt: the deterministic skeleton (as YAML), the
// fuzzy gaps with their evidence, and the live catalog description. The model returns a finished
// blueprint YAML. It is self-contained so it pastes into Claude Code OR a fresh Claude session.
func BuildPrompt(skeleton *Skeleton, gaps []Gap, catalog string) (string, error) {
	skelYAML, err := yaml.Marshal(skeleton)
	if err != nil {
		return "", fmt.Errorf("forge: marshal skeleton: %w", err)
	}
	var b strings.Builder
	b.WriteString("# Task: complete a synthkit blueprint from a captured environment\n\n")
	b.WriteString("You are completing a synthkit blueprint. The structural skeleton below was ")
	b.WriteString("derived deterministically from a real customer cluster. Your job: resolve the ")
	b.WriteString("GAPS using ONLY the construct/workload kinds in the catalog. Do not invent kinds, ")
	b.WriteString("metrics, or labels. Return a single valid blueprint YAML document.\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Classify each gap workload as `app` or `web_service` from its image/ports/probes.\n")
	b.WriteString("- Infer `calls` (db/cache/service) from service edges and external-name services.\n")
	b.WriteString("- Keep real names. Set each workload's `runs_on` to the cluster name.\n")
	b.WriteString("- The skeleton's `cloud.account_id`/`vpc_id` are PLACEHOLDERS — replace with the ")
	b.WriteString("real values if known, otherwise leave the placeholders (they keep the blueprint loadable).\n")
	b.WriteString("- Keep the `k8s_monitoring` block as-is unless the capture evidence says otherwise; ")
	b.WriteString("add `features` (cluster_metrics/pod_logs/etc.) only if you have evidence for them.\n")
	b.WriteString("- After producing YAML, it MUST pass `skforge validate`.\n\n")
	b.WriteString("## Catalog (the ONLY kinds you may use)\n\n")
	b.WriteString(catalog)
	b.WriteString("\n## Skeleton so far\n\n```yaml\n")
	b.Write(skelYAML)
	b.WriteString("```\n\n## Gaps to resolve\n\n")
	if len(gaps) == 0 {
		b.WriteString("(none — the skeleton may be complete; still validate.)\n")
	}
	for _, g := range gaps {
		b.WriteString(fmt.Sprintf("- **%s** `%s`", g.Category, g.Name))
		if g.Reason != "" {
			b.WriteString(" — " + g.Reason)
		}
		if len(g.Evidence) > 0 {
			b.WriteString("\n  - evidence: " + strings.Join(g.Evidence, "; "))
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}
