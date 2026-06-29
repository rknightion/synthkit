// SPDX-License-Identifier: AGPL-3.0-only

package nodeexp

import (
	"math"
	"testing"

	"github.com/rknightion/synthkit/internal/state"
)

// ── hostHash tests ─────────────────────────────────────────────────────────

func TestPhysicsHashDeterministic(t *testing.T) {
	v1 := hostHash("seed", "camden")
	v2 := hostHash("seed", "camden")
	if v1 != v2 {
		t.Errorf("hostHash not deterministic: %v != %v", v1, v2)
	}
}

func TestPhysicsHashRange(t *testing.T) {
	for _, key := range []string{"camden", "host-b", "oli", "alex", "lo", "eth0", "nvme0n1"} {
		v := hostHash("bp:host", key)
		if v < 0 || v >= 1 {
			t.Errorf("hostHash(%q) = %v, want [0,1)", key, v)
		}
	}
}

func TestPhysicsHashDiffersForDifferentKeys(t *testing.T) {
	a := hostHash("seed", "camden")
	b := hostHash("seed", "host-b")
	if a == b {
		t.Errorf("hostHash should differ for different keys, both returned %v", a)
	}
}

func TestPhysicsHashDiffersForDifferentSeeds(t *testing.T) {
	a := hostHash("seedA", "camden")
	b := hostHash("seedB", "camden")
	if a == b {
		t.Errorf("hostHash should differ for different seeds, both returned %v", a)
	}
}

// ── cpuFractions tests ─────────────────────────────────────────────────────

func TestCpuFractionsSum(t *testing.T) {
	// With uniform noise=1.0, the busy-mode fractions sum to busyFrac * (0.55+0.25+0.10+0.05+0.03)
	// = busyFrac * 0.98, plus idle = idleFrac * 1.0, plus two nice/steal slivers of 0.001 each.
	// The total is NOT exactly 1.0 by design (nice/steal are additive, not subtracted from busy)
	// but it must be in a plausible range.
	busyFrac := 0.6
	f := cpuFractions(busyFrac,
		1.0, // noiseIdle
		1.0, // noiseUser
		1.0, // noiseSys
		1.0, // noiseIO
		1.0, // noiseSoftIRQ
		1.0, // noiseIRQ
		1.0, // noiseNiceSteal
	)

	total := f.Idle + f.User + f.System + f.IOWait + f.SoftIRQ + f.IRQ + f.Nice + f.Steal
	// With uniform noise the fractions should be well under 2.0 and at least 0.5
	if total < 0.5 || total > 2.0 {
		t.Errorf("cpuFractions total %v out of plausible range [0.5,2.0]", total)
	}

	// The busy sub-modes must sum to busyFrac * (0.55+0.25+0.10+0.05+0.03) = busyFrac*0.98
	busyTotal := f.User + f.System + f.IOWait + f.SoftIRQ + f.IRQ
	wantBusy := busyFrac * 0.98
	if math.Abs(busyTotal-wantBusy) > 1e-9 {
		t.Errorf("busy-mode sum = %v, want %v", busyTotal, wantBusy)
	}
}

func TestCpuFractionsIdleIncreasesBelowBusy(t *testing.T) {
	// At busyFrac=0, idle=1*noiseIdle; at busyFrac=0.8, idle=0.2*noiseIdle.
	f0 := cpuFractions(0.0, 1, 1, 1, 1, 1, 1, 1)
	f8 := cpuFractions(0.8, 1, 1, 1, 1, 1, 1, 1)
	if f0.Idle <= f8.Idle {
		t.Errorf("idle should be higher at lower busyFrac: f0.Idle=%v, f8.Idle=%v", f0.Idle, f8.Idle)
	}
}

func TestCpuFractionsNoNegative(t *testing.T) {
	// Even with noise < 1, all fractions must be >= 0.
	f := cpuFractions(0.5, 0.5, 0.3, 0.2, 0.1, 0.05, 0.01, 0.1)
	for name, v := range map[string]float64{
		"Idle": f.Idle, "User": f.User, "System": f.System,
		"IOWait": f.IOWait, "SoftIRQ": f.SoftIRQ, "IRQ": f.IRQ,
		"Nice": f.Nice, "Steal": f.Steal,
	} {
		if v < 0 {
			t.Errorf("cpuFractions.%s = %v, want >= 0", name, v)
		}
	}
}

// ── keepWriter tests ───────────────────────────────────────────────────────

func TestKeepWriterDropsUnknownName(t *testing.T) {
	st := state.NewState()
	keep := map[string]bool{"node_load1": true}
	set, add := keepWriter(st, keep)

	// "node_load15" is not in keep → should be silently dropped.
	set("node_load15", map[string]string{"instance": "camden"}, 9.9)
	add("node_load15_counter", map[string]string{"instance": "camden"}, 1.0)

	series := st.Collect(testNow())
	for _, s := range series {
		if s.Name == "node_load15" || s.Name == "node_load15_counter" {
			t.Errorf("keepWriter should have dropped %q but it was written", s.Name)
		}
	}
}

func TestKeepWriterWritesKeptName(t *testing.T) {
	st := state.NewState()
	keep := map[string]bool{"node_load1": true, "node_cpu_seconds_total": true}
	set, add := keepWriter(st, keep)

	set("node_load1", map[string]string{"instance": "camden"}, 1.23)
	add("node_cpu_seconds_total", map[string]string{"instance": "camden", "cpu": "0", "mode": "idle"}, 30.0)

	series := st.Collect(testNow())
	names := make(map[string]bool)
	for _, s := range series {
		names[s.Name] = true
	}
	if !names["node_load1"] {
		t.Error("keepWriter should have written node_load1 (it is in keep)")
	}
	if !names["node_cpu_seconds_total"] {
		t.Error("keepWriter should have written node_cpu_seconds_total (it is in keep)")
	}
}

func TestKeepWriterSetAndAddBothFilter(t *testing.T) {
	st := state.NewState()
	// Only "gauge_kept" and "counter_kept" are in keep.
	keep := map[string]bool{"gauge_kept": true, "counter_kept": true}
	set, add := keepWriter(st, keep)

	set("gauge_kept", map[string]string{"x": "1"}, 42)
	set("gauge_dropped", map[string]string{"x": "2"}, 99)
	add("counter_kept", map[string]string{"x": "3"}, 5)
	add("counter_dropped", map[string]string{"x": "4"}, 7)

	series := st.Collect(testNow())
	names := make(map[string]bool)
	for _, s := range series {
		names[s.Name] = true
	}

	if !names["gauge_kept"] {
		t.Error("gauge_kept must be written")
	}
	if names["gauge_dropped"] {
		t.Error("gauge_dropped must be dropped")
	}
	if !names["counter_kept"] {
		t.Error("counter_kept must be written")
	}
	if names["counter_dropped"] {
		t.Error("counter_dropped must be dropped")
	}
}
