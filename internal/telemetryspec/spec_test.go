// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import "testing"

func TestMetricSpecHistogram(t *testing.T) {
	// histogram requires buckets
	m := MetricSpec{Name: "h", Instrument: "histogram", Value: ValueModel{Normal: &Normal{Mean: 0.3, Stddev: 0.1}}}
	if err := m.Validate(); err == nil {
		t.Fatal("histogram without buckets must error")
	}
	// buckets must be strictly ascending
	m.Buckets = []float64{1, 0.5, 2}
	if err := m.Validate(); err == nil {
		t.Fatal("non-ascending buckets must error")
	}
	// valid histogram
	m.Buckets = []float64{0.1, 0.5, 1, 5}
	m.LEStyle = LEStyleDotZero
	if err := m.Validate(); err != nil {
		t.Fatalf("valid histogram must pass: %v", err)
	}
	// invalid le_style
	m.LEStyle = "weird"
	if err := m.Validate(); err == nil {
		t.Fatal("invalid le_style must error")
	}
	// buckets on a gauge → error
	g := MetricSpec{Name: "g", Instrument: "gauge", Value: ValueModel{Const: ptr(1.0)}, Buckets: []float64{1}}
	if err := g.Validate(); err == nil {
		t.Fatal("buckets on a gauge must error")
	}
}

func TestInstrumentAndScopeValidation(t *testing.T) {
	if err := (MetricSpec{Name: "x", Instrument: "summary", Value: ValueModel{Const: ptr(1.0)}}).Validate(); err == nil {
		t.Fatal("invalid instrument must error")
	}
	if err := (MetricSpec{Name: "x", Instrument: "gauge", Scope: "galaxy", Value: ValueModel{Const: ptr(1.0)}}).Validate(); err == nil {
		t.Fatal("invalid scope must error")
	}
	if err := (MetricSpec{Name: "", Instrument: "gauge", Value: ValueModel{Const: ptr(1.0)}}).Validate(); err == nil {
		t.Fatal("empty metric name must error")
	}
}

func TestSpanKindValidation(t *testing.T) {
	if err := (SpanSpec{NameTemplate: "x", Kind: "weird"}).Validate(); err == nil {
		t.Fatal("invalid span kind must error")
	}
	if err := (SpanSpec{NameTemplate: ""}).Validate(); err == nil {
		t.Fatal("empty name_template must error")
	}
	if err := (SpanSpec{NameTemplate: "GET /", Kind: "server"}).Validate(); err != nil {
		t.Fatalf("valid span must pass: %v", err)
	}
}

func TestProfileValidate(t *testing.T) {
	// empty profile (no metrics/logs/spans) → error
	if err := (Profile{Name: "p"}).Validate(); err == nil {
		t.Fatal("profile declaring nothing must error")
	}
	// unnamed profile → error
	good := []MetricSpec{{Name: "m", Instrument: "gauge", Value: ValueModel{Const: ptr(1.0)}}}
	if err := (Profile{Metrics: good}).Validate(); err == nil {
		t.Fatal("unnamed profile must error")
	}
	// valid profile
	if err := (Profile{Name: "p", Metrics: good}).Validate(); err != nil {
		t.Fatalf("valid profile must pass: %v", err)
	}
	// a bad contained spec bubbles up
	bad := Profile{Name: "p", Logs: []LogSpec{{Source: "s", StreamLabels: map[string]ValueModel{"trace_id": {Ref: "trace_id"}}}}}
	if err := bad.Validate(); err == nil {
		t.Fatal("profile with a high-card stream label must error")
	}
}
