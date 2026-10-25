// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"math"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// clockDoc declares one gauge over a `site` enum. The workload clock is UTC; the apac value
// carries its own +8h zone. The shape base is 100, so a value reads the load percentage.
const clockDoc = `
shape_profile: {timezone: UTC}
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - name: site_sessions
        source: test fixture
        instrument: gauge
        labels:
          site:
            enum:
              - {value: emea}
              - {value: amer}
              - {value: apac, shape_profile: {timezone: Asia/Singapore}}
        value: {shape: {base: 100}}
`

// clockDay reads every hour of one weekday (Wed 2026-06-10 UTC) and returns the per-site value
// series plus the label set of the last tick.
func clockDay(t *testing.T, doc string, metric string) (map[string][]float64, []string) {
	t.Helper()
	w := buildApp(t, decodeStrict(t, doc))
	out := map[string][]float64{}
	var labels []string
	for h := range 24 {
		mc := tickN(t, w, time.Date(2026, 6, 10, h, 0, 0, 0, time.UTC), 1, 1)
		labels = labels[:0]
		for _, s := range mc.Find(metric) {
			out[s.Labels["site"]] = append(out[s.Labels["site"]], s.Value)
			labels = append(labels, s.Labels["site"])
		}
	}
	return out, labels
}

// firstPlateau is the first hour a series reaches the full load (the plateau start).
func firstPlateau(vals []float64) int {
	for h, v := range vals {
		if math.Abs(v-100) < 1e-9 {
			return h
		}
	}
	return -1
}

// TestSeriesClockEnumValue: one enum value declared on a +8h zone peaks eight hours before its
// siblings, with an identical label set; an undeclared enum reads the workload clock exactly.
func TestSeriesClockEnumValue(t *testing.T) {
	got, labels := clockDay(t, clockDoc, "site_sessions")
	if len(labels) != 3 {
		t.Fatalf("series per tick %v, want emea amer apac", labels)
	}
	emea, amer, apac := firstPlateau(got["emea"]), firstPlateau(got["amer"]), firstPlateau(got["apac"])
	if emea != 10 || amer != 10 {
		t.Fatalf("siblings plateau at %d/%d UTC, want 10 (workload clock)", emea, amer)
	}
	if apac != 2 {
		t.Fatalf("apac plateau at %d UTC, want 2 (10:00 Singapore, eight hours earlier)", apac)
	}
	for h := range 24 {
		if got["emea"][h] != got["amer"][h] {
			t.Fatalf("siblings diverge at %02d UTC: %v vs %v", h, got["emea"][h], got["amer"][h])
		}
	}

	// Undeclared: dropping the override reads the workload clock for every value, and the
	// label sets match the declared document.
	plain := strings.Replace(clockDoc, ", shape_profile: {timezone: Asia/Singapore}", "", 1)
	base, plainLabels := clockDay(t, plain, "site_sessions")
	if strings.Join(plainLabels, ",") != strings.Join(labels, ",") {
		t.Fatalf("label sets differ: declared %v, undeclared %v", labels, plainLabels)
	}
	for h := range 24 {
		if base["apac"][h] != base["emea"][h] || base["emea"][h] != got["emea"][h] {
			t.Fatalf("undeclared output changed at %02d UTC: %v", h, base)
		}
	}
	// The apac override equals an explicit workload clock of the same zone.
	sgt := strings.Replace(plain, "shape_profile: {timezone: UTC}", "shape_profile: {timezone: Asia/Singapore}", 1)
	want, _ := clockDay(t, sgt, "site_sessions")
	for h := range 24 {
		if got["apac"][h] != want["emea"][h] {
			t.Fatalf("apac at %02d UTC = %v, want the Singapore curve %v", h, got["apac"][h], want["emea"][h])
		}
	}
}

