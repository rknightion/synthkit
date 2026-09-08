// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type checkedInCaptureV2UnroutedManifest struct {
	Version  string                                   `json:"version"`
	Captures []checkedInCaptureV2UnroutedCaptureRoute `json:"captures"`
}

type checkedInCaptureV2UnroutedCaptureRoute struct {
	SHA256   string                    `json:"sha256"`
	Families []CaptureV2UnroutedFamily `json:"families"`
}

func TestConvertCaptureV2PreservesTypeEvidenceAndSanitizesIdentity(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}

	document, err := ConvertCaptureV2(data, CaptureV2PromotionSource{
		Area:                "00-canon",
		Kind:                "synthkit_terraform_capture",
		Substrate:           "eks",
		Scope:               "cluster",
		Collector:           "grafana/k8s-monitoring",
		CollectorVersion:    "4.5.0",
		CapturedOn:          "2026-08-31",
		MetricProducerLabel: []string{"ingest_path"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCorpusDocument(document); err != nil {
		t.Fatalf("converted document must validate: %v", err)
	}
	if document.Source.CaptureSchemaVersion != "2.0.0" || document.Source.CaptureToolVersion != "2.1.0" {
		t.Fatalf("capture provenance=%+v, want schema and tool versions", document.Source)
	}
	if document.Source.CaptureSHA256 == "" || document.Source.CaptureScope != "cluster" {
		t.Fatalf("capture identity=%+v, want hash and scope", document.Source)
	}
	if document.Source.CaptureDurationSeconds != 12.5 || document.Source.CaptureWindow != "1h" || document.Source.CaptureSoakDuration != "30m" || document.Source.CaptureLoadDriven != "yes" {
		t.Fatalf("capture conditions=%+v, want source duration, window, soak, and load declaration", document.Source)
	}
	if got, want := document.Source.CaptureWarnings, []string{"UNTYPED_FAMILIES_ARE_A_SENDER_PROPERTY", "WINDOW_EXCEEDS_SOAK"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("capture warnings=%v, want %v", got, want)
	}

	unknown := metricByName(t, document.Inventory, "example_requests_total")
	if got, want := unknown.Producers, []Producer{{Name: "promrw"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("producer=%+v, want direct privacy-safe identity %v", got, want)
	}
	if got, want := unknown.InstrumentTypes, []string{InstrumentUnknown}; !reflect.DeepEqual(got, want) || unknown.InstrumentTypeSource != "undetermined" {
		t.Fatalf("unknown metric=%+v, want undetermined unknown evidence", unknown)
	}
	for _, instrument := range []string{InstrumentCounter, InstrumentGauge, InstrumentHistogram, InstrumentSummary} {
		findings := Diff(metricSchema(Metric{Name: unknown.Name, InstrumentTypes: []string{instrument}}), metricSchema(unknown))
		for _, finding := range findings {
			if finding.Kind == KindInstrumentMismatch && finding.Disposition == DispositionContradiction {
				t.Fatalf("unknown type evidence contradicted %s despite only a suffix hint: %+v", instrument, finding)
			}
		}
	}

	observed := metricByName(t, document.Inventory, "example_duration_seconds")
	if observed.InstrumentTypeSource != "observed" || observed.Histogram == nil || !observed.Histogram.Classic || !reflect.DeepEqual(observed.Histogram.BucketBounds, []float64{0.5}) {
		t.Fatalf("observed metric=%+v, want observed classic histogram evidence", observed)
	}
	if !hasMetricLabel(observed, BucketBoundLabel) {
		t.Fatalf("observed metric labels=%+v, want structural classic-histogram key %q", observed.Labels, BucketBoundLabel)
	}
	declaredUnknown := metricByName(t, document.Inventory, "example_declared_unknown")
	if declaredUnknown.InstrumentTypeSource != "metadata" {
		t.Fatalf("declared unknown metric=%+v, want metadata source retained", declaredUnknown)
	}
	for _, metric := range document.Inventory.Metrics {
		for _, label := range metric.Labels {
			if len(label.Values) != 0 || !label.ValuesElided {
				t.Fatalf("label=%+v, capture values must be elided", label)
			}
			if label.Key == "tag_private_capture" {
				t.Fatalf("identity-bearing tag label must be omitted: %+v", metric)
			}
			if label.Key == "ingest_path" {
				t.Fatalf("producer selector must not remain a corpus label: %+v", metric)
			}
		}
	}
}

func TestConvertCaptureV2BuildsCompositeProducerIdentityAndConsumesEveryIdentityLabel(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}
	var capture captureV2
	if err := json.Unmarshal(data, &capture); err != nil {
		t.Fatal(err)
	}
	family := &capture.Signals.Metrics.Families[0]
	family.Labels = append(family.Labels, struct {
		Key    string   `json:"key"`
		Values []string `json:"values"`
	}{Key: "job", Values: []string{"integrations/kubernetes/kubelet", "integrations/node_exporter"}})
	data, err = json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}

	document, err := ConvertCaptureV2(data, CaptureV2PromotionSource{
		Area: "00-canon", Kind: "synthkit_terraform_capture", Substrate: "eks", Scope: "cluster",
		Collector: "grafana/k8s-monitoring", CollectorVersion: "4.5.0", CapturedOn: "2026-08-31",
		MetricProducerLabel: []string{"ingest_path", "job"},
	})
	if err != nil {
		t.Fatal(err)
	}
	metric := metricByName(t, document.Inventory, family.Name)
	want := []Producer{
		{Name: "promrw/integrations/kubernetes/kubelet"},
		{Name: "promrw/integrations/node_exporter"},
	}
	if !reflect.DeepEqual(metric.Producers, want) {
		t.Fatalf("producers=%+v, want %+v", metric.Producers, want)
	}
	for _, key := range []string{"ingest_path", "job"} {
		if hasMetricLabel(metric, key) {
			t.Fatalf("identity label %q remained in compared labels: %+v", key, metric.Labels)
		}
	}
}

func TestConvertCaptureV2CompositeProducerKeepsTransportWhenJobIsAbsent(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := ConvertCaptureV2(data, CaptureV2PromotionSource{
		Area: "00-canon", Kind: "synthkit_terraform_capture", Substrate: "eks", Scope: "cluster",
		Collector: "grafana/k8s-monitoring", CollectorVersion: "4.5.0", CapturedOn: "2026-08-31",
		MetricProducerLabel: []string{"ingest_path", "job"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := metricByName(t, document.Inventory, "example_requests_total").Producers, []Producer{{Name: "promrw"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("producers=%+v, want transport-only fallback %+v", got, want)
	}
}

func TestConvertCaptureV2AcceptsAdditiveSchema21Header(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["capture_schema_version"] = "2.1.0"
	data, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	document, err := ConvertCaptureV2(data, CaptureV2PromotionSource{
		Area: "00-canon", Kind: "synthkit_terraform_capture", Substrate: "eks", Scope: "cluster",
		Collector: "grafana/k8s-monitoring", CollectorVersion: "4.5.0", CapturedOn: "2026-08-31", MetricProducerLabel: []string{"ingest_path"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if document.Source.CaptureSchemaVersion != "2.1.0" {
		t.Fatalf("version=%q, want 2.1.0", document.Source.CaptureSchemaVersion)
	}
}

func TestConvertCaptureV2RejectsDuplicateMetricFamilies(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}
	var capture captureV2
	if err := json.Unmarshal(data, &capture); err != nil {
		t.Fatal(err)
	}
	capture.Signals.Metrics.Families = append(capture.Signals.Metrics.Families, capture.Signals.Metrics.Families[0])
	data, err = json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ConvertCaptureV2(data, CaptureV2PromotionSource{
		Area: "00-canon", Kind: "synthkit_terraform_capture", Substrate: "eks", Scope: "cluster",
		Collector: "grafana/k8s-monitoring", CollectorVersion: "4.5.0", CapturedOn: "2026-08-31", MetricProducerLabel: []string{"ingest_path"},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate family name") {
		t.Fatalf("error=%v, want duplicate family rejection", err)
	}
}

func TestConvertCaptureV2RejectsMissingDirectProducerIdentity(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ConvertCaptureV2(data, CaptureV2PromotionSource{
		Area: "00-canon", Kind: "synthkit_terraform_capture", Substrate: "eks", Scope: "cluster",
		Collector: "grafana/k8s-monitoring", CollectorVersion: "4.5.0", CapturedOn: "2026-08-31", MetricProducerLabel: []string{"missing"},
	})
	if err == nil || !strings.Contains(err.Error(), "direct producer identity is absent") {
		t.Fatalf("error=%v, want rejection when the configured direct producer identity is absent", err)
	}
}

func TestConvertCaptureV2UsesReviewedUnlabelledProducerIdentity(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}
	var capture captureV2
	if err := json.Unmarshal(data, &capture); err != nil {
		t.Fatal(err)
	}
	name := capture.Signals.Metrics.Families[0].Name
	for i := range capture.Signals.Metrics.Families[0].Labels {
		if capture.Signals.Metrics.Families[0].Labels[i].Key == "ingest_path" {
			capture.Signals.Metrics.Families[0].Labels = append(capture.Signals.Metrics.Families[0].Labels[:i], capture.Signals.Metrics.Families[0].Labels[i+1:]...)
			break
		}
	}
	data, err = json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	document, err := ConvertCaptureV2(data, CaptureV2PromotionSource{
		Area: "00-canon", Kind: "synthkit_terraform_capture", Substrate: "eks", Scope: "cluster",
		Collector: "grafana/k8s-monitoring", CollectorVersion: "4.5.0", CapturedOn: "2026-08-31",
		MetricProducerLabel: []string{"ingest_path"}, MetricProducerWhenAbsent: "unlabelled",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := metricByName(t, document.Inventory, name).Producers, []Producer{{Name: "unlabelled"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("producer=%+v, want reviewed absent-label identity %+v", got, want)
	}
}

func TestConvertCaptureV2UsesReviewedProducerWhenLabelValuesAreBlank(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}
	var capture captureV2
	if err := json.Unmarshal(data, &capture); err != nil {
		t.Fatal(err)
	}
	name := capture.Signals.Metrics.Families[0].Name
	for i := range capture.Signals.Metrics.Families[0].Labels {
		if capture.Signals.Metrics.Families[0].Labels[i].Key == "ingest_path" {
			capture.Signals.Metrics.Families[0].Labels[i].Values = []string{"", "  "}
			break
		}
	}
	data, err = json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	document, err := ConvertCaptureV2(data, CaptureV2PromotionSource{
		Area: "00-canon", Kind: "synthkit_terraform_capture", Substrate: "eks", Scope: "cluster",
		Collector: "grafana/k8s-monitoring", CollectorVersion: "4.5.0", CapturedOn: "2026-08-31",
		MetricProducerLabel: []string{"ingest_path"}, MetricProducerWhenAbsent: "unlabelled",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := metricByName(t, document.Inventory, name).Producers, []Producer{{Name: "unlabelled"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("producer=%+v, want reviewed fallback identity %+v", got, want)
	}
}

func TestProjectCaptureV2RejectsUnmappedFamilyWithoutNameInference(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = ProjectCaptureV2(data, CaptureV2RoutingManifest{
		Version: CaptureV2RoutingManifestVersion,
		Captures: []CaptureV2CaptureRoute{{
			SHA256:              captureV2SHA256(data),
			Kind:                "synthkit_terraform_capture",
			Substrate:           "eks",
			Scope:               "cluster",
			Collector:           "grafana/k8s-monitoring",
			CollectorVersion:    "4.5.0",
			CapturedOn:          "2026-08-31",
			MetricProducerLabel: []string{"ingest_path"},
			Families: []CaptureV2FamilyRoute{
				{Name: "example_requests_total", Area: "k8s"},
				{Name: "example_duration_seconds", Area: "k8s"},
			},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "example_declared_unknown") || !strings.Contains(err.Error(), "direct producer identity requires one reviewed direct route") {
		t.Fatalf("error=%v, want unmapped family rejection without a name or prefix fallback", err)
	}
}

func TestProjectCaptureV2ProjectsExplicitFamilyRoutesIntoTheirAreas(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}

	projection, err := ProjectCaptureV2(data, CaptureV2RoutingManifest{
		Version: CaptureV2RoutingManifestVersion,
		Captures: []CaptureV2CaptureRoute{{
			SHA256:              captureV2SHA256(data),
			Kind:                "synthkit_terraform_capture",
			Substrate:           "eks",
			Scope:               "cluster",
			Collector:           "grafana/k8s-monitoring",
			CollectorVersion:    "4.5.0",
			CapturedOn:          "2026-08-31",
			MetricProducerLabel: []string{"ingest_path"},
			Families: []CaptureV2FamilyRoute{
				{Name: "example_requests_total", Area: "k8s"},
				{Name: "example_duration_seconds", Area: "k8s"},
				{Name: "example_declared_unknown", Area: "cw"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Documents) != 2 {
		t.Fatalf("documents=%d, want one document per explicit area", len(projection.Documents))
	}
	if projection.Documents[0].Area != "cw" || len(projection.Documents[0].Inventory.Metrics) != 1 || projection.Documents[0].Inventory.Metrics[0].Name != "example_declared_unknown" {
		t.Fatalf("cw document=%+v, want only its explicitly-routed family", projection.Documents[0])
	}
	if projection.Documents[1].Area != "k8s" || len(projection.Documents[1].Inventory.Metrics) != 2 {
		t.Fatalf("k8s document=%+v, want only its explicitly-routed families", projection.Documents[1])
	}
}

func TestProjectCaptureV2RejectsProducerMappingTable(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := CaptureV2RoutingManifest{
		Version: CaptureV2RoutingManifestVersion,
		Captures: []CaptureV2CaptureRoute{{
			SHA256: captureV2SHA256(data), Kind: "synthkit_terraform_capture", Substrate: "eks", Scope: "cluster",
			Collector: "grafana/k8s-monitoring", CollectorVersion: "4.5.0", CapturedOn: "2026-08-31", MetricProducerLabel: []string{"ingest_path"},
			Families: []CaptureV2FamilyRoute{
				{Name: "example_requests_total", Area: "k8s", Producers: []Producer{{Name: "promrw"}}},
				{Name: "example_duration_seconds", Area: "k8s"},
				{Name: "example_declared_unknown", Area: "cw"},
			},
		}},
	}
	_, err = ProjectCaptureV2(data, manifest)
	if err == nil || !strings.Contains(err.Error(), "must derive producers from metric_producer_label") {
		t.Fatalf("error=%v, want producer mapping table rejection", err)
	}
}

func TestProjectCaptureV2ProjectsReviewedLimitationsIntoEveryDocument(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}

	limitations := []CaptureLimitation{
		{ID: "selector-gap", Text: "One reviewed selector did not discover the expected targets."},
		{ID: "retained-read", Text: "The source was retained telemetry rather than a pre-destroy capture."},
	}
	projection, err := ProjectCaptureV2(data, CaptureV2RoutingManifest{
		Version: CaptureV2RoutingManifestVersion,
		Captures: []CaptureV2CaptureRoute{{
			SHA256:              captureV2SHA256(data),
			Kind:                "synthkit_terraform_capture",
			Substrate:           "eks",
			Scope:               "cluster",
			Collector:           "grafana/k8s-monitoring",
			CollectorVersion:    "4.5.0",
			CapturedOn:          "2026-08-31",
			MetricProducerLabel: []string{"ingest_path"},
			Limitations:         limitations,
			Families: []CaptureV2FamilyRoute{
				{Name: "example_requests_total", Area: "k8s"},
				{Name: "example_duration_seconds", Area: "k8s"},
				{Name: "example_declared_unknown", Area: "cw"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Documents) != 2 {
		t.Fatalf("documents=%d, want two routed areas", len(projection.Documents))
	}
	for _, document := range projection.Documents {
		if !reflect.DeepEqual(document.Source.CaptureLimitations, limitations) {
			t.Fatalf("%s capture_limitations=%+v, want reviewed list %+v", document.Area, document.Source.CaptureLimitations, limitations)
		}
	}
}

func TestCaptureLimitationsValidateAndCanonicalMergeByID(t *testing.T) {
	existing := validCorpusDocument("k8s", "producer", "k3s")
	existing.Source.CaptureLimitations = []CaptureLimitation{
		{ID: "shared", Text: "Earlier reviewed text."},
		{ID: "existing-only", Text: "Existing reviewed limitation."},
	}
	existing.Inventory.AddMetric("requests_total", TransportPrometheusRW2, InstrumentCounter, nil, nil)

	candidate := validCorpusDocument("k8s", "producer", "k3s")
	candidate.Source.CaptureLimitations = []CaptureLimitation{
		{ID: "shared", Text: "Candidate reviewed text wins."},
		{ID: "candidate-only", Text: "Candidate reviewed limitation."},
	}
	candidate.Inventory.AddMetric("requests_total", TransportPrometheusRW2, InstrumentCounter, map[string]string{"method": "GET"}, nil)

	merged, err := CanonicalMerge(existing, candidate)
	if err != nil {
		t.Fatal(err)
	}
	want := []CaptureLimitation{
		{ID: "candidate-only", Text: "Candidate reviewed limitation."},
		{ID: "existing-only", Text: "Existing reviewed limitation."},
		{ID: "shared", Text: "Candidate reviewed text wins."},
	}
	if !reflect.DeepEqual(merged.Source.CaptureLimitations, want) {
		t.Fatalf("capture_limitations=%+v, want union by id %+v", merged.Source.CaptureLimitations, want)
	}

	for name, edit := range map[string]func(*CorpusDocument){
		"blank id": func(document *CorpusDocument) {
			document.Source.CaptureLimitations = []CaptureLimitation{{ID: " ", Text: "reviewed"}}
		},
		"blank text": func(document *CorpusDocument) {
			document.Source.CaptureLimitations = []CaptureLimitation{{ID: "reviewed", Text: " "}}
		},
		"duplicate id": func(document *CorpusDocument) {
			document.Source.CaptureLimitations = []CaptureLimitation{
				{ID: "reviewed", Text: "First reviewed limitation."},
				{ID: "reviewed", Text: "Second reviewed limitation."},
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			document := validCorpusDocument("k8s", "producer", "k3s")
			edit(&document)
			if err := validateCorpusDocument(document); err == nil {
				t.Fatalf("accepted capture limitation with %s", name)
			}
		})
	}
}

func TestCaptureLimitationsAreOptionalForExistingDocumentsAndManifests(t *testing.T) {
	document := validCorpusDocument("k8s", "producer", "k3s")
	if err := validateCorpusDocument(document); err != nil {
		t.Fatalf("document without capture_limitations: %v", err)
	}

	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := CaptureV2RoutingManifest{
		Version: CaptureV2RoutingManifestVersion,
		Captures: []CaptureV2CaptureRoute{{
			SHA256: captureV2SHA256(data), Kind: "synthkit_terraform_capture", Substrate: "eks", Scope: "cluster",
			Collector: "grafana/k8s-monitoring", CollectorVersion: "4.5.0", CapturedOn: "2026-08-31", MetricProducerLabel: []string{"ingest_path"},
			Families: []CaptureV2FamilyRoute{
				{Name: "example_requests_total", Area: "k8s"},
				{Name: "example_duration_seconds", Area: "k8s"},
				{Name: "example_declared_unknown", Area: "cw"},
			},
		}},
	}
	if _, err := ProjectCaptureV2(data, manifest); err != nil {
		t.Fatalf("manifest without limitations: %v", err)
	}
}

func TestProjectCaptureV2UsesExplicitRouteForProducerlessFamily(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}
	var capture captureV2
	if err := json.Unmarshal(data, &capture); err != nil {
		t.Fatal(err)
	}
	for i := range capture.Signals.Metrics.Families[2].Labels {
		if capture.Signals.Metrics.Families[2].Labels[i].Key == "ingest_path" {
			capture.Signals.Metrics.Families[2].Labels = append(capture.Signals.Metrics.Families[2].Labels[:i], capture.Signals.Metrics.Families[2].Labels[i+1:]...)
			break
		}
	}
	data, err = json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}

	projection, err := ProjectCaptureV2(data, CaptureV2RoutingManifest{
		Version: CaptureV2RoutingManifestVersion,
		Captures: []CaptureV2CaptureRoute{{
			SHA256:              captureV2SHA256(data),
			Kind:                "synthkit_terraform_capture",
			Substrate:           "eks",
			Scope:               "cluster",
			Collector:           "grafana/k8s-monitoring",
			CollectorVersion:    "4.5.0",
			CapturedOn:          "2026-08-31",
			MetricProducerLabel: []string{"ingest_path"},
			Families: []CaptureV2FamilyRoute{
				{Name: "example_requests_total", Area: "k8s"},
				{Name: "example_duration_seconds", Area: "k8s"},
			},
			Unrouted: []CaptureV2UnroutedFamily{{
				Name:   "example_declared_unknown",
				Reason: CaptureV2UnroutedMissingProducerUniqueArea,
				Area:   "cw",
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Unrouted) != 1 || projection.Unrouted[0].Name != "example_declared_unknown" || projection.Unrouted[0].Reason != CaptureV2UnroutedMissingProducerUniqueArea || projection.Unrouted[0].Area != "cw" {
		t.Fatalf("unrouted=%+v, want the explicit producerless residue", projection.Unrouted)
	}
	if _, err := projection.DocumentForFamily("example_declared_unknown"); err == nil || !strings.Contains(err.Error(), "unrouted") {
		t.Fatalf("unrouted comparison lookup error=%v, want fail-closed error", err)
	}
	for _, document := range projection.Documents {
		for _, metric := range document.Inventory.Metrics {
			if metric.Name == "example_declared_unknown" {
				t.Fatalf("producerless metric promoted into %+v", document)
			}
		}
	}
}

func TestProjectCaptureV2RejectsProducerlessFamilyWithoutAnExplicitUnroutedReason(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}
	var capture captureV2
	if err := json.Unmarshal(data, &capture); err != nil {
		t.Fatal(err)
	}
	capture.Signals.Metrics.Families[2].Labels = nil
	data, err = json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ProjectCaptureV2(data, CaptureV2RoutingManifest{
		Version: CaptureV2RoutingManifestVersion,
		Captures: []CaptureV2CaptureRoute{{
			SHA256:              captureV2SHA256(data),
			Kind:                "synthkit_terraform_capture",
			Substrate:           "eks",
			Scope:               "cluster",
			Collector:           "grafana/k8s-monitoring",
			CollectorVersion:    "4.5.0",
			CapturedOn:          "2026-08-31",
			MetricProducerLabel: []string{"ingest_path"},
			Families: []CaptureV2FamilyRoute{
				{Name: "example_requests_total", Area: "k8s"},
				{Name: "example_duration_seconds", Area: "k8s"},
			},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "no reviewed direct route or explicit unrouted reason") {
		t.Fatalf("error=%v, want explicit producerless residue requirement", err)
	}
}

func TestProjectCaptureV2KeepsAmbiguousDirectProducerAsExactResidue(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}

	projection, err := ProjectCaptureV2(data, CaptureV2RoutingManifest{
		Version: CaptureV2RoutingManifestVersion,
		Captures: []CaptureV2CaptureRoute{{
			SHA256:              captureV2SHA256(data),
			Kind:                "synthkit_terraform_capture",
			Substrate:           "eks",
			Scope:               "cluster",
			Collector:           "grafana/k8s-monitoring",
			CollectorVersion:    "4.5.0",
			CapturedOn:          "2026-08-31",
			MetricProducerLabel: []string{"ingest_path"},
			Families: []CaptureV2FamilyRoute{
				{Name: "example_duration_seconds", Area: "k8s"},
				{Name: "example_declared_unknown", Area: "cw"},
			},
			Unrouted: []CaptureV2UnroutedFamily{{
				Name:   "example_requests_total",
				Reason: CaptureV2UnroutedAmbiguousDirectProducer,
				Area:   "k8s",
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := projection.Unrouted, []CaptureV2UnroutedFamily{{
		Name: "example_requests_total", Reason: CaptureV2UnroutedAmbiguousDirectProducer, Area: "k8s",
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unrouted=%+v, want exact ambiguous direct residue %+v", got, want)
	}
	if _, err := projection.DocumentForFamily("example_requests_total"); err == nil || !strings.Contains(err.Error(), "ambiguous_direct_producer") {
		t.Fatalf("ambiguous direct lookup error=%v, want fail-closed residue", err)
	}
	for _, document := range projection.Documents {
		for _, metric := range document.Inventory.Metrics {
			if metric.Name == "example_requests_total" {
				t.Fatalf("ambiguous direct metric was promoted into %q", document.Area)
			}
		}
	}
}

func TestProjectCaptureV2RejectsAmbiguousDirectResidueWithoutDirectIdentity(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}
	var capture captureV2
	if err := json.Unmarshal(data, &capture); err != nil {
		t.Fatal(err)
	}
	capture.Signals.Metrics.Families[0].Labels = nil
	data, err = json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ProjectCaptureV2(data, CaptureV2RoutingManifest{
		Version: CaptureV2RoutingManifestVersion,
		Captures: []CaptureV2CaptureRoute{{
			SHA256:              captureV2SHA256(data),
			Kind:                "synthkit_terraform_capture",
			Substrate:           "eks",
			Scope:               "cluster",
			Collector:           "grafana/k8s-monitoring",
			CollectorVersion:    "4.5.0",
			CapturedOn:          "2026-08-31",
			MetricProducerLabel: []string{"ingest_path"},
			Families: []CaptureV2FamilyRoute{
				{Name: "example_duration_seconds", Area: "k8s"},
				{Name: "example_declared_unknown", Area: "cw"},
			},
			Unrouted: []CaptureV2UnroutedFamily{{
				Name: "example_requests_total", Reason: CaptureV2UnroutedAmbiguousDirectProducer, Area: "k8s",
			}},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "ambiguous direct producer residue requires direct producer identity") {
		t.Fatalf("error=%v, want direct identity requirement for ambiguity residue", err)
	}
}

func TestProjectCaptureV2FailsClosedForInvalidReviewedRoutes(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}

	base := CaptureV2RoutingManifest{
		Version: CaptureV2RoutingManifestVersion,
		Captures: []CaptureV2CaptureRoute{{
			SHA256:              captureV2SHA256(data),
			Kind:                "synthkit_terraform_capture",
			Substrate:           "eks",
			Scope:               "cluster",
			Collector:           "grafana/k8s-monitoring",
			CollectorVersion:    "4.5.0",
			CapturedOn:          "2026-08-31",
			MetricProducerLabel: []string{"ingest_path"},
			Families: []CaptureV2FamilyRoute{
				{Name: "example_requests_total", Area: "k8s"},
				{Name: "example_duration_seconds", Area: "k8s"},
				{Name: "example_declared_unknown", Area: "cw"},
			},
		}},
	}

	tests := []struct {
		name string
		edit func(*CaptureV2RoutingManifest)
		want string
	}{
		{
			name: "missing family route",
			edit: func(manifest *CaptureV2RoutingManifest) {
				manifest.Captures[0].Families = manifest.Captures[0].Families[:2]
			},
			want: "example_declared_unknown\": direct producer identity requires one reviewed direct route",
		},
		{
			name: "stale family route",
			edit: func(manifest *CaptureV2RoutingManifest) {
				manifest.Captures[0].Families = append(manifest.Captures[0].Families, CaptureV2FamilyRoute{Name: "stale_total", Area: "k8s"})
			},
			want: "reviewed family \"stale_total\" is absent",
		},
		{
			name: "duplicate family route",
			edit: func(manifest *CaptureV2RoutingManifest) {
				manifest.Captures[0].Families = append(manifest.Captures[0].Families, manifest.Captures[0].Families[0])
			},
			want: "has duplicate routes",
		},
		{
			name: "duplicate unrouted family",
			edit: func(manifest *CaptureV2RoutingManifest) {
				manifest.Captures[0].Families = manifest.Captures[0].Families[:2]
				residue := CaptureV2UnroutedFamily{Name: "example_declared_unknown", Reason: CaptureV2UnroutedMissingProducerAndArea}
				manifest.Captures[0].Unrouted = []CaptureV2UnroutedFamily{residue, residue}
			},
			want: "unrouted family \"example_declared_unknown\" is duplicated",
		},
		{
			name: "unknown area",
			edit: func(manifest *CaptureV2RoutingManifest) {
				manifest.Captures[0].Families[0].Area = "unreviewed"
			},
			want: "area \"unreviewed\" is not allowed",
		},
		{
			name: "producer mapping table",
			edit: func(manifest *CaptureV2RoutingManifest) {
				manifest.Captures[0].Families[0].Producers = []Producer{{Name: "not-promrw"}}
			},
			want: "must derive producers from metric_producer_label",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := base
			manifest.Captures = append([]CaptureV2CaptureRoute(nil), base.Captures...)
			manifest.Captures[0].Families = append([]CaptureV2FamilyRoute(nil), base.Captures[0].Families...)
			test.edit(&manifest)

			_, err := ProjectCaptureV2(data, manifest)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestCheckedInCaptureV2ProjectionIsActiveAndMatchesManifests(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	manifestsPath := filepath.Join(repositoryRoot, "reality-corpus", "manifests")

	routingData, err := os.ReadFile(filepath.Join(manifestsPath, "capture-v2-routing.json"))
	if err != nil {
		t.Fatal(err)
	}
	var routing CaptureV2RoutingManifest
	if err := json.Unmarshal(routingData, &routing); err != nil {
		t.Fatal(err)
	}
	if err := validateCaptureV2RoutingManifest(routing); err != nil {
		t.Fatalf("checked-in routing manifest: %v", err)
	}

	residueData, err := os.ReadFile(filepath.Join(manifestsPath, "capture-v2-unrouted.json"))
	if err != nil {
		t.Fatal(err)
	}
	var residueManifest checkedInCaptureV2UnroutedManifest
	if err := json.Unmarshal(residueData, &residueManifest); err != nil {
		t.Fatal(err)
	}
	if residueManifest.Version != CaptureV2RoutingManifestVersion {
		t.Fatalf("unrouted manifest version=%q, want %q", residueManifest.Version, CaptureV2RoutingManifestVersion)
	}

	if len(routing.Captures) == 0 {
		t.Fatal("routing manifest has no captures")
	}
	routesByHash := make(map[string]CaptureV2CaptureRoute, len(routing.Captures))
	identity := func(area, kind, substrate, collector, version string) string {
		return strings.Join([]string{area, kind, substrate, collector, version}, "\x00")
	}
	expected := map[string]map[string]struct{}{}
	for _, route := range routing.Captures {
		if got, want := route.MetricProducerLabel, []string{"rksy_ingest", "job"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("capture %s identity labels=%v", route.SHA256, got)
		}
		routesByHash[route.SHA256] = route
		for _, family := range route.Families {
			if len(family.Producers) != 0 {
				t.Fatalf("capture %s family %s carries a producer mapping", route.SHA256, family.Name)
			}
			key := identity(family.Area, route.Kind, route.Substrate, route.Collector, route.CollectorVersion)
			if expected[key] == nil {
				expected[key] = map[string]struct{}{}
			}
			expected[key][family.Name] = struct{}{}
		}
	}

	residueByHash := make(map[string][]CaptureV2UnroutedFamily, len(residueManifest.Captures))
	for _, capture := range residueManifest.Captures {
		residueByHash[capture.SHA256] = capture.Families
	}
	if !reflect.DeepEqual(residueByHash, routingUnroutedByHash(routing)) {
		t.Fatal("unrouted manifest does not exactly mirror the routing manifest residue")
	}

	activePath := filepath.Join(repositoryRoot, "reality-corpus", "00-canon")
	entries, err := os.ReadDir(activePath)
	if err != nil {
		t.Fatal(err)
	}
	activeDocuments := make([]CorpusDocument, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		document, err := loadCorpusFile(filepath.Join(activePath, entry.Name()))
		if err != nil {
			t.Fatalf("load active projection %q: %v", entry.Name(), err)
		}
		if document.Area != "00-canon" {
			t.Fatalf("active projection %q area=%q, want 00-canon", entry.Name(), document.Area)
		}
		route, found := routesByHash[document.Source.CaptureSHA256]
		if !found {
			t.Fatalf("active projection %q has unexpected capture hash %q", entry.Name(), document.Source.CaptureSHA256)
		}
		// File paths identify cumulative documents, not the newest raw capture.
		// The latest provenance must still name a reviewed route of this identity.
		if route.Kind != document.Source.Kind || route.Substrate != document.Source.Substrate || route.Collector != document.Source.Collector || route.CollectorVersion != document.Source.CollectorVersion {
			t.Fatalf("capture provenance changed document identity: %s", entry.Name())
		}
		key := identity(document.Area, document.Source.Kind, document.Source.Substrate, document.Source.Collector, document.Source.CollectorVersion)
		if got, want := metricNameSet(document.Inventory.Metrics), expected[key]; !reflect.DeepEqual(got, want) {
			t.Fatalf("cumulative projection %s does not exactly cover the union of reviewed routes", entry.Name())
		}
		delete(expected, key)
		activeDocuments = append(activeDocuments, document)
	}
	if len(expected) != 0 {
		t.Fatalf("%d reviewed corpus identities have no active document", len(expected))
	}
	// Exercise the actual loader, including its duplicate-identity rejection.
	loaded, err := LoadCorpusDir("../../reality-corpus")
	if err != nil {
		t.Fatal(err)
	}
	for _, active := range activeDocuments {
		found := false
		for _, document := range loaded {
			if matchingCorpusIdentity(active, document) == nil {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("active projection %s was not loaded", active.Source.CaptureSHA256)
		}
	}

	projection := CaptureV2Projection{Documents: activeDocuments}
	for _, route := range routing.Captures {
		projection.Unrouted = append(projection.Unrouted, route.Unrouted...)
	}
	if len(projection.Unrouted) == 0 {
		t.Fatal("routing manifest holds no unrouted residue")
	}
	if _, err := projection.DocumentForFamily(projection.Unrouted[0].Name); err == nil || !strings.Contains(err.Error(), "unrouted") {
		t.Fatalf("candidate residue lookup error=%v, want fail-closed unrouted error", err)
	}
}

func routingUnroutedByHash(manifest CaptureV2RoutingManifest) map[string][]CaptureV2UnroutedFamily {
	result := make(map[string][]CaptureV2UnroutedFamily, len(manifest.Captures))
	for _, capture := range manifest.Captures {
		result[capture.SHA256] = capture.Unrouted
	}
	return result
}

func routeFamilyNameSet(families []CaptureV2FamilyRoute) map[string]struct{} {
	result := make(map[string]struct{}, len(families))
	for _, family := range families {
		result[family.Name] = struct{}{}
	}
	return result
}

func metricNameSet(metrics []Metric) map[string]struct{} {
	result := make(map[string]struct{}, len(metrics))
	for _, metric := range metrics {
		result[metric.Name] = struct{}{}
	}
	return result
}

func hasMetricLabel(metric Metric, key string) bool {
	for _, label := range metric.Labels {
		if label.Key == key {
			return true
		}
	}
	return false
}

func metricByName(t *testing.T, schema Schema, name string) Metric {
	t.Helper()
	for _, metric := range schema.Metrics {
		if metric.Name == name {
			return metric
		}
	}
	t.Fatalf("metric %q missing from %+v", name, schema.Metrics)
	return Metric{}
}

func TestCaptureV2DoesNotInventUnobservedProducerPairs(t *testing.T) {
	var family captureV2Metric
	if err := json.Unmarshal([]byte(`{"name":"shared_total","type":"counter","type_source":"observed","labels":[{"key":"rksy_ingest","values":["promrw","dbo11y"]},{"key":"job","values":["integration/postgresql","integrations/db-o11y"]}]}`), &family); err != nil {
		t.Fatal(err)
	}
	metric, err := convertCaptureV2Metric(family, []string{"rksy_ingest", "job"}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []Producer{{Name: "dbo11y"}, {Name: "promrw"}}
	if !reflect.DeepEqual(metric.Producers, want) {
		t.Fatalf("unpaired family values invented producer pairs: got=%+v want=%+v", metric.Producers, want)
	}
	for _, key := range []string{"rksy_ingest", "job"} {
		if hasMetricLabel(metric, key) {
			t.Fatalf("identity label %s became a compared label", key)
		}
	}
}

func TestCaptureV2RejectsInvalidIdentityKeys(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "capture-v2-sanitized.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, keys := range [][]string{{""}, {" ingest_path"}, {"ingest_path "}, {"ingest_path", "ingest_path"}} {
		_, err := ConvertCaptureV2(data, CaptureV2PromotionSource{Area: "00-canon", Kind: "synthkit_terraform_capture", Substrate: "eks", Scope: "cluster", Collector: "grafana/k8s-monitoring", CollectorVersion: "4.5.0", CapturedOn: "2026-08-31", MetricProducerLabel: keys, MetricProducerWhenAbsent: "promrw"})
		if err == nil {
			t.Errorf("invalid keys %q accepted", keys)
		}
	}
}
