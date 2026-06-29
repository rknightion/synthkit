// SPDX-License-Identifier: AGPL-3.0-only

package bpsource_test

import (
	"path/filepath"
	"testing"

	"github.com/rknightion/synthkit/internal/bpsource"
	"github.com/rknightion/synthkit/internal/control"
)

func TestStoreSourceConfigSources_EmptyByDefault(t *testing.T) {
	store := control.NewStore(filepath.Join(t.TempDir(), "state.json"))
	cfg := bpsource.NewStoreSourceConfig(store)
	srcs := cfg.Sources()
	if srcs == nil {
		t.Fatal("Sources() must return non-nil empty slice by default")
	}
	if len(srcs) != 0 {
		t.Fatalf("Sources() must be empty by default, got %d", len(srcs))
	}
}

func TestStoreSourceConfigUpsertSource_AddAndUpdate(t *testing.T) {
	store := control.NewStore(filepath.Join(t.TempDir(), "state.json"))
	cfg := bpsource.NewStoreSourceConfig(store)

	s1 := bpsource.Source{
		ID:          "repo-a",
		Name:        "Repo A",
		Namespace:   "ns-a",
		URL:         "https://github.com/example/repo-a",
		Ref:         "refs/heads/main",
		Subpath:     "",
		TokenEnvVar: "GH_TOKEN_A",
		LastSHA:     "",
		LastFetchMs: 0,
		LastErr:     "",
	}
	if err := cfg.UpsertSource(s1); err != nil {
		t.Fatalf("UpsertSource (add): %v", err)
	}

	srcs := cfg.Sources()
	if len(srcs) != 1 {
		t.Fatalf("expected 1 source after add, got %d", len(srcs))
	}
	if srcs[0] != s1 {
		t.Fatalf("source mismatch:\n  got  %+v\n  want %+v", srcs[0], s1)
	}

	// Update the same ID.
	s1Updated := s1
	s1Updated.Name = "Repo A (updated)"
	s1Updated.LastSHA = "deadbeef"
	s1Updated.LastFetchMs = 1718700000001
	if err := cfg.UpsertSource(s1Updated); err != nil {
		t.Fatalf("UpsertSource (update): %v", err)
	}

	srcs = cfg.Sources()
	if len(srcs) != 1 {
		t.Fatalf("expected still 1 source after update, got %d", len(srcs))
	}
	if srcs[0] != s1Updated {
		t.Fatalf("update mismatch:\n  got  %+v\n  want %+v", srcs[0], s1Updated)
	}
}

func TestStoreSourceConfigUpsertSource_MultipleDistinctIDs(t *testing.T) {
	store := control.NewStore(filepath.Join(t.TempDir(), "state.json"))
	cfg := bpsource.NewStoreSourceConfig(store)

	for i, id := range []string{"repo-a", "repo-b", "repo-c"} {
		s := bpsource.Source{
			ID:   id,
			Name: "Source " + id,
			URL:  "https://example.com/" + id,
			Ref:  "refs/heads/main",
		}
		_ = i
		if err := cfg.UpsertSource(s); err != nil {
			t.Fatalf("UpsertSource %s: %v", id, err)
		}
	}
	if got := cfg.Sources(); len(got) != 3 {
		t.Fatalf("expected 3 sources, got %d", len(got))
	}
}

func TestStoreSourceConfigRemoveSource(t *testing.T) {
	store := control.NewStore(filepath.Join(t.TempDir(), "state.json"))
	cfg := bpsource.NewStoreSourceConfig(store)

	for _, id := range []string{"repo-a", "repo-b"} {
		_ = cfg.UpsertSource(bpsource.Source{ID: id, Name: id, URL: "https://example.com/" + id, Ref: "refs/heads/main"})
	}

	if err := cfg.RemoveSource("repo-a"); err != nil {
		t.Fatalf("RemoveSource: %v", err)
	}
	srcs := cfg.Sources()
	if len(srcs) != 1 {
		t.Fatalf("expected 1 source after remove, got %d: %v", len(srcs), srcs)
	}
	if srcs[0].ID != "repo-b" {
		t.Fatalf("wrong source remaining: %+v", srcs[0])
	}
}

func TestStoreSourceConfigRemoveSource_NonExistentIsNoOp(t *testing.T) {
	store := control.NewStore(filepath.Join(t.TempDir(), "state.json"))
	cfg := bpsource.NewStoreSourceConfig(store)

	if err := cfg.RemoveSource("ghost"); err != nil {
		t.Fatalf("RemoveSource non-existent must not error: %v", err)
	}
	if len(cfg.Sources()) != 0 {
		t.Fatalf("expected empty sources after no-op remove, got %d", len(cfg.Sources()))
	}
}

func TestStoreSourceConfigPersistsThroughStoreReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := control.NewStore(path)
	cfg := bpsource.NewStoreSourceConfig(store)

	s := bpsource.Source{
		ID:          "persist-test",
		Name:        "Persist Test",
		Namespace:   "pt",
		URL:         "https://github.com/example/persist",
		Ref:         "refs/heads/main",
		Subpath:     "blueprints",
		TokenEnvVar: "PT_TOKEN",
		LastSHA:     "cafebabe",
		LastFetchMs: 9999,
		LastErr:     "some error",
	}
	if err := cfg.UpsertSource(s); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}

	// Re-open the store from disk; the source must survive.
	reopened := control.NewStore(path)
	cfg2 := bpsource.NewStoreSourceConfig(reopened)
	got := cfg2.Sources()
	if len(got) != 1 {
		t.Fatalf("expected 1 source after reload, got %d", len(got))
	}
	if got[0] != s {
		t.Fatalf("source mismatch after reload:\n  got  %+v\n  want %+v", got[0], s)
	}
}

// TestStoreSourceConfigFieldForFieldMapping verifies every field maps losslessly both ways.
func TestStoreSourceConfigFieldForFieldMapping(t *testing.T) {
	store := control.NewStore(filepath.Join(t.TempDir(), "state.json"))
	cfg := bpsource.NewStoreSourceConfig(store)

	full := bpsource.Source{
		ID:          "full-mapping",
		Name:        "Full Mapping",
		Namespace:   "fm",
		URL:         "https://github.com/example/full",
		Ref:         "refs/heads/feat",
		Subpath:     "sub/path",
		TokenEnvVar: "FM_TOKEN",
		LastSHA:     "0123456789abcdef",
		LastFetchMs: 1718700000000,
		LastErr:     "connection refused",
	}
	if err := cfg.UpsertSource(full); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}

	// Check that the control.Store itself has an identical SourceView (field-for-field).
	sv := store.Snapshot().BlueprintSources
	if len(sv) != 1 {
		t.Fatalf("expected 1 SourceView in store, got %d", len(sv))
	}
	view := sv[0]
	if view.ID != full.ID || view.Name != full.Name || view.Namespace != full.Namespace ||
		view.URL != full.URL || view.Ref != full.Ref || view.Subpath != full.Subpath ||
		view.TokenEnvVar != full.TokenEnvVar || view.LastSHA != full.LastSHA ||
		view.LastFetchMs != full.LastFetchMs || view.LastErr != full.LastErr {
		t.Fatalf("SourceView field mismatch:\n  view   %+v\n  source %+v", view, full)
	}

	// And Sources() maps SourceView back to Source identically.
	roundTripped := cfg.Sources()
	if len(roundTripped) != 1 || roundTripped[0] != full {
		t.Fatalf("Sources() round-trip mismatch:\n  got  %+v\n  want %+v", roundTripped[0], full)
	}
}
