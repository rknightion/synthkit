// SPDX-License-Identifier: AGPL-3.0-only

package e2e

import "testing"

func TestE2EAgentFixtureRequiresExplicitOptIn(t *testing.T) {
	for _, raw := range []string{"", "false"} {
		names, sources := e2eBlueprintConfig(raw)
		if names != e2eBlueprint {
			t.Errorf("raw=%q: names=%q, want %q", raw, names, e2eBlueprint)
		}
		if _, ok := sources[e2eAgentFixture]; ok {
			t.Errorf("raw=%q: agent fixture present without explicit opt-in", raw)
		}
	}

	names, sources := e2eBlueprintConfig("true")
	if names != e2eBlueprint+","+e2eAgentFixture {
		t.Errorf("raw=true: names=%q, want explicit agent topology", names)
	}
	if _, ok := sources[e2eAgentFixture]; !ok {
		t.Error("raw=true: agent fixture missing after explicit opt-in")
	}
}
