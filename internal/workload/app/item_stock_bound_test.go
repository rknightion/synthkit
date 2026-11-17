// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// TestItemStockBackingSliceBounded: one long-tail item pins the head of the arrival order while
// millions of short residences come and go behind it; the backing slice still holds at most
// MaxStockItems entries, and the stock reads as before (count, and the pinned item's age).
func TestItemStockBackingSliceBounded(t *testing.T) {
	s := &itemStock{}
	s.push(0, 1e18) // the long-tail item: never completes
	const arrivals = 3 * telemetryspec.MaxStockItems
	for i := 1; i <= arrivals; i++ {
		s.push(int64(i), s.v+0.5)
		s.v++
		s.complete()
		if len(s.items) > telemetryspec.MaxStockItems {
			t.Fatalf("after %d arrivals the backing slice holds %d items, above MaxStockItems %d", i, len(s.items), telemetryspec.MaxStockItems)
		}
	}
	if got := s.read(telemetryspec.StockFieldCount, time.Unix(0, 0)); got != 1 {
		t.Fatalf("count %v, want only the long-tail item", got)
	}
	if got := s.read(telemetryspec.StockFieldOldestAge, time.Unix(10, 0)); got != 10 {
		t.Fatalf("oldest age %v, want 10 s (the long-tail item)", got)
	}
}

// TestItemStockBoundedUnderSaturatedChurn: a stock held at the cap by steady churn (a few items
// complete and as many arrive each tick) keeps its backing slice within the cap and loses no
// arrival the cap has room for, so its count stays pinned at MaxStockItems.
func TestItemStockBoundedUnderSaturatedChurn(t *testing.T) {
	s := &itemStock{}
	for i := 0; i < telemetryspec.MaxStockItems; i++ {
		s.push(int64(i), s.v+10+float64(i%50)) // residences 10..59 s
	}
	for tick := 0; tick < 200; tick++ {
		s.v++
		s.complete()
		freed := telemetryspec.MaxStockItems - s.n
		for j := 0; j < freed; j++ {
			s.push(int64(tick), s.v+10+float64(j%50))
		}
		if s.n != telemetryspec.MaxStockItems {
			t.Fatalf("tick %d: count %d, want the cap held by the arrivals the completions freed", tick, s.n)
		}
		if len(s.items) > telemetryspec.MaxStockItems {
			t.Fatalf("tick %d: backing slice holds %d items", tick, len(s.items))
		}
	}
}
