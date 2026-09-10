// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

const (
	// 2026-09-08 same-producer Kubernetes-reference medians, expressed as fractions
	// so the selected node's declared memory capacity remains authoritative. The Agent
	// source reports free and available separately; usable is not derived from free.
	observedFreeMemoryFraction   = 122.578125 / 7762.10546875
	observedUsableMemoryFraction = 4731.806640625 / 7762.10546875

	// The same reference's normalized 1/5/15-minute load medians. load.go establishes
	// raw load = normalized load * CPU count, so these survive the fixture's node size.
	observedNormalizedLoad1  = 1.1475
	observedNormalizedLoad5  = 0.66875
	observedNormalizedLoad15 = 0.28
)

// memoryLoadFixtureResources models the remaining fixture-mechanics candidates.
// Capacity comes from the selected Kubernetes node type. Agent current main
// memory_nix.go establishes the used/free and usable/total identities; load.go
// establishes normalized load. The reference baselines above are bounded with the
// deterministic shape engine rather than treated as deployment-time constants.
func (c *Construct) memoryLoadFixtureResources(now time.Time, w *core.World) []otlp.MetricResource {
	base := c.baseResourceAttrs()
	resources := make([]otlp.MetricResource, 0, 12)
	// A 3% Spread and 1% Wander band stays inside the captured steady-state load
	// range while retaining distinct, deterministic 1/5/15-minute observations.
	for _, window := range []struct {
		name string
		base float64
	}{
		{"1", observedNormalizedLoad1},
		{"5", observedNormalizedLoad5},
		{"15", observedNormalizedLoad15},
	} {
		normalized := window.base * c.fixtureSeriesVar(w, now, "system.load."+window.name, 0.03, 0.01)
		raw := normalized * float64(c.fixtureCPUCount)
		resources = append(resources,
			c.gaugeResource("system.load."+window.name, base, nil, raw, now),
			c.gaugeResource("system.load.norm."+window.name, base, nil, normalized, now),
		)
	}
	memoryMiB := c.fixtureMemoryBytes / (1024 * 1024)
	// 10% Spread and 4% Wander preserve the observed free-memory band; 1%/0.5% does
	// the same for usable memory. Clamp maintains the Agent's physical bounds.
	free := memoryMiB * observedFreeMemoryFraction * c.fixtureSeriesVar(w, now, "system.mem.free", 0.10, 0.04)
	usable := memoryMiB * observedUsableMemoryFraction * c.fixtureSeriesVar(w, now, "system.mem.usable", 0.01, 0.005)
	if free < 0 {
		free = 0
	}
	if free > memoryMiB {
		free = memoryMiB
	}
	if usable < free {
		usable = free
	}
	if usable > memoryMiB {
		usable = memoryMiB
	}
	resources = append(resources,
		c.gaugeResource("system.mem.total", base, nil, memoryMiB, now),
		c.gaugeResource("system.mem.free", base, nil, free, now),
		c.gaugeResource("system.mem.used", base, nil, memoryMiB-free, now),
		c.gaugeResource("system.mem.usable", base, nil, usable, now),
		c.gaugeResource("system.mem.pct_usable", base, nil, usable/memoryMiB, now),
	)
	// uptime.go reports host seconds. Construct start is its declaration-backed start
	// boundary, so this remains cumulative elapsed host time rather than a fake rate.
	resources = append(resources, c.gaugeResource("system.uptime", base, nil, now.Sub(c.start).Seconds(), now))
	return resources
}
