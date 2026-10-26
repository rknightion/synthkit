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
				key = outcomeKey{class: classTimeout}
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

// Compaction groups with a few distinct durations (a timeout atom's mean is the timeout or
// one rounding step from it) take the distinct-value stable sort. It must give every atom
// bit for bit, ties (including -0 and +0) in encounter order, through every entry point.
func TestTimingCompactionFewDistinctExact(t *testing.T) {
	rng := rand.New(rand.NewPCG(41, 7))
	timeout := 1000.0
	values := []float64{timeout, math.Nextafter(timeout, 0), math.Nextafter(timeout, 2000), 0, math.Copysign(0, -1), 50, 7.25, math.Nextafter(50, 0)}
	scratch := &timingScratch{}
	transient := &timingScratch{transient: true}
	for trial := 0; trial < 80; trial++ {
		distinct := 1 + trial%(len(values)+2) // the last two cases: at and beyond the distinct-sort bound
		in := make([]elapsed, 40+trial*9)
		for i := range in {
			key := outcomeKey{}
			if i%3 != 0 {
				key = outcomeKey{class: classTimeout}
			}
			v := values[rng.IntN(min(distinct, len(values)))]
			switch distinct - len(values) {
			case 1: // exactly the distinct-sort bound
				v = float64(rng.IntN(maxSortDistinct))
			case 2: // more distinct values than the bound: the merge
				v = float64(rng.IntN(4 * maxSortDistinct))
			}
			in[i] = elapsed{key: key, p: rng.Float64(), ms: v, held: float64(i)}
			switch trial % 4 {
			case 1: // all held parts zero, of either sign
				in[i].held = math.Copysign(0, float64(i%2)-0.5)
			case 2: // all durations and held parts zero, of either sign (the outcome-only atoms)
				in[i].ms = math.Copysign(0, float64(i%3)-0.5)
				in[i].held = math.Copysign(0, float64(i%2)-0.5)
			}
		}
		for _, bins := range []int{8, 32, 256} {
			want := referenceTimingCompact(in, bins)
			scratch.reset()
			for name, got := range map[string][]elapsed{
				"slot":      scratch.compact(in, bins),
				"transient": slices.Clone(scratch.compactTransient(in, bins)),
				"nil":       (*timingScratch)(nil).compact(in, bins),
				"temps":     transient.compact(in, bins),
			} {
				if !sameTimingAtoms(got, want) {
					t.Fatalf("trial=%d bins=%d %s compaction changed atoms", trial, bins, name)
				}
			}
		}
		// The sort itself, with held recording encounter order across ties.
		group := slices.Clone(in)
		ref := slices.Clone(in)
		scratch.sortElapsed(group)
		slices.SortStableFunc(ref, func(a, b elapsed) int {
			if a.ms < b.ms {
				return -1
			}
			if a.ms > b.ms {
				return 1
			}
			return 0
		})
		if !sameTimingAtoms(group, ref) {
			t.Fatalf("trial=%d stable sort order changed", trial)
		}
	}
}

// A transient scratch lends no slot buffers: every result is caller-owned, so a later
// evaluation cannot overwrite a result a node cache still holds.
func TestTimingTransientScratchOwnsResults(t *testing.T) {
	s := &timingScratch{transient: true}
	i, b := s.take()
	if i >= 0 || b != nil {
		t.Fatal("transient scratch lent a slot buffer")
	}
	in := make([]elapsed, 100)
	for j := range in {
		in[j] = elapsed{p: float64(j + 1), ms: float64(j % 3)}
	}
	first := s.compact(in, 8)
	saved := slices.Clone(first)
	for j := range in {
		in[j].ms = float64(j % 5)
	}
	s.compact(in, 8)
	s.compactTransient(in, 8)
	if !sameTimingAtoms(first, saved) {
		t.Fatal("a later compaction overwrote an owned result")
	}
}

// Timing atoms are copied and compacted by the million per evaluation: they must stay
// pointer-free (no write barriers, no GC scanning), so outcome classes are interned codes
// that round-trip to the declared FailureClass, an undeclared class included.
func TestTimingAtomsPointerFreeAndClassesRoundTrip(t *testing.T) {
	var walk func(reflect.Type) bool
	walk = func(ty reflect.Type) bool {
		switch ty.Kind() {
		case reflect.Struct:
			for i := range ty.NumField() {
				if !walk(ty.Field(i).Type) {
					return false
				}
			}
			return true
		case reflect.Array:
			return walk(ty.Elem())
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
			return true
		}
		return false
	}
	if !walk(reflect.TypeOf(elapsed{})) {
		t.Fatal("elapsed holds a pointer")
	}
	for _, c := range []FailureClass{ClassNone, ClassHTTP, ClassThrottle, ClassTimeout, ClassConnect, ClassCredential, "c2-undeclared", "c2-undeclared-2"} {
		code := classOf(c)
		if got := code.Class(); got != c {
			t.Fatalf("class %q round-trips as %q", c, got)
		}
		if classOf(c) != code {
			t.Fatalf("class %q interned twice", c)
		}
	}
	if classOf("c2-undeclared") == classOf("c2-undeclared-2") || classOf(ClassHTTP) == classOf(ClassThrottle) {
		t.Fatal("distinct classes share a code")
	}
}

// A non-legacy gate reuses an answer only for bit-identical request-tree inputs: every input
// it reads (a use's reject share or pool wait, a node's work, stretch, slowed latency or hop
// error) must miss, and a hit must equal the uncached evaluation bit for bit.
func TestArrivalsCacheExactKeys(t *testing.T) {
	m := mustCompile(t, storySet(true))
	cf := m.flows[0]
	cf.prepare(newClock(), t0)
	pool, conc := m.byName["intake-db-pool"], m.byName["enrich-fn-concurrency"]
	pool.last = rec{valid: true, st: State{WaitMS: 40}}
	g := conc.gates[0]
	reject := map[*compiled]float64{conc: .2, m.byName["partner-api"]: .1}
	rej := func(c *compiled) float64 { return reject[c] }
	check := func(step string) {
		t.Helper()
		want := g.computeArrivals(rej)
		if got := g.arrivals(rej); math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("%s: arrivals %.17g, uncached %.17g", step, got, want)
		}
	}
	check("first")
	n := len(g.arrivalsCache)
	check("repeat")
	if len(g.arrivalsCache) != n {
		t.Fatal("repeated inputs were not answered from the cache")
	}
	for i, change := range []func(){
		func() { reject[conc] = .21 },
		func() { reject[pool] = math.Nextafter(0, 1) },
		func() { pool.last.st.WaitMS = math.Nextafter(40, 41) },
		func() { cf.nodes[3].storm = 1.5 },
		func() { cf.nodes[3].shift = 12 },
		func() { cf.nodes[2].hop = .05 },
		func() { cf.nodes[2].mean = 901 },
	} {
		change()
		before := len(g.arrivalsCache)
		check("change")
		if len(g.arrivalsCache) != before+1 {
			t.Fatalf("change %d was answered from the cache", i)
		}
	}
	for i := range 3 * maxFixedPoint {
		reject[conc] = float64(i) / 1000
		g.arrivals(rej)
	}
	if len(g.arrivalsCache) > 2*maxFixedPoint {
		t.Fatal("unbounded arrivals cache")
	}
}
