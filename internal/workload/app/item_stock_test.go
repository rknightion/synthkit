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

// itemStockDoc declares a counter of 60 arrivals per 60 s tick (1/s) on api, an item-stock count
// gauge and an oldest-age gauge over it (one shared population), and a normal-valued gauge on a
// later node whose draws must not move when the stock is declared. %s is the stock gauges. The
// stock reads the const counter, not the request rate, so traffic is a trickle: minted requests
// only add span-metric work over these 40 to 120 tick runs that no assertion reads.
const itemStockDoc = `
traffic: {off_peak_rps: 0, peak_rps: 0.01}
services:
  - name: api
    type: web
    entry: true
    calls: [worker]
    custom_metrics:
      - {name: reviews_started_total, source: test fixture, instrument: counter, value: {const: 60}}
%s  # A later node: engine draws taken by api's stock would shift this gauge in the same tick.
  - name: worker
    type: web
    custom_metrics:
      - {name: review_jitter, source: test fixture, instrument: gauge, value: {normal: {mean: 10, stddev: 3}}}
`

const itemStockGauges = `      - name: reviews_open
        source: test fixture
        instrument: gauge
        stock_of: {metric: reviews_started_total, residence: %[1]s%[2]s}
      - name: reviews_oldest_age_seconds
        source: test fixture
        instrument: gauge
        stock_of: {metric: reviews_started_total, residence: %[1]s%[2]s, field: oldest_age_seconds}
`

func itemDoc(residence, stall string) string {
	return fmt.Sprintf(itemStockDoc, fmt.Sprintf(itemStockGauges, residence, stall))
}

type stockReading struct{ count, age, jitter float64 }

// runItemStock ticks one workload every 60 s for n ticks with the given incidents and returns
// api's readings per tick.
func runItemStock(t *testing.T, doc string, start time.Time, n int, incidents []string) []stockReading {
	t.Helper()
	w := buildApp(t, decodeStrict(t, doc))
	out := make([]stockReading, 0, n)
	for i := range n {
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		world.Shape = shape.New("UTC", incidents)
		if err := w.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		one := func(name string) float64 {
			s := mc.Find(name)
			if len(s) != 1 {
				return math.NaN()
			}
			return s[0].Value
		}
		out = append(out, stockReading{count: one("reviews_open"), age: one("reviews_oldest_age_seconds"), jitter: one("review_jitter")})
	}
	return out
}

var itemStart = time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)

// TestItemStockHoldsNearRateTimesResidence: fed 1 item/s, the stock is a whole-item count that
// holds near rate × the residence mean (600 for a fixed 600 s residence; 600 × exp(sigma²/2)
// ≈ 750 for median 600, p95 1800), from the first tick on.
func TestItemStockHoldsNearRateTimesResidence(t *testing.T) {
	for name, tc := range map[string]struct {
		residence string
		mean, tol float64
	}{
		"fixed":     {"{median: 600, p95: 600}", 600, 0.02},
		"lognormal": {"{median: 600, p95: 1800}", 600 * math.Exp(math.Pow(math.Log(3)/1.6448536269514722, 2)/2), 0.12},
	} {
		got := runItemStock(t, itemDoc(tc.residence, ""), itemStart, 120, nil)
		var sum float64
		for i, r := range got {
			if r.count != math.Trunc(r.count) || r.count < 0 {
				t.Fatalf("%s tick %d: count %v is not a whole item count", name, i, r.count)
			}
			if math.Abs(r.count-tc.mean) > 2*tc.tol*tc.mean {
				t.Fatalf("%s tick %d: count %v, want near %v", name, i, r.count, tc.mean)
			}
			sum += r.count
		}
		if avg := sum / float64(len(got)); math.Abs(avg-tc.mean) > tc.tol*tc.mean {
			t.Fatalf("%s: mean count %v, want %v within %v%%", name, avg, tc.mean, 100*tc.tol)
		}
	}
}

