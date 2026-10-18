// SPDX-License-Identifier: AGPL-3.0-only

// Package smstore owns bounded orchestration storage, not telemetry or API calls.
package smstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/smstate"
)

type ProvisionState struct {
	Ownership    *smstate.OwnershipLedger  `json:"ownership"`
	Registration *smstate.Registration     `json:"registration"`
	Adoption     *smstate.AdoptionPreview  `json:"adoption"`
	Migration    *smstate.MigrationPreview `json:"migration"`
	Pending      *smstate.Journal          `json:"pending"`
}
type Document struct {
	SchemaVersion      int               `json:"schema_version"`
	ProvisionLeaseName string            `json:"provision_lease_name"`
	Snapshot           *smstate.Snapshot `json:"snapshot"`
	Provision          ProvisionState    `json:"provision"`
}
type SnapshotBinding struct{ Hash, TargetFingerprint, SourceVersion string }

func Binding(s smstate.Snapshot) SnapshotBinding {
	return SnapshotBinding{s.SnapshotHash, s.TargetFingerprint, s.SourceVersion}
}

type Store interface {
	Load(context.Context) (Document, error)
	PublishSnapshot(context.Context, *smstate.Snapshot) error
	// PublishSnapshotIfIdle is the runtime (live-apply) publication: one compare-and-swap that
	// writes nothing when the published snapshot already has this hash and otherwise refuses with
	// ErrProvisionBusy while a provisioner mutation is pending, so it never rebinds an in-flight
	// apply.
	PublishSnapshotIfIdle(context.Context, smstate.Snapshot) error
	UpdateProvision(context.Context, SnapshotBinding, func(*ProvisionState) error) error
	// UpdateCustody is the cleanup-only write used when no snapshot is published. It is a
	// compare-and-swap on the recorded ownership target and the observed provision generation,
	// refuses once any snapshot is published, and admits only a cleanup-shaped result (see
	// validateCustodyWrite): it can never write a registration.
	UpdateCustody(context.Context, CustodyBinding, func(*ProvisionState) error) error
}

var ErrBinding = errors.New("SM snapshot binding changed or unavailable")

// ErrProvisionBusy reports a pending provisioner mutation; runtime publication waits for it.
var ErrProvisionBusy = errors.New("SM state busy: a provisioning mutation is pending")

// errUnchanged ends a runtime publication's CAS without a write.
var errUnchanged = errors.New("SM snapshot unchanged")

func idlePublication(d Document, snapshot smstate.Snapshot) error {
	if d.Snapshot != nil && d.Snapshot.SnapshotHash == snapshot.SnapshotHash {
		return errUnchanged // no write, so no rebinding; a pending journal still fails activation
	}
	if d.Provision.Pending != nil {
		return ErrProvisionBusy
	}
	return nil
}

func checkBinding(d Document, b SnapshotBinding) error {
	if d.Snapshot == nil || Binding(*d.Snapshot) != b {
		return ErrBinding
	}
	return nil
}
func validateProvision(p ProvisionState) error {
	if p.Ownership != nil {
		if err := smstate.ValidateOwnership(*p.Ownership); err != nil {
			return err
		}
	}
	if p.Registration != nil {
		if err := smstate.ValidateRegistrationState(*p.Registration); err != nil {
			return err
		}
	}
	if p.Adoption != nil {
		if err := smstate.ValidateAdoption(*p.Adoption); err != nil {
			return err
		}
	}
	if p.Migration != nil {
		if err := smstate.ValidateMigration(*p.Migration); err != nil {
			return err
		}
	}
	if p.Pending != nil {
		if err := smstate.ValidateJournal(*p.Pending); err != nil {
			return err
		}
	}
	return nil
}
func validate(d Document, lease string) error {
	if d.SchemaVersion != 1 || d.ProvisionLeaseName != lease || lease == "" {
		return fmt.Errorf("SM state envelope or provisioning Lease mismatch")
	}
	if d.Snapshot != nil {
		if err := smstate.ValidateSnapshot(*d.Snapshot); err != nil {
			return err
		}
	}
	return validateProvision(d.Provision)
}
func decode(data []byte, lease string) (Document, error) {
	if len(data) == 0 {
		return Document{SchemaVersion: 1, ProvisionLeaseName: lease}, nil
	}
	if len(data) > control.MaxStateDocumentBytes {
		return Document{}, fmt.Errorf("SM state exceeds byte cap")
	}
	var d Document
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return Document{}, fmt.Errorf("invalid SM state JSON")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return Document{}, fmt.Errorf("SM state trailing data")
	}
	if err := validate(d, lease); err != nil {
		return Document{}, err
	}
	return d, nil
}
func encode(d Document, lease string) ([]byte, error) {
	if err := validate(d, lease); err != nil {
		return nil, err
	}
	data, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if len(data) > control.MaxStateDocumentBytes {
		return nil, fmt.Errorf("SM state exceeds byte cap")
	}
	return data, nil
}

// Clone isolates caller-owned nested arrays and RawMessage before CAS replay.
func clone[T any](v T) T {
	data, err := json.Marshal(v)
	if err != nil {
		panic("SM store invalid clone input")
	}
	var out T
	if json.Unmarshal(data, &out) != nil {
		panic("SM store clone decode")
	}
	return out
}
