// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/beyla"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/shape"
)

// TestMinterVolumeZeroPausesRequests pins SKT-0147: a master volume of exactly 0 mints no
// requests (rps 0, not the off_peak_rps floor), while any non-zero volume keeps the unchanged
// off_peak -> peak interpolation by the shape factor.
func TestMinterVolumeZeroPausesRequests(t *testing.T) {
	m := testMinter()
	// A weekday night: the factor is well below 1, so a volume of 2 is visibly not clamped.
	now := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	eng := shape.New("", nil)

	eng.SetVolumeMultiplier(0)
	if got := m.rpsAt(now, eng); got != 0 {
		t.Fatalf("rps at volume 0 = %v, want 0", got)
	}
	if got := m.expectedVolume(now, 30, eng); got != 0 {
		t.Fatalf("expected volume at volume 0 = %v, want 0", got)
	}
	for range 50 {
		if n := len(m.Mint(now, 30, eng)); n != 0 {
			t.Fatalf("minted %d requests at volume 0, want 0", n)
		}
	}

	off, peak := m.cfg.Traffic.OffPeakRPS, m.cfg.Traffic.PeakRPS
	for _, vol := range []float64{1, 2} {
		eng.SetVolumeMultiplier(vol)
		f := math.Min(math.Max(eng.Factor(now, m.weight, m.nonProd), 0), 1)
		want := off + (peak-off)*f
		if got := m.rpsAt(now, eng); got != want {
			t.Fatalf("rps at volume %v = %v, want unchanged interpolation %v", vol, got, want)
		}
		if got := m.rpsAt(now, eng); got < off {
			t.Fatalf("rps at volume %v = %v fell below off_peak %v", vol, got, off)
		}
	}
}

// TestTickVolumeZeroEmitsNoRequestVolume: with the master volume at 0 the metric lane counts no
// calls, even for a prod service whose trough normally keeps one call alive; a non-zero volume
// still counts them.
func TestTickVolumeZeroEmitsNoRequestVolume(t *testing.T) {
	sum := func(vol float64) float64 {
		w, led := buildWS(t, nil)
		now := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		world.Ledger = led
		world.Shape.SetVolumeMultiplier(vol)
		if err := w.Tick(context.Background(), now, world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		total := 0.0
		for _, s := range mc.Find("traces_spanmetrics_calls_total") {
			total += s.Value
		}
		return total
	}
	if got := sum(0); got != 0 {
		t.Fatalf("calls_total at volume 0 = %v, want 0", got)
	}
	if got := sum(1); got <= 0 {
		t.Fatalf("calls_total at volume 1 = %v, want > 0", got)
	}
	if got := sum(2); got <= 0 {
		t.Fatalf("calls_total at volume 2 = %v, want > 0", got)
	}
}

// TestPyroscopeVolumeZeroEmitsNoProfiles: at master volume 0 the profile lane emits nothing (the
// flame builder reads a zero load factor as full load); a non-zero volume still emits.
func TestPyroscopeVolumeZeroEmitsNoProfiles(t *testing.T) {
	count := func(vol float64) int {
		w, led := buildPyroWS(t)
		now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
		mintNonEmpty(t, led, now, false)
		pc := &pyroCapture{}
		world := coretest.World(&coretest.MetricCapture{}, nil, nil)
		world.Ledger = led
		world.Pyroscope = pc
		world.Shape.SetVolumeMultiplier(vol)
		if err := w.Tick(context.Background(), now, world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		return len(pc.all())
	}
	if got := count(0); got != 0 {
		t.Fatalf("profile series at volume 0 = %d, want 0", got)
	}
	for _, vol := range []float64{1, 2} {
		if got := count(vol); got == 0 {
			t.Fatalf("profile series at volume %v = 0, want > 0", vol)
		}
	}
}

// TestBeylaLaneVolumeZeroEmitsNoCalls: the Beyla observation lane counts no calls and no
// keep-alive call at master volume 0; a non-zero volume still counts them.
func TestBeylaLaneVolumeZeroEmitsNoCalls(t *testing.T) {
	sum := func(vol float64) (calls, red float64) {
		w := buildBeylaWS(t, "kubernetes", "ebpf_only")
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		world.Shape.SetVolumeMultiplier(vol)
		now := time.Date(2026, 6, 15, 3, 0, 0, 0, time.UTC)
		w.beylaLane().tick(now, world)
		if err := world.Metrics.Write(context.Background(), w.st.Collect(now)); err != nil {
			t.Fatal(err)
		}
		for _, s := range mc.Find("traces_spanmetrics_calls_total") {
			calls += s.Value
		}
		for _, s := range mc.Find(beyla.MetricHTTPServerDuration + "_count") {
			red += s.Value
		}
		return calls, red
	}
	if calls, red := sum(0); calls != 0 || red != 0 {
		t.Fatalf("beyla calls=%v red_count=%v at volume 0, want 0", calls, red)
	}
	for _, vol := range []float64{1, 2} {
		if calls, red := sum(vol); calls <= 0 || red <= 0 {
			t.Fatalf("beyla calls=%v red_count=%v at volume %v, want > 0", calls, red, vol)
		}
	}
}
