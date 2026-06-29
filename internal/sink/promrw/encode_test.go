// SPDX-License-Identifier: AGPL-3.0-only

package promrw

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/sink/promrw/writev2"
	"google.golang.org/protobuf/proto"
)

// Compile-time guard: BucketSpan must stay field-compatible with writev2.BucketSpan so
// encodeNativeHistogram can copy Offset/Length 1:1. If either field's name or type drifts
// in the regenerated proto, this stops compiling.
var _ = BucketSpan{Offset: writev2.BucketSpan{}.Offset, Length: writev2.BucketSpan{}.Length}

func TestSymbolInternerStartsWithEmptyAndDedups(t *testing.T) {
	in := newInterner()
	if got := in.intern(""); got != 0 {
		t.Fatalf("empty string must intern to index 0, got %d", got)
	}
	a := in.intern("job")
	b := in.intern("api")
	a2 := in.intern("job")
	if a != a2 {
		t.Fatalf("repeated symbol must reuse index: %d vs %d", a, a2)
	}
	if a == b {
		t.Fatalf("distinct symbols must get distinct indices")
	}
	syms := in.symbols()
	if syms[0] != "" {
		t.Fatalf("symbols[0] must be empty string, got %q", syms[0])
	}
	if syms[a] != "job" || syms[b] != "api" {
		t.Fatalf("symbol table mismatch: %v", syms)
	}
}

func TestEncodeSingleSeriesRoundTrips(t *testing.T) {
	ts := time.UnixMilli(1_700_000_000_000)
	batch := []Series{{
		Name:   "http_requests_total",
		Labels: map[string]string{"job": "api", "method": "GET"},
		Value:  42,
		T:      ts,
		Kind:   KindCounter,
	}}

	req := encodeRequest(batch)

	// round-trip through the canonical generated marshaler
	raw, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got writev2.Request
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Timeseries) != 1 {
		t.Fatalf("want 1 series, got %d", len(got.Timeseries))
	}
	s := got.Timeseries[0]
	if len(s.LabelsRefs)%2 != 0 {
		t.Fatalf("labels_refs must be even-length, got %d", len(s.LabelsRefs))
	}
	// resolve labels via the symbol table and assert __name__ + user labels present
	labels := map[string]string{}
	for i := 0; i+1 < len(s.LabelsRefs); i += 2 {
		labels[got.Symbols[s.LabelsRefs[i]]] = got.Symbols[s.LabelsRefs[i+1]]
	}
	if labels["__name__"] != "http_requests_total" || labels["job"] != "api" || labels["method"] != "GET" {
		t.Fatalf("label round-trip mismatch: %v", labels)
	}
	if len(s.Samples) != 1 || s.Samples[0].Value != 42 || s.Samples[0].Timestamp != ts.UnixMilli() {
		t.Fatalf("sample round-trip mismatch: %+v", s.Samples)
	}
}

func TestEncodeSortsLabelNamesLexicographically(t *testing.T) {
	batch := []Series{{
		Name:   "m",
		Labels: map[string]string{"zeta": "1", "alpha": "2"},
		Value:  1,
		T:      time.UnixMilli(1),
		Kind:   KindGauge,
	}}
	req := encodeRequest(batch)
	s := req.Timeseries[0]
	var names []string
	for i := 0; i+1 < len(s.LabelsRefs); i += 2 {
		names = append(names, req.Symbols[s.LabelsRefs[i]])
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("label names must be sorted lexicographically, got %v", names)
	}
	if names[0] != "__name__" {
		t.Fatalf("__name__ sorts first, got %v", names)
	}
}

func TestEncodeStampsMetadataFromKind(t *testing.T) {
	cases := []struct {
		kind Kind
		want writev2.Metadata_MetricType
	}{
		{KindCounter, writev2.Metadata_METRIC_TYPE_COUNTER},
		{KindGauge, writev2.Metadata_METRIC_TYPE_GAUGE},
		{KindHistogram, writev2.Metadata_METRIC_TYPE_HISTOGRAM},
	}
	for _, c := range cases {
		req := encodeRequest([]Series{{Name: "m", Value: 1, T: time.UnixMilli(1), Kind: c.kind}})
		md := req.Timeseries[0].Metadata
		if md == nil || md.Type != c.want {
			t.Fatalf("kind %v: want %v, got %+v", c.kind, c.want, md)
		}
	}
}

