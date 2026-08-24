// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDiffMissingAndExtraMetricDispositions(t *testing.T) {
	tests := []struct {
		name        string
		synth       Schema
		reality     Schema
		kind        FindingKind
		disposition Disposition
		signal      string
		synthVals   []string
		realityVals []string
	}{
		{
			name:        "synth metric absent from reality is contradiction",
			synth:       metricSchema(Metric{Name: "synth_only"}),
			reality:     New(),
			kind:        KindMissingMetric,
			disposition: DispositionContradiction,
			signal:      "synth_only",
			synthVals:   []string{"synth_only"},
			realityVals: []string{},
		},
		{
			name:        "reality metric absent from synth is coverage gap",
			synth:       New(),
			reality:     metricSchema(Metric{Name: "reality_only"}),
			kind:        KindExtraMetric,
			disposition: DispositionCoverageGap,
			signal:      "reality_only",
			synthVals:   []string{},
			realityVals: []string{"reality_only"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings := Diff(test.synth, test.reality)
			if len(findings) != 1 {
				t.Fatalf("findings=%+v, want one finding", findings)
			}
			got := findings[0]
			if got.Kind != test.kind || got.Disposition != test.disposition || got.Signal != test.signal {
				t.Fatalf("finding=%+v, want kind=%q disposition=%q signal=%q", got, test.kind, test.disposition, test.signal)
			}
			if !reflect.DeepEqual(got.SynthValues, test.synthVals) || !reflect.DeepEqual(got.RealityValues, test.realityVals) {
				t.Fatalf("values=%+v/%+v, want %+v/%+v", got.SynthValues, got.RealityValues, test.synthVals, test.realityVals)
			}
		})
	}
}

func TestDiffUnexpectedLabelKeyDispositions(t *testing.T) {
	for _, test := range []struct {
		name          string
		synthLabels   []Attribute
		realityLabels []Attribute
		disposition   Disposition
		synthVals     []string
		realityVals   []string
	}{
		{
			name:          "synth-only label key is contradiction",
			synthLabels:   []Attribute{{Key: "deployment", Values: []string{"prod"}}},
			realityLabels: []Attribute{},
			disposition:   DispositionContradiction,
			synthVals:     []string{"deployment"},
			realityVals:   []string{},
		},
		{
			name:          "reality-only label key is coverage gap",
			synthLabels:   []Attribute{},
			realityLabels: []Attribute{{Key: "deployment", Values: []string{"prod"}}},
			disposition:   DispositionCoverageGap,
			synthVals:     []string{},
			realityVals:   []string{"deployment"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			findings := Diff(metricSchema(Metric{Name: "up", Labels: test.synthLabels}), metricSchema(Metric{Name: "up", Labels: test.realityLabels}))
			if len(findings) != 1 {
				t.Fatalf("findings=%+v, want one finding", findings)
			}
			got := findings[0]
			if got.Kind != KindUnexpectedLabelKey || got.Disposition != test.disposition || got.Signal != "up" || got.Field != "labels" {
				t.Fatalf("finding=%+v", got)
			}
			if !reflect.DeepEqual(got.SynthValues, test.synthVals) || !reflect.DeepEqual(got.RealityValues, test.realityVals) {
				t.Fatalf("values=%+v/%+v, want %+v/%+v", got.SynthValues, got.RealityValues, test.synthVals, test.realityVals)
			}
		})
	}
}

func TestDiffLabelValuesReportsBothDirections(t *testing.T) {
	synth := metricSchema(Metric{
		Name:   "requests_total",
		Labels: []Attribute{{Key: "environment", Values: []string{"prod"}}},
	})
	reality := metricSchema(Metric{
		Name:   "requests_total",
		Labels: []Attribute{{Key: "environment", Values: []string{"staging"}}},
	})

	findings := Diff(synth, reality)
	if len(findings) != 2 {
		t.Fatalf("findings=%+v, want contradiction and coverage gap", findings)
	}
	if findings[0].Disposition != DispositionContradiction || findings[1].Disposition != DispositionCoverageGap {
		t.Fatalf("dispositions=%q/%q, want contradiction/coverage_gap", findings[0].Disposition, findings[1].Disposition)
	}
	for _, finding := range findings {
		if finding.Kind != KindLabelValueContradiction || finding.Signal != "requests_total" || finding.Field != "labels.environment" {
			t.Fatalf("finding=%+v", finding)
		}
		if !reflect.DeepEqual(finding.SynthValues, []string{"prod"}) || !reflect.DeepEqual(finding.RealityValues, []string{"staging"}) {
			t.Fatalf("values=%+v/%+v", finding.SynthValues, finding.RealityValues)
		}
	}
}

