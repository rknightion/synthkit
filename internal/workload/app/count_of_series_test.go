// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// perSeriesDoc declares the counter as one spec per series (const_str labels, one value domain
// each) and histograms that each pick one counter series (equal labels), plus one projection
// ({route} only) that sums two counter series. A counter series with a fractional increment
// (0.7, 1.3) exercises the deterministic carry.
const perSeriesDoc = `
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: api
    type: web
    entry: true
    metrics:
      - name: decisions_total
        instrument: counter
        labels: {route: {const_str: a}, outcome: {const_str: approved}}
        value: {const: 0.7}
      - name: decisions_total
        instrument: counter
        labels: {route: {const_str: a}, outcome: {const_str: rejected}}
        value: {const: 1.3}
      - name: decisions_total
        instrument: counter
        labels: {route: {const_str: b}, outcome: {const_str: approved}}
        value: {int_range: {min: 0, max: 3}}
      - name: decision_seconds
        instrument: histogram
        count_of: decisions_total
        buckets: [1, 10, 100]
        labels: {route: {const_str: a}, outcome: {const_str: approved}}
        value: {lognormal: {median: 1, p95: 3}}
      - name: decision_seconds
        instrument: histogram
        count_of: decisions_total
        buckets: [1, 10, 100]
        labels: {route: {const_str: a}, outcome: {const_str: rejected}}
        value: {lognormal: {median: 20, p95: 60}}
      - name: decision_seconds
        instrument: histogram
        count_of: decisions_total
        buckets: [1, 10, 100]
        labels: {route: {const_str: b}, outcome: {const_str: approved}}
        value: {lognormal: {median: 2, p95: 6}}
      - name: route_seconds
        instrument: histogram
        count_of: decisions_total
        buckets: [1, 10, 100]
        labels: {route: {const_str: a}}
        value: {lognormal: {median: 5, p95: 15}}
`

// seriesBy keys a metric's series by its route/outcome labels (outcome empty when absent).
func seriesBy(series []promrw.Series) map[string]float64 {
	out := map[string]float64{}
	for _, s := range series {
		out[s.Labels["route"]+"/"+s.Labels["outcome"]] = s.Value
	}
	return out
}

// tickEach ticks the workload once a minute from start and calls check with each tick's batch.
func tickEach(t *testing.T, w *Workload, start time.Time, n int, check func(i int, mc *coretest.MetricCapture)) {
	t.Helper()
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	for i := range n {
		mc.Batches = nil
		if err := w.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		check(i, mc)
	}
}

func buildErr(doc string) error {
	cfg := NewConfig().(*Config)
	dec := yaml.NewDecoder(bytes.NewReader([]byte(doc)))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return err
	}
	_, err := buildGraph(cfg.Services)
	return err
}

// TestCountOfPerSeriesFollowsMatchingCounter: with one counter spec per series, each histogram
// spec observes exactly its matching counter series' increments, every tick. The per-tick
// observation count is the counter's increase with the fractional remainder carried, so the
// cumulative _count stays within half an observation of the cumulative counter and each tick
// adds a whole number.
func TestCountOfPerSeriesFollowsMatchingCounter(t *testing.T) {
	w := buildApp(t, decodeStrict(t, perSeriesDoc))
	prev := map[string]float64{}
	var last *coretest.MetricCapture
	tickEach(t, w, time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC), 40, func(i int, mc *coretest.MetricCapture) {
		last = mc
		ctr := seriesBy(mc.Find("decisions_total"))
		cnt := seriesBy(mc.Find("decision_seconds_count"))
		if len(ctr) != 3 {
			t.Fatalf("tick %d: counter series %v, want 3", i, ctr)
		}
		for key, c := range ctr {
			if math.Abs(cnt[key]-c) > 0.5+1e-9 {
				t.Fatalf("tick %d %s: histogram _count %v, counter %v (more than 0.5 apart)", i, key, cnt[key], c)
			}
			if d := cnt[key] - prev[key]; d != math.Trunc(d) || d < 0 {
				t.Fatalf("tick %d %s: _count increase %v is not a whole number of observations", i, key, d)
			}
			prev[key] = cnt[key]
		}
		// The projection onto {route=a} observes both route-a counter series.
		sum := ctr["a/approved"] + ctr["a/rejected"]
		if got := seriesBy(mc.Find("route_seconds_count"))["a/"]; math.Abs(got-sum) > 0.5+1e-9 {
			t.Fatalf("tick %d: route a histogram _count %v, summed counter series %v", i, got, sum)
		}
	})
	// Each histogram spec keeps its own value model: rejected observations are an order of
	// magnitude longer than approved ones.
	cnt := seriesBy(last.Find("decision_seconds_count"))
	sum := seriesBy(last.Find("decision_seconds_sum"))
	if cnt["a/rejected"] < 10 || cnt["a/approved"] < 10 {
		t.Fatalf("too few observations to compare means: %v", cnt)
	}
	if r := (sum["a/rejected"] / cnt["a/rejected"]) / (sum["a/approved"] / cnt["a/approved"]); r < 5 {
		t.Fatalf("mean ratio rejected/approved = %v, want about 20 (each spec its own lognormal)", r)
	}
}

