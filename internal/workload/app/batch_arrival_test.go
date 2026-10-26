// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"math"
	"strings"
	"testing"
	"time"
)

// batchDoc declares one shape-driven counter. The workload clock is UTC and the shape base is
// 100, so one smooth tick adds 100 x the load percentage.
const batchDoc = `
shape_profile: {timezone: UTC}
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - name: records_received_total
        source: test fixture
        instrument: counter
        labels:
          site:
            enum:
              - {value: emea}
              - {value: apac, shape_profile: {timezone: Asia/Singapore}}
        value: {shape: {base: 100%BATCH%}}
`

func batchDocWith(batch string) string {
	return strings.Replace(batchDoc, "%BATCH%", batch, 1)
}

// counterDay ticks one minute at a time from 00:00 UTC Wed 2026-06-10 to 24:00 inclusive and
// returns each site's cumulative counter reading per tick (1441 readings).
func counterDay(t *testing.T, doc, metric string) map[string][]float64 {
	t.Helper()
	w := buildApp(t, decodeStrict(t, doc))
	out := map[string][]float64{}
	day := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	for m := 0; m <= 24*60; m++ {
		mc := tickN(t, w, day.Add(time.Duration(m)*time.Minute), 1, 1)
		for _, s := range mc.Find(metric) {
			out[s.Labels["site"]] = append(out[s.Labels["site"]], s.Value)
		}
	}
	return out
}

// increments is the per-tick counter delta (the first reading is its own delta).
func increments(cum []float64) []float64 {
	out := make([]float64, len(cum))
	prev := 0.0
	for i, v := range cum {
		out[i] = v - prev
		prev = v
	}
	return out
}

// TestBatchedCounterReleasesOnCadence: a counter with a 30 minute batch adds zero on every tick
// between releases, releases once per cadence on the wall-clock half hour, and over the day
// delivers the smooth counter's total: the tolerance is float rounding, 1e-9 relative, because
// the same per-tick terms are added in a different grouping. The first release (the first tick
// of a run) delivers the window before the run and is excluded from the comparison.
func TestBatchedCounterReleasesOnCadence(t *testing.T) {
	smooth := counterDay(t, batchDocWith(""), "records_received_total")
	batched := counterDay(t, batchDocWith(", batch: {period: 30m}"), "records_received_total")
	for _, site := range []string{"emea", "apac"} {
		inc := increments(batched[site])
		if len(inc) != 24*60+1 {
			t.Fatalf("%s: %d readings", site, len(inc))
		}
		releases := 0
		for m := 1; m < len(inc); m++ {
			onBoundary := m%30 == 0
			if !onBoundary && inc[m] != 0 {
				t.Fatalf("%s: tick %02d:%02d added %v between releases", site, m/60, m%60, inc[m])
			}
			if onBoundary {
				if inc[m] <= 0 {
					t.Fatalf("%s: no release at %02d:%02d", site, m/60, m%60)
				}
				releases++
			}
		}
		if releases != 48 {
			t.Fatalf("%s: %d releases in the day, want 48", site, releases)
		}
		gotTotal := batched[site][len(inc)-1] - inc[0]
		wantTotal := smooth[site][len(inc)-2] // ticks 00:00..23:59, the smooth day
		if math.Abs(gotTotal-wantTotal) > 1e-9*wantTotal {
			t.Fatalf("%s: batched day total %v, smooth day total %v", site, gotTotal, wantTotal)
		}
	}
	// Default output is untouched: the smooth counter adds every tick.
	for m, v := range increments(smooth["emea"]) {
		if v <= 0 {
			t.Fatalf("smooth counter added %v at tick %d", v, m)
		}
	}
}

// TestBatchedCounterPhase: a phase moves the release off the half hour.
func TestBatchedCounterPhase(t *testing.T) {
	got := counterDay(t, batchDocWith(", batch: {period: 15m, phase: 4m}"), "records_received_total")
	inc := increments(got["emea"])
	for m := 1; m < len(inc); m++ {
		if (m%15 == 4) != (inc[m] > 0) {
			t.Fatalf("tick %02d:%02d added %v, want a release exactly at minute 4 of each quarter", m/60, m%60, inc[m])
		}
	}
}

