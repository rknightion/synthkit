// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"gopkg.in/yaml.v3"
)

// decodeStrict decodes an app config exactly as the blueprint loader does (unknown keys fail).
func decodeStrict(t *testing.T, doc string) *Config {
	t.Helper()
	cfg := NewConfig().(*Config)
	dec := yaml.NewDecoder(bytes.NewReader([]byte(doc)))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return cfg
}

func tickN(t *testing.T, w *Workload, start time.Time, n int, vol float64) *coretest.MetricCapture {
	t.Helper()
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Shape.SetVolumeMultiplier(vol)
	for i := range n {
		mc.Batches = nil // keep only the last tick's cumulative batch
		if err := w.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	return mc
}

func byLabel(t *testing.T, series []promrw.Series, key string) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, s := range series {
		out[s.Labels[key]] = s.Value
	}
	return out
}

const scaledDoc = `
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - name: decisions_total
        source: test fixture
        instrument: counter
        labels:
          outcome:
            enum:
              - {value: approved, rate_scale: 3}
              - {value: declined}
        value: {const: 2}
      - name: decision_duration_seconds
        source: test fixture
        instrument: histogram
        buckets: [1, 5, 50]
        labels:
          path:
            enum:
              - {value: fast}
              - {value: slow, rate_scale: 2, value_scale: 10}
        value: {const: 1}
      - name: pending_items
        source: test fixture
        instrument: gauge
        labels:
          tier:
            enum:
              - {value: small, value_scale: 0.5}
              - {value: large}
        value: {const: 40}
`

// TestEnumScalesDifferPerValue: per-value rate_scale/value_scale make enum series differ in rate
// (counter increments, histogram counts) and in value (histogram sum/buckets, gauge level).
func TestEnumScalesDifferPerValue(t *testing.T) {
	w := buildApp(t, decodeStrict(t, scaledDoc))
	mc := tickN(t, w, time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC), 1, 1)

	ctr := byLabel(t, mc.Find("decisions_total"), "outcome")
	if ctr["approved"] != 6 || ctr["declined"] != 2 {
		t.Fatalf("counter per value = %v, want approved 6 declined 2", ctr)
	}
	cnt := byLabel(t, mc.Find("decision_duration_seconds_count"), "path")
	sum := byLabel(t, mc.Find("decision_duration_seconds_sum"), "path")
	if cnt["fast"] != 1 || cnt["slow"] != 2 || sum["fast"] != 1 || sum["slow"] != 20 {
		t.Fatalf("histogram count %v sum %v, want fast 1/1 slow 2/20", cnt, sum)
	}
	g := byLabel(t, mc.Find("pending_items"), "tier")
	if g["small"] != 20 || g["large"] != 40 {
		t.Fatalf("gauge per value = %v, want small 20 large 40", g)
	}
}

// TestEnumScaleFractionalRateCarries: a fractional histogram rate observes a whole count per
// tick and carries the remainder, so the long-run count equals the rate exactly.
func TestEnumScaleFractionalRateCarries(t *testing.T) {
	doc := strings.Replace(scaledDoc, "rate_scale: 2, value_scale: 10", "rate_scale: 0.25", 1)
	w := buildApp(t, decodeStrict(t, doc))
	mc := tickN(t, w, time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC), 8, 1)
	cnt := byLabel(t, mc.Find("decision_duration_seconds_count"), "path")
	if cnt["slow"] != 2 || cnt["fast"] != 8 {
		t.Fatalf("after 8 ticks count = %v, want slow 2 (rate 0.25) fast 8", cnt)
	}
}

func TestEnumScaleRejectedWherePointless(t *testing.T) {
	for name, doc := range map[string]string{
		"rate_scale is not valid on a gauge":    strings.Replace(scaledDoc, "{value: small, value_scale: 0.5}", "{value: small, rate_scale: 0.5}", 1),
		"value_scale is not valid on a counter": strings.Replace(scaledDoc, "{value: approved, rate_scale: 3}", "{value: approved, value_scale: 3}", 1),
		"must be finite and >= 0":               strings.Replace(scaledDoc, "rate_scale: 3", "rate_scale: -1", 1),
	} {
		cfg := NewConfig().(*Config)
		dec := yaml.NewDecoder(bytes.NewReader([]byte(doc)))
		dec.KnownFields(true)
		err := dec.Decode(cfg)
		if err == nil {
			_, err = buildGraph(cfg.Services)
		}
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("err %v, want rejection containing %q", err, name)
		}
	}
	cfg := decodeStrict(t, `
services:
  - name: api
    type: web
    entry: true
    logs:
      - source: app
        body:
          kind: {enum: [{value: a, rate_scale: 2}]}
`)
	if _, err := buildGraph(cfg.Services); err == nil || !strings.Contains(err.Error(), "metric labels only") {
		t.Fatalf("scaled enum in a log body: err %v, want metric-labels-only rejection", err)
	}
}