// TestItemStockNegativeCounterSeedsEmpty: a counter model that can decrement seeds no items.
func TestItemStockNegativeCounterSeedsEmpty(t *testing.T) {
	doc := strings.Replace(itemDoc("{median: 600, p95: 600}", ""), "value: {const: 60}", "value: {const: -60}", 1)
	for i, r := range runItemStock(t, doc, itemStart, 3, nil) {
		if r.count != 0 || r.age != 0 {
			t.Fatalf("tick %d: %+v, want an empty stock", i, r)
		}
	}
}

// TestItemStockOldestAgeHasNoHorizon: a day-scale residence (median 85,000 s, p95 150,000 s)
// keeps items older than the causal queue's 24 h horizon, with the count still near rate × mean.
func TestItemStockOldestAgeHasNoHorizon(t *testing.T) {
	doc := strings.Replace(itemDoc("{median: 85000, p95: 150000}", ""), "value: {const: 60}", "value: {const: 0.6}", 1)
	got := runItemStock(t, doc, itemStart, 3, nil)
	sigma := math.Log(150000.0/85000) / 1.6448536269514722
	mean := 0.01 * 85000 * math.Exp(sigma*sigma/2)
	last := got[len(got)-1]
	if math.Abs(last.count-mean) > 0.1*mean {
		t.Fatalf("count %v, want near %v", last.count, mean)
	}
	if last.age <= 24*3600 {
		t.Fatalf("oldest age %v s, want beyond the 24 h causal horizon", last.age)
	}
}

// TestItemStockStallGrowsAndRecoveryDrains: while the stall_mode incident is active on api the
// count and the oldest age grow every tick (no completions); after recovery the frozen items
// finish and the stock drains back to its baseline.
func TestItemStockStallGrowsAndRecoveryDrains(t *testing.T) {
	doc := itemDoc("{median: 600, p95: 900}", ", stall_mode: latency_storm")
	stallAt := itemStart.Add(30 * time.Minute)
	incident := "latency_storm@" + stallAt.Format(time.RFC3339) + "/30m@api"
	got := runItemStock(t, doc, itemStart, 150, []string{incident})
	base := got[29]
	for i := 31; i < 60; i++ {
		if got[i].count <= got[i-1].count || got[i].age <= got[i-1].age {
			t.Fatalf("stall tick %d: count %v age %v, want both above tick %d's %v %v", i, got[i].count, got[i].age, i-1, got[i-1].count, got[i-1].age)
		}
		if math.Abs(got[i].age-got[i-1].age-60) > 1e-6 {
			t.Fatalf("stall tick %d: oldest age grew %v s, want the 60 s tick", i, got[i].age-got[i-1].age)
		}
	}
	if grown := got[59].count - base.count; grown < 1700 {
		t.Fatalf("stall grew the stock by %v, want about 30 min × 1/s", grown)
	}
	for i := 120; i < 150; i++ {
		if math.Abs(got[i].count-base.count) > 0.15*base.count {
			t.Fatalf("recovered tick %d: count %v, want drained back near %v", i, got[i].count, base.count)
		}
		if got[i].age > 3*900 {
			t.Fatalf("recovered tick %d: oldest age %v, want back under the residence tail", i, got[i].age)
		}
	}
	// Without stall_mode the same incident leaves the stock at its baseline.
	plain := runItemStock(t, itemDoc("{median: 600, p95: 900}", ""), itemStart, 60, []string{incident})
	if c := plain[59].count; math.Abs(c-base.count) > 0.15*base.count {
		t.Fatalf("no stall_mode: count %v under the incident, want near %v", c, base.count)
	}
}

