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
		MetricProducerLabel: "ingest_path",
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
		Collector: "grafana/k8s-monitoring", CollectorVersion: "4.5.0", CapturedOn: "2026-08-31", MetricProducerLabel: "ingest_path",
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
		Collector: "grafana/k8s-monitoring", CollectorVersion: "4.5.0", CapturedOn: "2026-08-31", MetricProducerLabel: "missing",
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
		MetricProducerLabel: "ingest_path", MetricProducerWhenAbsent: "unlabelled",
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
		MetricProducerLabel: "ingest_path", MetricProducerWhenAbsent: "unlabelled",
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
			MetricProducerLabel: "ingest_path",
			Families: []CaptureV2FamilyRoute{
				{Name: "example_requests_total", Area: "k8s", Producers: []Producer{{Name: "promrw"}}},
				{Name: "example_duration_seconds", Area: "k8s", Producers: []Producer{{Name: "promrw"}}},
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
			MetricProducerLabel: "ingest_path",
			Families: []CaptureV2FamilyRoute{
				{Name: "example_requests_total", Area: "k8s", Producers: []Producer{{Name: "promrw"}}},
				{Name: "example_duration_seconds", Area: "k8s", Producers: []Producer{{Name: "promrw"}}},
				{Name: "example_declared_unknown", Area: "cw", Producers: []Producer{{Name: "promrw"}}},
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
			MetricProducerLabel: "ingest_path",
			Families: []CaptureV2FamilyRoute{
				{Name: "example_requests_total", Area: "k8s", Producers: []Producer{{Name: "promrw"}}},
				{Name: "example_duration_seconds", Area: "k8s", Producers: []Producer{{Name: "promrw"}}},
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
			MetricProducerLabel: "ingest_path",
			Families: []CaptureV2FamilyRoute{
				{Name: "example_requests_total", Area: "k8s", Producers: []Producer{{Name: "promrw"}}},
				{Name: "example_duration_seconds", Area: "k8s", Producers: []Producer{{Name: "promrw"}}},
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
			MetricProducerLabel: "ingest_path",
			Families: []CaptureV2FamilyRoute{
				{Name: "example_duration_seconds", Area: "k8s", Producers: []Producer{{Name: "promrw"}}},
				{Name: "example_declared_unknown", Area: "cw", Producers: []Producer{{Name: "promrw"}}},
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
			MetricProducerLabel: "ingest_path",
			Families: []CaptureV2FamilyRoute{
				{Name: "example_duration_seconds", Area: "k8s", Producers: []Producer{{Name: "promrw"}}},
				{Name: "example_declared_unknown", Area: "cw", Producers: []Producer{{Name: "promrw"}}},
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
			MetricProducerLabel: "ingest_path",
			Families: []CaptureV2FamilyRoute{
				{Name: "example_requests_total", Area: "k8s", Producers: []Producer{{Name: "promrw"}}},
				{Name: "example_duration_seconds", Area: "k8s", Producers: []Producer{{Name: "promrw"}}},
				{Name: "example_declared_unknown", Area: "cw", Producers: []Producer{{Name: "promrw"}}},
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
				manifest.Captures[0].Families = append(manifest.Captures[0].Families, CaptureV2FamilyRoute{Name: "stale_total", Area: "k8s", Producers: []Producer{{Name: "promrw"}}})
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
			name: "producer mismatch",
			edit: func(manifest *CaptureV2RoutingManifest) {
				manifest.Captures[0].Families[0].Producers = []Producer{{Name: "not-promrw"}}
			},
			want: "reviewed producers do not match direct capture identity",
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

	if got, want := len(routing.Captures), 7; got != want {
		t.Fatalf("routing capture count=%d, want %d", got, want)
	}
	if got, want := len(residueManifest.Captures), 7; got != want {
		t.Fatalf("unrouted capture count=%d, want %d", got, want)
	}

	routesByHash := make(map[string]CaptureV2CaptureRoute, len(routing.Captures))
	directNames := make(map[string]struct{})
	unroutedNames := make(map[string]struct{})
	reasonCounts := make(map[CaptureV2UnroutedReason]int)
	var directRows, unroutedRows int
	for _, route := range routing.Captures {
		routesByHash[route.SHA256] = route
		directRows += len(route.Families)
		unroutedRows += len(route.Unrouted)
		for _, family := range route.Families {
			directNames[family.Name] = struct{}{}
		}
		for _, family := range route.Unrouted {
			unroutedNames[family.Name] = struct{}{}
			reasonCounts[family.Reason]++
		}
	}
	if got, want := directRows, 10649; got != want {
		t.Fatalf("direct routing rows=%d, want %d", got, want)
	}
	if got, want := len(directNames), 2225; got != want {
		t.Fatalf("direct routing distinct names=%d, want %d", got, want)
	}
	if got, want := unroutedRows, 3612; got != want {
		t.Fatalf("unrouted rows=%d, want %d", got, want)
	}
	if got, want := len(unroutedNames), 2979; got != want {
		t.Fatalf("unrouted distinct names=%d, want %d", got, want)
	}
	if got, want := reasonCounts[CaptureV2UnroutedMissingProducerUniqueArea], 851; got != want {
		t.Fatalf("unique-area residue rows=%d, want %d", got, want)
	}
	if got, want := reasonCounts[CaptureV2UnroutedMissingProducerAndArea], 2538; got != want {
		t.Fatalf("missing-area residue rows=%d, want %d", got, want)
	}
	if got, want := reasonCounts[CaptureV2UnroutedAmbiguousDirectProducer], 223; got != want {
		t.Fatalf("ambiguous-direct-producer residue rows=%d, want %d", got, want)
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
		if entry.Name() != "capture-v2-"+route.SHA256+".json" {
			t.Fatalf("active projection filename=%q, want capture hash-keyed filename", entry.Name())
		}
		if got, want := metricNameSet(document.Inventory.Metrics), routeFamilyNameSet(route.Families); !reflect.DeepEqual(got, want) {
			t.Fatalf("active projection %q metric set does not match its direct routes", entry.Name())
		}
		activeDocuments = append(activeDocuments, document)
	}
	if got, want := len(activeDocuments), 7; got != want {
		t.Fatalf("active projection document count=%d, want %d", got, want)
	}

	loaded, err := LoadCorpusDir(filepath.Join(repositoryRoot, "reality-corpus"))
	if err != nil {
		t.Fatal(err)
	}
	loadedByHash := make(map[string]CorpusDocument, len(loaded))
	for _, document := range loaded {
		loadedByHash[document.Source.CaptureSHA256] = document
	}
	for hash, route := range routesByHash {
		document, found := loadedByHash[hash]
		if !found {
			t.Fatalf("active projection capture %q was not loaded as corpus evidence", hash)
		}
		if got, want := metricNameSet(document.Inventory.Metrics), routeFamilyNameSet(route.Families); !reflect.DeepEqual(got, want) {
			t.Fatalf("loaded projection %q metric set does not match its direct routes", hash)
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
