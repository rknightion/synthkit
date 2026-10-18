// SPDX-License-Identifier: AGPL-3.0-only
package smstore

import (
	"context"
	"errors"
	"github.com/rknightion/synthkit/internal/smstate"
	"os"
	"path/filepath"
	"reflect"
	"sync"
)

type fileStore struct{ dir string }

func NewFileStore(dir string) Store { return &fileStore{dir} }

// AcquireFileStore positively acquires the private O_EXCL lock for a complete
// provisioning operation. The returned store supports Load and UpdateProvision
// only while owned and never supports PublishSnapshot, including after release.
// Use NewFileStore for emitter publication. An existing lock file is never
// treated as evidence of ownership.
func AcquireFileStore(dir string) (Store, func() error, error) {
	release, err := smstate.AcquireLock(dir)
	if err != nil {
		return nil, nil, err
	}
	s := &lockedFileStore{fileStore: &fileStore{dir: dir}, active: true}
	return s, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.active {
			return errors.New("SM file store lock already released")
		}
		s.active = false
		return release()
	}, nil
}

type lockedFileStore struct {
	*fileStore
	mu     sync.Mutex
	active bool
}

// PublishSnapshot is an emitter capability, unavailable on a provisioner handle.
// Reject it without inspecting files or attempting to reacquire the lock.
func (s *lockedFileStore) PublishSnapshot(context.Context, *smstate.Snapshot) error {
	return errors.New("SM acquired provisioner file store cannot publish snapshots")
}

func (s *lockedFileStore) PublishSnapshotIfIdle(context.Context, smstate.Snapshot) error {
	return errors.New("SM acquired provisioner file store cannot publish snapshots")
}

func (s *lockedFileStore) Load(ctx context.Context) (Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return Document{}, errors.New("SM file store lock is not owned")
	}
	return s.loadUnlocked(ctx)
}

func (s *lockedFileStore) UpdateProvision(ctx context.Context, b SnapshotBinding, fn func(*ProvisionState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return errors.New("SM file store lock is not owned")
	}
	return s.updateUnlocked(ctx, b, fn)
}

func (s *lockedFileStore) UpdateCustody(ctx context.Context, b CustodyBinding, fn func(*ProvisionState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return errors.New("SM file store lock is not owned")
	}
	return s.writeUnlocked(ctx, func(d Document) error { return checkCustody(d, b) }, fn,
		func(before, after ProvisionState) error {
			return validateCustodyWrite(before, after, b.TargetFingerprint)
		})
}

func (s *fileStore) UpdateProvision(context.Context, SnapshotBinding, func(*ProvisionState) error) error {
	return errors.New("SM provision update requires a positively acquired file store")
}

