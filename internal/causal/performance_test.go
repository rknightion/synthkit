// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"runtime"
	"testing"
	"time"
)

// This is a whole public evaluation, not a gate microbenchmark. Rates deliberately
// exercise pool waiting and the retry-demand fixed point; they are proof inputs,
// not a shipped deployment. A small caller share still exposes solver amplification.
func legacyPerformanceSet(ordered, noTimeout bool) Set {
	pol := CallPolicy{TimeoutMS: 1000, Retries: 2, RetryOn: []string{RetryTimeout}}
	if noTimeout {
		pol = CallPolicy{}
	}
	f := &Flow{Legacy: &LegacyTiming{P95MS: 8000}, Nodes: []FlowNode{
		{Parent: -1},
		{Parent: 0, Uses: []string{"p"}, Duration: "p", Policy: pol,
			Stretch: FaultScale{Mode: ModeDependencySlow, Scope: "work", AtFull: 4}},
		{Parent: 1}, {Parent: 1},
	}}
	s := set(Resource{Name: "p", Kind: KindPool, Size: 1, HoldMS: 50, AcquireTimeoutMS: 1000, Demand: constant(16)})
	s.Loads = []Load{{Resource: "p", Node: 1, Flow: f, Policy: pol, Share: .01}}
	if ordered {
		f.Nodes[1].Uses = append(f.Nodes[1].Uses, "q", "rule")
		s.Envs[0].Resources = append(s.Envs[0].Resources,
			Resource{Name: "q", Kind: KindPool, Size: 1, HoldMS: 50, AcquireTimeoutMS: 1000, Demand: constant(17)},
			Resource{Name: "rule", Kind: KindRule, RejectShare: .3})
		s.Loads = append(s.Loads, Load{Resource: "q", Node: 1, Flow: f, Policy: pol, Share: .01})
	}
	return s
}

type performanceClock struct {
	*fakeClock
	intensity float64
	start     time.Time
}

func (c performanceClock) Eval(t time.Time, mode, scope string) (bool, float64) {
	if t.Before(c.start) {
		return false, 0
	}
	if mode == ModeDependencySlow && scope == "work" {
		return c.intensity > 0, c.intensity
	}
	if mode == ModePoolExhausted && scope == "p" {
		return c.intensity > 0, c.intensity * .05
	}
	if mode == ModeRuleRegression && scope == "rule" {
		return c.intensity > 0, c.intensity
	}
	return false, 0
}
func (c performanceClock) EvalScheduled(t time.Time, mode, scope string) (bool, float64) {
	// A finite scheduled transition in the full horizon, in addition to final live faults.
	if t.Before(c.start.Add(-time.Hour)) && !t.Before(c.start.Add(-2*time.Hour)) {
		c.intensity = .15
		c.start = t
		return c.Eval(t, mode, scope)
	}
	return false, 0
}

// The 4 s limit is on every complete dynamic Advance+State+Window, not a
// per-gate or amortized bound. Cold and catch-up have separate measured budgets.
// Run in fresh bounded processes with GOMAXPROCS=2 and /usr/bin/time -v for RSS.
func BenchmarkModelLegacySurface(b *testing.B) {
	for _, shape := range []string{"siblings", "ordered", "no-timeout"} {
		for _, scenario := range []string{"cold", "catchup60", "catchup20m", "reset", "dynamic"} {
			b.Run(shape+"/"+scenario, func(b *testing.B) {
				for sample := 0; sample < b.N; sample++ {
					b.StopTimer()
					m, err := Compile(legacyPerformanceSet(shape == "ordered", shape == "no-timeout"))
					if err != nil {
						b.Fatal(err)
					}
					clk := performanceClock{fakeClock: newClock(), start: t0, intensity: .1}
					now := t0
					if scenario != "cold" {
						m.Advance(now, clk)
					}
					// Catch-up/reset must observe a new fault vector, not merely replay
					// the exact answers retained by the cold prime (rates unchanged).
					if scenario != "cold" && scenario != "dynamic" {
						clk.intensity = .11
					}
					switch scenario {
					case "catchup60":
						now = now.Add(time.Minute)
					case "catchup20m":
						now = now.Add(20 * time.Minute)
					case "reset":
						now = now.Add(Horizon + Step)
					}
					steps := 1
					if scenario == "dynamic" {
						steps = 12
					}
					for step := 0; step < steps; step++ {
						if scenario == "dynamic" {
							now = now.Add(Step)
							clk.intensity = .11 + float64(float64(step)*.0101)
							if step >= 6 {
								clk.intensity = .8 - float64(float64(step)*.0201)
							}
						}
						var before, after runtime.MemStats
						runtime.ReadMemStats(&before)
						b.StartTimer()
						start := time.Now()
						m.Advance(now, clk)
						st, ok := m.State("p", now)
						w, wok := m.Window("p", now.Add(-time.Minute), now)
						elapsed := time.Since(start)
						b.StopTimer()
						runtime.ReadMemStats(&after)
						if !ok || !wok || st.Demand < 16 || w.Arrivals <= 0 {
							b.Fatalf("surface not observed: %+v %+v", st, w)
						}
						b.Logf("sample=%d shape=%s scenario=%s step=%d elapsed=%s alloc_bytes=%d allocs=%d heap_before=%d heap_after=%d demand=%.15g wait=%.15g reject=%.15g", sample, shape, scenario, step, elapsed, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, before.HeapAlloc, after.HeapAlloc, st.Demand, st.WaitMS, st.RejectShare)
						if scenario == "dynamic" && elapsed > 4*time.Second {
							b.Fatalf("complete dynamic step %s exceeds 4s margin", elapsed)
						}
					}
				}
			})
		}
	}
}
