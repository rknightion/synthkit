// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"math"
	"math/bits"
)

// These exact subproblems recur even when the enclosing solver vector changes:
// a normalized law CDF, or a complete ordered atom vector's compaction. Cache
// neither a rounded argument nor a scaled answer. Hashes select a bounded slot;
// every dependency is compared before reuse. Collisions only cause recomputation.
const (
	timingCDFSlots     = 4096
	timingCompactSlots = 256
	maxTimingMemoAtoms = 1 << 18
)

type timingCDFCell struct {
	x     uint64
	law   workLaw
	valid bool
	value float64
}
type timingCompactCell struct {
	hash          uint64
	bins          int
	input, output []elapsed
}
type timingMemo struct {
	sharded                                      bool
	cdf                                          []timingCDFCell
	compact                                      []timingCompactCell
	emission                                     []timingEmissionEntry
	emissionAtoms                                int
	emissionCalls, emissionHits                  uint64
	atoms                                        int
	cdfCalls, cdfHits, compactCalls, compactHits uint64
}

func (m *timingMemo) share(total int) int {
	if m.sharded {
		return total / 2
	}
	return total
}

func (m *timingMemo) cdfValue(law workLaw, x float64) float64 {
	if m == nil {
		return law.cdf(x)
	}
	m.cdfCalls++
	// These laws/domain branches are cheaper than a cache lookup, and retain
	// their original exact hard endpoints. No integration cell is skipped.
	if law != workLognormal || x <= 0 {
		return law.cdf(x)
	}
	key := math.Float64bits(x)
	if m.cdf == nil {
		m.cdf = make([]timingCDFCell, m.share(timingCDFSlots))
	}
	slot := &m.cdf[(mixTimingBits(key)^uint64(law))&uint64(len(m.cdf)-1)]
	value := 0.0
	if slot.valid && slot.x == key && slot.law == law {
		m.cdfHits++
		value = slot.value
	} else {
		value = law.cdf(x)
		*slot = timingCDFCell{x: key, law: law, valid: true, value: value}
	}
	return value
}

func mixTimingBits(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func timingAtomHash(in []elapsed, bins int) uint64 {
	h := uint64(bins) ^ uint64(len(in))*0x9e3779b97f4a7c15
	for _, e := range in {
		h = bits.RotateLeft64(h, 11) ^ math.Float64bits(e.p)
		h = h*1099511628211 ^ bits.RotateLeft64(math.Float64bits(e.ms), 17) ^ bits.RotateLeft64(math.Float64bits(e.held), 31) ^ uint64(e.key.status) ^ uint64(len(e.key.class))
		if e.key.rule {
			h ^= 0xd6e8feb86659fd93
		}
	}
	// The complete key (including class bytes) is checked on every hit. A
	// deliberately cheap slot hash need not distinguish every dependency.
	return mixTimingBits(h)
}

func sameTimingAtoms(a, b []elapsed) bool {
	if len(a) != len(b) {
		return false
	}
	for i, e := range a {
		f := b[i]
		if e.key != f.key || math.Float64bits(e.p) != math.Float64bits(f.p) || math.Float64bits(e.ms) != math.Float64bits(f.ms) || math.Float64bits(e.held) != math.Float64bits(f.held) {
			return false
		}
	}
	return true
}

func (m *timingMemo) compactLookup(in []elapsed, bins int) ([]elapsed, uint64, bool) {
	if m == nil {
		return nil, 0, false
	}
	m.compactCalls++
	h := timingAtomHash(in, bins)
	if m.compact == nil {
		m.compact = make([]timingCompactCell, m.share(timingCompactSlots))
	}
	cell := &m.compact[h&uint64(len(m.compact)-1)]
	if cell.bins == bins && cell.hash == h && sameTimingAtoms(cell.input, in) {
		m.compactHits++
		return cell.output, h, true
	}
	return nil, h, false
}

func (m *timingMemo) compactKeep(in, out []elapsed, bins int, h uint64) {
	if m == nil {
		return
	}
	if len(in)+len(out) > m.share(maxTimingMemoAtoms) {
		return
	}
	cell := &m.compact[h&uint64(len(m.compact)-1)]
	previous := cap(cell.input) + cap(cell.output)
	remaining := m.share(maxTimingMemoAtoms) - (m.atoms - previous)
	if len(in)+len(out) > remaining {
		return
	}
	if max(cap(cell.input), len(in))+max(cap(cell.output), len(out)) > remaining {
		return
	}
	input, output := cell.input[:0], cell.output[:0]
	if cap(input) < len(in) {
		input = make([]elapsed, 0, len(in))
	}
	if cap(output) < len(out) {
		output = make([]elapsed, 0, len(out))
	}
	count := cap(input) + cap(output)
	if count > remaining {
		return
	}
	input = append(input, in...)
	output = append(output, out...)
	m.atoms = m.atoms - previous + count
	*cell = timingCompactCell{hash: h, bins: bins, input: input, output: output}
}
