// SPDX-License-Identifier: AGPL-3.0-only

// linux.go — EmitLinux: node_exporter metric emission for one Linux host.
//
// Ports the physics of internal/construct/k8scluster/nodeexporter.go verbatim,
// parameterized via the caller-supplied base label map and HostTopology.
// Every write goes through keepWriter(st, keepSet(prof)) so only profile-approved
// names are materialized in state; the union vocabulary is always computed.
//
// Counter-vs-gauge classification is PRESERVED EXACTLY from the source:
//   - st.Add (counter)  — node_cpu_seconds_total, node_disk_*, node_network_receive_*/transmit_*,
//     node_softnet_*, node_vmstat_*, node_netstat_*, node_context_switches_total,
//     node_intr_total, process_cpu_seconds_total.
//   - st.Set (gauge)    — everything else (load, memory, filesystem, network info/state,
//     timex, conntrack, filefd, process_{max,open}_fds, process_resident_memory_bytes,
//     node_cpu_online, up, node_os_info, node_uname_info, node_exporter_build_info).
package nodeexp

import (
	"fmt"

	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
)

// linuxCPUModes is the exact ordered mode set from k8scluster/helpers.go.
var linuxCPUModes = []string{"idle", "iowait", "irq", "nice", "softirq", "steal", "system", "user"}

// linuxGuestCPUModes are the modes for node_cpu_guest_seconds_total.
var linuxGuestCPUModes = []string{"nice", "user"}

// linuxNetstatMetrics are the 29 flat node_netstat_* counters.
var linuxNetstatMetrics = []string{
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
}

// linuxSockstatMetrics are the 18 flat node_sockstat_* gauges.
var linuxSockstatMetrics = []string{
	"node_sockstat_FRAG6_inuse", "node_sockstat_FRAG_inuse",
	"node_sockstat_RAW6_inuse", "node_sockstat_RAW_inuse",
	"node_sockstat_TCP6_inuse", "node_sockstat_TCP_alloc", "node_sockstat_TCP_inuse",
	"node_sockstat_TCP_mem", "node_sockstat_TCP_mem_bytes", "node_sockstat_TCP_orphan", "node_sockstat_TCP_tw",
	"node_sockstat_UDP6_inuse", "node_sockstat_UDP_inuse", "node_sockstat_UDP_mem", "node_sockstat_UDP_mem_bytes",
	"node_sockstat_UDPLITE6_inuse", "node_sockstat_UDPLITE_inuse",
	"node_sockstat_sockets_used",
}

// linuxVmstatCounters are the 7 node_vmstat_* cumulative counters.
var linuxVmstatCounters = []string{
	"node_vmstat_oom_kill", "node_vmstat_pgfault", "node_vmstat_pgmajfault",
	"node_vmstat_pgpgin", "node_vmstat_pgpgout", "node_vmstat_pswpin", "node_vmstat_pswpout",
}

// linuxMemMemInfoFields are the remaining node_memory_* MemInfo gauges beyond the core set.
// Ported verbatim from nodeexporter.go:neMemMemInfoFields (43 distinct fields total minus core 7).
var linuxMemMemInfoFields = []string{
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
}

// linuxNetstatRate returns a plausible per-second rate for a node_netstat_* counter.
// Ported verbatim from nodeexporter.go:netstatRate.
func linuxNetstatRate(nm string) float64 {
	switch nm {
	case "node_netstat_Tcp_InSegs", "node_netstat_Tcp_OutSegs":
		return 2000
	case "node_netstat_IpExt_InOctets", "node_netstat_IpExt_OutOctets":
		return 3_000_000
	case "node_netstat_Udp_InDatagrams", "node_netstat_Udp_OutDatagrams":
		return 200
	case "node_netstat_Icmp_InMsgs", "node_netstat_Icmp_OutMsgs":
		return 5
	default:
		return 0
	}
}

