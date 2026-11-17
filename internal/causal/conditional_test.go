// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"math"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type conditionalFacts [][][][]elapsed

func sameConditionalFacts(a, b conditionalFacts) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for n := range a[i] {
			if len(a[i][n]) != len(b[i][n]) {
				return false
			}
			for f := range a[i][n] {
				if (a[i][n][f] == nil) != (b[i][n][f] == nil) || !sameTimingAtoms(a[i][n][f], b[i][n][f]) {
					return false
				}
			}
		}
	}
	return true
}
func samePublicBits(a, b any) bool {
	x, y := reflect.ValueOf(a), reflect.ValueOf(b)
	if x.Type() != y.Type() {
		return false
	}
	for i := 0; i < x.NumField(); i++ {
		u, v := x.Field(i), y.Field(i)
		if u.Kind() == reflect.Float64 {
			if math.Float64bits(u.Float()) != math.Float64bits(v.Float()) {
				return false
			}
		} else if !reflect.DeepEqual(u.Interface(), v.Interface()) {
			return false
		}
	}
	return true
}

func captureConditional(facts conditionalFacts) func(int, *legacyEval) {
	return func(slot int, e *legacyEval) {
		facts[slot] = make([][][]elapsed, len(e.flow.nodes))
		for n := range e.flow.nodes {
			facts[slot][n] = make([][]elapsed, 2)
			for fresh := range 2 {
				facts[slot][n][fresh] = slices.Clone(e.serve(n, fresh != 0, true))
			}
		}
		if e.scratch != nil && e.scratch.retained > maxTimingScratchAtoms/2 {
			panic("worker scratch quota exceeded")
		}
	}
}
func serialConditionalFacts(g *gate, rej rejectFn, points, bins int) (float64, conditionalFacts) {
	facts := make(conditionalFacts, 4*points)
	observe := captureConditional(facts)
	primes := timingPrimes(len(g.flow.nodes))
	value := 0.0
	for slot := 0; slot < 4*points; slot++ {
		e := &legacyEval{flow: g.flow, rej: rej, bins: bins, base: g.flow.legacyBase(slot/4+1, primes, slot%4, points), cache: make([][4][]elapsed, len(g.flow.nodes))}
		memo := make([][2]float64, len(g.path))
		for i := range memo {
			memo[i] = [2]float64{math.NaN(), math.NaN()}
		}
		value += g.conditionalArrivals(e, 0, false, memo)
		observe(slot, e)
	}
	return value / float64(4*points), facts
}

