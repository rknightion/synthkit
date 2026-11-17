// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// memoryKernelFixtureResources emits the eight Linux-only memory families from
// Datadog Agent memory_nix.go linuxSpecificVirtualMemoryCheck. That check reports
// the gopsutil VirtualMemoryStat fields Cached, Buffers, Shared, Slab, Sreclaimable,
// PageTables, CommitLimit and CommittedAS, each divided by 1 MiB.
//
// Physics basis: internal/nodeexp/linux.go's meminfo partitions. There,
// used = MemTotal - MemAvailable and:
//
//	Buffers = 256 MiB, Cached = MemAvailable - MemFree, Shmem = used * 0.05,
//	Slab = used * 0.06, SReclaimable = used * 0.04, PageTables = used * 0.01,
//	CommitLimit = MemTotal * 0.5, Committed_AS = used * 1.2.
//
// CommitLimit = MemTotal * 0.5 is also the kernel's overcommit_ratio=50 rule with
// no swap, which matches this construct's disabled-swap fixture. gopsutil's Linux
// reader adds SReclaimable to Cached (mem_linux.go: ret.Cached += ret.Sreclaimable),
// so Datadog's cached is (MemAvailable - MemFree) + SReclaimable.
//
// MemTotal, MemFree and MemAvailable come from the unchanged core-memory helper's
// own output (system.mem.total, .free and .usable on this tick). The helper is a
// pure function of time and shape keys, so these identities hold exactly against
// the emitted core gauges. nodeexp's Buffers noise becomes the scoped
// fixtureSeriesVar with the same 10% amplitude.
func (c *Construct) memoryKernelFixtureResources(now time.Time, w *core.World) []otlp.MetricResource {
	mem := map[string]float64{}
	for _, resource := range c.memoryLoadFixtureResources(now, w) {
		for _, metric := range resource.Metrics {
			if len(metric.Numbers) == 1 {
				mem[metric.Name] = metric.Numbers[0].Value
			}
		}
	}
	totalMiB := mem["system.mem.total"]
	freeMiB := mem["system.mem.free"]
	usableMiB := mem["system.mem.usable"]
	nonAvailable := totalMiB - usableMiB
	if nonAvailable < 0 {
		nonAvailable = 0
	}
	slabReclaimable := nonAvailable * 0.04
	buffered := 256 * c.fixtureSeriesVar(w, now, "system.mem.buffered", 0.10, 0)
	base := c.baseResourceAttrs()
	resources := make([]otlp.MetricResource, 0, 8)
	for _, metric := range []struct {
		name  string
		value float64
	}{
		{"system.mem.buffered", buffered},
		{"system.mem.cached", (usableMiB - freeMiB) + slabReclaimable},
		{"system.mem.commit_limit", totalMiB * 0.5},
		{"system.mem.committed_as", nonAvailable * 1.2},
		{"system.mem.page_tables", nonAvailable * 0.01},
		{"system.mem.shared", nonAvailable * 0.05},
		{"system.mem.slab", nonAvailable * 0.06},
		{"system.mem.slab_reclaimable", slabReclaimable},
	} {
		resources = append(resources, c.gaugeResource(metric.name, base, nil, metric.value, now))
	}
	return resources
}
