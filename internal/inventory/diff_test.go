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

func TestDiffSkipsLabelsForSharedLibraryMultiProducerFamilies(t *testing.T) {
	for _, name := range []string{
		"go_goroutines",
		"process_cpu_seconds_total",
		"rest_client_requests_total",
	} {
		t.Run(name, func(t *testing.T) {
			findings := Diff(
				metricSchema(Metric{
					Name:            name,
					InstrumentTypes: []string{InstrumentGauge},
					Labels: []Attribute{
						{Key: "job", Values: []string{"one"}},
						{Key: "synth_producer", Values: []string{"one"}},
					},
				}),
				metricSchema(Metric{
					Name:            name,
					InstrumentTypes: []string{InstrumentGauge},
					Labels: []Attribute{
						{Key: "job", Values: []string{"many"}},
						{Key: "reality_producer", Values: []string{"many"}},
					},
				}),
			)
			if len(findings) != 0 {
				t.Fatalf("findings=%+v, multi-producer union labels must not compare", findings)
			}
		})
	}
}

func TestDiffSharedLibraryMultiProducerFamilyStillComparesInstrument(t *testing.T) {
	findings := Diff(
		metricSchema(Metric{
			Name:            "go_goroutines",
			InstrumentTypes: []string{InstrumentGauge},
			Labels:          []Attribute{{Key: "synth_producer"}},
		}),
		metricSchema(Metric{
			Name:            "go_goroutines",
			InstrumentTypes: []string{InstrumentCounter},
			Labels:          []Attribute{{Key: "reality_producer"}},
		}),
	)
	if len(findings) != 2 {
		t.Fatalf("findings=%+v, want only the two directional instrument findings", findings)
	}
	assertFinding(t, findings, KindInstrumentMismatch, DispositionContradiction, "go_goroutines", "instrument_types")
	assertFinding(t, findings, KindInstrumentMismatch, DispositionCoverageGap, "go_goroutines", "instrument_types")
}

func TestDiffNonSharedMetricStillComparesLabels(t *testing.T) {
	findings := Diff(
		metricSchema(Metric{Name: "application_process_state", Labels: []Attribute{{Key: "synth_only"}}}),
		metricSchema(Metric{Name: "application_process_state", Labels: []Attribute{{Key: "reality_only"}}}),
	)
	assertFinding(t, findings, KindUnexpectedLabelKey, DispositionContradiction, "application_process_state", "labels")
	assertFinding(t, findings, KindUnexpectedLabelKey, DispositionCoverageGap, "application_process_state", "labels")
}

func TestDiffFoldedBuildInfoSourceIsCoverageGap(t *testing.T) {
	findings := Diff(
		metricSchema(Metric{Name: "kubernetes_build_info", Labels: []Attribute{{Key: "source"}}}),
		metricSchema(Metric{Name: "kubernetes_build_info", Labels: []Attribute{{Key: "job"}}}),
	)
	assertFinding(t, findings, KindUnexpectedLabelKey, DispositionCoverageGap, "kubernetes_build_info", "labels")
	for _, finding := range findings {
		if finding.Disposition == DispositionContradiction {
			t.Fatalf("findings=%+v, folded build-info source must not contradict the combined corpus family", findings)
		}
	}
}

func TestDiffFoldedBuildInfoSourceStaysSeparateFromInventedKeys(t *testing.T) {
	findings := Diff(
		metricSchema(Metric{Name: "kubernetes_build_info", Labels: []Attribute{{Key: "source"}, {Key: "invented"}, {Key: "job"}}}),
		metricSchema(Metric{Name: "kubernetes_build_info", Labels: []Attribute{{Key: "job"}}}),
	)

	var sourceGap, inventedContradiction bool
	for _, finding := range findings {
		onlySynth := difference(finding.SynthValues, finding.RealityValues)
		if finding.Disposition == DispositionCoverageGap && equalStrings(onlySynth, []string{"source"}) {
			sourceGap = true
		}
		if finding.Disposition == DispositionContradiction && equalStrings(onlySynth, []string{"invented"}) {
			inventedContradiction = true
		}
	}
	if !sourceGap || !inventedContradiction {
		t.Fatalf("findings=%+v, want an independent source gap and invented-key contradiction", findings)
	}
}

