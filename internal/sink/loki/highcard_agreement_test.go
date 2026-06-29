// SPDX-License-Identifier: AGPL-3.0-only

package loki

import (
	"testing"

	"github.com/rknightion/synthkit/internal/highcard"
)

// TestForbiddenStreamLabelsMatchHighcard pins the Loki stream-label guard (I14/I15) to the
// canonical high-card set in internal/highcard, so the Loki sink, the promrw sink, and the
// telemetryspec DSL capability matrix can never silently drift apart.
func TestForbiddenStreamLabelsMatchHighcard(t *testing.T) {
	want := highcard.Set()
	if len(DefaultForbiddenStreamLabels) != len(want) {
		t.Fatalf("DefaultForbiddenStreamLabels has %d keys, highcard has %d",
			len(DefaultForbiddenStreamLabels), len(want))
	}
	for _, k := range DefaultForbiddenStreamLabels {
		if !highcard.Contains(k) {
			t.Errorf("loki forbidden key %q not in canonical highcard set", k)
		}
	}
}
