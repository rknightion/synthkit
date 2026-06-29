// SPDX-License-Identifier: AGPL-3.0-only

package promrw

import (
	"sort"

	"github.com/rknightion/synthkit/internal/sink/promrw/writev2"
)

// interner builds the RW2 symbols table: a de-duplicated string array referenced by
// uint32 index. Index 0 is ALWAYS the empty string (spec requirement).
type interner struct {
	idx  map[string]uint32
	syms []string
}

func newInterner() *interner {
	return &interner{idx: map[string]uint32{"": 0}, syms: []string{""}}
}

func (in *interner) intern(s string) uint32 {
	if i, ok := in.idx[s]; ok {
		return i
	}
	i := uint32(len(in.syms))
	in.idx[s] = i
	in.syms = append(in.syms, s)
	return i
}

func (in *interner) symbols() []string { return in.syms }

// encodeRequest builds a v2 Request from a Series batch. The metric name becomes the
// __name__ label; label name/value pairs are interned into the shared symbols table and
// emitted as labels_refs (sorted lexicographically by label NAME, __name__ included).
func encodeRequest(batch []Series) *writev2.Request {
	in := newInterner()
	series := make([]*writev2.TimeSeries, 0, len(batch))
	for _, m := range batch {
		series = append(series, encodeSeries(in, m))
	}
	return &writev2.Request{Symbols: in.symbols(), Timeseries: series}
}

func encodeSeries(in *interner, m Series) *writev2.TimeSeries {
	// collect names so we can sort (__name__ + user labels)
	names := make([]string, 0, len(m.Labels)+1)
	names = append(names, "__name__")
	for k := range m.Labels {
		names = append(names, k)
	}
	sort.Strings(names)

	refs := make([]uint32, 0, len(names)*2)
	for _, name := range names {
		val := m.Labels[name]
		if name == "__name__" {
			val = m.Name
		}
		refs = append(refs, in.intern(name), in.intern(val))
	}

	out := &writev2.TimeSeries{
		LabelsRefs: refs,
		Metadata:   encodeMetadata(in, m),
		Exemplars:  encodeExemplars(in, m),
	}
	if m.Native != nil {
		out.Histograms = []*writev2.Histogram{encodeNativeHistogram(m.Native, m.T.UnixMilli())}
	} else {
		out.Samples = []*writev2.Sample{{Value: m.Value, Timestamp: m.T.UnixMilli()}}
	}
	return out
}

// encodeNativeHistogram converts the sink-level NativeHistogram into the vendored writev2
// proto form. Integer counts (CountInt/ZeroCountInt oneofs); positive buckets only.
// ResetHint is left UNSPECIFIED (0) — synthkit histograms are cumulative and never reset
// mid-process; Mimir auto-detects the process-restart counter reset like any cumulative series.
func encodeNativeHistogram(n *NativeHistogram, tsMillis int64) *writev2.Histogram {
	spans := make([]*writev2.BucketSpan, 0, len(n.PositiveSpans))
	for _, s := range n.PositiveSpans {
		spans = append(spans, &writev2.BucketSpan{Offset: s.Offset, Length: s.Length})
	}
	return &writev2.Histogram{
		Count:          &writev2.Histogram_CountInt{CountInt: n.Count},
		Sum:            n.Sum,
		Schema:         n.Schema,
		ZeroThreshold:  n.ZeroThreshold,
		ZeroCount:      &writev2.Histogram_ZeroCountInt{ZeroCountInt: n.ZeroCount},
		PositiveSpans:  spans,
		PositiveDeltas: n.PositiveDeltas,
		Timestamp:      tsMillis,
	}
}

// encodeMetadata maps the Series instrument Kind to RW2 per-series metadata. help_ref and
// unit_ref are left 0 (= empty symbol) — synthkit does not model HELP/UNIT text. RW2
// per-series metadata is advisory; classic-histogram component series (_bucket/_sum/_count)
// all carry KindHistogram and Mimir tolerates that.
func encodeMetadata(_ *interner, m Series) *writev2.Metadata {
	var t writev2.Metadata_MetricType
	switch m.Kind {
	case KindCounter:
		t = writev2.Metadata_METRIC_TYPE_COUNTER
	case KindHistogram:
		t = writev2.Metadata_METRIC_TYPE_HISTOGRAM
	default: // KindGauge (zero value) and anything else
		t = writev2.Metadata_METRIC_TYPE_GAUGE
	}
	return &writev2.Metadata{Type: t}
}

// encodeExemplars interns each exemplar's labels (sorted lexicographically by name) into
// the shared symbols table and emits labels_refs + value + ms timestamp. Returns nil for a
// series with no exemplars so the absent-dimension contract holds on the wire.
func encodeExemplars(in *interner, m Series) []*writev2.Exemplar {
	if len(m.Exemplars) == 0 {
		return nil
	}
	out := make([]*writev2.Exemplar, 0, len(m.Exemplars))
	for _, e := range m.Exemplars {
		names := make([]string, 0, len(e.Labels))
		for k := range e.Labels {
			names = append(names, k)
		}
		sort.Strings(names)
		refs := make([]uint32, 0, len(names)*2)
		for _, n := range names {
			refs = append(refs, in.intern(n), in.intern(e.Labels[n]))
		}
		out = append(out, &writev2.Exemplar{LabelsRefs: refs, Value: e.Value, Timestamp: e.T.UnixMilli()})
	}
	return out
}