func TestDiffLabelValuesCompareDirectionally(t *testing.T) {
	for _, test := range []struct {
		name      string
		signal    string
		synth     Attribute
		reality   Attribute
		wantCount int
		want      map[Disposition]bool
	}{
		{
			name:      "unknown value set defaults synth-only value to a coverage gap",
			synth:     Attribute{Key: "region", Values: []string{"eu-west-1", "us-east-1", "us-west-2"}},
			reality:   Attribute{Key: "region", Values: []string{"us-east-1"}},
			wantCount: 1,
			want:      map[Disposition]bool{DispositionCoverageGap: true},
		},
		{
			name:      "explicit closed value set keeps an impossible synth value contradictory",
			signal:    "coredns_dns_responses_total",
			synth:     Attribute{Key: "rcode", Values: []string{"NOERROR", "NOT_A_DNS_RCODE"}},
			reality:   Attribute{Key: "rcode", Values: []string{"NOERROR"}},
			wantCount: 1,
			want:      map[Disposition]bool{DispositionContradiction: true},
		},
		{
			name:      "reviewed build-info job set keeps an unobserved job contradictory",
			signal:    "kubernetes_build_info",
			synth:     Attribute{Key: "job", Values: []string{"integrations/kubernetes/kubelet", "integrations/kubernetes/kube-proxy"}},
			reality:   Attribute{Key: "job", Values: []string{"integrations/kubernetes/kube-proxy"}},
			wantCount: 1,
			want:      map[Disposition]bool{DispositionContradiction: true},
		},
		{
			name:      "reality-only value is a coverage gap",
			synth:     Attribute{Key: "region", Values: []string{"us-east-1"}},
			reality:   Attribute{Key: "region", Values: []string{"us-east-1", "ap-south-1"}},
			wantCount: 1,
			want:      map[Disposition]bool{DispositionCoverageGap: true},
		},
		{
			name:      "unknown disjoint value sets produce one coverage gap",
			synth:     Attribute{Key: "environment", Values: []string{"prod"}},
			reality:   Attribute{Key: "environment", Values: []string{"staging"}},
			wantCount: 1,
			want:      map[Disposition]bool{DispositionCoverageGap: true},
		},
		{
			name:      "elided reality values run no value comparison",
			synth:     Attribute{Key: "region", Values: []string{"us-east-1"}},
			reality:   Attribute{Key: "region", Values: []string{}, ValuesElided: true},
			wantCount: 0,
		},
		{
			name:      "elided synth values run no value comparison",
			synth:     Attribute{Key: "region", Values: []string{}, ValuesElided: true},
			reality:   Attribute{Key: "region", Values: []string{"ap-south-1"}},
			wantCount: 0,
		},
		{
			name:      "reality key observed without any value carries no value evidence",
			synth:     Attribute{Key: "region", Values: []string{"us-east-1"}},
			reality:   Attribute{Key: "region", Values: []string{}},
			wantCount: 0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			signal := test.signal
			if signal == "" {
				signal = "requests_total"
			}
			findings := Diff(
				metricSchema(Metric{Name: signal, Labels: []Attribute{test.synth}}),
				metricSchema(Metric{Name: signal, Labels: []Attribute{test.reality}}),
			)
			if len(findings) != test.wantCount {
				t.Fatalf("findings=%+v, want %d", findings, test.wantCount)
			}
			if test.wantCount == 0 {
				return
			}
			seen := make(map[Disposition]bool, len(findings))
			for _, got := range findings {
				if got.Kind != KindLabelValueContradiction {
					t.Fatalf("finding=%+v, want a label-value finding", got)
				}
				if got.Field != "labels."+test.reality.Key {
					t.Fatalf("finding field=%q", got.Field)
				}
				seen[got.Disposition] = true
			}
			if !reflect.DeepEqual(seen, test.want) {
				t.Fatalf("findings=%+v, want dispositions=%v", findings, test.want)
			}
		})
	}
}