const derivedDoc = `
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - name: items_in_progress
        source: test fixture
        instrument: gauge
        stock_of: {metric: items_started_total, dwell_seconds: 600}
      - name: items_started_total
        source: test fixture
        instrument: counter
        labels:
          lane:
            enum:
              - {value: express, rate_scale: 2}
              - {value: standard}
        value: {const: 60}
      - name: item_duration_seconds
        source: test fixture
        instrument: histogram
        count_of: items_started_total
        buckets: [60, 3600, 86400]
        labels:
          lane:
            enum:
              - {value: express}
              - {value: standard, value_scale: 4}
        value: {lognormal: {median: 300, p95: 900}}
`

// TestStockOfSettlesAtRateTimesDwell: the stock gauge is seeded at counter rate x dwell per
// inherited label combination, and drains with time constant dwell when inflow stops.
func TestStockOfSettlesAtRateTimesDwell(t *testing.T) {
	w := buildApp(t, decodeStrict(t, derivedDoc))
	start := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	for i := range 3 {
		mc.Batches = nil
		if err := w.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), world); err != nil {
			t.Fatal(err)
		}
	}
	// 60 per 60 s tick = 1/s; express x2. Stock = rate x 600 s.
	st := byLabel(t, mc.Find("items_in_progress"), "lane")
	if st["standard"] != 600 || st["express"] != 1200 {
		t.Fatalf("stock = %v, want standard 600 express 1200", st)
	}
	world.Shape.SetVolumeMultiplier(0)
	mc.Batches = nil
	if err := w.Tick(context.Background(), start.Add(3*time.Minute), world); err != nil {
		t.Fatal(err)
	}
	want := 600 * math.Exp(-60.0/600)
	st = byLabel(t, mc.Find("items_in_progress"), "lane")
	if math.Abs(st["standard"]-want) > 1e-9 {
		t.Fatalf("paused stock = %v, want drain to %v", st["standard"], want)
	}
}

// TestCountOfFollowsCounter: a count_of histogram's _count equals its counter per combination,
// while value_scale still shifts that combination's observed values.
func TestCountOfFollowsCounter(t *testing.T) {
	w := buildApp(t, decodeStrict(t, derivedDoc))
	mc := tickN(t, w, time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC), 2, 1)
	ctr := byLabel(t, mc.Find("items_started_total"), "lane")
	cnt := byLabel(t, mc.Find("item_duration_seconds_count"), "lane")
	if cnt["express"] != ctr["express"] || cnt["standard"] != ctr["standard"] || ctr["express"] != 240 {
		t.Fatalf("histogram counts %v != counter %v", cnt, ctr)
	}
	sum := byLabel(t, mc.Find("item_duration_seconds_sum"), "lane")
	if r := (sum["standard"] / cnt["standard"]) / (sum["express"] / cnt["express"]); r < 3.2 || r > 5 {
		t.Fatalf("mean ratio standard/express = %v, want about value_scale 4", r)
	}
}

func TestDerivedReferenceRejected(t *testing.T) {
	for name, doc := range map[string]string{
		"is not a counter":      strings.Replace(derivedDoc, "stock_of: {metric: items_started_total", "stock_of: {metric: nope_total", 1),
		"same combinations":     strings.Replace(derivedDoc, "              - {value: standard, value_scale: 4}", "              - {value: other}", 1),
		"omit value":            strings.Replace(derivedDoc, "dwell_seconds: 600}", "dwell_seconds: 600}\n        value: {const: 1}", 1),
		"dwell_seconds must be": strings.Replace(derivedDoc, "dwell_seconds: 600", "dwell_seconds: 0", 1),
	} {
		cfg := NewConfig().(*Config)
		dec := yaml.NewDecoder(bytes.NewReader([]byte(doc)))
		dec.KnownFields(true)
		err := dec.Decode(cfg)
		if err == nil {
			_, err = buildGraph(cfg.Services)
		}
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("err %v, want rejection containing %q", err, name)
		}
	}
}

