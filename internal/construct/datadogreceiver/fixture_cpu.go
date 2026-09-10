// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"strconv"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

const (
	// These medians come from the same-producer Kubernetes receiver capture on
	// 2026-09-08. They are a baseline, not an inferred Agent default.
	observedCPUUserPercent      = 8.19010893
	observedCPUSystemPercent    = 6.08298745
	observedCPUInterruptPercent = 0.237701842
	observedCPUIOWaitPercent    = 0.485802524
	observedCPUStolenPercent    = 0

	// The same capture's post-reset system-wide Ctxt rate was 20.4k-25.9k/s.
	// Round its 22.2k/s median to 22,000/s to avoid false precision.
	observedContextSwitchesPerSecond = 22_000
)

// fixtureCPUTotals mirrors the raw per-core gopsutil fields reported by the Agent.
// Its fields form a disjoint time partition, unlike the Agent's derived percentage
// gauges where system and interrupt intentionally overlap.
type fixtureCPUTotals struct {
	guest, guestNice, idle, iowait, irq, nice, softirq, steal, system, user float64
}

type fixtureCPUPercentages struct {
	guest, idle, interrupt, iowait, stolen, system, user float64
}

// cpuFixtureResources models the Agent system-collection families that the native
// artifact classifies as fixture-mechanics candidates. Datadog Agent current main
// `pkg/collector/corechecks/system/cpu/cpu/cpu.go` derives percentages from elapsed
// gopsutil CPU time and reports raw per-core accumulations. The nonzero baseline is
// the retained reference observation above; Spread and Wander keep it bounded and
// deterministic without adding a new random source.
func (c *Construct) cpuFixtureResources(now time.Time, w *core.World) []otlp.MetricResource {
	c.advanceCPUFixture(now, w)
	base := c.baseResourceAttrs()
	resources := make([]otlp.MetricResource, 0, 19)
	percentages := c.aggregateCPUPercentages(now, w)
	for _, metric := range []struct {
		name  string
		value float64
	}{
		{"system.cpu.guest", percentages.guest},
		{"system.cpu.idle", percentages.idle},
		{"system.cpu.interrupt", percentages.interrupt},
		{"system.cpu.iowait", percentages.iowait},
		{"system.cpu.stolen", percentages.stolen},
		{"system.cpu.system", percentages.system},
		{"system.cpu.user", percentages.user},
	} {
		resources = append(resources, c.gaugeResource(metric.name, base, nil, metric.value, now))
	}
	// Agent reportCPUInfo derives this gauge from the selected node's CPU info.
	resources = append(resources, c.gaugeResource("system.cpu.num_cores", base, nil, float64(c.fixtureCPUCount), now))
	for core, totals := range c.fixtureCPUTotals {
		attrs := map[string]any{"core": strconv.Itoa(core)}
		for _, metric := range []struct {
			name  string
			value float64
		}{
			{"system.cpu.guest.total", totals.guest},
			{"system.cpu.guestnice.total", totals.guestNice},
			{"system.cpu.idle.total", totals.idle},
			{"system.cpu.iowait.total", totals.iowait},
			{"system.cpu.irq.total", totals.irq},
			{"system.cpu.nice.total", totals.nice},
			{"system.cpu.softirq.total", totals.softirq},
			{"system.cpu.steal.total", totals.steal},
			{"system.cpu.system.total", totals.system},
			{"system.cpu.user.total", totals.user},
		} {
			resources = append(resources, c.gaugeResource(metric.name, base, attrs, metric.value, now))
		}
	}
	// The source Agent sends a MonotonicCount, while the immutable receiver capture
	// observes a non-monotonic cumulative Sum. Keep that observed envelope unchanged.
	resources = append(resources, c.sumResource("system.cpu.context_switches", base, nil, c.fixtureContextSwitches, now))
	return resources
}

func (c *Construct) advanceCPUFixture(now time.Time, w *core.World) {
	if c.fixtureLast.IsZero() {
		c.fixtureLast = now
		c.fixtureCPUTotals = make([]fixtureCPUTotals, c.fixtureCPUCount)
		return
	}
	if !now.After(c.fixtureLast) {
		return
	}
	elapsed := now.Sub(c.fixtureLast).Seconds()
	for core := range c.fixtureCPUTotals {
		p := c.cpuPercentages(now, w, core)
		t := &c.fixtureCPUTotals[core]
		// cpu.go reports user as User+Nice and system as System+Irq+Softirq.
		// The observed reference's guest, guestnice, nice, hard-IRQ and steal
		// fields are zero, so retaining them at zero is source-backed rather than
		// inventing activity the declared worker does not establish.
		t.user += elapsed * p.user / 100
		t.system += elapsed * (p.system - p.interrupt) / 100
		t.softirq += elapsed * p.interrupt / 100
		t.iowait += elapsed * p.iowait / 100
		t.idle += elapsed * p.idle / 100
	}
	// Linux context_switches_linux.go reads the OS cumulative Ctxt counter. The
	// observed 22k/s reference rate is varied only within its captured band.
	c.fixtureContextSwitches += elapsed * observedContextSwitchesPerSecond * c.fixtureSeriesVar(w, now, "system.cpu.context_switches", 0.03, 0.01)
	c.fixtureLast = now
}

func (c *Construct) aggregateCPUPercentages(now time.Time, w *core.World) fixtureCPUPercentages {
	if c.fixtureCPUCount == 0 {
		return fixtureCPUPercentages{idle: 100}
	}
	var sum fixtureCPUPercentages
	for core := 0; core < c.fixtureCPUCount; core++ {
		p := c.cpuPercentages(now, w, core)
		sum.guest += p.guest
		sum.idle += p.idle
		sum.interrupt += p.interrupt
		sum.iowait += p.iowait
		sum.stolen += p.stolen
		sum.system += p.system
		sum.user += p.user
	}
	divisor := float64(c.fixtureCPUCount)
	sum.guest /= divisor
	sum.idle /= divisor
	sum.interrupt /= divisor
	sum.iowait /= divisor
	sum.stolen /= divisor
	sum.system /= divisor
	sum.user /= divisor
	return sum
}

func (c *Construct) cpuPercentages(now time.Time, w *core.World, core int) fixtureCPUPercentages {
	// The 10% Spread and 4% Wander bounds keep this small-worker baseline inside
	// the observed reference's steady-state CPU range while making peer cores distinct.
	scale := c.fixtureSeriesVar(w, now, "system.cpu.percent."+strconv.Itoa(core), 0.10, 0.04)
	p := fixtureCPUPercentages{
		interrupt: observedCPUInterruptPercent * scale,
		iowait:    observedCPUIOWaitPercent * scale,
		stolen:    observedCPUStolenPercent * scale,
		system:    observedCPUSystemPercent * scale,
		user:      observedCPUUserPercent * scale,
	}
	// The Agent denominator includes user, system, idle, iowait and steal once.
	// Interrupt is already included in system, and guest is excluded from it.
	p.idle = 100 - p.user - p.system - p.iowait - p.stolen
	return p
}

func (c *Construct) fixtureSeriesVar(w *core.World, now time.Time, key string, spreadAmp, wanderAmp float64) float64 {
	if w == nil || w.Shape == nil {
		return 1
	}
	// Runner shares one shape engine across a blueprint. Scope the shape key to this
	// declared receiver identity so separate receiver instances do not get identical
	// offsets or phases for the same emitted family.
	key = "datadog_receiver." + c.hostName + "." + key
	return w.Shape.Spread(key, spreadAmp) * w.Shape.Wander(key, now, wanderAmp)
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
