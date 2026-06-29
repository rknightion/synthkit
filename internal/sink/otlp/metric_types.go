// SPDX-License-Identifier: AGPL-3.0-only

package otlp

import "time"

// Temporality mirrors the OTLP AggregationTemporality enum for Sum/Histogram. Cumulative is
// the only fully-safe choice for the Grafana Cloud gateway (delta histograms are dropped);
// synthkit emits cumulative, matching how it pushes promrw totals.
type Temporality int

const (
	TemporalityCumulative Temporality = iota
	TemporalityDelta
)

// MetricKind selects the OTLP metric data shape.
type MetricKind int

const (
	MetricGauge MetricKind = iota
	MetricSum
	MetricHistogram
)

// NumberPoint is one Gauge/Sum data point. Start is the cumulative-series start time
// (omitted for gauges; stable across a Sum's lifetime so the gateway derives correct
// rate()/reset semantics). Attrs are the per-series metric attributes (NOT resource attrs).
type NumberPoint struct {
	Attrs map[string]any
	Start time.Time
	Time  time.Time
	Value float64
}

// HistogramPoint is one explicit-bucket histogram data point. BucketCounts is the count IN
// each bucket (non-cumulative), len == len(Bounds)+1 (the trailing element is the +Inf
// overflow bucket) — the OTLP wire shape, distinct from Prometheus cumulative _bucket series.
type HistogramPoint struct {
	Attrs        map[string]any
	Start        time.Time
	Time         time.Time
	Count        uint64
	Sum          float64
	Bounds       []float64
	BucketCounts []uint64
	Min, Max     float64
	HasMinMax    bool
}

// Metric is one named metric carrying either Numbers (Gauge/Sum) or Histograms.
type Metric struct {
	Name        string
	Description string
	Unit        string
	Kind        MetricKind
	Monotonic   bool        // Sum only: true ⇒ gateway makes a Counter (+_total)
	Temporality Temporality // Sum/Histogram
	Numbers     []NumberPoint
	Histograms  []HistogramPoint
}

// MetricResource is one resource's metric block (mirrors trace Resource). Multiple in one
// Write form one multi-resource ExportMetricsServiceRequest. Attrs are resource attributes
// (service.*, k8s.*, …) → target_info + promoted labels at the gateway.
type MetricResource struct {
	Attrs   map[string]any
	Scope   Scope
	Metrics []Metric
}