// TestItemStockDeterministic: two runs over the same ticks and incident agree exactly, and
// declaring the stock does not shift any engine draw (the sibling normal gauge is unchanged).
func TestItemStockDeterministic(t *testing.T) {
	doc := itemDoc("{median: 600, p95: 1800}", ", stall_mode: latency_storm")
	incident := []string{"latency_storm@" + itemStart.Add(10*time.Minute).Format(time.RFC3339) + "/10m@api"}
	a := runItemStock(t, doc, itemStart, 40, incident)
	b := runItemStock(t, doc, itemStart, 40, incident)
	bare := runItemStock(t, fmt.Sprintf(itemStockDoc, ""), itemStart, 40, incident)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("tick %d differs between runs: %+v vs %+v", i, a[i], b[i])
		}
		if a[i].jitter != bare[i].jitter {
			t.Fatalf("tick %d: sibling gauge %v with the stock, %v without: the stock consumed engine draws", i, a[i].jitter, bare[i].jitter)
		}
	}
}

// TestItemStockPausedDrains: a paused workload adds no items and the stock drains to empty.
func TestItemStockPausedDrains(t *testing.T) {
	w := buildApp(t, decodeStrict(t, itemDoc("{median: 600, p95: 600}", "")))
	mc := tickN(t, w, itemStart, 5, 1)
	if s := mc.Find("reviews_open"); len(s) != 1 || s[0].Value < 590 {
		t.Fatalf("running stock = %v, want about 600", s)
	}
	mc = tickN(t, w, itemStart.Add(5*time.Minute), 11, 0)
	if s := mc.Find("reviews_open"); len(s) != 1 || s[0].Value != 0 {
		t.Fatalf("paused stock after 11 min = %v, want drained to 0", s)
	}
	if s := mc.Find("reviews_oldest_age_seconds"); len(s) != 1 || s[0].Value != 0 {
		t.Fatalf("paused oldest age = %v, want 0 when empty", s)
	}
}

// multiSpecDoc declares one counter as one spec per series (the shape a blueprint with
// per-series values uses): lane express 120 and standard 60 per tick, each also split by route.
const multiSpecProjection = `      - name: jobs_by_lane
        source: test fixture
        instrument: gauge
        labels: {lane: {enum: [{value: express}, {value: standard}]}}
        stock_of: {metric: jobs_started_total, dwell_seconds: 600}
`

const multiSpecItems = `      - name: jobs_items_by_lane
        source: test fixture
        instrument: gauge
        labels: {lane: {enum: [{value: express}, {value: standard}]}}
        stock_of: {metric: jobs_started_total, residence: {median: 600, p95: 600}}
`

const multiSpecDoc = `
services:
  - name: api
    type: web
    entry: true
    # Inline metrics may repeat a name (custom_metrics reserve each name once).
    metrics:
      - name: jobs_started_total
        instrument: counter
        labels: {lane: {const_str: express}, route: {enum: [{value: a}, {value: b}]}}
        value: {const: 60}
      - name: jobs_started_total
        instrument: counter
        labels: {lane: {const_str: standard}, route: {const_str: a}}
        value: {const: 60}
    custom_metrics:
      - name: jobs_in_progress
        source: test fixture
        instrument: gauge
        stock_of: {metric: jobs_started_total, dwell_seconds: 600}
      - name: job_duration_seconds
        source: test fixture
        instrument: histogram
        count_of: jobs_started_total
        buckets: [60, 600]
        labels: {lane: {enum: [{value: express}, {value: standard}]}}
        value: {lognormal: {median: 30, p95: 90}}
`

// TestDerivedMultiSpecCounterResolvesPerSeries: stock_of and count_of read every same-named
// counter spec on the node, per series, and a stock gauge's labels project the counter's series.
func TestDerivedMultiSpecCounterResolvesPerSeries(t *testing.T) {
	w := buildApp(t, decodeStrict(t, multiSpecDoc))
	mc := tickN(t, w, itemStart, 3, 1)
	inherit := map[string]float64{}
	for _, s := range mc.Find("jobs_in_progress") {
		inherit[s.Labels["lane"]+"/"+s.Labels["route"]] = s.Value
	}
	if len(inherit) != 3 || inherit["express/a"] != 600 || inherit["express/b"] != 600 || inherit["standard/a"] != 600 {
		t.Fatalf("inherited per-series stock = %v, want 600 for each of express/a, express/b, standard/a", inherit)
	}
	ctr := map[string]float64{}
	for _, s := range mc.Find("jobs_started_total") {
		ctr[s.Labels["lane"]] += s.Value
	}
	cnt := byLabel(t, mc.Find("job_duration_seconds_count"), "lane")
	if cnt["express"] != ctr["express"] || cnt["standard"] != ctr["standard"] || ctr["express"] != 360 {
		t.Fatalf("histogram counts %v, want the counter per lane %v", cnt, ctr)
	}
}

