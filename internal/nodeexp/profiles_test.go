// SPDX-License-Identifier: AGPL-3.0-only

package nodeexp

import (
	"testing"

	"github.com/rknightion/synthkit/internal/state"
)

// TestKeepSetIntegrationLinux verifies the linux integration profile keepset
// against the Appendix-A allowlist contract.
func TestKeepSetIntegrationLinux(t *testing.T) {
	ks := keepSet(ProfileIntegration)

	// Names that MUST be in the linux integration allowlist (Appendix A).
	mustKeep := []string{
		"node_cpu_seconds_total",
		"node_load1",
		"node_filesystem_avail_bytes",
		"node_systemd_unit_state",
		"node_md_disks",
		"up",
		"process_open_fds",
	}
	for _, n := range mustKeep {
		if !ks[n] {
			t.Errorf("integration profile must keep %q", n)
		}
	}

	// Names that must NOT be in the linux integration allowlist
	// (they are full-profile-only extras).
	mustDrop := []string{
		"node_exporter_build_info",
		"process_cpu_seconds_total",
	}
	for _, n := range mustDrop {
		if ks[n] {
			t.Errorf("integration profile must NOT keep %q (full-only)", n)
		}
	}
}

// TestKeepSetK8sMatchesCurrent verifies that the k8s profile keepset reproduces
// the exact node-exporter metric names emitted by k8scluster/nodeexporter.go.
func TestKeepSetK8sMatchesCurrent(t *testing.T) {
	ks := keepSet(ProfileK8s)

	// k8scluster/nodeexporter.go emits these (present in k8sNodeNames, grepped 2026-06-17).
	if !ks["node_exporter_build_info"] {
		t.Error("k8s profile keeps node_exporter_build_info (present in current k8scluster)")
	}
	if !ks["process_cpu_seconds_total"] {
		t.Error("k8s profile keeps process_cpu_seconds_total (present in current k8scluster)")
	}

	// EKS/Bottlerocket nodes do not expose systemd or md-raid — these are absent
	// from k8scluster/nodeexporter.go (confirmed by grep 2026-06-17).
	if ks["node_systemd_unit_state"] {
		t.Error("k8s profile must NOT keep node_systemd_unit_state (EKS/Bottlerocket lack it)")
	}
	if ks["node_md_disks"] {
		t.Error("k8s profile must NOT keep node_md_disks (EKS/Bottlerocket lack it)")
	}
}

// TestKeepSetFullIsSuperset verifies that ProfileFull is a superset of ProfileIntegration.
func TestKeepSetFullIsSuperset(t *testing.T) {
	integration := keepSet(ProfileIntegration)
	full := keepSet(ProfileFull)

	for n := range integration {
		if !full[n] {
			t.Errorf("ProfileFull must be a superset of ProfileIntegration; missing %q", n)
		}
	}

	// ProfileFull must add the universal node_exporter self-metric delta (modest, capture-
	// confirmed on every real node_exporter). It must NOT add the hardware-specific families
	// (zfs/ethtool/mountstats) — those are out of scope for synthetic full.
	extras := []string{
		"process_cpu_seconds_total",
		"process_resident_memory_bytes",
		"node_exporter_build_info",
		"go_goroutines",
		"go_memstats_alloc_bytes",
		"promhttp_metric_handler_requests_total",
	}
	for _, n := range extras {
		if !full[n] {
			t.Errorf("ProfileFull must keep %q (universal self-metric delta)", n)
		}
	}
	// Hardware-specific families are intentionally NOT in synthetic full.
	for _, n := range []string{"node_zfs_arc_size", "node_ethtool_received_packets_total", "node_mountstats_nfs_total"} {
		if full[n] {
			t.Errorf("ProfileFull must NOT keep hardware-specific %q (out of scope for synthetic full)", n)
		}
	}
}

// TestEmitLinuxFullEmitsUniversalSelfMetrics asserts EmitLinux under ProfileFull actually
// emits the universal node_exporter self-metric delta (the keepset would drop them if the
// emitter never computed them — this guards that linux.go emits a real superset).
func TestEmitLinuxFullEmitsUniversalSelfMetrics(t *testing.T) {
	st := state.NewState()
	base := map[string]string{"job": "integrations/node_exporter", "instance": "camden"}
	top := HostTopology{Hostname: "camden", NumCPU: 2, MemTotal: 8 << 30,
		Disks: []string{"nvme0n1"}, NICs: []NIC{{Name: "eth0", SpeedBytes: 1e9}},
		FS: FSMount{Device: "/dev/nvme0n1p1", FSType: "ext4", Mountpoint: "/", SizeBytes: 100 << 30}}
	EmitLinux(st, base, top, ProfileFull, 1.0, 60, 2, testEngine())

	emitted := map[string]bool{}
	for _, s := range st.Collect(testNow()) {
		emitted[s.Name] = true
	}
	for _, n := range []string{
		"process_cpu_seconds_total", "process_resident_memory_bytes", "node_exporter_build_info",
		"go_goroutines", "go_memstats_alloc_bytes", "promhttp_metric_handler_requests_total",
	} {
		if !emitted[n] {
			t.Errorf("ProfileFull EmitLinux must emit %q", n)
		}
	}
}

