// SPDX-License-Identifier: AGPL-3.0-only
package smstore

import (
	"context"
	"errors"
	"fmt"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/smstate"
	"time"
)

type backendStore struct {
	backend  control.StateBackend
	attempts int
	lease    string
}

func NewBackendStore(b control.StateBackend, attempts int, lease string) (Store, error) {
	if b == nil || attempts < 1 || attempts > 5 || lease == "" {
		return nil, fmt.Errorf("invalid SM store mapping or CAS budget")
	}
	return &backendStore{b, attempts, lease}, nil
}
func (s *backendStore) Load(ctx context.Context) (Document, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	snap, err := s.backend.Load(ctx, control.SMState)
	if err != nil {
		return Document{}, err
	}
	return decode(snap.Data, s.lease)
}
func (s *backendStore) update(ctx context.Context, fn func(*Document) error) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for i := 0; i < s.attempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		snap, err := s.backend.Load(ctx, control.SMState)
		if err != nil {
			return err
		}
		d, err := decode(snap.Data, s.lease)
		if err != nil {
			return err
		}
		if err := fn(&d); err != nil {
			return err
		}
		data, err := encode(d, s.lease)
		if err != nil {
			return err
		}
		_, err = s.backend.CompareAndSwap(ctx, control.SMState, snap.Revision, data)
		if errors.Is(err, control.ErrConflict) {
			continue
		}
		return err
	}
	return control.ErrConflict
}
func (s *backendStore) PublishSnapshot(ctx context.Context, snapshot *smstate.Snapshot) error {
	// Copy once: the replay changes only Snapshot, retaining every provision field.
	if snapshot != nil {
		if err := smstate.ValidateSnapshot(*snapshot); err != nil {
			return err
		}
	}
	snapshot = clone(snapshot)
	return s.update(ctx, func(d *Document) error { d.Snapshot = clone(snapshot); return nil })
}
func (s *backendStore) PublishSnapshotIfIdle(ctx context.Context, snapshot smstate.Snapshot) error {
	if err := smstate.ValidateSnapshot(snapshot); err != nil {
		return err
	}
	snapshot = clone(snapshot)
	err := s.update(ctx, func(d *Document) error {
		if err := idlePublication(*d, snapshot); err != nil {
			return err
		}
		next := clone(snapshot)
		d.Snapshot = &next
		return nil
	})
	if errors.Is(err, errUnchanged) {
		return nil
	}
	return err
}
func (s *backendStore) UpdateProvision(ctx context.Context, b SnapshotBinding, fn func(*ProvisionState) error) error {
	return s.update(ctx, func(d *Document) error {
		if err := checkBinding(*d, b); err != nil {
			return err
		}
		p := clone(d.Provision)
		if err := fn(&p); err != nil {
			return err
		}
		d.Provision = p
		return nil
	})
}
func (s *backendStore) UpdateCustody(ctx context.Context, b CustodyBinding, fn func(*ProvisionState) error) error {
	return s.update(ctx, func(d *Document) error {
		if err := checkCustody(*d, b); err != nil {
			return err
		}
		p := clone(d.Provision)
		if err := fn(&p); err != nil {
			return err
		}
		if err := validateCustodyWrite(d.Provision, p, b.TargetFingerprint); err != nil {
			return err
		}
		d.Provision = p
		return nil
	})
}
