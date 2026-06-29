// SPDX-License-Identifier: AGPL-3.0-only

// Package promrw pushes synthetic series to a Prometheus remote_write endpoint (Mimir).
// ALL metrics travel this sink with FINAL pre-mangled names — the OTel metrics SDK is
// deliberately excluded (export-time timestamps, cumulative/delta hazards, Views-only
// bucket control). See ARCHITECTURE.md "Sinks" + signals/.
//
// types.go is part of the Phase-0 frozen seam (single owner: the wiring pass).
// The sink implementation lives in promrw.go (Phase-1 lane).
package promrw

import "time"

// Series is one sample for one fully-labelled series. Name becomes the __name__ label.
//
// ALIASING CONTRACT: state.Collect returns Series whose Labels alias live cumulative-state
// maps. Anything that mutates labels (the blueprint-selector stamping writer, tests) MUST
// clone the map first — never mutate in place.
type Series struct {
	Name      string
	Labels    map[string]string
	Value     float64
	T         time.Time
	Kind      Kind       // instrument type (set by state.Collect); zero value KindGauge
	Exemplars []Exemplar // optional; nil for series without exemplars. See Exemplar.
	// Native, when non-nil, makes this Series a native histogram: the encoder emits a
	// writev2 Histogram on TimeSeries.Histograms and omits the float Sample. Value is
	// ignored in that case. When set, Kind should be KindHistogram.
	Native *NativeHistogram
}

// Exemplar is an optional sample-linked exemplar (RW2). Labels typically carry the
// linking trace_id (best practice per the RW2 spec). Exemplars are FRESHLY ALLOCATED per
// Collect — unlike Series.Labels they never alias cumulative state, so they need no clone.
type Exemplar struct {
	Labels map[string]string
	Value  float64
	T      time.Time
}

// Kind classifies a series' instrument type so downstream tooling (the dashboard
// generator) picks the correct query form — rate() vs raw vs histogram_quantile — instead
// of guessing from the metric name. Stamped by state.Collect from the Add/Set/Observe
// origin: Add ⇒ counter (incl. cumulative _sum/_count summaries), Set ⇒ gauge, Observe ⇒
// histogram. The zero value is KindGauge — the conservative default for any Series not
// built via state. This field is NEVER read on the synthetic-emit path; it exists only for
// the dry-run inventory the dashboard generator reads (the OTel-SDK ban is unaffected).
type Kind uint8

const (
	KindGauge Kind = iota
	KindCounter
	KindHistogram
)

// NativeHistogram is a fully-encoded Prometheus native (exponential) histogram for one
// series at one timestamp, in cumulative integer-count form. When a Series carries a
// non-nil Native, the encoder emits a writev2 Histogram (TimeSeries.Histograms) INSTEAD of
// a float Sample. Built by the state layer via internal/state/nativehist.go. All counts are
// cumulative since process start (like every other synthkit series). Negative buckets are
// unused — synthkit's native histograms model non-negative latencies/durations only.
type NativeHistogram struct {
	Schema         int32        // exponential schema (bucket factor = 2^(2^-Schema))
	Count          uint64       // total observations (cumulative)
	Sum            float64      // sum of observations (cumulative)
	ZeroThreshold  float64      // breadth of the zero bucket
	ZeroCount      uint64       // observations in (−ZeroThreshold, +ZeroThreshold)
	PositiveSpans  []BucketSpan // sparse span layout of populated positive buckets
	PositiveDeltas []int64      // delta-encoded per-bucket counts, in span order
}

// BucketSpan mirrors writev2.BucketSpan: a run of Length consecutive populated buckets
// starting Offset buckets after the previous span's end (the first span's Offset is the
// absolute index of its first bucket).
type BucketSpan struct {
	Offset int32
	Length uint32
}
