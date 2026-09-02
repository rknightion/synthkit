// SPDX-License-Identifier: AGPL-3.0-only

package e2e

import "strings"

// e2eBlueprint is the non-agent smoke blueprint: it emits RW2 metrics, native
// OTLP metrics and traces, and Loki logs.
const e2eBlueprint = "otlp-native"

// e2eAgentFixture supplies the three Sigil lanes from a test-only fixture.
const e2eAgentFixture = "e2e-agents"

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
