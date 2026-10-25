// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"strings"
	"testing"
)

// Every resource-axis mode maps to exactly the kind its help text says it acts on, and every
// kind has at least one mode that moves it.
func TestModeKindCoversFailureModes(t *testing.T) {
	if len(modeKinds) != len(FailureModes) {
		t.Fatalf("%d mapped modes, %d failure modes", len(modeKinds), len(FailureModes))
	}
	moved := map[Kind]bool{}
	for _, m := range FailureModes {
		k, ok := ModeKind(m.Name)
		if !ok {
			t.Fatalf("mode %q has no kind", m.Name)
		}
		if !kinds[k] {
			t.Fatalf("mode %q maps to undeclared kind %q", m.Name, k)
		}
		if !strings.HasPrefix(m.Help, string(k)+":") {
			t.Errorf("mode %q maps to %q but its help says %q", m.Name, k, m.Help)
		}
		moved[k] = true
	}
	for k := range kinds {
		if !moved[k] {
			t.Errorf("no mode acts on kind %q", k)
		}
	}
}

func TestModeKindPairs(t *testing.T) {
	want := map[string]Kind{
		"pool_exhausted":         KindPool,
		"concurrency_reduced":    KindConcurrency,
		"quota_reduced":          KindQuota,
		"consumer_slowdown":      KindQueue,
		"dependency_slow":        KindDependency,
		"dependency_unavailable": KindDependency,
		"credential_expired":     KindCredential,
		"rule_regression":        KindRule,
	}
	for mode, kind := range want {
		if got, ok := ModeKind(mode); !ok || got != kind {
			t.Errorf("ModeKind(%q) = %q, %v; want %q", mode, got, ok, kind)
		}
	}
	for _, mode := range []string{"", "connection_saturation", "Pool_Exhausted"} {
		if k, ok := ModeKind(mode); ok {
			t.Errorf("ModeKind(%q) = %q, want not a resource mode", mode, k)
		}
	}
}
