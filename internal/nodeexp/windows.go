// SPDX-License-Identifier: AGPL-3.0-only

// windows.go — EmitWindows: windows_exporter metric emission for one Windows host.
//
// Ports the physics of internal/construct/k8scluster/windowsexporter.go verbatim,
// parameterized via the caller-supplied base label map and HostTopology:
//   - Identity comes from caller-supplied base (never hardcoded job/instance).
//   - CPU core count is driven by top.NumCPU (source hardcodes 2).
//   - Volumes are driven by top.Disks (source hardcodes []string{"C:"}).
//   - NICs are driven by top.NICs (source hardcodes a single "Amazon Elastic Network Adapter").
//   - Stable offsets use hostHash(top.Hostname, key) (same polynomial as weHash in source).
//
// Counter-vs-gauge classification is PRESERVED EXACTLY from the source:
//   - st.Add (counter): windows_cpu_time_total, windows_logical_disk_read_bytes_total,
//     windows_logical_disk_write_bytes_total, windows_net_bytes_received_total,
//     windows_net_bytes_sent_total, windows_net_packets_received_total.
//   - st.Set (gauge): everything else (memory, os_info, os_hostname, logical_disk_size,
//     logical_disk_free, cpu_logical_processor, up).
//
// LIVE-CONFIRMED names: the names emitted here are those confirmed by the
// 2026-06-15 k8s-monitoring live capture (see windowsexporter.go header) OR by the
// 2026-06-17 homelab reference WINSRV capture (host-capture.md). The integration-only
// extras (windows_service_state, windows_diskdrive_status, windows_system_*,
// windows_pagefile_*, windows_time_*) were ADDED from that WINSRV capture with the exact
// label keys it shows. Names the capture did NOT confirm — windows_cs_*,
// windows_service_status (phantom), windows_disk_drive_status (wrong spelling),
// windows_os_paging_limit_bytes, windows_os_physical_memory_free_bytes, windows_os_timezone,
// windows_system_system_up_time — are NOT emitted and were dropped from the keepset.
package nodeexp

import (
	"fmt"

	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
)

// wCPUModes are the Windows CPU time modes for windows_cpu_time_total.
var wCPUModes = []string{"idle", "user", "privileged", "interrupt", "dpc"}

