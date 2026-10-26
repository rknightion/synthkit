// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"math"
	"slices"
)

func rawProbabilityBoundary(i, bins int) float64 {
	if i >= bins {
		return 1
	}
	return (1 - math.Cos(math.Pi*float64(i)/float64(bins))) / 2
}

// timingScratch owns temporary atom slices for one legacy quadrature evaluation.
// Results can alias these buffers until reset, which happens only after the entire
// conditional evaluation (and all first/fresh/materialized caches) is discarded.
// It is evaluator-local, never shared between models or concurrent callers.
// Bound retained backing storage even when a declared tree has a large Cartesian
// intermediate. Larger intermediates still run normally, without being retained.
type timingScratch struct {
	buffers        [][]elapsed
	used, retained int
	memo           *timingMemo
	limit          int // zero preserves the standalone serial helper's original cap
	// transient scratch lends no slot buffers (take returns nil, as for a nil scratch, so
	// every result is caller-owned) and only reuses the call-local temporaries below. The
	// non-legacy flow evaluation uses it: its results persist in per-node caches.
	transient bool
	// Call-local temporaries that never escape the call using them: compaction's per-key
	// group, the sort's workspace, and the output of compactTransient (valid until that
	// method's next call). Reusing them changes no arithmetic; the slot buffers above would
	// otherwise grow by three per compaction until reset, and a point with many compactions
	// exceeds the retention bound and allocates.
	group, sortTmp, cout []elapsed
	// added and atoms are emitScratch's per-call emission and acquisition-wait cells: both
	// are copied out (or consumed) before that call returns, and emitScratch never nests.
	added, atoms []elapsed
}

const maxTimingScratchAtoms = 1 << 18

func (s *timingScratch) take() (int, []elapsed) {
	if s == nil || s.transient {
		return -1, nil
	}
	i := s.used
	s.used++
	if i == len(s.buffers) {
		s.buffers = append(s.buffers, nil)
	}
	return i, s.buffers[i][:0]
}

func (s *timingScratch) keep(i int, b []elapsed) {
	if s == nil || i < 0 {
		return
	}
	s.retained -= cap(s.buffers[i])
	limit := s.limit
	if limit == 0 {
		limit = maxTimingScratchAtoms
	}
	if cap(b) <= limit-s.retained {
		s.buffers[i] = b[:0]
		s.retained += cap(b)
	} else {
		s.buffers[i] = nil
	}
}

func (s *timingScratch) reset() { s.used = 0 }

// A stable merge uses bounded evaluator-local workspace instead of the standard
// library's in-place rotation sort. Ties retain encounter order, including held
// values: compression sees exactly the same sequence as SortStableFunc.
func (s *timingScratch) sortElapsed(group []elapsed) {
	sorted := true
	for i, e := range group {
		if math.IsNaN(e.ms) {
			// Preserve the original comparator's treatment of unordered values.
			slices.SortStableFunc(group, func(a, b elapsed) int {
				if a.ms < b.ms {
					return -1
				}
				if a.ms > b.ms {
					return 1
				}
				return 0
			})
			return
		}
		if i > 0 && e.ms < group[i-1].ms {
			sorted = false
		}
	}
	if sorted {
		return
	}
	if s.sortFewDistinct(group) {
		return
	}
	const run = 16
	for lo := 0; lo < len(group); lo += run {
		hi := min(lo+run, len(group))
		for i := lo + 1; i < hi; i++ {
			value := group[i]
			j := i
			for j > lo && value.ms < group[j-1].ms {
				group[j] = group[j-1]
				j--
			}
			group[j] = value
		}
	}
	if len(group) <= run {
		return
	}
	var tmp []elapsed
	if s != nil {
		tmp = s.sortTmp
	}
	if cap(tmp) < len(group) {
		tmp = make([]elapsed, len(group))
	} else {
		tmp = tmp[:len(group)]
	}
	if s != nil {
		defer func() { s.sortTmp = tmp[:0] }()
	}
	src, dst := group, tmp
	for width := run; width < len(group); width *= 2 {
		for lo := 0; lo < len(group); lo += 2 * width {
			mid, hi := min(lo+width, len(group)), min(lo+2*width, len(group))
			a, b := lo, mid
			for j := lo; j < hi; j++ {
				if b >= hi || (a < mid && !(src[b].ms < src[a].ms)) {
					dst[j] = src[a]
					a++
				} else {
					dst[j] = src[b]
					b++
				}
			}
		}
		src, dst = dst, src
	}
	copy(group, src)
}

