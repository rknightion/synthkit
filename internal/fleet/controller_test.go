// SPDX-License-Identifier: AGPL-3.0-only

package fleet

import (
	"context"
	"sync"
	"testing"

	"github.com/rknightion/synthkit/internal/fleethook"
)

// TestControllerFiresEvents asserts the controller reports register + heartbeat + unregister
// outcomes through the fleethook seam (dry-run client → all ops succeed, DryRun stamped).
func TestControllerFiresEvents(t *testing.T) {
	var mu sync.Mutex
	var got []fleethook.Event
	obs := func(_ context.Context, ev fleethook.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}
	c := NewController(Config{DryRun: true, FMURL: "http://x", StackID: "s", Token: "t", Observe: obs})
	roster := []Collector{{ID: "c1"}, {ID: "c2"}}

	// reconcile on a fresh controller: registers (first-sight) then heartbeats each collector.
	// unregisterRegistered then unregisters every still-registered id.
	c.reconcile(context.Background(), roster)
	c.unregisterRegistered(context.Background())

	counts := map[string]int{}
	for _, ev := range got {
		if !ev.DryRun {
			t.Errorf("event %+v missing DryRun flag", ev)
		}
		if ev.Err != nil {
			t.Errorf("dry-run op unexpectedly failed: %+v", ev)
		}
		counts[ev.Op]++
	}
	// reconcile: 2 register + 2 heartbeat; unregisterRegistered: 2 unregister — same totals as before.
	if counts[fleethook.OpRegister] != 2 || counts[fleethook.OpHeartbeat] != 2 || counts[fleethook.OpUnregister] != 2 {
		t.Fatalf("op counts = %v, want 2 each", counts)
	}
}

// TestNilObserverIsSafe asserts the lifecycle is unchanged when no observer is set.
func TestNilObserverIsSafe(t *testing.T) {
	c := NewController(Config{DryRun: true, FMURL: "http://x", StackID: "s", Token: "t"})
	// reconcile registers+heartbeats; unregisterRegistered unwinds — must not panic with nil observer.
	c.reconcile(context.Background(), []Collector{{ID: "c1"}})
	c.unregisterRegistered(context.Background())
}

// TestControllerReusesClient asserts the FM client is built once in NewController and reused
// across heartbeats rather than re-allocated per tick (L4 — connection-pool churn fix).
func TestControllerReusesClient(t *testing.T) {
	c := NewController(Config{DryRun: true, FMURL: "http://x", StackID: "s", HeartbeatInterval: 1})
	if c.client == nil {
		t.Fatal("client not constructed in NewController")
	}
	before := c.client

	// reconcile with an empty roster is a no-op and must NOT rebuild the client.
	c.reconcile(context.Background(), nil)
	if c.client != before {
		t.Errorf("client pointer changed across reconcile — not reused (got a fresh allocation)")
	}

	// unregisterRegistered (the shutdown path) also reuses the same client.
	c.unregisterRegistered(context.Background())
	if c.client != before {
		t.Errorf("client pointer changed across unregisterRegistered — not reused")
	}
}
