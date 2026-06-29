// SPDX-License-Identifier: AGPL-3.0-only

// physics.go — shared deterministic helpers for node/windows/macos emitters.
// Ported from internal/construct/k8scluster/nodeexporter.go (neHash, nodeRxBase, cpu
// fraction math) and windowsexporter.go (weHash — identical polynomial). All helpers
// are seeded only by the caller-supplied strings; no global rand (invariant I12).
package nodeexp

import (
	"github.com/rknightion/synthkit/internal/state"
)

// hostHash returns a stable per-(seed,key) float64 in [0,1) for deterministic-plausible
// gauge values. This is the FNV-variant shared by both neHash (nodeexporter.go) and
// weHash (windowsexporter.go) — same polynomial (FNV-1a 64-bit with the FNV offset
// basis 1469598103934665603 and prime 1099511628211).
//
// The seed+key inputs are caller-controlled so the result is anchored to the host identity
// and the specific quantity being randomised, never to a global RNG state (I12).
func hostHash(seed, key string) float64 {
	h := uint64(1469598103934665603)
	for _, c := range seed + "|" + key {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return float64(h%100000) / 100000.0
}

// nodeRxBase returns the per-device network-receive bytes baseline for one tick.
// Ported verbatim from nodeexporter.go:nodeRxBase. lo carries modest loopback
// traffic; all other devices carry node-level external traffic.
//
// Parameters:
//
//	dev     — device name (e.g. "lo", "eth0")
//	factor  — diurnal load factor from shape.Engine
//	noise   — a pre-computed noise multiplier (shape.Engine.Noise(0.3) at the call site)
//	scale   — tickSec/30 (keeps counters proportional to tick length)
func nodeRxBase(dev string, factor, noise, scale float64) float64 {
	var v float64
	if dev == "lo" {
		v = (1*1024*1024 + factor*4*1024*1024) * noise * scale
	} else {
		v = (5*1024*1024 + factor*80*1024*1024) * noise * scale
	}
	if v < 0 {
		v = 1024
	}
	return v
}

// CPUModeFractions holds the per-mode fraction of the total CPU-seconds delta for
// one CPU per tick. Each fraction is the proportion of tickSec that mode consumed;
// fractions should sum to 1.0 (within floating-point rounding).
//
// The busy fractions are split across the six active modes; idleFrac accounts for
// the remaining time. nice/steal are tiny slivers of the busy budget.
type CPUModeFractions struct {
	Idle    float64
	User    float64
	System  float64
	IOWait  float64
	SoftIRQ float64
	IRQ     float64
	Nice    float64
	Steal   float64
}

// cpuFractions returns the per-mode fractions of tickSec for node_cpu_seconds_total.
// Ported from the switch in emitNodeExporter's cpu loop (nodeexporter.go lines ~333–346).
//
// busyFrac is the proportion of the CPU that is not idle (0–1).
// noiseIdle / noiseBusy are pre-computed shape.Noise values (the caller owns the
// Engine so it can pass different noise values per CPU if desired).
func cpuFractions(busyFrac, noiseIdle, noiseUser, noiseSys, noiseIO, noiseSoftIRQ, noiseIRQ, noiseNiceSteal float64) CPUModeFractions {
	idleFrac := 1 - busyFrac
	f := CPUModeFractions{
		Idle:    idleFrac * noiseIdle,
		User:    busyFrac * 0.55 * noiseUser,
		System:  busyFrac * 0.25 * noiseSys,
		IOWait:  busyFrac * 0.10 * noiseIO,
		SoftIRQ: busyFrac * 0.05 * noiseSoftIRQ,
		IRQ:     busyFrac * 0.03 * noiseIRQ,
		Nice:    0.001 * noiseNiceSteal,
		Steal:   0.001 * noiseNiceSteal,
	}
	// clamp negatives (can occur when noise < 0 with high jitter)
	if f.Idle < 0 {
		f.Idle = 0
	}
	if f.User < 0 {
		f.User = 0
	}
	if f.System < 0 {
		f.System = 0
	}
	if f.IOWait < 0 {
		f.IOWait = 0
	}
	if f.SoftIRQ < 0 {
		f.SoftIRQ = 0
	}
	if f.IRQ < 0 {
		f.IRQ = 0
	}
	if f.Nice < 0 {
		f.Nice = 0
	}
	if f.Steal < 0 {
		f.Steal = 0
	}
	return f
}

// keepWriter returns two closures (set, add) that write to st only when keep[name]
// is true. The keep map is passed in by the caller — it is the pre-built result of
// keepSet(prof) or keepSetCadvisor(prof), evaluated ONCE by the emitter before the
// emission loop.
//
// This decouples physics.go from profiles.go, so both files can be owned by
// independent lanes without editing each other.
//
// Usage:
//
//	ks := keepSet(prof)
//	set, add := keepWriter(st, ks)
//	set("node_load1", base, 1.5)
//	add("node_cpu_seconds_total", cpuLbls, delta)
func keepWriter(st *state.State, keep map[string]bool) (
	set func(name string, lbls map[string]string, v float64),
	add func(name string, lbls map[string]string, delta float64),
) {
	set = func(name string, lbls map[string]string, v float64) {
		if keep[name] {
			st.Set(name, lbls, v)
		}
	}
	add = func(name string, lbls map[string]string, delta float64) {
		if keep[name] {
			st.Add(name, lbls, delta)
		}
	}
	return set, add
}
