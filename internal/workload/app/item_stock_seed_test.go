// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/shape"
)

// seedDoc declares a business-curve counter (0.6 arrivals per 60 s tick at full load, so 0.01/s
// at the plateau) feeding an item stock with a day-scale residence. %s is the node's extra
// declarations (a shape_profile). The real-world basis is only a fixture: a slow review queue
// whose items stay about a day.
const seedDoc = `
traffic: {off_peak_rps: 0, peak_rps: 10}
%s
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - {name: reviews_started_total, source: test fixture, instrument: counter, value: {shape: {base: 0.6}}}
      - name: reviews_open
        source: test fixture
        instrument: gauge
        stock_of: {metric: reviews_started_total, residence: {median: 85000, p95: 150000}}
`

const (
	seedMedian = 85000.0
	seedP95    = 150000.0
	seedBase   = 0.6 // arrivals per 60 s tick at load 1
)

// survival is P(residence > x) for the lognormal residence (median, p95).
func survival(x, median, p95 float64) float64 {
	if x <= 0 {
		return 1
	}
	sigma := math.Log(p95/median) / 1.6448536269514722
	return 0.5 * math.Erfc(math.Log(x/median)/sigma/math.Sqrt2)
}

// longRunMean is the expected population of an infinite-server delay at t0 under the arrival
// rate (per second): the integral over past arrivals of rate(a) x P(still resident at t0). It is
// an independent oracle: a one-minute midpoint sum over 20 days, far past the residence tail.
func longRunMean(rate func(time.Time) float64, t0 time.Time) float64 {
	var sum float64
	for age := 30.0; age < 20*86400; age += 60 {
		sum += rate(t0.Add(-time.Duration(age*1e9))) * survival(age, seedMedian, seedP95) * 60
	}
	return sum
}

// engineRate is the arrival rate of seedBase x the blueprint engine's business load (UTC).
func engineRate(eng *shape.Engine) func(time.Time) float64 {
	return func(a time.Time) float64 { return seedBase / 60 * eng.BusinessFactor(a) }
}

func seedRun(t *testing.T, doc string, start time.Time, n int, step time.Duration) []float64 {
	t.Helper()
	w := buildApp(t, decodeStrict(t, doc))
	out := make([]float64, 0, n)
	for i := range n {
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		world.Shape = shape.New("UTC", nil)
		if err := w.Tick(context.Background(), start.Add(time.Duration(i)*step), world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		s := mc.Find("reviews_open")
		if len(s) != 1 {
			t.Fatalf("tick %d: %d reviews_open series", i, len(s))
		}
		out = append(out, s[0].Value)
	}
	return out
}

// TestItemStockSeedFollowsArrivalCurve: a stock fed by the business curve, with a residence of
// about a day, seeds the population its own past arrivals would have left: within 10% of the
// long-run mean (the integral of rate x survival, computed above) whether the first tick is at the
// plateau or at night, on the blueprint curve and on a floor-0 shape_profile. The old seed took
// the first tick's rate x mean residence: about 2x the mean at the plateau and about 0.2x at night.
func TestItemStockSeedFollowsArrivalCurve(t *testing.T) {
	eng := shape.New("UTC", nil)
	floorZero := shape.DefaultCurve()
	zero := 0.0
	floorZero.Floor = zero
	profile := fmt.Sprintf("shape_profile: {timezone: UTC, floor: %v}", zero)
	profileRate := func(a time.Time) float64 {
		return seedBase / 60 * floorZero.Diurnal(a.UTC()) * floorZero.BusinessWeekly(a.UTC())
	}
	for name, tc := range map[string]struct {
		doc  string
		rate func(time.Time) float64
	}{
		"blueprint curve": {fmt.Sprintf(seedDoc, ""), engineRate(eng)},
		"floor-0 profile": {fmt.Sprintf(seedDoc, profile), profileRate},
	} {
		for when, at := range map[string]time.Time{
			"plateau": time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC),
			"night":   time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC),
			"monday":  time.Date(2026, 6, 8, 2, 0, 0, 0, time.UTC), // history spans the weekend
		} {
			mean := longRunMean(tc.rate, at)
			got := seedRun(t, tc.doc, at, 1, time.Minute)[0]
			t.Logf("%s %s: seeded %v, long-run mean %.1f", name, when, got, mean)
			if math.Abs(got-mean) > 0.1*mean {
				t.Errorf("%s %s: seeded %v, want within 10%% of the long-run mean %.1f", name, when, got, mean)
			}
		}
	}
}

