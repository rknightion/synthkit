// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
)

// Compare every atom, not only a mass/mean tolerance: sort ties, probability
// accumulation order, remainders and scratch lifetime are numerical contracts.
func TestTimingCompactionExact(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 29))
	scratch := &timingScratch{}
	for trial := 0; trial < 60; trial++ {
		in := make([]elapsed, trial*23)
		for i := range in {
			key := outcomeKey{}
			if i%3 == 0 {
				key = outcomeKey{class: ClassTimeout}
			}
			in[i] = elapsed{key: key, p: rng.Float64(), ms: float64(rng.IntN(25)), held: rng.Float64()}
			if i%17 == 0 {
				in[i].p = 0
			}
		}
		for _, bins := range []int{8, 32, 256, 512} {
			scratch.reset()
			want := referenceTimingCompact(in, bins)
			got := scratch.compact(in, bins)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("trial=%d bins=%d changed atoms", trial, bins)
			}
		}
	}
	if scratch.retained > maxTimingScratchAtoms {
		t.Fatal("unbounded scratch")
	}
}

// This is the pre-optimization compactor, retained as an arithmetic oracle.
func referenceTimingCompact(in []elapsed, bins int) []elapsed {
	var keys []outcomeKey
	for _, e := range in {
		if e.p > 0 && !slices.Contains(keys, e.key) {
			keys = append(keys, e.key)
		}
	}
	var out []elapsed
	for _, key := range keys {
		var group []elapsed
		total := 0.0
		for _, e := range in {
			if e.key == key && e.p > 0 {
				group = append(group, e)
				total += e.p
			}
		}
		if len(group) <= bins {
			out = append(out, group...)
			continue
		}
		slices.SortStableFunc(group, func(a, b elapsed) int {
			if a.ms < b.ms {
				return -1
			}
			if a.ms > b.ms {
				return 1
			}
			return 0
		})
		cell := 0
		width := total * referenceTimingCell(0, bins)
		bucket := elapsed{key: key}
		for _, e := range group {
			for e.p > 0 {
				take := math.Min(e.p, width-bucket.p)
				bucket.ms += float64(take * e.ms)
				bucket.held += float64(take * e.held)
				bucket.p += take
				e.p -= take
				if bucket.p >= width {
					bucket.ms /= bucket.p
					bucket.held /= bucket.p
					out = append(out, bucket)
					bucket = elapsed{key: key}
					cell++
					width = total * referenceTimingCell(cell, bins)
				}
			}
		}
		if bucket.p > 0 {
			bucket.ms /= bucket.p
			bucket.held /= bucket.p
			out = append(out, bucket)
		}
	}
	return out
}
func referenceTimingCell(i, bins int) float64 {
	boundary := func(j int) float64 {
		if j >= bins {
			return 1
		}
		return (1 - math.Cos(math.Pi*float64(j)/float64(bins))) / 2
	}
	if i >= bins {
		return 1
	}
	return boundary(i+1) - boundary(i)
}

func TestTimingChildPlanExactInvalidation(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		m := mustCompile(t, legacyPerformanceSet(ordered, false))
		cf := m.flows[0]
		cf.prepare(newClock(), t0)
		p := m.byName["p"]
		p.last = rec{valid: true, st: State{WaitMS: 100}}
		g := p.gates[0]
		rejection := 0.0
		rej := func(c *compiled) float64 {
			if c == p {
				return rejection
			}
			return .02
		}
		for step := 0; step < 8; step++ {
			switch step {
			case 1:
				p.last.st.WaitMS = 127.3 // parent wait does not invalidate child convolution
			case 2:
				cf.nodes[1].storm = 1.17 // nor does parent own work
			case 3:
				cf.nodes[2].storm = 1.3 // persisted child baseline DOES invalidate
			case 4:
				rejection = .07 // changed admission mass must not scale old centroids
			case 5:
				cf.legacyRootScale = 1.2 // shared request budget invalidates all affected children
			case 6:
				cf.nodes[2].hop = .1 // child failure outcome/mass and order
			case 7:
				if ordered {
					m.byName["q"].last = rec{valid: true, st: State{WaitMS: 210}}
				}
			}
			g.legacyCache = nil
			got := g.legacyArrivals(rej, 32, 8)
			want := referenceTimingArrivals(g, rej, 32, 8)
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("ordered=%v step=%d got %.17g want %.17g", ordered, step, got, want)
			}
			if cf.planSlots > maxLegacyPlanSlots || cf.planAtoms > maxLegacyPlanAtoms {
				t.Fatal("unbounded plan retention")
			}
		}
	}
}

// No answer cache, child-plan cache or reusable scratch. Exercise the same point
// count/reflections and first/fresh/materialized paths independently of reuse.
func referenceTimingArrivals(g *gate, rej rejectFn, points, bins int) float64 {
	cf := g.flow
	primes := timingPrimes(len(cf.nodes))
	value := 0.0
	for point := 1; point <= points; point++ {
		for reflection := range 4 {
			e := &legacyEval{flow: cf, rej: rej, base: cf.legacyBase(point, primes, reflection, points), bins: bins, cache: make([][4][]elapsed, len(cf.nodes))}
			memo := make([][2]float64, len(g.path))
			for i := range memo {
				memo[i] = [2]float64{math.NaN(), math.NaN()}
			}
			value += g.conditionalArrivals(e, 0, false, memo)
		}
	}
	return value / float64(4*points)
}
