// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"reflect"
	"testing"
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
