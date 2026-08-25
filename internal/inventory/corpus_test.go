// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLoadCorpusDirValidatesEnvelopeAndOrdersDocuments(t *testing.T) {
	root := t.TempDir()
	writeCorpusJSON(t, root, "k8s", "z-source", validCorpusDocument("k8s", "z", "k3s"))
	writeCorpusJSON(t, root, "k8s", "b-source", validCorpusDocument("k8s", "b", "eks"))
	writeCorpusJSON(t, root, "k8s", "a-source", validCorpusDocument("k8s", "a", "k3s"))

	docs, err := LoadCorpusDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{docs[0].Source.Kind, docs[1].Source.Kind, docs[2].Source.Kind}; !reflect.DeepEqual(got, []string{"a", "b", "z"}) {
		t.Fatalf("document order=%v, want path order", got)
	}
	for _, doc := range docs {
		if !reflect.DeepEqual(doc.Authority.Substrates, []string{doc.Source.Substrate}) {
			t.Fatalf("authority=%v for source substrate %q, want exact singleton", doc.Authority.Substrates, doc.Source.Substrate)
		}
	}

	cases := []struct {
		name   string
		mutate func(*CorpusDocument)
		want   string
	}{
		{
			name: "corpus version",
			mutate: func(doc *CorpusDocument) {
				doc.CorpusVersion = "wrong/v1"
			},
			want: "corpus_version",
		},
		{
			name: "area",
			mutate: func(doc *CorpusDocument) {
				doc.Area = "not-an-area"
			},
			want: "area",
		},
		{
			name: "generic source",
			mutate: func(doc *CorpusDocument) {
				doc.Source.Collector = ""
			},
			want: "source.collector",
		},
		{
			name: "date",
			mutate: func(doc *CorpusDocument) {
				doc.Source.CapturedOn = "2026-99-99"
			},
			want: "source.captured_on",
		},
		{
			name: "authority",
			mutate: func(doc *CorpusDocument) {
				doc.Authority.Substrates = []string{"other"}
			},
			want: "authority.substrates",
		},
		{
			name: "capture volume",
			mutate: func(doc *CorpusDocument) {
				doc.CaptureVolume.Runs = 0
			},
			want: "capture_volume.runs",
		},
		{
			name: "inventory schema",
			mutate: func(doc *CorpusDocument) {
				doc.Inventory.SchemaVersion = "wrong/v1"
			},
			want: "inventory.schema_version",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			bad := validCorpusDocument("k8s", "source", "k3s")
			test.mutate(&bad)
			caseRoot := t.TempDir()
			writeCorpusJSON(t, caseRoot, "k8s", "invalid", bad)
			_, err := LoadCorpusDir(caseRoot)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want field %q", err, test.want)
			}
		})
	}
}

func TestLoadCorpusDirRejectsDuplicateAreaSourceSubstrate(t *testing.T) {
	root := t.TempDir()
	writeCorpusJSON(t, root, "k8s", "one", validCorpusDocument("k8s", "producer", "k3s"))
	writeCorpusJSON(t, root, "k8s", "two", validCorpusDocument("k8s", "producer", "k3s"))

	_, err := LoadCorpusDir(root)
	if err == nil || !strings.Contains(err.Error(), "duplicate") || !strings.Contains(err.Error(), "k8s") {
		t.Fatalf("error=%v, want duplicate area/source/substrate error", err)
	}
}

func TestLoadCorpusDirRejectsDifferentAuthoritySubstrate(t *testing.T) {
	doc := validCorpusDocument("k8s", "producer", "k3s")
	doc.Authority.Substrates = []string{"eks", "k3s"}
	root := t.TempDir()
	writeCorpusJSON(t, root, "k8s", "invalid-authority", doc)

	_, err := LoadCorpusDir(root)
	if err == nil || !strings.Contains(err.Error(), "authority.substrates") || !strings.Contains(err.Error(), "exactly") {
		t.Fatalf("error=%v, want different authority substrate rejected", err)
	}
}

