// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"math"
	"runtime"
	"slices"
	"sync"
)

// Mechanical process budget, not a registry or persistent worker pool. A caller
// holds one permit while evaluating; a second permit is acquired nonblocking so
// concurrent Models cannot deadlock while each holds its first permit.
var conditionalPermits = make(chan struct{}, 2)

const maxConditionalResults = 1 << 17 // at most 1 MiB of scalar result staging

type legacyRunMode struct {
	resultLimit int // proof seam may only lower the production result bound
	serial      bool
	reference   bool // actual serial bypass: no answer, child-plan, or subproblem caches
	before      func(worker int, parallel bool)
	after       func(worker int, parallel bool)
	observe     func(slot int, e *legacyEval)
}

func conditionalPair(serial bool, run func(int, bool)) {
	// The caller already holds its first permit before freezing/staging inputs.
	parallel := false
	var joined sync.WaitGroup
	var helperPanic any
	helperReturned := false
	if !serial && runtime.GOMAXPROCS(0) > 1 {
		select {
		case conditionalPermits <- struct{}{}:
			parallel = true
			joined.Add(1)
			go func() {
				defer joined.Done()
				defer func() { <-conditionalPermits }()
				defer func() { helperPanic = recover() }()
				run(1, true)
				helperReturned = true
			}()
		default:
		}
	}
	callerReturned := false
	if parallel {
		defer func() {
			joined.Wait()
			// Wait publishes the helper's failure as well as its results. A
			// caller panic keeps unwinding unchanged (and takes precedence if
			// both panic); otherwise transfer the helper failure to this stack.
			// Track return separately so even a nil panic cannot become success.
			if callerReturned && !helperReturned {
				panic(helperPanic)
			}
		}()
	}
	run(0, parallel)
	callerReturned = true
	if !parallel {
		run(1, false)
	}
}

// Freeze only the fields the conditional evaluator reads. No mutable caches,
// rings, queues, clocks or original resource pointers are reachable by workers.
func (g *gate) frozen(rej rejectFn) (*gate, rejectFn) {
	source := g.flow
	frozen := &cflow{nodes: make([]flowNode, len(source.nodes)), legacyRootScale: source.legacyRootScale, needsLegacy: source.needsLegacy}
	if source.legacy != nil {
		copy := *source.legacy
		frozen.legacy = &copy
	}
	resources := map[*compiled]*compiled{}
	rejection := map[*compiled]float64{}
	var copyResource func(*compiled) *compiled
	copyResource = func(c *compiled) *compiled {
		if c == nil {
			return nil
		}
		if copy := resources[c]; copy != nil {
			return copy
		}
		r := c.r
		r.Demand = nil
		r.Error = nil
		copy := &compiled{r: r, key: c.key, transport: c.transport, last: rec{valid: true, st: State{WaitMS: c.waitMS()}}}
		resources[c] = copy
		rejection[copy] = rej(c)
		copy.ref = copyResource(c.ref)
		return copy
	}
	for i, n := range source.nodes {
		node := flowNode{policy: n.policy, covered: n.covered, mean: n.mean, shift: n.shift, storm: n.storm, hop: n.hop, hopKey: n.hopKey}
		node.policy.RetryOn = slices.Clone(n.policy.RetryOn)
		node.kids = slices.Clone(n.kids)
		node.dur = copyResource(n.dur)
		for _, u := range n.uses {
			node.uses = append(node.uses, copyResource(u))
		}
		frozen.nodes[i] = node
	}
	return &gate{flow: frozen, path: slices.Clone(g.path), use: g.use}, func(c *compiled) float64 { return rejection[c] }
}

func (g *gate) evaluateConditional(rej rejectFn, points, bins int, mode legacyRunMode) float64 {
	conditionalPermits <- struct{}{}
	defer func() { <-conditionalPermits }()
	original := g.flow
	frozen, read := g.frozen(rej)
	primes := timingPrimes(len(frozen.flow.nodes))
	count := 4 * points
	var plans []*timedChildrenCache
	var shards [2]*legacyPlanShard
	if !mode.reference {
		// conditionalPair joins before returning or unwinding here. Recount
		// even after a panic, before the parent permit and Model lock release.
		defer original.recountLegacyPlans()
		plans = original.childPlans(rej, points, bins)
		shards = splitLegacyPlans(plans, count)
		for i := range original.timingMemos {
			original.timingMemos[i].sharded = true
		}
	}
	runPoint := func(slot int, e *legacyEval, memo [][2]float64) float64 {
		if e.scratch != nil {
			e.scratch.reset()
		}
		clear(e.cache)
		e.point = slot
		e.base = frozen.flow.legacyBase(slot/4+1, primes, slot%4, points)
		for i := range memo {
			memo[i] = [2]float64{math.NaN(), math.NaN()}
		}
		value := frozen.conditionalArrivals(e, 0, false, memo)
		if mode.observe != nil {
			mode.observe(slot, e)
		}
		return value
	}
	// Oversized diagnostic resolutions stream serially rather than allocating an
	// unbounded result vector. Runtime resolutions and the refinement controls fit.
	resultLimit := maxConditionalResults
	if mode.resultLimit > 0 && mode.resultLimit < resultLimit {
		resultLimit = mode.resultLimit
	}
	if mode.reference || points > resultLimit/4 {
		e := &legacyEval{flow: frozen.flow, rej: read, bins: bins, cache: make([][4][]elapsed, len(frozen.flow.nodes))}
		memo := make([][2]float64, len(g.path))
		value := 0.0
		for slot := 0; slot < count; slot++ {
			value += runPoint(slot, e, memo)
		}
		return value / float64(count)
	}
	values := make([]float64, count)
	conditionalPair(mode.serial, func(worker int, parallel bool) {
		if mode.before != nil {
			mode.before(worker, parallel)
		}
		if mode.after != nil {
			defer mode.after(worker, parallel)
		}
		scratch := &timingScratch{memo: &original.timingMemos[worker], limit: maxTimingScratchAtoms / 2}
		e := &legacyEval{flow: frozen.flow, rej: read, bins: bins, cache: make([][4][]elapsed, len(frozen.flow.nodes)), scratch: scratch, plans: shards[worker]}
		memo := make([][2]float64, len(g.path))
		for slot := worker * count / 2; slot < (worker+1)*count/2; slot++ {
			values[slot] = runPoint(slot, e, memo)
		}
	})
	value := 0.0
	for _, result := range values {
		value += result
	}
	return value / float64(count)
}
