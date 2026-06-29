// SPDX-License-Identifier: AGPL-3.0-only

package ledger

import (
	"sort"
	"time"
)

// WindowStats is an aggregate over the requests minted within a time window. It is derived from a
// bounded per-minute rollup that is INDEPENDENT of the request-retention cap, so the counts stay
// EXACT even when the request ring has been cap-trimmed far below the minted volume. This is what
// the Infinity/JSON host surface reads for "requests in the last hour"-style counts: at high mint
// rates a len(Recent(1h)) over the ring under-reports by the cap ratio, whereas WindowStats does
// not (the ring only retains the newest `cap` requests; the rollup retains every minute's count).
type WindowStats struct {
	Count     int      // total requests minted within the window
	Workloads []string // distinct workload instance names seen within the window (sorted)
}

// minuteBucket accumulates one wall-clock minute of mint activity. minute is the unix-minute it
// represents; the ring slot is reused every len(buckets) minutes, so minute disambiguates a live
// bucket from a stale wrapped one (a never-written bucket has workloads==nil).
type minuteBucket struct {
	minute    int64
	count     int
	workloads map[string]struct{}
}

// rollup is a fixed-size ring of per-minute buckets covering the ledger's retention horizon. Memory
// is O(horizon × distinct-workloads-per-minute) — independent of mint RATE — so it holds exact
// aggregates even at the high RPS where the request ring is cap-trimmed.
type rollup struct {
	buckets []minuteBucket
}

// newRollup sizes the ring to cover `retain` (one bucket per minute, +1 slack). retain is the
// already-normalised ledger retention.
func newRollup(retain time.Duration) *rollup {
	n := max(int(retain/time.Minute)+1, 1)
	return &rollup{buckets: make([]minuteBucket, n)}
}

// add records a mint batch stamped at now. The caller holds the ledger write lock (add runs inside
// Mint's critical section, alongside the ring append).
func (r *rollup) add(now time.Time, batch []*Request) {
	if len(batch) == 0 {
		return
	}
	min := now.Unix() / 60
	b := &r.buckets[int(min%int64(len(r.buckets)))]
	if b.minute != min || b.workloads == nil {
		b.minute = min
		b.count = 0
		b.workloads = make(map[string]struct{})
	}
	b.count += len(batch)
	for _, req := range batch {
		b.workloads[req.Workload] = struct{}{}
	}
}

// stats aggregates buckets whose minute falls within [now-window, now]. Minute granularity makes the
// window edges approximate by up to a minute — acceptable for the count/presence KPIs it backs. The
// caller holds the read lock.
func (r *rollup) stats(now time.Time, window time.Duration) WindowStats {
	nowMin := now.Unix() / 60
	fromMin := now.Add(-window).Unix() / 60
	st := WindowStats{}
	seen := map[string]struct{}{}
	for i := range r.buckets {
		b := &r.buckets[i]
		if b.workloads == nil || b.minute < fromMin || b.minute > nowMin {
			continue
		}
		st.Count += b.count
		for w := range b.workloads {
			seen[w] = struct{}{}
		}
	}
	for w := range seen {
		st.Workloads = append(st.Workloads, w)
	}
	sort.Strings(st.Workloads)
	return st
}