func TestLoadCorpusDirRejectsSecondValidJSONValue(t *testing.T) {
	root := t.TempDir()
	path := writeCorpusJSON(t, root, "k8s", "trailing", validCorpusDocument("k8s", "trailing", "k3s"))
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, []byte("\n{}\n")...)
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = LoadCorpusDir(root)
	if err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("error=%v, want second valid JSON value rejected as trailing JSON", err)
	}
}

func TestCanonicalMergeIsCumulativeIdempotentAndElidesVaryingValues(t *testing.T) {
	existing := validCorpusDocument("k8s", "producer", "k3s")
	existing.CaptureVolume = CaptureVolume{Runs: 2, ObservedContractCounts: []int{3, 5}}
	existing.Inventory.AddMetric("requests_total", TransportPrometheusRW2, InstrumentCounter,
		map[string]string{"environment": "prod", "instance": "node-a"}, nil)

	subset := validCorpusDocument("k8s", "producer", "k3s")
	subset.CaptureVolume = CaptureVolume{Runs: 1, ObservedContractCounts: []int{3}}
	subset.Inventory.AddMetric("requests_total", TransportPrometheusRW2, InstrumentCounter,
		map[string]string{"environment": "prod"}, nil)

	merged, err := CanonicalMerge(existing, subset)
	if err != nil {
		t.Fatal(err)
	}
	attr := findMetricAttribute(merged.Inventory.Metrics[0], "environment")
	if !reflect.DeepEqual(attr.Values, []string{"prod"}) || attr.ValuesElided {
		t.Fatalf("stable subset attr=%+v, want retained value", attr)
	}
	if got := findMetricAttribute(merged.Inventory.Metrics[0], "instance"); !reflect.DeepEqual(got.Values, []string{"node-a"}) {
		t.Fatalf("missing established attr=%+v", got)
	}
	if !reflect.DeepEqual(merged.CaptureVolume.ObservedContractCounts, []int{3, 5}) {
		t.Fatalf("counts=%v, want sorted distinct cumulative counts", merged.CaptureVolume.ObservedContractCounts)
	}
	if merged.CaptureVolume.Runs != 2 {
		t.Fatalf("runs=%d, want unchanged subset provenance", merged.CaptureVolume.Runs)
	}
	subsetAgain, err := CanonicalMerge(merged, subset)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(subsetAgain, merged) {
		t.Fatalf("subset refresh churned canonical document\nmerged=%+v\nagain=%+v", merged, subsetAgain)
	}

	varying := validCorpusDocument("k8s", "producer", "k3s")
	varying.CaptureVolume = CaptureVolume{Runs: 1, ObservedContractCounts: []int{7}}
	varying.Inventory.AddMetric("requests_total", TransportPrometheusRW2, InstrumentCounter,
		map[string]string{"environment": "staging"}, nil)
	merged, err = CanonicalMerge(merged, varying)
	if err != nil {
		t.Fatal(err)
	}
	attr = findMetricAttribute(merged.Inventory.Metrics[0], "environment")
	if len(attr.Values) != 0 || !attr.ValuesElided {
		t.Fatalf("varying attr=%+v, want empty sticky elision", attr)
	}

	again, err := CanonicalMerge(merged, merged)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, merged) {
		t.Fatalf("merge is not idempotent\nmerged=%+v\nagain=%+v", merged, again)
	}
}

