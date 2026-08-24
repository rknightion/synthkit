// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"math"
	"sort"
	"strconv"
)

// FindingKind identifies the shape of a difference between a synthetic
// inventory and a reality inventory.
type FindingKind string

const (
	KindMissingMetric           FindingKind = "missing_metric"
	KindExtraMetric             FindingKind = "extra_metric"
	KindUnexpectedLabelKey      FindingKind = "unexpected_label_key"
	KindLabelValueContradiction FindingKind = "label_value_contradiction"
	KindInstrumentMismatch      FindingKind = "instrument_mismatch"
	KindBucketBoundMismatch     FindingKind = "bucket_bound_mismatch"

	// The Finding-prefixed spellings are aliases for callers that prefer the
	// full type name in constant references.
	FindingMissingMetric           FindingKind = KindMissingMetric
	FindingExtraMetric             FindingKind = KindExtraMetric
	FindingUnexpectedLabelKey      FindingKind = KindUnexpectedLabelKey
	FindingLabelValueContradiction FindingKind = KindLabelValueContradiction
	FindingInstrumentMismatch      FindingKind = KindInstrumentMismatch
	FindingBucketBoundMismatch     FindingKind = KindBucketBoundMismatch
)

// Disposition says which side of the comparison is unsupported by the other
// side. A contradiction is a synthetic claim absent from reality; a coverage
// gap is a reality shape not represented by synthkit.
type Disposition string

const (
	DispositionContradiction Disposition = "contradiction"
	DispositionCoverageGap   Disposition = "coverage_gap"

	// Short spellings keep the constants convenient while retaining the typed
	// Disposition contract.
	Contradiction Disposition = DispositionContradiction
	CoverageGap   Disposition = DispositionCoverageGap
)

// Finding is one typed difference between a synthetic inventory and a reality
// inventory. Values are normalized and sorted by Diff; empty value lists are
// represented as empty slices rather than nil slices.
type Finding struct {
	Kind          FindingKind `json:"kind"`
	Disposition   Disposition `json:"disposition"`
	Signal        string      `json:"signal"`
	Field         string      `json:"field"`
	SynthValues   []string    `json:"synth_values"`
	RealityValues []string    `json:"reality_values"`
}

