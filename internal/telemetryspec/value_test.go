// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func ptr[T any](v T) *T { return &v }

func TestValueModelEvalAndValidate(t *testing.T) {
	// const → numeric
	v := ValueModel{Const: ptr(2.0)}
	if got, _ := v.Eval(EvalCtx{}); got != 2.0 {
		t.Fatalf("const=%v want 2.0", got)
	}
	// const_str → string
	cs := ValueModel{ConstStr: ptr("hello")}
	if _, s := cs.Eval(EvalCtx{}); s != "hello" {
		t.Fatalf("const_str=%q want hello", s)
	}
	// enum domain enumeration (for labels): EnumDomain returns ALL values in order (determinism §3.4)
	e := ValueModel{Enum: []EnumEntry{{Value: "2xx", Weight: 9}, {Value: "5xx", Weight: 1}}}
	if got := e.EnumDomain(); !reflect.DeepEqual(got, []string{"2xx", "5xx"}) {
		t.Fatalf("domain=%v want [2xx 5xx]", got)
	}
	// ref pulls from EvalCtx correlation
	r := ValueModel{Ref: "trace_id"}
	if _, s := r.Eval(EvalCtx{Ref: map[string]string{"trace_id": "abc"}}); s != "abc" {
		t.Fatalf("ref pulled %q want abc", s)
	}
	// ref to an absent field → empty string (omitted downstream, I13)
	if _, s := r.Eval(EvalCtx{}); s != "" {
		t.Fatalf("absent ref = %q want empty", s)
	}
	// shape: Base × ShapeVal (incident-aware multiplier)
	sh := ValueModel{Shape: &ShapeModel{Base: 10}}
	if got, _ := sh.Eval(EvalCtx{ShapeVal: 2.0}); got != 20.0 {
		t.Fatalf("shape=%v want 20", got)
	}
	// shape with unset ShapeVal defaults the multiplier to 1.0 (never zeros the base)
	if got, _ := sh.Eval(EvalCtx{}); got != 10.0 {
		t.Fatalf("shape default=%v want 10", got)
	}
}

func TestValueModelValidate(t *testing.T) {
	// empty → error (must set exactly one)
	if err := (ValueModel{}).Validate(); err == nil {
		t.Fatal("empty ValueModel must error")
	}
	// two set → error
	if err := (ValueModel{Const: ptr(1.0), Ref: "x"}).Validate(); err == nil {
		t.Fatal("two-set ValueModel must error")
	}
	// each single form validates
	for name, vm := range map[string]ValueModel{
		"const":       {Const: ptr(1.0)},
		"const_str":   {ConstStr: ptr("x")},
		"enum":        {Enum: []EnumEntry{{Value: "a", Weight: 1}}},
		"int_range":   {IntRange: &IntRange{Min: 0, Max: 3}},
		"float_range": {FloatRange: &FloatRange{Min: 0, Max: 1}},
		"normal":      {Normal: &Normal{Mean: 1, Stddev: 0.1}},
		"bool":        {Bool: &BoolModel{PTrue: 0.5}},
		"shape":       {Shape: &ShapeModel{Base: 1}},
		"ref":         {Ref: "trace_id"},
	} {
		if err := vm.Validate(); err != nil {
			t.Errorf("%s must validate, got %v", name, err)
		}
	}
	// per-model bounds: int_range Min>Max rejected
	if err := (ValueModel{IntRange: &IntRange{Min: 5, Max: 1}}).Validate(); err == nil {
		t.Fatal("int_range min>max must error")
	}
	// per-model bounds: bool PTrue out of [0,1] rejected
	if err := (ValueModel{Bool: &BoolModel{PTrue: 1.5}}).Validate(); err == nil {
		t.Fatal("bool p_true>1 must error")
	}
	// per-model bounds: empty enum value rejected (would drop a label key / empty body field)
	if err := (ValueModel{Enum: []EnumEntry{{Value: "", Weight: 1}}}).Validate(); err == nil {
		t.Fatal("enum with empty value must error")
	}
	// per-model bounds: duplicate enum value rejected (would collapse series / double-count a counter)
	if err := (ValueModel{Enum: []EnumEntry{{Value: "a", Weight: 1}, {Value: "a", Weight: 2}}}).Validate(); err == nil {
		t.Fatal("enum with duplicate value must error")
	}
	// per-model bounds: normal negative stddev rejected
	if err := (ValueModel{Normal: &Normal{Mean: 1, Stddev: -1}}).Validate(); err == nil {
		t.Fatal("normal negative stddev must error")
	}
}

