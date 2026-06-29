// SPDX-License-Identifier: AGPL-3.0-only

package promrw

import (
	"testing"

	"github.com/rknightion/synthkit/internal/highcard"
)

// TestForbiddenSetMatchesHighcard pins the promrw Mimir-label guard (M4) to the canonical
// high-card set in internal/highcard, so the promrw sink, the Loki sink, and the telemetryspec
// DSL capability matrix can never silently drift apart.
func TestForbiddenSetMatchesHighcard(t *testing.T) {
	want := highcard.Set()
	if len(uuidClassLabels) != len(want) {
		t.Fatalf("uuidClassLabels has %d keys, highcard has %d", len(uuidClassLabels), len(want))
	}
	for k := range want {
		if _, ok := uuidClassLabels[k]; !ok {
			t.Errorf("highcard key %q missing from promrw uuidClassLabels", k)
		}
	}
}
