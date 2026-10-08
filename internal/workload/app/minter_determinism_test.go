// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// Exercise Mint -> ProjectBatch -> the real dry-run trace sink's inventory,
// rather than supplying a hand-made SpanID to emitAgentFlow.
func TestAppMinterAgentFlowInventoryRepeatable(t *testing.T) {
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	type inventory struct{ Resources, Names, Attrs map[string][]string }
	render := func(now time.Time) ([]inventory, []ledger.Correlation) {
		cfg := agentFlowCfg(false)
		cfg.Traffic = Traffic{OffPeakRPS: 1, PeakRPS: 1}
		cfg.Services[1].AgenticFlow.Agents = []AgentDecl{
			{Name: "planner", Tools: []string{"search", "retrieve"}},
			{Name: "checker", Tools: []string{"validate", "review"}},
			{Name: "classifier", Tools: []string{"classify", "triage"}},
		}
		w := buildApp(t, cfg)
		world := coretest.World(&coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{})
		var inventories []inventory
		var correlations []ledger.Correlation
		// Compare each tick separately: a union over many requests could hide drift.
		for range 24 {
			batch := w.m.Mint(now, 1, world.Shape)
			if len(batch) != 1 {
				t.Fatalf("minted %d requests, want 1", len(batch))
			}
			sink := otlp.New("", "", "", true)
			world.Traces = sink
			if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
				t.Fatal(err)
			}
			resources, names, attrs := sink.Inventory()
			if len(names["backend"]) == 0 {
				t.Fatal("no backend trace inventory")
			}
			inventories = append(inventories, inventory{resources, names, attrs})
			correlations = append(correlations, batch[0].Correlation)
		}
		return inventories, correlations
	}
	first, firstIDs := render(now)
	repeat, repeatIDs := render(now)
	if !reflect.DeepEqual(first, repeat) {
		t.Error("fixed-tick minter-to-trace inventory drift")
	}
	later, laterIDs := render(now.Add(time.Hour))
	if !reflect.DeepEqual(first, later) {
		t.Error("first-tick inventory depends on wall clock")
	}
	for i := range firstIDs {
		// Root span keys are trace-local deterministic choices, not live joins.
		if firstIDs[i].SpanID != repeatIDs[i].SpanID || firstIDs[i].SpanID != laterIDs[i].SpanID {
			t.Error("fixed-tick trace-local selection key drift")
		}
		if firstIDs[i].TraceID == repeatIDs[i].TraceID || firstIDs[i].TraceID == laterIDs[i].TraceID {
			t.Error("live trace identity repeated across reconstructed minters")
		}
	}
}

func TestAppMinterCorrelationIdentityUnits(t *testing.T) {
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	w := buildApp(t, agentFlowCfg(false))
	w.m.traffic = Traffic{OffPeakRPS: 3, PeakRPS: 3}
	world := coretest.World(&coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{})
	seen := map[string]bool{}
	check := func(r *ledger.Request) {
		t.Helper()
		// Every correlation field must be fresh across requests, ticks and bindings.
		v := reflect.ValueOf(r.Correlation)
		for i := range v.NumField() {
			key := v.Type().Field(i).Name + ":" + v.Field(i).String()
			if seen[key] {
				t.Fatalf("reused correlation field %s", key)
			}
			seen[key] = true
		}
	}
	for range 3 {
		for _, r := range w.m.Mint(now, 1, world.Shape) {
			check(r)
		}
	}
	// Preserve direct mintOne callers' fresh request identities too.
	check(w.m.mintOne(now, world.Shape))
	check(w.m.mintOne(now, world.Shape))
	for _, field := range []string{"workload", "env", "cluster"} {
		other := buildApp(t, agentFlowCfg(false))
		other.m.traffic = Traffic{OffPeakRPS: 3, PeakRPS: 3}
		switch field {
		case "workload":
			other.m.workloadName += "-other"
		case "env":
			other.m.env += "-other"
		case "cluster":
			other.m.cluster += "-other"
		}
		for _, r := range other.m.Mint(now, 1, world.Shape) {
			check(r)
		}
	}
}
