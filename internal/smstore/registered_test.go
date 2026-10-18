// SPDX-License-Identifier: AGPL-3.0-only
package smstore

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/smstate"
)

// targetSnapshot is a one-check snapshot whose specification (and so hash) differs per target.
func targetSnapshot(t *testing.T, target, label string) smstate.Snapshot {
	t.Helper()
	p := smstate.ProbeSpec{Name: "private", Region: "EMEA", Key: smstate.ProbeKey("private", "EMEA")}
	c := smstate.CheckSpec{Key: smstate.CheckKey("health", target), Job: "health", Target: target, Source: "sample", ProbeKey: p.Key, FrequencyMS: 60000, TimeoutMS: 3000}
	if label != "" {
		c.Labels = []smstate.Label{{Name: "pad", Value: label}}
	}
	s, err := smstate.NewSnapshot([]smstate.ProbeSpec{p}, []smstate.CheckSpec{c}, "https://sm.example", "", "v1", time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func completeRegistration(s smstate.Snapshot) smstate.Registration {
	r := smstate.Registration{SchemaVersion: smstate.ActivationSchemaVersion, SnapshotHash: s.SnapshotHash, TargetFingerprint: s.TargetFingerprint, SourceVersion: s.SourceVersion, WrittenUnixMS: 1}
	for i, p := range s.Probes {
		r.Resources = append(r.Resources, smstate.Resource{Kind: "probe", Key: p.Key, ID: int64(10 + i), Modified: 10, Ownership: smstate.Owned})
	}
	for i, c := range s.Checks {
		m := 1725000000 + float64(i)
		r.Resources = append(r.Resources, smstate.Resource{Kind: "check", Key: c.Key, ID: int64(100 + i), Modified: m, ConfigVersion: smstate.ConfigVersion(m), Ownership: smstate.Owned})
	}
	return r
}

type retentionStore struct {
	store Store
	dir   string // file backend only
	// provision is a provisioner write bound to the published snapshot s.
	provision func(t *testing.T, s smstate.Snapshot, fn func(*ProvisionState) error)
}

func (f retentionStore) register(t *testing.T, s smstate.Snapshot) {
	t.Helper()
	r := completeRegistration(s)
	f.provision(t, s, func(p *ProvisionState) error { p.Registration = &r; return nil })
}

func retentionStores(t *testing.T) map[string]func(t *testing.T) retentionStore {
	return map[string]func(t *testing.T) retentionStore{
		"kubernetes": func(t *testing.T) retentionStore {
			store, err := NewBackendStore(&memoryBackend{rev: "1"}, 5, "provision")
			if err != nil {
				t.Fatal(err)
			}
			return retentionStore{store: store, provision: func(t *testing.T, s smstate.Snapshot, fn func(*ProvisionState) error) {
				t.Helper()
				if err := store.UpdateProvision(context.Background(), Binding(s), fn); err != nil {
					t.Fatal(err)
				}
			}}
		},
		"file": func(t *testing.T) retentionStore {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "runtime")
			return retentionStore{store: NewFileStore(dir), dir: dir, provision: func(t *testing.T, s smstate.Snapshot, fn func(*ProvisionState) error) {
				t.Helper()
				owned, release, err := AcquireFileStore(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = release() }()
				if err := owned.UpdateProvision(context.Background(), Binding(s), fn); err != nil {
					t.Fatal(err)
				}
			}}
		},
	}
}

func registeredHash(t *testing.T, store Store) (published, registered string) {
	t.Helper()
	doc, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if doc.Snapshot != nil {
		published = doc.Snapshot.SnapshotHash
	}
	if doc.Registered != nil {
		registered = doc.Registered.SnapshotHash
	}
	return published, registered
}

// Publishing over the snapshot the stored registration is complete for retains it; later
// publications keep it while the registration still validates it, and drop it when the published
// snapshot is the registered one again, when SM is invalidated, or when there is no registration.
func TestPublicationRetainsRegisteredSnapshot(t *testing.T) {
	ctx := context.Background()
	for name, setup := range retentionStores(t) {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			a, b, c := targetSnapshot(t, "https://a.test", ""), targetSnapshot(t, "https://b.test", ""), targetSnapshot(t, "https://c.test", "")
			if err := f.store.PublishSnapshot(ctx, &b); err != nil {
				t.Fatal(err)
			}
			if err := f.store.PublishSnapshot(ctx, &a); err != nil {
				t.Fatal(err)
			}
			if _, reg := registeredHash(t, f.store); reg != "" {
				t.Fatalf("publication without a registration retained %s", reg)
			}
			f.register(t, a)
			if err := f.store.PublishSnapshotIfIdle(ctx, b); err != nil {
				t.Fatal(err)
			}
			if pub, reg := registeredHash(t, f.store); pub != b.SnapshotHash || reg != a.SnapshotHash {
				t.Fatalf("live publication over the registered snapshot: published %s retained %s, want %s and %s", pub, reg, b.SnapshotHash, a.SnapshotHash)
			}
			for _, next := range []smstate.Snapshot{c, c, b} {
				if err := f.store.PublishSnapshot(ctx, &next); err != nil {
					t.Fatal(err)
				}
				if _, reg := registeredHash(t, f.store); reg != a.SnapshotHash {
					t.Fatalf("later publication dropped the registered snapshot (retained %q)", reg)
				}
			}
			if err := f.store.PublishSnapshot(ctx, &a); err != nil {
				t.Fatal(err)
			}
			if pub, reg := registeredHash(t, f.store); pub != a.SnapshotHash || reg != "" {
				t.Fatalf("republishing the registered snapshot: published %s retained %q, want nothing retained", pub, reg)
			}
			if err := f.store.PublishSnapshot(ctx, &b); err != nil {
				t.Fatal(err)
			}
			if err := f.store.PublishSnapshot(ctx, nil); err != nil {
				t.Fatal(err)
			}
			if pub, reg := registeredHash(t, f.store); pub != "" || reg != "" {
				t.Fatalf("invalidation left published %q retained %q", pub, reg)
			}
		})
	}
}