func TestShapeIncidentValueValidation(t *testing.T) {
	for name, input := range map[string]string{
		"missing mode": "{base: 0, incident_value: 12}",
		"blank mode":   "{base: 0, mode: '  ', incident_value: 12}",
		"nan base":     "{base: .nan, mode: error_spike, incident_value: 12}",
		"inf base":     "{base: -.inf, mode: error_spike, incident_value: 12}",
		"nan endpoint": "{base: 0, mode: error_spike, incident_value: .nan}",
		"inf endpoint": "{base: 0, mode: error_spike, incident_value: .inf}",
	} {
		t.Run(name, func(t *testing.T) {
			var v ValueModel
			if err := yaml.Unmarshal([]byte("shape: "+input), &v); err != nil {
				t.Fatal(err)
			}
			if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "shape") {
				t.Fatalf("invalid incident endpoint must reject, got %v", err)
			}
		})
	}
}

func TestShapeIncidentValueEval(t *testing.T) {
	var v ValueModel
	if err := yaml.Unmarshal([]byte("shape: {base: 4, mode: error_spike, incident_value: 12}"), &v); err != nil {
		t.Fatal(err)
	}
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ intensity, load, want float64 }{
		{0, 1, 4}, {0.25, 1, 6}, {1, 1, 12}, {0.5, 0.5, 4},
	} {
		if got, _ := v.Eval(EvalCtx{ShapeVal: tc.load, IncidentIntensity: tc.intensity}); got != tc.want {
			t.Errorf("intensity=%v load=%v got %v want %v", tc.intensity, tc.load, got, tc.want)
		}
	}
	// A zero endpoint is set, not absent, and can drive a value downward.
	v.Shape.IncidentValue = ptr(0.0)
	if got, _ := v.Eval(EvalCtx{ShapeVal: 1, IncidentIntensity: 1}); got != 0 {
		t.Fatalf("zero endpoint got %v want 0", got)
	}
	// Convex interpolation must not overflow by subtracting opposite finite endpoints.
	v.Shape.Base, v.Shape.IncidentValue = -math.MaxFloat64, ptr(math.MaxFloat64)
	if got, _ := v.Eval(EvalCtx{ShapeVal: 1, IncidentIntensity: 0.5}); got != 0 {
		t.Fatalf("finite interpolation got %v want 0", got)
	}
	// Without the option, preserve the exact legacy operation even with intensity supplied.
	v.Shape.IncidentValue = nil
	v.Shape.Base = 0.1
	for _, mult := range []float64{0, 0.3, 1, 2.5} {
		wantMult := mult
		if wantMult == 0 {
			wantMult = 1
		}
		got, _ := v.Eval(EvalCtx{ShapeVal: mult, IncidentIntensity: 0.75})
		if math.Float64bits(got) != math.Float64bits(0.1*wantMult) {
			t.Fatalf("legacy shape mult=%v changed: got %v want %v", mult, got, 0.1*wantMult)
		}
	}
}

