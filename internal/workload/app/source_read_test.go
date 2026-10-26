// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"strings"
	"testing"
	"time"
)

const sourceReadDoc = `
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: collector
    type: web
    entry: true
    custom_metrics:
      - name: collector_records_inspected_total
        source: test fixture
        instrument: counter
        value: {source_read: {measure: records_inspected, per_tick: 40, stall_mode: read_stall}}
      - name: collector_checkpoint_total
        source: test fixture
        instrument: counter
        value: {source_read: {measure: checkpoint, per_tick: 40, stall_mode: read_stall}}
      - name: collector_read_failures_total
        source: test fixture
        instrument: counter
        value: {source_read: {measure: read_failures, per_tick: 3, stall_mode: read_stall}}
`

// totals returns the cumulative value of a single-series metric at each tick.
func totals(t *testing.T, doc, metric string, incidents []string, ticks []time.Time) []float64 {
	t.Helper()
	var out []float64
	for _, m := range runSeries(t, doc, metric, "service_name", incidents, ticks) {
		if len(m) != 1 {
			t.Fatalf("%s: series %v, want one", metric, m)
		}
		for _, v := range m {
			out = append(out, v)
		}
	}
	return out
}

// TestSourceReadAdvancesFreezesAndResumes: records inspected and checkpoint position advance every
// tick, stop while the stall mode is active for the collector and resume afterwards; read failures
// grow only during the stall. All three are cumulative, and inspected and checkpoint move together.
func TestSourceReadAdvancesFreezesAndResumes(t *testing.T) {
	t0 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	tick := func(i int) time.Time { return t0.Add(time.Duration(i) * 30 * time.Second) }
	var ticks []time.Time
	for i := range 8 {
		ticks = append(ticks, tick(i))
	}
	incidents := []string{"read_stall@" + tick(3).Format(time.RFC3339) + "/1m30s@collector"} // ticks 3, 4, 5
	stalled := func(i int) bool { return i >= 3 && i <= 5 }

	inspected := totals(t, sourceReadDoc, "collector_records_inspected_total", incidents, ticks)
	checkpoint := totals(t, sourceReadDoc, "collector_checkpoint_total", incidents, ticks)
	failures := totals(t, sourceReadDoc, "collector_read_failures_total", incidents, ticks)
	prev := [3]float64{}
	for i := range ticks {
		di, dc, df := inspected[i]-prev[0], checkpoint[i]-prev[1], failures[i]-prev[2]
		prev = [3]float64{inspected[i], checkpoint[i], failures[i]}
		if stalled(i) {
			if di != 0 || dc != 0 || df != 3 {
				t.Fatalf("tick %d stalled: inspected +%v checkpoint +%v failures +%v, want 0, 0, 3", i, di, dc, df)
			}
			continue
		}
		if di <= 0 || dc <= 0 || df != 0 {
			t.Fatalf("tick %d healthy: inspected +%v checkpoint +%v failures +%v, want >0, >0, 0", i, di, dc, df)
		}
		if di != dc {
			t.Fatalf("tick %d: inspected +%v and checkpoint +%v must advance together", i, di, dc)
		}
	}
	// Deterministic: a second run agrees.
	if again := totals(t, sourceReadDoc, "collector_records_inspected_total", incidents, ticks); again[7] != inspected[7] {
		t.Fatalf("second run %v != first %v", again[7], inspected[7])
	}
}

// TestSourceReadNoIncidentNeverFails: with no stall, failures stay at zero and the others advance.
func TestSourceReadNoIncidentNeverFails(t *testing.T) {
	t0 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	ticks := []time.Time{t0, t0.Add(30 * time.Second), t0.Add(60 * time.Second)}
	if f := totals(t, sourceReadDoc, "collector_read_failures_total", nil, ticks); f[2] != 0 {
		t.Fatalf("failures %v, want 0 with no incident", f)
	}
	if in := totals(t, sourceReadDoc, "collector_records_inspected_total", nil, ticks); !(in[0] < in[1] && in[1] < in[2]) {
		t.Fatalf("inspected %v, want strictly rising", in)
	}
}

