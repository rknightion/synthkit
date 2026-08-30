// SPDX-License-Identifier: AGPL-3.0-only

package failuremode

import "testing"

func TestAssertionForPrefersReversibleObservation(t *testing.T) {
	got, ok := AssertionFor([]string{"cpu_hotspot", "pod_crashloop", "error_spike"})
	if !ok {
		t.Fatal("AssertionFor returned no assertion")
	}
	if got.Mode != "pod_crashloop" {
		t.Fatalf("AssertionFor mode = %q, want pod_crashloop", got.Mode)
	}
}

func TestScenarioModeAssertionsAreSourced(t *testing.T) {
	for _, a := range Assertions() {
		if a.Mode == "" || a.Query == "" || a.Source == "" || a.QueryAPI == "" {
			t.Fatalf("incomplete assertion: %+v", a)
		}
		if a.QueryAPI != "prometheus" && a.QueryAPI != "loki" {
			t.Fatalf("assertion %q has invalid query API %q", a.Mode, a.QueryAPI)
		}
		if a.Direction != DirectionIncrease && a.Direction != DirectionDecrease {
			t.Fatalf("assertion %q has invalid direction %q", a.Mode, a.Direction)
		}
	}
}
