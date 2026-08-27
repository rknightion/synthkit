// SPDX-License-Identifier: AGPL-3.0-only

// histogram.go — the one rule every inventory producer uses to fold a classic Prometheus
// histogram's component series into the family they belong to.
//
// A classic histogram travels as three component series: <family>_bucket, <family>_sum and
// <family>_count. An inventory records the FAMILY, never the components. Two producers that
// disagree about what a family IS do not merely mis-count coverage — they manufacture
// instrument, label-key and bucket findings for every histogram family they both cover, and
// those land in the comparator's contradiction class. The rule lives here once so a fourth
// producer picks it up by calling it rather than by deriving it a fourth time.

package inventory

import (
	"strconv"
	"strings"
)

// classicHistogramSuffixes are the three component suffixes. They are mutually exclusive, so
// the match order does not matter.
var classicHistogramSuffixes = []string{"_bucket", "_sum", "_count"}

// BucketBoundLabel is the Prometheus label a classic-histogram bucket series carries. No other
// series kind carries it, which is what makes it the proof a family is a classic histogram, and
// it is exported so a capturing producer tests for the same label this package folds on.
const BucketBoundLabel = "le"

const bucketBoundLabel = BucketBoundLabel

// ClassicHistogramFamily returns the family base name a classic-histogram component series
// name belongs to. The second result reports whether the name carried a component suffix at
// all — it is NOT proof that the family is a histogram. CloudWatch's five-stat expansion emits
// a genuine `<metric>_sum` GAUGE and a `<metric>_sample_count`, so a caller working from real
// metric names must pair this with a ClassicHistogramProof.
func ClassicHistogramFamily(name string) (string, bool) {
	for _, suffix := range classicHistogramSuffixes {
		if base := strings.TrimSuffix(name, suffix); base != name && base != "" {
			return base, true
		}
	}
	return "", false
}

// ClassicHistogramEvidence returns the histogram evidence one classic-histogram component
// series carries: the classic representation, plus the finite bucket bound its `le` label
// names. The +Inf bucket names no finite bound, and a component without `le` carries none.
func ClassicHistogramEvidence(labels map[string]string) *Histogram {
	evidence := &Histogram{Classic: true, BucketBounds: []float64{}, NativeSchemas: []int32{}}
	if bound, ok := classicHistogramBucketBound(labels); ok {
		evidence.BucketBounds = append(evidence.BucketBounds, bound)
	}
	return evidence
}

func classicHistogramBucketBound(labels map[string]string) (float64, bool) {
	value, ok := labels[bucketBoundLabel]
	if !ok || value == "+Inf" {
		return 0, false
	}
	bound, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return bound, true
}

// ClassicHistogramProof records the families a producer has PROVED are classic histograms.
//
// The only proof is an observed <family>_bucket series carrying an `le` label: `le` appears on
// nothing else, and a producer that saw the bucket series saw the whole family. Without a
// proof the components keep their own names, which is what stops the fold from masking a real
// coverage gap — a family synthkit genuinely does not emit is never folded into one it does.
type ClassicHistogramProof map[string]struct{}

// Prove records a family proved by evidence that is not a bucket series — a producer's own
// metadata declaring the family a histogram. It is a declaration by the producer, never an
// inference from the name, and a caller with only a name must use Observe instead.
func (p ClassicHistogramProof) Prove(family string) {
	if family != "" {
		p[family] = struct{}{}
	}
}

// Observe records one raw series. hasBucketBound reports whether it carries an `le` label.
func (p ClassicHistogramProof) Observe(name string, hasBucketBound bool) {
	if !hasBucketBound || !strings.HasSuffix(name, "_bucket") {
		return
	}
	if base := strings.TrimSuffix(name, "_bucket"); base != "" {
		p[base] = struct{}{}
	}
}

// Family returns the proven family a component series name folds into.
func (p ClassicHistogramProof) Family(name string) (string, bool) {
	base, suffixed := ClassicHistogramFamily(name)
	if !suffixed {
		return "", false
	}
	if _, proven := p[base]; !proven {
		return "", false
	}
	return base, true
}

// ProveClassicHistogramsFromSeries proves families from raw Prometheus label sets, each keyed
// by its own `__name__`. This is the read-back producer's entry point.
func ProveClassicHistogramsFromSeries(series []map[string]string) ClassicHistogramProof {
	proof := make(ClassicHistogramProof)
	for _, labels := range series {
		_, hasBound := labels[bucketBoundLabel]
		proof.Observe(labels["__name__"], hasBound)
	}
	return proof
}

// ProveClassicHistogramsFromSchema proves families from metric entries already recorded in an
// inventory. It reads the `le` label KEY, never its values: a corpus document elides label
// values, so key presence is the only evidence that survives being written to disk.
func ProveClassicHistogramsFromSchema(schema Schema) ClassicHistogramProof {
	proof := make(ClassicHistogramProof)
	for _, metric := range schema.Metrics {
		hasBound := false
		for _, label := range metric.Labels {
			if label.Key == bucketBoundLabel {
				hasBound = true
				break
			}
		}
		proof.Observe(metric.Name, hasBound)
	}
	return proof
}

// FoldClassicHistogramMetrics returns schema with every proven classic-histogram component
// entry merged into its family entry, which gains the classic representation the bucket series
// proves. An unproven name is copied through untouched. Use it to bring a document recorded by
// an earlier producer into the canonical family shape; a producer building a fresh inventory
// folds at the series level instead, so the components never become entries.
func FoldClassicHistogramMetrics(schema Schema) Schema {
	return FoldClassicHistogramMetricsWithProof(schema, ProveClassicHistogramsFromSchema(schema))
}

// FoldClassicHistogramMetricsWithProof is FoldClassicHistogramMetrics for a producer that
// already holds its own proof. A streaming capture proves a family from evidence its recorded
// entries no longer show — the producer's own remote-write metadata declaring the family a
// histogram, for one — so re-deriving the proof from the schema would silently discard it.
func FoldClassicHistogramMetricsWithProof(schema Schema, proof ClassicHistogramProof) Schema {
	out := cloneSchema(schema)
	out.Metrics = make([]Metric, 0, len(schema.Metrics))
	for _, metric := range schema.Metrics {
		candidate := cloneMetric(metric)
		if family, folded := proof.Family(metric.Name); folded {
			candidate.Name = family
			if candidate.Histogram == nil {
				// Bounds are deliberately absent: a recorded document elides the `le` values,
				// so the fold can prove the representation but cannot recover the bounds. The
				// comparator treats an empty bound set as absent evidence for that reason.
				candidate.Histogram = &Histogram{Classic: true, BucketBounds: []float64{}, NativeSchemas: []int32{}}
			} else {
				candidate.Histogram.Classic = true
			}
		}
		if i := indexMetric(out.Metrics, candidate.Name); i >= 0 {
			mergeMetric(&out.Metrics[i], candidate)
			continue
		}
		out.Metrics = append(out.Metrics, candidate)
	}
	out.Normalize()
	normalizeElidedValues(&out)
	return out
}
