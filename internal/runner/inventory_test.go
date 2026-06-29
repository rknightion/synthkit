// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"testing"

	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// a fake metric sink that captures what it received (to assert no construct label leaks).
type capSink struct{ got []promrw.Series }

func (c *capSink) Write(_ context.Context, b []promrw.Series) error {
	c.got = append(c.got, b...)
	return nil
}

func TestWriterInventoryRecordsAndDoesNotLeakLabels(t *testing.T) {
	cap := &capSink{}
	inv := newConstructInv("bpA", "cloudflare", "cf1")
	w := &stampedMetrics{sink: cap, label: "bpA", bp: "bpA", budget: newSeriesBudget(0), inv: inv}
	err := w.Write(context.Background(), []promrw.Series{
		{Name: "http_requests_total", Labels: map[string]string{"method": "GET", "code": "200"}},
		{Name: "http_requests_total", Labels: map[string]string{"method": "POST", "code": "200"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snap := inv.snapshot()
	if snap.DistinctSeries != 2 {
		t.Fatalf("distinct series = %d, want 2", snap.DistinctSeries)
	}
	if len(snap.MetricNames) != 1 || snap.MetricNames[0] != "http_requests_total" {
		t.Fatalf("metric names wrong: %v", snap.MetricNames)
	}
	// wire-safety: emitted series carry blueprint (stamped) but NEVER a construct/kind/name label.
	for _, s := range cap.got {
		for _, banned := range []string{"construct", "construct_kind", "construct_instance", "kind", "name"} {
			if _, ok := s.Labels[banned]; ok {
				t.Fatalf("inventory leaked %q onto the wire: %v", banned, s.Labels)
			}
		}
		if s.Labels["blueprint"] != "bpA" {
			t.Fatalf("expected blueprint label stamped, got %v", s.Labels)
		}
	}
}

// identical signatures must not double-count.
func TestDistinctSeriesDedup(t *testing.T) {
	inv := newConstructInv("b", "k", "n")
	inv.recordMetric("m", [][2]string{{"a", "v1"}, {"b", "v2"}})
	inv.recordMetric("m", [][2]string{{"a", "v1"}, {"b", "v2"}})
	if got := inv.snapshot().DistinctSeries; got != 1 {
		t.Fatalf("want 1 distinct, got %d", got)
	}
}

// A DISABLED blueprint emits nothing, so its Overview stats must read zero for the emission-derived
// counters (series, metric names) — but its STRUCTURAL constructs are still listed (the card keeps
// showing what the blueprint contains). Guards the "disabled stats never drop to zero" bug.
func TestInventoryDisabledBlueprintZeroesEmissionKeepsConstructs(t *testing.T) {
	mk := func(bp string) *bpRuntime {
		ci := newConstructInv(bp, "cloudflare", bp+"-cf")
		ci.recordMetric("http_requests_total", [][2]string{{"code", "200"}})
		ci.recordMetric("http_requests_total", [][2]string{{"code", "500"}})
		return &bpRuntime{name: bp, constructs: []*boundConstruct{{name: bp + "-cf", kind: "cloudflare", inv: ci}}}
	}
	r := &Runner{bps: []*bpRuntime{mk("on"), mk("off")}}
	r.ctl.Store(&control.State{DisabledBlueprints: []string{"off"}})

	rep := r.Inventory()
	byBp := map[string]control.BlueprintInventory{}
	for _, bi := range rep.Blueprints {
		byBp[bi.Blueprint] = bi
	}

	on := byBp["on"]
	if on.DistinctSeries != 2 || on.MetricNames != 1 {
		t.Fatalf("enabled blueprint should report live stats: series=%d names=%d", on.DistinctSeries, on.MetricNames)
	}

	off := byBp["off"]
	if off.DistinctSeries != 0 || off.MetricNames != 0 || off.LabelKeys != 0 {
		t.Fatalf("disabled blueprint emission stats must be zero: series=%d names=%d labels=%d", off.DistinctSeries, off.MetricNames, off.LabelKeys)
	}
	if len(off.Constructs) != 1 {
		t.Fatalf("disabled blueprint must still list its constructs: got %d", len(off.Constructs))
	}
	if off.Constructs[0].DistinctSeries != 0 || len(off.Constructs[0].MetricNames) != 0 {
		t.Fatalf("disabled construct emission stats must be zero: %+v", off.Constructs[0])
	}

	// Totals: emission excludes the disabled blueprint; constructs are structural and still counted.
	if rep.Totals.DistinctSeries != 2 {
		t.Fatalf("totals series should exclude disabled: got %d", rep.Totals.DistinctSeries)
	}
	if rep.Totals.Constructs != 2 {
		t.Fatalf("totals constructs are structural and should count both: got %d", rep.Totals.Constructs)
	}
}

// Same metric name + same label KEYS but DIFFERENT values must count as distinct series — this is
// why the signature hashes values, not just keys. (Guards the keys-only undercounting bug.)
func TestDistinctSeriesValuesMatter(t *testing.T) {
	inv := newConstructInv("b", "k", "n")
	inv.recordMetric("m", [][2]string{{"method", "GET"}, {"code", "200"}})
	inv.recordMetric("m", [][2]string{{"method", "POST"}, {"code", "200"}})
	snap := inv.snapshot()
	if snap.DistinctSeries != 2 {
		t.Fatalf("want 2 distinct (same keys, different values), got %d", snap.DistinctSeries)
	}
	// ...but the label-KEY inventory dedups to the 2 keys (keys, not values, are exposed).
	if len(snap.MetricLabels) != 2 {
		t.Fatalf("want 2 distinct label keys, got %d: %v", len(snap.MetricLabels), snap.MetricLabels)
	}
}