// linuxSockstatVal returns a plausible deterministic gauge value for a node_sockstat_* field.
// Ported verbatim from nodeexporter.go:sockstatVal.
func linuxSockstatVal(nm, hostname string, factor float64) float64 {
	h := hostHash(hostname, nm)
	switch nm {
	case "node_sockstat_sockets_used":
		return float64(150 + int(factor*300) + int(h*50))
	case "node_sockstat_TCP_inuse":
		return float64(80 + int(factor*200) + int(h*30))
	case "node_sockstat_TCP_alloc":
		return float64(100 + int(factor*220) + int(h*30))
	case "node_sockstat_TCP_tw":
		return float64(int(factor*60) + int(h*40))
	case "node_sockstat_UDP_inuse":
		return float64(8 + int(h*12))
	case "node_sockstat_TCP_mem", "node_sockstat_UDP_mem":
		return float64(int(h * 20))
	case "node_sockstat_TCP_mem_bytes", "node_sockstat_UDP_mem_bytes":
		return float64(int(h*20)) * 4096
	default: // FRAG/RAW/UDPLITE/orphan/inuse6 — typically 0
		return 0
	}
}

// linuxMAC derives a stable locally-administered MAC for a (hostname,device).
// Ported verbatim from nodeexporter.go:neMAC.
func linuxMAC(hostname, dev string) string {
	h := uint64(1469598103934665603)
	for _, c := range hostname + ":" + dev {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x",
		byte(h), byte(h>>8), byte(h>>16), byte(h>>24), byte(h>>32))
}

// linuxCPUBusyFrac returns the deterministic CPU utilisation fraction (0–1).
// Mirrors nodeCPUPercent from k8scluster/helpers.go but takes a host-stable offset
// derived from hostHash rather than a per-cluster nodeIdx.
func linuxCPUBusyFrac(hostname string, factor float64) float64 {
	// Use a stable per-host offset (analogous to nodeIdx%4 * 4 in k8scluster).
	offset := hostHash(hostname, "cpuidx") * 16 // [0, 16) pct offset
	pct := 22 + factor*50 + offset
	if pct < 1 {
		pct = 1
	}
	if pct > 99 {
		pct = 99
	}
	return pct / 100.0
}

// cloneBase returns a shallow copy of base so per-series merges never mutate it.
func cloneBase(base map[string]string) map[string]string {
	out := make(map[string]string, len(base))
	for k, v := range base {
		out[k] = v
	}
	return out
}

