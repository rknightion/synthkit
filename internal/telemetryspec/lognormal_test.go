// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"bytes"
	"math/rand/v2"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestLognormalHitsMedianAndP95: a lognormal value model decoded from YAML reproduces its
// declared median and p95 and is right-skewed (mean above median), deterministically from seed.
func TestLognormalHitsMedianAndP95(t *testing.T) {
	var vm ValueModel
	dec := yaml.NewDecoder(bytes.NewReader([]byte("lognormal: {median: 120, p95: 3600}")))
	dec.KnownFields(true)
	if err := dec.Decode(&vm); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := checkValue(vm, posMetricValue, "test"); err != nil {
		t.Fatalf("lognormal rejected as a metric value: %v", err)
	}
	draw := func() []float64 {
		rng := rand.New(rand.NewPCG(1, 2))
		ctx := EvalCtx{Norm: rng.NormFloat64}
		out := make([]float64, 20000)
		for i := range out {
			out[i], _ = vm.Eval(ctx)
		}
		return out
	}
	vals := draw()
	again := draw()
	var sum float64
	for i, v := range vals {
		if v != again[i] {
			t.Fatalf("draw %d not deterministic: %v vs %v", i, v, again[i])
		}
		sum += v
	}
	sort.Float64s(vals)
	med, p95 := vals[len(vals)/2], vals[len(vals)*95/100]
	if med < 114 || med > 126 {
		t.Fatalf("median %v, want about 120", med)
	}
	if p95 < 3240 || p95 > 3960 {
		t.Fatalf("p95 %v, want about 3600", p95)
	}
	if mean := sum / float64(len(vals)); mean <= 2*med {
		t.Fatalf("mean %v not well above median %v: not right-skewed", mean, med)
	}
}

func TestLognormalValidation(t *testing.T) {
	for _, ln := range []Lognormal{{Median: 0, P95: 1}, {Median: 10, P95: 5}, {Median: -1, P95: 1}} {
		ln := ln
		if err := (ValueModel{Lognormal: &ln}).Validate(); err == nil {
			t.Fatalf("lognormal %+v accepted", ln)
		}
	}
	ln := Lognormal{Median: 5, P95: 5}
	if v, _ := (ValueModel{Lognormal: &ln}).Eval(EvalCtx{Norm: func() float64 { return 3 }}); v != 5 {
		t.Fatalf("degenerate lognormal = %v, want 5", v)
	}
	if err := checkValue(ValueModel{Lognormal: &ln}, posLabel, "label"); err == nil {
		t.Fatal("lognormal accepted as a label")
	}
}

// TestEnumScaleOnlyOnMetricLabels: scales are admitted on a metric label and rejected on a log
// stream label, log body and span attribute.
func TestEnumScaleOnlyOnMetricLabels(t *testing.T) {
	two := 2.0
	vm := ValueModel{Enum: []EnumEntry{{Value: "a", RateScale: &two}}}
	if err := checkValue(vm, posMetricLabel, "m"); err != nil {
		t.Fatalf("metric label rejected: %v", err)
	}
	for _, pos := range []position{posLabel, posBody, posAttr} {
		if err := checkValue(vm, pos, "x"); err == nil {
			t.Fatalf("position %d accepted a scaled enum", pos)
		}
	}
}