// TestSeriesClockCounterPeaksInOwnLocalDay: a counter series on its own zone reaches full
// arrivals on its local plateau (10:00 Singapore = 02:00 UTC), its sibling on the workload clock
// at 10:00 UTC; a batched counter's mass follows the same local day.
func TestSeriesClockCounterPeaksInOwnLocalDay(t *testing.T) {
	smooth := counterDay(t, batchDocWith(""), "records_received_total")
	firstFull := func(cum []float64) int {
		for h := 1; h < 24; h++ {
			if math.Abs(cum[h*60]-cum[h*60-1]-100) < 1e-9 {
				return h
			}
		}
		return -1
	}
	if h := firstFull(smooth["emea"]); h != 10 {
		t.Fatalf("workload-clock series reaches full arrivals at %d UTC, want 10", h)
	}
	if h := firstFull(smooth["apac"]); h != 2 {
		t.Fatalf("own-zone series reaches full arrivals at %d UTC, want 2 (10:00 Singapore)", h)
	}

	// Batched: each series delivers by its own local day. Delivery in the UTC hours that are
	// local midday for apac (02..08 UTC) must exceed emea's there, and the reverse in the evening.
	batched := counterDay(t, batchDocWith(", batch: {period: 30m}"), "records_received_total")
	window := func(cum []float64, from, to int) float64 { return cum[to*60] - cum[from*60] }
	if a, e := window(batched["apac"], 3, 7), window(batched["emea"], 3, 7); a <= 2*e {
		t.Fatalf("03-07 UTC: apac delivered %v, emea %v; the apac series should be at its local plateau", a, e)
	}
	if a, e := window(batched["apac"], 12, 16), window(batched["emea"], 12, 16); e <= 2*a {
		t.Fatalf("12-16 UTC: emea delivered %v, apac %v; the emea series should be at its plateau", e, a)
	}
}

// TestBatchedCounterFollowsTickSpacing: a blueprint that ticks the metric lane faster than the
// default minute (high_dpm) still releases once per cadence, with the whole window's mass: ten
// second ticks against a 30 minute batch read zero on all but the boundary tick, and the day's
// delivery equals the smooth counter's at the same spacing (1e-9 relative, float regrouping).
func TestBatchedCounterFollowsTickSpacing(t *testing.T) {
	const step = 10 * time.Second
	day := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	run := func(doc string) []float64 {
		w := buildApp(t, decodeStrict(t, doc))
		var cum []float64
		for k := 0; k <= int(24*time.Hour/step); k++ {
			mc := tickN(t, w, day.Add(time.Duration(k)*step), 1, 1)
			for _, s := range mc.Find("records_received_total") {
				if s.Labels["site"] == "emea" {
					cum = append(cum, s.Value)
				}
			}
		}
		return cum
	}
	smooth := run(batchDocWith(""))
	batched := run(batchDocWith(", batch: {period: 30m}"))
	inc := increments(batched)
	perWindow := int(30 * time.Minute / step)
	releases := 0
	for k := 1; k < len(inc); k++ {
		if k%perWindow != 0 {
			if inc[k] != 0 {
				t.Fatalf("tick %v added %v between releases", time.Duration(k)*step, inc[k])
			}
			continue
		}
		if inc[k] <= 0 {
			t.Fatalf("no release at %v", time.Duration(k)*step)
		}
		releases++
	}
	if releases != 48 {
		t.Fatalf("%d releases, want 48", releases)
	}
	got := batched[len(inc)-1] - inc[0]
	want := smooth[len(inc)-2]
	if math.Abs(got-want) > 1e-9*want {
		t.Fatalf("batched day total %v, smooth %v", got, want)
	}
}