// mergeBase clones base and adds extra labels, returning the merged map.
func mergeBase(base, extra map[string]string) map[string]string {
	out := cloneBase(base)
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// EmitLinux renders one Linux host's node_exporter series into st under the
// caller-supplied base identity labels (job, instance, + any context labels),
// filtered by prof. Ports the physics of internal/construct/k8scluster/nodeexporter.go.
func EmitLinux(st *state.State, base map[string]string, top HostTopology, prof Profile, factor, tickSec, scale float64, sh *shape.Engine) {
	ks := keepSet(prof)
	set, add := keepWriter(st, ks)

	hostname := top.Hostname

	// ── up (synthetic scrape-up; gauge) ────────────────────────────────────────
	set("up", cloneBase(base), 1)

	// ── Identity info metrics ───────────────────────────────────────────────────
	// node_uname_info: machine from OS.Machine; sysname always "Linux".
	unameLabels := mergeBase(base, map[string]string{
		"machine":    top.OS.Machine,
		"sysname":    "Linux",
		"release":    top.OS.Kernel,
		"nodename":   hostname,
		"domainname": "(none)",
		"version":    "#1 SMP PREEMPT_DYNAMIC",
	})
	// Omit any empty-string label values (absent-dimension rule I13) — e.g. release="" when
	// no kernel is supplied. Mirrors EmitMacOS.
	for k, v := range unameLabels {
		if v == "" {
			delete(unameLabels, k)
		}
	}
	set("node_uname_info", unameLabels, 1)

	// node_os_info: id/name/pretty_name/version/version_id always; variant_id/build_id only when set.
	osInfo := map[string]string{
		"id":          top.OS.ID,
		"name":        top.OS.Name,
		"pretty_name": top.OS.PrettyName,
		"version":     top.OS.Version,
		"version_id":  top.OS.VersionID,
	}
	// variant_id/build_id are present only on some distros (Bottlerocket) — add them
	// ONLY when non-empty; absent dimension OMITTED (I13).
	if top.OS.VariantID != "" {
		osInfo["variant_id"] = top.OS.VariantID
	}
	if top.OS.BuildID != "" {
		osInfo["build_id"] = top.OS.BuildID
	}
	osInfoLabels := mergeBase(base, osInfo)
	// Omit any empty-string label values (absent-dimension rule I13). Mirrors EmitMacOS.
	for k, v := range osInfoLabels {
		if v == "" {
			delete(osInfoLabels, k)
		}
	}
	set("node_os_info", osInfoLabels, 1)

	// ── Load gauges ───────────────────────────────────────────────────────────────
	load1 := 0.5 + factor*3.5*sh.Noise(0.25)
	if load1 < 0.1 {
		load1 = 0.1
	}
	set("node_load1", cloneBase(base), load1)
	set("node_load5", cloneBase(base), load1*(0.85+sh.Noise(0.05)*0.1))
	set("node_load15", cloneBase(base), load1*(0.7+sh.Noise(0.05)*0.1))

	// ── Memory: core gauges ─────────────────────────────────────────────────────
	memTotal := top.MemTotal
	availFrac := (0.30 + (1-factor)*0.45) * sh.Noise(0.04)
	if availFrac > 0.9 {
		availFrac = 0.9
	}
	if availFrac < 0.1 {
		availFrac = 0.1
	}
	memAvail := memTotal * availFrac
	memFree := memAvail * 0.6
	set("node_memory_MemAvailable_bytes", cloneBase(base), memAvail)
	set("node_memory_MemTotal_bytes", cloneBase(base), memTotal)
	set("node_memory_MemFree_bytes", cloneBase(base), memFree)
	set("node_memory_Buffers_bytes", cloneBase(base), 256*1024*1024*sh.Noise(0.1))
	set("node_memory_Cached_bytes", cloneBase(base), memAvail-memFree)
	set("node_memory_SwapTotal_bytes", cloneBase(base), 0)
	set("node_memory_SwapFree_bytes", cloneBase(base), 0)

	// Memory: remaining MemInfo gauges.
	used := memTotal - memAvail
	if used < 0 {
		used = 0
	}
	for _, nm := range linuxMemMemInfoFields {
		var v float64
		switch nm {
		case "node_memory_Active_bytes":
			v = used * 0.45
		case "node_memory_Active_anon_bytes":
			v = used * 0.25
		case "node_memory_Active_file_bytes":
			v = used * 0.20
		case "node_memory_Inactive_bytes":
			v = used * 0.30
		case "node_memory_Inactive_anon_bytes":
			v = used * 0.10
		case "node_memory_Inactive_file_bytes":
			v = used * 0.20
		case "node_memory_AnonPages_bytes":
			v = used * 0.35
		case "node_memory_Mapped_bytes":
			v = used * 0.08
		case "node_memory_Shmem_bytes":
			v = used * 0.05
		case "node_memory_Slab_bytes":
			v = used * 0.06
		case "node_memory_SReclaimable_bytes":
			v = used * 0.04
		case "node_memory_SUnreclaim_bytes":
			v = used * 0.02
		case "node_memory_KernelStack_bytes":
			v = 16 * 1024 * 1024
		case "node_memory_PageTables_bytes":
			v = used * 0.01
		case "node_memory_Percpu_bytes":
			v = float64(top.NumCPU) * 2 * 1024 * 1024
		case "node_memory_CommitLimit_bytes":
			v = memTotal * 0.5
		case "node_memory_Committed_AS_bytes":
			v = used * 1.2
		case "node_memory_Dirty_bytes":
			v = 256 * 1024 * hostHash(hostname, nm)
		case "node_memory_Writeback_bytes", "node_memory_WritebackTmp_bytes",
			"node_memory_NFS_Unstable_bytes", "node_memory_Bounce_bytes",
			"node_memory_HardwareCorrupted_bytes", "node_memory_Mlocked_bytes",
			"node_memory_CmaFree_bytes", "node_memory_CmaTotal_bytes",
			"node_memory_HugePages_Free", "node_memory_HugePages_Rsvd",
			"node_memory_HugePages_Surp", "node_memory_HugePages_Total",
			"node_memory_AnonHugePages_bytes", "node_memory_ShmemHugePages_bytes",
			"node_memory_ShmemPmdMapped_bytes", "node_memory_SwapCached_bytes",
			"node_memory_Zswap_bytes", "node_memory_Zswapped_bytes":
			v = 0
		case "node_memory_Hugepagesize_bytes":
			v = 2 * 1024 * 1024
		case "node_memory_Unevictable_bytes":
			v = used * 0.01
		case "node_memory_VmallocTotal_bytes":
			v = 0x7fffffffffff
		case "node_memory_VmallocUsed_bytes":
			v = used * 0.02
		case "node_memory_VmallocChunk_bytes":
			v = 0
		default:
			v = used * 0.01 * (0.5 + hostHash(hostname, nm))
		}
		set(nm, cloneBase(base), v)
	}

	// ── Filesystem gauges (caller-supplied FS mount) ──────────────────────────────
	fsBase := mergeBase(base, map[string]string{
		"device":     top.FS.Device,
		"fstype":     top.FS.FSType,
		"mountpoint": top.FS.Mountpoint,
	})
	fsSize := top.FS.SizeBytes
	if fsSize == 0 {
		fsSize = 100.0 * 1024 * 1024 * 1024
	}
	fsAvail := (30 + sh.Float64()*50) * 1024 * 1024 * 1024
	set("node_filesystem_avail_bytes", fsBase, fsAvail)
	set("node_filesystem_size_bytes", fsBase, fsSize)
	set("node_filesystem_free_bytes", fsBase, fsAvail)
	set("node_filesystem_files", fsBase, 6_553_600)
	set("node_filesystem_files_free", fsBase, 6_000_000)
	set("node_filesystem_device_error", fsBase, 0)
	set("node_filesystem_readonly", fsBase, 0)
	// node_filesystem_mount_info — labels: device, major, minor, mountpoint (NO fstype).
	set("node_filesystem_mount_info", mergeBase(base, map[string]string{
		"device":     top.FS.Device,
		"major":      "259",
		"minor":      "0",
		"mountpoint": top.FS.Mountpoint,
	}), 1)
	set("node_filesystem_purgeable_bytes", fsBase, 0)

	// ── CPU: node_cpu_seconds_total (CUMULATIVE counter, per cpu × mode) ──────────
	busyFrac := linuxCPUBusyFrac(hostname, factor)
	idleFrac := 1 - busyFrac
	for cpu := 0; cpu < top.NumCPU; cpu++ {
		for _, mode := range linuxCPUModes {
			cpuLbls := mergeBase(base, map[string]string{
				"cpu":  fmt.Sprintf("%d", cpu),
				"mode": mode,
			})
			var delta float64
			switch mode {
			case "idle":
				delta = tickSec * idleFrac * sh.Noise(0.05)
			case "user":
				delta = tickSec * busyFrac * 0.55 * sh.Noise(0.2)
			case "system":
				delta = tickSec * busyFrac * 0.25 * sh.Noise(0.2)
			case "iowait":
				delta = tickSec * busyFrac * 0.10 * sh.Noise(0.3)
			case "softirq":
				delta = tickSec * busyFrac * 0.05 * sh.Noise(0.4)
			case "irq":
				delta = tickSec * busyFrac * 0.03 * sh.Noise(0.4)
			case "nice", "steal":
				delta = tickSec * 0.001 * sh.Noise(0.5)
			}
			if delta < 0 {
				delta = 0
			}
			add("node_cpu_seconds_total", cpuLbls, delta)
		}
		// node_cpu_guest_seconds_total (counter; modes nice,user — near-zero on these nodes)
		for _, gm := range linuxGuestCPUModes {
			gl := mergeBase(base, map[string]string{"cpu": fmt.Sprintf("%d", cpu), "mode": gm})
			add("node_cpu_guest_seconds_total", gl, tickSec*0.0001*sh.Noise(0.5))
		}
		// node_cpu_online (gauge, per cpu)
		set("node_cpu_online", mergeBase(base, map[string]string{"cpu": fmt.Sprintf("%d", cpu)}), 1)
		// node_softnet_* (counters, per cpu)
		scpu := mergeBase(base, map[string]string{"cpu": fmt.Sprintf("%d", cpu)})
		add("node_softnet_processed_total", scpu, tickSec*1500*busyFrac*sh.Noise(0.3))
		add("node_softnet_times_squeezed_total", scpu, tickSec*0.5*busyFrac*sh.Noise(0.5))
		add("node_softnet_dropped_total", scpu, 0)
	}

	// ── Disk I/O (CUMULATIVE counters, per device from top.Disks) ─────────────────
	for _, dev := range top.Disks {
		dl := mergeBase(base, map[string]string{"device": dev})
		rw := factor * tickSec * (0.5 + hostHash(hostname, dev))
		add("node_disk_io_time_seconds_total", dl, tickSec*0.2*busyFrac*sh.Noise(0.3))
		add("node_disk_io_time_weighted_seconds_total", dl, tickSec*0.4*busyFrac*sh.Noise(0.3))
		add("node_disk_read_bytes_total", dl, rw*4*1024*1024*sh.Noise(0.4))
		add("node_disk_written_bytes_total", dl, rw*8*1024*1024*sh.Noise(0.4))
		add("node_disk_read_time_seconds_total", dl, tickSec*0.05*sh.Noise(0.4))
		add("node_disk_write_time_seconds_total", dl, tickSec*0.10*sh.Noise(0.4))
		add("node_disk_reads_completed_total", dl, rw*50*sh.Noise(0.4))
		add("node_disk_writes_completed_total", dl, rw*120*sh.Noise(0.4))
	}

	// ── Network (per NIC from top.NICs) ───────────────────────────────────────────
	for _, nic := range top.NICs {
		netDev := mergeBase(base, map[string]string{"device": nic.Name})
		rxBase := nodeRxBase(nic.Name, factor, sh.Noise(0.3), scale)
		nodeRx := rxBase
		nodeTx := rxBase * (0.4 + sh.Float64()*0.3)
		add("node_network_receive_bytes_total", netDev, nodeRx)
		add("node_network_transmit_bytes_total", netDev, nodeTx)
		add("node_network_receive_packets_total", netDev, nodeRx/1400*sh.Noise(0.2))
		add("node_network_transmit_packets_total", netDev, nodeTx/1400*sh.Noise(0.2))
		add("node_network_receive_drop_total", netDev, 0)
		add("node_network_transmit_drop_total", netDev, 0)
		add("node_network_receive_errs_total", netDev, 0)
		add("node_network_transmit_errs_total", netDev, 0)
		add("node_network_receive_fifo_total", netDev, 0)
		add("node_network_transmit_fifo_total", netDev, 0)
		add("node_network_receive_compressed_total", netDev, 0)
		add("node_network_transmit_compressed_total", netDev, 0)
		add("node_network_receive_multicast_total", netDev, hostHash(hostname, nic.Name)*5)

		// State/info gauges.
		nicUp := 1.0
		operstate := "up"
		if nic.Name == "lo" {
			operstate = "unknown"
		}
		set("node_network_up", netDev, nicUp)
		set("node_network_carrier", netDev, nicUp)
		set("node_network_mtu_bytes", netDev, 9001) // typical ENA MTU; lo uses 65536 in real nodes
		set("node_network_transmit_queue_length", netDev, 1000)
		if nic.SpeedBytes > 0 {
			set("node_network_speed_bytes", netDev, nic.SpeedBytes)
		}
		// node_network_info: device/address/adminstate/broadcast/operstate keys.
		addr := linuxMAC(hostname, nic.Name)
		bcast := "ff:ff:ff:ff:ff:ff"
		if nic.Name == "lo" {
			addr = "00:00:00:00:00:00"
			bcast = "00:00:00:00:00:00"
		}
		set("node_network_info", mergeBase(base, map[string]string{
			"device":     nic.Name,
			"address":    addr,
			"adminstate": "up",
			"broadcast":  bcast,
			"operstate":  operstate,
		}), 1)
	}

	// ── ARP (representative — use first non-lo NIC or eth0) ──────────────────────
	arpDev := "eth0"
	for _, nic := range top.NICs {
		if nic.Name != "lo" {
			arpDev = nic.Name
			break
		}
	}
	set("node_arp_entries", mergeBase(base, map[string]string{"device": arpDev}),
		float64(10+int(hostHash(hostname, "arp")*40)))

	// ── Netstat (29 flat cumulative counters) ──────────────────────────────────────
	for _, nm := range linuxNetstatMetrics {
		add(nm, cloneBase(base), tickSec*linuxNetstatRate(nm)*sh.Noise(0.3))
	}

	// ── Sockstat (18 flat gauges) ──────────────────────────────────────────────────
	for _, nm := range linuxSockstatMetrics {
		set(nm, cloneBase(base), linuxSockstatVal(nm, hostname, factor))
	}

	// ── Vmstat (7 flat cumulative counters) ────────────────────────────────────────
	for _, nm := range linuxVmstatCounters {
		var rate float64
		switch nm {
		case "node_vmstat_pgfault":
			rate = 5000
		case "node_vmstat_pgpgin":
			rate = 800
		case "node_vmstat_pgpgout":
			rate = 1200
		case "node_vmstat_pgmajfault":
			rate = 2
		default: // oom_kill, pswpin, pswpout — near-zero on healthy nodes
			rate = 0
		}
		add(nm, cloneBase(base), tickSec*rate*factor*sh.Noise(0.3))
	}

	// ── Timex / time ────────────────────────────────────────────────────────────────
	set("node_timex_estimated_error_seconds", cloneBase(base), 0.000001*hostHash(hostname, "esterr"))
	set("node_timex_maxerror_seconds", cloneBase(base), 0.0001*(1+hostHash(hostname, "maxerr")))
	set("node_timex_offset_seconds", cloneBase(base), 0.00001*(hostHash(hostname, "offset")-0.5))
	set("node_timex_sync_status", cloneBase(base), 1)
	set("node_time_zone_offset_seconds", mergeBase(base, map[string]string{"time_zone": "UTC"}), 0)

	// ── Conntrack ────────────────────────────────────────────────────────────────────
	set("node_nf_conntrack_entries", cloneBase(base), float64(200+int(hostHash(hostname, "ct")*2000)))
	set("node_nf_conntrack_entries_limit", cloneBase(base), 131072)

	// ── Misc scalar gauges / counters ──────────────────────────────────────────────
	set("node_boot_time_seconds", cloneBase(base), top.BootTime)
	add("node_context_switches_total", cloneBase(base), tickSec*20000*factor*sh.Noise(0.3))
	add("node_intr_total", cloneBase(base), tickSec*30000*factor*sh.Noise(0.3))
	set("node_procs_running", cloneBase(base), float64(1+int(busyFrac*float64(top.NumCPU)*4)))
	set("node_filefd_allocated", cloneBase(base), float64(1000+int(hostHash(hostname, "fd")*3000)))
	set("node_filefd_maximum", cloneBase(base), 9223372036854775807)
	set("node_textfile_scrape_error", cloneBase(base), 0)

	// node_exporter_build_info (full label set).
	set("node_exporter_build_info", mergeBase(base, map[string]string{
		"version":   "1.11.1",
		"revision":  "0dd664dece3f8319f6bec5a221acd2c7ad13a23d",
		"branch":    "HEAD",
		"goversion": "go1.26.1",
		"goos":      "linux",
		"goarch":    "arm64",
		"tags":      "unknown",
	}), 1)

	// Process metrics (node-exporter process itself).
	add("process_cpu_seconds_total", cloneBase(base), tickSec*0.002*sh.Noise(0.3))
	set("process_resident_memory_bytes", cloneBase(base), 22*1024*1024*sh.Noise(0.1))
	set("process_max_fds", cloneBase(base), 1048576)
	set("process_open_fds", cloneBase(base), float64(8+int(hostHash(hostname, "pfd")*12)))

	// Universal node_exporter self-metrics — the ProfileFull delta (host-capture.md confirms
	// promhttp_*/go_* present on every real node_exporter). keepSet filters them out under
	// ProfileIntegration; emitting them unconditionally keeps `full` a real superset.
	set("go_goroutines", cloneBase(base), float64(20+int(hostHash(hostname, "goroutines")*40)))
	set("go_memstats_alloc_bytes", cloneBase(base), 4*1024*1024*sh.Noise(0.15))
	add("promhttp_metric_handler_requests_total", mergeBase(base, map[string]string{"code": "200"}),
		tickSec/30.0*sh.Noise(0.05))

	// node_md_disks / node_md_disks_required — integration allowlist has these;
	// k8s profile nodes (EKS/Bottlerocket) typically have no RAID → not emitted
	// in the k8s source. Emit under integration/full profiles (keepSet filters for k8s).
	set("node_md_disks", cloneBase(base), 0)
	set("node_md_disks_required", cloneBase(base), 0)

	// node_systemd_unit_state — integration allowlist name; not in k8s keepset.
	// Emit a representative state for the synthetic node (loaded + active + running = 1).
	set("node_systemd_unit_state", mergeBase(base, map[string]string{
		"name":  "node-exporter.service",
		"state": "active",
		"type":  "service",
	}), 1)
}