func TestCanonicalMergeRejectsConfigurationMismatchAndSeparatesLogShapes(t *testing.T) {
	existing := validCorpusDocument("logs", "producer", "k3s")
	existing.Inventory.Logs = []Log{{
		Source:                 "pod-a",
		Transport:              TransportLoki,
		StreamLabels:           []Attribute{{Key: "cluster", Values: []string{"k3s"}}},
		StructuredMetadataKeys: []string{"trace_id"},
	}}
	candidate := validCorpusDocument("logs", "producer", "k3s")
	candidate.Inventory.Logs = []Log{{
		Source:                 "pod-b",
		Transport:              TransportLoki,
		StreamLabels:           []Attribute{{Key: "cluster", Values: []string{"k3s"}}},
		StructuredMetadataKeys: []string{"trace_id"},
	}}
	merged, err := CanonicalMerge(existing, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Inventory.Logs) != 1 {
		t.Fatalf("dynamic source exemplar created a family: %+v", merged.Inventory.Logs)
	}

	differentShape := validCorpusDocument("logs", "producer", "k3s")
	differentShape.Inventory.Logs = []Log{{
		Source:                 "pod-c",
		Transport:              TransportLoki,
		StreamLabels:           []Attribute{{Key: "cluster", Values: []string{"k3s"}}, {Key: "namespace", Values: []string{"default"}}},
		StructuredMetadataKeys: []string{"trace_id"},
	}}
	merged, err = CanonicalMerge(merged, differentShape)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Inventory.Logs) != 2 {
		t.Fatalf("different structural shape did not create family: %+v", merged.Inventory.Logs)
	}

	bad := candidate
	bad.Source.CollectorVersion = "9.9.9"
	if _, err := CanonicalMerge(existing, bad); err == nil || !strings.Contains(err.Error(), "collector_version") {
		t.Fatalf("error=%v, want collector_version mismatch", err)
	}
	bad = candidate
	bad.Source.Substrate = "eks"
	if _, err := CanonicalMerge(existing, bad); err == nil || !strings.Contains(err.Error(), "substrate") {
		t.Fatalf("error=%v, want substrate mismatch", err)
	}
}

func TestCompareCorpusDoesNotUnionSubstratesAndUsesArea(t *testing.T) {
	synth := New()
	synth.AddMetric("requests_total", TransportPrometheusRW2, InstrumentCounter, nil, nil)
	k3s := validCorpusDocument("k8s", "producer-k3s", "k3s")
	k3s.Inventory = New()
	k3s.Inventory.AddMetric("k3s_only", TransportPrometheusRW2, InstrumentCounter, nil, nil)
	eks := validCorpusDocument("k8s", "producer-eks", "eks")
	eks.Inventory = New()
	eks.Inventory.AddMetric("eks_only", TransportPrometheusRW2, InstrumentCounter, nil, nil)

	findings := CompareCorpus(synth, []CorpusDocument{eks, k3s})
	if len(findings) != 2 {
		t.Fatalf("findings=%+v, want independent substrate findings", findings)
	}
	if findings[0].Area != "k8s" || findings[1].Area != "k8s" {
		t.Fatalf("areas=%q/%q, want document-owned area", findings[0].Area, findings[1].Area)
	}
	if findings[0].Substrate != "eks" || findings[1].Substrate != "k3s" {
		t.Fatalf("findings=%+v, want each finding scoped to its own substrate", findings)
	}
	if findings[0].Finding.Disposition != DispositionCoverageGap || findings[1].Finding.Disposition != DispositionCoverageGap {
		t.Fatalf("findings=%+v, want independent coverage findings", findings)
	}
	if findings[0].Finding.Signal != "eks_only" || findings[1].Finding.Signal != "k3s_only" {
		t.Fatalf("signals=%q/%q, want document-local metric names", findings[0].Finding.Signal, findings[1].Finding.Signal)
	}
}

func validCorpusDocument(area, source, substrate string) CorpusDocument {
	return CorpusDocument{
		CorpusVersion: CorpusVersion,
		Area:          area,
		Source: CorpusSource{
			Kind:             source,
			Substrate:        substrate,
			Collector:        "grafana/k8s-monitoring",
			CollectorVersion: "4.4.0",
			CapturedOn:       "2026-08-25",
		},
		Authority: CorpusAuthority{Substrates: []string{substrate}},
		CaptureVolume: CaptureVolume{
			Runs:                   1,
			ObservedContractCounts: []int{1},
		},
		Inventory: New(),
	}
}

func writeCorpusJSON(t *testing.T, root, area, source string, document CorpusDocument) string {
	t.Helper()
	dir := root + "/" + area
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := dir + "/" + source + ".json"
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func findMetricAttribute(metric Metric, key string) Attribute {
	for _, attr := range metric.Labels {
		if attr.Key == key {
			return attr
		}
	}
	return Attribute{}
}