func TestDiffKubePodInfoValueGapsRemainVisible(t *testing.T) {
	synth := metricSchema(Metric{
		Name: "kube_pod_info",
		Labels: []Attribute{
			{Key: "created_by_kind", Values: []string{"DaemonSet", "Job", "ReplicaSet", "StatefulSet"}},
			{Key: "host_network", Values: []string{"false"}},
		},
	})
	reality := metricSchema(Metric{
		Name: "kube_pod_info",
		Labels: []Attribute{
			{Key: "created_by_kind", Values: []string{"AutoscalingListener", "DaemonSet", "EphemeralRunner", "Job", "ReplicaSet", "StatefulSet"}},
			{Key: "host_network", Values: []string{"false", "true"}},
		},
	})

	findings := Diff(synth, reality)
	if len(findings) != 2 {
		t.Fatalf("findings=%+v, want one gap for each kube_pod_info value limit", findings)
	}
	for _, finding := range findings {
		if finding.Kind != KindLabelValueContradiction || finding.Disposition != DispositionCoverageGap {
			t.Fatalf("finding=%+v, kube_pod_info value limits must be coverage gaps", finding)
		}
	}
}

func TestDiffUnobservedInstrumentTypeIsCoverageGapNotContradiction(t *testing.T) {
	for _, test := range []struct {
		name    string
		synth   []string
		reality []string
		kind    FindingKind
	}{
		{name: "reality never observed an instrument type", synth: []string{InstrumentGauge}, reality: []string{InstrumentUnknown}, kind: KindUnknownInstrumentEvidence},
		{name: "synth never observed an instrument type", synth: []string{InstrumentUnknown}, reality: []string{InstrumentGauge}, kind: KindInstrumentMismatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			findings := Diff(
				metricSchema(Metric{Name: "node_cpu_seconds_total", InstrumentTypes: test.synth}),
				metricSchema(Metric{Name: "node_cpu_seconds_total", InstrumentTypes: test.reality}),
			)
			for _, finding := range findings {
				if finding.Disposition == DispositionContradiction {
					t.Fatalf("finding=%+v, the unknown sentinel must never contradict", finding)
				}
			}
			if len(findings) != 1 {
				t.Fatalf("findings=%+v, want exactly one coverage gap", findings)
			}
			assertFinding(t, findings, test.kind, DispositionCoverageGap, "node_cpu_seconds_total", "instrument_types")
		})
	}
}

func TestDiffRecordedInstrumentTypeStillContradicts(t *testing.T) {
	findings := Diff(
		metricSchema(Metric{Name: "node_cpu_seconds_total", InstrumentTypes: []string{InstrumentGauge}}),
		metricSchema(Metric{Name: "node_cpu_seconds_total", InstrumentTypes: []string{InstrumentCounter}}),
	)
	assertFinding(t, findings, KindInstrumentMismatch, DispositionContradiction, "node_cpu_seconds_total", "instrument_types")
	assertFinding(t, findings, KindInstrumentMismatch, DispositionCoverageGap, "node_cpu_seconds_total", "instrument_types")
}

