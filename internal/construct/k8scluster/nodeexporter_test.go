// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster_test

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// neTick builds the coretest cluster construct and runs one tick, returning the capture.
func neTick(t *testing.T) *coretest.MetricCapture {
	t.Helper()
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)
	return mc
}

// TestNodeMemoryUsedNonZeroOffPeak guards the node memory-used contract: MemAvailable must never
// exceed MemTotal (a node never reports more available than installed), so used = MemTotal-MemAvailable
// stays positive and the Active/Anon/file gauges (a fraction of `used`) are non-zero. The old absolute
// MemAvailable range exceeded MemTotal on small nodes at low load → used clamped to 0 → those gauges
// read 0 → the k8s-monitoring app's node memory-used (Active_file+AnonPages) was 0 → blank Memory
// column. Reproduced live on a Grafana Cloud stack (MemAvailable 12.9GB > MemTotal 8GiB, Active_file=AnonPages=0).
func TestNodeMemoryUsedNonZeroOffPeak(t *testing.T) {
	cl := coretest.Cluster()
	for i := range cl.Nodes {
		cl.Nodes[i].InstanceType = "m6i.large" // 8 GiB — small enough that the old MemAvailable exceeded it
	}
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	// Saturday 02:00 UTC → low shape factor (weekend + off-hours): the regime that zeroed `used`.
	offPeak := time.Date(2026, 6, 13, 2, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), offPeak, coretest.World(mc, lc, nil)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	val := func(name string) float64 {
		s := mc.Find(name)
		if len(s) == 0 {
			t.Fatalf("%s not emitted", name)
		}
		return s[0].Value
	}
	total, avail := val("node_memory_MemTotal_bytes"), val("node_memory_MemAvailable_bytes")
	if avail >= total {
		t.Errorf("MemAvailable (%.0f) must be < MemTotal (%.0f) — a node never has more available than installed", avail, total)
	}
	for _, nm := range []string{"node_memory_Active_file_bytes", "node_memory_AnonPages_bytes", "node_memory_Active_anon_bytes"} {
		if v := val(nm); v <= 0 {
			t.Errorf("%s = %v, want > 0 (the app sums Active_file+AnonPages for node memory-used)", nm, v)
		}
	}
}

// TestNodeExporterFullFamilyPresence asserts every expanded family is emitted at least once.
func TestNodeExporterFullFamilyPresence(t *testing.T) {
	mc := neTick(t)

	want := []string{
		// identity
		"node_uname_info", "node_os_info", "node_exporter_build_info",
		// cpu
		"node_cpu_seconds_total", "node_cpu_guest_seconds_total", "node_cpu_online",
		// disk
		"node_disk_io_time_seconds_total", "node_disk_io_time_weighted_seconds_total",
		"node_disk_read_bytes_total", "node_disk_read_time_seconds_total",
		"node_disk_reads_completed_total", "node_disk_write_time_seconds_total",
		"node_disk_writes_completed_total", "node_disk_written_bytes_total",
		// load
		"node_load1", "node_load5", "node_load15",
		// softnet
		"node_softnet_dropped_total", "node_softnet_processed_total", "node_softnet_times_squeezed_total",
		// netstat (sample)
		"node_netstat_Tcp_InSegs", "node_netstat_Udp_InDatagrams", "node_netstat_IpExt_InOctets",
		// sockstat (sample)
		"node_sockstat_sockets_used", "node_sockstat_TCP_inuse", "node_sockstat_TCP_mem_bytes",
		// vmstat
		"node_vmstat_oom_kill", "node_vmstat_pgfault", "node_vmstat_pgmajfault",
		"node_vmstat_pgpgin", "node_vmstat_pgpgout", "node_vmstat_pswpin", "node_vmstat_pswpout",
		// timex / time
		"node_timex_estimated_error_seconds", "node_timex_maxerror_seconds",
		"node_timex_offset_seconds", "node_timex_sync_status", "node_time_zone_offset_seconds",
		// conntrack
		"node_nf_conntrack_entries", "node_nf_conntrack_entries_limit",
		// network detail
		"node_network_info", "node_network_carrier", "node_network_up",
		"node_network_mtu_bytes", "node_network_speed_bytes", "node_network_transmit_queue_length",
		"node_network_receive_errs_total", "node_network_transmit_errs_total",
		"node_network_receive_packets_total", "node_network_transmit_packets_total",
		"node_network_receive_compressed_total", "node_network_transmit_compressed_total",
		"node_network_receive_fifo_total", "node_network_transmit_fifo_total",
		"node_network_receive_multicast_total",
		// memory detail (sample)
		"node_memory_Active_bytes", "node_memory_Slab_bytes", "node_memory_Committed_AS_bytes",
		"node_memory_HugePages_Total", "node_memory_DirectMap_or_Dirty_check",
		// misc scalar
		"node_arp_entries", "node_boot_time_seconds", "node_context_switches_total",
		"node_intr_total", "node_procs_running", "node_filefd_allocated", "node_filefd_maximum",
		"node_textfile_scrape_error", "node_filesystem_device_error", "node_filesystem_readonly",
		"process_max_fds", "process_open_fds",
	}
	// node_memory_DirectMap_or_Dirty_check is a sentinel that should NOT exist (negative control):
	// drop it from the present-list and assert it's absent below.
	for _, nm := range want {
		if nm == "node_memory_DirectMap_or_Dirty_check" {
			if hasSeries(mc, nm) {
				t.Errorf("sentinel %q unexpectedly present (we must not invent metrics)", nm)
			}
			continue
		}
		if !hasSeries(mc, nm) {
			t.Errorf("missing node-exporter family: %q", nm)
		}
	}
}

