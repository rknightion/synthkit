// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"encoding/json"
	"reflect"
	"strings"
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
		wantCount   int
	}{
		{
			name:      "synth metric absent from reality is outside corpus scope",
			synth:     metricSchema(Metric{Name: "synth_only"}),
			reality:   New(),
			wantCount: 0,
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
			wantCount:   1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings := Diff(test.synth, test.reality)
			if len(findings) != test.wantCount {
				t.Fatalf("findings=%+v, want %d finding(s)", findings, test.wantCount)
			}
			if test.wantCount == 0 {
				return
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

func TestDiffLogs(t *testing.T) {
	synth := Schema{
		Logs: []Log{{
			Source:                 "pod-a",
			Transport:              TransportLoki,
			StreamLabels:           []Attribute{{Key: "cluster", Values: []string{"synthetic"}}},
			StructuredMetadataKeys: []string{"trace_id"},
		}},
	}
	reality := Schema{
		Logs: []Log{{
			Source:                 "pod-b",
			Transport:              TransportLoki,
			StreamLabels:           []Attribute{{Key: "cluster", Values: []string{"real"}}},
			StructuredMetadataKeys: []string{"trace_id"},
		}},
	}

	findings := Diff(synth, reality)
	if len(findings) != 2 {
		t.Fatalf("findings=%+v, want log value contradiction and coverage gap", findings)
	}
	signal := "loki[stream_labels=cluster;structured_metadata_keys=trace_id]"
	assertFinding(t, findings, KindLabelValueContradiction, DispositionContradiction, signal, "stream_labels.cluster")
	assertFinding(t, findings, KindLabelValueContradiction, DispositionCoverageGap, signal, "stream_labels.cluster")
}

func TestDiffLogSignalsIdentifyStructuralShapes(t *testing.T) {
	reality := Schema{
		Logs: []Log{
			{Transport: TransportLoki},
			{Transport: TransportLoki, StreamLabels: []Attribute{{Key: "pod"}}},
			{Transport: TransportLoki, StructuredMetadataKeys: []string{"trace_id"}},
			{
				Transport:              TransportLoki,
				StreamLabels:           []Attribute{{Key: "cluster"}},
				StructuredMetadataKeys: []string{"severity", "trace_id"},
			},
		},
	}

	findings := Diff(New(), reality)
	wantSignals := map[string]struct{}{
		TransportLoki:                             {},
		"loki[stream_labels=pod]":                 {},
		"loki[structured_metadata_keys=trace_id]": {},
		"loki[stream_labels=cluster;structured_metadata_keys=severity,trace_id]": {},
	}
	if len(findings) != len(wantSignals) {
		t.Fatalf("findings=%+v, want one finding per structural log shape", findings)
	}
	for _, finding := range findings {
		if finding.Kind != KindExtraLog || finding.Disposition != DispositionCoverageGap {
			t.Fatalf("finding=%+v, want extra_log coverage gap", finding)
		}
		if _, ok := wantSignals[finding.Signal]; !ok {
			t.Fatalf("finding signal=%q, want one of %v", finding.Signal, wantSignals)
		}
		delete(wantSignals, finding.Signal)
	}
	if len(wantSignals) != 0 {
		t.Fatalf("missing structural log signals: %v", wantSignals)
	}
}

func TestDiffTraces(t *testing.T) {
	synth := Schema{
		Traces: []Trace{{
			Service:   "api",
			SpanNames: []string{"GET /items"},
		}},
	}
	reality := Schema{
		Traces: []Trace{{
			Service:   "api",
			SpanNames: []string{"POST /items"},
		}},
	}

	findings := Diff(synth, reality)
	assertFinding(t, findings, KindInstrumentMismatch, DispositionContradiction, "api", "span_names")
	assertFinding(t, findings, KindInstrumentMismatch, DispositionCoverageGap, "api", "span_names")
}

func TestDiffProfiles(t *testing.T) {
	synth := Schema{
		Profiles: []Profile{{
			ProfileType: "cpu",
			Labels:      []Attribute{{Key: "service", Values: []string{"api"}}},
		}},
	}
	reality := Schema{
		Profiles: []Profile{{
			ProfileType: "cpu",
			Labels:      []Attribute{{Key: "pod", Values: []string{"api-1"}}},
		}},
	}

	findings := Diff(synth, reality)
	assertFinding(t, findings, KindUnexpectedLabelKey, DispositionContradiction, "cpu", "labels")
	assertFinding(t, findings, KindUnexpectedLabelKey, DispositionCoverageGap, "cpu", "labels")
}

func TestDiffSigil(t *testing.T) {
	synth := Schema{
		Sigil: []Sigil{{
			IngestKind:     "generation",
			OperationNames: []string{"chat"},
		}},
	}
	reality := Schema{
		Sigil: []Sigil{{
			IngestKind:     "generation",
			OperationNames: []string{"completion"},
		}},
	}

	findings := Diff(synth, reality)
	assertFinding(t, findings, KindInstrumentMismatch, DispositionContradiction, "generation", "operation_names")
	assertFinding(t, findings, KindInstrumentMismatch, DispositionCoverageGap, "generation", "operation_names")
}

func TestDiffScopesFamiliesToRealityCorpus(t *testing.T) {
	synth := Schema{
		Metrics: []Metric{{Name: "uncovered_metric"}},
		Logs:    []Log{{Source: "synth", Transport: TransportLoki}},
		Traces:  []Trace{{Service: "uncovered-service"}},
		Profiles: []Profile{{
			ProfileType: "uncovered-profile",
		}},
		Sigil: []Sigil{{IngestKind: "uncovered-ingest"}},
	}
	reality := Schema{
		Metrics: []Metric{{Name: "covered_metric"}},
		Logs:    []Log{{Source: "reality", Transport: TransportOTLPLogs}},
		Traces:  []Trace{{Service: "covered-service"}},
		Profiles: []Profile{{
			ProfileType: "covered-profile",
		}},
		Sigil: []Sigil{{IngestKind: "covered-ingest"}},
	}

	findings := Diff(synth, reality)
	if len(findings) != 5 {
		t.Fatalf("findings=%+v, want one reality-only coverage gap per class", findings)
	}
	wantKinds := map[string]FindingKind{
		"covered_metric":  KindExtraMetric,
		TransportOTLPLogs: KindExtraLog,
		"covered-service": KindExtraTrace,
		"covered-profile": KindExtraProfile,
		"covered-ingest":  KindExtraSigil,
	}
	for _, finding := range findings {
		if finding.Disposition != DispositionCoverageGap {
			t.Fatalf("finding=%+v, want coverage gap", finding)
		}
		if strings.HasPrefix(finding.Signal, "uncovered") || finding.Signal == "synth" {
			t.Fatalf("synth-only family leaked into corpus-scoped diff: %+v", finding)
		}
		if want, ok := wantKinds[finding.Signal]; !ok || finding.Kind != want {
			t.Fatalf("finding=%+v, want class-specific kind %q", finding, want)
		}
	}
}

func TestDiffHonorsElidedValuesAcrossSignalClasses(t *testing.T) {
	synth := Schema{
		Logs: []Log{{
			Transport:    TransportLoki,
			StreamLabels: []Attribute{{Key: "cluster", Values: []string{"synth"}, ValuesElided: true}},
		}},
		Traces: []Trace{{
			Service:            "api",
			ResourceAttributes: []Attribute{{Key: "cluster", Values: []string{"synth"}, ValuesElided: true}},
		}},
		Profiles: []Profile{{
			ProfileType: "cpu",
			Labels:      []Attribute{{Key: "cluster", Values: []string{"synth"}, ValuesElided: true}},
		}},
	}
	reality := Schema{
		Logs: []Log{{
			Transport:    TransportLoki,
			StreamLabels: []Attribute{{Key: "cluster", Values: []string{"reality"}, ValuesElided: true}},
		}},
		Traces: []Trace{{
			Service:            "api",
			ResourceAttributes: []Attribute{{Key: "cluster", Values: []string{"reality"}, ValuesElided: true}},
		}},
		Profiles: []Profile{{
			ProfileType: "cpu",
			Labels:      []Attribute{{Key: "cluster", Values: []string{"reality"}, ValuesElided: true}},
		}},
	}

	if findings := Diff(synth, reality); len(findings) != 0 {
		t.Fatalf("findings=%+v, elided values must remain open-ended for all classes", findings)
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
