// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/semconv"
	"github.com/rknightion/synthkit/internal/workload/app"
)

// Exercise the public registration, minter and projection boundaries with freshly
// reconstructed equivalent workloads, as on a restart or handoff.
func TestAppRestartLiveTraceIdentity(t *testing.T) {
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	var previous []*ledger.Request
	for restart := range 2 {
		cfg := &app.Config{
			Traffic: app.Traffic{OffPeakRPS: 3, PeakRPS: 3},
			Services: []app.ServiceNode{{
				Name: "backend", Type: "web", Entry: true,
				AgenticFlow: &app.AgenticFlow{Workflow: "workflow", Agents: []app.AgentDecl{
					{Name: "planner", Tools: []string{"search", "retrieve"}},
					{Name: "checker", Tools: []string{"validate", "review"}},
				}},
			}},
		}
		w, err := app.Registration().Build(cfg, core.Binding{Name: "restart-app", Env: coretest.Env(), Cluster: coretest.Cluster()})
		if err != nil {
			t.Fatal(err)
		}
		traces := &coretest.TraceCapture{}
		world := coretest.World(&coretest.MetricCapture{}, &coretest.LogCapture{}, traces)
		batch := w.Minter().Mint(now, 1, world.Shape)
		if len(batch) != 3 {
			t.Fatalf("minted %d requests, want 3", len(batch))
		}
		if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
			t.Fatal(err)
		}
		for i, r := range batch {
			if restart > 0 {
				if r.TraceID == previous[i].TraceID {
					t.Fatalf("repeated live TraceID across equivalent minter reconstruction: %s", r.TraceID)
				}
				// Span IDs are trace-scoped; every other key is a cross-signal
				// or cross-system join and must not alias the previous request.
				current, old := reflect.ValueOf(r.Correlation), reflect.ValueOf(previous[i].Correlation)
				for field := range current.NumField() {
					if current.Type().Field(field).Name != "SpanID" && current.Field(field).String() == old.Field(field).String() {
						t.Errorf("repeated live join key %s", current.Type().Field(field).Name)
					}
				}
			}
			found := false
			for _, resource := range traces.Resources {
				for _, span := range resource.Spans {
					if span.TraceID == r.TraceID {
						found = true
						if span.Attrs[semconv.AttrCorrelationID] != r.CorrelationID {
							t.Error("projected span lost request correlation")
						}
					}
				}
			}
			if !found {
				t.Error("minted live TraceID absent from projected trace sink")
			}
		}
		previous = batch
	}
}
