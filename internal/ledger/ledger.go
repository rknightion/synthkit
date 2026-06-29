// SPDX-License-Identifier: AGPL-3.0-only

package ledger

import (
	"hash/fnv"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
)

// RenderJitterWindow bounds the deterministic per-request RenderOffset, which jitters each trace's
// COMPLETION time back from the mint instant: a Mint batch's ends spread across this window (ending
// in [Start−RenderJitterWindow, Start]) instead of stacking at the mint instant. ~one fast (master)
// tick wide. (Traces are backdated to completion — see Request.RenderStart.)
const RenderJitterWindow = 5 * time.Second

// renderOffsetFor maps a TraceID to a stable offset in [0, RenderJitterWindow).
// Deterministic so the same request always renders the same waterfall across every
// projection lane.
func renderOffsetFor(traceID string) time.Duration {
	h := fnv.New64a()
	_, _ = h.Write([]byte(traceID))
	return time.Duration(h.Sum64() % uint64(RenderJitterWindow))
}

// defaultRetain bounds how long a minted request stays readable. It must exceed the
// WIDEST consumer window (the Infinity host reads Recent(2h) in the source design);
// 3h leaves margin. This makes ledger size PLATEAU inside the retention window; the
// count cap is only a backstop against a runaway mint rate.
const defaultRetain = 3 * time.Hour

// defaultCap is the hard backstop on retained requests.
const defaultCap = 20000

// Ledger is the per-blueprint time-indexed ring of minted requests: one writer (the
// master clock calling Mint) and many concurrent projection readers.
type Ledger struct {
	eng     *shape.Engine
	cap     int
	retain  time.Duration
	tickSec float64

	mu      sync.RWMutex
	minters []Minter
	ring    []*Request // append-only, time-ordered; trimmed by retain, then cap
	roll    *rollup    // per-minute mint-count rollup; cap-independent, backs WindowStats
}

// New builds a ledger. cap<=0 → 20k backstop; retain<=0 → 3h.
func New(eng *shape.Engine, cap int, retain time.Duration) *Ledger {
	if cap <= 0 {
		cap = defaultCap
	}
	if retain <= 0 {
		retain = defaultRetain
	}
	return &Ledger{eng: eng, cap: cap, retain: retain, tickSec: ReferenceTickSeconds, ring: make([]*Request, 0, 1024), roll: newRollup(retain)}
}

// AddMinter registers a workload instance's minter (composition-root wiring, before
// the clock starts).
func (l *Ledger) AddMinter(m Minter) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.minters = append(l.minters, m)
}

// SetTickSeconds tells the ledger the master-tick period so minters can keep total
// request volume cadence-invariant. Called once at startup by the master clock.
func (l *Ledger) SetTickSeconds(s float64) {
	if s > 0 {
		l.tickSec = s
	}
}

// Mint collects this tick's batch from every registered minter, stamps the
// deterministic RenderOffset, appends to the ring, trims, and RETURNS the fresh batch
// so the master clock can hand it directly to ProjectBatch lanes (exactly-once by
// construction — no Active-window phase race).
func (l *Ledger) Mint(now time.Time) []*Request {
	l.mu.RLock()
	minters := l.minters
	tickSec := l.tickSec
	l.mu.RUnlock()

	var batch []*Request
	for _, m := range minters {
		batch = append(batch, m.Mint(now, tickSec, l.eng)...)
	}
	for _, r := range batch {
		r.RenderOffset = renderOffsetFor(r.TraceID)
	}
	if len(batch) == 0 {
		return batch
	}

	l.mu.Lock()
	l.roll.add(now, batch) // cap-independent count rollup (inside the same critical section)
	l.ring = append(l.ring, batch...)
	// Primary trim: the ring is time-ordered (Mint is called with non-decreasing now),
	// so stale entries are a contiguous prefix.
	cutoff := now.Add(-l.retain)
	drop := 0
	for drop < len(l.ring) && !l.ring[drop].Start.After(cutoff) {
		drop++
	}
	if drop > 0 {
		l.ring = l.ring[drop:]
	}
	if over := len(l.ring) - l.cap; over > 0 {
		l.ring = l.ring[over:] // backstop: bounded memory under any mint rate
	}
	l.mu.Unlock()
	return batch
}

// Active returns requests whose Start falls within (now-window, now] —
// newest-inclusive, oldest-exclusive: the slice a metric lane ticking on `window`
// cadence should project exactly once. Returned pointers are read-only to callers.
func (l *Ledger) Active(now time.Time, window time.Duration) []*Request {
	return l.active(now, window, "")
}

// ActiveFor is Active filtered to one workload instance's requests.
func (l *Ledger) ActiveFor(workload string, now time.Time, window time.Duration) []*Request {
	return l.active(now, window, workload)
}

func (l *Ledger) active(now time.Time, window time.Duration, workload string) []*Request {
	from := now.Add(-window)
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []*Request
	for _, r := range l.ring {
		if r.Start.After(from) && !r.Start.After(now) && (workload == "" || r.Workload == workload) {
			out = append(out, r)
		}
	}
	return out
}

// Recent returns the newest requests within window (the Infinity/JSON host surface).
func (l *Ledger) Recent(now time.Time, window time.Duration) []*Request {
	return l.active(now, window, "")
}

// Len reports the number of retained requests (diagnostics / -dump).
func (l *Ledger) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.ring)
}

// WindowStats returns cap-independent aggregates (total minted + distinct workloads) over
// (now-window, now]. Unlike len(Recent(window)), it is not distorted when the request ring is
// cap-trimmed below the minted volume — the JSON host's count surfaces read this. window must be
// <= the ledger's retain (the rollup only spans the retention horizon); a larger window silently
// returns only the most-recent retain of data.
func (l *Ledger) WindowStats(now time.Time, window time.Duration) WindowStats {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.roll.stats(now, window)
}

// StochasticRound rounds expected to an int, carrying the fractional part as a
// probability so long-run means converge (0.3 → 1 with p=0.3, else 0). u is a uniform
// [0,1) draw. Exported for Minter implementations.
func StochasticRound(expected, u float64) int {
	if expected <= 0 {
		return 0
	}
	n := int(expected)
	if u < (expected - float64(n)) {
		n++
	}
	return n
}
