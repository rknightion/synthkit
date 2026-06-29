// SPDX-License-Identifier: AGPL-3.0-only

package state

import (
	"math"
	"slices"

	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// NativeSchemaSpanMetrics is the exponential schema synthkit uses for the
// metrics-generator-derived span histograms. Schema 3 ⇒ bucket factor 2^(2^-3) =
// 2^(1/8) ≈ 1.0905, the closest standard schema to the Grafana Mimir / Tempo
// metrics-generator default native-histogram bucket factor of 1.1
// (Mimir docs: NativeHistogramBucketFactor: 1.1). Chosen so quantiles match a real
// metrics-generator and so the populated-bucket count over a sub-second–to–10s latency
// range stays well under nativeHistogramMaxBuckets.
const NativeSchemaSpanMetrics int32 = 3

// nativeHistogramMaxBuckets caps the number of distinct populated positive buckets in one
// native histogram. Mirrors the Mimir/client_golang NativeHistogramMaxBucketNumber default
// (100); above this we coarsen the schema (halve resolution) until we fit.
const nativeHistogramMaxBuckets = 100

// nativeZeroThreshold: observations with v <= this land in the zero bucket. synthkit latency
// draws are strictly > 0, so in practice nothing lands there.
const nativeZeroThreshold = 0.0

// nativeBucketIndex returns the positive-bucket index for v > 0 at the given schema.
// Bucket k covers (base^(k-1), base^k] with base = 2^(2^-schema); index = ceil(log2(v) * 2^schema).
func nativeBucketIndex(v float64, schema int32) int {
	scale := float64(int64(1) << uint(schema)) // 2^schema
	return int(math.Ceil(math.Log2(v) * scale))
}

// encodePositiveBuckets converts a sparse map of absolute-bucket-index → count-in-that-bucket
// into RW2 positive spans + delta-encoded counts. Spans group contiguous populated indices; the
// first span's Offset is the absolute index of its first bucket, each later span's Offset is the
// number of empty buckets since the previous span's last bucket. Deltas are a single global
// delta-encoding over the flattened populated-bucket sequence (count[i] - count[i-1]).
func encodePositiveBuckets(counts map[int]int64) ([]promrw.BucketSpan, []int64) {
	if len(counts) == 0 {
		return nil, nil
	}
	idxs := make([]int, 0, len(counts))
	for i := range counts {
		idxs = append(idxs, i)
	}
	slices.Sort(idxs)

	var spans []promrw.BucketSpan
	var deltas []int64
	var prevCount int64
	var prevEnd int // absolute index one past the previous span's last bucket
	i := 0
	first := true
	for i < len(idxs) {
		start := idxs[i]
		j := i
		for j+1 < len(idxs) && idxs[j+1] == idxs[j]+1 {
			j++
		}
		length := j - i + 1
		var offset int
		if first {
			offset = start
			first = false
		} else {
			offset = start - prevEnd
		}
		spans = append(spans, promrw.BucketSpan{Offset: int32(offset), Length: uint32(length)})
		for k := i; k <= j; k++ {
			c := counts[idxs[k]]
			deltas = append(deltas, c-prevCount)
			prevCount = c
		}
		prevEnd = idxs[j] + 1
		i = j + 1
	}
	return spans, deltas
}

// coarsenToLimit reduces native-histogram resolution until the number of distinct populated
// buckets is <= nativeHistogramMaxBuckets. Halving the schema merges each pair of adjacent
// buckets via newIndex = ceil(oldIndex/2) (computed as floor((idx-1)/2)+1) — the Prometheus
// resolution-reduction convention (model/histogram/float_histogram.go: targetIdx=((idx-1)>>1)+1).
// At schema s-1 the base squares, so old bucket k ∈ {2m-1, 2m} nests in new bucket m = ceil(k/2);
// using floor would shift every odd bucket one factor-base step too low (silent ~10% quantile
// skew at schema 3). Preserves total count and keeps quantiles monotonic. Returns the coarsened map
// and the schema actually used (<= the input schema). Schema floor is -4 (Prometheus minimum). Builds a fresh
// map when it coarsens; returns the input map unchanged when already under the cap (the caller
// relies on this: it must NOT mutate the live cumulative-state map). If the schema hits the -4
// floor while still over the cap, it stops and returns a map that may exceed
// nativeHistogramMaxBuckets — the caller must tolerate that edge case.
func coarsenToLimit(counts map[int]int64, schema int32) (map[int]int64, int32) {
	for len(counts) > nativeHistogramMaxBuckets && schema > -4 {
		merged := make(map[int]int64, len(counts))
		for idx, c := range counts {
			merged[int(math.Floor(float64(idx-1)/2.0))+1] += c // ceil(idx/2): Prometheus resolution reduction (float_histogram.go targetIdx=((idx-1)>>1)+1)
		}
		counts = merged
		schema--
	}
	return counts, schema
}