// TestCountOfPerSeriesSpecsAreIndependent: a series whose counter does not increment observes
// nothing while its siblings do.
func TestCountOfPerSeriesSpecsAreIndependent(t *testing.T) {
	doc := strings.Replace(perSeriesDoc, "value: {int_range: {min: 0, max: 3}}", "value: {const: 0}", 1)
	w := buildApp(t, decodeStrict(t, doc))
	tickEach(t, w, time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC), 5, func(i int, mc *coretest.MetricCapture) {
		cnt := seriesBy(mc.Find("decision_seconds_count"))
		if cnt["b/approved"] != 0 {
			t.Fatalf("tick %d: idle counter series observed %v values", i, cnt["b/approved"])
		}
		if cnt["a/rejected"] == 0 && i > 0 {
			t.Fatalf("tick %d: active sibling observed nothing", i)
		}
	})
}

// crossNodeDoc splits the counters over two nodes and reads them from a third, by service name.
const crossNodeDoc = `
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: api
    type: web
    entry: true
    calls: [intake, review, summary]
  - name: intake
    type: web
    metrics:
      - name: decisions_total
        instrument: counter
        labels: {route: {const_str: a}, outcome: {const_str: approved}}
        value: {const: 0.7}
  - name: review
    type: web
    metrics:
      - name: decisions_total
        instrument: counter
        labels: {route: {const_str: a}, outcome: {const_str: rejected}}
        value: {const: 1.3}
  - name: summary
    type: web
    metrics:
      - name: decision_seconds
        instrument: histogram
        count_of: decisions_total
        count_of_service: intake
        buckets: [1, 10, 100]
        labels: {route: {const_str: a}, outcome: {const_str: approved}}
        value: {lognormal: {median: 1, p95: 3}}
      - name: decision_seconds
        instrument: histogram
        count_of: decisions_total
        count_of_service: review
        buckets: [1, 10, 100]
        labels: {route: {const_str: a}, outcome: {const_str: rejected}}
        value: {lognormal: {median: 20, p95: 60}}
`

// lateSourceDoc declares the histogram's service before the service holding its counter.
const lateSourceDoc = `
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: api
    type: web
    entry: true
    calls: [summary, intake]
  - name: summary
    type: web
    metrics:
      - name: decision_seconds
        instrument: histogram
        count_of: decisions_total
        count_of_service: intake
        buckets: [1, 10, 100]
        value: {lognormal: {median: 1, p95: 3}}
  - name: intake
    type: web
    metrics:
      - name: decisions_total
        instrument: counter
        value: {const: 0.7}
`

// TestCountOfCrossNodeFollowsNamedService: a histogram on one node counts the increments of the
// counter on the service it names, every tick, including the fractional carry; a series is not
// confused by the same-named counter on another node.
func TestCountOfCrossNodeFollowsNamedService(t *testing.T) {
	w := buildApp(t, decodeStrict(t, crossNodeDoc))
	prev := map[string]float64{}
	tickEach(t, w, time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC), 30, func(i int, mc *coretest.MetricCapture) {
		ctr := map[string]float64{}
		for _, s := range mc.Find("decisions_total") {
			ctr[s.Labels["service_name"]+"/"+s.Labels["outcome"]] = s.Value
		}
		cnt := map[string]float64{}
		for _, s := range mc.Find("decision_seconds_count") {
			cnt[s.Labels["outcome"]] = s.Value
		}
		for outcome, c := range map[string]float64{"approved": ctr["intake/approved"], "rejected": ctr["review/rejected"]} {
			if c == 0 {
				t.Fatalf("tick %d: no %s counter series in %v", i, outcome, ctr)
			}
			if math.Abs(cnt[outcome]-c) > 0.5+1e-9 {
				t.Fatalf("tick %d %s: histogram _count %v, source counter %v", i, outcome, cnt[outcome], c)
			}
			if d := cnt[outcome] - prev[outcome]; d != math.Trunc(d) || d < 0 {
				t.Fatalf("tick %d %s: _count increase %v is not whole", i, outcome, d)
			}
			prev[outcome] = cnt[outcome]
		}
	})
}