func (s *fileStore) UpdateCustody(context.Context, CustodyBinding, func(*ProvisionState) error) error {
	return errors.New("SM provision update requires a positively acquired file store")
}
func optional[T any](read func() (T, error)) (*T, error) {
	v, err := read()
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// Load holds the same O_EXCL lock as provisioning across the entire activation
// snapshot. Publication's shorter lock cannot protect these subsequent reads.
func (s *fileStore) Load(ctx context.Context) (out Document, err error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	owned, release, err := AcquireFileStore(s.dir)
	if err != nil {
		return Document{}, err
	}
	defer func() { err = errors.Join(err, release()) }()
	return owned.Load(ctx)
}

func (s *fileStore) loadUnlocked(ctx context.Context) (Document, error) {
	d := Document{SchemaVersion: 1}
	var err error
	// Pending is read first and every unreadable/corrupt artifact fails closed.
	d.Provision.Pending, err = optional(func() (smstate.Journal, error) { return smstate.ReadJournal(s.dir) })
	if err != nil {
		return d, err
	}
	// Honor cancellation before reading the remaining private evidence.
	if err := ctx.Err(); err != nil {
		return d, err
	}
	d.Snapshot, err = optional(func() (smstate.Snapshot, error) { return smstate.ReadSnapshot(s.dir) })
	if err != nil {
		return d, err
	}
	d.Registered, err = optional(func() (smstate.Snapshot, error) { return smstate.ReadRegisteredSnapshot(s.dir) })
	if err != nil {
		return d, err
	}
	d.Provision.Ownership, err = optional(func() (smstate.OwnershipLedger, error) { return smstate.ReadOwnershipState(s.dir) })
	if err != nil {
		return d, err
	}
	d.Provision.Registration, err = optional(func() (smstate.Registration, error) { return smstate.ReadRegistrationEvidence(s.dir) })
	if err != nil {
		return d, err
	}
	d.Provision.Adoption, err = optional(func() (smstate.AdoptionPreview, error) { return smstate.ReadAdoptionPreview(s.dir) })
	if err != nil {
		return d, err
	}
	d.Provision.Migration, err = optional(func() (smstate.MigrationPreview, error) { return smstate.ReadMigrationPreview(s.dir) })
	return d, err
}
func (s *fileStore) PublishSnapshot(ctx context.Context, snapshot *smstate.Snapshot) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Absent activation is a true no-op, not a reason to manufacture a private
	// runtime directory on credential-free/no-selection boots. Any actual removal
	// still takes the same lock as provisioning and never touches v1 evidence.
	if snapshot == nil {
		absent := true
		for _, name := range []string{smstate.SnapshotFilename, smstate.RegisteredFilename} {
			if _, err := os.Lstat(filepath.Join(s.dir, name)); err == nil {
				absent = false
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if absent {
			return nil
		}
	}
	release, err := smstate.AcquireLock(s.dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	if snapshot == nil {
		if err := smstate.RemoveRegisteredSnapshot(s.dir); err != nil {
			return err
		}
		return smstate.RemoveSnapshot(s.dir)
	}
	if err := smstate.ValidateSnapshot(*snapshot); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.publishUnlocked(s.retentionInputs(), *snapshot)
}

// retentionInputs reads only what boot publication needs to choose a retained registered
// snapshot. An unreadable or invalid artifact is no retention candidate, never an error: boot
// publication overwrites the published snapshot (and removes any retained one) exactly as it did
// before retention existed, and Load still fails closed on whatever remains invalid.
func (s *fileStore) retentionInputs() Document {
	var d Document
	if v, err := smstate.ReadSnapshot(s.dir); err == nil {
		d.Snapshot = &v
	}
	if v, err := smstate.ReadRegisteredSnapshot(s.dir); err == nil {
		d.Registered = &v
	}
	if v, err := smstate.ReadRegistrationEvidence(s.dir); err == nil {
		d.Provision.Registration = &v
	}
	return d
}

// publishUnlocked writes the retained registered snapshot (or removes a stale one) before the
// published snapshot, under the provisioning lock. Either crash point is fail-closed: admission
// only uses a retained snapshot the stored registration validates exactly against.
func (s *fileStore) publishUnlocked(d Document, snapshot smstate.Snapshot) error {
	if retained := retainRegistered(d, &snapshot); retained != nil {
		if d.Registered == nil || d.Registered.SnapshotHash != retained.SnapshotHash {
			if err := smstate.WriteRegisteredSnapshot(s.dir, *retained); err != nil {
				return err
			}
		}
	} else if err := smstate.RemoveRegisteredSnapshot(s.dir); err != nil {
		return err // also removes an unreadable retained file, which is no candidate
	}
	return smstate.WriteSnapshot(s.dir, snapshot)
}

// PublishSnapshotIfIdle holds the provisioning O_EXCL lock (a held lock fails like Load), refuses
// while a pending journal exists and skips the write when the published hash is unchanged.
func (s *fileStore) PublishSnapshotIfIdle(ctx context.Context, snapshot smstate.Snapshot) (err error) {
	if err := smstate.ValidateSnapshot(snapshot); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	release, err := smstate.AcquireLock(s.dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	d, err := s.loadUnlocked(ctx)
	if err != nil {
		return err
	}
	switch err := idlePublication(d, snapshot); {
	case errors.Is(err, errUnchanged):
		return nil
	case err != nil:
		return err
	}
	return s.publishUnlocked(d, snapshot)
}

// UpdateProvision requires the command's complete-operation O_EXCL file lock.
// Each private artifact retains its atomic rename/fsync codec. Pending is persisted
// and activation removed before remote admission; clearing pending is always last.
func (s *fileStore) updateUnlocked(ctx context.Context, b SnapshotBinding, fn func(*ProvisionState) error) error {
	return s.writeUnlocked(ctx, func(d Document) error { return checkBinding(d, b) }, fn, nil)
}

// writeUnlocked is the shared file write: bind checks the loaded document and admit, when set,
// checks the result against the loaded provision state before any artifact is written.
func (s *fileStore) writeUnlocked(ctx context.Context, bind func(Document) error, fn func(*ProvisionState) error, admit func(before, after ProvisionState) error) error {
	d, err := s.loadUnlocked(ctx)
	if err != nil {
		return err
	}
	if err := bind(d); err != nil {
		return err
	}
	before := clone(d.Provision)
	after := clone(before)
	if err := fn(&after); err != nil {
		return err
	}
	if err := validateProvision(after); err != nil {
		return err
	}
	if admit != nil {
		if err := admit(before, after); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if staleRegistered(d.Registered, after) {
		if err := smstate.RemoveRegisteredSnapshot(s.dir); err != nil {
			return err
		}
	}
	if after.Pending != nil && !reflect.DeepEqual(before.Pending, after.Pending) {
		if err := smstate.WriteJournal(s.dir, *after.Pending); err != nil {
			return err
		}
	}
	if before.Registration != nil && after.Registration == nil {
		if err := smstate.RemoveRegistration(s.dir); err != nil {
			return err
		}
	}
	if after.Ownership != nil && !reflect.DeepEqual(before.Ownership, after.Ownership) {
		if err := smstate.WriteOwnership(s.dir, *after.Ownership); err != nil {
			return err
		}
	}
	if after.Registration != nil && !reflect.DeepEqual(before.Registration, after.Registration) {
		if err := smstate.WriteRegistration(s.dir, *after.Registration); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(before.Adoption, after.Adoption) {
		if after.Adoption == nil {
			err = smstate.RemoveAdoptionPreview(s.dir)
		} else {
			err = smstate.WriteAdoptionPreview(s.dir, *after.Adoption)
		}
		if err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(before.Migration, after.Migration) {
		if after.Migration == nil {
			err = smstate.RemoveMigrationPreview(s.dir)
		} else {
			err = smstate.WriteMigrationPreview(s.dir, *after.Migration)
		}
		if err != nil {
			return err
		}
	}
	if before.Pending != nil && after.Pending == nil {
		return smstate.RemoveJournal(s.dir)
	}
	return nil
}