func TestConditionalOrderedFoldAndAtoms(t *testing.T) {
	old := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(old)
	m := mustCompile(t, legacyPerformanceSet(true, false))
	cf := m.flows[0]
	cf.prepare(newClock(), t0)
	p, q := m.byName["p"], m.byName["q"]
	p.last = rec{valid: true, st: State{WaitMS: 100}}
	q.last = rec{valid: true, st: State{WaitMS: 200}}
	rejection := .03
	rej := func(c *compiled) float64 {
		if c == p {
			return rejection
		}
		return .02
	}
	g := p.gates[0]
	for change := 0; change < 4; change++ {
		if change == 1 {
			p.last.st.WaitMS = 127.3
			cf.nodes[1].storm = 1.17
		}
		if change == 2 {
			cf.nodes[2].storm = 1.3
			rejection = .07
			cf.legacyRootScale = 1.2
		}
		if change == 3 {
			q.last.st.WaitMS = 210
			cf.nodes[2].hop = .1
		}
		want, atoms := serialConditionalFacts(g, rej, 32, 8)
		for _, serial := range []bool{true, false, false} {
			actual := make(conditionalFacts, 128)
			release := make(chan struct{})
			var inverted atomic.Bool
			mode := legacyRunMode{serial: serial, observe: captureConditional(actual), before: func(worker int, parallel bool) {
				if parallel && worker == 0 {
					<-release
				}
			}, after: func(worker int, parallel bool) {
				if parallel && worker == 1 {
					inverted.Store(true)
					close(release)
				}
			}}
			got := g.evaluateConditional(rej, 32, 8, mode)
			if math.Float64bits(got) != math.Float64bits(want) || !sameConditionalFacts(actual, atoms) {
				t.Fatalf("change=%d serial=%v conditional result/atoms differ: %.17g want %.17g", change, serial, got, want)
			}
			if !serial && !inverted.Load() {
				t.Fatal("forced completion inversion was not exercised")
			}
			if cf.planAtoms > maxLegacyPlanAtoms || cf.planSlots > maxLegacyPlanSlots {
				t.Fatal("child plan aggregate quota")
			}
			for _, memo := range cf.timingMemos {
				if memo.atoms > maxTimingMemoAtoms/2 || memo.emissionAtoms > maxTimingMemoAtoms/2 || len(memo.cdf) > timingCDFSlots/2 || len(memo.compact) > timingCompactSlots/2 || len(memo.emission) > timingCompactSlots/2 {
					t.Fatal("memo aggregate quota")
				}
			}
		}
		fallbackAtoms := make(conditionalFacts, 128)
		fallback := g.evaluateConditional(rej, 32, 8, legacyRunMode{resultLimit: 16, observe: captureConditional(fallbackAtoms)})
		if math.Float64bits(fallback) != math.Float64bits(want) || !sameConditionalFacts(fallbackAtoms, atoms) {
			t.Fatal("bounded-result rejection changed complete evaluation")
		}
	}
	var calls, hits uint64
	for _, memo := range cf.timingMemos {
		calls += memo.cdfCalls + memo.compactCalls + memo.emissionCalls
		hits += memo.cdfHits + memo.compactHits + memo.emissionHits
	}
	if calls == 0 || hits == 0 {
		t.Fatal("worker cache misses/hits were not exercised")
	}
	if len(conditionalPermits) != 0 {
		t.Fatal("executor leaked a permit")
	}
}

// Exercise each panic independently: an uncontained helper panic terminates the
// test process, while a caller panic must not leave published plans unaccounted.
func TestConditionalPanicLifecycle(t *testing.T) {
	old := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(old)
	for _, fault := range []struct {
		name   string
		worker int
	}{{"helper", 1}, {"caller", 0}} {
		t.Run(fault.name, func(t *testing.T) {
			m := mustCompile(t, legacyPerformanceSet(false, false))
			cf := m.flows[0]
			cf.prepare(newClock(), t0)
			p := m.byName["p"]
			p.last = rec{valid: true, st: State{WaitMS: 100}}
			g := p.gates[0]
			rej := func(*compiled) float64 { return .03 }
			const points, bins = 32, 8
			marker := &struct{ worker int }{fault.worker}
			var active, published atomic.Int32
			var started sync.WaitGroup
			started.Add(2)
			survivorPublished, faultUnwound := make(chan struct{}), make(chan struct{})
			cf.runMode = &legacyRunMode{
				before: func(worker int, parallel bool) {
					if !parallel {
						panic("panic proof did not acquire both permits")
					}
					active.Add(1)
					started.Done()
					started.Wait()
				},
				after: func(worker int, _ bool) {
					active.Add(-1)
					if worker == fault.worker {
						close(faultUnwound)
					}
				},
				observe: func(slot int, e *legacyEval) {
					worker := slot / (2 * points)
					if slot != worker*2*points {
						return
					}
					// Real evaluation has already published child plans in each
					// exclusively owned region before either side may unwind.
					if e.plans.atoms == 0 {
						panic("panic proof did not publish child plans")
					}
					published.Add(1)
					if worker == fault.worker {
						<-survivorPublished
						panic(marker)
					}
					close(survivorPublished)
					<-faultUnwound
				},
			}
			var recovered any
			var activeAtRecover int32
			var permitsAtRecover int
			returned := false
			func() {
				defer func() {
					recovered = recover()
					activeAtRecover = active.Load()
					permitsAtRecover = len(conditionalPermits)
				}()
				g.legacyArrivals(rej, points, bins)
				returned = true
			}()
			if recovered != marker || returned || len(g.legacyCache) != 0 {
				t.Fatalf("panic identity/publication: recovered=%v returned=%v cached=%d", recovered, returned, len(g.legacyCache))
			}
			if activeAtRecover != 0 || permitsAtRecover != 0 || published.Load() != 2 {
				t.Fatalf("recover boundary: active=%d permits=%d published=%d", activeAtRecover, permitsAtRecover, published.Load())
			}
			total := 0
			for n := range cf.nodes {
				cache := &cf.nodes[n].childrenCache
				actual := 0
				for _, pair := range cache.slots {
					for _, entry := range pair {
						if entry != nil {
							actual += cap(entry.paths) + cap(entry.failures)
						}
					}
				}
				if cache.atoms != actual {
					t.Errorf("post-panic node %d retained count=%d actual=%d", n, cache.atoms, actual)
				}
				total += actual
			}
			if total == 0 || cf.planAtoms != total {
				t.Errorf("post-panic flow retained count=%d actual=%d", cf.planAtoms, total)
			}
			// Both permits must support another actual parallel evaluation on
			// the same partially populated caches, without hiding an answer hit.
			var reused atomic.Int32
			cf.runMode = &legacyRunMode{before: func(worker int, parallel bool) {
				if !parallel {
					panic("permits not reusable after panic")
				}
				reused.Add(1 << worker)
			}}
			want, _ := serialConditionalFacts(g, rej, points, bins)
			got := g.legacyArrivals(rej, points, bins)
			if reused.Load() != 3 || len(conditionalPermits) != 0 || math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("post-panic reuse: workers=%d permits=%d got=%.17g want=%.17g", reused.Load(), len(conditionalPermits), got, want)
			}
			t.Logf("panic worker=%d recovered on caller; active=%d permits=%d published=%d retained_atoms=%d; reuse matches serial bits", fault.worker, activeAtRecover, permitsAtRecover, published.Load(), total)
		})
	}
}

