// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"
	"time"
)

// TestItemStockCompactionKeepsReadings: compacting completed items out of the backing slice never
// changes what the stock reads. Two stocks take the same arrivals (a pinned long-tail item among
// pseudo-random residences, completed in heap order); one is compacted at irregular points. Their
// counts, oldest ages and heap order agree at every step.
func TestItemStockCompactionKeepsReadings(t *testing.T) {
	var plain, packed itemStock
	rng := uint64(1)
	next := func() float64 {
		rng = rng*6364136223846793005 + 1442695040888963407
		return float64(rng>>40) / float64(1<<24)
	}
	both := func(f func(*itemStock)) { f(&plain); f(&packed) }
	both(func(s *itemStock) { s.push(0, 1e18) }) // pinned head
	for i := 1; i <= 5000; i++ {
		res := 1 + 200*next()
		both(func(s *itemStock) { s.push(int64(i)*1e9, s.v+res) })
		both(func(s *itemStock) { s.v += 0.5 })
		both(func(s *itemStock) { s.complete() })
		if i%97 == 0 || i%311 == 0 {
			packed.compact()
		}
		now := time.Unix(int64(i)+10, 0)
		for _, field := range []string{"count", "oldest_age_seconds"} {
			if a, b := plain.read(field, now), packed.read(field, now); a != b {
				t.Fatalf("step %d %s: plain %v, compacted %v", i, field, a, b)
			}
		}
		if len(plain.due) != len(packed.due) {
			t.Fatalf("step %d: heap sizes %d vs %d", i, len(plain.due), len(packed.due))
		}
		for k := range plain.due {
			if plain.item(plain.due[k]).done != packed.item(packed.due[k]).done {
				t.Fatalf("step %d: heap slot %d differs", i, k)
			}
		}
	}
	if len(packed.items) >= len(plain.items) {
		t.Fatalf("compaction did not shrink the backing slice: %d vs %d", len(packed.items), len(plain.items))
	}
}
