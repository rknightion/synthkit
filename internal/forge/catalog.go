// SPDX-License-Identifier: AGPL-3.0-only

package forge

import (
	"sort"
	"strings"

	"github.com/rknightion/synthkit/internal/core"
)

// CatalogDescription renders every registered construct and workload kind with its one-line Doc,
// so an LLM prompt can ground itself in EXACTLY the constructs this build ships (never inventing
// one we lack). Generated from the live registry — it cannot drift from the catalog.
func CatalogDescription(reg *core.Registry) string {
	var b strings.Builder
	b.WriteString("## Construct kinds (infrastructure / platform)\n")
	for _, k := range sortedCopy(reg.ConstructKinds()) {
		if c, ok := reg.Construct(k); ok {
			b.WriteString("- `" + k + "`: " + c.Doc + "\n")
		}
	}
	b.WriteString("\n## Workload kinds (applications)\n")
	for _, k := range sortedCopy(reg.WorkloadKinds()) {
		if w, ok := reg.Workload(k); ok {
			b.WriteString("- `" + k + "`: " + w.Doc + "\n")
		}
	}
	return b.String()
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
