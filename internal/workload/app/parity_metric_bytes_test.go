// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// The original complete-series sort plus array serializer is deliberately the
// oracle, not a projection of fields or a parse/re-encode of the candidate bytes.
func TestCanonicalMetricJSONMatchesLegacy(t *testing.T) {
	at := time.Date(2026, 9, 27, 9, 30, 0, 123456789, time.FixedZone("offset", -7*3600))
	rich := promrw.Series{
		Name: "prefix", Labels: map[string]string{"z": "<>&\u2028\u2029\"\\\n", "a": "alpha"},
		Value: 1.25, T: at, Kind: promrw.KindHistogram,
		Producer: "producer", ProducerAllowListVersion: "version", ProducerAllowListVariant: "variant",
		Exemplars: []promrw.Exemplar{{Labels: map[string]string{"trace_id": "trace", "span_id": "span"}, Value: math.SmallestNonzeroFloat64, T: at.Add(time.Nanosecond)}},
		Native: &promrw.NativeHistogram{Schema: -4, Count: math.MaxUint64, Sum: math.MaxFloat64, ZeroThreshold: math.SmallestNonzeroFloat64, ZeroCount: 2,
			PositiveSpans: []promrw.BucketSpan{{Offset: -3, Length: 2}, {Offset: 1, Length: 1}}, PositiveDeltas: []int64{3, -2, 1}},
	}
	empty := promrw.Series{Name: "prefix", Labels: map[string]string{}, Exemplars: []promrw.Exemplar{},
		Native: &promrw.NativeHistogram{PositiveSpans: []promrw.BucketSpan{}, PositiveDeltas: []int64{}}}
	reordered := rich
	reordered.Labels = make(map[string]string)
	reordered.Labels["a"] = "alpha"
	reordered.Labels["z"] = rich.Labels["z"]
	for _, tc := range []struct {
		name  string
		batch []promrw.Series
	}{
		{"nil", nil},
		{"empty", []promrw.Series{}},
		{"complete_duplicates_and_prefixes", []promrw.Series{rich, {Name: "prefix-long", Value: math.Copysign(0, -1)}, empty, rich, {Name: "prefix", Value: -math.SmallestNonzeroFloat64}, reordered, {Name: "prefix", Value: math.MaxFloat64, T: time.Time{}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := parityJSON(t, sortedMetricBatch(t, tc.batch))
			got := canonicalMetricJSON(t, tc.batch)
			if !bytes.Equal(got, want) {
				t.Fatalf("canonical metric raw bytes changed:\n got: %s\nwant: %s", got, want)
			}
		})
	}
}
