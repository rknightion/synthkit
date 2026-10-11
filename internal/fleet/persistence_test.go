// SPDX-License-Identifier: AGPL-3.0-only
package fleet_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	. "github.com/rknightion/synthkit/internal/fleet"
	"github.com/rknightion/synthkit/internal/ha"
)

type memoryRegistered struct {
	ids  map[string]bool
	gate ha.LeaderGate
	fail bool
}

func (s *memoryRegistered) Load(ctx context.Context) ([]string, error) {
	if s.fail {
		return nil, errors.New("store unavailable")
	}
	var out []string
	for id := range s.ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, ctx.Err()
}
func (s *memoryRegistered) Set(ctx context.Context, id string, present bool) error {
	return s.gate.Do(ctx, ha.Mutation, func(context.Context) error {
		if s.fail {
			return errors.New("store unavailable")
		}
		if present {
			s.ids[id] = true
		} else {
			delete(s.ids, id)
		}
		return nil
	})
}
func TestPersistedHandoffRestartAndEmptyRoster(t *testing.T) {
	ctx := context.Background()
	gate := ha.NewGate()
	_ = gate.Activate(ctx, func(context.Context) error { return nil })
	store := &memoryRegistered{ids: map[string]bool{}, gate: gate}
	var registered, deleted []string
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/RegisterCollector"):
			registered = append(registered, body.ID)
			if !store.ids[body.ID] {
				t.Error("registration escaped durable intent")
			}
		case strings.HasSuffix(r.URL.Path, "/UnregisterCollector"):
			deleted = append(deleted, body.ID)
		}
		_, _ = w.Write([]byte(`{}`))
	})
	cfg := Config{FMURL: srv.URL, Token: "test", Store: store}
	first := NewController(cfg)
	if err := first.Reconcile(ctx, []Collector{{ID: "retired"}, {ID: "kept"}}); err != nil {
		t.Fatal(err)
	}
	if len(store.ids) != 2 {
		t.Fatalf("persisted IDs = %v, want both registrations", store.ids)
	}
	gate.Revoke()
	nextGate := ha.NewGate()
	_ = nextGate.Activate(ctx, func(context.Context) error { return nil })
	store.gate = nextGate
	next := NewController(cfg)
	if err := next.Reconcile(ctx, []Collector{{ID: "kept"}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(deleted, []string{"retired"}) {
		t.Fatalf("handoff deleted %v, want retired only", deleted)
	}
	if !store.ids["kept"] || store.ids["retired"] {
		t.Fatalf("handoff ledger %v", store.ids)
	}
	restart := NewController(cfg)
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	restart.StartDynamic(cancelCtx, func() []Collector { return []Collector{{ID: "kept"}} })
	if len(deleted) != 1 || !store.ids["kept"] {
		t.Fatal("restart cancellation tore down retained collector")
	}
	if err := restart.Reconcile(ctx, nil); err != nil {
		t.Fatal(err)
	}
	sort.Strings(deleted)
	if !reflect.DeepEqual(deleted, []string{"kept", "retired"}) || len(store.ids) != 0 {
		t.Fatalf("empty selected roster failed cleanup: %v %v", deleted, store.ids)
	}
}
func TestExplicitUnregisterExactSelectedSet(t *testing.T) {
	var deleted []string
	var registered int
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.HasSuffix(r.URL.Path, "/UnregisterCollector") {
			deleted = append(deleted, body.ID)
		}
		if strings.HasSuffix(r.URL.Path, "/RegisterCollector") {
			registered++
		}
		_, _ = w.Write([]byte(`{}`))
	})
	store := &memoryRegistered{ids: map[string]bool{"unselected": true, "selected-a": true}, gate: ha.AlwaysLeader{}}
	c := NewController(Config{FMURL: srv.URL, Token: "test", Store: store})
	if err := c.Unregister(context.Background(), []Collector{{ID: "selected-a"}, {ID: "selected-b"}, {ID: "selected-a"}}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(deleted)
	if !reflect.DeepEqual(deleted, []string{"selected-a", "selected-b"}) || registered != 0 {
		t.Fatalf("deleted=%v registered=%d", deleted, registered)
	}
	if len(store.ids) != 1 || !store.ids["unselected"] {
		t.Fatalf("unselected state changed: %v", store.ids)
	}
}
func TestPersistenceFailureAndRevocationFailClosed(t *testing.T) {
	store := &memoryRegistered{ids: map[string]bool{}, gate: ha.NewGate()}
	calls := 0
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(`{}`)) })
	c := NewController(Config{FMURL: srv.URL, Token: "test", Store: store})
	if err := c.Reconcile(context.Background(), []Collector{{ID: "blocked"}}); err == nil {
		t.Fatal("revoked store accepted registration")
	}
	if calls != 0 {
		t.Fatal("HTTP before gated state write")
	}
	store.fail = true
	if err := c.Unregister(context.Background(), []Collector{{ID: "blocked"}}); err == nil {
		t.Fatal("store load failure ignored")
	}
	if calls != 0 {
		t.Fatal("HTTP despite failed store")
	}
}
