// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"encoding/json"
	"testing"
)

// TestZeroKindPartJSONIsLegacy pins that a Part without an explicit kind marshals exactly as it
// did before Kind existed: the app pipeline sizes records from json.Marshal length, so an extra
// key would silently change legacy usage and budget numbers. A set Kind round-trips.
func TestZeroKindPartJSONIsLegacy(t *testing.T) {
	g := Generation{Input: []Message{{Role: RoleUser, Parts: []Part{{Text: "hi", ProviderType: "text"}}}}}
	b, err := json.Marshal(g.Input)
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"Role":"MESSAGE_ROLE_USER","Name":"","Parts":[{"ProviderType":"text","Text":"hi","Thinking":"","ToolCall":null,"ToolResult":null,"Media":null}]}]`
	if string(b) != want {
		t.Fatalf("zero-Kind part JSON changed\ngot  %s\nwant %s", b, want)
	}
	k, err := json.Marshal(Part{Kind: PartKindThinking})
	if err != nil {
		t.Fatal(err)
	}
	var back Part
	if err := json.Unmarshal(k, &back); err != nil || back.Kind != PartKindThinking {
		t.Fatalf("set Kind did not round-trip: %s %v", k, err)
	}
}
