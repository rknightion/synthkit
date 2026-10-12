// SPDX-License-Identifier: AGPL-3.0-only
package bpsource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
)

const declarationJSON = `[{"id":"declared","name":"Desired","namespace":"declared","url":"https://example.invalid/repo","ref":"refs/heads/main"}]`

func TestDeclaredFilePreviewCommitAndRefusal(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "sources.json")
	if err := os.WriteFile(path, []byte(declarationJSON), 0600); err != nil {
		t.Fatal(err)
	}
	gate := ha.NewGate()
	store, err := control.NewHAStore(filepath.Join(root, "state.json"), gate)
	if err != nil {
		t.Fatal(err)
	}
	sc := NewStoreSourceConfig(store)
	mgr := NewManager(Options{Config: sc, Gate: gate})
	before, _ := json.Marshal(store.Snapshot())
	if err := mgr.PreviewDeclaredFile(ctx, path); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(store.Snapshot())
	if !bytes.Equal(before, after) {
		t.Fatal("preview mutated control state")
	}
	if _, err := os.Stat(filepath.Join(root, "state.json")); !os.IsNotExist(err) {
		t.Fatal("standby preview wrote state")
	}
	got := mgr.Sources()
	if len(got) != 1 || !got[0].Declared {
		t.Fatalf("preview: %+v", got)
	}
	if err := gate.Activate(ctx, func(prep context.Context) error { return mgr.CommitDeclared(prep) }); err != nil {
		t.Fatal(err)
	}
	if len(store.Snapshot().DeclaredBlueprintSources) != 1 {
		t.Fatal("preparation did not commit desired definitions")
	}
	forged := got[0]
	forged.Declared = false
	if err := mgr.UpsertSource(forged); !errors.Is(err, control.ErrDeclaredSource) {
		t.Fatalf("manager edit: %v", err)
	}
	if err := sc.RemoveSource("declared"); !errors.Is(err, control.ErrDeclaredSource) {
		t.Fatalf("adapter delete: %v", err)
	}
	// Actual sync status updates remain permitted and cannot overwrite desired definitions.
	status := mgr.Sources()[0]
	status.FetchedSHA = "fetched"
	status.LoadedSHA = "applied"
	status.LastErr = "retained"
	if err := sc.UpsertSource(status); err != nil {
		t.Fatal(err)
	}
	owned, _ := json.Marshal(store.Snapshot().BlueprintSources)
	changed := []control.DeclaredSource{{ID: "declared", Name: "Changed", Namespace: "declared", URL: "https://example.invalid/new", Ref: "refs/heads/main"}}
	data, _ := json.Marshal(changed)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ReconcileDeclaredFile(ctx, path); err != nil {
		t.Fatal(err)
	}
	if mgr.Sources()[0].FetchedSHA != "" {
		t.Fatal("changed declaration attributed old fetched snapshot to new URL")
	}
	preserved, _ := json.Marshal(store.Snapshot().BlueprintSources)
	if !bytes.Equal(owned, preserved) {
		t.Fatal("projection rewrote lifecycle record")
	}
	gate.Revoke()
	if err := mgr.ReconcileDeclaredFile(ctx, path); !errors.Is(err, ha.ErrNotLeader) {
		t.Fatalf("revoked watch wrote: %v", err)
	}
}

func TestDeclaredProjectedVolumeWatch(t *testing.T) {
	root := t.TempDir()
	for _, version := range []string{"..first", "..second"} {
		if err := os.Mkdir(filepath.Join(root, version), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "..first", "sources.json"), []byte(declarationJSON), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "..second", "sources.json"), []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..first", filepath.Join(root, "..data")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "sources.json")
	if err := os.Symlink("..data/sources.json", path); err != nil {
		t.Fatal(err)
	}
	store := control.NewStore(filepath.Join(root, "state.json"))
	mgr := NewManager(Options{Config: NewStoreSourceConfig(store)})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mgr.ReconcileDeclaredFile(ctx, path); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	failures := make(chan error, 1)
	go func() {
		defer close(done)
		mgr.WatchDeclared(ctx, path, func(c context.Context, fn func(context.Context) error) error { return fn(c) }, func(err error) {
			select {
			case failures <- err:
			default:
			}
		})
	}()
	defer func() { cancel(); <-done }()
	if err := os.Symlink("..second", filepath.Join(root, "..next")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "..next"), filepath.Join(root, "..data")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(mgr.Sources()) == 0 {
			return
		}
		select {
		case err := <-failures:
			t.Fatal(err)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("watch stayed on obsolete projected-volume inode")
}

func TestDeclaredFileInvalidInputsKeepLastGoodSet(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "sources.json")
	store := control.NewStore(filepath.Join(root, "state.json"))
	mgr := NewManager(Options{Config: NewStoreSourceConfig(store)})
	if err := os.WriteFile(path, []byte(declarationJSON), 0600); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ReconcileDeclaredFile(ctx, path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`null`, `{}`, `[] []`, `[{"id":"x","id":"y"}]`, `[{"id":"declared","fetched_sha":"forged"}]`, declarationJSON[:len(declarationJSON)-1] + "," + declarationJSON[1:], `[{"id":"declared","name":"N","namespace":"n","url":"https://user:secret@example.invalid/repo","ref":"main"}]`} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if err := mgr.ReconcileDeclaredFile(ctx, path); err == nil {
			t.Fatalf("accepted bad declaration %q", bad)
		}
		after, e := os.ReadFile(filepath.Join(root, "state.json"))
		if e != nil || !bytes.Equal(before, after) {
			t.Fatal("invalid input changed persisted state")
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ReconcileDeclaredFile(ctx, path); err == nil {
		t.Fatal("missing file cleared declarations")
	}
	if len(mgr.Sources()) != 1 {
		t.Fatal("lost last good set")
	}
	if err := mgr.ReconcileDeclaredFile(ctx, ""); err != nil {
		t.Fatal(err)
	}
}
