// SPDX-License-Identifier: AGPL-3.0-only

package forge

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rknightion/synthkit/internal/core"
)

// CoverageReport summarises what was matched vs unmodellable. It distinguishes two categories
// that route to completely different fixes (SKT-0012.04 finding 1):
//   - "no construct exists" — genuinely detected, no construct in this build models it. A real
//     roadmap signal. Includes both names the recognised-platform-product scan found (finding 3:
//     detected but never surfaced by addon detection at all) and names capture's own detector
//     flagged with no matching kind.
//   - "construct kind not registered" — capture (or the supplemental table) named a construct
//     kind that this build's registry does not actually have. A build/registry-drift bug, never
//     a roadmap item — fix the mapping or the registration, not "build the construct".
//
// Silence is never used to mean "not present": every detected-but-unmodelled name is named
// explicitly, with how many workloads it was seen in, so a reader can never confuse "not present
// in the cluster" with "present and deliberately unmodelled" (SKT-0012.04 finding 3).
func CoverageReport(sk *Skeleton, gaps []Gap, reg *core.Registry) string {
	_ = reg // kept for signature stability / future construct-level detail; matched count comes from sk.
	var b strings.Builder
	b.WriteString("# Capture coverage report\n\n")

	matched := 0
	for _, env := range sk.Environments {
		if env.Cluster != nil {
			matched += len(env.Cluster.Addons)
		}
	}

	roadmap := map[string][]string{} // addon-category gaps: no construct exists at all
	driftGaps := map[string]string{} // addon-category gaps: construct kind not registered
	for _, g := range gaps {
		if g.Category != "addon" {
			continue
		}
		if strings.HasPrefix(g.Reason, "construct kind ") {
			driftGaps[g.Name] = g.Reason
			continue
		}
		roadmap[g.Name] = append(roadmap[g.Name], g.Evidence...)
	}

	b.WriteString(fmt.Sprintf("- addons matched to constructs: %d\n", matched))
	b.WriteString(fmt.Sprintf("- workloads needing model classification: %d\n", countCat(gaps, "workload")))

	b.WriteString("\n## No construct exists (roadmap signal)\n\n")
	b.WriteString("Detected in the capture; this build has no construct that models them. A genuine\n")
	b.WriteString("gap, not a mapping bug — fixing it means adding a construct, not a name mapping.\n\n")
	if len(roadmap) == 0 {
		b.WriteString("(everything detected maps to a construct)\n")
	}
	for _, name := range sortedKeysS(roadmap) {
		b.WriteString(fmt.Sprintf("- `%s` (seen %d×, detected — not absent)\n", name, len(roadmap[name])))
	}

	b.WriteString("\n## Construct exists but is not registered in this build (fix the mapping, not the roadmap)\n\n")
	if len(driftGaps) == 0 {
		b.WriteString("(none)\n")
	}
	for _, name := range sortedStringMapKeys(driftGaps) {
		b.WriteString(fmt.Sprintf("- `%s`: %s\n", name, driftGaps[name]))
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

func sortedKeysS(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedStringMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