const profileDoc = `
shape_profile:
  timezone: America/New_York
  weekend_factor: 0.5
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - name: active_sessions
        source: test fixture
        instrument: gauge
        value: {shape: {base: 100}}
`

// TestShapeProfileOwnCurve: a workload shape_profile reads its own timezone and weekend factor
// for metrics and request volume, independent of the blueprint curve (Europe/Zurich here).
func TestShapeProfileOwnCurve(t *testing.T) {
	w := buildApp(t, decodeStrict(t, profileDoc))
	read := func(at time.Time) float64 {
		mc := tickN(t, w, at, 1, 1)
		s := mc.Find("active_sessions")
		if len(s) != 1 {
			t.Fatalf("series %d, want 1", len(s))
		}
		return s[0].Value
	}
	// Wed 08:00 UTC: 04:00 New York (floor) but 10:00 Zurich (plateau).
	if got := read(time.Date(2026, 6, 10, 8, 0, 0, 0, time.UTC)); math.Abs(got-10) > 1e-9 {
		t.Fatalf("NY night = %v, want floor 10", got)
	}
	// Wed 17:00 UTC: 13:00 New York (plateau), 19:00 Zurich (ramp down).
	if got := read(time.Date(2026, 6, 10, 17, 0, 0, 0, time.UTC)); got != 100 {
		t.Fatalf("NY midday = %v, want 100", got)
	}
	// Sat 17:00 UTC: weekend factor 0.5.
	if got := read(time.Date(2026, 6, 13, 17, 0, 0, 0, time.UTC)); got != 50 {
		t.Fatalf("NY Saturday = %v, want 50", got)
	}
	eng := coretest.World(nil, nil, nil).Shape
	if got := w.m.rpsAt(time.Date(2026, 6, 10, 8, 0, 0, 0, time.UTC), eng); math.Abs(got-1) > 1e-9 {
		t.Fatalf("rps at NY night = %v, want floor 0.1 x peak 10 = 1", got)
	}
}

func TestShapeProfileRejectsBadInput(t *testing.T) {
	for _, bad := range []string{"timezone: Nowhere/City", "plateau_start_hour: 5", "floor: 2", "weekend_factor: -1"} {
		doc := strings.Replace(profileDoc, "  timezone: America/New_York\n  weekend_factor: 0.5", "  "+bad, 1)
		cfg := decodeStrict(t, doc)
		if _, err := resolveLoadProfile(cfg.ShapeProfile); err == nil {
			t.Fatalf("%q accepted, want rejection", bad)
		}
	}
}

// TestStockOfPerNode: two service nodes declaring the same stock gauge keep independent levels.
func TestStockOfPerNode(t *testing.T) {
	doc := `
services:
  - name: api
    type: web
    entry: true
    calls: [worker]
    custom_metrics:
      - {name: q_depth, source: f, instrument: gauge, stock_of: {metric: q_in_total, dwell_seconds: 60}}
      - {name: q_in_total, source: f, instrument: counter, value: {const: 60}}
  - name: worker
    type: web
    custom_metrics:
      - {name: q_depth, source: f, instrument: gauge, stock_of: {metric: q_in_total, dwell_seconds: 60}}
      - {name: q_in_total, source: f, instrument: counter, value: {const: 6}}
`
	w := buildApp(t, decodeStrict(t, doc))
	mc := tickN(t, w, time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC), 3, 1)
	got := byLabel(t, mc.Find("q_depth"), "service")
	if got["api"] != 60 || got["worker"] != 6 {
		t.Fatalf("per-node stock = %v, want api 60 worker 6", got)
	}
}

// TestShapeProfileZeroLoadReadsZero: a profile that reaches an exact zero load (floor 0 at night,
// weekend_factor 0 on a weekend) emits 0 for shape values, not the evaluator's 1.0 fallback.
func TestShapeProfileZeroLoadReadsZero(t *testing.T) {
	doc := strings.Replace(profileDoc, "  weekend_factor: 0.5", "  weekend_factor: 0\n  floor: 0", 1)
	w := buildApp(t, decodeStrict(t, doc))
	for name, at := range map[string]time.Time{
		"weekend": time.Date(2026, 6, 13, 17, 0, 0, 0, time.UTC), // Sat 13:00 New York
		"night":   time.Date(2026, 6, 10, 8, 0, 0, 0, time.UTC),  // Wed 04:00 New York
	} {
		mc := tickN(t, w, at, 1, 1)
		s := mc.Find("active_sessions")
		if len(s) != 1 || s[0].Value != 0 {
			t.Fatalf("%s: active_sessions = %v, want one series at 0", name, s)
		}
	}
	mc := tickN(t, w, time.Date(2026, 6, 10, 17, 0, 0, 0, time.UTC), 1, 1)
	if s := mc.Find("active_sessions"); len(s) != 1 || s[0].Value != 100 {
		t.Fatalf("weekday plateau = %v, want 100", s)
	}
}