// TestItemStockSeedHoldsAfterRestart: after a seed at night the stock keeps tracking the
// long-run mean as the day turns (each sampled hour within 10%), so there is no slow transient.
func TestItemStockSeedHoldsAfterRestart(t *testing.T) {
	eng := shape.New("UTC", nil)
	rate := engineRate(eng)
	start := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	got := seedRun(t, fmt.Sprintf(seedDoc, ""), start, 12*60, time.Minute)
	for h := 0; h < 12; h++ {
		at := start.Add(time.Duration(h) * time.Hour)
		mean := longRunMean(rate, at)
		if g := got[h*60]; math.Abs(g-mean) > 0.1*mean {
			t.Errorf("hour %d: stock %v, want within 10%% of the long-run mean %.1f", h, g, mean)
		}
	}
}

// TestItemStockSeedConstantRateUnchanged: a constant arrival rate seeds rate x residence mean as
// before (here 0.01/s x median 85,000 x exp(sigma^2/2)), at the plateau and at night alike.
func TestItemStockSeedConstantRateUnchanged(t *testing.T) {
	doc := strings.Replace(fmt.Sprintf(seedDoc, ""), "value: {shape: {base: 0.6}}", "value: {const: 0.6}", 1)
	sigma := math.Log(seedP95/seedMedian) / 1.6448536269514722
	want := 0.01 * seedMedian * math.Exp(sigma*sigma/2)
	for _, at := range []time.Time{
		time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC),
	} {
		got := seedRun(t, doc, at, 1, time.Minute)[0]
		if math.Abs(got-want) > 0.01*want {
			t.Errorf("constant rate seeded %v at %v, want %v within 1%%", got, at, want)
		}
	}
}

// TestItemStockSeedDeterministic: two runs over the same ticks agree exactly.
func TestItemStockSeedDeterministic(t *testing.T) {
	doc := fmt.Sprintf(seedDoc, "")
	start := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	a := seedRun(t, doc, start, 90, time.Minute)
	b := seedRun(t, doc, start, 90, time.Minute)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("tick %d differs between runs: %v vs %v", i, a[i], b[i])
		}
	}
}

// TestItemStockSeedCostBounded: the seed integrates the arrival curve over a bounded grid, so
// a very long residence (median 30 days) still seeds, capped at the item limit, quickly.
func TestItemStockSeedCostBounded(t *testing.T) {
	doc := strings.Replace(fmt.Sprintf(seedDoc, ""), "{median: 85000, p95: 150000}", "{median: 2592000, p95: 5184000}", 1)
	start := time.Now()
	got := seedRun(t, doc, time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC), 1, time.Minute)[0]
	if got <= 0 || got > 100000 {
		t.Fatalf("seeded %v items, want within (0, 100000]", got)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("seeding took %v, want a bounded cost", d)
	}
}

// TestItemStockSeedReadsEachSeriesClock: a series whose enum value declares its own timezone seeds
// from that clock's curve, not the node's. At 03:00 UTC the UTC series is at night and the
// Singapore series (11:00 local) at the plateau, so their long-run means differ and each seed
// matches its own.
func TestItemStockSeedReadsEachSeriesClock(t *testing.T) {
	doc := `
shape_profile: {timezone: UTC}
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - name: reviews_started_total
        source: test fixture
        instrument: counter
        labels:
          site:
            enum:
              - {value: emea}
              - {value: apac, shape_profile: {timezone: Asia/Singapore}}
        value: {shape: {base: 0.6}}
      - name: reviews_open
        source: test fixture
        instrument: gauge
        labels: {site: {enum: [{value: emea}, {value: apac}]}}
        stock_of: {metric: reviews_started_total, residence: {median: 85000, p95: 150000}}
`
	sg, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	curve := shape.DefaultCurve()
	rates := map[string]func(time.Time) float64{
		"emea": func(a time.Time) float64 {
			return seedBase / 60 * curve.Diurnal(a.UTC()) * curve.BusinessWeekly(a.UTC())
		},
		"apac": func(a time.Time) float64 {
			return seedBase / 60 * curve.Diurnal(a.In(sg)) * curve.BusinessWeekly(a.In(sg))
		},
	}
	at := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	w := buildApp(t, decodeStrict(t, doc))
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Shape = shape.New("UTC", nil)
	if err := w.Tick(context.Background(), at, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got := map[string]float64{}
	for _, s := range mc.Find("reviews_open") {
		got[s.Labels["site"]] = s.Value
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want a series per site", got)
	}
	for site, rate := range rates {
		mean := longRunMean(rate, at)
		t.Logf("%s: seeded %v, long-run mean %.1f", site, got[site], mean)
		if math.Abs(got[site]-mean) > 0.1*mean {
			t.Errorf("%s: seeded %v, want within 10%% of its own clock's long-run mean %.1f", site, got[site], mean)
		}
	}
}
