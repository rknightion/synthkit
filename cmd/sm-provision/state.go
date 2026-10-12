// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
	"os"
)

type boundedProvisionStore struct {
	smstore.Store
	bound ha.Bounded
}

func (s boundedProvisionStore) Load(ctx context.Context) (out smstore.Document, err error) {
	err = s.bound.Run(ctx, ha.Mutation, func(ctx context.Context) error { var err error; out, err = s.Store.Load(ctx); return err })
	return
}
func (s boundedProvisionStore) UpdateProvision(ctx context.Context, b smstore.SnapshotBinding, fn func(*smstore.ProvisionState) error) error {
	return s.bound.Run(ctx, ha.Mutation, func(ctx context.Context) error { return s.Store.UpdateProvision(ctx, b, fn) })
}
func (p *provisioner) update(fn func(*smstore.ProvisionState) error) error {
	return p.store.UpdateProvision(p.ctx, smstore.Binding(p.snapshot), fn)
}
func (p *provisioner) clearPending() error {
	return p.update(func(s *smstore.ProvisionState) error { s.Pending = nil; return nil })
}
func (p *provisioner) readMigration() (smstate.MigrationPreview, error) {
	d, err := p.store.Load(p.ctx)
	if err != nil {
		return smstate.MigrationPreview{}, err
	}
	if d.Provision.Migration == nil {
		return smstate.MigrationPreview{}, os.ErrNotExist
	}
	return *d.Provision.Migration, nil
}
func (p *provisioner) readAdoption() (smstate.AdoptionPreview, error) {
	d, err := p.store.Load(p.ctx)
	if err != nil {
		return smstate.AdoptionPreview{}, err
	}
	if d.Provision.Adoption == nil {
		return smstate.AdoptionPreview{}, os.ErrNotExist
	}
	return *d.Provision.Adoption, nil
}
