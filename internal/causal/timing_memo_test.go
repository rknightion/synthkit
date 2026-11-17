// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

// The normalized constraint is an exact CDF subproblem, not a minimum of rounded
// probability approximations. Compare against the original term-by-term path.
func TestTimingLawCDFExact(t *testing.T) {
	memo := &timingMemo{}
	rng := rand.New(rand.NewPCG(127, 131))
	for law := workLognormal; law <= workJitter; law++ {
		for trial := 0; trial < 12000; trial++ {
			terms := make([]durationTerm, 1+trial%4)
			for i := range terms {
				terms[i] = durationTerm{a: math.Exp(rng.Float64()*16 - 8), b: rng.Float64()*2000 - 500}
				if trial%13 == 0 {
					terms[i].a = 0
				}
			}
			got := elapsedLawCDF(terms, 1000, false, law, memo)
			want := elapsedLawCDF(terms, 1000, false, law, nil)
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("law=%d trial=%d got %.17g want %.17g", law, trial, got, want)
			}
		}
	}
	for _, terms := range [][]durationTerm{
		{{1, 0}}, {{1, 1000}}, {{1, 999}, {-1, 1001}}, {{math.Inf(1), math.Inf(1)}, {0, 0}},
		{{math.NaN(), 0}, {1, 1000}}, {{1, math.Inf(-1)}, {1, math.Inf(1)}},
	} {
		for law := workLognormal; law <= workJitter; law++ {
			got, want := elapsedLawCDF(terms, 1000, false, law, memo), elapsedLawCDF(terms, 1000, false, law, nil)
			if !(math.IsNaN(got) && math.IsNaN(want)) && math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("special law=%d got %v want %v", law, got, want)
			}
		}
	}
	// Adjacent representable constraints exercise the rounding boundary that a
	// broad random numerical-tolerance comparison would miss.
	for trial := 0; trial < 200000; trial++ {
		x := .5 + 1.5*rng.Float64()
		terms := []durationTerm{{1, 1 - x}, {1, 1 - math.Nextafter(x, 2)}}
		got, want := elapsedLawCDF(terms, 1, false, workLognormal, memo), elapsedLawCDF(terms, 1, false, workLognormal, nil)
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("adjacent constraints x=%.17g got %.17g want %.17g", x, got, want)
		}
	}
	// Same normalized x under different laws must not alias.
	for _, law := range []workLaw{workFixed, workJitter, workLognormal, workFixed} {
		if got, want := memo.cdfValue(law, .9), law.cdf(.9); math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("CDF law key: got %v want %v", got, want)
		}
	}
}

func TestTimingCDFMemoExactArguments(t *testing.T) {
	memo := &timingMemo{}
	for _, arguments := range [][]float64{{.9, 1.1, 2}, {.9, 1.1, 2}, {1.2, 1.1, 2}, {1.2, .9, 1.1, 2}, {.9, math.Nextafter(1.1, 2), 2}} {
		for _, x := range arguments {
			got, want := memo.cdfValue(workLognormal, x), workLognormal.cdf(x)
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("CDF memo reused changed argument %v: got %.17g want %.17g", x, got, want)
			}
		}
	}
	if memo.cdfHits == 0 {
		t.Fatal("repeated exact CDF argument not reused")
	}
}

