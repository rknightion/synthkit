// SPDX-License-Identifier: AGPL-3.0-only

package forge

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rknightion/synthkit/internal/capture"
	"github.com/rknightion/synthkit/internal/core"
)

// CoverageReport summarises what was matched vs unmodellable. The "no construct exists" section
// is the trustworthy roadmap signal: it is derived deterministically, not LLM-guessed.
func CoverageReport(inv *capture.Inventory, gaps []Gap, reg *core.Registry) string {
	var b strings.Builder
	b.WriteString("# Capture coverage report\n\n")
	unmodelled := map[string]int{}
	for _, g := range gaps {
		if g.Category == "addon" {
			unmodelled[g.Name]++
		}
	}
	matched := 0
	for _, cl := range inv.Clusters {
		for _, a := range cl.Addons {
			if a.Kind != "" {
				if _, ok := reg.Construct(a.Kind); ok {
					matched++
				}
			}
		}
	}
	b.WriteString(fmt.Sprintf("- addons matched to constructs: %d\n", matched))
	b.WriteString(fmt.Sprintf("- workloads needing model classification: %d\n", countCat(gaps, "workload")))
	b.WriteString("\n## No construct exists (roadmap signal)\n\n")
	if len(unmodelled) == 0 {
		b.WriteString("(everything detected maps to a construct)\n")
	}
	for _, name := range sortedKeys(unmodelled) {
		b.WriteString(fmt.Sprintf("- `%s` (seen %d×)\n", name, unmodelled[name]))
	}
	return b.String()
}

func countCat(gaps []Gap, cat string) int {
	n := 0
	for _, g := range gaps {
		if g.Category == cat {
			n++
		}
	}
	return n
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
