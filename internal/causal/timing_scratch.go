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
}

const maxTimingScratchAtoms = 1 << 18

func (s *timingScratch) take() (int, []elapsed) {
	if s == nil {
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
	if s == nil {
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
	ti, tmp := s.take()
	if cap(tmp) < len(group) {
		tmp = make([]elapsed, len(group))
	} else {
		tmp = tmp[:len(group)]
	}
	defer func() { s.keep(ti, tmp) }()
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
