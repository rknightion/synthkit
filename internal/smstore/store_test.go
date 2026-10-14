// SPDX-License-Identifier: AGPL-3.0-only
package smstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/smstate"
)

type memoryBackend struct {
	data    []byte
	rev     control.Revision
	calls   int
	hook    func()
	failure error
}

func (m *memoryBackend) Load(context.Context, control.Key) (control.Snapshot, error) {
	return control.Snapshot{Data: append([]byte(nil), m.data...), Revision: m.rev}, nil
}
func (m *memoryBackend) CompareAndSwap(_ context.Context, k control.Key, rev control.Revision, data []byte) (control.Revision, error) {
	if k != control.SMState {
		panic("wrong slot")
	}
	m.calls++
	if m.hook != nil {
		fn := m.hook
		m.hook = nil
		fn()
	}
	if m.failure != nil {
		return "", m.failure
	}
	if rev != m.rev {
		return "", control.ErrConflict
	}
	m.data = append([]byte(nil), data...)
	m.rev += "x"
	return m.rev, nil
}
func snapshot(t *testing.T, release string) smstate.Snapshot {
	t.Helper()
	p := smstate.ProbeSpec{Name: "private", Region: "EMEA", Key: smstate.ProbeKey("private", "EMEA")}
	c := smstate.CheckSpec{Key: smstate.CheckKey("health", "https://example.test"), Job: "health", Target: "https://example.test", Source: "sample", ProbeKey: p.Key, FrequencyMS: 60000, TimeoutMS: 3000}
	s, err := smstate.NewSnapshot([]smstate.ProbeSpec{p}, []smstate.CheckSpec{c}, "https://sm.example", "", release, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func journal(s smstate.Snapshot) smstate.Journal {
	expected := json.RawMessage(`{"job":"health"}`)
	return smstate.Journal{SchemaVersion: 1, Operation: "create", Kind: "check", Key: s.Checks[0].Key, SnapshotHash: s.SnapshotHash, ExpectedHash: smstate.SpecHash(expected), ExpectedSpec: expected, StartedUnixMS: 1}
}
func TestBackendConcurrentSnapshotPublicationPreservesEvidence(t *testing.T) {
	ctx := context.Background()
	m := &memoryBackend{rev: "1"}
	store, err := NewBackendStore(m, 5, "provision")
	if err != nil {
		t.Fatal(err)
	}
	a := snapshot(t, "a")
	if err := store.PublishSnapshot(ctx, &a); err != nil {
		t.Fatal(err)
	}
	j := journal(a)
	if err := store.UpdateProvision(ctx, Binding(a), func(p *ProvisionState) error { p.Pending = &j; return nil }); err != nil {
		t.Fatal(err)
	}
	b := snapshot(t, "b")
	if a.SnapshotHash != b.SnapshotHash {
		t.Fatal("release-only hash drift")
	}
	m.hook = func() {
		d, err := decode(m.data, "provision")
		if err != nil {
			t.Fatal(err)
		}
		d.Snapshot = &b
		m.data, _ = encode(d, "provision")
		m.rev += "b"
	}
	err = store.UpdateProvision(ctx, Binding(a), func(p *ProvisionState) error { p.Pending = nil; return nil })
	if !errors.Is(err, ErrBinding) {
		t.Fatalf("concurrent version change not rejected: %v", err)
	}
	d, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.Snapshot.SourceVersion != "b" || d.Provision.Pending == nil {
		t.Fatal("CAS replay overwrote snapshot or evidence")
	}
	if err := store.PublishSnapshot(ctx, nil); err != nil {
		t.Fatal(err)
	}
	d, err = store.Load(ctx)
	if err != nil || d.Snapshot != nil || d.Provision.Pending == nil {
		t.Fatal("deselect erased provision evidence", err)
	}
}
func TestStoreStopsOnUnknownAndBoundsConflicts(t *testing.T) {
	for _, red := range []error{control.ErrConflict, control.ErrOutcomeUnknown} {
		m := &memoryBackend{rev: "1", failure: red}
		s, err := NewBackendStore(m, 3, "provision")
		if err != nil {
			t.Fatal(err)
		}
		snap := snapshot(t, "a")
		err = s.PublishSnapshot(context.Background(), &snap)
		expected := 3
		if red == control.ErrOutcomeUnknown {
			expected = 1
		}
		if !errors.Is(err, red) || m.calls != expected {
			t.Fatalf("failure=%v calls=%d expected=%d", err, m.calls, expected)
		}
	}
}
func TestStoreStrictEnvelopeAndNestedEvidence(t *testing.T) {
	s := snapshot(t, "a")
	base := Document{SchemaVersion: 1, ProvisionLeaseName: "provision", Snapshot: &s}
	valid, _ := encode(base, "provision")
	if _, err := decode(valid, "provision"); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"trailing": append(append([]byte(nil), valid...), []byte(` {}`)...), "unknown": []byte(`{"schema_version":1,"provision_lease_name":"provision","extra":true}`), "cap": []byte(strings.Repeat(" ", control.MaxStateDocumentBytes+1)), "badjson": []byte(`{"schema_version":`)} {
		t.Run(name, func(t *testing.T) {
			if _, err := decode(data, "provision"); err == nil {
				t.Fatal("corrupt evidence admitted")
			}
		})
	}
	if _, err := decode(valid, "other"); err == nil {
		t.Fatal("Lease identity silently rebound")
	}
	j := journal(s)
	j.ExpectedHash = "wrong"
	base.Provision.Pending = &j
	data, _ := json.Marshal(base)
	if _, err := decode(data, "provision"); err == nil {
		t.Fatal("corrupt pending accepted")
	}
	if _, err := NewBackendStore(&memoryBackend{}, 6, "provision"); err == nil {
		t.Fatal("CAS budget widened")
	}
}
func TestFileStoreReadsRequirePositiveLockOwnership(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plain := NewFileStore(dir)
	snap := snapshot(t, "a")
	if err := plain.PublishSnapshot(context.Background(), &snap); err != nil {
		t.Fatal(err)
	}
	owned, release, err := AcquireFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if release != nil {
			_ = release()
		}
	}()
	t.Run("existing-lock-is-not-ownership", func(t *testing.T) {
		if _, err := plain.Load(context.Background()); err == nil {
			t.Fatal("unowned reader bypassed existing O_EXCL lock")
		}
		if err := plain.UpdateProvision(context.Background(), Binding(snap), func(p *ProvisionState) error { p.Pending = nil; return nil }); err == nil {
			t.Fatal("unowned writer inferred ownership")
		}
		if _, _, err := AcquireFileStore(dir); err == nil {
			t.Fatal("second owner admitted")
		}
	})
	t.Run("positive-owner", func(t *testing.T) {
		d, err := owned.Load(context.Background())
		if err != nil || d.Snapshot == nil || d.Snapshot.SnapshotHash != snap.SnapshotHash {
			t.Fatal("owned reader unavailable", err)
		}
	})
	if err := release(); err != nil {
		t.Fatal(err)
	}
	release = nil
	t.Run("released-store-denied", func(t *testing.T) {
		if _, err := owned.Load(context.Background()); err == nil {
			t.Fatal("released reader admitted")
		}
		if err := owned.UpdateProvision(context.Background(), Binding(snap), func(p *ProvisionState) error { return nil }); err == nil {
			t.Fatal("released writer admitted")
		}
	})
	if err := os.WriteFile(filepath.Join(dir, smstate.LockFilename), []byte("untrusted stale owner\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Run("stale-lock-refused", func(t *testing.T) {
		if _, err := plain.Load(context.Background()); err == nil {
			t.Fatal("stale lock guessed removable")
		}
		for _, candidate := range []*smstate.Snapshot{&snap, nil} {
			if err := plain.PublishSnapshot(context.Background(), candidate); err == nil {
				t.Fatal("publisher bypassed stale O_EXCL lock")
			}
		}
		if _, err := os.Stat(filepath.Join(dir, smstate.LockFilename)); err != nil {
			t.Fatal("stale lock erased", err)
		}
	})
}

