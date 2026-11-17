// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/control"
)

// TestApplyControlExplicitZeroVolume pins SKT-0147 through the runner: an explicit control
// volume of 0 reaches every blueprint engine as a zero multiplier (never the unset 1.0), and
// restoring a non-zero value restores scaling.
func TestApplyControlExplicitZeroVolume(t *testing.T) {
	r := newTestRunnerWithBlueprint(t, "starter", []string{"starter-api"})
	eng := r.bps[0].eng
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	st := control.DefaultState()
	st.VolumeMultiplier = 0
	r.ApplyControl(st)
	if got := eng.VolumeMultiplier(); got != 0 {
		t.Fatalf("engine volume after ApplyControl(0) = %v, want 0", got)
	}
	if got := eng.Factor(now, 1.0, false); got != 0 {
		t.Fatalf("Factor at volume 0 = %v, want 0", got)
	}
	if got := r.VolumeMultiplier(); got != 0 {
		t.Fatalf("runner VolumeMultiplier gauge = %v, want 0", got)
	}

	r.ApplyControl(control.DefaultState())
	if got := eng.VolumeMultiplier(); got != 1 {
		t.Fatalf("engine volume after restoring default = %v, want 1", got)
	}
	if got := eng.Factor(now, 1.0, false); got <= 0 {
		t.Fatalf("Factor after restoring volume = %v, want > 0", got)
	}
}