// maxSortDistinct bounds the distinct-value stable sort. Compaction groups are often a few
// values (a timeout atom's mean is the timeout or a few rounding steps from it), where a
// counting placement replaces the O(n log n) merge.
const maxSortDistinct = 64

// sortFewDistinct stably sorts a NaN-free group with at most maxSortDistinct distinct ms
// values (ordered by <, so -0 and +0 are one value, as they tie under the merge): each
// value's elements are placed in encounter order, values ascending. A stable sort's output
// is unique for a given comparator, so this is exactly the merge below. It reports false,
// leaving the group untouched, when there are more distinct values.
func (s *timingScratch) sortFewDistinct(group []elapsed) bool {
	var storage [maxSortDistinct]float64
	values := storage[:0]
	// lower is the first index whose value is not below v.
	lower := func(v float64) int {
		lo, hi := 0, len(values)
		for lo < hi {
			mid := (lo + hi) / 2
			if values[mid] < v {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return lo
	}
	// Runs of one value are the common case (most atoms of a timeout group are exactly the
	// timeout): the previous element's value needs no search.
	prev, prevSet := 0.0, false
	for _, e := range group {
		if prevSet && e.ms == prev {
			continue
		}
		prev, prevSet = e.ms, true
		i := lower(e.ms)
		if i < len(values) && values[i] == e.ms {
			continue
		}
		if len(values) == maxSortDistinct {
			return false
		}
		values = append(values, 0)
		copy(values[i+1:], values[i:])
		values[i] = e.ms
	}
	var rankStorage [512]uint8
	ranks := rankStorage[:0]
	if len(group) > len(rankStorage) {
		ranks = make([]uint8, 0, len(group))
	}
	var start [maxSortDistinct + 1]int
	r := 0
	prevSet = false
	for _, e := range group {
		if !prevSet || e.ms != prev {
			prev, prevSet, r = e.ms, true, lower(e.ms)
		}
		ranks = append(ranks, uint8(r))
		start[r+1]++
	}
	for r := 1; r <= len(values); r++ {
		start[r] += start[r-1]
	}
	var tmp []elapsed
	if s != nil {
		tmp = s.sortTmp
	}
	if cap(tmp) < len(group) {
		tmp = make([]elapsed, len(group))
	} else {
		tmp = tmp[:len(group)]
	}
	for i, e := range group {
		r := ranks[i]
		tmp[start[r]] = e
		start[r]++
	}
	copy(group, tmp)
	if s != nil {
		s.sortTmp = tmp[:0]
	}
	return true
}

// Fixed, small immutable probability grids avoid repeating transcendental work.
// Other resolutions (including refinement tests) use the identical formula.
// Values are evaluated with precisely the same operations and rounding barriers
// as the uncached path; widths are subtractions of the same rounded boundaries.
var timingUnitQuantiles = func() [3][elapsedBins + 1][]float64 {
	var tables [3][elapsedBins + 1][]float64
	for law := workLognormal; law <= workJitter; law++ {
		for bins := 1; bins <= elapsedBins; bins++ {
			q := make([]float64, bins)
			for i := range q {
				lo, hi := rawProbabilityBoundary(i, bins), rawProbabilityBoundary(i+1, bins)
				q[i] = law.quantile((lo + hi) / 2)
			}
			tables[law][bins] = q
		}
	}
	return tables
}()

var timingProbabilityGrids = func() [elapsedBins + 1][]float64 {
	var grids [elapsedBins + 1][]float64
	for bins := 1; bins <= elapsedBins; bins++ {
		grid := make([]float64, bins+1)
		for i := range grid {
			grid[i] = rawProbabilityBoundary(i, bins)
		}
		grids[bins] = grid
	}
	return grids
}()
