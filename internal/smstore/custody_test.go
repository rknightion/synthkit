// SPDX-License-Identifier: AGPL-3.0-only
package smstore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/smstate"
)

// custodyFixture is one store with applied ownership and activation evidence and no published
// snapshot: the state an emitter leaves once SM is startup-disabled or deselected.
type custodyFixture struct {
	store  Store
	write  func(CustodyBinding, func(*ProvisionState) error) error
	target string
	a      smstate.Snapshot
}

func custodyFixtures(t *testing.T) map[string]func(t *testing.T) custodyFixture {
	ctx := context.Background()
	seed := func(t *testing.T, store Store, provision func(SnapshotBinding, func(*ProvisionState) error) error) smstate.Snapshot {
		t.Helper()
		a := snapshot(t, "a")
		if err := store.PublishSnapshot(ctx, &a); err != nil {
			t.Fatal(err)
		}
		resources := []smstate.Resource{
			{Kind: "check", Key: a.Checks[0].Key, ID: 12, Modified: 20, ConfigVersion: smstate.ConfigVersion(20), Ownership: smstate.Owned, SpecHash: "check-spec"},
			{Kind: "probe", Key: a.Probes[0].Key, ID: 11, Modified: 10, Ownership: smstate.Owned, SpecHash: "probe-spec"},
		}
		if err := provision(Binding(a), func(p *ProvisionState) error {
			p.Ownership = &smstate.OwnershipLedger{SchemaVersion: smstate.SchemaVersion, TargetFingerprint: a.TargetFingerprint, WrittenUnixMS: 1, Resources: resources}
			p.Registration = &smstate.Registration{SchemaVersion: smstate.ActivationSchemaVersion, SnapshotHash: a.SnapshotHash, TargetFingerprint: a.TargetFingerprint, SourceVersion: "a", WrittenUnixMS: 1, Resources: resources}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return a
	}
	return map[string]func(t *testing.T) custodyFixture{
		"backend": func(t *testing.T) custodyFixture {
			store, err := NewBackendStore(&memoryBackend{rev: "1"}, 5, "provision")
			if err != nil {
				t.Fatal(err)
			}
			a := seed(t, store, func(b SnapshotBinding, fn func(*ProvisionState) error) error {
				return store.UpdateProvision(ctx, b, fn)
			})
			return custodyFixture{store: store, target: a.TargetFingerprint, a: a,
				write: func(b CustodyBinding, fn func(*ProvisionState) error) error { return store.UpdateCustody(ctx, b, fn) }}
		},
		"file": func(t *testing.T) custodyFixture {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "runtime")
			store := NewFileStore(dir)
			acquired := func(write func(Store) error) error {
				owned, release, err := AcquireFileStore(dir)
				if err != nil {
					return err
				}
				return errors.Join(write(owned), release())
			}
			a := seed(t, store, func(b SnapshotBinding, fn func(*ProvisionState) error) error {
				return acquired(func(s Store) error { return s.UpdateProvision(ctx, b, fn) })
			})
			return custodyFixture{store: store, target: a.TargetFingerprint, a: a,
				write: func(b CustodyBinding, fn func(*ProvisionState) error) error {
					return acquired(func(s Store) error { return s.UpdateCustody(ctx, b, fn) })
				}}
		},
	}
}

func (f custodyFixture) load(t *testing.T) Document {
	t.Helper()
	d, err := f.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func deleteJournal(t *testing.T, target string, r smstate.Resource) smstate.Journal {
	t.Helper()
	j, err := smstate.NewDeleteJournal(smstate.DeleteIntent{SchemaVersion: smstate.SchemaVersion, TargetFingerprint: target, Kind: r.Kind, Key: r.Key,
		ID: r.ID, Modified: smstate.ConfigVersion(r.Modified), SpecHash: r.SpecHash}, "custody-marker", time.Unix(300, 0))
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestCustodyWriteBindsTargetAndGenerationWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	for name, build := range custodyFixtures(t) {
		t.Run(name, func(t *testing.T) {
			f := build(t)
			published := f.load(t)
			// While a snapshot is published, it alone binds writes.
			if err := f.write(Custody(f.target, published.Provision), func(*ProvisionState) error { return nil }); !errors.Is(err, ErrBinding) {
				t.Fatalf("custody write with a published snapshot = %v, want ErrBinding", err)
			}
			if err := f.store.PublishSnapshot(ctx, nil); err != nil {
				t.Fatal(err)
			}
			d := f.load(t)
			if d.Snapshot != nil || d.Provision.Ownership == nil || d.Provision.Registration == nil {
				t.Fatalf("unpublication changed custody: %+v", d)
			}
			bound := Custody(f.target, d.Provision)
			check := d.Provision.Ownership.Resources[0]
			for refusal, tc := range map[string]struct {
				binding CustodyBinding
				fn      func(*ProvisionState)
				want    error
			}{
				"another target":     {CustodyBinding{smstate.TargetFingerprint("https://sm.example", "other"), bound.Generation}, func(*ProvisionState) {}, ErrBinding},
				"stale generation":   {CustodyBinding{f.target, Generation(ProvisionState{})}, func(*ProvisionState) {}, ErrBinding},
				"empty binding":      {CustodyBinding{}, func(*ProvisionState) {}, ErrBinding},
				"keeps registration": {bound, func(*ProvisionState) {}, errCustodyWrite},
				"writes registration": {bound, func(p *ProvisionState) {
					r := *p.Registration
					r.WrittenUnixMS = 2
					p.Registration = &r
				}, errCustodyWrite},
				"adds a resource": {bound, func(p *ProvisionState) {
					p.Registration = nil
					extra := check
					extra.Key, extra.ID = "other", 99
					p.Ownership.Resources = append(p.Ownership.Resources, extra)
				}, errCustodyWrite},
				"changes a resource": {bound, func(p *ProvisionState) {
					p.Registration = nil
					p.Ownership.Resources[0].ID++
				}, errCustodyWrite},
				"retargets ownership": {bound, func(p *ProvisionState) {
					p.Registration = nil
					p.Ownership.TargetFingerprint = smstate.TargetFingerprint("https://sm.example", "other")
				}, errCustodyWrite},
				"writes adoption": {bound, func(p *ProvisionState) {
					p.Registration = nil
					p.Adoption = &smstate.AdoptionPreview{SchemaVersion: smstate.SchemaVersion, SnapshotHash: f.a.SnapshotHash, TargetFingerprint: f.target, SourceVersion: "a", PlanHash: "plan", WrittenUnixMS: 1}
				}, errCustodyWrite},
				"non-delete pending": {bound, func(p *ProvisionState) {
					p.Registration = nil
					spec := json.RawMessage(`{"job":"health"}`)
					p.Pending = &smstate.Journal{SchemaVersion: 1, Operation: "create", Kind: "check", Key: check.Key, SnapshotHash: "x", ExpectedHash: smstate.SpecHash(spec), ExpectedSpec: spec, StartedUnixMS: 1}
				}, errCustodyWrite},
				"intent for an unrecorded ID": {bound, func(p *ProvisionState) {
					p.Registration = nil
					unrecorded := check
					unrecorded.ID += 1000
					j := deleteJournal(t, f.target, unrecorded)
					p.Pending = &j
				}, errCustodyWrite},
				"intent for an unrecorded key": {bound, func(p *ProvisionState) {
					p.Registration = nil
					unrecorded := check
					unrecorded.Key = "unrecorded"
					j := deleteJournal(t, f.target, unrecorded)
					p.Pending = &j
				}, errCustodyWrite},
				"intent for a resource removed in the same write": {bound, func(p *ProvisionState) {
					p.Registration = nil
					p.Ownership.Resources = p.Ownership.Resources[1:]
					j := deleteJournal(t, f.target, check)
					p.Pending = &j
				}, errCustodyWrite},
				"foreign delete intent": {bound, func(p *ProvisionState) {
					p.Registration = nil
					j := deleteJournal(t, smstate.TargetFingerprint("https://sm.example", "other"), check)
					p.Pending = &j
				}, errCustodyWrite},
			} {
				if err := f.write(tc.binding, func(p *ProvisionState) error { tc.fn(p); return nil }); !errors.Is(err, tc.want) {
					t.Fatalf("%s: err = %v, want %v", refusal, err, tc.want)
				}
				if after := f.load(t); !reflect.DeepEqual(after, d) {
					t.Fatalf("%s: refused custody write changed state", refusal)
				}
			}

			// Durable intent: activation removed and the delete intent recorded in one write.
			var next CustodyBinding
			intent := deleteJournal(t, f.target, check)
			if err := f.write(bound, func(p *ProvisionState) error {
				p.Registration, p.Pending = nil, &intent
				next = Custody(f.target, *p)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			d = f.load(t)
			if d.Snapshot != nil || d.Provision.Registration != nil || d.Provision.Pending == nil || Custody(f.target, d.Provision) != next {
				t.Fatalf("intent write = %+v", d.Provision)
			}
			// The generation advanced: the pre-intent binding no longer writes.
			if err := f.write(bound, func(p *ProvisionState) error { p.Pending = nil; return nil }); !errors.Is(err, ErrBinding) {
				t.Fatalf("stale custody binding = %v, want ErrBinding", err)
			}
			// The live-apply CAS still refuses a changed snapshot while the intent is pending.
			if err := f.store.PublishSnapshotIfIdle(ctx, snapshot(t, "a")); !errors.Is(err, ErrProvisionBusy) {
				t.Fatalf("live publication with pending custody = %v, want ErrProvisionBusy", err)
			}
			// Release: the identity leaves custody and its intent clears together.
			if err := f.write(next, func(p *ProvisionState) error {
				p.Ownership.Resources, p.Pending = p.Ownership.Resources[1:], nil
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			d = f.load(t)
			if d.Snapshot != nil || d.Provision.Pending != nil || d.Provision.Registration != nil || len(d.Provision.Ownership.Resources) != 1 || d.Provision.Ownership.Resources[0].Kind != "probe" {
				t.Fatalf("release = %+v", d.Provision)
			}
		})
	}
}

func TestUnacquiredFileStoreRefusesCustodyWrite(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = NewFileStore(filepath.Join(root, "runtime")).UpdateCustody(context.Background(), CustodyBinding{"t", "g"}, func(*ProvisionState) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("unacquired custody write = %v called=%t", err, called)
	}
}
