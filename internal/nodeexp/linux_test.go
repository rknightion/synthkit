// SPDX-License-Identifier: AGPL-3.0-only

package nodeexp

import (
	"testing"

	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// testLinuxTop returns a representative HostTopology for Linux tests.
func testLinuxTop() HostTopology {
	return HostTopology{
		Hostname: "camden",
		NumCPU:   2,
		MemTotal: 8 << 30,
		Disks:    []string{"sda", "sdb"},
		NICs:     []NIC{{Name: "eth0", SpeedBytes: 1e9}, {Name: "lo", SpeedBytes: 0}},
		FS:       FSMount{Device: "/dev/sda1", FSType: "ext4", Mountpoint: "/", SizeBytes: 100 << 30},
		OS: OSInfo{
			ID:         "ubuntu",
			Name:       "Ubuntu",
			PrettyName: "Ubuntu 22.04.4 LTS",
			Version:    "22.04",
			VersionID:  "22.04",
			Kernel:     "6.8.0-40-generic",
			Machine:    "x86_64",
		},
		BootTime: 1_748_000_000,
	}
}

// testLinuxBase returns a minimal base label map for Linux tests.
func testLinuxBase() map[string]string {
	return map[string]string{
		"job":      "integrations/node_exporter",
		"instance": "camden",
	}
}

// emitAndCollect calls EmitLinux once and returns all collected series.
func emitAndCollect(prof Profile) []promrw.Series {
	st := state.NewState()
	EmitLinux(st, testLinuxBase(), testLinuxTop(), prof, 0.5, 60, 2, testEngine())
	return st.Collect(testNow())
}

// seriesByName indexes collected series by metric name → list of series.
func seriesByName(series []promrw.Series) map[string][]promrw.Series {
	m := make(map[string][]promrw.Series)
	for _, s := range series {
		m[s.Name] = append(m[s.Name], s)
	}
	return m
}

// TestEmitLinuxCounterMonotonic asserts that node_cpu_seconds_total is a cumulative counter
// (calling EmitLinux twice on the same state increases the value).
func TestEmitLinuxCounterMonotonic(t *testing.T) {
	st := state.NewState()
	base := testLinuxBase()
	top := testLinuxTop()
	sh := testEngine()

	EmitLinux(st, base, top, ProfileIntegration, 0.5, 60, 2, sh)
	series1 := st.Collect(testNow())

	// Find node_cpu_seconds_total value after first tick.
	var cpu1 float64
	for _, s := range series1 {
		if s.Name == "node_cpu_seconds_total" {
			cpu1 += s.Value
		}
	}
	if cpu1 == 0 {
		t.Fatal("node_cpu_seconds_total is 0 after first EmitLinux — counter not emitted")
	}

	// Emit a second tick.
	EmitLinux(st, base, top, ProfileIntegration, 0.5, 60, 2, sh)
	series2 := st.Collect(testNow())

	var cpu2 float64
	for _, s := range series2 {
		if s.Name == "node_cpu_seconds_total" {
			cpu2 += s.Value
		}
	}

	if cpu2 <= cpu1 {
		t.Errorf("node_cpu_seconds_total did not grow: tick1=%.2f tick2=%.2f (must be monotonically increasing)", cpu1, cpu2)
	}
}

// TestEmitLinuxIntegrationFiltered asserts that with ProfileIntegration, every emitted
// name is in keepSet(ProfileIntegration).
func TestEmitLinuxIntegrationFiltered(t *testing.T) {
	ks := keepSet(ProfileIntegration)
	series := emitAndCollect(ProfileIntegration)
	if len(series) == 0 {
		t.Fatal("EmitLinux(ProfileIntegration) emitted no series")
	}
	for _, s := range series {
		if !ks[s.Name] {
			t.Errorf("ProfileIntegration: emitted %q which is not in keepSet(ProfileIntegration)", s.Name)
		}
	}
}

// TestEmitLinuxCoreGaugesPresent asserts node_load1 and node_memory_MemTotal_bytes are
// emitted and have non-negative values.
func TestEmitLinuxCoreGaugesPresent(t *testing.T) {
	series := emitAndCollect(ProfileIntegration)
	byName := seriesByName(series)

	for _, name := range []string{"node_load1", "node_memory_MemTotal_bytes"} {
		s, ok := byName[name]
		if !ok || len(s) == 0 {
			t.Errorf("expected gauge %q to be emitted", name)
			continue
		}
		if s[0].Value < 0 {
			t.Errorf("%q has negative value %v", name, s[0].Value)
		}
	}
}

// TestEmitLinuxFullSupersetOfIntegration asserts that ProfileFull emits at least all names
// that ProfileIntegration emits (full ⊇ integration).
func TestEmitLinuxFullSupersetOfIntegration(t *testing.T) {
	intNames := make(map[string]bool)
	for _, s := range emitAndCollect(ProfileIntegration) {
		intNames[s.Name] = true
	}

	fullNames := make(map[string]bool)
	for _, s := range emitAndCollect(ProfileFull) {
		fullNames[s.Name] = true
	}

	for n := range intNames {
		if !fullNames[n] {
			t.Errorf("ProfileFull is missing %q which ProfileIntegration emits", n)
		}
	}
}

// TestEmitLinuxNoEmptyStringLabels asserts that no emitted series carries an empty-string
// label value (absent dimension rule I13).
func TestEmitLinuxNoEmptyStringLabels(t *testing.T) {
	for _, prof := range []Profile{ProfileIntegration, ProfileFull, ProfileK8s} {
		series := emitAndCollect(prof)
		for _, s := range series {
			for k, v := range s.Labels {
				if v == "" {
					t.Errorf("profile=%s metric=%s has empty label %q=''", prof, s.Name, k)
				}
			}
		}
	}
}

// TestEmitLinuxOmitsEmptyKernelRelease asserts that when OS.Kernel is empty, EmitLinux
// emits node_uname_info WITHOUT a `release` label (absent dimension OMITTED, never "" — I13).
func TestEmitLinuxOmitsEmptyKernelRelease(t *testing.T) {
	top := testLinuxTop()
	top.OS.Kernel = "" // no kernel supplied
	st := state.NewState()
	EmitLinux(st, testLinuxBase(), top, ProfileIntegration, 0.5, 60, 2, testEngine())
	series := st.Collect(testNow())

	var found bool
	for _, s := range series {
		if s.Name != "node_uname_info" {
			continue
		}
		found = true
		if v, ok := s.Labels["release"]; ok {
			t.Errorf("node_uname_info has release=%q with empty kernel; want release label OMITTED", v)
		}
	}
	if !found {
		t.Fatal("node_uname_info not emitted")
	}
}

// counterNamesFromSource is the authoritative set of names the source (nodeexporter.go) writes
// via st.Add (counters). This is the ground-truth that TestEmitLinuxCounterGaugeClassification
// verifies against. Derived by grepping st.Add("... in nodeexporter.go on 2026-06-17.
//
// The netstat/vmstat/softnet names are also counters but are more easily verified collectively
// via the linuxNetstatMetrics / linuxVmstatCounters / per-cpu softnet vars defined in linux.go.
var counterNamesFromSource = []string{
	"node_context_switches_total",
	"node_cpu_guest_seconds_total",
	"node_cpu_seconds_total",
	"node_disk_io_time_seconds_total",
	"node_disk_io_time_weighted_seconds_total",
	"node_disk_read_bytes_total",
	"node_disk_read_time_seconds_total",
	"node_disk_reads_completed_total",
	"node_disk_write_time_seconds_total",
	"node_disk_writes_completed_total",
	"node_disk_written_bytes_total",
	"node_intr_total",
	"node_network_receive_bytes_total",
	"node_network_receive_compressed_total",
	"node_network_receive_drop_total",
	"node_network_receive_errs_total",
	"node_network_receive_fifo_total",
	"node_network_receive_multicast_total",
	"node_network_receive_packets_total",
	"node_network_transmit_bytes_total",
	"node_network_transmit_compressed_total",
	"node_network_transmit_drop_total",
	"node_network_transmit_errs_total",
	"node_network_transmit_fifo_total",
	"node_network_transmit_packets_total",
	"node_softnet_dropped_total",
	"node_softnet_processed_total",
	"node_softnet_times_squeezed_total",
	"process_cpu_seconds_total",
	// netstat counters (all 29 are st.Add in the source)
	"node_netstat_Icmp6_InErrors", "node_netstat_Icmp6_InMsgs", "node_netstat_Icmp6_OutMsgs",
	"node_netstat_Icmp_InErrors", "node_netstat_Icmp_InMsgs", "node_netstat_Icmp_OutMsgs",
	"node_netstat_IpExt_InOctets", "node_netstat_IpExt_OutOctets",
	"node_netstat_Tcp_InErrs", "node_netstat_Tcp_InSegs", "node_netstat_Tcp_OutRsts",
	"node_netstat_Tcp_OutSegs", "node_netstat_Tcp_RetransSegs",
	"node_netstat_TcpExt_ListenDrops", "node_netstat_TcpExt_ListenOverflows", "node_netstat_TcpExt_TCPSynRetrans",
	"node_netstat_Udp6_InDatagrams", "node_netstat_Udp6_InErrors", "node_netstat_Udp6_NoPorts",
	"node_netstat_Udp6_OutDatagrams", "node_netstat_Udp6_RcvbufErrors", "node_netstat_Udp6_SndbufErrors",
	"node_netstat_Udp_InDatagrams", "node_netstat_Udp_InErrors", "node_netstat_Udp_NoPorts",
	"node_netstat_Udp_OutDatagrams", "node_netstat_Udp_RcvbufErrors", "node_netstat_Udp_SndbufErrors",
	"node_netstat_UdpLite_InErrors",
	// vmstat counters (all 7 are st.Add in the source)
	"node_vmstat_oom_kill", "node_vmstat_pgfault", "node_vmstat_pgmajfault",
	"node_vmstat_pgpgin", "node_vmstat_pgpgout", "node_vmstat_pswpin", "node_vmstat_pswpout",
}

// gaugeNamesFromSource is the authoritative set of names the source writes via st.Set.
// Derived by grepping st.Set("... in nodeexporter.go on 2026-06-17.
var gaugeNamesFromSource = []string{
	"node_arp_entries",
	"node_boot_time_seconds",
	"node_cpu_online",
	"node_exporter_build_info",
	"node_filefd_allocated",
	"node_filefd_maximum",
	"node_filesystem_avail_bytes",
	"node_filesystem_device_error",
	"node_filesystem_files",
	"node_filesystem_files_free",
	"node_filesystem_free_bytes",
	"node_filesystem_mount_info",
	"node_filesystem_purgeable_bytes",
	"node_filesystem_readonly",
	"node_filesystem_size_bytes",
	"node_load1",
	"node_load15",
	"node_load5",
	"node_memory_Buffers_bytes",
	"node_memory_Cached_bytes",
	"node_memory_MemAvailable_bytes",
	"node_memory_MemFree_bytes",
	"node_memory_MemTotal_bytes",
	"node_memory_SwapFree_bytes",
	"node_memory_SwapTotal_bytes",
	"node_network_carrier",
	"node_network_info",
	"node_network_mtu_bytes",
	"node_network_speed_bytes",
	"node_network_transmit_queue_length",
	"node_network_up",
	"node_nf_conntrack_entries",
	"node_nf_conntrack_entries_limit",
	"node_os_info",
	"node_procs_running",
	"node_textfile_scrape_error",
	"node_time_zone_offset_seconds",
	"node_timex_estimated_error_seconds",
	"node_timex_maxerror_seconds",
	"node_timex_offset_seconds",
	"node_timex_sync_status",
	"node_uname_info",
	"process_max_fds",
	"process_open_fds",
	"process_resident_memory_bytes",
	// sockstat (all 18 are st.Set in the source)
	"node_sockstat_FRAG6_inuse", "node_sockstat_FRAG_inuse",
	"node_sockstat_RAW6_inuse", "node_sockstat_RAW_inuse",
	"node_sockstat_TCP6_inuse", "node_sockstat_TCP_alloc", "node_sockstat_TCP_inuse",
	"node_sockstat_TCP_mem", "node_sockstat_TCP_mem_bytes", "node_sockstat_TCP_orphan", "node_sockstat_TCP_tw",
	"node_sockstat_UDP6_inuse", "node_sockstat_UDP_inuse", "node_sockstat_UDP_mem", "node_sockstat_UDP_mem_bytes",
	"node_sockstat_UDPLITE6_inuse", "node_sockstat_UDPLITE_inuse",
	"node_sockstat_sockets_used",
	// memory extended MemInfo fields (all st.Set in the source)
	"node_memory_Active_anon_bytes", "node_memory_Active_bytes", "node_memory_Active_file_bytes",
	"node_memory_AnonHugePages_bytes", "node_memory_AnonPages_bytes", "node_memory_Bounce_bytes",
	"node_memory_CmaFree_bytes", "node_memory_CmaTotal_bytes",
	"node_memory_CommitLimit_bytes", "node_memory_Committed_AS_bytes",
	"node_memory_Dirty_bytes", "node_memory_HardwareCorrupted_bytes",
	"node_memory_HugePages_Free", "node_memory_HugePages_Rsvd", "node_memory_HugePages_Surp",
	"node_memory_HugePages_Total", "node_memory_Hugepagesize_bytes",
	"node_memory_Inactive_anon_bytes", "node_memory_Inactive_bytes", "node_memory_Inactive_file_bytes",
	"node_memory_KernelStack_bytes", "node_memory_Mapped_bytes", "node_memory_Mlocked_bytes",
	"node_memory_NFS_Unstable_bytes", "node_memory_PageTables_bytes", "node_memory_Percpu_bytes",
	"node_memory_SReclaimable_bytes", "node_memory_SUnreclaim_bytes",
	"node_memory_ShmemHugePages_bytes", "node_memory_ShmemPmdMapped_bytes", "node_memory_Shmem_bytes",
	"node_memory_Slab_bytes", "node_memory_SwapCached_bytes",
	"node_memory_Unevictable_bytes",
	"node_memory_VmallocChunk_bytes", "node_memory_VmallocTotal_bytes", "node_memory_VmallocUsed_bytes",
	"node_memory_WritebackTmp_bytes", "node_memory_Writeback_bytes",
	"node_memory_Zswap_bytes", "node_memory_Zswapped_bytes",
	// up (gauge, emitted by EmitLinux itself)
	"up",
	// integration-only names not in k8s source but in keepSet for integration/full
	"node_md_disks",
	"node_md_disks_required",
	"node_systemd_unit_state",
}

// TestEmitLinuxCounterGaugeClassification is the key guard ensuring that every name
// emitted via st.Add (counter) in the source is still emitted as KindCounter in the
// ported implementation, and every st.Set (gauge) name is KindGauge.
//
// This test is the ONLY guard against a counter→gauge flip — the -dump inventory does
// NOT verify Kind. Pay special attention to the zero-valued counters:
// node_softnet_dropped_total / node_network_receive_drop_total /
// node_network_transmit_drop_total / node_vmstat_oom_kill (all st.Add with delta=0).
func TestEmitLinuxCounterGaugeClassification(t *testing.T) {
	// Use ProfileK8s so we get the full union vocabulary (k8s keepset is the broadest set
	// that is a strict superset of the src counters/gauges — integration would filter some out).
	series := emitAndCollect(ProfileK8s)
	if len(series) == 0 {
		t.Fatal("EmitLinux(ProfileK8s) emitted no series")
	}

	// Build per-name Kind index: track which kinds each name appears as.
	nameKind := make(map[string]promrw.Kind)
	for _, s := range series {
		// If a name appears with multiple label sets, all should have same Kind.
		if existing, seen := nameKind[s.Name]; seen {
			if existing != s.Kind {
				t.Errorf("metric %q appears with conflicting Kinds: %v and %v", s.Name, existing, s.Kind)
			}
		} else {
			nameKind[s.Name] = s.Kind
		}
	}

	// Assert all expected counters are KindCounter.
	for _, name := range counterNamesFromSource {
		k, ok := nameKind[name]
		if !ok {
			// Some counter names are in the full union but not in ProfileK8s keepset → skip.
			// Only assert for names that ARE emitted.
			continue
		}
		if k != promrw.KindCounter {
			t.Errorf("counter %q is classified as Kind=%v (expected KindCounter)", name, k)
		}
	}

	// Assert all expected gauges are KindGauge.
	for _, name := range gaugeNamesFromSource {
		k, ok := nameKind[name]
		if !ok {
			continue
		}
		if k != promrw.KindGauge {
			t.Errorf("gauge %q is classified as Kind=%v (expected KindGauge)", name, k)
		}
	}

	// Assert no name appears in BOTH sets (sanity check for the test data itself).
	counterSet := make(map[string]bool, len(counterNamesFromSource))
	for _, n := range counterNamesFromSource {
		counterSet[n] = true
	}
	for _, n := range gaugeNamesFromSource {
		if counterSet[n] {
			t.Errorf("test data error: %q appears in both counterNamesFromSource and gaugeNamesFromSource", n)
		}
	}
}

// TestEmitLinuxK8sUnionVocabulary asserts that with ProfileK8s the exact names from
// k8sNodeNames are all emitted (the migration gate: k8s keepset reproduces the source).
func TestEmitLinuxK8sUnionVocabulary(t *testing.T) {
	series := emitAndCollect(ProfileK8s)
	byName := seriesByName(series)

	for _, name := range k8sNodeNames {
		if _, ok := byName[name]; !ok {
			t.Errorf("ProfileK8s: expected %q (from k8sNodeNames) to be emitted but it was not", name)
		}
	}
}

// TestEmitLinuxBaseNotMutated asserts that the caller's base map is never mutated by EmitLinux.
func TestEmitLinuxBaseNotMutated(t *testing.T) {
	base := testLinuxBase()
	origLen := len(base)
	origJob := base["job"]
	origInstance := base["instance"]

	st := state.NewState()
	EmitLinux(st, base, testLinuxTop(), ProfileIntegration, 0.5, 60, 2, testEngine())

	if len(base) != origLen {
		t.Errorf("base map mutated: len changed from %d to %d", origLen, len(base))
	}
	if base["job"] != origJob {
		t.Errorf("base[job] mutated: was %q, now %q", origJob, base["job"])
	}
	if base["instance"] != origInstance {
		t.Errorf("base[instance] mutated: was %q, now %q", origInstance, base["instance"])
	}
	if _, extra := base["cpu"]; extra {
		t.Error("base map has unexpected 'cpu' key after EmitLinux — base was mutated")
	}
}
