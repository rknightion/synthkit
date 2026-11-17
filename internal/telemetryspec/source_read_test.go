// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"math"
	"strings"
	"testing"
	"time"
)

func readValue(measure string, perTick float64, stallMode string) ValueModel {
	return ValueModel{SourceRead: &SourceReadModel{Measure: measure, PerTick: perTick, StallMode: stallMode}}
}

func TestSourceReadEval(t *testing.T) {
	stalled := func(map[string]string) bool { return true }
	healthy := func(map[string]string) bool { return false }
	cases := []struct {
		name    string
		v       ValueModel
		ctx     EvalCtx
		want    float64
		wantNil bool
	}{
		{"inspected advances", readValue(ReadRecordsInspected, 40, "m"), EvalCtx{Stalled: healthy, ShapeVal: 0.5}, 20, false},
		{"inspected default load", readValue(ReadRecordsInspected, 40, "m"), EvalCtx{}, 40, false},
		{"inspected exact zero load", readValue(ReadRecordsInspected, 40, "m"), EvalCtx{ExactLoad: true}, 0, false},
		{"inspected frozen", readValue(ReadRecordsInspected, 40, "m"), EvalCtx{Stalled: stalled}, 0, false},
		{"checkpoint advances", readValue(ReadCheckpoint, 7, "m"), EvalCtx{Stalled: healthy}, 7, false},
		{"checkpoint frozen", readValue(ReadCheckpoint, 7, "m"), EvalCtx{Stalled: stalled}, 0, false},
		{"failures quiet", readValue(ReadFailures, 3, "m"), EvalCtx{Stalled: healthy, ShapeVal: 9}, 0, false},
		{"failures nil provider", readValue(ReadFailures, 3, "m"), EvalCtx{}, 0, false},
		{"failures while stalled ignore load", readValue(ReadFailures, 3, "m"), EvalCtx{Stalled: stalled, ShapeVal: 9}, 3, false},
	}
	for _, c := range cases {
		if got, s := c.v.Eval(c.ctx); got != c.want || s != "" {
			t.Fatalf("%s: Eval = (%v, %q), want %v", c.name, got, s, c.want)
		}
	}
	if k := readValue(ReadFailures, 1, "m").Kind(); k != KindSourceRead {
		t.Fatalf("Kind = %q, want %q", k, KindSourceRead)
	}
	// The model draws no randomness, so declaring one never shifts a neighbour's values.
	draws := 0
	rnd := func() float64 { draws++; return 0.5 }
	readValue(ReadRecordsInspected, 1, "m").Eval(EvalCtx{Rand: rnd, Norm: rnd})
	if draws != 0 {
		t.Fatalf("source_read consumed %d draws", draws)
	}
}

func TestSourceReadValidate(t *testing.T) {
	for name, v := range map[string]ValueModel{
		"unknown measure": readValue("rows", 1, ""),
		"empty measure":   readValue("", 1, ""),
		"zero per_tick":   readValue(ReadCheckpoint, 0, ""),
		"negative":        readValue(ReadCheckpoint, -1, ""),
		"nan":             readValue(ReadCheckpoint, math.NaN(), ""),
		"inf":             readValue(ReadCheckpoint, math.Inf(1), ""),
		"padded mode":     readValue(ReadCheckpoint, 1, " m"),
	} {
		if err := v.Validate(); err == nil {
			t.Fatalf("%s: want a validation error", name)
		}
	}
	c := 1.0
	both := readValue(ReadCheckpoint, 1, "")
	both.Const = &c
	if err := both.Validate(); err == nil {
		t.Fatal("source_read plus const must be rejected")
	}
	if err := readValue(ReadCheckpoint, 1, "").Validate(); err != nil {
		t.Fatalf("checkpoint without a stall mode: %v", err)
	}
}

func TestSourceReadCapability(t *testing.T) {
	v := readValue(ReadCheckpoint, 1, "m")
	if err := checkValue(v, posMetricValue, "w"); err != nil {
		t.Fatalf("metric value: %v", err)
	}
	for _, pos := range []position{posLabel, posMetricLabel, posBody, posAttr} {
		if err := checkValue(v, pos, "w"); err == nil {
			t.Fatalf("position %d must reject source_read", pos)
		}
	}
}

func stallEnum(label string, entries ...EnumEntry) map[string]ValueModel {
	return map[string]ValueModel{label: {Enum: entries}}
}

