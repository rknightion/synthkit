// SPDX-License-Identifier: AGPL-3.0-only

package nodeexp

import (
	"testing"

	"github.com/rknightion/synthkit/internal/state"
)

// TestEmitMacOSMemorySubset asserts that the macOS-specific memory gauges are emitted
// and that the Linux-only node_memory_MemTotal_bytes is NOT emitted.
func TestEmitMacOSMemorySubset(t *testing.T) {
	st := state.NewState()
	base := map[string]string{"job": "integrations/macos-node", "instance": "alex"}
	top := HostTopology{
		Hostname: "alex",
		NumCPU:   8,
		MemTotal: 16 * 1024 * 1024 * 1024, // 16 GiB
		Disks:    []string{"disk0"},
		NICs:     []NIC{{Name: "en0", SpeedBytes: 1e9}},
		FS:       FSMount{Device: "/dev/disk1s1", FSType: "apfs", Mountpoint: "/", SizeBytes: 500 * 1024 * 1024 * 1024},
		OS: OSInfo{
			ID:         "darwin",
			Name:       "macOS",
			PrettyName: "macOS 14.5",
			VersionID:  "14.5",
			Kernel:     "23.5.0",
			Machine:    "arm64",
		},
		BootTime: 1718000000,
	}

	EmitMacOS(st, base, top, ProfileIntegration, 0.5, 60, 2, testEngine())
	series := st.Collect(testNow())

	names := make(map[string]bool)
	for _, s := range series {
		names[s.Name] = true
	}

	// macOS-specific memory names MUST be present.
	macosMemNames := []string{
		"node_memory_total_bytes",
		"node_memory_compressed_bytes",
		"node_memory_internal_bytes",
		"node_memory_purgeable_bytes",
		"node_memory_wired_bytes",
		"node_memory_swap_total_bytes",
		"node_memory_swap_used_bytes",
	}
	for _, nm := range macosMemNames {
		if !names[nm] {
			t.Errorf("EmitMacOS must emit %q, but it was absent", nm)
		}
	}

	// Linux-only memory name must NOT be present.
	if names["node_memory_MemTotal_bytes"] {
		t.Error("EmitMacOS must NOT emit node_memory_MemTotal_bytes (Linux-only)")
	}
}

// TestEmitMacOSMemoryMagnitudes asserts the macOS memory gauge magnitudes are in plausible
// ratio bands vs the a homelab reference host (host-capture.md "macOS memory
// metrics"): 32 GiB total → wired≈3.8 GiB (~12%), compressed≈4.2 GiB (~13%),
// internal≈7.7 GiB (~24%), purgeable≈89 MB (~0.3%), swap_used≈near-zero.
func TestEmitMacOSMemoryMagnitudes(t *testing.T) {
	const total = 32 * 1024 * 1024 * 1024 // match captured alex
	st := state.NewState()
	base := map[string]string{"job": "integrations/macos-node", "instance": "alex"}
	top := HostTopology{Hostname: "alex", NumCPU: 8, MemTotal: total,
		Disks: []string{"disk0"}, NICs: []NIC{{Name: "en0", SpeedBytes: 1e9}},
		FS: FSMount{Device: "/dev/disk1s1", FSType: "apfs", Mountpoint: "/", SizeBytes: 500 << 30}}
	EmitMacOS(st, base, top, ProfileIntegration, 0.5, 60, 2, testEngine())

	val := map[string]float64{}
	for _, s := range st.Collect(testNow()) {
		val[s.Name] = s.Value
	}

	check := func(name string, loFrac, hiFrac float64) {
		v := val[name]
		lo, hi := total*loFrac, total*hiFrac
		if v < lo || v > hi {
			t.Errorf("%s = %.0f (%.1f%% of total), want within [%.1f%%, %.1f%%]",
				name, v, v/total*100, loFrac*100, hiFrac*100)
		}
	}
	// Captured alex: wired ~12%, compressed ~13%, internal ~24%, purgeable ~0.3%.
	check("node_memory_wired_bytes", 0.08, 0.20)
	check("node_memory_compressed_bytes", 0.07, 0.20)
	check("node_memory_internal_bytes", 0.15, 0.35)
	check("node_memory_purgeable_bytes", 0.0005, 0.03)

	// swap_used must be near-zero (capture: ~192 KB on a 32 GiB host) — well under 64 MiB.
	if su := val["node_memory_swap_used_bytes"]; su > 64*1024*1024 {
		t.Errorf("node_memory_swap_used_bytes = %.0f, want near-zero (< 64 MiB) per capture", su)
	}
}

