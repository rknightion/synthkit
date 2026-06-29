// SPDX-License-Identifier: AGPL-3.0-only

// cadvisor.go — EmitContainer + EmitMachine: cadvisor metric emission for one container.
//
// Ports the physics of internal/construct/k8scluster/cadvisor.go (emitCAdvisor) and
// extends the union vocabulary with docker-only series (container_fs_usage_bytes,
// container_last_seen, container_spec_memory_reservation_limit_bytes,
// container_network_receive_errors_total, container_network_transmit_errors_total).
//
// Counter-vs-gauge classification is PRESERVED EXACTLY from the source:
//
//	Counters (st.Add): container_cpu_usage_seconds_total, container_cpu_cfs_periods_total,
//	  container_cpu_cfs_throttled_periods_total, container_fs_reads_bytes_total,
//	  container_fs_writes_bytes_total, container_fs_reads_total, container_fs_writes_total,
//	  container_network_receive_bytes_total, container_network_transmit_bytes_total,
//	  container_network_receive_packets_total, container_network_transmit_packets_total,
//	  container_network_receive_packets_dropped_total,
//	  container_network_transmit_packets_dropped_total,
//	  container_network_receive_errors_total, container_network_transmit_errors_total (docker-only).
//	Gauges  (st.Set): container_memory_working_set_bytes, container_memory_cache,
//	  container_memory_rss, container_memory_swap, container_memory_usage_bytes,
//	  container_fs_usage_bytes (docker-only), container_last_seen (docker-only),
//	  container_spec_memory_reservation_limit_bytes (docker-only),
//	  machine_memory_bytes, machine_scrape_error (docker-only), up (docker-only).
//
// Label scopes:
//   - Container-scoped series (cpu/mem/fs):  base merged with c.Labels (+ vocab labels cpu="total", device).
//   - Network series:                         base merged with c.NetLabels.
//   - NEVER mutate base, c.Labels, or c.NetLabels.
package nodeexp

import (
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
)

