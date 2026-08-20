// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"path/filepath"
	"testing"

	"github.com/rknightion/synthkit/internal/bpsource"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/runner"
)

func TestBlueprintAdminUpsertValidatesAndIgnoresClientLifecycleState(t *testing.T) {
	store := control.NewStore(filepath.Join(t.TempDir(), "control-state.json"))
	sc := bpsource.NewStoreSourceConfig(store)
	mgr := bpsource.NewManager(bpsource.Options{
		BakedDir: t.TempDir(), DataDir: t.TempDir(), Registry: runner.Catalog(), Config: sc,
	})
	adapter := &blueprintAdminAdapter{mgr: mgr, sc: sc}

	invalid := control.SourceView{ID: "team", Name: "Team", Namespace: "team", URL: "http://example.invalid/repo", Ref: "refs/heads/main"}
	if err := adapter.UpsertSource(invalid); err == nil {
		t.Fatal("insecure source URL unexpectedly persisted")
	}
	if got := adapter.Sources(); len(got) != 0 {
		t.Fatalf("invalid source persisted: %+v", got)
	}

	valid := invalid
	valid.URL = "https://example.invalid/repo"
	valid.FetchedSHA = "client-forged"
	valid.LoadedSHA = "client-forged"
	valid.PendingRestart = true
	if err := adapter.UpsertSource(valid); err != nil {
		t.Fatalf("valid source: %v", err)
	}
	got := adapter.Sources()
	if len(got) != 1 || got[0].ID != "team" {
		t.Fatalf("sources = %+v", got)
	}
	if got[0].FetchedSHA != "" || got[0].LoadedSHA != "" || got[0].PendingRestart {
		t.Fatalf("client lifecycle fields were trusted: %+v", got[0])
	}
}
