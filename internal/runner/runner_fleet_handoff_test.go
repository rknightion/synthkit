// SPDX-License-Identifier: AGPL-3.0-only
package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/fleet"
	"github.com/rknightion/synthkit/internal/fleethook"
)

func TestFleetAggregateControllerOwnsSelectedRoster(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "siblings", true: "empty"}[empty], func(t *testing.T) {
			events := make(chan fleethook.Event, 32)
			r := New(Sinks{}, fleetRegistry(t), Options{Fleet: fleet.Config{FMURL: "https://fm.example", DryRun: true, Observe: func(_ context.Context, event fleethook.Event) { events <- event }}})
			var want []string
			if !empty {
				for _, name := range []string{"first", "second"} {
					res, fx := resolvedWithFleet(name, map[string]int{"linux": 1}, false)
					if err := r.AddBlueprint(res); err != nil {
						t.Fatal(err)
					}
					for _, col := range fleetmgmt.Roster(fx.Seed, "", map[string]int{"linux": 1}) {
						want = append(want, col.ID)
					}
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			var wg sync.WaitGroup
			r.startFleetControllers(ctx, &wg)
			var got []string
			for len(got) < len(want) {
				select {
				case event := <-events:
					if event.Op == fleethook.OpRegister {
						got = append(got, event.Collector)
					}
				case <-time.After(2 * time.Second):
					cancel()
					wg.Wait()
					t.Fatal("aggregate controller failed to register selected siblings")
				}
			}
			cancel()
			wg.Wait()
			if len(r.fleetControllers) != 1 {
				t.Fatalf("controllers=%d, want one aggregate owner even for empty selection", len(r.fleetControllers))
			}
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("registered=%v want=%v", got, want)
			}
			close(events)
			for event := range events {
				if event.Op == fleethook.OpUnregister {
					t.Fatal("ordinary cancellation unregistered a collector")
				}
			}
		})
	}
}

func TestUnregisterFleetIncludesStandaloneAndScaledMirror(t *testing.T) {
	var got []string
	var otherCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.HasSuffix(r.URL.Path, "/UnregisterCollector") {
			got = append(got, body.ID)
		} else {
			otherCalls++
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	r := New(Sinks{}, fleetRegistry(t), Options{Fleet: fleet.Config{FMURL: server.URL, Token: "test"}})
	cl := mirrorCluster("selected-cluster")
	res := &blueprint.Resolved{Name: "selected", Label: "selected", Constructs: []blueprint.ConstructInstance{
		{Kind: fleetmgmt.Kind, Name: "standalone", Config: &fleetmgmt.Config{CollectorsPerOS: map[string]int{"linux": 2}}, Fixtures: &fixture.Set{Seed: "selected"}},
		{Kind: fleetmgmt.Kind, Name: "mirror", Config: &fleetmgmt.Config{}, Fixtures: &fixture.Set{Seed: "selected:" + cl.Name, Cluster: cl}},
	}}
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	r.bps[0].scale.Set(map[string]int{"api": 200})
	nodes := fixture.DeriveNodes(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, 200)
	want := append(fleetmgmt.Roster("selected", "", map[string]int{"linux": 2}), fleetmgmt.K8sRoster(cl.Seed, "", cl.Name, "", nodes, cl.K8sMonitoring)...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.UnregisterFleet(ctx); err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	wantIDs := idsOf(want)
	sort.Strings(wantIDs)
	if !reflect.DeepEqual(got, wantIDs) || otherCalls != 0 {
		t.Fatalf("deleted=%v want=%v non-teardown=%d", got, idsOf(want), otherCalls)
	}
}