// EmitContainer renders one container's cadvisor series filtered by prof.
// Ports internal/construct/k8scluster/cadvisor.go physics. base = identity (k8s or docker).
//
// Container-scoped series (cpu/mem/fs) use: clone(base) merged with c.Labels,
// plus any vocab labels (cpu="total", device=<dev>). Network series use:
// clone(base) merged with c.NetLabels. Neither base nor c.Labels nor c.NetLabels
// is ever mutated.
func EmitContainer(st *state.State, base map[string]string, c Container, prof CadvisorProfile, factor, tickSec, scale float64, sh *shape.Engine) {
	set, add := keepWriter(st, keepSetCadvisor(prof))

	// ── Container-scoped base (cpu / mem / fs) ──────────────────────────────
	// Clone base then overlay c.Labels. The cpu="total" and device vocab labels
	// are added per-metric below so they never pollute the shared map.
	cbase := mergeBase(base, c.Labels)

	// ── CPU (cumulative counter, cpu="total") ────────────────────────────────
	// cpuDelta is a plausible per-tick CPU-seconds delta: a deterministic fraction
	// of the container's CPURequest, modulated by the diurnal factor and noise.
	cpuRequest := c.CPURequest
	if cpuRequest <= 0 {
		cpuRequest = 0.25 // sensible default: 0.25 cores
	}
	cpuDelta := (0.005 + factor*cpuRequest*0.4*sh.Noise(0.3)) * tickSec
	if cpuDelta < 0.005 {
		cpuDelta = 0.005
	}
	cpuLbls := mergeBase(cbase, map[string]string{"cpu": "total"})
	add("container_cpu_usage_seconds_total", cpuLbls, cpuDelta)

	// CFS periods + throttling (counter)
	periods := 10.0 * tickSec * (0.5 + factor)
	add("container_cpu_cfs_periods_total", cbase, periods)
	add("container_cpu_cfs_throttled_periods_total", cbase, periods*0.02)

	// ── Memory (gauges) ──────────────────────────────────────────────────────
	memLimit := c.MemLimit
	if memLimit <= 0 {
		memLimit = 128 * 1024 * 1024 // sensible default: 128 MiB
	}
	memBytes := memLimit * sh.Noise(0.15) * (0.5 + factor*0.5)
	if memBytes < 32*1024*1024 {
		memBytes = 32 * 1024 * 1024
	}
	cache := memBytes * 0.25
	set("container_memory_working_set_bytes", cbase, memBytes)
	set("container_memory_cache", cbase, cache)
	set("container_memory_rss", cbase, memBytes*0.8)
	set("container_memory_swap", cbase, 0)
	set("container_memory_usage_bytes", cbase, memBytes+cache)

	// docker-only gauges
	// container_fs_usage_bytes: approximate disk usage, similar to working-set magnitude.
	set("container_fs_usage_bytes", cbase, memBytes*1.2)
	// container_last_seen: the current tick's unix seconds; use a fixed placeholder
	// (real scrape epoch is not available in the lib — 1748000000 is a stable stand-in).
	set("container_last_seen", cbase, 1_748_000_000)
	// container_spec_memory_reservation_limit_bytes: memory reservation = MemLimit.
	set("container_spec_memory_reservation_limit_bytes", cbase, memLimit)

	// ── Filesystem I/O (counters, + device label) ────────────────────────────
	fsLbls := mergeBase(cbase, map[string]string{"device": "/dev/nvme0n1p1"})
	fsRd := (256*1024 + factor*4*1024*1024) * sh.Noise(0.4) * scale
	fsWr := fsRd * (0.3 + sh.Float64()*0.3)
	if fsRd < 0 {
		fsRd = 1024
	}
	if fsWr < 0 {
		fsWr = 512
	}
	add("container_fs_reads_bytes_total", fsLbls, fsRd)
	add("container_fs_writes_bytes_total", fsLbls, fsWr)
	add("container_fs_reads_total", fsLbls, (5+factor*40)*scale)
	add("container_fs_writes_total", fsLbls, (2+factor*20)*scale)

	// ── Network (pod/container-scoped via c.NetLabels) ───────────────────────
	// Network series use c.NetLabels NOT c.Labels: k8s drops `container` and gains
	// `interface`; docker keeps `container` (= Labels). The caller controls this via
	// distinct Labels/NetLabels maps on the Container struct.
	netBase := mergeBase(base, c.NetLabels)
	rx := (1*1024*1024 + factor*50*1024*1024) * sh.Noise(0.4) * scale
	tx := rx * (0.3 + sh.Float64()*0.4)
	if rx < 0 {
		rx = 1024
	}
	if tx < 0 {
		tx = 512
	}
	add("container_network_receive_bytes_total", netBase, rx)
	add("container_network_transmit_bytes_total", netBase, tx)
	add("container_network_receive_packets_total", netBase, rx/1400)
	add("container_network_transmit_packets_total", netBase, tx/1400)
	add("container_network_receive_packets_dropped_total", netBase, rx/1400*0.0001)
	add("container_network_transmit_packets_dropped_total", netBase, tx/1400*0.0001)
	// docker-only error counters (small values — real networks rarely drop, almost never error)
	add("container_network_receive_errors_total", netBase, rx/1400*0.00001)
	add("container_network_transmit_errors_total", netBase, tx/1400*0.00001)
}

// EmitMachine renders the per-host machine_memory_bytes plus, for CadvisorDocker, the
// machine_scrape_error and up{job=integrations/docker} series. The caller includes any
// node label in base (k8s machine_memory_bytes carries node=<hostname>; docker carries none).
func EmitMachine(st *state.State, base map[string]string, memTotal float64, prof CadvisorProfile) {
	ks := keepSetCadvisor(prof)
	set, _ := keepWriter(st, ks)

	set("machine_memory_bytes", cloneBase(base), memTotal)

	if prof == CadvisorDocker {
		set("machine_scrape_error", cloneBase(base), 0)
		set("up", cloneBase(base), 1)
	}
}
