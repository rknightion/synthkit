// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"math"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// TestAppMinterFullVolume verifies the app workload mints the TRUE request volume
// (rps × tickSec), not a clamped narrative sample — so every request emits a trace and the
// trace volume matches the rps-derived metric volume (1 request ⇒ 1 traceparent).
func TestAppMinterFullVolume(t *testing.T) {
	m := &minter{traffic: Traffic{OffPeakRPS: 5, PeakRPS: 1000}, weight: 1.0}
	eng := shape.New("", nil)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC) // business-hours plateau

	for _, tickSec := range []float64{1, 5, 30, 60} {
		rps := m.rpsAt(now, eng)
		got := m.expectedVolume(now, tickSec, eng)
		if want := rps * tickSec; math.Abs(got-want) > 1e-9 {
			t.Fatalf("expectedVolume(%.0fs)=%.6f want rps×tick=%.6f", tickSec, got, want)
		}
	}
	// ≥ off_peak(5)×60 = 300, far above the old narrative-sample ceiling of 40.
	if got := m.expectedVolume(now, 60, eng); got <= 40 {
		t.Fatalf("expectation %.6f appears clamped (want full rps×60)", got)
	}
}

// TestDrawModelVolumeWeighted verifies that drawModel selects high-VolumeWeight models more
// frequently than low-weight ones. Two models: "gpt-4o-mini" (VolumeWeight=6.0) and
// "anthropic.claude-opus-4-6-v1" (VolumeWeight=0.6). Over N draws the high-weight model
// must appear ≥ 2× as often as the low-weight one (expected ratio ≈ 10×).
func TestDrawModelVolumeWeighted(t *testing.T) {
	models := []ModelChoice{
		{Model: "gpt-4o-mini", Provider: "azure-openai"},             // VolumeWeight=6.0
		{Model: "anthropic.claude-opus-4-6-v1", Provider: "bedrock"}, // VolumeWeight=0.6
	}
	m := &minter{models: models}
	eng := shape.New("", nil)

	counts := map[string]int{}
	const N = 1000
	for range N {
		mc := m.drawModel(eng)
		counts[mc.Model]++
	}

	high := counts["gpt-4o-mini"]
	low := counts["anthropic.claude-opus-4-6-v1"]
	if high+low != N {
		t.Fatalf("unexpected total draws: high=%d low=%d sum=%d want %d", high, low, high+low, N)
	}
	// Uniform draw would give ~50/50; weighted should give ≥ 2× ratio (expected ~10×).
	if high < 2*low {
		t.Fatalf("drawModel is not volume-weighted: high=%d low=%d ratio=%.2f (want ≥ 2.0)", high, low, float64(high)/float64(low+1))
	}
}

// TestAppMinterCadenceInvariance: minting on a fast cadence mints proportionally less each
// tick, so the long-run rate is cadence-independent (ARCHITECTURE I10). The rps×tickSec form
// is exactly linear in tickSec, so this holds without the old reference-window scaling.
func TestAppMinterCadenceInvariance(t *testing.T) {
	m := &minter{traffic: Traffic{OffPeakRPS: 5, PeakRPS: 50}, weight: 1.0}
	eng := shape.New("", nil)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	oneSlow := m.expectedVolume(now, ledger.ReferenceTickSeconds, eng)
	sixFast := 0.0
	for range 6 {
		sixFast += m.expectedVolume(now, ledger.ReferenceTickSeconds/6, eng)
	}
	if math.Abs(oneSlow-sixFast) > 1e-9 {
		t.Fatalf("cadence variance: one slow tick %.6f vs six fast %.6f", oneSlow, sixFast)
	}
	if oneSlow <= 0 {
		t.Fatalf("expected positive volume at plateau, got %.6f", oneSlow)
	}
}