func TestDiffPartiallyObservedInstrumentTypeStillContradicts(t *testing.T) {
	findings := Diff(
		metricSchema(Metric{Name: "node_cpu_seconds_total", InstrumentTypes: []string{InstrumentGauge}}),
		metricSchema(Metric{Name: "node_cpu_seconds_total", InstrumentTypes: []string{InstrumentCounter, InstrumentUnknown}}),
	)
	assertFinding(t, findings, KindInstrumentMismatch, DispositionContradiction, "node_cpu_seconds_total", "instrument_types")
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
	if len(findings) != 1 {
		t.Fatalf("findings=%+v, want one open-set coverage gap carrying both directions", findings)
	}
	signal := "loki[stream_labels=cluster;structured_metadata_keys=trace_id]"
	assertFinding(t, findings, KindLabelValueContradiction, DispositionCoverageGap, signal, "stream_labels.cluster")
	if !equalStrings(findings[0].SynthValues, []string{"synthetic"}) || !equalStrings(findings[0].RealityValues, []string{"real"}) {
		t.Fatalf("findings=%+v, want both open-set directions preserved", findings)
	}
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

// podLogKeys is the OTLP pod-log resource shape captured at collector egress.
var podLogKeys = []string{
	"cluster", "k8s.cluster.name", "k8s.namespace.name", "k8s.pod.name", "k8s.container.name",
	"service.instance.id", "service.name", "service.namespace",
}

func podLogEntry(extra ...string) Log {
	log := Log{
		Source:                 LogFamilyPodLogs,
		Transport:              TransportOTLPLogs,
		StreamLabels:           []Attribute{},
		StructuredMetadataKeys: []string{"log.iostream", "logtag"},
	}
	for _, key := range append(append([]string{}, podLogKeys...), extra...) {
		log.StreamLabels = append(log.StreamLabels, Attribute{Key: key, Values: []string{}})
	}
	return log
}

func logSchema(logs ...Log) Schema {
	schema := New()
	schema.Logs = logs
	schema.Normalize()
	return schema
}

func TestDiffJoinsPodLogShapeVariantsIntoOneFamily(t *testing.T) {
	// The capture records two shapes of the same family: a pod with a Deployment owner and a
	// node, and a pod with neither. Keying the family on its raw label set makes the second one
	// a whole missing log family, which synthkit is then reported as not emitting at all.
	reality := logSchema(
		podLogEntry("k8s.deployment.name", "k8s.node.name", "app_kubernetes_io_name"),
		podLogEntry(),
	)
	synth := logSchema(podLogEntry("k8s.deployment.name", "k8s.node.name", "app_kubernetes_io_name"))

	findings := Diff(synth, reality)
	for _, finding := range findings {
		if finding.Kind == KindExtraLog {
			t.Fatalf("findings=%+v, want no extra_log: both shapes are the %s family", findings, LogFamilyPodLogs)
		}
	}
}

func TestDiffReportsOptionalPodLogKeysRatherThanAbsorbingTheirVariant(t *testing.T) {
	// The owner/node shape and the unowned/unscheduled shape are one family, but the
	// latter must remain comparable. A union alone would make the optional keys look
	// mandatory and conceal a synth inventory that emitted only the owned variant.
	reality := logSchema(
		podLogEntry("k8s.deployment.name", "k8s.node.name", "app_kubernetes_io_name"),
		podLogEntry(),
	)
	synth := logSchema(podLogEntry("k8s.deployment.name", "k8s.node.name", "app_kubernetes_io_name"))

	findings := Diff(synth, reality)
	signal := TransportOTLPLogs + "[family=" + LogFamilyPodLogs + "]"
	assertFinding(t, findings, KindUnexpectedLabelKey, DispositionCoverageGap, signal, "optional_stream_label_keys")

	// The reverse direction is absent evidence: synth declaring both shapes does not
	// contradict a capture that happened to observe only the owned shape.
	reverse := Diff(reality, synth)
	for _, finding := range reverse {
		if finding.Signal == signal && (finding.Field == "stream_labels" || finding.Field == "optional_stream_label_keys") {
			t.Fatalf("reverse findings=%+v, optional synth-only keys must not contradict reality", reverse)
		}
	}
}

func TestDiffComparesPodLogLabelKeysOnceTheFamilyJoins(t *testing.T) {
	// Keys can only be compared across a family that joins; while the raw label set WAS the
	// identity, a key difference made a separate family instead of a finding.
	reality := logSchema(podLogEntry("k8s.node.name"))
	synth := logSchema(podLogEntry("k8s.deployment.name"))

	findings := Diff(synth, reality)
	signal := TransportOTLPLogs + "[family=" + LogFamilyPodLogs + "]"
	assertFinding(t, findings, KindUnexpectedLabelKey, DispositionContradiction, signal, "stream_labels")
	assertFinding(t, findings, KindUnexpectedLabelKey, DispositionCoverageGap, signal, "stream_labels")
}

func TestDiffKeepsStructuralIdentityForUnclassifiedLogFamilies(t *testing.T) {
	// A lane whose shape no rule recognises keeps the structural identity, so an unrelated
	// source-less lane never merges into another one.
	reality := logSchema(
		Log{Source: "journal", Transport: TransportLoki, StreamLabels: []Attribute{{Key: "unit", Values: []string{}}}, StructuredMetadataKeys: []string{}},
		Log{Source: "", Transport: TransportLoki, StreamLabels: []Attribute{{Key: "topic", Values: []string{}}}, StructuredMetadataKeys: []string{}},
	)
	synth := logSchema(
		Log{Source: "journal", Transport: TransportLoki, StreamLabels: []Attribute{{Key: "unit", Values: []string{}}}, StructuredMetadataKeys: []string{}},
	)

	findings := Diff(synth, reality)
	count := 0
	for _, finding := range findings {
		if finding.Kind == KindExtraLog {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("findings=%+v, want exactly one extra_log for the unmatched lane", findings)
	}
}

func TestDiffPairsCapturedDeclaredLogFamiliesBeforeComparingKeys(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		family   string
		keys     []string
		metadata []string
	}{
		"kubernetes_events": {
			family:   LogFamilyKubernetesEvents,
			keys:     []string{"cluster", "job", "k8s_cluster_name", "level", "namespace", "reason", "service_name", "source"},
			metadata: []string{"name", "node"},
		},
		"manifests": {
			family:   LogFamilyKubernetesManifests,
			keys:     []string{"action", "cluster", "job", "k8s_cluster_name", "k8s_kind", "k8s_namespace_name"},
			metadata: []string{"k8s_daemonset_name", "k8s_deployment_name", "k8s_pod_name"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			synthLog := logWithKeys(tc.family, tc.keys)
			synthLog.Transport = TransportLoki
			synthLog.StructuredMetadataKeys = tc.metadata
			realityLog := logWithKeys(tc.family, append(append([]string{}, tc.keys...), "instance"))
			realityLog.Transport = TransportLoki
			realityLog.StructuredMetadataKeys = tc.metadata

			findings := Diff(logSchema(synthLog), logSchema(realityLog))
			for _, finding := range findings {
				if finding.Kind == KindExtraLog {
					t.Fatalf("findings=%+v, want the declared lane paired before key comparison", findings)
				}
			}
			assertFinding(t, findings, KindUnexpectedLabelKey, DispositionCoverageGap,
				TransportLoki+"[family="+tc.family+"]", "stream_labels")
		})
	}
}

func TestDiffTreatsAnEmptyBucketBoundSetAsAbsentEvidence(t *testing.T) {
	// A producer that recorded the classic representation but no bounds did not observe them;
	// a classic histogram always has bounds, so an empty set is absent evidence, never a claim.
	findings := Diff(
		metricSchema(Metric{Name: "latency_seconds", InstrumentTypes: []string{InstrumentHistogram}, Histogram: &Histogram{Classic: true, BucketBounds: []float64{0.5, 1}}}),
		metricSchema(Metric{Name: "latency_seconds", InstrumentTypes: []string{InstrumentHistogram}, Histogram: &Histogram{Classic: true}}),
	)
	if len(findings) != 0 {
		t.Fatalf("findings=%+v, want none: reality recorded no bucket bounds to compare", findings)
	}
}
