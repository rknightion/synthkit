// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"strconv"
	"time"

	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// cpuFixtureResources models the Agent system-collection families that the native
// artifact classifies as fixture-mechanics candidates. Datadog Agent's current
// system check emits CPU activity as percentages and per-core accumulated time
// (pkg/collector/corechecks/system/cpu/cpu/cpu.go). This isolated model starts an
// idle synthetic host at its first collection: all busy components are zero,
// idle is 100%, and each core's idle time equals synthetic host uptime.
func (c *Construct) cpuFixtureResources(now time.Time) []otlp.MetricResource {
	base := c.baseResourceAttrs()
	resources := make([]otlp.MetricResource, 0, 19)
	for _, name := range []string{
		"system.cpu.guest", "system.cpu.idle", "system.cpu.interrupt", "system.cpu.iowait",
		"system.cpu.stolen", "system.cpu.system", "system.cpu.user",
	} {
		value := 0.0
		if name == "system.cpu.idle" {
			value = 100
		}
		resources = append(resources, c.gaugeResource(name, base, nil, value, now))
	}
	resources = append(resources, c.gaugeResource("system.cpu.num_cores", base, nil, float64(c.fixtureCPUCount), now))
	uptime := now.Sub(c.start).Seconds()
	for _, name := range []string{
		"system.cpu.guest.total", "system.cpu.guestnice.total", "system.cpu.idle.total",
		"system.cpu.iowait.total", "system.cpu.irq.total", "system.cpu.nice.total",
		"system.cpu.softirq.total", "system.cpu.steal.total", "system.cpu.system.total", "system.cpu.user.total",
	} {
		for core := 0; core < c.fixtureCPUCount; core++ {
			value := 0.0
			if name == "system.cpu.idle.total" {
				value = uptime
			}
			resources = append(resources, c.gaugeResource(name, base, map[string]any{"core": strconv.Itoa(core)}, value, now))
		}
	}
	// The Agent reports context switches as a monotonic count. A no-work host
	// has zero switches; do not substitute unrelated collection activity.
	resources = append(resources, c.sumResource("system.cpu.context_switches", base, nil, 0, now))
	return resources
}

func (c *Construct) baseResourceAttrs() map[string]any {
	return map[string]any{"host.name": c.hostName, "source": c.source}
}

func (c *Construct) gaugeResource(name string, attrs, pointAttrs map[string]any, value float64, now time.Time) otlp.MetricResource {
	return otlp.MetricResource{
		Attrs: attrs, Scope: otlp.Scope{Name: translatorScope, Version: translatorVersion},
		Metrics: []otlp.Metric{{Name: name, Unit: "", Kind: otlp.MetricGauge, Numbers: []otlp.NumberPoint{{Attrs: pointAttrs, Time: now, Value: value}}}},
	}
}

func (c *Construct) sumResource(name string, attrs, pointAttrs map[string]any, value float64, now time.Time) otlp.MetricResource {
	return otlp.MetricResource{
		Attrs: attrs, Scope: otlp.Scope{Name: translatorScope, Version: translatorVersion},
		Metrics: []otlp.Metric{{
			Name: name, Unit: "", Kind: otlp.MetricSum, Monotonic: false, Temporality: otlp.TemporalityCumulative,
			Numbers: []otlp.NumberPoint{{Attrs: pointAttrs, Start: c.start, Time: now, Value: value}},
		}},
	}
}
