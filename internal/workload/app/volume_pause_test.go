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
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// TestMinterVolumeZeroPausesRequests pins SKT-0147: a master volume of exactly 0 mints no
// requests (rps 0, not the off_peak_rps floor), while any non-zero volume keeps the unchanged
// off_peak -> peak interpolation by the shape factor.
func TestMinterVolumeZeroPausesRequests(t *testing.T) {
	m := &minter{traffic: Traffic{OffPeakRPS: 5, PeakRPS: 50}, weight: 1.0}
	now := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC) // weekday night: factor well below 1
	eng := shape.New("", nil)

	eng.SetVolumeMultiplier(0)
	if got := m.rpsAt(now, eng); got != 0 {
		t.Fatalf("rps at volume 0 = %v, want 0", got)
	}
	if got := m.expectedVolume(now, 30, eng); got != 0 {
		t.Fatalf("expected volume at volume 0 = %v, want 0", got)
	}
	for range 50 {
		if n := len(m.Mint(now, 30, eng)); n != 0 {
			t.Fatalf("minted %d requests at volume 0, want 0", n)
		}
	}

	for _, vol := range []float64{1, 2} {
		eng.SetVolumeMultiplier(vol)
		f := math.Min(math.Max(eng.Factor(now, m.weight, m.nonProd), 0), 1)
		want := 5 + (50-5)*f
		if got := m.rpsAt(now, eng); got != want {
			t.Fatalf("rps at volume %v = %v, want unchanged interpolation %v", vol, got, want)
		}
	}
}

