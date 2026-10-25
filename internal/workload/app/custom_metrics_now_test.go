// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"gopkg.in/yaml.v3"
)

const nowGaugeDoc = `
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - name: collector_last_success_timestamp_seconds
        source: test fixture
        instrument: gauge
        value: {now: {}}
`

// nowGaugeAt ticks a fresh workload decoded from the blueprint YAML once at a fixed time and
// returns the single heartbeat series value.
func nowGaugeAt(t *testing.T, at time.Time, volume float64) float64 {
	t.Helper()
	w := buildApp(t, decodeStrict(t, nowGaugeDoc))
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	if volume >= 0 {
		world.Shape.SetVolumeMultiplier(volume)
	}
	if err := w.Tick(context.Background(), at, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	s := mc.Find("collector_last_success_timestamp_seconds")
	if len(s) != 1 {
		t.Fatalf("series %d, want 1", len(s))
	}
	return s[0].Value
}

// TestNowGaugeEqualsTickTimeInSeconds: a `now` gauge reads the tick timestamp in Unix seconds,
// loaded through the strict blueprint decoder, and two independent runs at the same fixed time
// agree (the -once -dump determinism contract).
func TestNowGaugeEqualsTickTimeInSeconds(t *testing.T) {
	at := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	want := float64(at.Unix())
	first := nowGaugeAt(t, at, -1)
	if first != want {
		t.Fatalf("now gauge = %v, want tick time %v", first, want)
	}
	if second := nowGaugeAt(t, at, -1); second != first {
		t.Fatalf("second run = %v, first = %v: not deterministic under a fixed clock", second, first)
	}
	// A later tick moves with the clock: the reading is the tick time, not a constant.
	later := at.Add(90 * time.Second)
	if got := nowGaugeAt(t, later, -1); got != want+90 {
		t.Fatalf("later tick = %v, want %v", got, want+90)
	}
}

// TestNowGaugeKeepsReadingWhenPaused: a heartbeat describes the process itself, so a paused
// workload (master volume 0) still reports the tick time like any non-shape gauge.
func TestNowGaugeKeepsReadingWhenPaused(t *testing.T) {
	at := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	if got := nowGaugeAt(t, at, 0); got != float64(at.Unix()) {
		t.Fatalf("paused now gauge = %v, want %v", got, float64(at.Unix()))
	}
}

// TestNowValueRejectedOffGauge: the model is valid on a gauge metric value only.
func TestNowValueRejectedOffGauge(t *testing.T) {
	for _, instrument := range []string{"counter", "histogram"} {
		var cfg Config
		err := yaml.Unmarshal([]byte(`services:
  - name: api
    type: web
    custom_metrics:
      - name: heartbeat
        source: test fixture
        instrument: `+instrument+`
        buckets: [1, 2]
        value: {now: {}}
`), &cfg)
		if err == nil {
			_, err = buildGraph(cfg.Services)
		}
		if err == nil || !strings.Contains(err.Error(), "gauge") {
			t.Fatalf("%s with now must be rejected naming gauge, got %v", instrument, err)
		}
	}
}
