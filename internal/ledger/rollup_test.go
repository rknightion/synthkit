// SPDX-License-Identifier: AGPL-3.0-only

package ledger

import (
	"testing"
	"time"
)

// TestWindowStatsCountsAndWorkloads: WindowStats reports total requests minted in the window plus
// the distinct workload instances seen.
func TestWindowStatsCountsAndWorkloads(t *testing.T) {
	l := New(eng(), 0, 0)
	l.AddMinter(&fakeMinter{name: "api", perTick: 3})
	l.AddMinter(&fakeMinter{name: "web", perTick: 2})

	t0 := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	l.Mint(t0)
	l.Mint(t0.Add(time.Minute))

	st := l.WindowStats(t0.Add(time.Minute), time.Hour)
	if st.Count != 10 {
		t.Fatalf("WindowStats.Count = %d, want 10 (5/tick × 2 ticks)", st.Count)
	}
	if len(st.Workloads) != 2 || st.Workloads[0] != "api" || st.Workloads[1] != "web" {
		t.Fatalf("WindowStats.Workloads = %v, want [api web] (distinct, sorted)", st.Workloads)
	}
}

// TestWindowStatsExactUnderCapTrim is the whole reason the rollup exists: the count must be
// INDEPENDENT of the request-retention cap. Even when the ring is cap-trimmed far below the
// minted volume, WindowStats reports the true total minted within the window.
func TestWindowStatsExactUnderCapTrim(t *testing.T) {
	l := New(eng(), 5, 0) // tiny cap: the request ring holds at most 5 requests
	l.AddMinter(&fakeMinter{name: "api", perTick: 10})

	t0 := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	for i := range 6 {
		l.Mint(t0.Add(time.Duration(i) * time.Minute))
	}
	now := t0.Add(5 * time.Minute)

	if l.Len() > 5 {
		t.Fatalf("ring not cap-trimmed: Len=%d, want <=5", l.Len())
	}
	st := l.WindowStats(now, time.Hour)
	if st.Count != 60 {
		t.Fatalf("WindowStats.Count = %d, want 60 (10/tick × 6 ticks) — must survive cap trim", st.Count)
	}
}

// TestWindowStatsExcludesOutsideWindow: buckets older than the window are not counted, even though
// they are still inside the rollup's retention horizon.
func TestWindowStatsExcludesOutsideWindow(t *testing.T) {
	l := New(eng(), 0, 0) // 3h retain → rollup horizon covers 2h-old bucket
	l.AddMinter(&fakeMinter{name: "api", perTick: 4})

	t0 := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	l.Mint(t0)                    // 2h before now — outside the 1h window
	l.Mint(t0.Add(2 * time.Hour)) // at now

	st := l.WindowStats(t0.Add(2*time.Hour), time.Hour)
	if st.Count != 4 {
		t.Fatalf("WindowStats.Count = %d, want 4 (only the in-window tick)", st.Count)
	}
}