// TestSpanMetricsVolumeZeroEmitsNoCalls: at master volume 0 the derived span metrics count no
// calls; a non-zero volume still counts them.
func TestSpanMetricsVolumeZeroEmitsNoCalls(t *testing.T) {
	sum := func(vol float64) float64 {
		w := buildApp(t, graphCfg())
		now := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		world.Shape.SetVolumeMultiplier(vol)
		if err := w.Tick(context.Background(), now, world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		total := 0.0
		for _, s := range mc.Find("traces_spanmetrics_calls_total") {
			total += s.Value
		}
		return total
	}
	if got := sum(0); got != 0 {
		t.Fatalf("calls_total at volume 0 = %v, want 0", got)
	}
	if got := sum(1); got <= 0 {
		t.Fatalf("calls_total at volume 1 = %v, want > 0", got)
	}
	if got := sum(2); got <= 0 {
		t.Fatalf("calls_total at volume 2 = %v, want > 0", got)
	}
}

func pausedSpecCfg(profs ...string) *Config {
	return &Config{
		Traffic: Traffic{OffPeakRPS: 20, PeakRPS: 50},
		Services: []ServiceNode{
			{Name: "svc", Type: "web", Runtime: "go", Entry: true, Profiles: profs},
		},
	}
}

func specSeries(t *testing.T, cfg *Config, vol float64, ticks int) map[string]float64 {
	t.Helper()
	w := buildApp(t, cfg)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.EmitSpanMetrics = false
	world.Shape.SetVolumeMultiplier(vol)
	base := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	for i := range ticks {
		if err := w.Tick(context.Background(), base.Add(time.Duration(i)*30*time.Second), world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	out := map[string]float64{}
	for _, s := range mc.All() {
		out[s.Name] += s.Value // sums across ticks and label sets; enough to detect any emission
	}
	return out
}

// TestSpecMetricsVolumeZeroEmitNoNominalLoad pins the review findings: at master volume 0 the
// shape evaluator's zero-means-1.0 fallback must not reach the spec metrics, and request-shaped
// spec histograms and counters (the RED duration histogram) must not observe anything.
func TestSpecMetricsVolumeZeroEmitNoNominalLoad(t *testing.T) {
	cfg := func() *Config {
		return pausedSpecCfg("scraped_http_server", "gateway_native_scrape", "runtime_go")
	}
	paused := specSeries(t, cfg(), 0, 1)
	for name, v := range paused {
		if v == 0 {
			continue
		}
		switch name {
		case "go_goroutines", "node_process_resident_memory_bytes", "node_eventloop_lag_seconds":
			// Process-describing gauges (normal / float_range values): documented to keep emitting.
		case "target_info", "traces_target_info":
			// Constant identity info series (value 1), not load.
		default:
			t.Errorf("series %s = %v while paused, want 0 or absent", name, v)
		}
	}
	for _, name := range []string{"http_server_active_requests", "llm_token_sum", "llm_cost_sum", "go_memstats_heap_inuse_bytes"} {
		if v := paused[name]; v != 0 {
			t.Errorf("shape gauge %s = %v while paused, want 0", name, v)
		}
	}
	for name := range paused {
		if strings.HasSuffix(name, "_count") || strings.HasSuffix(name, "_bucket") {
			if paused[name] != 0 {
				t.Errorf("histogram series %s = %v while paused, want 0", name, paused[name])
			}
		}
	}
	for _, vol := range []float64{1, 2} {
		got := specSeries(t, cfg(), vol, 1)
		for _, name := range []string{"http_server_active_requests", "request_count", "http_server_request_duration_seconds_count"} {
			if got[name] <= 0 {
				t.Errorf("volume %v: %s = %v, want > 0", vol, name, got[name])
			}
		}
	}
}

// TestSpecMetricsPauseFromRunningHoldsNoLoad: pausing a workload that already emitted keeps the
// counters and histograms flat and zeroes the shape gauges instead of freezing their last
// full-load value.
func TestSpecMetricsPauseFromRunningHoldsNoLoad(t *testing.T) {
	shapeVal := func(base float64) telemetryspec.ValueModel {
		return telemetryspec.ValueModel{Shape: &telemetryspec.ShapeModel{Base: base}}
	}
	w := buildApp(t, &Config{
		Traffic: Traffic{OffPeakRPS: 20, PeakRPS: 50},
		Services: []ServiceNode{{
			Name: "svc", Type: "web", Runtime: "go", Entry: true,
			Metrics: []telemetryspec.MetricSpec{
				{Name: "spec_active", Instrument: "gauge", Value: shapeVal(5)},
				{Name: "spec_requests_total", Instrument: "counter", Value: shapeVal(10)},
				{Name: "spec_duration_seconds", Instrument: "histogram", Buckets: []float64{0.1, 1},
					Value: telemetryspec.ValueModel{Normal: &telemetryspec.Normal{Mean: 0.2, Stddev: 0.01}}},
				{Name: "spec_process_gauge", Instrument: "gauge", Value: telemetryspec.ValueModel{Const: ptr(7.0)}},
			},
		}},
	})
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.EmitSpanMetrics = false
	base := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	tick := func(i int) {
		t.Helper()
		if err := w.Tick(context.Background(), base.Add(time.Duration(i)*30*time.Second), world); err != nil {
			t.Fatal(err)
		}
	}
	last := func(name string) float64 {
		all := mc.Find(name)
		if len(all) == 0 {
			return -1
		}
		return all[len(all)-1].Value
	}
	tick(0)
	tick(1)
	runningCount := last("spec_requests_total")
	runningHist := last("spec_duration_seconds_count")
	if runningCount <= 0 || last("spec_active") <= 0 || runningHist <= 0 {
		t.Fatalf("precondition: running series must be positive, got %v %v %v", runningCount, last("spec_active"), runningHist)
	}
	world.Shape.SetVolumeMultiplier(0)
	tick(2)
	tick(3)
	if got := last("spec_requests_total"); got != runningCount {
		t.Errorf("counter grew while paused: %v -> %v", runningCount, got)
	}
	if got := last("spec_duration_seconds_count"); got != runningHist {
		t.Errorf("histogram count grew while paused: %v -> %v", runningHist, got)
	}
	if got := last("spec_active"); got != 0 {
		t.Errorf("shape gauge = %v while paused, want 0", got)
	}
	if got := last("spec_process_gauge"); got != 7 {
		t.Errorf("const process gauge = %v while paused, want it kept at 7", got)
	}
}

// TestPyroscopeVolumeZeroEmitsNoProfiles: at master volume 0 the profile lane emits nothing; a
// non-zero volume still emits.
func TestPyroscopeVolumeZeroEmitsNoProfiles(t *testing.T) {
	count := func(vol float64) int {
		w, led := buildPyroApp(t)
		now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
		pc := &pyroCapture{}
		world := worldWithPyro(&coretest.MetricCapture{}, pc, led)
		world.Shape.SetVolumeMultiplier(vol)
		if err := w.Tick(context.Background(), now, world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		return len(pc.all())
	}
	if got := count(0); got != 0 {
		t.Fatalf("profile series at volume 0 = %d, want 0", got)
	}
	for _, vol := range []float64{1, 2} {
		if got := count(vol); got == 0 {
			t.Fatalf("profile series at volume %v = 0, want > 0", vol)
		}
	}
}