// TestEmitMacOSCPUCounter asserts that node_cpu_seconds_total is emitted as a
// monotonic counter (calling twice on the same state, the value grows).
func TestEmitMacOSCPUCounter(t *testing.T) {
	st := state.NewState()
	base := map[string]string{"job": "integrations/macos-node", "instance": "alex"}
	top := HostTopology{
		Hostname: "alex",
		NumCPU:   2,
		MemTotal: 8 * 1024 * 1024 * 1024,
		Disks:    []string{"disk0"},
		NICs:     []NIC{{Name: "en0", SpeedBytes: 1e9}},
		FS:       FSMount{Device: "/dev/disk1s1", FSType: "apfs", Mountpoint: "/", SizeBytes: 100 * 1024 * 1024 * 1024},
	}
	eng := testEngine()
	now := testNow()

	EmitMacOS(st, base, top, ProfileIntegration, 0.5, 60, 2, eng)
	after1 := st.Collect(now)

	EmitMacOS(st, base, top, ProfileIntegration, 0.5, 60, 2, eng)
	after2 := st.Collect(now)

	// Find the cpu seconds total value in both collections and verify it grew.
	findCPU := func(series []interface{ getName() string }) float64 { return 0 }
	_ = findCPU

	// SUM across all node_cpu_seconds_total series (per cpu × mode). state.Collect is
	// map-ordered, so reading a single series ([0] / break) compares different cores between
	// collections and flakes — sum them (documented Collect map-order gotcha).
	var v1, v2 float64
	for _, s := range after1 {
		if s.Name == "node_cpu_seconds_total" {
			v1 += s.Value
		}
	}
	for _, s := range after2 {
		if s.Name == "node_cpu_seconds_total" {
			v2 += s.Value
		}
	}

	if v1 <= 0 {
		t.Error("node_cpu_seconds_total should be > 0 after first EmitMacOS call")
	}
	if v2 <= v1 {
		t.Errorf("node_cpu_seconds_total should grow on second call: v1=%v, v2=%v", v1, v2)
	}
}

// TestEmitMacOSAllNamesInKeepSet asserts that every series emitted by EmitMacOS
// is in the macOS integration allowlist (no invented names).
func TestEmitMacOSAllNamesInKeepSet(t *testing.T) {
	st := state.NewState()
	base := map[string]string{"job": "integrations/macos-node", "instance": "alex"}
	top := HostTopology{
		Hostname: "alex",
		NumCPU:   4,
		MemTotal: 16 * 1024 * 1024 * 1024,
		Disks:    []string{"disk0"},
		NICs:     []NIC{{Name: "en0", SpeedBytes: 1e9}},
		FS:       FSMount{Device: "/dev/disk1s1", FSType: "apfs", Mountpoint: "/", SizeBytes: 500 * 1024 * 1024 * 1024},
	}

	EmitMacOS(st, base, top, ProfileIntegration, 0.5, 60, 2, testEngine())
	series := st.Collect(testNow())

	ks := macosKeepSet(ProfileIntegration)
	for _, s := range series {
		if !ks[s.Name] {
			t.Errorf("EmitMacOS emitted %q which is NOT in macosKeepSet(ProfileIntegration)", s.Name)
		}
	}
}

// TestEmitMacOSNoEmptyLabelValues asserts that no emitted series carries an
// empty string as any label value (violates the absent-dimension rule).
func TestEmitMacOSNoEmptyLabelValues(t *testing.T) {
	st := state.NewState()
	base := map[string]string{"job": "integrations/macos-node", "instance": "alex"}
	top := HostTopology{
		Hostname: "alex",
		NumCPU:   4,
		MemTotal: 16 * 1024 * 1024 * 1024,
		Disks:    []string{"disk0"},
		NICs:     []NIC{{Name: "en0", SpeedBytes: 1e9}},
		FS:       FSMount{Device: "/dev/disk1s1", FSType: "apfs", Mountpoint: "/", SizeBytes: 500 * 1024 * 1024 * 1024},
		OS: OSInfo{
			ID:         "darwin",
			Name:       "macOS",
			PrettyName: "macOS 14.5",
			VersionID:  "14.5",
			Kernel:     "23.5.0",
			Machine:    "arm64",
		},
	}

	EmitMacOS(st, base, top, ProfileIntegration, 0.5, 60, 2, testEngine())
	series := st.Collect(testNow())

	for _, s := range series {
		for k, v := range s.Labels {
			if v == "" {
				t.Errorf("series %q has empty label value for key %q (violates absent-dimension rule)", s.Name, k)
			}
		}
	}
}

// TestEmitMacOSWritesSeries is the basic smoke test: EmitMacOS emits at least one series.
func TestEmitMacOSWritesSeries(t *testing.T) {
	st := state.NewState()
	base := map[string]string{"job": "integrations/macos-node", "instance": "alex"}
	top := HostTopology{
		Hostname: "alex",
		NumCPU:   2,
		MemTotal: 8 * 1024 * 1024 * 1024,
		Disks:    []string{"disk0"},
		NICs:     []NIC{{Name: "en0", SpeedBytes: 1e9}},
		FS:       FSMount{Device: "/dev/disk1s1", FSType: "apfs", Mountpoint: "/", SizeBytes: 100 * 1024 * 1024 * 1024},
	}

	EmitMacOS(st, base, top, ProfileIntegration, 0.5, 60, 2, testEngine())
	series := st.Collect(testNow())

	if len(series) == 0 {
		t.Fatal("EmitMacOS emitted no series")
	}
}