// TestNodeUnameInfoLabels asserts node_uname_info carries the audit label keys + values.
func TestNodeUnameInfoLabels(t *testing.T) {
	cl := coretest.Cluster()
	cl.Platform = fixture.Platform{OSImage: "Bottlerocket OS 1.62.0 (aws-k8s-1.35)", OSID: "bottlerocket", KubernetesVersion: "1.35", KernelVersion: "6.12.88"}
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)
	s := mc.Find("node_uname_info")
	if len(s) == 0 {
		t.Fatal("node_uname_info not emitted")
	}
	for _, key := range []string{"machine", "sysname", "release", "nodename", "domainname", "version"} {
		if v := s[0].Labels[key]; v == "" {
			t.Errorf("node_uname_info missing label key %q", key)
		}
	}
	if s[0].Labels["sysname"] != "Linux" {
		t.Errorf("node_uname_info sysname=%q, want Linux", s[0].Labels["sysname"])
	}
	if s[0].Labels["release"] != "6.12.88" {
		t.Errorf("node_uname_info release=%q, want 6.12.88", s[0].Labels["release"])
	}
	if s[0].Labels["domainname"] != "(none)" {
		t.Errorf("node_uname_info domainname=%q, want (none)", s[0].Labels["domainname"])
	}
	// nodename == instance (hostname) — must equal the series' instance label.
	if s[0].Labels["nodename"] != s[0].Labels["instance"] {
		t.Errorf("node_uname_info nodename=%q != instance=%q", s[0].Labels["nodename"], s[0].Labels["instance"])
	}
	// machine must be a valid uname machine value.
	m := s[0].Labels["machine"]
	if m != "aarch64" && m != "x86_64" {
		t.Errorf("node_uname_info machine=%q, want aarch64 or x86_64", m)
	}
}

// TestNodeUnameMachineMatchesInstanceArch asserts the machine value tracks the node's instance type.
func TestNodeUnameMachineMatchesInstanceArch(t *testing.T) {
	cl := coretest.Cluster()
	cl.Seed = cl.Name
	cl.Region = cl.Cloud.Region
	cl.NodeGroups = []fixture.NodeGroupSpec{
		{Name: "general", InstanceType: "m6i.xlarge", Desired: 2}, // x86_64 → aarch64? no → x86_64
		{Name: "arm", InstanceType: "m8g.xlarge", Desired: 2},     // Graviton → aarch64
	}
	total := 0
	for _, wl := range cl.Workloads {
		total += wl.Replicas
	}
	cl.Nodes = fixture.DeriveNodes(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, total)
	for i := range cl.Workloads {
		wl := &cl.Workloads[i]
		wl.PodNames, wl.NodeIdx = nil, nil
		for p := 0; p < wl.Replicas; p++ {
			wl.PodNames = append(wl.PodNames, fixture.PodName(cl.Seed, wl.Name, p))
			wl.NodeIdx = append(wl.NodeIdx, p%len(cl.Nodes))
		}
	}
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	machines := distinctVals(mc, "node_uname_info", "machine")
	if !machines["aarch64"] || !machines["x86_64"] {
		t.Errorf("node_uname_info machine values = %v, want both aarch64 and x86_64", machines)
	}
}

