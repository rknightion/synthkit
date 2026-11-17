// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/shape"
)

const nowStallDoc = `
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - name: collector_last_success_timestamp_seconds
        source: test fixture
        instrument: gauge
        value: {now: {%s}}
  - name: other
    type: web
    custom_metrics:
      - name: collector_last_success_timestamp_seconds
        source: test fixture
        instrument: gauge
        value: {now: {%s}}
`

// stallRun drives one workload decoded from the blueprint YAML across ticks and returns the
// api service's heartbeat reading per tick (and the other service's).
func stallRun(t *testing.T, apiNow, otherNow string, incident string, ticks []time.Time) (api, other []float64) {
	t.Helper()
	w := buildApp(t, decodeStrict(t, sprintfNow(apiNow, otherNow)))
	var incidents []string
	if incident != "" {
		incidents = []string{incident}
	}
	for _, at := range ticks {
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		world.Shape = shape.New("UTC", incidents)
		if err := w.Tick(context.Background(), at, world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		got := map[string]float64{}
		for _, s := range mc.Find("collector_last_success_timestamp_seconds") {
			got[s.Labels["service_name"]] = s.Value
		}
		if len(got) != 2 {
			t.Fatalf("series %v, want one per service", got)
		}
		api = append(api, got["api"])
		other = append(other, got["other"])
	}
	return api, other
}

func sprintfNow(apiNow, otherNow string) string {
	return fmt.Sprintf(nowStallDoc, apiNow, otherNow)
}

// TestNowGaugeStallModeFreezesAndResumes: a `now` gauge that declares stall_mode equals the tick
// time at baseline, holds its pre-incident reading while that mode is active on its service, and
// resumes at the tick time once the incident ends. A sibling service outside the incident scope,
// and a `now: {}` gauge in the same document, keep advancing throughout.
func TestNowGaugeStallModeFreezesAndResumes(t *testing.T) {
	t0 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	tick := func(i int) time.Time { return t0.Add(time.Duration(i) * 30 * time.Second) }
	ticks := []time.Time{tick(0), tick(1), tick(2), tick(3), tick(4), tick(5)}
	// error_spike on api for ticks 2 and 3 only: [tick(2), tick(4)).
	incident := "error_spike@" + tick(2).Format(time.RFC3339) + "/1m@api"

	api, other := stallRun(t, "stall_mode: error_spike", "stall_mode: error_spike", incident, ticks)
	want := []float64{
		float64(tick(0).Unix()), float64(tick(1).Unix()), // baseline: tick time
		float64(tick(1).Unix()), float64(tick(1).Unix()), // incident: held at the last pre-incident reading
		float64(tick(4).Unix()), float64(tick(5).Unix()), // resumed at tick time
	}
	for i := range want {
		if api[i] != want[i] {
			t.Fatalf("api tick %d = %v, want %v (all %v)", i, api[i], want[i], api)
		}
		if wantOther := float64(ticks[i].Unix()); other[i] != wantOther {
			t.Fatalf("other tick %d = %v, want %v: the incident is scoped to api", i, other[i], wantOther)
		}
	}

	// `now: {}` is unchanged by an active incident of any mode.
	plain, _ := stallRun(t, "", "", incident, ticks)
	for i, at := range ticks {
		if plain[i] != float64(at.Unix()) {
			t.Fatalf("plain now gauge tick %d = %v, want %v", i, plain[i], float64(at.Unix()))
		}
	}

	// A different incident mode does not stall it.
	otherMode := "latency_storm@" + tick(2).Format(time.RFC3339) + "/1m@api"
	unrelated, _ := stallRun(t, "stall_mode: error_spike", "", otherMode, ticks)
	for i, at := range ticks {
		if unrelated[i] != float64(at.Unix()) {
			t.Fatalf("unrelated mode stalled tick %d: %v, want %v", i, unrelated[i], float64(at.Unix()))
		}
	}
}

// TestNowGaugeStallDeterministic: two independent runs over the same ticks and incident agree
// (the held value is carried workload state, never the wall clock).
func TestNowGaugeStallDeterministic(t *testing.T) {
	t0 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	var ticks []time.Time
	for i := range 6 {
		ticks = append(ticks, t0.Add(time.Duration(i)*30*time.Second))
	}
	incident := "error_spike@" + ticks[2].Format(time.RFC3339) + "/1m@api"
	a, _ := stallRun(t, "stall_mode: error_spike", "", incident, ticks)
	b, _ := stallRun(t, "stall_mode: error_spike", "", incident, ticks)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("tick %d differs between runs: %v vs %v", i, a[i], b[i])
		}
	}
}

// TestNowGaugeStallFromFirstTickHoldsFirstReading: with no earlier reading to hold, the first
// tick reads the tick time and later stalled ticks hold that.
func TestNowGaugeStallFromFirstTickHoldsFirstReading(t *testing.T) {
	t0 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	ticks := []time.Time{t0, t0.Add(30 * time.Second), t0.Add(60 * time.Second)}
	incident := "error_spike@" + t0.Format(time.RFC3339) + "/1h@api"
	api, _ := stallRun(t, "stall_mode: error_spike", "", incident, ticks)
	for i, v := range api {
		if v != float64(t0.Unix()) {
			t.Fatalf("tick %d = %v, want held first reading %v", i, v, float64(t0.Unix()))
		}
	}
}

// TestNowGaugeStallIgnoresZeroIntensityIncident: an authored intensity-0 window is a no-op, as for
// every shape value, so it does not stall the heartbeat.
func TestNowGaugeStallIgnoresZeroIntensityIncident(t *testing.T) {
	t0 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	ticks := []time.Time{t0, t0.Add(30 * time.Second), t0.Add(60 * time.Second)}
	api, _ := stallRun(t, "stall_mode: error_spike", "", "error_spike@"+t0.Format(time.RFC3339)+"/1h#0@api", ticks)
	for i, at := range ticks {
		if api[i] != float64(at.Unix()) {
			t.Fatalf("tick %d = %v, want %v: a zero-intensity incident must not stall", i, api[i], float64(at.Unix()))
		}
	}
}
