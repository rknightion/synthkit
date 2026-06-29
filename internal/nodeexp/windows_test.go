// SPDX-License-Identifier: AGPL-3.0-only

package nodeexp

import (
	"testing"

	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// testWindowsTop returns a representative HostTopology for Windows tests.
func testWindowsTop() HostTopology {
	return HostTopology{
		Hostname: "winhost",
		NumCPU:   4,
		MemTotal: 8 * 1024 * 1024 * 1024,
		Disks:    []string{"C:"},
		NICs:     []NIC{{Name: "Ethernet", SpeedBytes: 1e9}},
		FS:       FSMount{Device: "C:", FSType: "NTFS", Mountpoint: "C:", SizeBytes: 100 * 1024 * 1024 * 1024},
		OS: OSInfo{
			Product:      "Windows Server 2022 Datacenter",
			Version:      "10.0.20348",
			MajorVersion: "10",
			MinorVersion: "0",
			Build:        "20348",
			Revision:     "5139",
		},
		BootTime: 1_748_000_000,
	}
}

// testWindowsBase returns a minimal base label map for Windows tests.
func testWindowsBase() map[string]string {
	return map[string]string{
		"job":      "integrations/windows_exporter",
		"instance": "winhost",
	}
}

// emitWindowsAndCollect calls EmitWindows once and returns all collected series.
func emitWindowsAndCollect(prof Profile) []promrw.Series {
	st := state.NewState()
	EmitWindows(st, testWindowsBase(), testWindowsTop(), prof, 0.5, 60, 2, testEngine())
	return st.Collect(testNow())
}

// seriesByNameWindows indexes collected series by metric name → list of series.
// (mirrors seriesByName from linux_test.go, which is package-internal)
func seriesByNameWindows(series []promrw.Series) map[string][]promrw.Series {
	m := make(map[string][]promrw.Series)
	for _, s := range series {
		m[s.Name] = append(m[s.Name], s)
	}
	return m
}

// TestEmitWindowsCPUTimeCounterWithCoreAndMode asserts that windows_cpu_time_total is
// emitted as a counter with core and mode labels for all expected modes.
func TestEmitWindowsCPUTimeCounterWithCoreAndMode(t *testing.T) {
	series := emitWindowsAndCollect(ProfileIntegration)
	byName := seriesByNameWindows(series)

	cpuSeries, ok := byName["windows_cpu_time_total"]
	if !ok || len(cpuSeries) == 0 {
		t.Fatal("windows_cpu_time_total not emitted")
	}

	expectedModes := map[string]bool{
		"idle": false, "user": false, "privileged": false, "interrupt": false, "dpc": false,
	}
	coresFound := map[string]bool{}

	for _, s := range cpuSeries {
		if s.Kind != promrw.KindCounter {
			t.Errorf("windows_cpu_time_total has Kind=%v, want KindCounter", s.Kind)
		}
		mode, hasMode := s.Labels["mode"]
		if !hasMode || mode == "" {
			t.Errorf("windows_cpu_time_total series missing 'mode' label: %v", s.Labels)
		} else {
			expectedModes[mode] = true
		}
		core, hasCore := s.Labels["core"]
		if !hasCore || core == "" {
			t.Errorf("windows_cpu_time_total series missing 'core' label: %v", s.Labels)
		} else {
			coresFound[core] = true
		}
	}

	for mode, found := range expectedModes {
		if !found {
			t.Errorf("windows_cpu_time_total missing mode=%q", mode)
		}
	}

	// Top topology has NumCPU=4, so we expect 4 distinct cores.
	if len(coresFound) != 4 {
		t.Errorf("windows_cpu_time_total: want 4 distinct cores, got %d: %v", len(coresFound), coresFound)
	}
}

// TestEmitWindowsMemoryTotalGauge asserts windows_memory_physical_total_bytes is emitted
// as a gauge with a positive value.
func TestEmitWindowsMemoryTotalGauge(t *testing.T) {
	series := emitWindowsAndCollect(ProfileIntegration)
	byName := seriesByNameWindows(series)

	s, ok := byName["windows_memory_physical_total_bytes"]
	if !ok || len(s) == 0 {
		t.Fatal("windows_memory_physical_total_bytes not emitted")
	}
	if s[0].Kind != promrw.KindGauge {
		t.Errorf("windows_memory_physical_total_bytes has Kind=%v, want KindGauge", s[0].Kind)
	}
	if s[0].Value <= 0 {
		t.Errorf("windows_memory_physical_total_bytes value=%v, want >0", s[0].Value)
	}
}

// TestEmitWindowsOsInfoAndHostname asserts:
//   - windows_os_info is emitted as a gauge=1 under ProfileIntegration (it IS in the integration allowlist).
//   - windows_os_hostname is emitted as a gauge=1 under ProfileK8s (it is k8s-only per plan Seam 3 note;
//     NOT in the integration allowlist).
func TestEmitWindowsOsInfoAndHostname(t *testing.T) {
	// windows_os_info: in integration allowlist.
	intSeries := emitWindowsAndCollect(ProfileIntegration)
	intByName := seriesByNameWindows(intSeries)

	ss, ok := intByName["windows_os_info"]
	if !ok || len(ss) == 0 {
		t.Error("windows_os_info not emitted under ProfileIntegration")
	} else {
		if ss[0].Kind != promrw.KindGauge {
			t.Errorf("windows_os_info has Kind=%v, want KindGauge", ss[0].Kind)
		}
		if ss[0].Value != 1 {
			t.Errorf("windows_os_info value=%v, want 1 (info series)", ss[0].Value)
		}
	}

	// windows_os_hostname: k8s-only (not in integration allowlist; see plan Seam 3 ⚠ note).
	st := state.NewState()
	EmitWindows(st, testWindowsBase(), testWindowsTop(), ProfileK8s, 0.5, 60, 2, testEngine())
	k8sSeries := st.Collect(testNow())
	k8sByName := seriesByNameWindows(k8sSeries)

	hs, ok := k8sByName["windows_os_hostname"]
	if !ok || len(hs) == 0 {
		t.Error("windows_os_hostname not emitted under ProfileK8s")
	} else {
		if hs[0].Kind != promrw.KindGauge {
			t.Errorf("windows_os_hostname has Kind=%v, want KindGauge", hs[0].Kind)
		}
		if hs[0].Value != 1 {
			t.Errorf("windows_os_hostname value=%v, want 1 (info series)", hs[0].Value)
		}
		if hs[0].Labels["hostname"] != "winhost" {
			t.Errorf("windows_os_hostname: hostname label=%q, want %q", hs[0].Labels["hostname"], "winhost")
		}
	}
}

// TestEmitWindowsAllNamesInKeepSet asserts that every emitted metric name is in
// keepSetWindows(ProfileIntegration).
func TestEmitWindowsAllNamesInKeepSet(t *testing.T) {
	ks := keepSetWindows(ProfileIntegration)
	series := emitWindowsAndCollect(ProfileIntegration)
	if len(series) == 0 {
		t.Fatal("EmitWindows(ProfileIntegration) emitted no series")
	}
	for _, s := range series {
		if !ks[s.Name] {
			t.Errorf("ProfileIntegration: emitted %q which is NOT in keepSetWindows(ProfileIntegration)", s.Name)
		}
	}
}

// windowsCounterNamesFromSource is the authoritative counter set from windowsexporter.go
// (derived by grepping st.Add calls — these are the metrics emitted via state.Add).
var windowsCounterNamesFromSource = []string{
	"windows_cpu_time_total",
	"windows_logical_disk_read_bytes_total",
	"windows_logical_disk_write_bytes_total",
	"windows_net_bytes_received_total",
	"windows_net_bytes_sent_total",
	"windows_net_packets_received_total",
	// capture-confirmed integration extra (counter):
	"windows_system_context_switches_total",
}

// windowsGaugeNamesFromSource is the authoritative gauge set from windowsexporter.go
// (derived by grepping st.Set calls — these are the metrics emitted via state.Set).
var windowsGaugeNamesFromSource = []string{
	"windows_cpu_logical_processor",
	"windows_memory_physical_total_bytes",
	"windows_memory_available_bytes",
	"windows_memory_physical_free_bytes",
	"windows_memory_committed_bytes",
	"windows_os_info",
	"windows_os_hostname",
	"windows_logical_disk_size_bytes",
	"windows_logical_disk_free_bytes",
	"up",
}

// TestEmitWindowsCounterGaugeClassification asserts that every name emitted via st.Add
// in the source (windowsexporter.go) is KindCounter in the ported implementation, and
// every st.Set name is KindGauge. This is the only guard against counter↔gauge flips.
func TestEmitWindowsCounterGaugeClassification(t *testing.T) {
	// Use ProfileK8s to get the full k8s keepset which covers all source-emitted names.
	st := state.NewState()
	EmitWindows(st, testWindowsBase(), testWindowsTop(), ProfileK8s, 0.5, 60, 2, testEngine())
	series := st.Collect(testNow())

	nameKind := make(map[string]promrw.Kind)
	for _, s := range series {
		if existing, seen := nameKind[s.Name]; seen {
			if existing != s.Kind {
				t.Errorf("metric %q has conflicting Kinds: %v and %v", s.Name, existing, s.Kind)
			}
		} else {
			nameKind[s.Name] = s.Kind
		}
	}

	for _, name := range windowsCounterNamesFromSource {
		k, ok := nameKind[name]
		if !ok {
			continue // not in this keepset → skip
		}
		if k != promrw.KindCounter {
			t.Errorf("counter %q classified as Kind=%v (expected KindCounter)", name, k)
		}
	}

	for _, name := range windowsGaugeNamesFromSource {
		k, ok := nameKind[name]
		if !ok {
			continue
		}
		if k != promrw.KindGauge {
			t.Errorf("gauge %q classified as Kind=%v (expected KindGauge)", name, k)
		}
	}

	// Sanity: no name in both sets.
	counterSet := make(map[string]bool, len(windowsCounterNamesFromSource))
	for _, n := range windowsCounterNamesFromSource {
		counterSet[n] = true
	}
	for _, n := range windowsGaugeNamesFromSource {
		if counterSet[n] {
			t.Errorf("test data error: %q in both counter and gauge source sets", n)
		}
	}

	// Capture-confirmed integration extras: verify their kinds under ProfileIntegration.
	intSt := state.NewState()
	EmitWindows(intSt, testWindowsBase(), testWindowsTop(), ProfileIntegration, 0.5, 60, 2, testEngine())
	intKind := make(map[string]promrw.Kind)
	for _, s := range intSt.Collect(testNow()) {
		intKind[s.Name] = s.Kind
	}
	intGauges := []string{
		"windows_service_state", "windows_diskdrive_status",
		"windows_system_processor_queue_length",
		"windows_pagefile_free_bytes", "windows_pagefile_limit_bytes",
		"windows_time_computed_time_offset_seconds", "windows_time_timezone",
	}
	for _, n := range intGauges {
		if k, ok := intKind[n]; ok && k != promrw.KindGauge {
			t.Errorf("integration extra gauge %q classified as Kind=%v", n, k)
		}
	}
	if k, ok := intKind["windows_system_context_switches_total"]; ok && k != promrw.KindCounter {
		t.Errorf("windows_system_context_switches_total classified as Kind=%v, want counter", k)
	}
}

// TestEmitWindowsDiskFreeIsDirectValue asserts that windows_logical_disk_free_bytes emits
// the computed diskFree value directly — NOT diskFree*diskSize/100 (the bug in the k8s
// source that is corrected in this lib).
func TestEmitWindowsDiskFreeIsDirectValue(t *testing.T) {
	// We can't easily compute the exact expected value without duplicating the physics,
	// so we verify that it is a reasonable absolute byte value (not a scaled-down quirk).
	//
	// The source bug: diskFree = (30 + hash*50) * 1<<30 bytes (i.e. 30–80 GiB).
	// The bug applied: diskFree * diskSize / 100 = diskFree * (100<<30) / 100 = diskFree * (1<<30).
	// That would produce values in the range ~30*(1<<60) which is astronomically large.
	// The correct value is diskFree directly: 30–80 GiB, i.e. 32_212_254_720 – 85_899_345_920 bytes.
	//
	// We check that the emitted value is in a sane absolute range.
	series := emitWindowsAndCollect(ProfileIntegration)
	byName := seriesByNameWindows(series)

	ss, ok := byName["windows_logical_disk_free_bytes"]
	if !ok || len(ss) == 0 {
		t.Fatal("windows_logical_disk_free_bytes not emitted")
	}
	v := ss[0].Value
	const minFree = 1 * 1024 * 1024 * 1024   // 1 GiB minimum
	const maxFree = 100 * 1024 * 1024 * 1024 // 100 GiB maximum (bounded by disk size)
	if v < minFree || v > maxFree {
		t.Errorf("windows_logical_disk_free_bytes value=%v is out of sane range [%v, %v]; expected direct diskFree, not the diskFree*diskSize/100 quirk", v, minFree, maxFree)
	}
}

// TestEmitWindowsServiceStateNotPhantom asserts the capture-driven fix: the integration
// keepset no longer references the PHANTOM windows_service_status, and EmitWindows emits the
// REAL windows_service_state{name,state} metric instead (host-capture.md WINSRV section).
func TestEmitWindowsServiceStateNotPhantom(t *testing.T) {
	ks := keepSetWindows(ProfileIntegration)
	if ks["windows_service_status"] {
		t.Error("windows_service_status is a phantom — must NOT be in the integration keepset")
	}
	if !ks["windows_service_state"] {
		t.Error("windows_service_state must be in the integration keepset (real metric)")
	}
	// windows_cs_* collector is absent on the real host — must not be kept.
	for _, n := range []string{"windows_cs_logical_processors", "windows_cs_physical_memory_bytes"} {
		if ks[n] {
			t.Errorf("%s must NOT be kept (windows_cs_* does not exist on real WINSRV)", n)
		}
	}

	series := emitWindowsAndCollect(ProfileIntegration)
	byName := seriesByNameWindows(series)

	ss, ok := byName["windows_service_state"]
	if !ok || len(ss) == 0 {
		t.Fatal("windows_service_state not emitted")
	}
	if hasState := func() bool {
		for _, s := range ss {
			if s.Labels["name"] != "" && s.Labels["state"] != "" {
				return true
			}
		}
		return false
	}(); !hasState {
		t.Errorf("windows_service_state must carry name+state labels; got %v", ss[0].Labels)
	}
}

// TestEmitWindowsCaptureExtras asserts the capture-confirmed integration extras are emitted
// with the EXACT label keys the WINSRV capture shows.
func TestEmitWindowsCaptureExtras(t *testing.T) {
	series := emitWindowsAndCollect(ProfileIntegration)
	byName := seriesByNameWindows(series)

	// Scalar system/time/pagefile metrics (no extra labels beyond base).
	for _, n := range []string{
		"windows_system_context_switches_total",
		"windows_system_processor_queue_length",
		"windows_pagefile_free_bytes",
		"windows_pagefile_limit_bytes",
		"windows_time_computed_time_offset_seconds",
	} {
		if _, ok := byName[n]; !ok {
			t.Errorf("capture-confirmed extra %q not emitted", n)
		}
	}

	// windows_diskdrive_status carries name+status (NOT the phantom windows_disk_drive_status).
	if byName["windows_disk_drive_status"] != nil {
		t.Error("windows_disk_drive_status is wrong (underscore-split); capture name is windows_diskdrive_status")
	}
	dd, ok := byName["windows_diskdrive_status"]
	if !ok || len(dd) == 0 {
		t.Fatal("windows_diskdrive_status not emitted")
	}
	if dd[0].Labels["name"] == "" || dd[0].Labels["status"] == "" {
		t.Errorf("windows_diskdrive_status must carry name+status labels; got %v", dd[0].Labels)
	}

	// windows_time_timezone carries a `timezone` label per capture.
	tz, ok := byName["windows_time_timezone"]
	if ok && len(tz) > 0 && tz[0].Labels["timezone"] == "" {
		t.Errorf("windows_time_timezone must carry a `timezone` label; got %v", tz[0].Labels)
	}
}

// TestEmitWindowsK8sProfileNamesPresent asserts that all k8sWindowsNames are emitted
// when using ProfileK8s.
func TestEmitWindowsK8sProfileNamesPresent(t *testing.T) {
	st := state.NewState()
	EmitWindows(st, testWindowsBase(), testWindowsTop(), ProfileK8s, 0.5, 60, 2, testEngine())
	series := st.Collect(testNow())
	byName := seriesByNameWindows(series)

	for _, name := range k8sWindowsNames {
		if _, ok := byName[name]; !ok {
			t.Errorf("ProfileK8s: expected %q (from k8sWindowsNames) to be emitted but was not", name)
		}
	}
}

// TestEmitWindowsNoEmptyStringLabels asserts that no emitted series carries an empty-string
// label value (absent dimension rule I13).
func TestEmitWindowsNoEmptyStringLabels(t *testing.T) {
	for _, prof := range []Profile{ProfileIntegration, ProfileK8s} {
		series := emitWindowsAndCollect(prof)
		for _, s := range series {
			for k, v := range s.Labels {
				if v == "" {
					t.Errorf("profile=%s metric=%s has empty label %q=''", prof, s.Name, k)
				}
			}
		}
	}
}

// TestEmitWindowsBaseNotMutated asserts that the caller's base map is never mutated.
func TestEmitWindowsBaseNotMutated(t *testing.T) {
	base := testWindowsBase()
	origLen := len(base)
	origJob := base["job"]
	origInstance := base["instance"]

	st := state.NewState()
	EmitWindows(st, base, testWindowsTop(), ProfileIntegration, 0.5, 60, 2, testEngine())

	if len(base) != origLen {
		t.Errorf("base map mutated: len changed from %d to %d", origLen, len(base))
	}
	if base["job"] != origJob {
		t.Errorf("base[job] mutated: was %q, now %q", origJob, base["job"])
	}
	if base["instance"] != origInstance {
		t.Errorf("base[instance] mutated: was %q, now %q", origInstance, base["instance"])
	}
	if _, extra := base["core"]; extra {
		t.Error("base map has unexpected 'core' key after EmitWindows — base was mutated")
	}
}