// TestSourceReadSeriesStallPerEnumValue: an enum value's own stall mode stalls only its series.
func TestSourceReadSeriesStallPerEnumValue(t *testing.T) {
	doc := strings.Replace(sourceReadDoc, `      - name: collector_records_inspected_total
        source: test fixture
        instrument: counter
        value: {source_read: {measure: records_inspected, per_tick: 40, stall_mode: read_stall}}
`, `      - name: collector_records_inspected_total
        source: test fixture
        instrument: counter
        labels:
          source:
            enum:
              - {value: alpha, stall_mode: stall_alpha}
              - {value: beta}
        value: {source_read: {measure: records_inspected, per_tick: 40}}
`, 1)
	t0 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	tick := func(i int) time.Time { return t0.Add(time.Duration(i) * 30 * time.Second) }
	ticks := []time.Time{tick(0), tick(1), tick(2), tick(3)}
	got := runSeries(t, doc, "collector_records_inspected_total", "source", []string{"stall_alpha@" + tick(2).Format(time.RFC3339) + "/30s@collector"}, ticks)
	if got[2]["alpha"] != got[1]["alpha"] || got[3]["alpha"] <= got[2]["alpha"] {
		t.Fatalf("alpha: %v %v %v, want frozen at tick 2 then resumed", got[1]["alpha"], got[2]["alpha"], got[3]["alpha"])
	}
	if got[2]["beta"] <= got[1]["beta"] {
		t.Fatalf("beta %v -> %v: must keep advancing", got[1]["beta"], got[2]["beta"])
	}
}

const arrivalsDoc = `
shape_profile:
  timezone: America/New_York
  weekend_factor: 0
  floor: 0
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: collector
    type: web
    entry: true
    custom_metrics:
      - name: source_newest_event_timestamp_seconds
        source: test fixture
        instrument: gauge
        value: {now: {arrivals_only: true, stall_mode: read_stall}}
`

// TestNowArrivalsOnlyHoldsWhenSourceIsQuiet: a source-event-time gauge advances while the source
// has arrivals, holds through a quiet weekend (the collector is healthy but nothing arrived), and
// advances again on the next weekday; a stall holds it too.
func TestNowArrivalsOnlyHoldsWhenSourceIsQuiet(t *testing.T) {
	fri := time.Date(2026, 6, 12, 17, 0, 0, 0, time.UTC) // Fri 13:00 New York
	sat := time.Date(2026, 6, 13, 17, 0, 0, 0, time.UTC) // Sat 13:00 New York: zero load
	mon := time.Date(2026, 6, 15, 17, 0, 0, 0, time.UTC) // Mon 13:00 New York
	ticks := []time.Time{fri, sat, sat.Add(time.Minute), mon}
	got := totals(t, arrivalsDoc, "source_newest_event_timestamp_seconds", nil, ticks)
	want := []float64{float64(fri.Unix()), float64(fri.Unix()), float64(fri.Unix()), float64(mon.Unix())}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tick %d = %v, want %v (all %v)", i, got[i], want[i], got)
		}
	}
	// Stalled on a weekday: held at the previous reading even though arrivals exist.
	wed := time.Date(2026, 6, 10, 17, 0, 0, 0, time.UTC)
	wed2 := wed.Add(time.Minute)
	got = totals(t, arrivalsDoc, "source_newest_event_timestamp_seconds", []string{"read_stall@" + wed2.Format(time.RFC3339) + "/1m@collector"}, []time.Time{wed, wed2})
	if got[0] != float64(wed.Unix()) || got[1] != float64(wed.Unix()) {
		t.Fatalf("stalled = %v, want held at %v", got, wed.Unix())
	}
}
