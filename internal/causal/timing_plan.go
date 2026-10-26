// SPDX-License-Identifier: AGPL-3.0-only

package causal

import "slices"

// Child convolution does not depend on a caller's own work or acquisition waits.
// Keep its exact conditional results across solver iterates, keyed by ALL child
// inputs, parent admission mass, resolution and shared legacy budget scale. In
// particular, never scale a cached probability vector by a new admission mass:
// that would change the rounding inside compactElapsed.
//
// Both tables and atom storage are bounded per flow. Exhausting either bound
// disables retention of additional entries, never evaluation or quadrature.
const (
	maxLegacyPlanSlots = 32768
	maxLegacyPlanAtoms = 1 << 20
)

type timedChildren struct{ failures, paths []elapsed }
type timedChildrenCache struct {
	inputs       []float64
	points, bins int
	slots        [][2]*timedChildren
	atoms        int
}

func (cf *cflow) childPlans(rej rejectFn, points, bins int) []*timedChildrenCache {
	plans := make([]*timedChildrenCache, len(cf.nodes))
	if 4*points > maxLegacyPlanSlots {
		return plans
	}
	for n := range cf.nodes {
		fn := &cf.nodes[n]
		if len(fn.kids) == 0 {
			continue
		}
		remaining := 1.0
		for _, u := range fn.uses {
			if p := rej(u); p > 0 {
				remaining *= 1 - p
			}
		}
		inputs := []float64{cf.legacyRootScale, remaining}
		for _, k := range fn.kids {
			inputs = cf.elapsedInputs(k, rej, inputs)
		}
		cache := &fn.childrenCache
		if cache.points != points || cache.bins != bins || !slices.Equal(cache.inputs, inputs) {
			cf.planAtoms -= cache.atoms
			cache.atoms = 0
			cache.inputs, cache.points, cache.bins = inputs, points, bins
			clear(cache.slots)
			if len(cache.slots) != 4*points {
				cf.planSlots -= len(cache.slots)
				cache.slots = nil
				if cf.planSlots+4*points <= maxLegacyPlanSlots {
					cache.slots = make([][2]*timedChildren, 4*points)
					cf.planSlots += len(cache.slots)
				}
			}
		}
		if len(cache.slots) > 0 {
			plans[n] = cache
		}
	}
	return plans
}

// Transfer exclusive, capacity-clipped disjoint slot regions for one joined
// epoch. No pointer-table copy or second publication cache is retained. Only the
// owning worker touches its region, and each has half the aggregate atom quota.
// Parent cache metadata/counters are untouched until both regions are returned.
type legacyPlanView struct {
	slots  [][2]*timedChildren
	offset int
}
type legacyPlanShard struct {
	views        []legacyPlanView
	atoms, limit int
}

func splitLegacyPlans(plans []*timedChildrenCache, count int) [2]*legacyPlanShard {
	var shards [2]*legacyPlanShard
	for worker := range 2 {
		shard := &legacyPlanShard{views: make([]legacyPlanView, len(plans)), limit: maxLegacyPlanAtoms / 2}
		start, end := worker*count/2, (worker+1)*count/2
		for n, cache := range plans {
			if cache == nil {
				continue
			}
			slots := cache.slots[start:end:end]
			shard.views[n] = legacyPlanView{slots: slots, offset: start}
			for _, pair := range slots {
				for _, entry := range pair {
					if entry != nil {
						shard.atoms += cap(entry.paths) + cap(entry.failures)
					}
				}
			}
		}
		// A serially constructed proof/input may predate the fixed ownership split.
		// Drop retention, not evaluation, if that owner already exceeds its quota.
		if shard.atoms > shard.limit {
			for _, view := range shard.views {
				clear(view.slots)
			}
			shard.atoms = 0
		}
		shards[worker] = shard
	}
	return shards
}

func (cf *cflow) recountLegacyPlans() {
	cf.planAtoms = 0
	for n := range cf.nodes {
		cache := &cf.nodes[n].childrenCache
		cache.atoms = 0
		for _, pair := range cache.slots {
			for _, entry := range pair {
				if entry != nil {
					cache.atoms += cap(entry.paths) + cap(entry.failures)
				}
			}
		}
		cf.planAtoms += cache.atoms
	}
}

func (e *legacyEval) childPlan(n int, fresh bool) *timedChildren {
	if e.plans == nil || e.plans.views[n].slots == nil {
		return nil
	}
	view := e.plans.views[n]
	f := 0
	if fresh {
		f = 1
	}
	return view.slots[e.point-view.offset][f]
}

func (e *legacyEval) keepChildPlan(n int, fresh bool, children timedChildren) {
	if e.plans == nil || e.plans.views[n].slots == nil {
		return
	}
	view := e.plans.views[n]
	f := 0
	if fresh {
		f = 1
	}
	slot := &view.slots[e.point-view.offset][f]
	if *slot != nil {
		return
	}
	count := len(children.paths) + len(children.failures)
	if count > e.plans.limit-e.plans.atoms {
		return
	}
	// Exact-length owned copies charge unpublished construction to this same
	// owner's quota; they never alias point scratch after reset.
	paths, failures := make([]elapsed, len(children.paths)), make([]elapsed, len(children.failures))
	copy(paths, children.paths)
	copy(failures, children.failures)
	*slot = &timedChildren{paths: paths, failures: failures}
	e.plans.atoms += cap(paths) + cap(failures)
}
