// SPDX-License-Identifier: AGPL-3.0-only
package control

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ha"
)

func TestFleetRegisteredKeyNamedKubernetesSlot(t *testing.T) {
	ctx := context.Background()
	gate := ha.NewGate()
	// Reuse the sanctioned adapter fake; Kubernetes imports stay in its exact test file.
	_, memory := testNamedBackend(t, gate)
	memory.object.Name = "fleet"
	memory.object.Data["unrelated"] = "kept"
	backend, err := newKubernetesBackend(KubernetesBackendOptions{Gate: gate, Namespace: "test", Objects: map[Key]string{FleetRegistered: "fleet"}, RequestTimeout: time.Second, MaxDocumentBytes: MaxStateDocumentBytes}, memory)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := backend.Load(ctx, FleetRegistered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.CompareAndSwap(ctx, FleetRegistered, snap.Revision, []byte(`{"version":1,"ids":["collector"]}`)); !errors.Is(err, ha.ErrNotLeader) || memory.writes != 0 {
		t.Fatalf("standby: %v writes=%d", err, memory.writes)
	}
	_ = gate.Activate(ctx, func(context.Context) error { return nil })
	if _, err := backend.CompareAndSwap(ctx, FleetRegistered, snap.Revision, []byte(`{"version":1,"ids":["collector"]}`)); err != nil {
		t.Fatal(err)
	}
	if memory.object.Data["unrelated"] != "kept" || memory.writes != 1 {
		t.Fatal("named update lost unrelated data")
	}
	gate.Revoke()
	current, _ := backend.Load(ctx, FleetRegistered)
	if _, err := backend.CompareAndSwap(ctx, FleetRegistered, current.Revision, []byte(`{}`)); !errors.Is(err, ha.ErrNotLeader) {
		t.Fatalf("revoked=%v", err)
	}
}

func TestFleetRegisteredKeyUsesExistingGatedBackend(t *testing.T) {
	gate := ha.NewGate()
	backend, err := NewFileBackend(FileBackendOptions{Gate: gate, Paths: map[Key]string{Key("fleet-registered"): filepath.Join(t.TempDir(), "fleet.json")}})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := backend.Load(context.Background(), Key("fleet-registered"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = backend.CompareAndSwap(context.Background(), Key("fleet-registered"), snap.Revision, []byte(`{"ids":["collector"]}`)); err == nil {
		t.Fatal("standby wrote fleet state")
	}
	_ = gate.Activate(context.Background(), func(context.Context) error { return nil })
	if _, err = backend.CompareAndSwap(context.Background(), Key("fleet-registered"), snap.Revision, []byte(`{"ids":["collector"]}`)); err != nil {
		t.Fatal(err)
	}
}
