// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// TestHeartbeatHoldIsPerSeries: two now gauges that share a service and a metric name but differ
// in stall mode (the shape a per-series stall takes inside one spec) must each hold their own last
// advancing reading. A hold keyed on service and name alone lets the second overwrite the first.
func TestHeartbeatHoldIsPerSeries(t *testing.T) {
	w := buildApp(t, decodeStrict(t, nowStallDoc2))
	id := nodeIdentity{service: "collector"}
	mk := func(src, mode string) telemetryspec.MetricSpec {
		return telemetryspec.MetricSpec{
			Name: "last_success", Source: "t", Instrument: telemetryspec.InstrumentGauge,
			Labels: map[string]telemetryspec.ValueModel{"source": {ConstStr: &src}},
			Value:  telemetryspec.ValueModel{Now: &telemetryspec.NowModel{StallMode: mode}},
		}
	}
	alpha, beta := mk("alpha", "mode_alpha"), mk("beta", "mode_beta")
	t0 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	tick := func(i int) time.Time { return t0.Add(time.Duration(i) * 30 * time.Second) }
	incidents := []string{
		"mode_alpha@" + tick(2).Format(time.RFC3339) + "/1m@collector",
		"mode_beta@" + tick(3).Format(time.RFC3339) + "/1m@collector",
	}
	world := coretest.World(&coretest.MetricCapture{}, nil, nil)
	world.Shape = shape.New("UTC", incidents)
	var gotA, gotB []int64
	for i := range 6 {
		gotA = append(gotA, w.heartbeatNow(tick(i), world, id, nil, alpha, nil).Unix())
		gotB = append(gotB, w.heartbeatNow(tick(i), world, id, nil, beta, nil).Unix())
	}
	u := func(i int) int64 { return tick(i).Unix() }
	wantA := []int64{u(0), u(1), u(1), u(1), u(4), u(5)}
	wantB := []int64{u(0), u(1), u(2), u(2), u(2), u(5)}
	for i := range wantA {
		if gotA[i] != wantA[i] || gotB[i] != wantB[i] {
			t.Fatalf("tick %d: alpha %d beta %d, want alpha %d beta %d", i, gotA[i], gotB[i], wantA[i], wantB[i])
		}
	}
}

const nowStallDoc2 = `
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: collector
    type: web
    entry: true
`

const nowSeriesDoc = `
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: collector
    type: web
    entry: true
    custom_metrics:
      - name: collector_last_success_timestamp_seconds
        source: test fixture
        instrument: gauge
        labels:
          source:
            enum:
              - {value: alpha, stall_mode: mode_alpha}
              - {value: beta, stall_mode: mode_beta}
              - {value: gamma}
        value: {now: {stall_mode: mode_gamma}}
`

// runSeries ticks a fresh workload decoded from doc at each time and returns, per tick, the named
// metric's value by the given label.
func runSeries(t *testing.T, doc, metric, label string, incidents []string, ticks []time.Time) []map[string]float64 {
	t.Helper()
	w := buildApp(t, decodeStrict(t, doc))
	var out []map[string]float64
	for _, at := range ticks {
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		world.Shape = shape.New("UTC", incidents)
		if err := w.Tick(context.Background(), at, world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		got := map[string]float64{}
		for _, s := range mc.Find(metric) {
			got[s.Labels[label]] = s.Value
		}
		out = append(out, got)
	}
	return out
}

// TestNowGaugeEnumSeriesStallIndependently: through the strict decoder, two series of one gauge
// carry different stall modes and hold independently; a third follows the metric's own mode, which
// no incident touches, and keeps advancing.
func TestNowGaugeEnumSeriesStallIndependently(t *testing.T) {
	t0 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	tick := func(i int) time.Time { return t0.Add(time.Duration(i) * 30 * time.Second) }
	var ticks []time.Time
	for i := range 7 {
		ticks = append(ticks, tick(i))
	}
	incidents := []string{
		"mode_alpha@" + tick(2).Format(time.RFC3339) + "/1m@collector", // ticks 2, 3
		"mode_beta@" + tick(3).Format(time.RFC3339) + "/1m@collector",  // ticks 3, 4
	}
	got := runSeries(t, nowSeriesDoc, "collector_last_success_timestamp_seconds", "source", incidents, ticks)
	u := func(i int) float64 { return float64(tick(i).Unix()) }
	wantAlpha := []float64{u(0), u(1), u(1), u(1), u(4), u(5), u(6)}
	wantBeta := []float64{u(0), u(1), u(2), u(2), u(2), u(5), u(6)}
	for i := range ticks {
		if len(got[i]) != 3 {
			t.Fatalf("tick %d series %v, want alpha, beta and gamma", i, got[i])
		}
		if got[i]["alpha"] != wantAlpha[i] || got[i]["beta"] != wantBeta[i] || got[i]["gamma"] != u(i) {
			t.Fatalf("tick %d = %v, want alpha %v beta %v gamma %v", i, got[i], wantAlpha[i], wantBeta[i], u(i))
		}
	}
}