// TestNodeOSInfoKeys asserts node_os_info is driven by cluster.Platform: a Bottlerocket cluster
// carries all 7 keys incl. variant_id/build_id; an Amazon Linux cluster OMITs those two (I13).
func TestNodeOSInfoKeys(t *testing.T) {
	osInfo := func(p fixture.Platform) map[string]string {
		cl := coretest.Cluster()
		cl.Platform = p
		c := buildConstruct(t, cl)
		mc := &coretest.MetricCapture{}
		lc := &coretest.LogCapture{}
		tick(t, c, mc, lc)
		s := mc.Find("node_os_info")
		if len(s) == 0 {
			t.Fatal("node_os_info not emitted")
		}
		return s[0].Labels
	}

	br := osInfo(fixture.Platform{OSImage: "Bottlerocket OS 1.62.0 (aws-k8s-1.35)", OSID: "bottlerocket", KubernetesVersion: "1.35", KernelVersion: "6.12.88"})
	for _, key := range []string{"id", "name", "pretty_name", "version", "version_id", "variant_id", "build_id"} {
		if _, ok := br[key]; !ok {
			t.Errorf("bottlerocket node_os_info missing label key %q", key)
		}
	}
	if br["id"] != "bottlerocket" {
		t.Errorf("node_os_info id=%q, want bottlerocket", br["id"])
	}

	al := osInfo(fixture.Platform{OSImage: "Amazon Linux 2023", OSID: "amzn", KubernetesVersion: "1.31", KernelVersion: "6.1.141"})
	if al["id"] != "amzn" {
		t.Errorf("AL node_os_info id=%q, want amzn", al["id"])
	}
	for _, key := range []string{"variant_id", "build_id"} {
		if _, ok := al[key]; ok {
			t.Errorf("AL node_os_info should OMIT %q (Bottlerocket-only), got %q", key, al[key])
		}
	}
}

// TestNodeDiskDeviceLabel asserts node_disk_* carry the representative device set.
func TestNodeDiskDeviceLabel(t *testing.T) {
	mc := neTick(t)
	devs := distinctVals(mc, "node_disk_read_bytes_total", "device")
	for _, want := range []string{"nvme0n1", "nvme1n1", "dm-0"} {
		if !devs[want] {
			t.Errorf("node_disk_read_bytes_total missing device=%q (got %v)", want, devs)
		}
	}
	if devs[""] {
		t.Error("node_disk_read_bytes_total carried an empty device label")
	}
}

// TestNodeSoftnetCPULabel asserts node_softnet_* carry the cpu key.
func TestNodeSoftnetCPULabel(t *testing.T) {
	mc := neTick(t)
	if v := labelVal(mc, "node_softnet_processed_total", "cpu"); v == "" {
		t.Error("node_softnet_processed_total missing cpu label")
	}
}

// TestNetstatSockstatVmstatFlat asserts these families carry ONLY ambient labels (no
// metric-specific keys like device/cpu/mode).
func TestNetstatSockstatVmstatFlat(t *testing.T) {
	mc := neTick(t)
	forbidden := []string{"device", "cpu", "mode", "fstype", "mountpoint"}
	for _, nm := range []string{"node_netstat_Tcp_InSegs", "node_sockstat_sockets_used", "node_vmstat_pgfault"} {
		for _, s := range mc.Find(nm) {
			for _, k := range forbidden {
				if _, ok := s.Labels[k]; ok {
					t.Errorf("flat metric %q unexpectedly carries metric-specific key %q", nm, k)
				}
			}
		}
	}
}

// TestNodeNetworkInfoKeys asserts node_network_info carries device/address/adminstate/broadcast/operstate.
func TestNodeNetworkInfoKeys(t *testing.T) {
	mc := neTick(t)
	s := mc.Find("node_network_info")
	if len(s) == 0 {
		t.Fatal("node_network_info not emitted")
	}
	for _, key := range []string{"device", "address", "adminstate", "broadcast", "operstate"} {
		if _, ok := s[0].Labels[key]; !ok {
			t.Errorf("node_network_info missing label key %q", key)
		}
	}
	// lo must report operstate=unknown (audit), eth* report up.
	var loFound, ethFound bool
	for _, ser := range s {
		switch ser.Labels["device"] {
		case "lo":
			loFound = true
			if ser.Labels["operstate"] != "unknown" {
				t.Errorf("lo operstate=%q, want unknown", ser.Labels["operstate"])
			}
		case "eth0":
			ethFound = true
			if ser.Labels["operstate"] != "up" {
				t.Errorf("eth0 operstate=%q, want up", ser.Labels["operstate"])
			}
		}
	}
	if !loFound || !ethFound {
		t.Errorf("node_network_info missing lo (%v) or eth0 (%v)", loFound, ethFound)
	}
}

// TestNodeNetworkSpeedOnlyEth asserts node_network_speed_bytes is emitted for eth* but NOT lo
// (matching the live capture where lo lacks a speed).
func TestNodeNetworkSpeedOnlyEth(t *testing.T) {
	mc := neTick(t)
	devs := distinctVals(mc, "node_network_speed_bytes", "device")
	if devs["lo"] {
		t.Error("node_network_speed_bytes must NOT be emitted for lo")
	}
	if !devs["eth0"] {
		t.Error("node_network_speed_bytes must be emitted for eth0")
	}
}

