// SPDX-License-Identifier: AGPL-3.0-only

package fleet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rknightion/synthkit/internal/fleethook"
	"github.com/rknightion/synthkit/internal/operationalerr"
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
		if ev.ErrorCode != operationalerr.CodeNone {
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

func TestControllerForwardsBoundedConfigReceiptSeparatelyFromHeartbeat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/GetConfig") {
			_, _ = w.Write([]byte(`{"content":"logging { level = \"info\" }"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	var got Receipt
	c := NewController(Config{
		FMURL: srv.URL, StackID: "s", Token: "t",
		ObserveReceipt: func(_ context.Context, collector string, receipt Receipt) {
			if collector != "c1" {
				t.Errorf("collector = %q, want c1", collector)
			}
			got = receipt
		},
	})
	c.reconcile(context.Background(), []Collector{{ID: "c1", OS: "linux"}})
	if got.State != ReceiptReceived || len(got.Digest) != 64 {
		t.Fatalf("receipt = %+v, want a bounded received digest", got)
	}
}

// TestControllerPausedReconcileIsSilentAndKeepsRegistrations: while Paused reports true a reconcile
// fires no event (register, heartbeat or unregister), including for an empty want; resuming
// heartbeats the already-registered collectors without registering them again.
func TestControllerPausedReconcileIsSilentAndKeepsRegistrations(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	obs := func(_ context.Context, ev fleethook.Event) {
		mu.Lock()
		counts[ev.Op]++
		mu.Unlock()
	}
	paused := false
	c := NewController(Config{DryRun: true, FMURL: "http://x", StackID: "s", Token: "t", Observe: obs, Paused: func() bool { return paused }})
	roster := []Collector{{ID: "c1"}, {ID: "c2"}}
	if err := c.reconcile(context.Background(), roster); err != nil {
		t.Fatal(err)
	}
	registered, heartbeats := counts[fleethook.OpRegister], counts[fleethook.OpHeartbeat]
	if registered != 2 || heartbeats != 2 {
		t.Fatalf("setup counts = %v", counts)
	}
	paused = true
	for _, want := range [][]Collector{roster, nil} {
		if err := c.reconcile(context.Background(), want); err != nil {
			t.Fatal(err)
		}
	}
	if counts[fleethook.OpRegister] != registered || counts[fleethook.OpHeartbeat] != heartbeats || counts[fleethook.OpUnregister] != 0 {
		t.Fatalf("paused reconcile made requests: %v", counts)
	}
	paused = false
	if err := c.reconcile(context.Background(), roster); err != nil {
		t.Fatal(err)
	}
	if counts[fleethook.OpRegister] != registered || counts[fleethook.OpHeartbeat] != heartbeats+2 {
		t.Fatalf("resume counts = %v", counts)
	}
}

// TestControllerPauseMidReconcileStopsRemainingRequests: a pause that lands while a reconcile is
// already running (the kind disabled by a control POST) stops the requests for later collectors.
func TestControllerPauseMidReconcileStopsRemainingRequests(t *testing.T) {
	var mu sync.Mutex
	registers := 0
	paused := false
	obs := func(_ context.Context, ev fleethook.Event) {
		if ev.Op == fleethook.OpRegister {
			mu.Lock()
			registers++
			paused = true
			mu.Unlock()
		}
	}
	c := NewController(Config{DryRun: true, FMURL: "http://x", StackID: "s", Token: "t", Observe: obs, Paused: func() bool { mu.Lock(); defer mu.Unlock(); return paused }})
	if err := c.reconcile(context.Background(), []Collector{{ID: "c1"}, {ID: "c2"}, {ID: "c3"}}); err != nil {
		t.Fatal(err)
	}
	if registers != 1 {
		t.Fatalf("registered %d collectors after the pause landed, want 1", registers)
	}
}
