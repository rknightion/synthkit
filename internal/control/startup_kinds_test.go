// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/ha"
)

func startupKindsSchema() Schema {
	return Schema{Kinds: []string{"rds", "ec2", "alpha", "beta"}}
}

func TestValidateKindsMatchesPostControlKinds(t *testing.T) {
	if err := ValidateKinds([]string{"rds", "alpha"}, startupKindsSchema()); err != nil {
		t.Fatalf("known kinds rejected: %v", err)
	}
	err := ValidateKinds([]string{"rds", "ghost"}, startupKindsSchema())
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("unknown kind error = %v, want one naming ghost", err)
	}
}

func TestSeedDisabledKindsEmptyListDoesNotWriteOrChangeState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-state.json")
	st := NewStore(path)
	out, err := st.SeedDisabledKinds(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.DisabledKinds) != 0 {
		t.Fatalf("empty seed changed state: %v", out.DisabledKinds)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("empty seed must not write the snapshot (stat err=%v)", err)
	}
}

func TestSeedDisabledKindsUnionsWithPersistedAndKeepsOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-state.json")
	first := NewStore(path)
	first.Update(func(s *State) { s.DisabledKinds = []string{"rds", "ec2"} })

	restarted := NewStore(path)
	out, err := restarted.SeedDisabledKinds(context.Background(), []string{"alpha", "ec2", "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"rds", "ec2", "alpha"}
	if !reflect.DeepEqual(out.DisabledKinds, want) || !reflect.DeepEqual(restarted.Snapshot().DisabledKinds, want) {
		t.Fatalf("effective disabled_kinds = %v / %v, want %v", out.DisabledKinds, restarted.Snapshot().DisabledKinds, want)
	}
}

// TestStartupKindsRestartPrecedence is the AC4 contract end to end through the HTTP handler:
// persisted list UNION startup list at boot, POST /control/kinds replaces and persists, and the
// next boot re-adds the startup kinds on top of whatever was persisted.
func TestStartupKindsRestartPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-state.json")
	startup := []string{"beta"}
	get := func(h http.Handler) []string {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/control/state", nil))
		if rec.Code != 200 {
			t.Fatalf("GET /control/state = %d %s", rec.Code, rec.Body)
		}
		var s State
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		return s.DisabledKinds
	}
	boot := func() (*Store, http.Handler, *State) {
		st := NewStore(path)
		var applied State
		if _, err := st.SeedDisabledKinds(context.Background(), startup); err != nil {
			t.Fatal(err)
		}
		applied = st.Snapshot()
		return st, NewHandler(st, func(s State) { applied = s }, "", fakeSchemaSource{schema: startupKindsSchema()}), &applied
	}

	// Boot 1: nothing persisted, startup list alone is effective before any POST.
	_, h, applied := boot()
	if got := get(h); !reflect.DeepEqual(got, []string{"beta"}) {
		t.Fatalf("boot 1 effective = %v, want [beta]", got)
	}
	if !reflect.DeepEqual(applied.DisabledKinds, []string{"beta"}) {
		t.Fatalf("boot 1 applied = %v", applied.DisabledKinds)
	}

	// A later POST replaces the whole list and persists it, even dropping the startup kind.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/kinds", strings.NewReader(`{"disabled_kinds":["rds"]}`)))
	if rec.Code != 200 {
		t.Fatalf("POST /control/kinds = %d %s", rec.Code, rec.Body)
	}
	if got := get(h); !reflect.DeepEqual(got, []string{"rds"}) {
		t.Fatalf("after POST effective = %v, want [rds]", got)
	}

	// Boot 2: persisted [rds] UNION startup [beta].
	_, h2, applied2 := boot()
	want := []string{"rds", "beta"}
	if got := get(h2); !reflect.DeepEqual(got, want) {
		t.Fatalf("boot 2 effective = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(applied2.DisabledKinds, want) {
		t.Fatalf("boot 2 applied = %v, want %v", applied2.DisabledKinds, want)
	}

	// POST still works after a seeded boot, and an empty replacement clears until the next boot.
	rec = httptest.NewRecorder()
	h2.ServeHTTP(rec, httptest.NewRequest("POST", "/control/kinds", strings.NewReader(`{"disabled_kinds":[]}`)))
	if rec.Code != 200 {
		t.Fatalf("POST empty = %d %s", rec.Code, rec.Body)
	}
	if got := get(h2); len(got) != 0 {
		t.Fatalf("after empty POST effective = %v, want none", got)
	}
	_, h3, _ := boot()
	if got := get(h3); !reflect.DeepEqual(got, []string{"beta"}) {
		t.Fatalf("boot 3 effective = %v, want [beta]", got)
	}
}

// TestSeedDisabledKindsOnBackendStoreSurvivesUnrelatedUpdates covers the compare-and-swap path used
// by the Kubernetes state backend: the seed is a committed document, so a later unrelated mutation
// (which reloads the document) keeps it, and POST /control/kinds still replaces it.
func TestSeedDisabledKindsOnBackendStoreSurvivesUnrelatedUpdates(t *testing.T) {
	root := t.TempDir()
	open := func() *Store {
		t.Helper()
		backend, err := NewFileBackend(FileBackendOptions{Gate: ha.AlwaysLeader{}, Paths: map[Key]string{Control: filepath.Join(root, "control.json")}})
		if err != nil {
			t.Fatal(err)
		}
		st, err := NewBackendStore(context.Background(), backend, ha.AlwaysLeader{}, 3)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	first := open()
	if _, err := first.SeedDisabledKinds(context.Background(), []string{"beta"}); err != nil {
		t.Fatal(err)
	}
	first.Update(func(s *State) { s.VolumeMultiplier = 2 })
	if got := first.Snapshot().DisabledKinds; !reflect.DeepEqual(got, []string{"beta"}) {
		t.Fatalf("unrelated update dropped the seed: %v", got)
	}

	restarted := open()
	if _, err := restarted.SeedDisabledKinds(context.Background(), []string{"alpha", "beta"}); err != nil {
		t.Fatal(err)
	}
	if got := restarted.Snapshot().DisabledKinds; !reflect.DeepEqual(got, []string{"beta", "alpha"}) {
		t.Fatalf("restart effective = %v, want [beta alpha]", got)
	}
	h := NewHandler(restarted, nil, "", fakeSchemaSource{schema: startupKindsSchema()})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/kinds", strings.NewReader(`{"disabled_kinds":["rds"]}`)))
	if rec.Code != 200 || !reflect.DeepEqual(restarted.Snapshot().DisabledKinds, []string{"rds"}) {
		t.Fatalf("POST after seed = %d %s / %v", rec.Code, rec.Body, restarted.Snapshot().DisabledKinds)
	}
}
