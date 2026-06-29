// SPDX-License-Identifier: AGPL-3.0-only

package fleetmgmt_test

// variation_test.go — per-series value variation tests for fleet_management.
//
// (A) Peer series for the same metric (different collectors) must have DISTINCT values.
// (B) A single series drifts across ≥5 distinct values over 30 ticks at 13-minute steps.
//
// Varied metrics tested: alloy_resources_process_resident_memory_bytes,
// alloy_resources_process_cpu_seconds_total, alloy_resources_machine_rx_bytes_total,
// go_goroutines, go_memstats_heap_inuse_bytes, scrape_duration_seconds.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// buildVariationConstruct builds a fleet_management construct with 3 linux collectors
// (enough peer series to test distinctness).
func buildVariationConstruct(t *testing.T) core.Construct {
	t.Helper()
	cfg := &fleetmgmt.Config{CollectorsPerOS: map[string]int{"linux": 3}}
	fx := &fixture.Set{
		Seed:    "vartest",
		Cluster: coretest.Cluster(),
	}
	c, err := fleetmgmt.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

// tickAt calls Tick at the given time and returns the MetricCapture.
func tickAt(t *testing.T, c core.Construct, now time.Time) *coretest.MetricCapture {
	t.Helper()
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick at %v: %v", now, err)
	}
	return mc
}

// valsByCollector returns a map of collector_id → value for the given metric name.
func valsByCollector(mc *coretest.MetricCapture, name string) map[string]float64 {
	out := map[string]float64{}
	for _, s := range mc.Find(name) {
		if id, ok := s.Labels["collector_id"]; ok {
			out[id] = s.Value
		}
	}
	return out
}

// TestPeerSeriesDistinct verifies that ≥2 collectors with the same metric have distinct values.
// This will FAIL until seriesVar is wired into BuildWith.
func TestPeerSeriesDistinct(t *testing.T) {
	c := buildVariationConstruct(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	mc := tickAt(t, c, now)

	varied := []string{
		"alloy_resources_process_resident_memory_bytes",
		"go_goroutines",
		"go_memstats_heap_inuse_bytes",
		"scrape_duration_seconds",
	}

	for _, name := range varied {
		vals := valsByCollector(mc, name)
		if len(vals) < 2 {
			t.Errorf("%q: fewer than 2 collector series found (got %d) — cannot test distinctness", name, len(vals))
			continue
		}
		// Check that not all values are identical.
		seen := map[float64]bool{}
		for _, v := range vals {
			seen[v] = true
		}
		if len(seen) == 1 {
			t.Errorf("%q: all %d collector series have identical value %.6g — peer series must be distinct (seriesVar not wired)", name, len(vals), vals[firstKey(vals)])
		}
	}
}

// TestPeerCounterIncrementsDistinct verifies that per-tick counter increments differ between
// collectors. We take two ticks and compare the delta (tick2-tick1) per collector.
func TestPeerCounterIncrementsDistinct(t *testing.T) {
	c := buildVariationConstruct(t)
	t1 := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(60 * time.Second)

	mc1 := tickAt(t, c, t1)
	mc2 := tickAt(t, c, t2)

	counters := []string{
		"alloy_resources_process_cpu_seconds_total",
		"alloy_resources_machine_rx_bytes_total",
		"alloy_resources_machine_tx_bytes_total",
	}

	for _, name := range counters {
		v1 := valsByCollector(mc1, name)
		v2 := valsByCollector(mc2, name)

		deltas := map[string]float64{}
		for id, val1 := range v1 {
			if val2, ok := v2[id]; ok {
				deltas[id] = val2 - val1
			}
		}
		if len(deltas) < 2 {
			t.Errorf("%q: fewer than 2 collectors with consecutive ticks", name)
			continue
		}

		seen := map[string]bool{}
		for _, d := range deltas {
			seen[fmt.Sprintf("%.9g", d)] = true
		}
		if len(seen) == 1 {
			t.Errorf("%q: all collector deltas identical (%.6g) — counter increments must vary per series", name, firstDelta(deltas))
		}
	}
}

// TestSingleSeriesDriftsAcrossTicks verifies that one collector's value is NOT constant
// across 30 ticks at 13-minute steps (≥5 distinct values required).
func TestSingleSeriesDriftsAcrossTicks(t *testing.T) {
	c := buildVariationConstruct(t)
	base := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	varied := []string{
		"alloy_resources_process_resident_memory_bytes",
		"go_goroutines",
		"go_memstats_heap_inuse_bytes",
		"scrape_duration_seconds",
	}

	for _, name := range varied {
		seen := map[float64]bool{}
		var pickedID string

		for i := range 30 {
			now := base.Add(time.Duration(i) * 13 * time.Minute)
			mc := tickAt(t, c, now)
			vals := valsByCollector(mc, name)
			if len(vals) == 0 {
				t.Fatalf("%q: no series at tick %d", name, i)
			}
			if pickedID == "" {
				for id := range vals {
					pickedID = id
					break
				}
			}
			if v, ok := vals[pickedID]; ok {
				seen[v] = true
			}
		}

		if len(seen) < 5 {
			t.Errorf("%q: series for collector %q has only %d distinct values over 30 ticks — must drift (want ≥5)", name, pickedID, len(seen))
		}
	}
}

// TestVariedMetricsNonZero verifies that variation doesn't crater values to zero.
func TestVariedMetricsNonZero(t *testing.T) {
	c := buildVariationConstruct(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	mc := tickAt(t, c, now)

	checks := map[string]float64{
		"alloy_resources_process_resident_memory_bytes": 1e6, // at least 1 MB
		"go_goroutines":                10,   // at least 10 goroutines
		"go_memstats_heap_inuse_bytes": 1e6,  // at least 1 MB
		"scrape_duration_seconds":      1e-4, // at least 0.1ms
	}

	for name, minVal := range checks {
		for _, s := range mc.Find(name) {
			if s.Value < minVal {
				t.Errorf("%q: value %.6g < floor %.6g (variation craters to near-zero)", name, s.Value, minVal)
			}
		}
	}
}

// TestConstantMetricsUnchanged verifies that info/config/health metrics stay constant
// across two ticks (they must NOT vary with seriesVar).
func TestConstantMetricsUnchanged(t *testing.T) {
	c := buildVariationConstruct(t)
	t1 := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(13 * time.Minute)

	mc1 := tickAt(t, c, t1)
	mc2 := tickAt(t, c, t2)

	constants := []string{
		"alloy_build_info",  // info metric — always 1
		"up",                // health flag — always 0 or 1
		"alloy_config_hash", // config hash — always 1
	}

	for _, name := range constants {
		s1 := valsByCollector(mc1, name)
		s2 := valsByCollector(mc2, name)
		for id, v1 := range s1 {
			if v2, ok := s2[id]; ok {
				if v1 != v2 {
					t.Errorf("%q collector %q: value changed %.6g→%.6g — must be constant (info/health metric)", name, id, v1, v2)
				}
			}
		}
	}
}

// firstKey returns an arbitrary key from the map (for error messages).
func firstKey(m map[string]float64) string {
	for k := range m {
		return k
	}
	return ""
}

// firstDelta returns an arbitrary delta value from the map (for error messages).
func firstDelta(m map[string]float64) float64 {
	for _, v := range m {
		return v
	}
	return 0
}