func TestFileStoreSnapshotPublicationJoinsProvisionLockAndKeepsV1Evidence(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewFileStore(dir)
	snap := snapshot(t, "a")
	if err := s.PublishSnapshot(context.Background(), &snap); err != nil {
		t.Fatal(err)
	}
	owned, release, err := AcquireFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	j := journal(snap)
	if err := owned.UpdateProvision(context.Background(), Binding(snap), func(p *ProvisionState) error { p.Pending = &j; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishSnapshot(context.Background(), nil); err == nil {
		t.Fatal("publisher rewrote snapshot while provisioner held lock")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishSnapshot(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, smstate.JournalFilename)); err != nil {
		t.Fatal("v1 pending erased", err)
	}
}

func TestAcquiredFileStoreCannotPublishSnapshots(t *testing.T) {
	for _, released := range []bool{false, true} {
		name := "owned"
		if released {
			name = "released"
		}
		t.Run(name, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			original := snapshot(t, "original")
			if err := NewFileStore(dir).PublishSnapshot(ctx, &original); err != nil {
				t.Fatal(err)
			}
			owned, release, err := AcquireFileStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if !released {
					_ = release()
				}
			})
			pending := journal(original)
			ownership := smstate.OwnershipLedger{SchemaVersion: smstate.SchemaVersion, TargetFingerprint: original.TargetFingerprint, WrittenUnixMS: 1,
				Resources: []smstate.Resource{{Kind: "check", Key: original.Checks[0].Key, ID: 123, Modified: 1, Ownership: smstate.Owned}}}
			if err := owned.UpdateProvision(ctx, Binding(original), func(p *ProvisionState) error {
				p.Pending = &pending
				p.Ownership = &ownership
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if released {
				if err := release(); err != nil {
					t.Fatal(err)
				}
			}
			// Compare every real private file, including the lock when owned, so a
			// denied call cannot quietly alter snapshot, pending or ownership evidence.
			readFiles := func() map[string]string {
				t.Helper()
				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}
				files := make(map[string]string)
				for _, entry := range entries {
					data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
					if err != nil {
						t.Fatal(err)
					}
					files[entry.Name()] = string(data)
				}
				return files
			}
			before := readFiles()
			replacement := snapshot(t, "replacement")
			for _, candidate := range []*smstate.Snapshot{&replacement, nil} {
				err := owned.PublishSnapshot(ctx, candidate)
				if err == nil || err.Error() != "SM acquired provisioner file store cannot publish snapshots" {
					t.Errorf("publication did not return capability denial: %v", err)
				}
				if !reflect.DeepEqual(before, readFiles()) {
					t.Error("forbidden publication changed private files")
				}
			}
		})
	}
}
