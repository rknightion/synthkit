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
	return w.atomsScratch(bins, nil, cuts...)
}

func (w acquisitionWait) atomsScratch(bins int, scratch *timingScratch, cuts ...float64) []elapsed {
	maxMS := float64(w.quantile(1)) / float64(time.Millisecond)
	if maxMS == 0 {
		return []elapsed{{p: 1}}
	}
	continuous := w.cdf(maxMS - 1/float64(time.Millisecond))
	var boundaryStorage [13]float64
	boundaries := append(boundaryStorage[:0], 0, continuous)
	for _, ms := range cuts {
		u := w.cdf(ms)
		if u > 0 && u < continuous {
			boundaries = append(boundaries, u)
		}
	}
	// Only the small cut list is sorted; each interval has its own bounded cells.
	slices.Sort(boundaries)
	oi, out := scratch.take()
	defer func() { scratch.keep(oi, out) }()
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
	w.addScratch(next, bins, nil)
}

func (w *waitTiming) addScratch(next acquisitionWait, bins int, scratch *timingScratch) {
	if next.quantile(1) == 0 {
		return
	}
	if w.earlier == nil {
		w.earlier = []elapsed{{p: 1}}
	}
	if w.last.quantile(1) > 0 {
		si, sum := scratch.take()
		atoms := w.last.atomsScratch(bins, scratch)
		for _, a := range w.earlier {
			for _, b := range atoms {
				sum = append(sum, elapsed{p: a.p * b.p, ms: a.ms + b.ms})
			}
		}
		scratch.keep(si, sum)
		w.earlier = scratch.compact(sum, bins)
	}
	w.last = next
}

func (w waitTiming) emit(out []elapsed, key outcomeKey, p float64, terms []durationTerm, timeout float64, transport, materialize bool, bins int, law workLaw) []elapsed {
	return w.emitScratch(out, key, p, terms, timeout, transport, materialize, bins, law, nil)
}

func (w waitTiming) emitScratch(out []elapsed, key outcomeKey, p float64, terms []durationTerm, timeout float64, transport, materialize bool, bins int, law workLaw, scratch *timingScratch) []elapsed {
	if p <= 0 {
		return out
	}
	var memo *timingMemo
	if scratch != nil {
		memo = scratch.memo
	}
	if w.earlier == nil || transport {
		return emitElapsedLawMemo(out, key, p, terms, timeout, transport, materialize, bins, law, memo)
	}
	ai, added := scratch.take()
	defer func() { scratch.keep(ai, added) }()
	shifted := make([]durationTerm, len(terms))
	var bases [11]float64
	if timeout > 0 {
		for i, u := range [...]float64{1e-12, 1e-6, .001, .01, .1, .5, .9, .99, .999, 1 - 1e-6, 1 - 1e-12} {
			x := law.quantile(u)
			for _, t := range terms {
				bases[i] = math.Max(bases[i], float64(t.a*x)+t.b)
			}
		}
	}
	for _, prior := range w.earlier {
		var cutStorage [11]float64
		cuts := cutStorage[:0]
		if timeout > 0 {
			// Resolve the transition where the own-work CDF changes, even when
			// its scale is tiny compared with the acquisition wait. Fixed work
			// (including zero work) gets an exact deadline split. These are
			// integration breakpoints, not new sampler draws.
			for _, base := range bases {
				cuts = append(cuts, timeout-base-prior.ms)
			}
		}
		var programStorage [128]timingEmissionCell
		program := programStorage[:0]
		useProgram := !materialize && memo != nil
		for _, a := range w.last.atomsScratch(bins, scratch, cuts...) {
			for i, t := range terms {
				shifted[i] = durationTerm{t.a, t.b + prior.ms + a.ms}
			}
			if useProgram {
				mass := p * prior.p * a.p
				cdf, pending := 1.0, false
				if mass > 0 {
					cdf, pending = elapsedLawInput(shifted, timeout, transport, law)
				}
				program = append(program, timingEmissionCell{p: mass, cdf: cdf, pending: pending})
			} else {
				added = emitElapsedLawMemo(added, key, p*prior.p*a.p, shifted, timeout, transport, materialize, bins, law, memo)
			}
		}
		// Bound temporary storage between prior cells, not just at the end of a node.
		if useProgram {
			added = scratch.emitProgram(added, program, key, timeout, bins, law, memo)
		} else {
			compacted := scratch.compact(added, bins)
			added = append(added[:0], compacted...)
		}
	}
	return append(out, added...)
}
