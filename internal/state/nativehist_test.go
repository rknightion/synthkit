// SPDX-License-Identifier: AGPL-3.0-only

package state

import (
	"math"
	"reflect"
	"testing"

	"github.com/rknightion/synthkit/internal/sink/promrw"
)

func TestNativeBucketIndex(t *testing.T) {
	// schema 3: index(v) = ceil(log2(v) * 8). Compute expected with the SAME formula
	// and pin it (this guards against accidental drift, not a hand-guess).
	for _, v := range []float64{1.0, 1.0905078, 0.5, 2.0, 0.1, 0.02, 9.9} {
		want := int(math.Ceil(math.Log2(v) * 8))
		if got := nativeBucketIndex(v, 3); got != want {
			t.Errorf("nativeBucketIndex(%v,3) = %d, want %d", v, got, want)
		}
	}

	// Hand-pinned expected values: asserts the FORMULA itself, not just constant drift.
	// 1.0 → ceil(log2(1)*8)=0; 0.5 → ceil(-1*8)=-8; 2.0 → ceil(1*8)=8.
	for _, tc := range []struct {
		v    float64
		want int
	}{
		{1.0, 0},
		{0.5, -8},
		{2.0, 8},
	} {
		if got := nativeBucketIndex(tc.v, 3); got != tc.want {
			t.Errorf("nativeBucketIndex(%v,3) = %d, want %d (hand-pinned)", tc.v, got, tc.want)
		}
	}
}

// decodeSpans reconstructs absolute-index → count from positive spans + delta-encoded counts,
// applying the Prometheus decoding convention (first span Offset absolute; later Offset is the
// empty-bucket gap since the previous span's end; deltas are global delta-encoding over the
// flattened populated-bucket sequence).
func decodeSpans(n promrw.NativeHistogram) map[int]int64 {
	out := map[int]int64{}
	idx := 0
	var cur int64
	di := 0
	for si, sp := range n.PositiveSpans {
		if si == 0 {
			idx = int(sp.Offset)
		} else {
			idx += int(sp.Offset)
		}
		for j := 0; j < int(sp.Length); j++ {
			cur += n.PositiveDeltas[di]
			di++
			out[idx] = cur
			idx++
		}
	}
	return out
}

func TestEncodePositiveBucketsRoundTrip(t *testing.T) {
	counts := map[int]int64{3: 2, 4: 5, 5: 1, 9: 4} // total 12, with a gap between 5 and 9
	spans, deltas := encodePositiveBuckets(counts)
	got := decodeSpans(promrw.NativeHistogram{PositiveSpans: spans, PositiveDeltas: deltas})
	if !reflect.DeepEqual(got, counts) {
		t.Fatalf("round-trip mismatch: want %v got %v (spans=%+v deltas=%v)", counts, got, spans, deltas)
	}
	want := []promrw.BucketSpan{{Offset: 3, Length: 3}, {Offset: 3, Length: 1}}
	if !reflect.DeepEqual(spans, want) {
		t.Errorf("spans: want %+v got %+v", want, spans)
	}
	if !reflect.DeepEqual(deltas, []int64{2, 3, -4, 3}) {
		t.Errorf("deltas: want [2 3 -4 3] got %v", deltas)
	}
}

func TestCoarsenToLimit(t *testing.T) {
	counts := map[int]int64{}
	for i := 0; i < 150; i++ {
		counts[i] = 1
	}
	got, schema := coarsenToLimit(counts, 3)
	if len(got) > nativeHistogramMaxBuckets {
		t.Errorf("after coarsen: %d buckets > cap %d", len(got), nativeHistogramMaxBuckets)
	}
	if schema >= 3 {
		t.Errorf("schema should have been reduced from 3, got %d", schema)
	}
	var sum int64
	for _, c := range got {
		sum += c
	}
	if sum != 150 {
		t.Errorf("coarsen must preserve total count: want 150 got %d", sum)
	}
}

// TestCoarsenToLimitNegativeIndices mirrors TestCoarsenToLimit over NEGATIVE indices —
// these exercise the ceil(idx/2) merge path on negatives. The count/cap/schema invariants
// hold under any 2-into-1 merge, so they're a weaker guard; TestCoarsenMergeNegativeIndices
// below pins the exact ceil-merged cell values.
func TestCoarsenToLimitNegativeIndices(t *testing.T) {
	counts := map[int]int64{}
	for i := -150; i < 0; i++ {
		counts[i] = 1
	}
	got, schema := coarsenToLimit(counts, 3)
	if len(got) > nativeHistogramMaxBuckets {
		t.Errorf("after coarsen: %d buckets > cap %d", len(got), nativeHistogramMaxBuckets)
	}
	if schema >= 3 {
		t.Errorf("schema should have been reduced from 3, got %d", schema)
	}
	var sum int64
	for _, c := range got {
		sum += c
	}
	if sum != 150 {
		t.Errorf("coarsen must preserve total count: want 150 got %d", sum)
	}
}

// TestCoarsenMergeNegativeIndices pins the EXACT ceil(idx/2) merge result on negative indices,
// so the test fails if the Prometheus resolution-reduction convention is ever broken (e.g. by a
// floor(idx/2) regression). The cluster {-8:10,-7:20,-6:15,-5:5,-4:3} ceil-merges
// (ceil(-8/2)=-4, ceil(-7/2)=-3, ceil(-6/2)=-3, ceil(-5/2)=-2, ceil(-4/2)=-2) to
// {-4:10,-3:35,-2:8} (total preserved = 53). The OLD wrong floor(idx/2) would instead give
// {-4:30,-3:20,-2:3}, so these assertions discriminate ceil from floor. Padded with 100 disjoint
// high-negative indices so the map exceeds the cap and triggers exactly one coarsen pass; those
// ceil-merge among themselves to -500..-450 (disjoint from -4..-2), leaving the cluster verifiable.
func TestCoarsenMergeNegativeIndices(t *testing.T) {
	counts := map[int]int64{-8: 10, -7: 20, -6: 15, -5: 5, -4: 3}
	for i := -1000; i < -900; i++ { // 100 padding buckets → 105 total > cap
		counts[i] = 7
	}
	got, schema := coarsenToLimit(counts, 3)
	if schema != 2 {
		t.Errorf("expected exactly one coarsen pass (schema 3→2), got schema %d", schema)
	}
	// ceil(idx/2)-merged cluster cells. The old floor(idx/2) put -7→-4 and -5→-3, giving
	// {-4:30,-3:20,-2:3} — these assertions catch that (now-fixed) regression.
	for idx, want := range map[int]int64{-4: 10, -3: 35, -2: 8} {
		if got[idx] != want {
			t.Errorf("ceil-merge cell %d: got %d want %d (full=%v)", idx, got[idx], want, got)
		}
	}
	var sum int64
	for _, c := range got {
		sum += c
	}
	if want := int64(10 + 20 + 15 + 5 + 3 + 100*7); sum != want {
		t.Errorf("coarsen must preserve total count: want %d got %d", want, sum)
	}
}
