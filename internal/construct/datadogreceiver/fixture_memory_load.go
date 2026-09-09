// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"time"

	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// memoryLoadFixtureResources models the remaining fixture-mechanics candidates.
// Memory capacity is drawn from the selected Kubernetes node type. Datadog's
// system check reports total, free, used (= total - free), usable, and usable
// fraction in MiB. The synthetic host begins idle with no swap or workload, so
// free and usable equal total and used is zero. Values that require kernel or
// filesystem state beyond the fixture remain unimplemented.
func (c *Construct) memoryLoadFixtureResources(now time.Time) []otlp.MetricResource {
	base := c.baseResourceAttrs()
	resources := make([]otlp.MetricResource, 0, 12)
	for _, name := range []string{
		"system.load.1", "system.load.15", "system.load.5", "system.load.norm.1", "system.load.norm.15", "system.load.norm.5",
	} {
		resources = append(resources, c.gaugeResource(name, base, nil, 0, now))
	}
	memoryMiB := c.fixtureMemoryBytes / (1024 * 1024)
	resources = append(resources,
		c.gaugeResource("system.mem.total", base, nil, memoryMiB, now),
		c.gaugeResource("system.mem.free", base, nil, memoryMiB, now),
		c.gaugeResource("system.mem.used", base, nil, 0, now),
		c.gaugeResource("system.mem.usable", base, nil, memoryMiB, now),
		c.gaugeResource("system.mem.pct_usable", base, nil, 1, now),
	)
	resources = append(resources, c.gaugeResource("system.uptime", base, nil, now.Sub(c.start).Seconds(), now))
	return resources
}
