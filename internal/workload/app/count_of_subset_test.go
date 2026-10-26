// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// subsetDoc declares a counter on {region, route} that follows the load curve, a histogram that
// counts only by {region} (a subset of the counter's labels), and a histogram on the counter's
// exact labels.
const subsetDoc = `
shape_profile:
  timezone: UTC
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - name: jobs_total
        source: test fixture
        instrument: counter
        labels:
          region:
            enum:
              - {value: east, rate_scale: 2}
              - {value: west}
          route:
            enum:
              - {value: batch}
              - {value: interactive, rate_scale: 3}
        value: {shape: {base: 60}}
      - name: job_wait_seconds
        source: test fixture
        instrument: histogram
        count_of: jobs_total
        buckets: [1, 10, 100]
        labels:
          region:
            enum:
              - {value: east}
              - {value: west, value_scale: 2}
        value: {lognormal: {median: 5, p95: 20}}
      - name: job_run_seconds
        source: test fixture
        instrument: histogram
        count_of: jobs_total
        buckets: [1, 10, 100]
        labels:
          region:
            enum:
              - {value: east}
              - {value: west}
          route:
            enum:
              - {value: batch}
              - {value: interactive}
        value: {lognormal: {median: 5, p95: 20}}
`

func sumBy(series []promrw.Series, key string) map[string]float64 {
	out := map[string]float64{}
	for _, s := range series {
		out[s.Labels[key]] += s.Value
	}
	return out
}

// TestCountOfLabelSubsetProjectsCounter: a count_of histogram whose labels are a subset of the
// counter's observes, per series and tick, the summed increments of the counter series that match
// it, so its _count follows the load curve (lower at night than at the plateau).
func TestCountOfLabelSubsetProjectsCounter(t *testing.T) {
	night := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	peak := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	totals := map[string]float64{}
	for name, at := range map[string]time.Time{"night": night, "peak": peak} {
		w := buildApp(t, decodeStrict(t, subsetDoc))
		mc := tickN(t, w, at, 1, 1)
		ctr := sumBy(mc.Find("jobs_total"), "region")
		cnt := sumBy(mc.Find("job_wait_seconds_count"), "region")
		if len(cnt) != 2 {
			t.Fatalf("%s: histogram regions %v, want east and west", name, cnt)
		}
		for _, region := range []string{"east", "west"} {
			if ctr[region] == 0 || math.Abs(cnt[region]-ctr[region]) >= 1 {
				t.Fatalf("%s: %s histogram count %v != summed counter increments %v", name, region, cnt[region], ctr[region])
			}
		}
		// east carries rate_scale 2 on the counter, so it observes twice west's count.
		if r := cnt["east"] / cnt["west"]; math.Abs(r-2) > 0.2 {
			t.Fatalf("%s: east/west count ratio %v, want about 2 (counter rate_scale)", name, r)
		}
		totals[name] = cnt["east"] + cnt["west"]
		// value_scale still shifts that combination's observed values.
		sum := sumBy(mc.Find("job_wait_seconds_sum"), "region")
		if m := (sum["west"] / cnt["west"]) / (sum["east"] / cnt["east"]); m < 1.4 || m > 2.8 {
			t.Fatalf("%s: mean ratio west/east = %v, want about value_scale 2", name, m)
		}
	}
	if !(totals["night"] > 0 && totals["night"] < totals["peak"]/2) {
		t.Fatalf("night observations %v not well below peak %v", totals["night"], totals["peak"])
	}
}

// TestCountOfIdenticalLabelsUnchanged: a histogram on the counter's exact labels still observes
// the one matching counter series' increment per combination.
func TestCountOfIdenticalLabelsUnchanged(t *testing.T) {
	w := buildApp(t, decodeStrict(t, subsetDoc))
	mc := tickN(t, w, time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC), 1, 1)
	ctr := mc.Find("jobs_total")
	cnt := mc.Find("job_run_seconds_count")
	if len(cnt) != len(ctr) || len(cnt) != 4 {
		t.Fatalf("series: histogram %d counter %d, want 4", len(cnt), len(ctr))
	}
	for _, c := range ctr {
		var got float64 = -1
		for _, h := range cnt {
			if h.Labels["region"] == c.Labels["region"] && h.Labels["route"] == c.Labels["route"] {
				got = h.Value
			}
		}
		if got < 0 || math.Abs(got-c.Value) >= 1 {
			t.Fatalf("%v: histogram count %v != counter %v", c.Labels, got, c.Value)
		}
	}
}

func TestCountOfLabelSubsetRejected(t *testing.T) {
	labelDoc := "        labels:\n          region:\n            enum:\n              - {value: east}\n              - {value: west, value_scale: 2}"
	for _, tc := range []struct{ name, doc string }{
		{"is not a label of counter", strings.Replace(subsetDoc, labelDoc, labelDoc+"\n          tier: {const_str: gold}", 1)},
		{"is not a value of counter", strings.Replace(subsetDoc, "              - {value: west, value_scale: 2}", "              - {value: north}", 1)},
	} {
		name, doc := tc.name, tc.doc
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
