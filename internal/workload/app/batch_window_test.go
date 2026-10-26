// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/shape"
)

// batchWindowDoc declares one incident-responsive shape counter. %BATCH% is empty for the smooth
// counter the batched release is compared with.
const batchWindowDoc = `
shape_profile: {timezone: UTC}
traffic: {off_peak_rps: 0, peak_rps: 0.01}
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - name: records_received_total
        source: test fixture
        instrument: counter
        value: {shape: {base: 100, mode: error_spike%BATCH%}}
`

// windowTick is the master state of one minute of a batch window test.
type windowTick struct {
	vol  float64 // master volume (0 pauses)
	live bool    // a live error_spike fault is on
}

// runWindow ticks doc once a minute from 12:00 UTC (a weekday plateau) for 31 ticks under the
// per-minute state and returns the counter's cumulative reading per minute, carried across ticks
// that report no series (a paused tick).
func runWindow(t *testing.T, batch string, state func(m int) windowTick) []float64 {
	t.Helper()
	w := buildApp(t, decodeStrict(t, strings.Replace(batchWindowDoc, "%BATCH%", batch, 1)))
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	var live bool
	world.Shape.Live = func(mode string) []shape.LiveFailure {
		if live && mode == "error_spike" {
			return []shape.LiveFailure{{Enabled: true, Intensity: 1}}
		}
		return nil
	}
	start := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	var cum []float64
	prev := 0.0
	for m := 0; m <= 30; m++ {
		st := state(m)
		world.Shape.SetVolumeMultiplier(st.vol)
		live = st.live
		mc.Batches = nil
		if err := w.Tick(context.Background(), start.Add(time.Duration(m)*time.Minute), world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if s := mc.Find("records_received_total"); len(s) == 1 {
			prev = s[0].Value
		}
		cum = append(cum, prev)
	}
	return cum
}

// release is the counter increase at the window boundary tick (minute 30).
func release(cum []float64) float64 { return cum[30] - cum[29] }

func steady(int) windowTick { return windowTick{vol: 1} }

// TestBatchReleaseIgnoresLiveFaultOnPastTicks: a live fault switched on late in a window speaks
// for the ticks it is on, not for the ticks already accumulated, so the release is the scheduled
// window's (docs/workloads.md: amplification "active at each accumulated tick").
func TestBatchReleaseIgnoresLiveFaultOnPastTicks(t *testing.T) {
	calm := release(runWindow(t, ", batch: {period: 30m}", steady))
	late := release(runWindow(t, ", batch: {period: 30m}", func(m int) windowTick {
		return windowTick{vol: 1, live: m >= 20}
	}))
	if calm <= 0 {
		t.Fatalf("calm release %v", calm)
	}
	if math.Abs(late-calm) > 1e-9*calm {
		t.Fatalf("a live fault from minute 20 changed the window's release: %v, want the scheduled %v", late, calm)
	}
}

// TestBatchReleaseFollowsPauseAndVolumePerTick: a paused tick contributes nothing to the window it
// falls in and a volume change scales only the ticks it ran under, so a batched release equals
// the smooth counter's increase over the same window (the batch conserves what the smooth curve
// delivers), however the master volume moved inside it.
func TestBatchReleaseFollowsPauseAndVolumePerTick(t *testing.T) {
	for name, state := range map[string]func(int) windowTick{
		"pause": func(m int) windowTick {
			if m >= 10 && m < 20 {
				return windowTick{vol: 0}
			}
			return windowTick{vol: 1}
		},
		"volume change": func(m int) windowTick {
			if m >= 20 && m < 25 {
				return windowTick{vol: 0.5}
			}
			return windowTick{vol: 1}
		},
		"paused from the start of the run": func(m int) windowTick {
			if m < 10 {
				return windowTick{vol: 0}
			}
			return windowTick{vol: 1}
		},
		"resume at reduced volume": func(m int) windowTick {
			switch {
			case m >= 5 && m < 15:
				return windowTick{vol: 0}
			case m >= 15:
				return windowTick{vol: 0.25}
			}
			return windowTick{vol: 1}
		},
	} {
		t.Run(name, func(t *testing.T) {
			smooth := runWindow(t, "", state)
			got := release(runWindow(t, ", batch: {period: 30m}", state))
			want := smooth[29] // ticks 0..29, the window that closes at minute 30
			if want <= 0 {
				t.Fatalf("smooth window total %v", want)
			}
			if math.Abs(got-want) > 1e-9*want {
				t.Fatalf("batched release %v, want the smooth window total %v", got, want)
			}
		})
	}
}

// TestBatchedCountOfRejectedAtLoad: a count_of histogram over a batched counter is bounded by the
// release (a whole window of arrivals in one tick), not by one tick's nominal increment.
func TestBatchedCountOfRejectedAtLoad(t *testing.T) {
	doc := func(base, period string) string {
		return `
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - name: items_total
        source: test fixture
        instrument: counter
        labels: {lane: {enum: [{value: standard}]}}
        value: {shape: {base: ` + base + `, batch: {period: ` + period + `}}}
      - name: item_duration_seconds
        source: test fixture
        instrument: histogram
        count_of: items_total
        buckets: [60, 3600]
        labels: {lane: {enum: [{value: standard}]}}
        value: {lognormal: {median: 300, p95: 900}}
`
	}
	// 10 per tick over a 24h window is 14,400 in one release: above the 10,000 clamp.
	_, err := buildGraph(decodeStrict(t, doc("10", "24h")).Services)
	if err == nil || !strings.Contains(err.Error(), "can increment 14400") {
		t.Fatalf("err %v, want the batched release rejected as 14400 per tick", err)
	}
	// 10 per tick over 15 minutes is 150, and 5 per tick over a day is 7,200: both fit.
	for _, ok := range [][2]string{{"10", "15m"}, {"5", "24h"}} {
		if _, err := buildGraph(decodeStrict(t, doc(ok[0], ok[1])).Services); err != nil {
			t.Fatalf("base %s period %s: %v", ok[0], ok[1], err)
		}
	}
}
