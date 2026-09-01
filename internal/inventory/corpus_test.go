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

func TestCanonicalMergeRefreshesCaptureMetadataWithStructuralEvidence(t *testing.T) {
	existing := validCorpusDocument("k8s", "producer", "k3s")
	existing.Source.CaptureSchemaVersion = "2.0.0"
	existing.Source.CaptureToolVersion = "2.0.0"
	existing.Source.CaptureSHA256 = strings.Repeat("a", 64)
	existing.Source.CaptureScope = "cluster"
	existing.Source.CaptureWarnings = []string{"OLD_WARNING"}
	existing.Inventory.AddMetric("requests_total", TransportPrometheusRW2, InstrumentCounter, nil, nil)

	candidate := validCorpusDocument("k8s", "producer", "k3s")
	candidate.Source.CapturedOn = "2026-08-31"
	candidate.Source.CaptureSchemaVersion = "2.1.0"
	candidate.Source.CaptureToolVersion = "3.0.0"
	candidate.Source.CaptureSHA256 = strings.Repeat("b", 64)
	candidate.Source.CaptureScope = "cluster"
	candidate.Source.CaptureWarnings = []string{"NEW_WARNING", "OLD_WARNING"}
	candidate.Inventory.AddMetric("requests_total", TransportPrometheusRW2, InstrumentCounter, map[string]string{"method": "GET"}, nil)

	merged, err := CanonicalMerge(existing, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Source.CaptureSchemaVersion != "2.1.0" || merged.Source.CaptureToolVersion != "3.0.0" || merged.Source.CaptureSHA256 != strings.Repeat("b", 64) {
		t.Fatalf("capture metadata was not refreshed: %+v", merged.Source)
	}
	if got, want := merged.Source.CaptureWarnings, []string{"NEW_WARNING", "OLD_WARNING"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("capture warnings=%v, want %v", got, want)
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

func TestCompareCorpusNamesMatchingAndAbsentSubstrateEvidence(t *testing.T) {
	synth := New()
	// The clean GCP capture records label_cloud_google_com_gke_nodepool on
	// kube_node_labels; the committed EKS corpus records
	// label_eks_amazonaws_com_nodegroup for the same family. These are observed
	// cloud-specific label keys, not invented fixture values.
	synth.AddMetric("kube_node_labels", TransportPrometheusRW2, InstrumentGauge, map[string]string{"label_eks_amazonaws_com_nodegroup": ""}, nil)

	eks := validCorpusDocument("k8s", "producer-eks", "eks")
	eks.Inventory.AddMetric("kube_node_labels", TransportPrometheusRW2, InstrumentGauge, map[string]string{"label_eks_amazonaws_com_nodegroup": ""}, nil)
	gcp := validCorpusDocument("k8s", "producer-gcp", "gcp")
	gcp.Inventory.AddMetric("kube_node_labels", TransportPrometheusRW2, InstrumentGauge, map[string]string{"label_cloud_google_com_gke_nodepool": ""}, nil)
	k3s := validCorpusDocument("k8s", "producer-k3s", "k3s")
	k3s.Inventory.AddMetric("kube_node_info", TransportPrometheusRW2, InstrumentGauge, nil, nil)

	findings := CompareCorpus(synth, []CorpusDocument{gcp, k3s, eks})
	var contradiction *ScopedFinding
	for i := range findings {
		if findings[i].Substrate == "gcp" && findings[i].Finding.Disposition == DispositionContradiction && findings[i].Finding.Field == "labels" {
			contradiction = &findings[i]
			break
		}
	}
	if contradiction == nil {
		t.Fatalf("findings=%+v, want GCP kube_node_labels contradiction", findings)
	}
	if got, want := contradiction.MatchingSubstrates, []string{"eks"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("matching substrates=%v, want %v", got, want)
	}
	if got, want := contradiction.AbsentEvidenceSubstrates, []string{"k3s"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("absent-evidence substrates=%v, want %v", got, want)
	}
}

func TestCompareCorpusRequiresEverySubstrateDocumentToContainSignalBeforeMatching(t *testing.T) {
	synth := New()
	synth.AddMetric("kube_node_labels", TransportPrometheusRW2, InstrumentGauge, map[string]string{"label_eks_amazonaws_com_nodegroup": ""}, nil)

	gcp := validCorpusDocument("k8s", "producer-gcp", "gcp")
	gcp.Inventory.AddMetric("kube_node_labels", TransportPrometheusRW2, InstrumentGauge, map[string]string{"label_cloud_google_com_gke_nodepool": ""}, nil)
	eksWithSignal := validCorpusDocument("k8s", "producer-eks-default", "eks")
	eksWithSignal.Inventory.AddMetric("kube_node_labels", TransportPrometheusRW2, InstrumentGauge, map[string]string{"label_eks_amazonaws_com_nodegroup": ""}, nil)
	eksWithoutSignal := validCorpusDocument("k8s", "producer-eks-alternate", "eks")
	eksWithoutSignal.Inventory.AddMetric("kube_node_info", TransportPrometheusRW2, InstrumentGauge, nil, nil)

	findings := CompareCorpus(synth, []CorpusDocument{gcp, eksWithSignal, eksWithoutSignal})
	for _, finding := range findings {
		if finding.Substrate != "gcp" || finding.Finding.Disposition != DispositionContradiction || finding.Finding.Field != "labels" {
			continue
		}
		if len(finding.MatchingSubstrates) != 0 {
			t.Fatalf("matching substrates=%v, want none when one EKS document lacks the signal", finding.MatchingSubstrates)
		}
		if got, want := finding.AbsentEvidenceSubstrates, []string{"eks"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("absent-evidence substrates=%v, want %v", got, want)
		}
		return
	}
	t.Fatalf("findings=%+v, want GCP label contradiction", findings)
}

func validCorpusDocument(area, source, substrate string) CorpusDocument {
	return CorpusDocument{
		CorpusVersion: CorpusVersion,
		Area:          area,
		Source: CorpusSource{
			Kind:             source,
			Substrate:        substrate,
			Collector:        "grafana/k8s-monitoring",
			CollectorRole:    CollectorRoleAudited,
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

func TestCompareCorpusExcludesDeclaredEnrichmentLabels(t *testing.T) {
	synth := New()
	synth.Metrics = []Metric{{
		Name:            "aws_applicationelb_request_count_sum",
		InstrumentTypes: []string{InstrumentGauge},
		Labels:          []Attribute{{Key: "job", Values: []string{"cloud/aws/applicationelb"}}},
	}}

	realityLabels := []Attribute{
		{Key: "job", Values: []string{"cloud/aws/applicationelb"}},
		{Key: "asserts_env", Values: []string{"prod"}},
	}
	declared := validCorpusDocument("cw", "gcx_live_readback", "eks")
	declared.Source.EnrichmentLabels = []EnrichmentLabel{{
		Key:        "asserts_env",
		Provenance: "Grafana Cloud asserts read-path enrichment added after ingest; not present at collector egress.",
	}}
	declared.Inventory.Metrics = []Metric{{
		Name:            "aws_applicationelb_request_count_sum",
		InstrumentTypes: []string{InstrumentGauge},
		Labels:          realityLabels,
	}}

	if findings := CompareCorpus(synth, []CorpusDocument{declared}); len(findings) != 0 {
		t.Fatalf("findings=%+v, a declared enrichment label must not count as a missing synth label", findings)
	}

	undeclared := validCorpusDocument("cw", "other_readback", "eks")
	undeclared.Inventory.Metrics = declared.Inventory.Metrics
	findings := CompareCorpus(synth, []CorpusDocument{undeclared})
	if len(findings) != 1 {
		t.Fatalf("findings=%+v, an undeclared reality label must remain a coverage gap", findings)
	}
	if findings[0].Finding.Kind != KindUnexpectedLabelKey || findings[0].Finding.Disposition != DispositionCoverageGap {
		t.Fatalf("finding=%+v", findings[0].Finding)
	}
}

func TestCompareCorpusKeepsSynthOnlyLabelKeyContradictionForEnrichmentKeys(t *testing.T) {
	synth := New()
	synth.Metrics = []Metric{{
		Name:            "aws_target_group_info",
		InstrumentTypes: []string{InstrumentGauge},
		Labels: []Attribute{
			{Key: "job", Values: []string{"cloud/aws/applicationelb"}},
			{Key: "service", Values: []string{"checkout"}},
		},
	}}
	document := validCorpusDocument("cw", "gcx_live_readback", "eks")
	document.Source.EnrichmentLabels = []EnrichmentLabel{{
		Key:        "service",
		Provenance: "Grafana Cloud asserts read-path enrichment added after ingest; not present at collector egress.",
	}}
	document.Inventory.Metrics = []Metric{{
		Name:            "aws_target_group_info",
		InstrumentTypes: []string{InstrumentGauge},
		Labels: []Attribute{
			{Key: "job", Values: []string{"cloud/aws/applicationelb"}},
			{Key: "service", Values: []string{"checkout"}},
		},
	}}

	findings := CompareCorpus(synth, []CorpusDocument{document})
	if len(findings) != 1 {
		t.Fatalf("findings=%+v, want the synth-only direction preserved", findings)
	}
	got := findings[0].Finding
	if got.Kind != KindUnexpectedLabelKey || got.Disposition != DispositionContradiction || got.Field != "labels" {
		t.Fatalf("finding=%+v, want a synth-only label-key contradiction", got)
	}
	if !reflect.DeepEqual(got.RealityValues, []string{"job"}) {
		t.Fatalf("reality keys=%v, want the enrichment key removed from the reality view", got.RealityValues)
	}
}

func TestCorpusRejectsEnrichmentLabelWithoutKeyOrProvenance(t *testing.T) {
	for _, test := range []struct {
		name  string
		label EnrichmentLabel
		want  string
	}{
		{name: "missing key", label: EnrichmentLabel{Provenance: "read path"}, want: "source.enrichment_labels[0].key"},
		{name: "missing provenance", label: EnrichmentLabel{Key: "asserts_env"}, want: "source.enrichment_labels[0].provenance"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			document := validCorpusDocument("k8s", "gcx_live_readback", "eks")
			document.Source.EnrichmentLabels = []EnrichmentLabel{test.label}
			writeCorpusJSON(t, root, "k8s", "source", document)
			_, err := LoadCorpusDir(root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestCorpusRejectsDuplicateEnrichmentLabelKeys(t *testing.T) {
	root := t.TempDir()
	document := validCorpusDocument("k8s", "gcx_live_readback", "eks")
	document.Source.EnrichmentLabels = []EnrichmentLabel{
		{Key: "asserts_env", Provenance: "read path"},
		{Key: "asserts_env", Provenance: "read path"},
	}
	writeCorpusJSON(t, root, "k8s", "source", document)
	if _, err := LoadCorpusDir(root); err == nil || !strings.Contains(err.Error(), "source.enrichment_labels") {
		t.Fatalf("error=%v, want a duplicate-key rejection", err)
	}
}

func TestCanonicalMergePreservesDeclaredEnrichmentLabels(t *testing.T) {
	existing := validCorpusDocument("k8s", "gcx_live_readback", "eks")
	existing.Source.EnrichmentLabels = []EnrichmentLabel{{Key: "asserts_env", Provenance: "read path"}}
	existing.Inventory.Metrics = []Metric{{Name: "kubelet_running_pods", InstrumentTypes: []string{InstrumentUnknown}}}

	candidate := validCorpusDocument("k8s", "gcx_live_readback", "eks")
	candidate.Source.CapturedOn = "2026-08-26"
	candidate.Inventory.Metrics = []Metric{{Name: "kubelet_running_containers", InstrumentTypes: []string{InstrumentGauge}}}

	merged, err := CanonicalMerge(existing, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(merged.Source.EnrichmentLabels, existing.Source.EnrichmentLabels) {
		t.Fatalf("enrichment labels=%+v, a producer re-run must not drop the declaration", merged.Source.EnrichmentLabels)
	}
}

func TestCompareCorpusExemptsDeclaredSynthSelectorLabel(t *testing.T) {
	synth := New()
	synth.Provenance = &Provenance{SelectorLabels: []string{"blueprint"}}
	synth.Metrics = []Metric{{
		Name:            "aws_ec2_cpuutilization_average",
		InstrumentTypes: []string{InstrumentGauge},
		Labels: []Attribute{
			{Key: "blueprint", Values: []string{"cwinfra"}},
			{Key: "job", Values: []string{"cloud/aws/ec2"}},
		},
	}}
	document := validCorpusDocument("cw", "gcx_live_readback", "eks")
	document.Inventory.Metrics = []Metric{{
		Name:            "aws_ec2_cpuutilization_average",
		InstrumentTypes: []string{InstrumentGauge},
		Labels:          []Attribute{{Key: "job", Values: []string{"cloud/aws/ec2"}}},
	}}

	if findings := CompareCorpus(synth, []CorpusDocument{document}); len(findings) != 0 {
		t.Fatalf("findings=%+v, the declared routing selector must not contradict reality", findings)
	}
}

func TestCompareCorpusKeepsOtherSynthOnlyKeysBesideTheSelectorLabel(t *testing.T) {
	synth := New()
	synth.Provenance = &Provenance{SelectorLabels: []string{"blueprint"}}
	synth.Metrics = []Metric{{
		Name:            "coredns_panics_total",
		InstrumentTypes: []string{InstrumentCounter},
		Labels: []Attribute{
			{Key: "blueprint", Values: []string{"netobs"}},
			{Key: "job", Values: []string{"integrations/coredns"}},
			{Key: "node", Values: []string{"ip-10-0-0-1"}},
		},
	}}
	document := validCorpusDocument("k8s-addons", "k3d_lab", "k3s")
	document.Inventory.Metrics = []Metric{{
		Name:            "coredns_panics_total",
		InstrumentTypes: []string{InstrumentCounter},
		Labels:          []Attribute{{Key: "job", Values: []string{"integrations/coredns"}}},
	}}

	findings := CompareCorpus(synth, []CorpusDocument{document})
	if len(findings) != 1 {
		t.Fatalf("findings=%+v, want the invented dimension still reported", findings)
	}
	got := findings[0].Finding
	if got.Kind != KindUnexpectedLabelKey || got.Disposition != DispositionContradiction {
		t.Fatalf("finding=%+v, want a synth-only label-key contradiction", got)
	}
	if !reflect.DeepEqual(got.SynthValues, []string{"job", "node"}) {
		t.Fatalf("synth keys=%v, want the selector label removed from the synth view", got.SynthValues)
	}
}

func TestCompareCorpusKeepsUndeclaredSelectorLabelAsContradiction(t *testing.T) {
	synth := New()
	synth.Metrics = []Metric{{
		Name:            "aws_ec2_cpuutilization_average",
		InstrumentTypes: []string{InstrumentGauge},
		Labels: []Attribute{
			{Key: "blueprint", Values: []string{"cwinfra"}},
			{Key: "job", Values: []string{"cloud/aws/ec2"}},
		},
	}}
	document := validCorpusDocument("cw", "gcx_live_readback", "eks")
	document.Inventory.Metrics = []Metric{{
		Name:            "aws_ec2_cpuutilization_average",
		InstrumentTypes: []string{InstrumentGauge},
		Labels:          []Attribute{{Key: "job", Values: []string{"cloud/aws/ec2"}}},
	}}

	findings := CompareCorpus(synth, []CorpusDocument{document})
	if len(findings) != 1 || findings[0].Finding.Disposition != DispositionContradiction {
		t.Fatalf("findings=%+v, an undeclared synth-only key must still contradict", findings)
	}
}

func TestCompareCorpusExcludesDeclaredEnrichmentValues(t *testing.T) {
	synth := New()
	synth.Metrics = []Metric{{
		Name:            "kubeproxy_sync_proxy_rules_iptables_total",
		InstrumentTypes: []string{InstrumentGauge},
		Labels:          []Attribute{{Key: "ip_family", Values: []string{"IPv4", "IPv6"}}},
	}}
	document := validCorpusDocument("k8s", "gcx_live_readback", "eks")
	document.Source.EnrichmentLabels = []EnrichmentLabel{{
		Key:        "ip_family",
		Values:     []string{"<aggregated>"},
		Provenance: "Grafana Cloud Adaptive Metrics writes this marker into a retained label value when the series is aggregated away.",
	}}
	document.Inventory.Metrics = []Metric{{
		Name:            "kubeproxy_sync_proxy_rules_iptables_total",
		InstrumentTypes: []string{InstrumentGauge},
		Labels:          []Attribute{{Key: "ip_family", Values: []string{"<aggregated>", "IPv4", "IPv6"}}},
	}}

	if findings := CompareCorpus(synth, []CorpusDocument{document}); len(findings) != 0 {
		t.Fatalf("findings=%+v, a declared enrichment value must not contradict", findings)
	}

	document.Inventory.Metrics[0].Labels[0].Values = []string{"<aggregated>", "IPv4", "IPv6", "IPv9"}
	findings := CompareCorpus(synth, []CorpusDocument{document})
	if len(findings) != 1 {
		t.Fatalf("findings=%+v, an undeclared reality value must remain visible", findings)
	}
	got := findings[0].Finding
	if got.Kind != KindLabelValueContradiction || got.Disposition != DispositionCoverageGap {
		t.Fatalf("finding=%+v, want a label-value coverage gap", got)
	}
	if !reflect.DeepEqual(got.RealityValues, []string{"IPv4", "IPv6", "IPv9"}) {
		t.Fatalf("reality values=%v, want the declared enrichment value removed", got.RealityValues)
	}
}

func TestCompareCorpusKeepsEnrichmentValueKeyForKeyComparison(t *testing.T) {
	synth := New()
	synth.Metrics = []Metric{{
		Name:            "kubeproxy_sync_proxy_rules_iptables_total",
		InstrumentTypes: []string{InstrumentGauge},
		Labels:          []Attribute{{Key: "job", Values: []string{"integrations/kubernetes/kube-proxy"}}},
	}}
	document := validCorpusDocument("k8s", "gcx_live_readback", "eks")
	document.Source.EnrichmentLabels = []EnrichmentLabel{{
		Key:        "ip_family",
		Values:     []string{"<aggregated>"},
		Provenance: "Grafana Cloud Adaptive Metrics marker written into a retained label value.",
	}}
	document.Inventory.Metrics = []Metric{{
		Name:            "kubeproxy_sync_proxy_rules_iptables_total",
		InstrumentTypes: []string{InstrumentGauge},
		Labels: []Attribute{
			{Key: "ip_family", Values: []string{"<aggregated>"}},
			{Key: "job", Values: []string{"integrations/kubernetes/kube-proxy"}},
		},
	}}

	findings := CompareCorpus(synth, []CorpusDocument{document})
	if len(findings) != 1 {
		t.Fatalf("findings=%+v, a value-scoped declaration must leave the key itself as evidence", findings)
	}
	got := findings[0].Finding
	if got.Kind != KindUnexpectedLabelKey || got.Disposition != DispositionCoverageGap {
		t.Fatalf("finding=%+v, want the missing label key still reported", got)
	}
}

func TestCorpusRejectsEmptyEnrichmentLabelValue(t *testing.T) {
	root := t.TempDir()
	document := validCorpusDocument("k8s", "gcx_live_readback", "eks")
	document.Source.EnrichmentLabels = []EnrichmentLabel{{
		Key:        "ip_family",
		Values:     []string{" "},
		Provenance: "read path",
	}}
	writeCorpusJSON(t, root, "k8s", "source", document)
	if _, err := LoadCorpusDir(root); err == nil || !strings.Contains(err.Error(), "source.enrichment_labels[0].values[0]") {
		t.Fatalf("error=%v, want an empty-value rejection", err)
	}
}

func TestCorpusRejectsDuplicateEnrichmentLabelValues(t *testing.T) {
	root := t.TempDir()
	document := validCorpusDocument("k8s", "gcx_live_readback", "eks")
	document.Source.EnrichmentLabels = []EnrichmentLabel{{
		Key:        "ip_family",
		Values:     []string{"<aggregated>", "<aggregated>"},
		Provenance: "read path",
	}}
	writeCorpusJSON(t, root, "k8s", "source", document)
	if _, err := LoadCorpusDir(root); err == nil || !strings.Contains(err.Error(), "source.enrichment_labels[0].values[1]") {
		t.Fatalf("error=%v, want a duplicate-value rejection", err)
	}
}

func TestCanonicalMergePreservesDeclaredEnrichmentValues(t *testing.T) {
	existing := validCorpusDocument("k8s", "gcx_live_readback", "eks")
	existing.Source.EnrichmentLabels = []EnrichmentLabel{{
		Key:        "ip_family",
		Values:     []string{"<aggregated>"},
		Provenance: "adaptive metrics marker",
	}}
	existing.Inventory.Metrics = []Metric{{Name: "kubelet_running_pods", InstrumentTypes: []string{InstrumentUnknown}}}

	candidate := validCorpusDocument("k8s", "gcx_live_readback", "eks")
	candidate.Source.CapturedOn = "2026-08-26"
	candidate.Inventory.Metrics = []Metric{{Name: "kubelet_running_containers", InstrumentTypes: []string{InstrumentGauge}}}

	merged, err := CanonicalMerge(existing, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(merged.Source.EnrichmentLabels, existing.Source.EnrichmentLabels) {
		t.Fatalf("enrichment labels=%+v, a producer re-run must not drop declared values", merged.Source.EnrichmentLabels)
	}
}

func TestCanonicalMergeKeepsKeyScopedEnrichmentDeclarationBroad(t *testing.T) {
	existing := validCorpusDocument("k8s", "gcx_live_readback", "eks")
	existing.Source.EnrichmentLabels = []EnrichmentLabel{{Key: "service", Provenance: "read-path entity label"}}
	existing.Inventory.Metrics = []Metric{{Name: "kubelet_running_pods", InstrumentTypes: []string{InstrumentUnknown}}}

	candidate := validCorpusDocument("k8s", "gcx_live_readback", "eks")
	candidate.Source.CapturedOn = "2026-08-26"
	candidate.Source.EnrichmentLabels = []EnrichmentLabel{{Key: "service", Values: []string{"checkout"}, Provenance: "read-path entity label"}}
	candidate.Inventory.Metrics = []Metric{{Name: "kubelet_running_containers", InstrumentTypes: []string{InstrumentGauge}}}

	merged, err := CanonicalMerge(existing, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Source.EnrichmentLabels) != 1 || len(merged.Source.EnrichmentLabels[0].Values) != 0 {
		t.Fatalf("enrichment labels=%+v, a key-scoped declaration must not narrow to a value list", merged.Source.EnrichmentLabels)
	}
}

func TestLoadCorpusDirIgnoresNonAreaDirectories(t *testing.T) {
	root := t.TempDir()
	writeCorpusJSON(t, root, "k8s", "k3d-lab", validCorpusDocument("k8s", "k3d_lab", "k3s"))
	if err := os.MkdirAll(root+"/verdicts", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root+"/verdicts/coverage-verdicts.json", []byte(`{"record_version":"other"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	documents, err := LoadCorpusDir(root)
	if err != nil {
		t.Fatalf("error=%v, a sibling record directory must not be parsed as a corpus document", err)
	}
	if len(documents) != 1 || documents[0].Area != "k8s" {
		t.Fatalf("documents=%+v, want only the area-directory document", documents)
	}
}

// A reader producer's collector_version tracks the CLI build that read the store back, not
// anything about the telemetry. It moved 1.1.1 to 1.2.0 between a committed corpus and a
// routine refresh, and with the version in the merge identity the merge was REJECTED —
// leaving overwrite as the only route, which would have silently deleted every family the
// newer read-back window did not return. This test exists because that failed in the
// direction of data loss.
func TestCanonicalMergeSurvivesReadingToolVersionBumpWithoutLosingEvidence(t *testing.T) {
	existing := validCorpusDocument("k8s", "gcx_live_readback", "eks")
	existing.Source.Collector = "grafana/gcx"
	existing.Source.CollectorRole = CollectorRoleReader
	existing.Source.CollectorVersion = "1.1.1"
	existing.Source.InstrumentTypeSource = "the mechanism as first recorded"
	existing.Inventory.AddMetric("only_in_the_committed_window", "", InstrumentCounter, map[string]string{"cluster": "a"}, nil)
	existing.Inventory.AddMetric("in_both_windows", "", InstrumentCounter, map[string]string{"cluster": "a"}, nil)

	refreshed := validCorpusDocument("k8s", "gcx_live_readback", "eks")
	refreshed.Source.Collector = "grafana/gcx"
	refreshed.Source.CollectorRole = CollectorRoleReader
	refreshed.Source.CollectorVersion = "1.2.0"
	refreshed.Source.CapturedOn = "2026-08-27"
	refreshed.Source.InstrumentTypeSource = "the corrected mechanism"
	refreshed.Inventory.AddMetric("in_both_windows", "", InstrumentCounter, map[string]string{"cluster": "a"}, nil)
	refreshed.Inventory.AddMetric("only_in_the_refresh", "", InstrumentCounter, map[string]string{"cluster": "a"}, nil)

	merged, err := CanonicalMerge(existing, refreshed)
	if err != nil {
		t.Fatalf("merge across a reading-tool version bump: %v", err)
	}
	if got := metricNames(merged); !reflect.DeepEqual(got, []string{"in_both_windows", "only_in_the_committed_window", "only_in_the_refresh"}) {
		t.Fatalf("metrics=%v, want the cumulative union with nothing dropped", got)
	}
	if merged.Source.CollectorVersion != "1.2.0" {
		t.Fatalf("collector_version=%q, want the refreshed tool version recorded as provenance", merged.Source.CollectorVersion)
	}
	if merged.Source.InstrumentTypeSource != "the corrected mechanism" {
		t.Fatalf("instrument_type_source=%q, want the refresh to be able to correct the mechanism", merged.Source.InstrumentTypeSource)
	}
}

// The mirror case: for an AUDITED collector the version names the configuration the evidence
// came from, so two versions are two producers and fusing them would invent a document no
// single deployment ever produced.
func TestCanonicalMergeStillRejectsAnAuditedCollectorVersionChange(t *testing.T) {
	existing := validCorpusDocument("k8s", "k3d_lab", "k3s")
	candidate := validCorpusDocument("k8s", "k3d_lab", "k3s")
	candidate.Source.CollectorVersion = "4.5.0"
	if _, err := CanonicalMerge(existing, candidate); err == nil {
		t.Fatal("merged two audited collector versions, want a rejection")
	}
}

// Two permutations of the same producer on the same substrate are deliberately different
// collector configurations. They must stay separate documents, not fuse.
func TestPermutationSeparatesOtherwiseIdenticalCorpusIdentities(t *testing.T) {
	root := t.TempDir()
	first := validCorpusDocument("k8s", "k3d_lab", "k3s")
	second := validCorpusDocument("k8s", "k3d_lab", "k3s")
	second.Source.Permutation = "otel-receivers"
	writeCorpusJSON(t, root, "k8s", "default", first)
	writeCorpusJSON(t, root, "k8s", "otel-receivers", second)

	documents, err := LoadCorpusDir(root)
	if err != nil {
		t.Fatalf("load two permutations of one producer: %v", err)
	}
	if len(documents) != 2 {
		t.Fatalf("documents=%d, want both permutations kept", len(documents))
	}
	if _, err := CanonicalMerge(first, second); err == nil {
		t.Fatal("merged two permutations into one document, want a rejection")
	}
}

// An absent permutation keeps its pre-permutation identity, so every committed corpus file
// stays valid and keeps colliding with itself exactly as before.
func TestAbsentPermutationKeepsTheSingleDocumentIdentity(t *testing.T) {
	root := t.TempDir()
	writeCorpusJSON(t, root, "k8s", "one", validCorpusDocument("k8s", "k3d_lab", "k3s"))
	writeCorpusJSON(t, root, "k8s", "two", validCorpusDocument("k8s", "k3d_lab", "k3s"))
	if _, err := LoadCorpusDir(root); err == nil {
		t.Fatal("accepted two permutation-less documents of one producer, want a duplicate-identity rejection")
	}
}

func TestCorpusRejectsUndeclaredCollectorRole(t *testing.T) {
	document := validCorpusDocument("k8s", "k3d_lab", "k3s")
	document.Source.CollectorRole = ""
	if err := validateCorpusDocument(document); err == nil {
		t.Fatal("accepted a document that does not say what its collector version names")
	}
	document.Source.CollectorRole = "something-else"
	if err := validateCorpusDocument(document); err == nil {
		t.Fatal("accepted an unrecognised collector role")
	}
}

// A permutation-tagged document is a DIFFERENT collector configuration, so a key synthkit
// emits that this permutation does not carry is a permutation difference, not drift. Measured
// case: the OTel-native receiver permutation's pod logs carry no `cluster` label, while the
// Alloy permutation synthkit models does.
func TestPermutationDocumentNeverContradictsButStillReportsCoverage(t *testing.T) {
	document := validCorpusDocument("k8s", "k3d_lab", "k3s")
	document.Source.Permutation = "otel-receivers"
	document.Inventory.AddMetric("reality_only_family", TransportOTLPMetrics, InstrumentGauge, map[string]string{"k8s.cluster.name": "a"}, nil)
	document.Inventory.AddMetric("shared_family", TransportOTLPMetrics, InstrumentGauge, map[string]string{"k8s.cluster.name": "a"}, nil)

	synth := New()
	synth.AddMetric("shared_family", TransportOTLPMetrics, InstrumentGauge, map[string]string{"cluster": "a"}, nil)

	findings := CompareCorpus(synth, []CorpusDocument{document})
	if len(findings) == 0 {
		t.Fatal("no findings at all, want the reality-only coverage evidence kept")
	}
	sawRealityOnlyCoverage := false
	for _, finding := range findings {
		if finding.Finding.Disposition == DispositionContradiction {
			t.Fatalf("permutation document produced a contradiction: %+v", finding.Finding)
		}
		if finding.Finding.Signal == "reality_only_family" && finding.Finding.Disposition == DispositionCoverageGap {
			sawRealityOnlyCoverage = true
		}
	}
	if !sawRealityOnlyCoverage {
		t.Fatal("permutation document reported no coverage gap for the family only reality has")
	}

	// The same shapes in an untagged document are the default deployment, where a synth-only
	// key IS drift and must still fail.
	untagged := validCorpusDocument("k8s", "k3d_lab", "k3s")
	untagged.Inventory = cloneSchema(document.Inventory)
	contradictions := 0
	for _, finding := range CompareCorpus(synth, []CorpusDocument{untagged}) {
		if finding.Finding.Disposition == DispositionContradiction {
			contradictions++
		}
	}
	if contradictions == 0 {
		t.Fatal("an untagged document produced no contradiction, so the permutation rule is suppressing drift everywhere")
	}
}

// The paired half of SKT-0013.06. Once the capture receiver stopped asserting `classic` from a
// `_count` suffix, a reality entry can legitimately carry NO representation. Left ungated, that
// turned synthkit's correct classic claim into a brand-new contradiction — trading a false
// positive for a false negative, which is worse because it is invisible.
func synthNative() Schema {
	out := New()
	out.AddMetric("observed_latency_seconds", TransportPrometheusRW2, InstrumentHistogram,
		map[string]string{"job": "lab"}, &Histogram{Native: true, BucketBounds: []float64{}, NativeSchemas: []int32{3}})
	return out
}

func TestHistogramRepresentationIsAbsentEvidenceNotAClaim(t *testing.T) {
	synth := New()
	synth.AddMetric("observed_latency_seconds", TransportPrometheusRW2, InstrumentHistogram,
		map[string]string{"job": "lab"}, &Histogram{Classic: true, BucketBounds: []float64{}, NativeSchemas: []int32{}})

	// Reality recorded the family but observed no bucket series, so it proves no representation.
	realityWithoutEvidence := New()
	realityWithoutEvidence.AddMetric("observed_latency_seconds", TransportPrometheusRW2, InstrumentUnknown,
		map[string]string{"job": "lab"}, nil)
	for _, finding := range Diff(synth, realityWithoutEvidence) {
		if finding.Field == "histogram.representations" && finding.Disposition == DispositionContradiction {
			t.Fatalf("absent representation evidence contradicted synth: %+v", finding)
		}
	}

	// Reality that DID observe a representation still contradicts a synth claim that differs.
	realityWithEvidence := New()
	realityWithEvidence.AddMetric("observed_latency_seconds", TransportPrometheusRW2, InstrumentHistogram,
		map[string]string{"job": "lab"}, &Histogram{Native: true, BucketBounds: []float64{}, NativeSchemas: []int32{3}})
	contradicted := false
	for _, finding := range Diff(synth, realityWithEvidence) {
		if finding.Field == "histogram.representations" && finding.Disposition == DispositionContradiction {
			contradicted = true
		}
	}
	if !contradicted {
		t.Fatal("real representation evidence no longer contradicts a differing synth claim, so the gate lost its teeth")
	}

	// native_schemas takes the SAME gate. Synth claiming a native schema against reality that
	// observed no representation must not contradict...
	for _, finding := range Diff(synthNative(), realityWithoutEvidence) {
		if finding.Field == "histogram.native_schemas" && finding.Disposition == DispositionContradiction {
			t.Fatalf("absent representation evidence contradicted a synth native schema: %+v", finding)
		}
	}
	// ...but against reality that observed a CLASSIC representation, an empty reality schema set
	// is real evidence of "not native" and must still contradict.
	realityClassic := New()
	realityClassic.AddMetric("observed_latency_seconds", TransportPrometheusRW2, InstrumentHistogram,
		map[string]string{"job": "lab"}, &Histogram{Classic: true, BucketBounds: []float64{0.5}, NativeSchemas: []int32{}})
	schemaContradicted := false
	for _, finding := range Diff(synthNative(), realityClassic) {
		if finding.Field == "histogram.native_schemas" && finding.Disposition == DispositionContradiction {
			schemaContradicted = true
		}
	}
	if !schemaContradicted {
		t.Fatal("a synth native schema no longer contradicts an observed classic reality, so the gate over-suppressed")
	}

	// Synth with no representation must still see reality's as a coverage gap.
	synthWithout := New()
	synthWithout.AddMetric("observed_latency_seconds", TransportPrometheusRW2, InstrumentUnknown,
		map[string]string{"job": "lab"}, nil)
	gap := false
	for _, finding := range Diff(synthWithout, realityWithEvidence) {
		if finding.Field == "histogram.representations" && finding.Disposition == DispositionCoverageGap {
			gap = true
		}
	}
	if !gap {
		t.Fatal("a representation only reality has stopped being reported as a coverage gap")
	}
}

func TestLiteralStorageCountPairsWithoutInventingHistogramFamily(t *testing.T) {
	synth := New()
	synth.AddMetric("storage_operation_duration_seconds_count", TransportPrometheusRW2, InstrumentCounter,
		map[string]string{
			"cluster": "", "instance": "", "job": "", "k8s_cluster_name": "", "migrated": "",
			"node": "", "operation_name": "", "source": "", "status": "", "volume_plugin": "",
		}, nil)
	reality := New()
	reality.AddMetric("storage_operation_duration_seconds_count", TransportPrometheusRW1, InstrumentUnknown,
		map[string]string{
			"cluster": "", "instance": "", "job": "", "k8s_cluster_name": "", "migrated": "",
			"node": "", "operation_name": "", "source": "", "status": "", "volume_plugin": "",
		}, nil)

	sawUnknownEvidence := false
	for _, finding := range Diff(synth, reality) {
		if finding.Kind == KindMissingMetric || finding.Kind == KindExtraMetric || finding.Kind == KindInstrumentMismatch {
			t.Fatalf("literal storage count was folded or contradicted: %+v", finding)
		}
		if finding.Kind == KindUnknownInstrumentEvidence && finding.Signal == "storage_operation_duration_seconds_count" {
			sawUnknownEvidence = true
		}
	}
	if !sawUnknownEvidence {
		t.Fatal("unknown capture instrument evidence was hidden instead of reported as a coverage gap")
	}
}
