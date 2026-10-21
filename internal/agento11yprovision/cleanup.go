// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision

import (
	"context"
	"crypto/rand"
	"slices"
	"time"
)

func engineOverlaps(a, b []string) bool {
	for _, v := range a {
		if slices.Contains(b, v) {
			return true
		}
	}
	return false
}
func engineVerifyCustody(ctx context.Context, c *Client, p enginePlan, s Snapshot, inv engineInventory, except string) error {
	for _, r := range p.plan.input.Resources {
		if r.Key == except {
			continue
		}
		record := engineRecord(s, r.Key)
		if record == nil || len(record.Acquisition) == 0 {
			return ErrConflict
		}
		o, _, e := engineReadExact(ctx, c, p, inv, record.Coordinate)
		if e != nil {
			return e
		}
		if record.Status == ResourceRemoved {
			if o.Presence != PresenceAbsent {
				return ErrConflict
			}
		} else if o.Presence == PresencePresent && o.ManagedHash != record.ManagedHash {
			return ErrConflict
		}
	}
	return engineVerifyPhases(p, s, inv, "")
}
func engineCleanupPreflight(ctx context.Context, c *Client, p enginePlan, s Snapshot, inv engineInventory) error {
	if s.Document == nil {
		return ErrEnrolledMissing
	}
	for _, historical := range s.Document.Resources {
		if !isConsumer(historical.Coordinate.Kind) || len(historical.Acquisition) == 0 || !engineOverlaps(historical.Namespaces, p.plan.names) {
			continue
		}
		found := false
		for _, r := range p.plan.input.Resources {
			if r.Key == historical.Key && r.Coordinate == historical.Coordinate {
				found = true
			}
		}
		if !found {
			return ErrConflict
		}
	}
	for _, r := range p.plan.input.Resources {
		record := engineRecord(s, r.Key)
		if record == nil || len(record.Acquisition) == 0 || record.Coordinate != r.Coordinate || record.Authored.Digest() != r.Spec.Digest() || record.Origin != r.Origin || !slices.Equal(record.Namespaces, r.Namespaces) || !slices.Equal(record.Dependencies, r.Dependencies) {
			return ErrConflict
		}
	}
	// A foreign action under an owned rule is not a deletion target. The caller
	// must request a separately approved deactivation-only plan rather than hide
	// that retention dependency in a parent deletion request.
	for _, step := range p.plan.input.Steps {
		r := planResource(p.plan.input, step.ResourceKey)
		if step.Operation != OpDelete || r.Coordinate.Kind != KindRule {
			continue
		}
		for _, action := range inv.Actions[r.Coordinate.ID] {
			owned := false
			for _, record := range s.Document.Resources {
				if record.Coordinate.Kind == KindAction && record.Coordinate.ID == action.ActionID && record.Coordinate.ParentID == r.Coordinate.ID && len(record.Acquisition) > 0 {
					owned = true
				}
			}
			if !owned {
				return ErrConflict
			}
		}
	}
	except := ""
	if s.Document.Pending != nil {
		except = s.Document.Pending.ResourceKey
	}
	return engineVerifyCustody(ctx, c, p, s, inv, except)
}
func engineConsumerBarrier(ctx context.Context, c *Client, p enginePlan, s Snapshot, inv engineInventory) error {
	if s.Document == nil {
		return ErrConflict
	}
	for _, record := range s.Document.Resources {
		if !isConsumer(record.Coordinate.Kind) || len(record.Acquisition) == 0 || !engineOverlaps(record.Namespaces, p.plan.names) {
			continue
		}
		o, spec, e := engineReadExact(ctx, c, p, inv, record.Coordinate)
		if e != nil {
			return e
		}
		if o.Presence == PresenceAbsent {
			continue
		}
		disabled := false
		switch record.Coordinate.Kind {
		case KindHook:
			disabled = spec.Hook != nil && spec.Hook.Enabled != nil && !*spec.Hook.Enabled
		case KindRule:
			disabled = spec.Rule != nil && spec.Rule.Enabled != nil && !*spec.Rule.Enabled
		case KindAction:
			disabled = spec.Action != nil && spec.Action.Enabled != nil && !*spec.Action.Enabled
		}
		if !disabled {
			return ErrConflict
		}
	}
	return nil
}
func engineCleanupBefore(ctx context.Context, c *Client, p enginePlan, s Snapshot, inv engineInventory, r PlannedResource, op Operation) (Coordinate, engineSpec, Digest, error) {
	record := engineRecord(s, r.Key)
	if record == nil || len(record.Acquisition) == 0 {
		return Coordinate{}, engineSpec{}, "", ErrConflict
	}
	co := record.Coordinate
	o, spec, e := engineReadExact(ctx, c, p, inv, co)
	if e != nil || o.Presence != PresencePresent || o.ManagedHash != record.ManagedHash {
		return co, spec, "", ErrConflict
	}
	if e = engineVerifyCustody(ctx, c, p, s, inv, ""); e != nil {
		return co, spec, "", e
	}
	switch op {
	case OpDisable:
		switch co.Kind {
		case KindHook:
			spec.Hook.Enabled = enginePtr(false)
		case KindRule:
			spec.Rule.Enabled = enginePtr(false)
		case KindAction:
			spec.Action.Enabled = enginePtr(false)
		default:
			return co, spec, "", ErrUnsupported
		}
	case OpDelete:
		if co.Kind != KindHook && co.Kind != KindRule && co.Kind != KindAction {
			return co, spec, "", ErrUnsupported
		}
		if e = engineConsumerBarrier(ctx, c, p, s, inv); e != nil {
			return co, spec, "", e
		}
	default:
		return co, spec, "", ErrUnsupported
	}
	return co, spec, o.ExactRead, nil
}
func engineClassifyCleanup(ctx context.Context, c *Client, locked *LockedState, p enginePlan, s Snapshot, auth *Authorization, id string, chain Digest) (Snapshot, Digest, error) {
	for _, r := range p.plan.input.Resources {
		inv, e := engineInventoryRead(ctx, c, p, s)
		if e != nil {
			return s, chain, e
		}
		if inv.Digest != chain {
			return s, chain, ErrConflict
		}
		record := engineRecord(s, r.Key)
		if record == nil {
			return s, chain, ErrConflict
		}
		o, _, e := engineReadExact(ctx, c, p, inv, record.Coordinate)
		if e != nil {
			return s, chain, e
		}
		reason := CleanupReason("")
		if o.Presence == PresenceAbsent {
			reason = CleanupAlreadyAbsent
		} else if !isConsumer(record.Coordinate.Kind) {
			if record.Coordinate.Kind == KindEvaluator || record.Coordinate.Kind == KindCollection {
				reason = CleanupRetainedDependency
			} else {
				reason = CleanupRetainedUnsupported
			}
		}
		if reason == "" {
			continue
		}
		classification := CleanupClassification{ResourceKey: r.Key, Coordinate: record.Coordinate, ExistingHead: *s.Base.Head, Reason: reason, Observation: &o, Inventory: inv.Digest, Evidence: o.Evidence}
		s, e = locked.Commit(ctx, *s.Base.Head, auth, Change{Kind: ChangeClassify, ExpectedInventory: chain, ConstructionID: id, Classification: &classification})
		if e != nil {
			return s, chain, e
		}
		chain = inv.Digest
	}
	return s, chain, nil
}
func engineCleanupReceipt(ctx context.Context, c *Client, p enginePlan, s Snapshot, id string, inv engineInventory, pv Preview, approval Approval) (Receipt, error) {
	if e := engineVerifyCustody(ctx, c, p, s, inv, ""); e != nil {
		return Receipt{}, e
	}
	if e := engineConsumerBarrier(ctx, c, p, s, inv); e != nil {
		return Receipt{}, e
	}
	construction := engineConstruction(s, id)
	if construction == nil {
		return Receipt{}, ErrConflict
	}
	members := []ReceiptMember{}
	allAbsent := true
	for _, r := range p.plan.input.Resources {
		record := engineRecord(s, r.Key)
		if record == nil {
			return Receipt{}, ErrConflict
		}
		o, _, e := engineReadExact(ctx, c, p, inv, record.Coordinate)
		if e != nil {
			return Receipt{}, e
		}
		outcome := MemberOutcome("")
		for _, cl := range construction.Classifications {
			if cl.ResourceKey == r.Key {
				outcome = MemberOutcome(cl.Reason)
			}
		}
		if outcome == "" {
			if record.Status == ResourceRemoved && o.Presence == PresenceAbsent {
				outcome = OutcomeRemoved
			} else if isConsumer(record.Coordinate.Kind) && o.Presence == PresencePresent {
				outcome = OutcomePresentDisabled
			} else {
				return Receipt{}, ErrConflict
			}
		}
		if o.Presence != PresenceAbsent {
			allAbsent = false
		}
		members = append(members, ReceiptMember{ResourceKey: r.Key, Coordinate: record.Coordinate, Outcome: outcome, Observation: &o, Evidence: o.Evidence})
	}
	result := ReceiptIncomplete
	if p.plan.input.Mode == ModeDeactivate {
		result = ReceiptDeactivated
	} else if allAbsent {
		result = ReceiptFullyRemoved
	}
	return Receipt{ID: "receipt-" + rand.Text(), ConstructionID: id, Mode: p.plan.input.Mode, Result: result, BindingDigest: custodyBindingHash(p.plan.input.Binding), Input: p.plan.Digest(), Preview: pv.Digest(), Inventory: inv.Digest, Namespaces: append([]string{}, p.plan.names...), Members: members, OperatorApproval: approval.hash, ObservedMS: time.Now().UnixMilli()}, nil
}
func cleanupEngine(ctx context.Context, c *Client, locked *LockedState, p enginePlan, pv Preview, approval Approval) (Receipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if p.plan.input.Mode != ModeDeactivate && p.plan.input.Mode != ModeTeardown {
		return Receipt{}, ErrInvalid
	}
	s, inv, auth, e := engineAuthorize(ctx, c, locked, p, pv, approval, p.plan.input.Mode)
	if e != nil {
		return Receipt{}, e
	}
	id := "cleanup-" + rand.Text()
	s, e = locked.Commit(ctx, *s.Base.Head, auth, Change{Kind: ChangeStart, ExpectedInventory: inv.Digest, ConstructionID: id})
	if e != nil {
		return Receipt{}, e
	}
	s, chain, e := engineClassifyCleanup(ctx, c, locked, p, s, auth, id, inv.Digest)
	if e != nil {
		return Receipt{}, e
	}
	receipt, e := engineContinue(ctx, c, locked, p, s, auth, id, chain, pv, approval)
	if e != nil {
		return receipt, e
	}
	if receipt.Result == ReceiptIncomplete {
		return receipt, ErrConflict
	}
	return receipt, nil
}
