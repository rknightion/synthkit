// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"math"
	"slices"
	"time"
)

// acquisitionWait is Apply's admitted pool wait, not the state's WaitMS used as a
// deterministic duration: min(round(scale*(0.5+U) ms), round(limit ms)). The cap
// creates a point mass. Keep the draw transform and timing integration together.
// Rounding is in integer nanoseconds, just as Effect.Wait observed by the app.
type acquisitionWait struct{ scale, limit float64 }

func (w acquisitionWait) quantile(u float64) time.Duration {
	if w.scale <= 0 {
		return 0
	}
	return min(msDuration(float64(w.scale*float64(0.5+u))), msDuration(w.limit))
}

// cdf returns P(Wait <= ms), including the atom at the rounded acquisition limit.
func (w acquisitionWait) cdf(ms float64) float64 {
	if ms < 0 {
		return 0
	}
	if ms >= float64(w.quantile(1))/float64(time.Millisecond) {
		return 1
	}
	if w.scale <= 0 {
		return 1
	}
	// Round-to-nearest (positive half ties up): Wait <= floor(ms*1e6) iff
	// the unrounded wait is below (floor(ms*1e6)+0.5) ns. A continuous U
	// assigns zero probability to the boundary itself.
	edge := (math.Floor(ms*float64(time.Millisecond)) + 0.5) / float64(time.Millisecond)
	return clamp01(edge/w.scale - 0.5)
}

func (c *compiled) admittedWait() acquisitionWait {
	return acquisitionWait{scale: c.waitMS(), limit: c.r.AcquireTimeoutMS}
}

// waitAtoms integrates the continuous part by probability cells and retains the cap
// atom separately. Optional cuts split cells at observable fixed-work deadlines, so
// deterministic work is not misclassified by a cell straddling the timeout.
func (w acquisitionWait) atoms(bins int, cuts ...float64) []elapsed {
	maxMS := float64(w.quantile(1)) / float64(time.Millisecond)
	if maxMS == 0 {
		return []elapsed{{p: 1}}
	}
	continuous := w.cdf(maxMS - 1/float64(time.Millisecond))
	boundaries := []float64{0, continuous}
	for _, ms := range cuts {
		u := w.cdf(ms)
		if u > 0 && u < continuous {
			boundaries = append(boundaries, u)
		}
	}
	// Only the small cut list is sorted; each interval has its own bounded cells.
	slices.Sort(boundaries)
	out := make([]elapsed, 0, bins+len(cuts)+1)
	for i := 1; i < len(boundaries); i++ {
		lo, hi := boundaries[i-1], boundaries[i]
		if hi <= lo {
			continue
		}
		count := max(8, int(math.Ceil(float64(bins)*(hi-lo))))
		for j := range count {
			a := lo + float64((hi-lo)*probabilityBoundary(j, count))
			b := lo + float64((hi-lo)*probabilityBoundary(j+1, count))
			out = append(out, elapsed{p: b - a, ms: float64(w.quantile(float64((a+b)/2))) / float64(time.Millisecond)})
		}
	}
	if continuous < 1 {
		out = append(out, elapsed{p: 1 - continuous, ms: maxMS})
	}
	return out
}

// waitTiming keeps the last admitted acquisition as a law, with a bounded
// convolution of earlier independent acquisitions. A later resource rejection
// observes exactly the waits accumulated before it (except transport failures,
// whose evaluator deliberately discards those waits).
type waitTiming struct {
	earlier []elapsed
	last    acquisitionWait
}

func (w *waitTiming) add(next acquisitionWait, bins int) {
	if next.quantile(1) == 0 {
		return
	}
	if w.earlier == nil {
		w.earlier = []elapsed{{p: 1}}
	}
	if w.last.quantile(1) > 0 {
		var sum []elapsed
		for _, a := range w.earlier {
			for _, b := range w.last.atoms(bins) {
				sum = append(sum, elapsed{p: a.p * b.p, ms: a.ms + b.ms})
			}
		}
		w.earlier = compactElapsed(sum, bins)
	}
	w.last = next
}

func (w waitTiming) emit(out []elapsed, key outcomeKey, p float64, terms []durationTerm, timeout float64, transport, materialize bool, bins int, law workLaw) []elapsed {
	if p <= 0 {
		return out
	}
	if w.earlier == nil || transport {
		return emitElapsedLaw(out, key, p, terms, timeout, transport, materialize, bins, law)
	}
	var added []elapsed
	shifted := make([]durationTerm, len(terms))
	for _, prior := range w.earlier {
		var cuts []float64
		if timeout > 0 {
			// Resolve the transition where the own-work CDF changes, even when
			// its scale is tiny compared with the acquisition wait. Fixed work
			// (including zero work) gets an exact deadline split. These are
			// integration breakpoints, not new sampler draws.
			for _, u := range []float64{1e-12, 1e-6, .001, .01, .1, .5, .9, .99, .999, 1 - 1e-6, 1 - 1e-12} {
				x, base := law.quantile(u), 0.0
				for _, t := range terms {
					base = math.Max(base, float64(t.a*x)+t.b)
				}
				cuts = append(cuts, timeout-base-prior.ms)
			}
		}
		for _, a := range w.last.atoms(bins, cuts...) {
			for i, t := range terms {
				shifted[i] = durationTerm{t.a, t.b + prior.ms + a.ms}
			}
			added = emitElapsedLaw(added, key, p*prior.p*a.p, shifted, timeout, transport, materialize, bins, law)
		}
		// Bound temporary storage between prior cells, not just at the end of a node.
		added = compactElapsed(added, bins)
	}
	return append(out, added...)
}