// TestCountOfCrossNodePausedSourceObservesNothing: under a paused workload (volume 0) the
// histogram stops observing and stays flat. The stale-increment guard itself is covered by
// TestCountOfCrossNodeNeverReplaysAnOlderTick.
func TestCountOfCrossNodePausedSourceObservesNothing(t *testing.T) {
	w := buildApp(t, decodeStrict(t, crossNodeDoc))
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	start := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	count := func() float64 { return seriesBy(mc.Find("decision_seconds_count"))["a/approved"] }
	for i := range 3 {
		mc.Batches = nil
		if err := w.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), world); err != nil {
			t.Fatal(err)
		}
	}
	before := count()
	if before == 0 {
		t.Fatal("histogram observed nothing while the source ran")
	}
	world.Shape.SetVolumeMultiplier(0)
	for i := 3; i < 6; i++ {
		mc.Batches = nil
		if err := w.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), world); err != nil {
			t.Fatal(err)
		}
	}
	if after := count(); after != before {
		t.Fatalf("paused source: histogram _count moved %v -> %v", before, after)
	}
}

func TestCountOfSeriesRejected(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{
			"histogram label value the counter never takes",
			strings.Replace(perSeriesDoc, "labels: {route: {const_str: b}, outcome: {const_str: approved}}\n        value: {lognormal", "labels: {route: {const_str: c}, outcome: {const_str: approved}}\n        value: {lognormal", 1),
			"is not a value of counter",
		},
		{
			"histogram combination no counter series carries",
			strings.Replace(perSeriesDoc, "labels: {route: {const_str: b}, outcome: {const_str: approved}}\n        value: {lognormal", "labels: {route: {const_str: b}, outcome: {const_str: rejected}}\n        value: {lognormal", 1),
			"matches no series of counter",
		},
		{
			"unknown source service",
			strings.Replace(crossNodeDoc, "count_of_service: intake", "count_of_service: nope", 1),
			`count_of_service "nope"`,
		},
		{
			"source service declared later",
			lateSourceDoc,
			"declared before",
		},
		{
			"source service lacks the counter",
			strings.Replace(crossNodeDoc, "count_of_service: review", "count_of_service: api", 1),
			"is not a counter on service",
		},
		{
			"cross-node value the source counter never takes",
			strings.Replace(crossNodeDoc, "count_of_service: review", "count_of_service: intake", 1),
			"is not a value of counter",
		},
		{
			"count_of_service without count_of",
			strings.Replace(crossNodeDoc, "        count_of: decisions_total\n        count_of_service: intake\n", "        count_of_service: intake\n", 1),
			"count_of_service requires count_of",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := buildErr(tc.doc); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want rejection containing %q", err, tc.want)
			}
		})
	}
}

// TestCountOfLabelValueSubsetObservesSelectedSeries: a histogram that names only some of the
// counter's label values (here the east series of an enum counter) is valid and observes the
// increments of the matching counter series alone.
func TestCountOfLabelValueSubsetObservesSelectedSeries(t *testing.T) {
	doc := strings.Replace(subsetDoc, "              - {value: east}\n              - {value: west, value_scale: 2}", "              - {value: east}", 1)
	w := buildApp(t, decodeStrict(t, doc))
	mc := tickN(t, w, time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC), 3, 1)
	ctr := sumBy(mc.Find("jobs_total"), "region")
	cnt := sumBy(mc.Find("job_wait_seconds_count"), "region")
	if len(cnt) != 1 || ctr["east"] == 0 || math.Abs(cnt["east"]-ctr["east"]) > 0.5+1e-9 {
		t.Fatalf("histogram counts %v, counter %v: want the east series only, equal", cnt, ctr)
	}
}

// TestCountOfCrossNodeNeverReplaysAnOlderTick: a reader takes the source's increments only when
// they were recorded at the tick being evaluated; an older tick's map reads as no increments.
func TestCountOfCrossNodeNeverReplaysAnOlderTick(t *testing.T) {
	w := buildApp(t, decodeStrict(t, crossNodeDoc))
	reader := w.graph.byName["summary"]
	spec := reader.metrics[0]
	t0 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	recorded := map[string]float64{incKey("decisions_total", map[string]string{"route": "a", "outcome": "approved"}): 5}
	w.publishIncs("intake", t0, recorded)
	if _, got := w.sourceIncs(reader, spec, nil, t0); len(got) != 1 {
		t.Fatalf("same tick: increments %v, want the recorded map", got)
	}
	if _, got := w.sourceIncs(reader, spec, nil, t0.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("next tick: increments %v replayed from the previous tick", got)
	}
	// A service nothing names records nothing to publish.
	w.publishIncs("summary", t0, recorded)
	if _, ok := w.exported["summary"]; ok {
		t.Fatal("a node no count_of_service names published its increments")
	}
}