func TestEncodeExemplarsRoundTrip(t *testing.T) {
	batch := []Series{{
		Name:   "http_request_duration_seconds_bucket",
		Labels: map[string]string{"le": "0.5", "job": "api"},
		Value:  7, T: time.UnixMilli(1000), Kind: KindHistogram,
		Exemplars: []Exemplar{{
			Labels: map[string]string{"trace_id": "abc123"},
			Value:  0.42,
			T:      time.UnixMilli(950),
		}},
	}}
	req := encodeRequest(batch)
	ex := req.Timeseries[0].Exemplars
	if len(ex) != 1 {
		t.Fatalf("want 1 exemplar, got %d", len(ex))
	}
	if ex[0].Value != 0.42 || ex[0].Timestamp != 950 {
		t.Fatalf("exemplar value/ts mismatch: %+v", ex[0])
	}
	labels := map[string]string{}
	for i := 0; i+1 < len(ex[0].LabelsRefs); i += 2 {
		labels[req.Symbols[ex[0].LabelsRefs[i]]] = req.Symbols[ex[0].LabelsRefs[i+1]]
	}
	if labels["trace_id"] != "abc123" {
		t.Fatalf("exemplar trace_id round-trip mismatch: %v", labels)
	}
}

func TestEncodeNoExemplarsIsNil(t *testing.T) {
	req := encodeRequest([]Series{{Name: "m", Value: 1, T: time.UnixMilli(1)}})
	if req.Timeseries[0].Exemplars != nil {
		t.Fatalf("series without exemplars must encode nil, got %v", req.Timeseries[0].Exemplars)
	}
}

func TestEncodeNativeHistogramSeries(t *testing.T) {
	ts := time.UnixMilli(1_700_000_000_000)
	s := Series{
		Name:   "traces_spanmetrics_latency",
		Labels: map[string]string{"service": "checkout"},
		T:      ts,
		Kind:   KindHistogram,
		Native: &NativeHistogram{
			Schema:         3,
			Count:          7,
			Sum:            1.5,
			ZeroThreshold:  0,
			ZeroCount:      1,
			PositiveSpans:  []BucketSpan{{Offset: -45, Length: 2}, {Offset: 3, Length: 1}},
			PositiveDeltas: []int64{2, 1, 1},
		},
	}
	req := encodeRequest([]Series{s})
	if len(req.Timeseries) != 1 {
		t.Fatalf("want 1 series, got %d", len(req.Timeseries))
	}
	got := req.Timeseries[0]
	if len(got.Samples) != 0 {
		t.Fatalf("native series must carry NO float samples, got %d", len(got.Samples))
	}
	if len(got.Histograms) != 1 {
		t.Fatalf("want 1 histogram, got %d", len(got.Histograms))
	}
	h := got.Histograms[0]
	if h.GetCountInt() != 7 {
		t.Errorf("count: want 7, got %d", h.GetCountInt())
	}
	if h.GetZeroCountInt() != 1 {
		t.Errorf("zero count: want 1, got %d", h.GetZeroCountInt())
	}
	if h.Sum != 1.5 || h.Schema != 3 {
		t.Errorf("sum/schema: got sum=%v schema=%d", h.Sum, h.Schema)
	}
	if h.Timestamp != ts.UnixMilli() {
		t.Errorf("histogram timestamp must be set to the series timestamp, got %d", h.Timestamp)
	}
	if len(h.PositiveSpans) != 2 ||
		h.PositiveSpans[0].Offset != -45 || h.PositiveSpans[0].Length != 2 ||
		h.PositiveSpans[1].Offset != 3 || h.PositiveSpans[1].Length != 1 {
		t.Errorf("spans not faithfully encoded: %+v", h.PositiveSpans)
	}
	if !reflect.DeepEqual(h.PositiveDeltas, []int64{2, 1, 1}) {
		t.Errorf("deltas: want [2 1 1] got %v", h.PositiveDeltas)
	}
	if got.Metadata.GetType() != writev2.Metadata_METRIC_TYPE_HISTOGRAM {
		t.Errorf("metadata type: want HISTOGRAM, got %v", got.Metadata.GetType())
	}
}
