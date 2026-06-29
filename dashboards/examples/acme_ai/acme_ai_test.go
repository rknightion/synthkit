// SPDX-License-Identifier: AGPL-3.0-only

package acme_ai

import "testing"

func TestAppSel(t *testing.T) {
	if got := AppSel(""); got != `{blueprint="$scenario",deployment_environment_name=~"$env"}` {
		t.Errorf("AppSel() = %q", got)
	}
	if got := AppSel(`service="acme-backend"`); got != `{blueprint="$scenario",deployment_environment_name=~"$env",service="acme-backend"}` {
		t.Errorf("AppSel(extra) = %q", got)
	}
}

func TestIntSel(t *testing.T) {
	if got := IntSel(""); got != `{env=~"$env"}` {
		t.Errorf("IntSel() = %q", got)
	}
	if got := IntSel(`ai_model="$ai_model"`); got != `{env=~"$env",ai_model="$ai_model"}` {
		t.Errorf("IntSel(extra) = %q", got)
	}
}

func TestLangsmithSel(t *testing.T) {
	// WS1 excludes WS2's -gw projects.
	if got := LangsmithSel(true, ""); got != `{env=~"$env",project!~".+-gw"}` {
		t.Errorf("LangsmithSel(WS1) = %q", got)
	}
	// WS2 keeps only -gw projects.
	if got := LangsmithSel(false, `use_case="$use_case"`); got != `{env=~"$env",project=~".+-gw",use_case="$use_case"}` {
		t.Errorf("LangsmithSel(WS2) = %q", got)
	}
}

// The family-name reconciliation constants must hold the SYNTHKIT-emitted names —
// never a customer-prefixed token (which would yield empty panels).
func TestMetricConstantsAreGeneric(t *testing.T) {
	for _, m := range []string{MetricContentLeakTest, MetricContentDropped, MetricPollerLastOK, MetricPollerErrors, MetricPollerWindowLag} {
		if len(m) == 0 {
			t.Fatal("empty metric constant")
		}
		if hasPrefix(m, "acme_") {
			t.Errorf("metric constant %q must be the synthkit-emitted name, not an acme_* token", m)
		}
	}
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