// mergeLabels returns a new map with all keys from base plus the extra pairs,
// without mutating base. This is the windows-emitter equivalent of weWith.
func mergeLabels(base map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// EmitWindows renders one Windows host's windows_exporter series into st under the
// caller-supplied base identity labels (job, instance, + any context labels),
// filtered by prof.
//
// Ports internal/construct/k8scluster/windowsexporter.go physics:
//   - emitWindowsExporter / weHash / weWith
//
// Deviations from source intentionally made here:
//  1. Cores driven by top.NumCPU (source hardcodes 2).
//  2. Volumes driven by top.Disks (source hardcodes ["C:"]).
//  3. NICs driven by top.NICs[i].Name (source hardcodes ["Amazon Elastic Network Adapter"]).
//  4. FIX: windows_logical_disk_free_bytes = diskFree directly.
//     Source bug: diskFree*diskSize/100 applies an unintentional 1 GiB multiplier
//     (diskSize=100<<30, so /100 = 1<<30 ≈ 1 GiB) making the output astronomically large.
//     Correct behaviour: emit diskFree (already in bytes).
func EmitWindows(st *state.State, base map[string]string, top HostTopology, prof Profile, factor, tickSec, scale float64, sh *shape.Engine) {
	_ = sh // not used currently — windows physics are deterministic-plausible without shape noise

	set, add := keepWriter(st, keepSetWindows(prof))

	hostname := top.Hostname
	h := hostHash(hostname, "windows")

	// ── up (gauge) ──────────────────────────────────────────────────────────────────────
	set("up", base, 1)

	// ── windows_cpu_time_total (counter, per core × mode) ───────────────────────────────
	busyFrac := factor*(0.4+h*0.3) + 0.1
	if busyFrac > 0.95 {
		busyFrac = 0.95
	}
	idleFrac := 1 - busyFrac

	for core := 0; core < top.NumCPU; core++ {
		for _, mode := range wCPUModes {
			cpuLbls := mergeLabels(base, map[string]string{
				"core": fmt.Sprintf("0,%d", core), // live format: "<group>,<core>" e.g. "0,0"
				"mode": mode,
			})
			var delta float64
			switch mode {
			case "idle":
				delta = tickSec * idleFrac * scale
			case "user":
				delta = tickSec * busyFrac * 0.55 * scale
			case "privileged":
				delta = tickSec * busyFrac * 0.30 * scale
			case "interrupt":
				delta = tickSec * busyFrac * 0.10 * scale
			case "dpc":
				delta = tickSec * busyFrac * 0.05 * scale
			}
			if delta < 0 {
				delta = 0
			}
			add("windows_cpu_time_total", cpuLbls, delta)
		}
	}

	// ── windows_cpu_logical_processor (gauge) — cpu collector ───────────────────────────
	// Live: replaces windows_cs_logical_processors (cs collector not enabled by default).
	set("windows_cpu_logical_processor", base, float64(top.NumCPU))

	// ── windows_memory_* (gauges; memory collector) ─────────────────────────────────────
	physMem := top.MemTotal
	set("windows_memory_physical_total_bytes", base, physMem)
	memAvail := (2 + (1-factor)*4) * 1024 * 1024 * 1024 * (0.9 + h*0.2)
	if memAvail < 256*1024*1024 {
		memAvail = 256 * 1024 * 1024
	}
	set("windows_memory_available_bytes", base, memAvail)
	set("windows_memory_physical_free_bytes", base, memAvail)
	set("windows_memory_committed_bytes", base, physMem*0.6+factor*physMem*0.2)

	// ── windows_os_info / windows_os_hostname (gauges=1; info series; os collector) ─────
	// Use top.OS fields if available, falling back to the live-captured Windows Server 2022
	// defaults from the k8s source (product/version/major_version/minor_version/build_number/revision).
	product := top.OS.Product
	if product == "" {
		product = "Windows Server 2022 Datacenter"
	}
	osVersion := top.OS.Version
	if osVersion == "" {
		osVersion = "10.0.20348"
	}
	majorVersion := top.OS.MajorVersion
	if majorVersion == "" {
		majorVersion = "10"
	}
	minorVersion := top.OS.MinorVersion
	if minorVersion == "" {
		minorVersion = "0"
	}
	buildNumber := top.OS.Build
	if buildNumber == "" {
		buildNumber = "20348"
	}
	revision := top.OS.Revision
	if revision == "" {
		revision = "5139"
	}

	set("windows_os_info", mergeLabels(base, map[string]string{
		"product":       product,
		"version":       osVersion,
		"major_version": majorVersion,
		"minor_version": minorVersion,
		"build_number":  buildNumber,
		"revision":      revision,
	}), 1)
	set("windows_os_hostname", mergeLabels(base, map[string]string{"hostname": hostname}), 1)

	// ── windows_logical_disk_* (per volume; logical_disk collector) ─────────────────────
	volumes := top.Disks
	if len(volumes) == 0 {
		volumes = []string{"C:"}
	}
	for _, vol := range volumes {
		diskLbls := mergeLabels(base, map[string]string{"volume": vol})
		diskSize := 100.0 * 1024 * 1024 * 1024
		diskFree := (30 + hostHash(hostname, vol)*50) * 1024 * 1024 * 1024
		ioRate := factor * tickSec * (0.5 + hostHash(hostname, vol+"-io"))
		set("windows_logical_disk_size_bytes", diskLbls, diskSize)
		add("windows_logical_disk_read_bytes_total", diskLbls, ioRate*4*1024*1024*scale)
		add("windows_logical_disk_write_bytes_total", diskLbls, ioRate*8*1024*1024*scale)
		// FIX: emit diskFree directly.
		// Source (windowsexporter.go:125) has `diskFree*diskSize/100` which is a bug:
		// diskFree is already in bytes; multiplying by diskSize(100<<30)/100 = 1<<30
		// produces ~32× larger values than intended. The lib corrects this.
		set("windows_logical_disk_free_bytes", diskLbls, diskFree)
	}

	// ── windows_net_* (per NIC; net collector) ──────────────────────────────────────────
	nics := top.NICs
	if len(nics) == 0 {
		nics = []NIC{{Name: "Ethernet", SpeedBytes: 1e9}}
	}
	for _, nic := range nics {
		nicLbls := mergeLabels(base, map[string]string{"nic": nic.Name})
		netBase := (5*1024*1024 + factor*80*1024*1024) * (0.7 + hostHash(hostname, nic.Name)*0.6) * scale
		if netBase < 1024 {
			netBase = 1024
		}
		add("windows_net_bytes_received_total", nicLbls, netBase)
		add("windows_net_bytes_sent_total", nicLbls, netBase*(0.4+hostHash(hostname, nic.Name+"tx")*0.3))
		add("windows_net_packets_received_total", nicLbls, netBase/1500)
	}

	// ── Capture-confirmed integration extras (host-capture.md WINSRV section) ───────────
	// Every name + label key below is sourced VERBATIM from a homelab reference host
	// WINSRV capture (Windows Server 2025). Names the capture does NOT confirm are NOT
	// emitted (see profiles.go windowsIntegrationNames comment + the task cantfind list).

	// windows_service_state{name,state} — the REAL metric (windows_service_status is a phantom).
	// Emit a small representative set of services; the per-service `state` reflects the host's
	// service status (running for most, stopped for a few). Capture state vocab includes
	// running|stopped|start pending|stop pending|paused|… — we use the two common terminal states.
	wSvcs := []struct {
		name  string
		state string
	}{
		{"Dhcp", "running"},
		{"Dnscache", "running"},
		{"LanmanServer", "running"},
		{"W32Time", "running"},
		{"Spooler", "stopped"},
	}
	for _, svc := range wSvcs {
		set("windows_service_state", mergeLabels(base, map[string]string{
			"name":  svc.name,
			"state": svc.state,
		}), 1)
	}

	// windows_diskdrive_status{name,status} — capture name (NO underscore split). The real
	// status vocab is OK|Degraded|Error|… ; a healthy host reports OK.
	set("windows_diskdrive_status", mergeLabels(base, map[string]string{
		"name":   "PHYSICALDRIVE0",
		"status": "OK",
	}), 1)

	// windows_system_* (counters + gauge; system collector).
	add("windows_system_context_switches_total", base, tickSec*40000*(0.5+busyFrac)*scale)
	set("windows_system_processor_queue_length", base, float64(int(busyFrac*float64(top.NumCPU)*2)))

	// windows_pagefile_* (gauges; label `file` on the per-file series — capture shows
	// file="C:\\pagefile.sys"). limit is fixed; free is a fraction of limit.
	pageLimit := top.MemTotal * 0.5
	pageLbls := mergeLabels(base, map[string]string{"file": `C:\pagefile.sys`})
	set("windows_pagefile_limit_bytes", pageLbls, pageLimit)
	set("windows_pagefile_free_bytes", pageLbls, pageLimit*(0.7+hostHash(hostname, "pagefree")*0.25))

	// windows_time_* (gauges + the timezone info series; time collector).
	set("windows_time_computed_time_offset_seconds", base, 0.00001*(hostHash(hostname, "wtoffset")-0.5))
	set("windows_time_ntp_round_trip_delay_seconds", base, 0.002*(1+hostHash(hostname, "wtrtt")))
	set("windows_time_ntp_client_time_sources", base, 1)
	set("windows_time_timezone", mergeLabels(base, map[string]string{
		"timezone": "GMT Standard Time",
	}), 1)
}
