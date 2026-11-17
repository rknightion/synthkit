// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"time"
)

// window is one scheduled fault in the fake clock: active on [from, to).
type window struct {
	mode, scope string
	from, to    time.Time
	intensity   float64
}

// fakeClock is a pure function of time: faults on fixed windows, a load factor function and a
// volume multiplier. It counts Eval calls so tests can prove what the model reads.
type fakeClock struct {
	faults []window
	factor func(time.Time) float64
	vm     float64
	evals  int
	// live faults (mode + "\x00" + scope -> intensity) are read at call time whatever the
	// instant asked about, as the shape engine's Live hook is.
	live map[string]float64
}

func newClock() *fakeClock { return &fakeClock{vm: 1} }

func (c *fakeClock) on(mode, scope string, from, to time.Time, i float64) *fakeClock {
	c.faults = append(c.faults, window{mode, scope, from, to, i})
	return c
}

func (c *fakeClock) Eval(now time.Time, mode, scope string) (bool, float64) {
	active, inten := c.EvalScheduled(now, mode, scope)
	if v, ok := c.live[mode+"\x00"+scope]; ok {
		active, inten = true, max(inten, v)
	}
	return active, inten
}

// EvalScheduled reads the fixed windows alone, as shape.Engine.EvalScheduled does.
func (c *fakeClock) EvalScheduled(now time.Time, mode, scope string) (bool, float64) {
	c.evals++
	active, inten := false, 0.0
	for _, w := range c.faults {
		if w.mode == mode && w.scope == scope && !now.Before(w.from) && now.Before(w.to) {
			active = true
			inten = max(inten, w.intensity)
		}
	}
	return active, inten
}

func (c *fakeClock) Factor(now time.Time, weight float64, nonProd bool) float64 {
	f := 1.0
	if c.factor != nil {
		f = c.factor(now)
	}
	return f * weight * c.vm
}

func (c *fakeClock) VolumeMultiplier() float64 { return c.vm }

// t0 is a grid-aligned instant (a multiple of Step since the epoch).
var t0 = time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

func at(steps int) time.Time { return t0.Add(time.Duration(steps) * Step) }

func constant(peak float64) *Demand {
	return &Demand{OffPeakRPS: peak, PeakRPS: peak, Load: LoadConstant}
}

func set(rs ...Resource) Set {
	return Set{Envs: []Declaration{{Env: "prod", Weight: 1, Resources: rs}}}
}

func mustCompile(t interface {
	Helper()
	Fatal(...any)
}, s Set) *Model {
	t.Helper()
	m, err := Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// fixedDraw returns the same value for every purpose.
func fixedDraw(v float64) Draw { return func(string) float64 { return v } }
