// SPDX-License-Identifier: AGPL-3.0-only
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rknightion/synthkit/internal/ha"
)

func TestDeclaredProjectionPreservesOwnedState(t *testing.T) {
	for _, kind := range []string{"legacy-file", "file-backend", "kubernetes-fake"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			var backend StateBackend
			var store *Store
			var err error
			if kind == "legacy-file" {
				store = NewStore(filepath.Join(root, "control.json"))
			} else {
				if kind == "kubernetes-fake" {
					backend = testProjectionBackend(t)
				} else {
					backend, err = NewFileBackend(FileBackendOptions{Gate: ha.AlwaysLeader{}, Paths: map[Key]string{Control: filepath.Join(root, "control.json"), BootManifest: filepath.Join(root, "boot.json"), Key("git-source/declared"): filepath.Join(root, "source.json")}})
					if err != nil {
						t.Fatal(err)
					}
				}
				store, err = NewBackendStore(ctx, backend, ha.AlwaysLeader{}, 5)
				if err != nil {
					t.Fatal(err)
				}
			}
			source := SourceView{Declared: true, ID: "declared", Name: "Old", Namespace: "declared", URL: "https://example.invalid/old", Ref: "refs/heads/main", FetchedSHA: "fetched", LoadedSHA: "applied", ObservedSHA: "observed", FetchedFileCount: 3, EffectiveNames: []string{"declared/one"}, LoadedNames: []string{"declared/one"}, Skipped: []string{"retained"}, LastFetchMs: 123, LastErr: "sync detail", PendingRestart: true}
			console := source
			console.Declared = false
			console.ID = "console"
			console.Name = "Console"
			console.EffectiveNames, console.LoadedNames, console.Skipped = nil, nil, nil
			_, err = store.UpdateContext(ctx, func(st *State) {
				st.BlueprintSources = []SourceView{console, source}
				st.Scaling["api"] = 7
				st.VolumeMultiplier = 2
				st.DeclaredBlueprintSources = []DeclaredSource{SourceDefinition(source)}
			})
			if err != nil {
				t.Fatal(err)
			}
			ownedBefore, _ := json.Marshal(store.Snapshot().BlueprintSources)
			var frozen map[Key][]byte
			if backend != nil {
				frozen = map[Key][]byte{BootManifest: []byte("{\"applied_revision\":\"keep exactly\"}\n"), Key("git-source/declared"): []byte("{\"sync\":\"retained\",\"applied\":\"revision\"}\n")}
				for key, data := range frozen {
					snap, e := backend.Load(ctx, key)
					if e != nil {
						t.Fatal(e)
					}
					if _, e = backend.CompareAndSwap(ctx, key, snap.Revision, data); e != nil {
						t.Fatal(e)
					}
				}
			} else {
				frozen = map[Key][]byte{BootManifest: []byte("applied manifest\n")}
				if e := os.WriteFile(filepath.Join(root, "boot.json"), frozen[BootManifest], 0600); e != nil {
					t.Fatal(e)
				}
			}
			desired := SourceDefinition(source)
			desired.Name = "Changed"
			desired.URL = "https://example.invalid/new"
			added := desired
			added.ID = "added"
			added.Namespace = "added"
			for _, set := range [][]DeclaredSource{{desired, added}, {added}, {}} {
				if err := store.ReconcileDeclaredSources(ctx, set); err != nil {
					t.Fatal(err)
				}
				ownedAfter, _ := json.Marshal(store.Snapshot().BlueprintSources)
				if !bytes.Equal(ownedBefore, ownedAfter) {
					t.Fatalf("instance-owned source state changed: before=%s after=%s", ownedBefore, ownedAfter)
				}
				if store.Snapshot().Scaling["api"] != 7 || store.Snapshot().VolumeMultiplier != 2 {
					t.Fatal("unrelated knobs rewritten")
				}
				if backend != nil {
					for key, data := range frozen {
						snap, e := backend.Load(ctx, key)
						if e != nil || !bytes.Equal(snap.Data, data) {
							t.Fatalf("%s rewritten: %q error %v", key, snap.Data, e)
						}
					}
				} else {
					data, e := os.ReadFile(filepath.Join(root, "boot.json"))
					if e != nil || !bytes.Equal(data, frozen[BootManifest]) {
						t.Fatal("file manifest rewritten")
					}
				}
			}
			conflict := desired
			conflict.ID = "console"
			before, _ := json.Marshal(store.Snapshot())
			if err := store.ReconcileDeclaredSources(ctx, []DeclaredSource{conflict}); err == nil {
				t.Fatal("console ownership adopted")
			}
			after, _ := json.Marshal(store.Snapshot())
			if !bytes.Equal(before, after) {
				t.Fatal("refused projection changed logical state")
			}
			if err := store.ReconcileDeclaredSources(ctx, []DeclaredSource{desired}); err != nil {
				t.Fatal(err)
			}
			reset, err := store.ResetContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(reset.DeclaredBlueprintSources) != 1 || len(reset.BlueprintSources) != 1 || !reset.BlueprintSources[0].Declared {
				t.Fatal("reset erased declarations or their status")
			}
		})
	}
}
