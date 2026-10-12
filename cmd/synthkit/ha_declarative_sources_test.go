// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
)

func TestHADeclaredSourcesReadOnlyViewAndPreparation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "sources.json")
	if err := os.WriteFile(path, []byte(`[{"id":"example","name":"Example","namespace":"example","url":"https://example.invalid/repo","ref":"main"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(root, "absent.env"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.HAMode = "lease"
	cfg.DryRun = true
	cfg.SelfObsEnabled = false
	cfg.GitSourcesFile = path
	cfg.BlueprintsDir = filepath.Join(root, "baked")
	cfg.BlueprintDataDir = filepath.Join(root, "data")
	cfg.SnapshotPath = filepath.Join(root, "control.json")
	cfg.BlueprintNames = nil
	gate := ha.NewGate()
	backend, err := control.NewFileBackend(control.FileBackendOptions{Gate: gate, Paths: map[control.Key]string{control.Control: filepath.Join(root, "control.json"), control.BootManifest: filepath.Join(root, "boot.json"), control.Key("git-source/example"): filepath.Join(root, "source.json")}})
	if err != nil {
		t.Fatal(err)
	}
	bound := ha.Bounded{Gate: gate, Timeout: 2 * time.Second, Margin: time.Second, Crash: func() { t.Error("unexpected fence crash") }}
	standby, err := newHAView(ctx, cfg, gate, bound, false, backend)
	if err != nil {
		t.Fatal(err)
	}
	if len(standby.manager.Sources()) != 1 || !standby.manager.Sources()[0].Declared {
		t.Fatal("standby failed to preview input")
	}
	for _, file := range []string{"control.json", "boot.json", "source.json"} {
		if _, err := os.Stat(filepath.Join(root, file)); !os.IsNotExist(err) {
			t.Fatalf("read-only standby created %s", file)
		}
	}
	var leader *haView
	if err := gate.Activate(ctx, func(prep context.Context) error {
		var err error
		leader, err = newHAView(prep, cfg, gate, bound, true, backend)
		if err != nil {
			return err
		}
		return bound.Run(prep, ha.Mutation, func(c context.Context) error { return leader.manager.CommitDeclared(c) })
	}); err != nil {
		t.Fatal(err)
	}
	if len(leader.store.Snapshot().DeclaredBlueprintSources) != 1 {
		t.Fatal("leader preparation did not persist definitions")
	}
	before, err := os.ReadFile(filepath.Join(root, "control.json"))
	if err != nil {
		t.Fatal(err)
	}
	gate.Revoke()
	if err := leader.manager.ReconcileDeclaredFile(ctx, path); err == nil {
		t.Fatal("revoked leader reconciled")
	}
	after, err := os.ReadFile(filepath.Join(root, "control.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("revocation allowed projection write")
	}
}