func TestTimingMemoExactKeysAndLifetime(t *testing.T) {
	memo := &timingMemo{}
	scratch := &timingScratch{memo: memo}
	in := make([]elapsed, 80)
	for i := range in {
		in[i] = elapsed{p: float64(i+1) / 10000, ms: float64(i % 5), held: float64(i % 7)}
	}
	first := slices.Clone(scratch.compact(in, 8))
	hit := slices.Clone(scratch.compact(in, 8))
	if !sameTimingAtoms(first, hit) || memo.compactHits == 0 {
		t.Fatal("exact repeated compaction not reused")
	}
	for change := 0; change < 8; change++ {
		next := slices.Clone(in)
		bins := 8
		switch change {
		case 0:
			next[0].p = math.Nextafter(next[0].p, 1)
		case 1:
			next[0].ms = math.Nextafter(next[0].ms, 1)
		case 2:
			next[0].held = math.Nextafter(next[0].held, 1)
		case 3:
			next[0].key = outcomeKey{class: classHTTP, status: 503}
		case 4:
			next[0].key.rule = true
		case 5:
			slices.Reverse(next)
		case 6:
			bins = 32
		case 7:
			next[0].ms = math.Copysign(0, -1)
		}
		got, want := scratch.compact(next, bins), referenceTimingCompact(next, bins)
		if !sameTimingAtoms(got, want) {
			t.Fatalf("compaction dependency %d", change)
		}
	}
	// Same cheap slot hash but a different class must fail the complete comparison (the
	// slot hash folds the interned class code into the status bits).
	a, b := slices.Clone(in), slices.Clone(in)
	for i := range a {
		a[i].key = outcomeKey{class: classHTTP, status: 0x200}
		b[i].key = outcomeKey{class: classThrottle, status: 0x100}
	}
	if timingAtomHash(a, 8) != timingAtomHash(b, 8) {
		t.Fatal("fixture must collide")
	}
	live := scratch.compact(a, 8)
	saved := slices.Clone(live)
	scratch.compact(b, 8)
	if !sameTimingAtoms(live, saved) {
		t.Fatal("replacement mutated a live point result")
	}
	if !sameTimingAtoms(scratch.compact(a, 8), referenceTimingCompact(a, 8)) {
		t.Fatal("collision reused wrong key")
	}
	scratch.reset()
	if !sameTimingAtoms(scratch.compact(in, 8), first) {
		t.Fatal("reset corrupted memo-owned input/output")
	}
	if memo.atoms > maxTimingMemoAtoms {
		t.Fatal("unbounded memo")
	}
}

func TestTimingEmissionExactKeysAndLifetime(t *testing.T) {
	memo := &timingMemo{}
	scratch := &timingScratch{memo: memo}
	cells := []timingEmissionCell{{p: .3, cdf: .2}, {p: .2, cdf: .7}, {p: .5, cdf: .9}}
	key := outcomeKey{class: classHTTP, status: 503}
	prefix := []elapsed{{p: .1, ms: 23}}
	expected := func(cells []timingEmissionCell, key outcomeKey, timeout float64, bins int, law workLaw) []elapsed {
		out := slices.Clone(prefix)
		for _, c := range cells {
			if c.p <= 0 {
				continue
			}
			cdf := c.cdf
			if c.pending {
				cdf = law.cdf(c.cdf)
			}
			if timeout > 0 && cdf < 1 {
				out = append(out, elapsed{key: outcomeKey{class: classTimeout}, p: c.p * (1 - cdf), ms: timeout})
			}
			if cdf != 0 {
				out = append(out, elapsed{key: key, p: c.p * cdf})
			}
		}
		return referenceTimingCompact(out, bins)
	}
	for change := 0; change < 10; change++ {
		program := slices.Clone(cells)
		k, timeout, bins, law := key, 1000.0, 2, workLognormal
		switch change {
		case 1:
			program[0].p = math.Nextafter(program[0].p, 1)
		case 2:
			program[0].cdf = math.Nextafter(program[0].cdf, 1)
		case 3:
			k.rule = true
		case 4:
			timeout = 1200
		case 5:
			bins = 3
		case 6:
			slices.Reverse(program)
		case 7:
			prefix[0].p = math.Nextafter(prefix[0].p, 1)
		case 8:
			program[0].pending = true
		case 9:
			program[0].pending = true
			law = workJitter
		}
		for repeat := 0; repeat < 2; repeat++ {
			got := scratch.emitProgram(slices.Clone(prefix), program, k, timeout, bins, law, memo)
			if !sameTimingAtoms(got, expected(program, k, timeout, bins, law)) {
				t.Fatalf("emission dependency %d repeat %d", change, repeat)
			}
		}
	}
	if memo.emissionHits < 7 {
		t.Fatal("repeated exact emissions not reused")
	}
	if memo.emissionAtoms > maxTimingMemoAtoms {
		t.Fatal("unbounded emission memo")
	}
}