// TestMacosKeepSetIntegration verifies the macOS integration allowlist.
func TestMacosKeepSetIntegration(t *testing.T) {
	ks := macosKeepSet(ProfileIntegration)

	// macOS-specific memory metrics (distinct from linux).
	macosMemory := []string{
		"node_memory_total_bytes",
		"node_memory_compressed_bytes",
		"node_memory_internal_bytes",
		"node_memory_purgeable_bytes",
		"node_memory_wired_bytes",
		"node_memory_swap_total_bytes",
		"node_memory_swap_used_bytes",
	}
	for _, n := range macosMemory {
		if !ks[n] {
			t.Errorf("macos integration profile must keep %q", n)
		}
	}

	// Linux-only memory metrics must NOT appear in the macOS set.
	if ks["node_memory_MemTotal_bytes"] {
		t.Error("macos integration profile must NOT keep node_memory_MemTotal_bytes (linux-only)")
	}
}

// TestKeepSetCadvisorDockerKeepsAllowlist verifies the docker cadvisor keepset
// against the Appendix-A docker allowlist.
func TestKeepSetCadvisorDockerKeepsAllowlist(t *testing.T) {
	ks := keepSetCadvisor(CadvisorDocker)

	// Must keep (Appendix-A docker cadvisor allowlist).
	mustKeep := []string{
		"container_cpu_usage_seconds_total",
		"container_fs_usage_bytes",
		"container_last_seen",
		"container_memory_usage_bytes",
		"container_network_receive_bytes_total",
		"container_network_transmit_errors_total",
		"container_spec_memory_reservation_limit_bytes",
		"machine_memory_bytes",
		"machine_scrape_error",
		"up",
	}
	for _, n := range mustKeep {
		if !ks[n] {
			t.Errorf("CadvisorDocker must keep %q", n)
		}
	}

	// CadvisorDocker must NOT keep k8s-only cadvisor names.
	mustDrop := []string{
		"container_cpu_cfs_periods_total",
		"container_memory_working_set_bytes",
		"container_memory_rss",
		"container_memory_cache",
	}
	for _, n := range mustDrop {
		if ks[n] {
			t.Errorf("CadvisorDocker must NOT keep %q (k8s-only)", n)
		}
	}
}

// TestKeepSetCadvisorK8sMatchesCurrent verifies the k8s cadvisor keepset
// reproduces the exact names from k8scluster/cadvisor.go.
func TestKeepSetCadvisorK8sMatchesCurrent(t *testing.T) {
	ks := keepSetCadvisor(CadvisorK8s)

	// Present in k8scluster/cadvisor.go (grepped 2026-06-17).
	mustKeep := []string{
		"container_cpu_cfs_periods_total",
		"container_memory_working_set_bytes",
		"container_memory_rss",
		"container_memory_cache",
		"container_memory_swap",
		"container_fs_reads_bytes_total",
		"container_fs_writes_bytes_total",
		"machine_memory_bytes",
	}
	for _, n := range mustKeep {
		if !ks[n] {
			t.Errorf("CadvisorK8s must keep %q (in current k8scluster)", n)
		}
	}

	// Docker-only names must NOT be in the k8s cadvisor keepset.
	mustDrop := []string{
		"container_fs_usage_bytes",
		"container_last_seen",
		"container_spec_memory_reservation_limit_bytes",
		"machine_scrape_error",
	}
	for _, n := range mustDrop {
		if ks[n] {
			t.Errorf("CadvisorK8s must NOT keep %q (docker-only)", n)
		}
	}
}

// TestKeepSetWindowsIntegration verifies the windows integration allowlist.
func TestKeepSetWindowsIntegration(t *testing.T) {
	ks := keepSetWindows(ProfileIntegration)

	mustKeep := []string{
		"up",
		"windows_cpu_time_total",
		"windows_memory_physical_total_bytes",
		"windows_logical_disk_free_bytes",
		"windows_net_bytes_received_total",
		"windows_os_info",
	}
	for _, n := range mustKeep {
		if !ks[n] {
			t.Errorf("windows integration profile must keep %q", n)
		}
	}
}

// TestKeepSetWindowsK8sMatchesCurrent verifies the k8s windows keepset
// reproduces the exact names from k8scluster/windowsexporter.go.
func TestKeepSetWindowsK8sMatchesCurrent(t *testing.T) {
	ks := keepSetWindows(ProfileK8s)

	// Present in k8scluster/windowsexporter.go (grepped 2026-06-17).
	mustKeep := []string{
		"windows_cpu_time_total",
		"windows_cpu_logical_processor",
		"windows_memory_physical_total_bytes",
		"windows_memory_available_bytes",
		"windows_memory_committed_bytes",
		"windows_os_hostname",
		"windows_os_info",
	}
	for _, n := range mustKeep {
		if !ks[n] {
			t.Errorf("ProfileK8s windows must keep %q (in current k8scluster)", n)
		}
	}

	// Integration-only names NOT in the k8s set.
	mustDrop := []string{
		"windows_service_status",
		"windows_pagefile_limit_bytes",
		"windows_time_timezone",
	}
	for _, n := range mustDrop {
		if ks[n] {
			t.Errorf("ProfileK8s windows must NOT keep %q (integration-only, not in k8scluster)", n)
		}
	}
}
