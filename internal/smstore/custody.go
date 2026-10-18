// SPDX-License-Identifier: AGPL-3.0-only
package smstore

import (
	"errors"
	"reflect"

	"github.com/rknightion/synthkit/internal/smstate"
)

// CustodyBinding binds a cleanup-only provision write when no snapshot is published (SM
// startup-disabled or deselected). It names the recorded ownership target (destination and
// credential fingerprint) and the generation of the exact provision state the writer last
// observed, so every write is a compare-and-swap on custody rather than on a live snapshot.
type CustodyBinding struct{ TargetFingerprint, Generation string }

// Generation is the content hash of a complete provision state. Any change to ownership,
// registration, adoption, migration or pending evidence changes it.
func Generation(p ProvisionState) string {
	return smstate.SpecHash(struct {
		Schema    string         `json:"schema"`
		Provision ProvisionState `json:"provision"`
	}{"synthkit-sm-custody-generation-v1", p})
}

// Custody returns the binding for target over the provision state p as observed.
func Custody(target string, p ProvisionState) CustodyBinding {
	return CustodyBinding{TargetFingerprint: target, Generation: Generation(p)}
}

// checkCustody admits a custody write only while no snapshot is published (a published snapshot
// governs through SnapshotBinding instead), the recorded ledger is bound to the binding's target,
// and the provision state is exactly the generation the writer observed.
func checkCustody(d Document, b CustodyBinding) error {
	if b.TargetFingerprint == "" || b.Generation == "" || d.Snapshot != nil || d.Provision.Ownership == nil ||
		d.Provision.Ownership.TargetFingerprint != b.TargetFingerprint || Generation(d.Provision) != b.Generation {
		return ErrBinding
	}
	return nil
}

// errCustodyWrite rejects a custody write that is not cleanup-only.
var errCustodyWrite = errors.New("SM custody write is limited to cleanup of recorded ownership")

// validateCustodyWrite limits a custody write to cleanup: it may remove recorded resources, set or
// clear a delete intent bound to the same target, and remove activation. It can never write a
// registration, so it never authorises SM emission, and it never touches adoption or migration.
func validateCustodyWrite(before, after ProvisionState, target string) error {
	if after.Registration != nil || after.Ownership == nil || after.Ownership.TargetFingerprint != target ||
		after.Ownership.SchemaVersion != before.Ownership.SchemaVersion ||
		!reflect.DeepEqual(before.Adoption, after.Adoption) || !reflect.DeepEqual(before.Migration, after.Migration) {
		return errCustodyWrite
	}
	recorded := make(map[string]smstate.Resource, len(before.Ownership.Resources))
	for _, r := range before.Ownership.Resources {
		recorded[r.Kind+"\x00"+r.Key] = r
	}
	kept := make(map[string]smstate.Resource, len(after.Ownership.Resources))
	for _, r := range after.Ownership.Resources {
		if prior, ok := recorded[r.Kind+"\x00"+r.Key]; !ok || prior != r {
			return errCustodyWrite
		}
		kept[r.Kind+"\x00"+r.Key] = r
	}
	if after.Pending != nil {
		intent, err := smstate.DeleteIntentFromJournal(*after.Pending)
		if err != nil || intent.TargetFingerprint != target {
			return errCustodyWrite
		}
		// A new or changed intent must name an identity recorded before the write and still
		// recorded after it. An unchanged intent may outlive its ledger entry: a release that
		// committed the removal before clearing the intent is resolved by observed absence.
		if !reflect.DeepEqual(before.Pending, after.Pending) {
			key := intent.Kind + "\x00" + intent.Key
			prior, wasRecorded := recorded[key]
			_, stillRecorded := kept[key]
			if !wasRecorded || !stillRecorded || prior.ID != intent.ID {
				return errCustodyWrite
			}
		}
	}
	return nil
}