// The provisioner registering the published snapshot makes the retained one stale; the next
// publication retains the newly registered snapshot instead.
func TestPublicationRetainsTheCurrentlyRegisteredSnapshot(t *testing.T) {
	ctx := context.Background()
	for name, setup := range retentionStores(t) {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			a, b, c := targetSnapshot(t, "https://a.test", ""), targetSnapshot(t, "https://b.test", ""), targetSnapshot(t, "https://c.test", "")
			for _, step := range []*smstate.Snapshot{&a, nil, &b} {
				if step == nil {
					f.register(t, a)
					continue
				}
				if err := f.store.PublishSnapshot(ctx, step); err != nil {
					t.Fatal(err)
				}
			}
			f.register(t, b)
			if err := f.store.PublishSnapshot(ctx, &c); err != nil {
				t.Fatal(err)
			}
			if _, reg := registeredHash(t, f.store); reg != b.SnapshotHash {
				t.Fatalf("retained %q, want the snapshot registered last %s", reg, b.SnapshotHash)
			}
		})
	}
}

// A retained snapshot that would exceed the document byte cap is dropped, not written.
func TestBackendRetentionDroppedPastByteCap(t *testing.T) {
	ctx := context.Background()
	m := &memoryBackend{rev: "1"}
	store, err := NewBackendStore(m, 5, "provision")
	if err != nil {
		t.Fatal(err)
	}
	pad := strings.Repeat("x", 450_000)
	a, b := targetSnapshot(t, "https://a.test", pad), targetSnapshot(t, "https://b.test", pad)
	if err := store.PublishSnapshot(ctx, &a); err != nil {
		t.Fatal(err)
	}
	r := completeRegistration(a)
	if err := store.UpdateProvision(ctx, Binding(a), func(p *ProvisionState) error { p.Registration = &r; return nil }); err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	if err := store.PublishSnapshot(ctx, &b); err != nil {
		t.Fatalf("publication past the cap with retention = %v, want the retention dropped", err)
	}
	if !strings.Contains(logs.String(), "retained registered SM snapshot dropped") {
		t.Fatalf("size drop was not logged: %q", logs.String())
	}
	if pub, reg := registeredHash(t, store); pub != b.SnapshotHash || reg != "" {
		t.Fatalf("published %s retained %q, want b with nothing retained", pub, reg)
	}
}

// The provisioner's first write that replaces or removes the registration (here a pending journal
// with activation removed, as beginMutation writes it) drops the retained snapshot in the same
// write; a write that leaves the registration valid for it keeps it.
func TestProvisionWriteDropsStaleRegisteredSnapshot(t *testing.T) {
	ctx := context.Background()
	for name, setup := range retentionStores(t) {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			a, b := targetSnapshot(t, "https://a.test", ""), targetSnapshot(t, "https://b.test", "")
			if err := f.store.PublishSnapshot(ctx, &a); err != nil {
				t.Fatal(err)
			}
			f.register(t, a)
			if err := f.store.PublishSnapshot(ctx, &b); err != nil {
				t.Fatal(err)
			}
			f.provision(t, b, func(*ProvisionState) error { return nil })
			if _, reg := registeredHash(t, f.store); reg != a.SnapshotHash {
				t.Fatalf("a write keeping the registration dropped the retained snapshot (%q)", reg)
			}
			j := journal(b)
			f.provision(t, b, func(p *ProvisionState) error { p.Pending = &j; p.Registration = nil; return nil })
			if _, reg := registeredHash(t, f.store); reg != "" {
				t.Fatalf("provisioner mutation start kept the unusable retained snapshot %s", reg)
			}
		})
	}
	t.Run("file partial registration", func(t *testing.T) {
		f := retentionStores(t)["file"](t)
		a, b := targetSnapshot(t, "https://a.test", ""), targetSnapshot(t, "https://b.test", "")
		if err := f.store.PublishSnapshot(ctx, &a); err != nil {
			t.Fatal(err)
		}
		f.register(t, a)
		if err := f.store.PublishSnapshot(ctx, &b); err != nil {
			t.Fatal(err)
		}
		partial := completeRegistration(b)
		partial.Resources = partial.Resources[:1] // persistResource's first write for b
		f.provision(t, b, func(p *ProvisionState) error { p.Registration = &partial; return nil })
		if _, err := os.Lstat(filepath.Join(f.dir, smstate.RegisteredFilename)); !os.IsNotExist(err) {
			t.Fatalf("retained file after the provisioner's first resource write: %v", err)
		}
	})
}

