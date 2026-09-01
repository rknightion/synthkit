// SPDX-License-Identifier: AGPL-3.0-only

package otlp

import "time"

// Temporality mirrors the OTLP AggregationTemporality enum for Sum/Histogram/ExponentialHistogram.
//
// SYNTHKIT EMITS CUMULATIVE ONLY. Evidence for that choice, rather than inheritance from the
// promrw cumulative rule (ARCHITECTURE I3), which governs a different protocol:
//
//   - The OTLP metrics exporter's spec default is cumulative.
//     OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE recognises Cumulative (DEFAULT), Delta and
//     LowMemory — https://opentelemetry.io/docs/specs/otel/metrics/sdk_exporters/otlp (read
//     2026-08-27). OTEP 0131 states the rationale: "By default, the OTLP exporter is designed to
//     export cumulative values since the start time, as this is generally considered more reliable
//     for ensuring data integrity in the event of transit losses."
//   - The reference collector deployment emits cumulative. Its spanmetrics connector
//     (m7kni/rkps-awsinfra applications/k8s-monitoring/values.yaml, HEAD a888e11 2026-08-26) sets
//     histogram.type: exponential but leaves aggregation_temporality unset, so it takes Alloy's
//     default of CUMULATIVE (grafana/alloy internal/component/otelcol/connector/spanmetrics/
//     spanmetrics.go DefaultArguments, HEAD 2eeaa3e 2026-08-17).
//   - The receiving side treats delta as experimental. Grafana Mimir — which backs the Grafana
//     Cloud OTLP gateway — lists "native ingestion of delta OTLP metrics" under experimental
//     features (docs/sources/mimir/configure/about-versioning.md and the v3.0 release notes),
//     i.e. off unless a tenant opts in; the supported path expects cumulative.
//
// TemporalityDelta is therefore ENCODABLE but UNUSED: it exists so a future construct with real
// delta-exporting evidence can opt in per metric, and so the encoder round-trips the enum
// honestly. No synthkit lane sets it. Whether the Grafana Cloud gateway accepts, converts or
// drops a delta point is NOT verified against a live stack — see cantfind.md SK-91, awaiting a
// live capture. Do not restate the old unverified claim that the gateway drops delta histograms;
// nothing in this repository ever evidenced it.
type Temporality int

const (
	TemporalityCumulative Temporality = iota
	TemporalityDelta
)

// MetricKind selects the OTLP metric data shape. Values are append-only: internal/inventory and
// the e2e receiver switch on them.
//
//	MetricGauge                 → Gauge                 (asynchronous observation; no start time)
//	MetricSum + Monotonic       → Sum,  is_monotonic=true  (Counter → gateway appends _total)
//	MetricSum + !Monotonic      → Sum,  is_monotonic=false (UpDownCounter → gateway gauge)
//	MetricHistogram             → Histogram             (explicit bounds)
//	MetricExponentialHistogram  → ExponentialHistogram  (base-2 exponential buckets)
type MetricKind int

const (
	MetricGauge MetricKind = iota
	MetricSum
	MetricHistogram
	MetricExponentialHistogram
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

// ExponentialBuckets is one sign range of an exponential histogram: BucketCounts[i] is the
// count in absolute bucket index Offset+i, covering (base^(Offset+i), base^(Offset+i+1)] with
// base = 2^(2^-Scale). Unlike the Prometheus native-histogram wire form (sparse spans +
// delta-encoded counts, internal/sink/promrw.NativeHistogram), OTLP carries ONE contiguous
// dense run of ABSOLUTE counts — interior empty buckets are explicit zeroes, not a new span.
type ExponentialBuckets struct {
	Offset       int32
	BucketCounts []uint64
}

// ExponentialHistogramPoint is one base-2 exponential ("native") histogram data point.
//
// Real producer: the OTel Collector / Alloy spanmetrics connector under histogram.type:
// exponential, which the reference k8s-monitoring deployment runs (see Temporality above). Its
// max_size default of 160 buckets per sign range (grafana/alloy spanmetrics types.go
// ExponentialHistogramConfig.SetToDefault, HEAD 2eeaa3e 2026-08-17) is the bucket budget a
// caller should respect; the encoder does not impose it.
//
// Scale is the OTLP scale, numerically identical to the Prometheus native-histogram schema —
// both define base = 2^(2^-n) — so internal/state.NativeSchemaSpanMetrics transfers unchanged.
// Negative is left zero for the non-negative quantities synthkit models (durations, sizes,
// counts); it exists because the OTLP shape has it, not because a lane populates it.
//
// OTLP invariant the CALLER owns: Count == ZeroCount + Σ Positive.BucketCounts +
// Σ Negative.BucketCounts. The encoder writes what it is given rather than reconciling it.
type ExponentialHistogramPoint struct {
	Attrs         map[string]any
	Start         time.Time
	Time          time.Time
	Count         uint64
	Sum           float64
	Scale         int32
	ZeroCount     uint64
	ZeroThreshold float64
	Positive      ExponentialBuckets
	Negative      ExponentialBuckets
	Min, Max      float64
	HasMinMax     bool
}

// Metric is one named metric carrying Numbers (Gauge/Sum), Histograms (explicit-bound) or
// ExpHistograms (exponential). Only the slice matching Kind is read.
type Metric struct {
	Name          string
	Description   string
	Unit          string
	Kind          MetricKind
	Monotonic     bool        // Sum only: true ⇒ gateway makes a Counter (+_total)
	Temporality   Temporality // Sum/Histogram/ExponentialHistogram
	Numbers       []NumberPoint
	Histograms    []HistogramPoint
	ExpHistograms []ExponentialHistogramPoint
}

// MetricResource is one resource's metric block (mirrors trace Resource). Multiple in one
// Write form one multi-resource ExportMetricsServiceRequest. Attrs are resource attributes
// (service.*, k8s.*, …) → target_info + promoted labels at the gateway.
//
// INSTRUMENTATION SCOPE (see signals/otlp-metrics.md "Instrumentation scope on the wire"):
// exactly one ScopeMetrics per resource, carrying the real instrumentation library's name and
// version (zero Scope ⇒ name "synthkit"). Scope ATTRIBUTES are never emitted — no modelled
// producer sets them, and inventing them would fabricate telemetry. Grafana Cloud does not
// surface otel_scope_name/otel_scope_version as Prometheus labels (live-validated 2026-06-18),
// so no synthkit-emitted shape, dashboard or query may depend on scope becoming a label.
type MetricResource struct {
	Attrs map[string]any
	Scope Scope
	// Producer is composition-root inventory metadata. It is deliberately absent
	// from the hand-encoded OTLP request and exists only in dry-run capture.
	Producer string
	Metrics  []Metric
}
