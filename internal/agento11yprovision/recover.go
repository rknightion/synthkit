// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision

import (
	"context"
	"encoding/json"
	"time"
)

// Recovery never calls a mutation for an already attempted step. Attribution
// references are explicitly reviewed operator evidence, not inferred from a
// matching name, an inventory difference, cancellation or current absence.
func recoverEngine(ctx context.Context, c *Client, locked *LockedState, p enginePlan, pv Preview, approval Approval) (Receipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	s, inv, auth, e := engineAuthorize(ctx, c, locked, p, pv, approval, ModeRecover)
	if e != nil {
		return Receipt{}, e
	}
	id := approval.document.ConstructionID
	construction := engineConstruction(s, id)
	if construction == nil || construction.Input != p.plan.Digest() {
		return Receipt{}, ErrConflict
	}
	if s.Document.Pending == nil {
		s, e = locked.Commit(ctx, *s.Base.Head, auth, Change{Kind: ChangeStart, ExpectedInventory: inv.Digest, ConstructionID: id})
		if e != nil {
			return Receipt{}, e
		}
		receipt, err := engineContinue(ctx, c, locked, p, s, auth, id, inv.Digest, pv, approval)
		// Recovery preserves the ORIGINAL mode's completion semantics as well as
		// its plan/id: retained teardown is never a zero-error full teardown.
		if err == nil && receipt.Result == ReceiptIncomplete {
			return receipt, ErrConflict
		}
		return receipt, err
	}
	pending := s.Document.Pending
	rec := approval.document.Recovery
	if rec == nil || rec.AttemptID != pending.AttemptID || len(rec.Attribution) == 0 {
		return Receipt{}, ErrApproval
	}
	change := Change{Kind: ChangeResolve, ExpectedInventory: inv.Digest, ConstructionID: id, AttemptID: pending.AttemptID}
	for _, step := range construction.Steps {
		if step.AttemptID == pending.AttemptID {
			change.Step = step.Number
		}
	}
	if change.Step == 0 {
		return Receipt{}, ErrConflict
	}
	if rec.Disposition == DispositionNotApplied {
		// No negative reads are converted to noncommit evidence. The existing B
		// exact recovery approval checks independently bind the reviewed references.
		_, e = locked.Commit(ctx, *s.Base.Head, auth, change)
		if e != nil {
			return Receipt{}, e
		}
		return Receipt{}, ErrConflict // fresh approval is required for any suffix
	}
	r := planResource(p.plan.input, pending.ResourceKey)
	if r == nil {
		return Receipt{}, ErrConflict
	}
	expected, e := engineResolved(p, *r, s)
	if e != nil {
		return Receipt{}, e
	}
	if pending.Operation == OpDisable || pending.Operation == OpDelete {
		record := engineRecord(s, r.Key)
		disabled := pending.Operation == OpDisable || record != nil && record.Status == ResourceDisabled
		if disabled {
			if expected.Hook != nil {
				expected.Hook.Enabled = enginePtr(false)
			}
			if expected.Rule != nil {
				expected.Rule.Enabled = enginePtr(false)
			}
			if expected.Action != nil {
				expected.Action.Enabled = enginePtr(false)
			}
		}
	}
	raw, e := json.Marshal(enginePayload(expected, r.Coordinate.Kind))
	if e != nil {
		return Receipt{}, ErrInvalid
	}
	desired, e := NewAuthoredSpec(raw)
	if e != nil || desired.Digest() != pending.Desired.Digest() {
		return Receipt{}, ErrConflict
	}
	o, _, e := engineReadExact(ctx, c, p, inv, rec.Coordinate)
	if e != nil {
		return Receipt{}, e
	}
	if pending.Operation == OpDelete {
		if o.Presence != PresenceAbsent {
			return Receipt{}, ErrConflict
		}
	} else {
		want, e := engineDigest(p.plan.input.Binding, "managed", rec.Coordinate, expected)
		if e != nil || o.Presence != PresencePresent || want != o.ManagedHash {
			return Receipt{}, ErrConflict
		}
	}
	if p.plan.input.Mode == ModeApply {
		if e = engineVerifyPhases(p, s, inv, r.Key, rec.Coordinate); e != nil {
			return Receipt{}, e
		}
	} else if e = engineVerifyCustody(ctx, c, p, s, inv, r.Key); e != nil {
		return Receipt{}, e
	}
	if o.ManagedHash != rec.ExpectedManagedHash || o.ExactRead != rec.ExactRead || o.Inventory != rec.Inventory {
		return Receipt{}, ErrApproval
	}
	change.Observation = &o
	_, e = locked.Commit(ctx, *s.Base.Head, auth, change)
	if e != nil {
		return Receipt{}, e
	}
	return Receipt{}, ErrConflict // observation settled, not authority to resume/replay
}
