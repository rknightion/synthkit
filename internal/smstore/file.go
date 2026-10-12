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
// provisioning operation. The returned store cannot read or write after release;
// an existing lock file is never treated as evidence of ownership.
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

func (s *fileStore) UpdateProvision(context.Context, SnapshotBinding, func(*ProvisionState) error) error {
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
		if _, err := os.Lstat(filepath.Join(s.dir, smstate.SnapshotFilename)); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
	}
	release, err := smstate.AcquireLock(s.dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	if snapshot == nil {
		return smstate.RemoveSnapshot(s.dir)
	}
	return smstate.WriteSnapshot(s.dir, *snapshot)
}

// UpdateProvision requires the command's complete-operation O_EXCL file lock.
// Each private artifact retains its atomic rename/fsync codec. Pending is persisted
// and activation removed before remote admission; clearing pending is always last.
func (s *fileStore) updateUnlocked(ctx context.Context, b SnapshotBinding, fn func(*ProvisionState) error) error {
	d, err := s.loadUnlocked(ctx)
	if err != nil {
		return err
	}
	if err := checkBinding(d, b); err != nil {
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
	if err := ctx.Err(); err != nil {
		return err
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