// Diff compares the metric portions of synth and reality. The inventory is a
// structural contract for the signal catalogue, so metrics are matched by
// logical family name, independently of their transport. It reports both
// directions when a set has values unique to each side: the synth-only claim is
// a contradiction and the reality-only claim is a coverage gap.
//
// Label values marked ValuesElided are deliberately treated as open-ended. A
// value absent from an elided set cannot prove a contradiction or a coverage
// gap, so Diff reports only differences that remain certain after accounting
// for the cap.
func Diff(synth, reality Schema) []Finding {
	synthMetrics := indexMetrics(synth.Metrics)
	realityMetrics := indexMetrics(reality.Metrics)

	names := make([]string, 0, len(synthMetrics)+len(realityMetrics))
	seenNames := make(map[string]struct{}, len(synthMetrics)+len(realityMetrics))
	for name := range synthMetrics {
		seenNames[name] = struct{}{}
		names = append(names, name)
	}
	for name := range realityMetrics {
		if _, seen := seenNames[name]; !seen {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	out := make([]Finding, 0)
	for _, name := range names {
		synthMetric, inSynth := synthMetrics[name]
		realityMetric, inReality := realityMetrics[name]
		switch {
		case !inReality:
			out = append(out, Finding{
				Kind:          KindMissingMetric,
				Disposition:   DispositionContradiction,
				Signal:        name,
				Field:         "name",
				SynthValues:   []string{name},
				RealityValues: []string{},
			})
		case !inSynth:
			out = append(out, Finding{
				Kind:          KindExtraMetric,
				Disposition:   DispositionCoverageGap,
				Signal:        name,
				Field:         "name",
				SynthValues:   []string{},
				RealityValues: []string{name},
			})
		default:
			diffMetric(&out, synthMetric, realityMetric)
		}
	}

	sortFindings(out)
	return out
}

type metricView struct {
	name        string
	instruments map[string]struct{}
	labels      map[string]attributeView
	histogram   histogramView
}

type attributeView struct {
	values map[string]struct{}
	elided bool
}

type histogramView struct {
	classic       bool
	native        bool
	bounds        map[uint64]float64
	nativeSchemas map[int32]struct{}
}

func indexMetrics(metrics []Metric) map[string]metricView {
	out := make(map[string]metricView, len(metrics))
	for _, metric := range metrics {
		view, ok := out[metric.Name]
		if !ok {
			view = metricView{
				name:        metric.Name,
				instruments: make(map[string]struct{}),
				labels:      make(map[string]attributeView),
			}
		}
		for _, instrument := range metric.InstrumentTypes {
			view.instruments[instrument] = struct{}{}
		}
		for _, label := range metric.Labels {
			attribute, ok := view.labels[label.Key]
			if !ok {
				attribute = attributeView{values: make(map[string]struct{})}
			}
			for _, value := range label.Values {
				attribute.values[value] = struct{}{}
			}
			attribute.elided = attribute.elided || label.ValuesElided
			view.labels[label.Key] = attribute
		}
		if metric.Histogram != nil {
			view.histogram.classic = view.histogram.classic || metric.Histogram.Classic
			view.histogram.native = view.histogram.native || metric.Histogram.Native
			if view.histogram.bounds == nil {
				view.histogram.bounds = make(map[uint64]float64)
			}
			for _, bound := range metric.Histogram.BucketBounds {
				view.histogram.bounds[floatKey(bound)] = canonicalFloat(bound)
			}
			if view.histogram.nativeSchemas == nil {
				view.histogram.nativeSchemas = make(map[int32]struct{})
			}
			for _, schema := range metric.Histogram.NativeSchemas {
				view.histogram.nativeSchemas[schema] = struct{}{}
			}
		}
		out[metric.Name] = view
	}
	return out
}

func diffMetric(out *[]Finding, synth, reality metricView) {
	synthInstruments := sortedStrings(synth.instruments)
	realityInstruments := sortedStrings(reality.instruments)
	appendDirectional(out, KindInstrumentMismatch, synth.name, "instrument_types", synthInstruments, realityInstruments, true, true)
	appendDirectional(
		out,
		KindInstrumentMismatch,
		synth.name,
		"histogram.representations",
		histogramRepresentations(synth.histogram),
		histogramRepresentations(reality.histogram),
		true,
		true,
	)
	appendDirectional(
		out,
		KindInstrumentMismatch,
		synth.name,
		"histogram.native_schemas",
		formatInt32s(synth.histogram.nativeSchemas),
		formatInt32s(reality.histogram.nativeSchemas),
		true,
		true,
	)

	synthKeys := sortedAttributeKeys(synth.labels)
	realityKeys := sortedAttributeKeys(reality.labels)
	appendDirectional(out, KindUnexpectedLabelKey, synth.name, "labels", synthKeys, realityKeys, true, true)

	commonKeys := intersection(synthKeys, realityKeys)
	for _, key := range commonKeys {
		synthAttribute := synth.labels[key]
		realityAttribute := reality.labels[key]
		appendDirectional(
			out,
			KindLabelValueContradiction,
			synth.name,
			"labels."+key,
			sortedStrings(synthAttribute.values),
			sortedStrings(realityAttribute.values),
			!realityAttribute.elided,
			!synthAttribute.elided,
		)
	}

	synthBounds := sortedBounds(synth.histogram.bounds)
	realityBounds := sortedBounds(reality.histogram.bounds)
	appendDirectional(out, KindBucketBoundMismatch, synth.name, "histogram.bucket_bounds", formatBounds(synthBounds), formatBounds(realityBounds), true, true)
}

func histogramRepresentations(histogram histogramView) []string {
	representations := make([]string, 0, 2)
	if histogram.classic {
		representations = append(representations, "classic")
	}
	if histogram.native {
		representations = append(representations, "native")
	}
	return representations
}

// appendDirectional emits one finding for each certain direction of a set
// mismatch. synthCanProveMissing and realityCanProveMissing allow callers to
// suppress a direction when the opposite inventory is explicitly elided.
func appendDirectional(out *[]Finding, kind FindingKind, signal, field string, synthValues, realityValues []string, synthCanProveMissing, realityCanProveMissing bool) {
	synthOnly := difference(synthValues, realityValues)
	realityOnly := difference(realityValues, synthValues)
	if len(synthOnly) > 0 && synthCanProveMissing {
		*out = append(*out, Finding{
			Kind:          kind,
			Disposition:   DispositionContradiction,
			Signal:        signal,
			Field:         field,
			SynthValues:   cloneStrings(synthValues),
			RealityValues: cloneStrings(realityValues),
		})
	}
	if len(realityOnly) > 0 && realityCanProveMissing {
		*out = append(*out, Finding{
			Kind:          kind,
			Disposition:   DispositionCoverageGap,
			Signal:        signal,
			Field:         field,
			SynthValues:   cloneStrings(synthValues),
			RealityValues: cloneStrings(realityValues),
		})
	}
}

func sortedAttributeKeys(labels map[string]attributeView) []string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStrings(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func difference(a, b []string) []string {
	bSet := make(map[string]struct{}, len(b))
	for _, value := range b {
		bSet[value] = struct{}{}
	}
	out := make([]string, 0, len(a))
	for _, value := range a {
		if _, found := bSet[value]; !found {
			out = append(out, value)
		}
	}
	return out
}

func intersection(a, b []string) []string {
	bSet := make(map[string]struct{}, len(b))
	for _, value := range b {
		bSet[value] = struct{}{}
	}
	out := make([]string, 0)
	for _, value := range a {
		if _, found := bSet[value]; found {
			out = append(out, value)
		}
	}
	return out
}

func sortedBounds(bounds map[uint64]float64) []float64 {
	out := make([]float64, 0, len(bounds))
	for _, bound := range bounds {
		out = append(out, bound)
	}
	sort.Slice(out, func(i, j int) bool {
		leftNaN, rightNaN := math.IsNaN(out[i]), math.IsNaN(out[j])
		if leftNaN != rightNaN {
			return leftNaN
		}
		return out[i] < out[j]
	})
	return out
}

func formatBounds(bounds []float64) []string {
	out := make([]string, 0, len(bounds))
	for _, bound := range bounds {
		out = append(out, strconv.FormatFloat(bound, 'g', -1, 64))
	}
	return out
}

func formatInt32s(values map[int32]struct{}) []string {
	ordered := make([]int32, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	out := make([]string, 0, len(ordered))
	for _, value := range ordered {
		out = append(out, strconv.FormatInt(int64(value), 10))
	}
	return out
}

func floatKey(value float64) uint64 {
	return math.Float64bits(canonicalFloat(value))
}

func canonicalFloat(value float64) float64 {
	if value == 0 {
		return 0
	}
	return value
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string{}, values...)
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		left, right := findings[i], findings[j]
		if left.Signal != right.Signal {
			return left.Signal < right.Signal
		}
		if left.Field != right.Field {
			return left.Field < right.Field
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Disposition != right.Disposition {
			return left.Disposition < right.Disposition
		}
		if cmp := compareStrings(left.SynthValues, right.SynthValues); cmp != 0 {
			return cmp < 0
		}
		return compareStrings(left.RealityValues, right.RealityValues) < 0
	})
}

func compareStrings(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}
