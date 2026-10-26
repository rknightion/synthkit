// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"math"
	"math/bits"
)

// An outcome-only emission has no observable own-work duration atoms: its
// complete dependencies are the ordered masses and exact normalized CDF inputs
// (or already-known CDF constants), law, key, timeout and resolution. Cache BEFORE
// evaluating the transcendental CDFs, not just after rebuilding all their atoms.
// Every cell remains in the program and retains its original probability order.
type timingEmissionCell struct {
	p, cdf  float64
	pending bool // cdf is a normalized argument until evaluated under the program law
}
type timingEmissionEntry struct {
	hash           uint64
	key            outcomeKey
	timeout        uint64
	bins           int
	law            workLaw
	prefix, output []elapsed
	cells          []timingEmissionCell
}

func emissionHash(prefix []elapsed, cells []timingEmissionCell, key outcomeKey, timeout float64, bins int, law workLaw) uint64 {
	h := timingAtomHash(prefix, bins) ^ math.Float64bits(timeout) ^ uint64(key.status) ^ uint64(law)
	for i := 0; i < len(key.class); i++ {
		h = h*1099511628211 ^ uint64(key.class[i])
	}
	if key.rule {
		h ^= 0xd6e8feb86659fd93
	}
	for _, cell := range cells {
		h = (bits.RotateLeft64(h, 13)^math.Float64bits(cell.p))*1099511628211 ^ bits.RotateLeft64(math.Float64bits(cell.cdf), 23)
		if cell.pending {
			h ^= 0xd6e8feb86659fd93
		}
	}
	return mixTimingBits(h)
}

func sameEmissionCells(a, b []timingEmissionCell) bool {
	if len(a) != len(b) {
		return false
	}
	for i, c := range a {
		if c.pending != b[i].pending || math.Float64bits(c.p) != math.Float64bits(b[i].p) || math.Float64bits(c.cdf) != math.Float64bits(b[i].cdf) {
			return false
		}
	}
	return true
}

// Result is copied to point-owned scratch before any cache slot can be replaced.
// Thus fixed-size slots can safely reuse their own storage on a collision/miss.
func (m *timingMemo) emissionLookup(prefix []elapsed, cells []timingEmissionCell, key outcomeKey, timeout float64, bins int, law workLaw) ([]elapsed, uint64, bool) {
	if m == nil {
		return nil, 0, false
	}
	m.emissionCalls++
	h := emissionHash(prefix, cells, key, timeout, bins, law)
	if m.emission == nil {
		m.emission = make([]timingEmissionEntry, m.share(timingCompactSlots))
	}
	e := &m.emission[h&uint64(len(m.emission)-1)]
	if e.hash == h && e.key == key && e.timeout == math.Float64bits(timeout) && e.bins == bins && e.law == law && sameTimingAtoms(e.prefix, prefix) && sameEmissionCells(e.cells, cells) {
		m.emissionHits++
		return e.output, h, true
	}
	return nil, h, false
}

func (m *timingMemo) emissionKeep(prefix []elapsed, cells []timingEmissionCell, out []elapsed, key outcomeKey, timeout float64, bins int, law workLaw, h uint64) {
	if m == nil {
		return
	}
	e := &m.emission[h&uint64(len(m.emission)-1)]
	// Counting each smaller program cell as one elapsed atom is conservative.
	previous := cap(e.prefix) + cap(e.cells) + cap(e.output)
	remaining := m.share(maxTimingMemoAtoms) - (m.emissionAtoms - previous)
	if len(prefix)+len(cells)+len(out) > remaining {
		return
	}
	if max(cap(e.prefix), len(prefix))+max(cap(e.cells), len(cells))+max(cap(e.output), len(out)) > remaining {
		return
	}
	pre, program, result := e.prefix[:0], e.cells[:0], e.output[:0]
	if cap(pre) < len(prefix) {
		pre = make([]elapsed, 0, len(prefix))
	}
	if cap(program) < len(cells) {
		program = make([]timingEmissionCell, 0, len(cells))
	}
	if cap(result) < len(out) {
		result = make([]elapsed, 0, len(out))
	}
	count := cap(pre) + cap(program) + cap(result)
	if count > remaining {
		return
	}
	pre = append(pre, prefix...)
	program = append(program, cells...)
	result = append(result, out...)
	m.emissionAtoms = m.emissionAtoms - previous + count
	*e = timingEmissionEntry{hash: h, key: key, timeout: math.Float64bits(timeout), bins: bins, law: law, prefix: pre, cells: program, output: result}
}

func (scratch *timingScratch) emitProgram(added []elapsed, cells []timingEmissionCell, key outcomeKey, timeout float64, bins int, law workLaw, memo *timingMemo) []elapsed {
	cached, h, hit := memo.emissionLookup(added, cells, key, timeout, bins, law)
	if hit {
		return append(added[:0], cached...)
	}
	// Cache the prefix before append can overwrite its backing array. There is
	// only one earlier-wait atom in the common case, so that prefix is empty.
	pi, prefix := scratch.take()
	prefix = append(prefix, added...)
	defer func() { scratch.keep(pi, prefix) }()
	for _, cell := range cells {
		if cell.p <= 0 {
			continue
		}
		cdf := cell.cdf
		if cell.pending {
			cdf = memo.cdfValue(law, cell.cdf)
		}
		if timeout > 0 && cdf < 1 {
			added = append(added, elapsed{key: outcomeKey{class: ClassTimeout}, p: cell.p * (1 - cdf), ms: timeout})
		}
		if cdf != 0 {
			added = append(added, elapsed{key: key, p: cell.p * cdf})
		}
	}
	compacted := scratch.compact(added, bins)
	memo.emissionKeep(prefix, cells, compacted, key, timeout, bins, law, h)
	return append(added[:0], compacted...)
}
