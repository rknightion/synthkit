// SPDX-License-Identifier: AGPL-3.0-only

// macos.go — EmitMacOS: macOS node_exporter emission for one macOS host.
//
// job="integrations/macos-node"; instance = hostname (caller-supplied in base).
//
// Metric vocabulary is the Appendix A macOS integration allowlist from the plan:
// the macOS memory family (node_memory_total_bytes / compressed / internal / purgeable /
// wired / swap_total / swap_used) replaces the Linux MemInfo family entirely.
// Netstat / sockstat / vmstat / timex are omitted (not present in the macOS allowlist;
// macOS node_exporter does not expose them).
//
// All metric names are sourced verbatim from macosIntegrationNames in profiles.go.
// No name is invented here.
package nodeexp

import (
	"fmt"

	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
)

// macosMAC derives a stable locally-administered MAC for a (hostname, device) pair,
// using the same FNV-1a polynomial as hostHash / neMAC.
func macosMAC(hostname, dev string) string {
	h := uint64(1469598103934665603)
	for _, c := range hostname + ":" + dev {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x",
		byte(h), byte(h>>8), byte(h>>16), byte(h>>24), byte(h>>32))
}

// macosLabels returns a new map cloned from base and extended with extra.
// Never modifies base in-place (clone-before-stamp contract).
func macosLabels(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// cpuModesMacOS are the cpu modes emitted by macOS node_exporter.
// macOS exposes idle/nice/system/user (no iowait/softirq/irq/steal).
var cpuModesMacOS = []string{"idle", "nice", "system", "user"}

// EmitMacOS renders one macOS host's node_exporter series into st under the
// caller-supplied base identity labels (job, instance, + any context labels),
// filtered by macosKeepSet(prof).
//
// The macOS memory family (node_memory_total_bytes / compressed / internal /
// purgeable / wired / swap_total / swap_used) replaces the Linux MemInfo family.
// CPU / disk / network / filesystem / load / identity physics match EmitLinux
// (shared helpers from physics.go). Netstat / sockstat / vmstat / timex are NOT
// emitted (absent from the macOS allowlist; macOS node_exporter does not expose them).
func EmitMacOS(st *state.State, base map[string]string, top HostTopology, prof Profile, factor, tickSec, scale float64, sh *shape.Engine) {
	ks := macosKeepSet(prof)
	set, add := keepWriter(st, ks)

	seed := top.Hostname

	// ── Identity info metrics ─────────────────────────────────────────────────────
	// node_uname_info: machine/sysname/release/nodename/domainname/version
	unameLabels := macosLabels(base, map[string]string{
		"machine":    top.OS.Machine,
		"sysname":    "Darwin",
		"release":    top.OS.Kernel,
		"nodename":   top.Hostname,
		"domainname": "(none)",
		"version":    "Darwin Kernel Version " + top.OS.Kernel,
	})
	// Omit any empty-string label values (absent-dimension rule I13).
	for k, v := range unameLabels {
		if v == "" {
			delete(unameLabels, k)
		}
	}
	set("node_uname_info", unameLabels, 1)

	// node_os_info
	osInfo := macosLabels(base, map[string]string{
		"id":          top.OS.ID,
		"name":        top.OS.Name,
		"pretty_name": top.OS.PrettyName,
		"version_id":  top.OS.VersionID,
	})
	// Only add version if non-empty.
	if top.OS.Version != "" {
		osInfo["version"] = top.OS.Version
	}
	// Omit empty strings.
	for k, v := range osInfo {
		if v == "" {
			delete(osInfo, k)
		}
	}
	set("node_os_info", osInfo, 1)

	// ── Identity / scrape health ──────────────────────────────────────────────────
	set("node_boot_time_seconds", base, top.BootTime)
	set("node_textfile_scrape_error", base, 0)
	set("up", base, 1)

	// ── Load gauges ───────────────────────────────────────────────────────────────
	noise := sh.Noise(0.25)
	load1 := 0.5 + factor*float64(top.NumCPU)*0.5*noise
	if load1 < 0.1 {
		load1 = 0.1
	}
	set("node_load1", base, load1)
	set("node_load5", base, load1*(0.85+sh.Noise(0.05)*0.1))
	set("node_load15", base, load1*(0.7+sh.Noise(0.05)*0.1))

	// ── macOS memory family (replaces Linux MemInfo entirely) ─────────────────────
	// Ratios RECONCILED against the captured a homelab reference host (host-capture.md
	// "macOS memory metrics", 32 GiB total):
	//   total      = top.MemTotal (declared)
	//   wired      ≈ 12% (4.08 GiB / 32 GiB) — kernel + wired processes
	//   compressed ≈ 13% (4.48 GiB / 32 GiB) — compressed VM pages (the REAL pressure on macOS)
	//   internal   ≈ 24% (8.29 GiB / 32 GiB) — app memory / working set
	//   purgeable  ≈ 0.3% (93 MiB / 32 GiB) — evictable caches (small)
	//   swap_total = 1 GiB (capture: node_memory_swap_total_bytes = 1,073,741,824)
	//   swap_used  ≈ near-zero (capture: 196,608 bytes; macOS leans on compression, not swap)
	memTotal := top.MemTotal
	set("node_memory_total_bytes", base, memTotal)
	set("node_memory_wired_bytes", base, memTotal*0.12*(0.9+sh.Noise(0.1)*0.2))
	set("node_memory_compressed_bytes", base, memTotal*0.13*(0.7+factor*0.4+sh.Noise(0.15)*0.2))
	set("node_memory_internal_bytes", base, memTotal*0.24*(0.85+factor*0.2+sh.Noise(0.1)*0.2))
	set("node_memory_purgeable_bytes", base, memTotal*0.003*(0.5+sh.Noise(0.3)))

	const swapTotal = 1.0 * 1024 * 1024 * 1024 // 1 GiB — capture: node_memory_swap_total_bytes
	// swap_used is near-zero on macOS (compression absorbs pressure). Stay in the low-MiB band
	// even under load — capture saw ~192 KiB; cap well under the test's 64 MiB ceiling.
	swapUsed := 200_000 + factor*4*1024*1024*sh.Noise(0.3)
	if swapUsed < 0 {
		swapUsed = 0
	}
	set("node_memory_swap_total_bytes", base, swapTotal)
	set("node_memory_swap_used_bytes", base, swapUsed)

	// ── CPU: node_cpu_seconds_total (CUMULATIVE counter, per cpu × mode) ──────────
	// macOS node_exporter exposes idle/nice/system/user (no iowait/softirq/irq/steal).
	// busyFrac is derived from factor + a per-host stable offset via hostHash.
	busyFrac := (0.1 + factor*0.4*(0.7+hostHash(seed, "cpubusy")*0.6))
	if busyFrac > 0.95 {
		busyFrac = 0.95
	}
	idleFrac := 1.0 - busyFrac

	for cpu := 0; cpu < top.NumCPU; cpu++ {
		for _, mode := range cpuModesMacOS {
			cpuLbls := macosLabels(base, map[string]string{
				"cpu":  fmt.Sprintf("%d", cpu),
				"mode": mode,
			})
			var delta float64
			switch mode {
			case "idle":
				delta = tickSec * idleFrac * sh.Noise(0.05)
			case "user":
				delta = tickSec * busyFrac * 0.60 * sh.Noise(0.2)
			case "system":
				delta = tickSec * busyFrac * 0.35 * sh.Noise(0.2)
			case "nice":
				delta = tickSec * 0.001 * sh.Noise(0.5)
			}
			if delta < 0 {
				delta = 0
			}
			add("node_cpu_seconds_total", cpuLbls, delta)
		}
	}

	// ── Disk I/O (CUMULATIVE counters) ────────────────────────────────────────────
	// macOS allowlist: node_disk_io_time_seconds_total, node_disk_read_bytes_total,
	// node_disk_written_bytes_total.
	for _, dev := range top.Disks {
		dl := macosLabels(base, map[string]string{"device": dev})
		rw := factor * tickSec * (0.5 + hostHash(seed, dev))
		add("node_disk_io_time_seconds_total", dl, tickSec*0.2*busyFrac*sh.Noise(0.3))
		add("node_disk_read_bytes_total", dl, rw*4*1024*1024*sh.Noise(0.4))
		add("node_disk_written_bytes_total", dl, rw*8*1024*1024*sh.Noise(0.4))
	}

	// ── Network (per-NIC; representative set) ──────────────────────────────────────
	// macOS allowlist: node_network_receive_bytes_total, transmit_bytes_total,
	// receive_drop_total, transmit_drop_total, receive_errs_total, transmit_errs_total,
	// receive_packets_total, transmit_packets_total.
	noise30 := sh.Noise(0.3)
	for _, nic := range top.NICs {
		netDev := macosLabels(base, map[string]string{"device": nic.Name})
		rxBase := nodeRxBase(nic.Name, factor, noise30, scale)
		rx := rxBase
		tx := rx * (0.4 + sh.Float64()*0.3)
		add("node_network_receive_bytes_total", netDev, rx)
		add("node_network_transmit_bytes_total", netDev, tx)
		add("node_network_receive_packets_total", netDev, rx/1400*sh.Noise(0.2))
		add("node_network_transmit_packets_total", netDev, tx/1400*sh.Noise(0.2))
		add("node_network_receive_drop_total", netDev, 0)
		add("node_network_transmit_drop_total", netDev, 0)
		add("node_network_receive_errs_total", netDev, 0)
		add("node_network_transmit_errs_total", netDev, 0)
	}

	// ── Filesystem gauges ─────────────────────────────────────────────────────────
	// macOS allowlist: node_filesystem_avail_bytes, files, files_free, readonly, size_bytes.
	// (node_filesystem_device_error is NOT in the macOS allowlist — omit.)
	if top.FS.Device != "" && top.FS.Mountpoint != "" && top.FS.FSType != "" {
		fsBase := macosLabels(base, map[string]string{
			"device":     top.FS.Device,
			"fstype":     top.FS.FSType,
			"mountpoint": top.FS.Mountpoint,
		})
		fsSize := top.FS.SizeBytes
		fsAvail := fsSize * (0.30 + sh.Float64()*0.50)
		set("node_filesystem_avail_bytes", fsBase, fsAvail)
		set("node_filesystem_size_bytes", fsBase, fsSize)
		set("node_filesystem_files", fsBase, 1_000_000_000) // APFS has no fixed inode count
		set("node_filesystem_files_free", fsBase, float64(int(1_000_000_000*(0.85+hostHash(seed, "inodes")*0.10))))
		set("node_filesystem_readonly", fsBase, 0)
	}
}