// TestObservationCapRejected: per-tick histogram observation counts above the documented bound
// are rejected at build for rate_scale and for a statically bounded count_of counter.
func TestObservationCapRejected(t *testing.T) {
	for name, doc := range map[string]string{
		"rate_scale product":  strings.Replace(scaledDoc, "rate_scale: 2, value_scale: 10", "rate_scale: 20000", 1),
		"can increment 24000": strings.Replace(derivedDoc, "value: {const: 60}", "value: {const: 12000}", 1),
	} {
		cfg := NewConfig().(*Config)
		dec := yaml.NewDecoder(bytes.NewReader([]byte(doc)))
		dec.KnownFields(true)
		err := dec.Decode(cfg)
		if err == nil {
			_, err = buildGraph(cfg.Services)
		}
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("err %v, want rejection containing %q", err, name)
		}
	}
}

// TestCountOfClampedAtRuntime: an unbounded counter model cannot make one series observe more
// than MaxObservationsPerTick values in a tick.
func TestCountOfClampedAtRuntime(t *testing.T) {
	doc := strings.Replace(derivedDoc, "value: {const: 60}", "value: {normal: {mean: 50000, stddev: 1}}", 1)
	w := buildApp(t, decodeStrict(t, doc))
	mc := tickN(t, w, time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC), 1, 1)
	for _, s := range mc.Find("item_duration_seconds_count") {
		if s.Value != 10000 {
			t.Fatalf("count %v for %v, want clamp 10000", s.Value, s.Labels)
		}
	}
}

// TestStockOfVaryingRateIrregularTicks: with a varying counter increment and irregular tick
// spacing, the stock matches an independent integration of dS/dt = r - S/dwell per tick.
func TestStockOfVaryingRateIrregularTicks(t *testing.T) {
	doc := strings.Replace(derivedDoc, "value: {const: 60}", "value: {int_range: {min: 10, max: 200}}", 1)
	w := buildApp(t, decodeStrict(t, doc))
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	const dwell = 600.0
	var prevCum, model float64
	gaps := []int{60, 90, 30, 120, 45, 300, 60}
	incs := map[float64]bool{}
	for i, gap := range gaps {
		if i > 0 {
			now = now.Add(time.Duration(gap) * time.Second)
		}
		mc.Batches = nil
		if err := w.Tick(context.Background(), now, world); err != nil {
			t.Fatal(err)
		}
		cum := byLabel(t, mc.Find("items_started_total"), "lane")["standard"]
		inc := cum - prevCum
		prevCum = cum
		incs[inc] = true
		dt := 60.0
		if i > 0 {
			dt = float64(gap)
		}
		r := inc / dt
		if i == 0 {
			model = r * dwell
		} else {
			model = r*dwell + (model-r*dwell)*math.Exp(-dt/dwell)
		}
		got := byLabel(t, mc.Find("items_in_progress"), "lane")["standard"]
		if math.Abs(got-model) > 1e-6*math.Max(1, model) {
			t.Fatalf("tick %d (dt %v): stock %v, independent model %v", i, dt, got, model)
		}
	}
	if len(incs) < 3 {
		t.Fatalf("counter increments did not vary: %v", incs)
	}
}

const errDoc = `
traffic: {off_peak_rps: 100, peak_rps: 100}
services:
  - name: api
    type: web
    entry: true
    error_rate: 0.2
    calls: [store]
  - name: store
    type: db
`