// TestShapeIncidentAbsoluteEval: with incident_absolute the incident endpoint is not multiplied
// by load (a night/weekend load of 0.1 must not shrink a 4600 incident level), while the baseline
// share still follows load. Default (flag unset) output stays bit-identical to the old formula.
func TestShapeIncidentAbsoluteEval(t *testing.T) {
	var abs, def ValueModel
	if err := yaml.Unmarshal([]byte("shape: {base: 100, mode: latency_storm, incident_value: 4600, incident_absolute: true}"), &abs); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte("shape: {base: 100, mode: latency_storm, incident_value: 4600}"), &def); err != nil {
		t.Fatal(err)
	}
	for _, v := range []ValueModel{abs, def} {
		if err := v.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ intensity, load, want float64 }{
		{0, 0.1, 10},     // baseline follows the curve
		{0, 1, 100},      // ... unchanged at full load
		{1, 0.1, 4600},   // night: the incident level does not shrink
		{1, 0.02, 4600},  // weekend night
		{1, 1, 4600},     // peak: same level
		{0.5, 0.1, 2305}, // 0.5 x 100 x 0.1 + 0.5 x 4600
		{0.25, 1, 1225},  // 0.75 x 100 + 0.25 x 4600
	} {
		if got, _ := abs.Eval(EvalCtx{ShapeVal: tc.load, IncidentIntensity: tc.intensity}); got != tc.want {
			t.Errorf("absolute intensity=%v load=%v got %v want %v", tc.intensity, tc.load, got, tc.want)
		}
	}
	// An authoritative zero load reads zero even mid-incident; the 0-means-1.0 fallback does not.
	if got, _ := abs.Eval(EvalCtx{ShapeVal: 0, ExactLoad: true, IncidentIntensity: 1}); got != 0 {
		t.Errorf("exact zero load got %v want 0", got)
	}
	if got, _ := abs.Eval(EvalCtx{ShapeVal: 0, IncidentIntensity: 1}); got != 4600 {
		t.Errorf("zero load with fallback got %v want 4600", got)
	}
	// Default path: the incident level still scales with load (the pre-existing contract).
	if got, _ := def.Eval(EvalCtx{ShapeVal: 0.1, IncidentIntensity: 1}); got != 460 {
		t.Fatalf("default incident_value at load 0.1 got %v want 460", got)
	}
	for _, tc := range []struct{ intensity, load float64 }{{0, 0.3}, {0.25, 1}, {1, 0.1}, {0.7, 2.5}} {
		got, _ := def.Eval(EvalCtx{ShapeVal: tc.load, IncidentIntensity: tc.intensity})
		want := ((1-tc.intensity)*100 + tc.intensity*4600) * tc.load
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Errorf("default path changed: intensity=%v load=%v got %v want %v", tc.intensity, tc.load, got, want)
		}
	}
}

func TestShapeIncidentAbsoluteValidation(t *testing.T) {
	var v ValueModel
	if err := yaml.Unmarshal([]byte("shape: {base: 1, mode: latency_storm, incident_absolute: true}"), &v); err != nil {
		t.Fatal(err)
	}
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "incident_absolute requires incident_value") {
		t.Fatalf("incident_absolute without incident_value must reject, got %v", err)
	}
}

func TestValueModelKind(t *testing.T) {
	cases := map[string]ValueModel{
		KindConst:      {Const: ptr(1.0)},
		KindConstStr:   {ConstStr: ptr("x")},
		KindEnum:       {Enum: []EnumEntry{{Value: "a", Weight: 1}}},
		KindIntRange:   {IntRange: &IntRange{}},
		KindFloatRange: {FloatRange: &FloatRange{}},
		KindNormal:     {Normal: &Normal{}},
		KindBool:       {Bool: &BoolModel{}},
		KindShape:      {Shape: &ShapeModel{}},
		KindRef:        {Ref: "trace_id"},
	}
	for want, vm := range cases {
		if got := vm.Kind(); got != want {
			t.Errorf("Kind()=%q want %q", got, want)
		}
	}
	if (ValueModel{}).Kind() != "" {
		t.Error("empty ValueModel Kind() must be empty string")
	}
}
