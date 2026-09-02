// SPDX-License-Identifier: AGPL-3.0-only

package e2e

import "strings"

// e2eBlueprint is the non-agent smoke blueprint: it emits RW2 metrics, native
// OTLP metrics and traces, and Loki logs.
const e2eBlueprint = "otlp-native"

// e2eAgentFixture supplies the three Sigil lanes from a test-only fixture.
const e2eAgentFixture = "e2e-agents"

// e2eAgentSelected reports whether the agent fixture is in a selected blueprint set. The
// fixture is the only source of the three sigil lanes, so a suite asserting lane completeness
// must require sigil receipts only when it is selected, and must require their ABSENCE when it
// is not — a shipped blueprint that reintroduced an agent fleet is the defect this catches.
func e2eAgentSelected(names string) bool {
	for _, name := range strings.Split(names, ",") {
		if strings.TrimSpace(name) == e2eAgentFixture {
			return true
		}
	}
	return false
}

func e2eBlueprintConfig(raw string) (string, map[string]string) {
	sources := map[string]string{e2eBlueprint: "../blueprints/" + e2eBlueprint + ".yaml"}
	switch strings.TrimSpace(raw) {
	case "true":
		sources[e2eAgentFixture] = "../e2e/fixtures/" + e2eAgentFixture + ".yaml"
		return e2eBlueprint + "," + e2eAgentFixture, sources
	case "", "false":
		return e2eBlueprint, sources
	default:
		panic("SYNTHKIT_E2E_INCLUDE_AGENT must be true or false")
	}
}