func TestSourceReadMetricRules(t *testing.T) {
	counter := func(v ValueModel, labels map[string]ValueModel) MetricSpec {
		return MetricSpec{Name: "x_total", Source: "s", Instrument: InstrumentCounter, Value: v, Labels: labels}
	}
	if err := counter(readValue(ReadFailures, 1, "m"), nil).Validate(); err != nil {
		t.Fatalf("failures counter: %v", err)
	}
	for _, inst := range []string{InstrumentGauge, InstrumentHistogram} {
		m := MetricSpec{Name: "x", Source: "s", Instrument: inst, Buckets: []float64{1}, Value: readValue(ReadCheckpoint, 1, "m")}
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "counter") {
			t.Fatalf("%s: want counter-only error, got %v", inst, err)
		}
	}
	if err := counter(readValue(ReadFailures, 1, ""), nil).Validate(); err == nil || !strings.Contains(err.Error(), "stall_mode") {
		t.Fatalf("read_failures with no stall mode anywhere: got %v", err)
	}
	// A stall mode on an enum value satisfies read_failures.
	withEnum := stallEnum("source", EnumEntry{Value: "a", StallMode: "m"}, EnumEntry{Value: "b"})
	if err := counter(readValue(ReadFailures, 1, ""), withEnum).Validate(); err != nil {
		t.Fatalf("enum stall mode: %v", err)
	}
}

func TestEnumStallModeRules(t *testing.T) {
	gauge := func(v ValueModel, labels map[string]ValueModel) MetricSpec {
		return MetricSpec{Name: "x", Source: "s", Instrument: InstrumentGauge, Value: v, Labels: labels}
	}
	now := ValueModel{Now: &NowModel{}}
	one := stallEnum("source", EnumEntry{Value: "a", StallMode: "m"}, EnumEntry{Value: "b"})
	if err := gauge(now, one).Validate(); err != nil {
		t.Fatalf("now with enum stall mode: %v", err)
	}
	k := 1.0
	if err := gauge(ValueModel{Const: &k}, one).Validate(); err == nil || !strings.Contains(err.Error(), "now or source_read") {
		t.Fatalf("stall_mode on a const gauge: got %v", err)
	}
	two := map[string]ValueModel{
		"a": {Enum: []EnumEntry{{Value: "x", StallMode: "m"}}},
		"b": {Enum: []EnumEntry{{Value: "y", StallMode: "m"}}},
	}
	if err := gauge(now, two).Validate(); err == nil || !strings.Contains(err.Error(), "one label only") {
		t.Fatalf("two stalling labels: got %v", err)
	}
	padded := stallEnum("source", EnumEntry{Value: "a", StallMode: " m"})
	if err := gauge(now, padded).Validate(); err == nil {
		t.Fatal("padded enum stall_mode must be rejected")
	}
	// Only a metric label may carry one.
	e := ValueModel{Enum: []EnumEntry{{Value: "a", StallMode: "m"}}}
	for _, pos := range []position{posLabel, posBody, posAttr} {
		if err := checkValue(e, pos, "w"); err == nil {
			t.Fatalf("position %d must reject enum stall_mode", pos)
		}
	}
	draw := ValueModel{RequestDraw: &RequestDraw{Key: "k", Enum: []EnumEntry{{Value: "a", StallMode: "m"}}}}
	if err := draw.Validate(); err == nil {
		t.Fatal("request_draw enum stall_mode must be rejected")
	}
}

func TestSeriesStallMode(t *testing.T) {
	m := MetricSpec{
		Name: "x", Instrument: InstrumentGauge,
		Value:  ValueModel{Now: &NowModel{StallMode: "base"}},
		Labels: stallEnum("source", EnumEntry{Value: "a", StallMode: "own"}, EnumEntry{Value: "b"}),
	}
	for labels, want := range map[string]string{"a": "own", "b": "base", "c": "base"} {
		if got := m.SeriesStallMode(map[string]string{"source": labels}); got != want {
			t.Fatalf("series %q mode = %q, want %q", labels, got, want)
		}
	}
	if got := m.SeriesStallMode(nil); got != "base" {
		t.Fatalf("nil labels mode = %q, want base", got)
	}
	if !m.HasSeriesStall() {
		t.Fatal("HasSeriesStall = false")
	}
}

func TestNowForReadsPerSeriesTime(t *testing.T) {
	at := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	held := at.Add(-time.Hour)
	v := ValueModel{Now: &NowModel{}}
	ctx := EvalCtx{Now: at, Labels: map[string]string{"s": "a"}, NowFor: func(l map[string]string) time.Time {
		if l["s"] == "a" {
			return held
		}
		return at
	}}
	if got, _ := v.Eval(ctx); got != float64(held.Unix()) {
		t.Fatalf("NowFor series = %v, want %v", got, held.Unix())
	}
	ctx.Labels = map[string]string{"s": "b"}
	if got, _ := v.Eval(ctx); got != float64(at.Unix()) {
		t.Fatalf("other series = %v, want %v", got, at.Unix())
	}
}

func TestNowArrivalsOnlyIsDeclarable(t *testing.T) {
	m := MetricSpec{Name: "x", Source: "s", Instrument: InstrumentGauge, Value: ValueModel{Now: &NowModel{ArrivalsOnly: true}}}
	if err := m.Validate(); err != nil {
		t.Fatalf("arrivals_only: %v", err)
	}
}
