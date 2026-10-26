// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"strings"
	"testing"
)

var whenDecision = RequestDraw{Key: "decision", Enum: []EnumEntry{{Value: "APPROVED", Weight: 7}, {Value: "REJECTED", Weight: 3}}}

func gatedLog(c *LogCondition) LogSpec {
	return LogSpec{
		Source: "app.applied",
		Body:   map[string]ValueModel{"decision": drawModel(whenDecision)},
		When:   c,
	}
}

// TestLogWhenWritesTheDrawnValueOnly: a gated line is written exactly when the request's draw of
// the gate's declaration (the same value its body field draws) is listed; an ungated line always.
func TestLogWhenWritesTheDrawnValueOnly(t *testing.T) {
	l := gatedLog(&LogCondition{RequestDraw: whenDecision, In: []string{"APPROVED"}})
	if err := l.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	written := 0
	for salt := range 2000 {
		ctx := EvalCtx{Draw: counterDraw(salt)}
		_, drawn := l.Body["decision"].Eval(ctx)
		if got, want := l.Writes(ctx), drawn == "APPROVED"; got != want {
			t.Fatalf("salt %d: drew %q, Writes = %v", salt, drawn, got)
		}
		if l.Writes(ctx) {
			written++
		}
		if !gatedLog(nil).Writes(ctx) {
			t.Fatal("an ungated line was not written")
		}
	}
	if share := float64(written) / 2000; share < 0.66 || share > 0.74 {
		t.Fatalf("APPROVED lines written for %.3f of requests, want about 0.7", share)
	}
}

func TestLogWhenValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		c    LogCondition
		want string
	}{
		"format draw":   {LogCondition{RequestDraw: RequestDraw{Key: "id", Format: "{digits:4}"}, In: []string{"1"}}, "must be an enum"},
		"blank key":     {LogCondition{RequestDraw: RequestDraw{Enum: whenDecision.Enum}, In: []string{"APPROVED"}}, "key must be non-blank"},
		"no draw":       {LogCondition{RequestDraw: RequestDraw{Key: "decision"}, In: []string{"APPROVED"}}, "need a format or an enum"},
		"empty in":      {LogCondition{RequestDraw: whenDecision}, "at least one"},
		"unknown value": {LogCondition{RequestDraw: whenDecision, In: []string{"PENDING"}}, "not one of its enum values"},
		"repeat value":  {LogCondition{RequestDraw: whenDecision, In: []string{"REJECTED", "REJECTED"}}, "repeats"},
	} {
		c := tc.c
		err := gatedLog(&c).Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

// TestValidateLogBundleMatchesGateToItsKey: a gate must select on the weights its bundle's fields
// of the same key draw with; a paired enum (same key and weights, other values) is accepted.
func TestValidateLogBundleMatchesGateToItsKey(t *testing.T) {
	gate := &LogCondition{RequestDraw: whenDecision, In: []string{"APPROVED"}}
	paired := RequestDraw{Key: "decision", Enum: []EnumEntry{{Value: "rule-ok", Weight: 7}, {Value: "rule-dq", Weight: 3}}}
	reweighted := RequestDraw{Key: "decision", Enum: []EnumEntry{{Value: "APPROVED", Weight: 5}, {Value: "REJECTED", Weight: 5}}}
	other := RequestDraw{Key: "path", Enum: []EnumEntry{{Value: "/a", Weight: 1}}}
	line := func(src string, d RequestDraw, c *LogCondition) LogSpec {
		return LogSpec{Source: src, Body: map[string]ValueModel{"f": drawModel(d)}, When: c}
	}
	if err := ValidateLogBundle([]LogSpec{line("a", whenDecision, nil), line("b", paired, gate), line("c", other, nil)}); err != nil {
		t.Fatalf("consistent bundle rejected: %v", err)
	}
	for name, logs := range map[string][]LogSpec{
		"reweighted body":   {line("a", reweighted, nil), line("b", whenDecision, gate)},
		"format body":       {line("a", RequestDraw{Key: "decision", Format: "{digits:4}"}, nil), line("b", whenDecision, gate)},
		"reweighted gate":   {line("a", whenDecision, gate), line("b", whenDecision, &LogCondition{RequestDraw: reweighted, In: []string{"APPROVED"}})},
		"shorter body enum": {line("a", RequestDraw{Key: "decision", Enum: whenDecision.Enum[:1]}, nil), line("b", whenDecision, gate)},
	} {
		if err := ValidateLogBundle(logs); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	p := Profile{Name: "p", Logs: []LogSpec{line("a", reweighted, nil), line("b", whenDecision, gate)}}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "same weights") {
		t.Errorf("profile with a mismatched gate: err = %v", err)
	}
}

func dwellLog(d *ValueModel) LogSpec {
	return LogSpec{Source: "app.step", Body: map[string]ValueModel{"msg": {ConstStr: dwellStr("step")}}, DwellSeconds: d}
}

// TestLogDwellValidationAndClamp: a dwell must be a numeric model; draws are clamped to
// [0, MaxLogDwellSeconds] and an undeclared dwell draws nothing.
func TestLogDwellValidationAndClamp(t *testing.T) {
	five := ValueModel{Const: dwellConst(5)}
	if err := dwellLog(&five).Validate(); err != nil {
		t.Fatalf("numeric dwell rejected: %v", err)
	}
	if n, ok := dwellLog(&five).DwellSecondsOf(EvalCtx{}); !ok || n != 5 {
		t.Fatalf("dwell = %v (%v), want 5", n, ok)
	}
	if _, ok := dwellLog(nil).DwellSecondsOf(EvalCtx{}); ok {
		t.Fatal("an undeclared dwell reported a draw")
	}
	for name, vm := range map[string]ValueModel{
		"const_str": {ConstStr: dwellStr("5")},
		"enum":      {Enum: []EnumEntry{{Value: "5", Weight: 1}}},
		"ref":       {Ref: "trace_id"},
	} {
		vm := vm
		if err := dwellLog(&vm).Validate(); err == nil || !strings.Contains(err.Error(), "dwell_seconds") {
			t.Errorf("%s dwell: err = %v, want a dwell_seconds rejection", name, err)
		}
	}
	for in, want := range map[float64]float64{-3: 0, 1e12: MaxLogDwellSeconds, 90: 90} {
		c := ValueModel{Const: dwellConst(in)}
		if n, _ := dwellLog(&c).DwellSecondsOf(EvalCtx{}); n != want {
			t.Errorf("dwell %v clamped to %v, want %v", in, n, want)
		}
	}
}
func dwellStr(s string) *string     { return &s }
func dwellConst(f float64) *float64 { return &f }