func TestConditionalFrozenEpoch(t *testing.T) {
	m := mustCompile(t, legacyPerformanceSet(false, false))
	cf := m.flows[0]
	cf.prepare(newClock(), t0)
	p := m.byName["p"]
	p.last = rec{valid: true, st: State{WaitMS: 100}}
	rej := func(*compiled) float64 { return .03 }
	g := p.gates[0]
	want := referenceTimingArrivals(g, rej, 32, 8)
	var once sync.Once
	got := g.evaluateConditional(rej, 32, 8, legacyRunMode{before: func(int, bool) { once.Do(func() { p.last.st.WaitMS = 999; cf.nodes[1].storm = 4 }) }})
	if math.Float64bits(got) != math.Float64bits(want) {
		t.Fatal("workers observed a changed input epoch")
	}
}

func TestConditionalQuotaRejection(t *testing.T) {
	memo := &timingMemo{sharded: true}
	scratch := &timingScratch{memo: memo, limit: maxTimingScratchAtoms / 2}
	in := make([]elapsed, maxTimingMemoAtoms/2+1)
	for i := range in {
		in[i] = elapsed{p: 1, ms: float64(i % 2)}
	}
	_, hash, _ := memo.compactLookup(in, 8)
	memo.compactKeep(in, []elapsed{{p: 1}}, 8, hash)
	if memo.atoms != 0 {
		t.Fatal("oversized compaction retained")
	}
	program := make([]timingEmissionCell, maxTimingMemoAtoms/2+1)
	_, emissionHash, _ := memo.emissionLookup(nil, program, outcomeKey{}, 100, 8, workLognormal)
	memo.emissionKeep(nil, program, []elapsed{{p: 1}}, outcomeKey{}, 100, 8, workLognormal, emissionHash)
	if memo.emissionAtoms != 0 {
		t.Fatal("oversized emission retained")
	}
	cached := scratch.compact(in[:80], 8)
	want := referenceTimingCompact(in[:80], 8)
	if !sameTimingAtoms(cached, want) {
		t.Fatal("rejection changed subsequent output")
	}
	tiny := &timingScratch{limit: 1}
	i, b := tiny.take()
	b = append(b, in[:2]...)
	tiny.keep(i, b)
	if tiny.retained != 0 {
		t.Fatal("scratch rejection failed")
	}
	plans := []*timedChildrenCache{{slots: make([][2]*timedChildren, 8)}}
	shards := splitLegacyPlans(plans, 8)
	shards[0].limit = 0
	e := &legacyEval{plans: shards[0], point: 0}
	e.keepChildPlan(0, false, timedChildren{paths: in[:2]})
	if plans[0].slots[0][0] != nil || shards[0].atoms != 0 {
		t.Fatal("unpublished child exceeded its reserved quota")
	}
	// No second full cache/pointer table hides in the transfer views.
	if cap(shards[0].views[0].slots) != 4 || cap(shards[1].views[0].slots) != 4 {
		t.Fatal("overlapping owner capacity")
	}
}

