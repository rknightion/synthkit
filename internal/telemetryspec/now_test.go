// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"strings"
	"testing"
	"time"
)

func TestNowModelReadsTickTimeInUnixSeconds(t *testing.T) {
	v := ValueModel{Now: &NowModel{}}
	if err := v.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if v.Kind() != KindNow {
		t.Fatalf("Kind = %q, want %q", v.Kind(), KindNow)
	}
	at := time.Date(2026, 6, 10, 13, 0, 0, 500_000_000, time.UTC)
	got, s := v.Eval(EvalCtx{Now: at})
	if want := float64(at.Unix()) + 0.5; got != want || s != "" {
		t.Fatalf("Eval = (%v, %q), want (%v, \"\")", got, s, want)
	}
	if got, _ := v.Eval(EvalCtx{}); got != 0 {
		t.Fatalf("zero tick time must read 0, got %v", got)
	}
}

func TestNowModelIsExactlyOneOf(t *testing.T) {
	c := 1.0
	if err := (ValueModel{Now: &NowModel{}, Const: &c}).Validate(); err == nil {
		t.Fatal("now plus const must be rejected")
	}
}

// now is a metric value only: no label, log body or span attribute evaluator supplies a tick time.
func TestNowModelCapability(t *testing.T) {
	v := ValueModel{Now: &NowModel{}}
	if err := checkValue(v, posMetricValue, "w"); err != nil {
		t.Fatalf("metric value: %v", err)
	}
	for _, pos := range []position{posLabel, posMetricLabel, posBody, posAttr} {
		if err := checkValue(v, pos, "w"); err == nil {
			t.Fatalf("position %d must reject now", pos)
		}
	}
}

func TestNowModelGaugeOnly(t *testing.T) {
	for _, inst := range []string{InstrumentCounter, InstrumentHistogram} {
		m := MetricSpec{Name: "x", Source: "s", Instrument: inst, Buckets: []float64{1}, Value: ValueModel{Now: &NowModel{}}}
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "gauge") {
			t.Fatalf("%s: want gauge-only error, got %v", inst, err)
		}
	}
	m := MetricSpec{Name: "x", Source: "s", Instrument: InstrumentGauge, Value: ValueModel{Now: &NowModel{}}}
	if err := m.Validate(); err != nil {
		t.Fatalf("gauge: %v", err)
	}
}

// value_scale would multiply the timestamp, so it is rejected on a now gauge's labels.
func TestNowModelRejectsValueScale(t *testing.T) {
	two := 2.0
	m := MetricSpec{Name: "x", Source: "s", Instrument: InstrumentGauge, Value: ValueModel{Now: &NowModel{}},
		Labels: map[string]ValueModel{"k": {Enum: []EnumEntry{{Value: "a", Weight: 1, ValueScale: &two}}}}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "now gauge") {
		t.Fatalf("want now-gauge value_scale rejection, got %v", err)
	}
}

// stall_mode names an incident mode; the model carries it without evaluating it, so the reading
// is unchanged, and a blank (whitespace-only) name is a load error.
func TestNowModelStallMode(t *testing.T) {
	v := ValueModel{Now: &NowModel{StallMode: "error_spike"}}
	if err := v.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	at := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	if got, _ := v.Eval(EvalCtx{Now: at}); got != float64(at.Unix()) {
		t.Fatalf("Eval = %v, want the tick time %v", got, float64(at.Unix()))
	}
	for _, bad := range []string{"  ", " error_spike"} {
		if err := (ValueModel{Now: &NowModel{StallMode: bad}}).Validate(); err == nil {
			t.Fatalf("stall_mode %q must be rejected", bad)
		}
	}
}