// A large provisioner journal fits once the retained snapshot it makes unusable is dropped: the
// retained snapshot never pushes that write past the document cap and strands the mutation.
func TestBackendProvisionWriteFitsCapWithoutStaleRetention(t *testing.T) {
	ctx := context.Background()
	store, err := NewBackendStore(&memoryBackend{rev: "1"}, 5, "provision")
	if err != nil {
		t.Fatal(err)
	}
	pad := strings.Repeat("x", 300_000)
	a, b := targetSnapshot(t, "https://a.test", pad), targetSnapshot(t, "https://b.test", pad)
	if err := store.PublishSnapshot(ctx, &a); err != nil {
		t.Fatal(err)
	}
	r := completeRegistration(a)
	if err := store.UpdateProvision(ctx, Binding(a), func(p *ProvisionState) error { p.Registration = &r; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishSnapshot(ctx, &b); err != nil {
		t.Fatal(err)
	}
	if _, reg := registeredHash(t, store); reg != a.SnapshotHash {
		t.Fatalf("precondition: retained %q, want a", reg)
	}
	expected, _ := json.Marshal(map[string]string{"pad": strings.Repeat("y", 250_000)})
	j := smstate.Journal{SchemaVersion: 1, Operation: "update", Kind: "check", Key: b.Checks[0].Key, SnapshotHash: b.SnapshotHash, ExpectedHash: smstate.SpecHash(json.RawMessage(expected)), ExpectedSpec: expected, StartedUnixMS: 1}
	if err := store.UpdateProvision(ctx, Binding(b), func(p *ProvisionState) error { p.Pending = &j; p.Registration = nil; return nil }); err != nil {
		t.Fatalf("provisioner journal write = %v, want it to fit once the stale retention is dropped", err)
	}
}

// Boot publication overwrites an unreadable or invalid published or retained snapshot file, as it
// did before retention: such a file is no retention candidate and never aborts the boot.
func TestFileBootPublishOverwritesInvalidSnapshotFiles(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{smstate.SnapshotFilename, smstate.RegisteredFilename} {
		t.Run(name, func(t *testing.T) {
			f := retentionStores(t)["file"](t)
			a, b := targetSnapshot(t, "https://a.test", ""), targetSnapshot(t, "https://b.test", "")
			if err := f.store.PublishSnapshot(ctx, &a); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.dir, name), []byte(`{"schema_version":2}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := f.store.PublishSnapshot(ctx, &b); err != nil {
				t.Fatalf("boot publication over an invalid %s = %v, want it overwritten", name, err)
			}
			if pub, reg := registeredHash(t, f.store); pub != b.SnapshotHash || reg != "" {
				t.Fatalf("after overwrite: published %s retained %q, want b and nothing retained", pub, reg)
			}
		})
	}
}

// A registered snapshot for another SM target can never admit anything after a retargeting
// publication, so it is not retained (it would otherwise survive migration writes and use cap).
func TestPublicationDoesNotRetainAnotherTargetsSnapshot(t *testing.T) {
	ctx := context.Background()
	for name, setup := range retentionStores(t) {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			a := targetSnapshot(t, "https://a.test", "")
			rotated, err := smstate.NewSnapshot(a.Probes, []smstate.CheckSpec{{Key: smstate.CheckKey("health", "https://b.test"), Job: "health", Target: "https://b.test", Source: "sample", ProbeKey: a.Probes[0].Key, FrequencyMS: 60000, TimeoutMS: 3000}}, "https://sm.example", "rotated", "v1", time.Unix(100, 0))
			if err != nil {
				t.Fatal(err)
			}
			if rotated.TargetFingerprint == a.TargetFingerprint {
				t.Fatal("precondition: rotated credential kept the target fingerprint")
			}
			if err := f.store.PublishSnapshot(ctx, &a); err != nil {
				t.Fatal(err)
			}
			f.register(t, a)
			if err := f.store.PublishSnapshot(ctx, &rotated); err != nil {
				t.Fatal(err)
			}
			if _, reg := registeredHash(t, f.store); reg != "" {
				t.Fatalf("retargeting publication retained the old target's snapshot %s", reg)
			}
		})
	}
}