// TestPerCPUCountMatchesInstanceVCPUs asserts node_cpu_online has one series per vCPU per node.
func TestPerCPUCountMatchesInstanceVCPUs(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	// Expected total node_cpu_online = sum of vCPUs across nodes.
	wantTotal := 0
	wantCPUIdx := map[string]bool{}
	for _, n := range cl.Nodes {
		v := fixture.LookupInstanceSpec(n.InstanceType).VCPU
		wantTotal += v
		for i := 0; i < v; i++ {
			wantCPUIdx[itoa(i)] = true
		}
	}
	got := mc.Find("node_cpu_online")
	if len(got) != wantTotal {
		t.Errorf("node_cpu_online series count=%d, want %d (sum of node vCPUs)", len(got), wantTotal)
	}
	// distinct cpu indices must equal 0..max(vCPU)-1
	cpuVals := distinctVals(mc, "node_cpu_online", "cpu")
	for idx := range wantCPUIdx {
		if !cpuVals[idx] {
			t.Errorf("node_cpu_online missing cpu=%q", idx)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestNodeExporterCountersMonotone asserts a sample of the new _total counters climb across ticks.
func TestNodeExporterCountersMonotone(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	lc := &coretest.LogCapture{}

	counters := []string{
		"node_disk_read_bytes_total", "node_softnet_processed_total",
		"node_netstat_Tcp_InSegs", "node_context_switches_total",
		"node_network_receive_packets_total",
	}

	mc1 := &coretest.MetricCapture{}
	tick(t, c, mc1, lc)
	prev := map[string]float64{}
	for _, s := range mc1.All() {
		prev[s.Name+"|"+labelSig(s.Labels)] = s.Value
	}

	mc2 := &coretest.MetricCapture{}
	tick(t, c, mc2, lc)
	checked := 0
	want := map[string]bool{}
	for _, c := range counters {
		want[c] = true
	}
	for _, s := range mc2.All() {
		if !want[s.Name] {
			continue
		}
		key := s.Name + "|" + labelSig(s.Labels)
		if v1, ok := prev[key]; ok {
			checked++
			if s.Value < v1 {
				t.Errorf("counter %q decreased: tick1=%.4f tick2=%.4f", s.Name, v1, s.Value)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no counter series matched across ticks — test asserted nothing")
	}
}

// TestNodeExporterJobLabelOnNewFamilies asserts the expanded families still carry the
// exact node_exporter job (NO kubernetes/ segment) and the DaemonSet ambient labels.
func TestNodeExporterJobLabelOnNewFamilies(t *testing.T) {
	mc := neTick(t)
	for _, nm := range []string{"node_uname_info", "node_disk_read_bytes_total", "node_sockstat_sockets_used"} {
		for _, s := range mc.Find(nm) {
			if s.Labels["job"] != "integrations/node_exporter" {
				t.Errorf("%q job=%q, want integrations/node_exporter", nm, s.Labels["job"])
			}
			if s.Labels["app"] != "node-exporter" {
				t.Errorf("%q app=%q, want node-exporter", nm, s.Labels["app"])
			}
		}
	}
}

// TestNodeFilesystemMountInfo asserts node_filesystem_mount_info carries device/major/minor/mountpoint
// and does NOT carry fstype (the live-reference audit: label set differs from avail_bytes).
func TestNodeFilesystemMountInfo(t *testing.T) {
	mc := neTick(t)
	series := mc.Find("node_filesystem_mount_info")
	if len(series) == 0 {
		t.Fatal("node_filesystem_mount_info: no series emitted")
	}
	for _, s := range series {
		for _, k := range []string{"device", "major", "minor", "mountpoint"} {
			if s.Labels[k] == "" {
				t.Errorf("node_filesystem_mount_info missing non-empty label %q", k)
			}
		}
		if _, ok := s.Labels["fstype"]; ok {
			t.Errorf("node_filesystem_mount_info must NOT carry fstype label (audit: no fstype on this metric)")
		}
	}
}

// TestNodeFilesystemPurgeableBytes asserts node_filesystem_purgeable_bytes carries
// device/fstype/mountpoint (same label set as node_filesystem_avail_bytes — audit).
func TestNodeFilesystemPurgeableBytes(t *testing.T) {
	mc := neTick(t)
	series := mc.Find("node_filesystem_purgeable_bytes")
	if len(series) == 0 {
		t.Fatal("node_filesystem_purgeable_bytes: no series emitted")
	}
	for _, s := range series {
		for _, k := range []string{"device", "fstype", "mountpoint"} {
			if s.Labels[k] == "" {
				t.Errorf("node_filesystem_purgeable_bytes missing non-empty label %q", k)
			}
		}
	}
}