func TestDiffInstrumentAndBucketBoundDispositions(t *testing.T) {
	synth := metricSchema(Metric{
		Name:            "latency_seconds",
		InstrumentTypes: []string{"gauge"},
		Histogram:       &Histogram{Classic: true, BucketBounds: []float64{0.5}},
	})
	reality := metricSchema(Metric{
		Name:            "latency_seconds",
		InstrumentTypes: []string{},
		Histogram:       &Histogram{Classic: true, BucketBounds: []float64{1}},
	})

	findings := Diff(synth, reality)
	assertFinding(t, findings, KindInstrumentMismatch, DispositionContradiction, "latency_seconds", "instrument_types")
	assertFinding(t, findings, KindBucketBoundMismatch, DispositionContradiction, "latency_seconds", "histogram.bucket_bounds")
	assertFinding(t, findings, KindBucketBoundMismatch, DispositionCoverageGap, "latency_seconds", "histogram.bucket_bounds")
}

func TestDiffHistogramRepresentationMismatchIsInstrumentMismatch(t *testing.T) {
	synth := metricSchema(Metric{
		Name:            "span_latency",
		InstrumentTypes: []string{"histogram"},
		Histogram:       &Histogram{Classic: true, Native: true, BucketBounds: []float64{0.5}},
	})
	reality := metricSchema(Metric{
		Name:            "span_latency",
		InstrumentTypes: []string{"histogram"},
		Histogram:       &Histogram{Classic: true, BucketBounds: []float64{0.5}},
	})

	findings := Diff(synth, reality)
	if len(findings) != 1 {
		t.Fatalf("findings=%+v, want one representation finding", findings)
	}
	got := findings[0]
	if got.Kind != KindInstrumentMismatch || got.Disposition != DispositionContradiction || got.Field != "histogram.representations" {
		t.Fatalf("finding=%+v", got)
	}
	if !reflect.DeepEqual(got.SynthValues, []string{"classic", "native"}) || !reflect.DeepEqual(got.RealityValues, []string{"classic"}) {
		t.Fatalf("values=%+v/%+v", got.SynthValues, got.RealityValues)
	}
}

func TestDiffHistogramNativeSchemaMismatchIsInstrumentMismatch(t *testing.T) {
	synth := metricSchema(Metric{
		Name:            "span_latency",
		InstrumentTypes: []string{"histogram"},
		Histogram:       &Histogram{Native: true, NativeSchemas: []int32{3}},
	})
	reality := metricSchema(Metric{
		Name:            "span_latency",
		InstrumentTypes: []string{"histogram"},
		Histogram:       &Histogram{Native: true, NativeSchemas: []int32{4}},
	})

	findings := Diff(synth, reality)
	assertFinding(t, findings, KindInstrumentMismatch, DispositionContradiction, "span_latency", "histogram.native_schemas")
	assertFinding(t, findings, KindInstrumentMismatch, DispositionCoverageGap, "span_latency", "histogram.native_schemas")
}

func TestDiffHonorsElidedLabelValues(t *testing.T) {
	synth := metricSchema(Metric{
		Name:   "kube_pod_labels",
		Labels: []Attribute{{Key: "label_team", Values: []string{"team-a"}, ValuesElided: true}},
	})
	reality := metricSchema(Metric{
		Name:   "kube_pod_labels",
		Labels: []Attribute{{Key: "label_team", Values: []string{"team-b"}, ValuesElided: true}},
	})

	if findings := Diff(synth, reality); len(findings) != 0 {
		t.Fatalf("findings=%+v, elided sets must not imply either side is absent", findings)
	}
}

func TestDiffIsDeterministicAndReturnsJSONArrays(t *testing.T) {
	synth := Schema{
		Metrics: []Metric{
			{Name: "z_metric", InstrumentTypes: []string{"counter"}},
			{Name: "a_metric", InstrumentTypes: []string{"gauge"}, Labels: []Attribute{{Key: "zone", Values: []string{"b", "a"}}}},
		},
	}
	reality := Schema{
		Metrics: []Metric{
			{Name: "a_metric", InstrumentTypes: []string{"counter"}, Labels: []Attribute{{Key: "zone", Values: []string{"c", "a"}}}},
			{Name: "r_metric"},
		},
	}

	first := Diff(synth, reality)
	second := Diff(synth, reality)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("non-deterministic findings\nfirst=%+v\nsecond=%+v", first, second)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "null" {
		t.Fatalf("findings JSON must be an array: %s", encoded)
	}
	empty, err := json.Marshal(Diff(New(), New()))
	if err != nil {
		t.Fatal(err)
	}
	if string(empty) != "[]" {
		t.Fatalf("empty findings JSON=%s, want []", empty)
	}
}

func metricSchema(metric Metric) Schema {
	schema := New()
	schema.Metrics = []Metric{metric}
	return schema
}

func assertFinding(t *testing.T, findings []Finding, kind FindingKind, disposition Disposition, signal, field string) {
	t.Helper()
	for _, finding := range findings {
		if finding.Kind == kind && finding.Disposition == disposition && finding.Signal == signal && finding.Field == field {
			return
		}
	}
	t.Fatalf("findings=%+v, missing kind=%q disposition=%q signal=%q field=%q", findings, kind, disposition, signal, field)
}