func smallConditionalSet() Set {
	pol := CallPolicy{TimeoutMS: 750, Retries: 1, RetryOn: []string{RetryTimeout}}
	f := &Flow{Legacy: &LegacyTiming{P95MS: 1000}, Nodes: []FlowNode{{Parent: -1}, {Parent: 0, Uses: []string{"p", "q"}, Policy: pol, Stretch: FaultScale{Mode: ModeDependencySlow, Scope: "work", AtFull: 4}}}}
	s := set(Resource{Name: "p", Kind: KindPool, Size: 1, HoldMS: 10, AcquireTimeoutMS: 1000, Demand: constant(10)}, Resource{Name: "q", Kind: KindQuota, LimitRPS: 100, Demand: constant(10)})
	s.Loads = []Load{{Resource: "p", Node: 1, Flow: f, Policy: pol, Share: 1}, {Resource: "q", Node: 1, Flow: f, Policy: pol, Share: 1}}
	return s
}
func TestConditionalModelSerialReference(t *testing.T) {
	left, right := mustCompile(t, smallConditionalSet()), mustCompile(t, smallConditionalSet())
	clk := performanceClock{fakeClock: newClock(), start: t0, intensity: .1}
	left.Advance(t0, clk)
	right.Advance(t0, clk)
	right.flows[0].runMode = &legacyRunMode{reference: true}
	for step, intensity := range []float64{.2, .8, 0} {
		now := t0.Add(time.Duration(step+1) * Step)
		clk.intensity = intensity
		left.Advance(now, clk)
		right.Advance(now, clk)
		for _, name := range []string{"p", "q"} {
			a, _ := left.State(name, now)
			b, _ := right.State(name, now)
			aw, _ := left.Window(name, t0, now)
			bw, _ := right.Window(name, t0, now)
			if !samePublicBits(a, b) || !samePublicBits(aw, bw) {
				t.Fatal("complete public state/window bits differ from serial bypass")
			}
		}
	}
}

func TestConditionalConcurrentModelsAndPublicCalls(t *testing.T) {
	old := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(old)
	models := []*Model{mustCompile(t, smallConditionalSet()), mustCompile(t, smallConditionalSet())}
	var active, peak atomic.Int32
	hook := func(int, bool) {
		n := active.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
	}
	var all sync.WaitGroup
	for _, m := range models {
		m.flows[0].runMode = &legacyRunMode{before: hook, after: func(int, bool) { active.Add(-1) }}
		all.Add(2)
		go func(m *Model) {
			defer all.Done()
			clk := performanceClock{fakeClock: newClock(), start: t0, intensity: .1}
			m.Advance(t0, clk)
			for step := 1; step <= 3; step++ {
				clk.intensity = .1 * float64(step+1)
				m.Advance(t0.Add(time.Duration(step)*Step), clk)
			}
		}(m)
		go func(m *Model) {
			defer all.Done()
			for step := 0; step < 10; step++ {
				m.State("p", t0)
				m.Window("p", t0.Add(-time.Minute), t0)
				m.Apply("p", t0, func(string) float64 { return .5 })
			}
		}(m)
	}
	all.Wait()
	if peak.Load() > 2 || peak.Load() == 0 || active.Load() != 0 || len(conditionalPermits) != 0 {
		t.Fatalf("process budget/lifecycle active=%d peak=%d", active.Load(), peak.Load())
	}
}