// TestSeriesClockMetricLevelAndPrecedence: a metric-level profile moves every series of the
// metric; an enum value's profile overrides it field by field; a service profile is the floor.
func TestSeriesClockMetricLevelAndPrecedence(t *testing.T) {
	doc := `
shape_profile: {timezone: UTC}
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - name: site_sessions
        source: test fixture
        instrument: gauge
        shape_profile: {timezone: Asia/Singapore, floor: 0.5}
        labels:
          site:
            enum:
              - {value: apac}
              - {value: amer, shape_profile: {timezone: America/Los_Angeles}}
        value: {shape: {base: 100}}
      - name: other_sessions
        source: test fixture
        instrument: gauge
        value: {shape: {base: 100}}
`
	w := buildApp(t, decodeStrict(t, doc))
	read := func(h int) (map[string]float64, float64) {
		mc := tickN(t, w, time.Date(2026, 6, 10, h, 0, 0, 0, time.UTC), 1, 1)
		return byLabel(t, mc.Find("site_sessions"), "site"), mc.Find("other_sessions")[0].Value
	}
	// 20:00 UTC Wed: 04:00 Singapore Thu (floor 0.5 inherited from the metric), 13:00 Los
	// Angeles (plateau; the enum keeps the metric's floor, which the plateau hides).
	sites, other := read(20)
	if sites["apac"] != 50 || sites["amer"] != 100 {
		t.Fatalf("20:00 UTC = %v, want apac 50 (metric floor) amer 100 (enum zone)", sites)
	}
	// A metric without a declaration still reads the workload clock (20:00 UTC = floor 0.1x100).
	if math.Abs(other-10) > 1e-9 {
		t.Fatalf("undeclared metric = %v, want the workload floor 10", other)
	}
	// 06:00 UTC: 14:00 Singapore (plateau), 23:00 Los Angeles prior day (floor 0.5 by merge).
	sites, _ = read(6)
	if sites["apac"] != 100 || sites["amer"] != 50 {
		t.Fatalf("06:00 UTC = %v, want apac 100 amer 50", sites)
	}
}

func buildDoc(t *testing.T, doc string) error {
	t.Helper()
	cfg := NewConfig().(*Config)
	dec := yaml.NewDecoder(strings.NewReader(doc))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return err
	}
	_, err := build(cfg, coretestBinding())
	return err
}

func TestSeriesClockRejectsBadInput(t *testing.T) {
	const sgt = ", shape_profile: {timezone: Asia/Singapore}"
	constValue := strings.Replace(clockDoc, "value: {shape: {base: 100}}", "value: {const: 1}", 1)
	cases := map[string]string{
		"unknown time zone": strings.Replace(clockDoc, "Asia/Singapore", "Nowhere/City", 1),
		"floor":             strings.Replace(clockDoc, "{timezone: Asia/Singapore}", "{floor: 2}", 1),
		"needs a shape":     constValue,
	}
	for name, doc := range cases {
		if err := buildDoc(t, doc); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s: err %v, want rejection containing it", name, err)
		}
	}
	// Control: the same const metric with no clock declared builds.
	if err := buildDoc(t, strings.Replace(constValue, sgt, "", 1)); err != nil {
		t.Fatalf("undeclared const metric rejected: %v", err)
	}
	// A log enum has no series to move.
	logDoc := `
services:
  - name: api
    type: web
    entry: true
    logs:
      - source: app
        stream_labels:
          tier: {enum: [{value: a, shape_profile: {timezone: Asia/Singapore}}]}
        body: {msg: {const_str: hi}}
`
	if err := buildDoc(t, logDoc); err == nil || !strings.Contains(err.Error(), "metric labels only") {
		t.Fatalf("log enum clock: err %v, want rejection", err)
	}
	// Derived metrics read another metric's clock; they take none of their own.
	derived := `
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - {name: q_in_total, source: f, instrument: counter, value: {const: 6}}
      - {name: q_depth, source: f, instrument: gauge, shape_profile: {timezone: Asia/Singapore}, stock_of: {metric: q_in_total, dwell_seconds: 60}}
`
	if err := buildDoc(t, derived); err == nil || !strings.Contains(err.Error(), "stock_of") {
		t.Fatalf("stock_of clock: err %v, want rejection", err)
	}
}
