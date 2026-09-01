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