// TestStockProjectsMultiSpecCounter: a fluid or item stock's labels project the series of every
// same-named counter spec (lane express sums its two routes).
func TestStockProjectsMultiSpecCounter(t *testing.T) {
	w := buildApp(t, decodeStrict(t, multiSpecDoc+multiSpecProjection+multiSpecItems))
	mc := tickN(t, w, itemStart, 3, 1)
	if got := byLabel(t, mc.Find("jobs_by_lane"), "lane"); got["express"] != 1200 || got["standard"] != 600 {
		t.Fatalf("projected stock = %v, want express 1200 standard 600", got)
	}
	if got := byLabel(t, mc.Find("jobs_items_by_lane"), "lane"); got["express"] < 1190 || got["express"] > 1210 || got["standard"] < 595 || got["standard"] > 605 {
		t.Fatalf("projected item stock = %v, want about express 1200 standard 600", got)
	}
}

// TestStockOfDeclarationRejected: item-stock and projection declarations fail loudly.
func TestStockOfDeclarationRejected(t *testing.T) {
	items := itemDoc("{median: 600, p95: 600}", "")
	proj := multiSpecDoc + multiSpecProjection
	for name, doc := range map[string]string{
		"exactly one of dwell_seconds and residence": strings.Replace(items, "residence: {median: 600, p95: 600}}", "residence: {median: 600, p95: 600}, dwell_seconds: 60}", 1),
		"need stock_of.residence":                    strings.Replace(multiSpecDoc, "dwell_seconds: 600}", "dwell_seconds: 600, stall_mode: latency_storm}", 1),
		"stock_of.field \"depth\" invalid":           strings.Replace(items, ", field: oldest_age_seconds}", ", field: depth}", 1),
		"p95 300 must be >= median":                  strings.Replace(items, "p95: 600}}", "p95: 300}}", 1),
		"surrounding whitespace":                     strings.Replace(items, "p95: 600}}", "p95: 600}, stall_mode: ' x'}", 1),
		"is not a label of counter":                  strings.Replace(proj, "labels: {lane: {enum: [{value: express}, {value: standard}]}}\n        stock_of: {metric: jobs_started_total, dwell", "labels: {tier: {const_str: x}}\n        stock_of: {metric: jobs_started_total, dwell", 1),
		"value \"other\" is not a value":             strings.Replace(proj, "[{value: express}, {value: standard}]}}\n        stock_of: {metric: jobs_started_total, dwell", "[{value: express}, {value: other}]}}\n        stock_of: {metric: jobs_started_total, dwell", 1),
		"matches no series":                          strings.Replace(proj, "labels: {lane: {enum: [{value: express}, {value: standard}]}}\n        stock_of: {metric: jobs_started_total, dwell", "labels: {lane: {const_str: standard}, route: {const_str: b}}\n        stock_of: {metric: jobs_started_total, dwell", 1),
		"value_scale is not valid with stock_of":     strings.Replace(proj, "[{value: express}, {value: standard}]}}\n        stock_of: {metric: jobs_started_total, dwell", "[{value: express, value_scale: 2}, {value: standard}]}}\n        stock_of: {metric: jobs_started_total, dwell", 1),
	} {
		if doc == items || doc == multiSpecDoc || doc == proj {
			t.Fatalf("%s: replacement did not apply", name)
		}
		err := buildDoc(t, doc)
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("err %v, want rejection containing %q", err, name)
		}
	}
}