// TestServiceErrorRate: the entry's error_rate replaces the default 0.01 baseline and minted
// outcomes follow it; a downstream service's error_rate is rejected at build.
func TestServiceErrorRate(t *testing.T) {
	w := buildApp(t, decodeStrict(t, errDoc))
	if got := w.m.baseErrorRate(); got != 0.2 {
		t.Fatalf("entry error_rate baseline = %v, want 0.2", got)
	}
	eng := coretest.World(nil, nil, nil).Shape
	now := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	var total, failed int
	for range 50 {
		for _, r := range w.m.Mint(now, 60, eng) {
			total++
			if r.StatusCode != 200 {
				failed++
			}
		}
	}
	if frac := float64(failed) / float64(total); frac < 0.18 || frac > 0.22 {
		t.Fatalf("non-success fraction %v over %d requests, want about 0.2", frac, total)
	}
	down := decodeStrict(t, strings.Replace(errDoc, "    type: db\n", "    type: db\n    error_rate: 0.5\n", 1))
	if _, err := build(down, coretestBinding()); err == nil || !strings.Contains(err.Error(), `service "store": error_rate is valid on the entry service only`) {
		t.Fatalf("downstream error_rate: err %v, want entry-only rejection naming the service", err)
	}
	w = buildApp(t, decodeStrict(t, strings.Replace(errDoc, "    error_rate: 0.2\n", "", 1)))
	if got := w.m.baseErrorRate(); got != 0.01 {
		t.Fatalf("default baseline = %v, want exactly 0.01", got)
	}
	cfg := decodeStrict(t, strings.Replace(errDoc, "error_rate: 0.2", "error_rate: 1.5", 1))
	if _, err := build(cfg, coretestBinding()); err == nil || !strings.Contains(err.Error(), "out of [0,1]") {
		t.Fatalf("error_rate 1.5: err %v, want rejection", err)
	}
}

const serviceProfileDoc = `
shape_profile:
  timezone: America/New_York
  weekend_factor: 0.5
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: api
    type: web
    entry: true
    calls: [worker]
    custom_metrics:
      - {name: active_sessions, source: f, instrument: gauge, value: {shape: {base: 100}}}
  - name: worker
    type: web
    shape_profile: {timezone: Asia/Tokyo}
    custom_metrics:
      - {name: active_sessions, source: f, instrument: gauge, value: {shape: {base: 100}}}
`

// TestServiceShapeProfileOverride: a service shape_profile overrides the workload's for that
// service only, inheriting the workload's unset fields (weekend_factor here).
func TestServiceShapeProfileOverride(t *testing.T) {
	w := buildApp(t, decodeStrict(t, serviceProfileDoc))
	read := func(at time.Time) map[string]float64 {
		return byLabel(t, tickN(t, w, at, 1, 1).Find("active_sessions"), "service")
	}
	// Wed 17:00 UTC: 13:00 New York (plateau), Thu 02:00 Tokyo (floor).
	if got := read(time.Date(2026, 6, 10, 17, 0, 0, 0, time.UTC)); got["api"] != 100 || math.Abs(got["worker"]-10) > 1e-9 {
		t.Fatalf("Wed 17:00 UTC = %v, want api 100 worker 10", got)
	}
	// Sat 03:00 UTC: 12:00 Saturday Tokyo, inherited weekend_factor 0.5; Fri 23:00 New York (floor).
	if got := read(time.Date(2026, 6, 13, 3, 0, 0, 0, time.UTC)); got["worker"] != 50 || math.Abs(got["api"]-10) > 1e-9 {
		t.Fatalf("Sat 03:00 UTC = %v, want worker 50 api 10", got)
	}
	// An entry override drives request volume: Tokyo floor at Wed 17:00 UTC -> 0.1 x 10.
	doc := strings.Replace(serviceProfileDoc, "    entry: true\n", "    entry: true\n    shape_profile: {timezone: Asia/Tokyo}\n", 1)
	w = buildApp(t, decodeStrict(t, doc))
	eng := coretest.World(nil, nil, nil).Shape
	if got := w.m.rpsAt(time.Date(2026, 6, 10, 17, 0, 0, 0, time.UTC), eng); math.Abs(got-1) > 1e-9 {
		t.Fatalf("entry-override rps = %v, want 1", got)
	}
	bad := strings.Replace(serviceProfileDoc, "{timezone: Asia/Tokyo}", "{plateau_start_hour: 3}", 1)
	if _, err := build(decodeStrict(t, bad), coretestBinding()); err == nil || !strings.Contains(err.Error(), `service "worker"`) {
		t.Fatalf("bad service profile: err %v, want rejection naming the service", err)
	}
}

func coretestBinding() core.Binding {
	return core.Binding{Name: "demo", Env: coretest.Env(), Cluster: coretest.Cluster()}
}
