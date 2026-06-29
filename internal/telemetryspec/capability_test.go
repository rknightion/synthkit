// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"strings"
	"testing"
)

func TestCapabilityMatrix_LabelRules(t *testing.T) {
	// high-card ref in a metric label → rejected with a high-card-specific message
	m := MetricSpec{Name: "x", Instrument: "gauge", Value: ValueModel{Const: ptr(1.0)},
		Labels: map[string]ValueModel{"trace_id": {Ref: "trace_id"}}}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "high-card") {
		t.Fatalf("high-card ref in label must be rejected as high-card, got %v", err)
	}
	// low-card ref in a label → still rejected (labels are const/const_str/enum only — determinism)
	m2 := MetricSpec{Name: "x", Instrument: "gauge", Value: ValueModel{Const: ptr(1.0)},
		Labels: map[string]ValueModel{"route": {Ref: "route"}}}
	if err := m2.Validate(); err == nil {
		t.Fatal("ref (even low-card) as a label source must be rejected for -dump determinism")
	}
	// non-enum numeric model as a label → rejected (determinism §3.4)
	m3 := MetricSpec{Name: "x", Instrument: "gauge", Value: ValueModel{Const: ptr(1.0)},
		Labels: map[string]ValueModel{"k": {Normal: &Normal{Mean: 1, Stddev: 1}}}}
	if err := m3.Validate(); err == nil {
		t.Fatal("normal label source must be rejected")
	}
	// const / const_str / enum labels → allowed
	m4 := MetricSpec{Name: "x", Instrument: "gauge", Value: ValueModel{Const: ptr(1.0)},
		Labels: map[string]ValueModel{
			"a": {ConstStr: ptr("v")},
			"b": {Enum: []EnumEntry{{Value: "2xx", Weight: 9}, {Value: "5xx", Weight: 1}}},
		}}
	if err := m4.Validate(); err != nil {
		t.Fatalf("const/const_str/enum labels must be allowed: %v", err)
	}
}

func TestCapabilityMatrix_BodyAndAttrAllowHighCardRef(t *testing.T) {
	// log BODY allows high-card refs + arbitrary models (the correlation glue + retry/fallback realism)
	l := LogSpec{Source: "portkey", Body: map[string]ValueModel{
		"trace_id":    {Ref: "trace_id"},
		"ai_model":    {Ref: "model"},
		"fallback":    {Bool: &BoolModel{PTrue: 0.03}},
		"retry_count": {IntRange: &IntRange{Min: 0, Max: 3, PZero: 0.95}},
	}}
	if err := l.Validate(); err != nil {
		t.Fatalf("high-card ref + arbitrary models in body must be allowed: %v", err)
	}
	// span ATTRIBUTES allow high-card refs
	s := SpanSpec{NameTemplate: "POST {route}", Kind: "server", Attributes: map[string]ValueModel{
		"http.response.status_code": {Ref: "status"},
		"portkey_trace_id":          {Ref: "portkey_trace_id"},
	}}
	if err := s.Validate(); err != nil {
		t.Fatalf("high-card ref in span attrs must be allowed: %v", err)
	}
	// but a high-card ref in a STREAM label → rejected
	l2 := LogSpec{Source: "portkey", StreamLabels: map[string]ValueModel{"trace_id": {Ref: "trace_id"}}}
	if err := l2.Validate(); err == nil {
		t.Fatal("high-card ref in stream_labels must be rejected")
	}
}

func TestCapabilityMatrix_MetricValueMustBeNumeric(t *testing.T) {
	// enum (string) as a metric value → rejected
	m := MetricSpec{Name: "x", Instrument: "counter", Value: ValueModel{Enum: []EnumEntry{{Value: "a", Weight: 1}}}}
	if err := m.Validate(); err == nil {
		t.Fatal("enum metric value must be rejected (not numeric)")
	}
	// const_str as a metric value → rejected
	m2 := MetricSpec{Name: "x", Instrument: "counter", Value: ValueModel{ConstStr: ptr("a")}}
	if err := m2.Validate(); err == nil {
		t.Fatal("const_str metric value must be rejected")
	}
	// ref as a metric value → rejected
	m3 := MetricSpec{Name: "x", Instrument: "counter", Value: ValueModel{Ref: "status"}}
	if err := m3.Validate(); err == nil {
		t.Fatal("ref metric value must be rejected")
	}
	// shape / int_range / normal values → allowed
	for name, vm := range map[string]ValueModel{
		"shape":     {Shape: &ShapeModel{Base: 5}},
		"int_range": {IntRange: &IntRange{Min: 1, Max: 10}},
		"normal":    {Normal: &Normal{Mean: 0.3, Stddev: 0.1}},
	} {
		mm := MetricSpec{Name: "x", Instrument: "gauge", Value: vm}
		if err := mm.Validate(); err != nil {
			t.Errorf("%s metric value must be allowed: %v", name, err)
		}
	}
}
