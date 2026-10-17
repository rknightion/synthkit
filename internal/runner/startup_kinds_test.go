// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fleet"
)

func stateDisabling(kinds ...string) control.State {
	s := control.DefaultState()
	s.DisabledKinds = kinds
	return s
}

func alloySeries(mc *coretest.MetricCapture) []string {
	var out []string
	for _, name := range mc.Names() {
		if strings.HasPrefix(name, "alloy_") {
			out = append(out, name)
		}
	}
	return out
}

// TestStartupDisabledKindFirstTickPushesNoSeries: a state seeded before the first tick (no POST
// /control/kinds) suppresses every series of the kind, including its alloy_* families.
func TestStartupDisabledKindFirstTickPushesNoSeries(t *testing.T) {
	run := func(state *control.State) []string {
		mc := &coretest.MetricCapture{}
		r := New(Sinks{Metrics: mc}, fleetRegistry(t), Options{})
		res, _ := resolvedWithFleet("startupkinds", map[string]int{"linux": 2}, false)
		if err := r.AddBlueprint(res); err != nil {
			t.Fatal(err)
		}
		if state != nil {
			r.ApplyControl(*state)
		}
		if err := r.RunOnce(context.Background(), time.Unix(1_700_000_000, 0)); err != nil {
			t.Fatal(err)
		}
		return alloySeries(mc)
	}
	if got := run(nil); len(got) == 0 {
		t.Fatal("control run emitted no alloy_* series, so the absence below proves nothing")
	}
	seeded := stateDisabling("synthetic_monitoring", "fleet_management")
	if got := run(&seeded); len(got) != 0 {
		t.Fatalf("first tick pushed alloy_* series with fleet_management disabled at startup: %v", got)
	}
}

// TestStartupDisabledFleetKindMakesNoFMRequests: the Fleet Management controller registers and
// heartbeats only collectors whose kind is enabled, from the very first reconcile.
func TestStartupDisabledFleetKindMakesNoFMRequests(t *testing.T) {
	var (
		mu    sync.Mutex
		paths []string
		total atomic.Int32
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		total.Add(1)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	r := New(Sinks{}, fleetRegistry(t), Options{Fleet: fleet.Config{FMURL: server.URL, Token: "test", HeartbeatInterval: 1}})
	res, _ := resolvedWithFleet("startupfm", map[string]int{"linux": 2}, false)
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	r.ApplyControl(stateDisabling("fleet_management"))

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	r.startFleetControllers(ctx, &wg)
	defer func() { cancel(); wg.Wait() }()

	// The first reconcile runs immediately and a second one a heartbeat later.
	time.Sleep(1600 * time.Millisecond)
	if got := total.Load(); got != 0 {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("FM endpoint received %d requests with fleet_management disabled at startup: %v", got, paths)
	}

	// Re-enabling through the ordinary control path starts registration on the next heartbeat.
	r.ApplyControl(stateDisabling())
	deadline := time.After(4 * time.Second)
	for total.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("enabling the kind never resumed FM registration")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// TestRuntimeDisabledFleetKindSuspendsWithoutTeardown: disabling fleet_management while collectors
// are registered pauses the controller (no registration, heartbeat or unregistration of any kind)
// and re-enabling resumes heartbeats. Teardown stays the explicit UnregisterFleet path.
func TestRuntimeDisabledFleetKindSuspendsWithoutTeardown(t *testing.T) {
	var (
		mu     sync.Mutex
		counts = map[string]int{}
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]]++
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	snapshot := func() (map[string]int, int) {
		mu.Lock()
		defer mu.Unlock()
		out, total := map[string]int{}, 0
		for k, v := range counts {
			out[k] = v
			total += v
		}
		return out, total
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for !cond() {
			select {
			case <-deadline:
				t.Fatalf("timed out waiting for %s: %v", what, counts)
			case <-time.After(25 * time.Millisecond):
			}
		}
	}

	r := New(Sinks{}, fleetRegistry(t), Options{Fleet: fleet.Config{FMURL: server.URL, Token: "test", HeartbeatInterval: 1}})
	res, _ := resolvedWithFleet("suspendfm", map[string]int{"linux": 2}, false)
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	r.ApplyControl(stateDisabling())
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	r.startFleetControllers(ctx, &wg)
	defer func() { cancel(); wg.Wait() }()

	waitFor("registration and a heartbeat", func() bool {
		c, _ := snapshot()
		return c["RegisterCollector"] >= 2 && c["GetConfig"] >= 2
	})

	r.ApplyControl(stateDisabling("fleet_management"))
	time.Sleep(200 * time.Millisecond) // let an in-flight reconcile finish
	before, beforeTotal := snapshot()
	time.Sleep(3200 * time.Millisecond) // three heartbeat intervals while disabled
	after, afterTotal := snapshot()
	if afterTotal != beforeTotal {
		t.Fatalf("FM requests while fleet_management is disabled: before=%v after=%v", before, after)
	}
	if after["UnregisterCollector"] != 0 {
		t.Fatalf("disabling the kind unregistered collectors: %v", after)
	}

	r.ApplyControl(stateDisabling())
	waitFor("heartbeats to resume", func() bool {
		c, _ := snapshot()
		return c["GetConfig"] > after["GetConfig"]
	})
	if c, _ := snapshot(); c["UnregisterCollector"] != 0 || c["RegisterCollector"] != 2 {
		t.Fatalf("resume re-registered or unregistered: %v", c)
	}
}
