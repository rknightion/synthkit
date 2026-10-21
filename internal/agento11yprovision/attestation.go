// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
)

func custodyEvidence(es []Evidence, required bool) bool {
	if len(es) > 16 || required && len(es) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, e := range es {
		if !custodyText(e.ID, false) || strings.ContainsAny(e.ID, "/\\") || strings.Contains(e.ID, "://") || !custodyDigest(e.Digest) || seen[e.ID] {
			return false
		}
		seen[e.ID] = true
	}
	return true
}
func custodyObservation(o Observation) bool {
	return custodyCoordinate(o.Coordinate, false, "") && custodyDigest(o.ExactRead) && custodyDigest(o.Inventory) && custodyText(o.ServerRevision, true) && custodyEvidence(o.Evidence, true) &&
		(o.Presence == PresencePresent && custodyDigest(o.ManagedHash) || o.Presence == PresenceAbsent && o.ManagedHash == "" && o.ServerRevision == "")
}
func custodyAssertions(a Assertions) bool {
	if !a.AuthorizedOperator || !a.EffectiveDestination || !a.AllAffectedEmittersStopped || !a.KeepStoppedUntilReceiptAccepted || !a.ExclusiveNamespace || !a.OutstandingWorkMayFinish || !a.NonAtomicInitialPublication || !a.PermanentRecordsMayRemain || !a.NoSyntheticReentry {
		return false
	}
	for _, es := range [][]Evidence{a.DestinationEvidence, a.RuntimeEvidence, a.StopEvidence, a.NamespaceEvidence} {
		if !custodyEvidence(es, true) {
			return false
		}
	}
	seen := map[Coordinate]bool{}
	for _, f := range a.Foreign {
		if !(f.Coordinate.Kind == KindHook || f.Coordinate.Kind == KindRule) || !custodyCoordinate(f.Coordinate, false, "") || !custodyDigest(f.ManagedHash) || f.Reason != "controlled_real_only" || !custodyEvidence(f.Evidence, true) || seen[f.Coordinate] {
			return false
		}
		seen[f.Coordinate] = true
	}
	return true
}
func assertionsFromWire(a assertionsWire) Assertions {
	f := []ForeignCoverage{}
	for _, x := range a.Foreign {
		f = append(f, ForeignCoverage{Coordinate(x.Coordinate), x.ManagedHash, x.Reason, evidenceFromWire(x.Evidence)})
	}
	return Assertions{a.AuthorizedOperator, a.EffectiveDestination, a.AllAffectedEmittersStopped, a.KeepStoppedUntilReceiptAccepted, a.ExclusiveNamespace, a.OutstandingWorkMayFinish, a.NonAtomicInitialPublication, a.PermanentRecordsMayRemain, a.NoSyntheticReentry, evidenceFromWire(a.DestinationEvidence), evidenceFromWire(a.RuntimeEvidence), evidenceFromWire(a.StopEvidence), evidenceFromWire(a.NamespaceEvidence), f}
}
func DecodeApproval(raw []byte) (Approval, error) {
	var w approvalWire
	if e := custodyDecode(raw, &w, custodySpecBytes); e != nil {
		return Approval{}, ErrApproval
	}
	a := ApprovalDocument{w.Schema, w.ApprovalID, w.Operator, w.ConstructionID, w.IssuedMS, w.ExpiresMS, bindingFromWire(w.Binding), w.Input, w.Preview, w.Inventory, baseFromWire(w.Base), w.Mode, append([]string{}, w.Namespaces...), assertionsFromWire(w.Assertions), nil}
	if r := w.Recovery; r != nil {
		a.Recovery = &Recovery{r.AttemptID, r.OriginalInput, r.OriginalPreview, r.OriginalApproval, r.ResourceKey, Coordinate(r.Coordinate), r.Operation, r.AuthoredHash, r.ExpectedManagedHash, r.ExactRead, r.Inventory, evidenceFromWire(r.Attribution), r.Disposition}
	}
	if a.Schema != 1 || !custodyText(a.ApprovalID, false) || !custodyText(a.Operator, false) || !custodyText(a.ConstructionID, true) || !custodyBinding(a.Binding) || !custodyBase(a.Base) || !custodyDigest(a.Input) || !custodyDigest(a.Preview) || !custodyDigest(a.Inventory) || !custodyAssertions(a.Assertions) || a.IssuedMS <= 0 || a.ExpiresMS <= a.IssuedMS || a.ExpiresMS-a.IssuedMS > int64(15*time.Minute/time.Millisecond) {
		return Approval{}, ErrApproval
	}
	if !(a.Mode == ModeApply || a.Mode == ModeRecover || a.Mode == ModeDeactivate || a.Mode == ModeTeardown) || !custodyNamespaces(a.Namespaces) {
		return Approval{}, ErrApproval
	}
	if r := a.Recovery; r != nil {
		if a.Mode != ModeRecover || a.ConstructionID == "" || !custodyText(r.AttemptID, false) || !custodyText(r.ResourceKey, false) || !custodyCoordinate(r.Coordinate, r.Disposition == DispositionNotApplied, "") || !custodyDigest(r.OriginalInput) || !custodyDigest(r.OriginalPreview) || !custodyDigest(r.OriginalApproval) || !custodyDigest(r.AuthoredHash) || !custodyDigest(r.ExactRead) || !custodyDigest(r.Inventory) || !custodyEvidence(r.Attribution, true) || custodyMethod(r.Coordinate.Kind, r.Operation) == "" || !(r.Disposition == DispositionObserved || r.Disposition == DispositionNotApplied) {
			return Approval{}, ErrApproval
		}
		if !custodyDigest(r.ExpectedManagedHash) && !(r.ExpectedManagedHash == "" && r.Operation == OpDelete && r.Disposition == DispositionObserved) {
			return Approval{}, ErrApproval
		}
	}
	b, e := json.Marshal(w)
	if e != nil {
		return Approval{}, ErrApproval
	}
	return Approval{a, custodyHash("approval", b)}, nil
}
func custodyNamespaces(ns []string) bool {
	if ns == nil || !slices.IsSorted(ns) {
		return false
	}
	for i, n := range ns {
		if !custodyName(n) || n != strings.ToLower(n) || i > 0 && ns[i-1] == n {
			return false
		}
	}
	return true
}
func custodyCheckApproval(a Approval, p Preview, base Base, inventory Digest, now time.Time) error {
	d := a.document
	if !custodyDigest(a.hash) || d.Binding != p.plan.input.Binding || d.Input != p.plan.hash || d.Preview != p.hash || d.Inventory != p.inventory || inventory != p.inventory || d.Mode != p.mode || !custodySameBase(d.Base, base) || !custodySameBase(p.base, base) || !slices.Equal(d.Namespaces, p.plan.names) {
		return ErrApproval
	}
	n := now.UnixMilli()
	if n < d.IssuedMS || n >= d.ExpiresMS {
		return ErrExpired
	}
	return nil
}

// Full reload checks are deliberately independent of transition generation. A
// checksum certifies bytes, not that a partially decoded ledger is safe to use.
func validateCustodyDocument(d Document) error {
	if d.Schema != 1 || !custodyBinding(d.Binding) || d.Generation == 0 || !custodyDigest(d.Checksum) {
		return ErrCorrupt
	}
	if len(d.Resources) > custodyMaxResources || len(d.Reservations) > custodyMaxClaims {
		return ErrCapacity
	}
	resources := map[string]ResourceRecord{}
	identities := map[string]bool{}
	claims := map[string]Reservation{}
	consumed := map[Digest]ConsumedApproval{}
	approvalIDs := map[string]bool{}
	constructs := map[string]Construction{}
	attempts := map[string]string{}
	acquisitions := map[string]bool{}
	inflight := 0
	for _, a := range d.Consumed {
		if !custodyText(a.ID, false) || !custodyDigest(a.Digest) || !custodyText(a.ConstructionID, false) || !custodyBase(a.Base) || approvalIDs[a.ID] {
			return ErrCorrupt
		}
		if _, ok := consumed[a.Digest]; ok {
			return ErrCorrupt
		}
		if a.Base.Head != nil && a.Base.Head.Generation >= d.Generation {
			return ErrCorrupt
		}
		approvalIDs[a.ID] = true
		consumed[a.Digest] = a
	}
	for _, r := range d.Reservations {
		if !custodyName(r.WireName) || r.Namespace != strings.ToLower(strings.TrimSpace(r.WireName)) || !custodyOrigin(r.Origin) || !custodyText(r.FirstConstruction, false) || !custodyDigest(r.Approval) {
			return ErrCorrupt
		}
		if _, ok := claims[r.Namespace]; ok {
			return ErrCorrupt
		}
		claims[r.Namespace] = r
	}
	for _, r := range d.Resources {
		if !custodyText(r.Key, false) || !custodyOrigin(r.Origin) || !custodySpec(r.Authored) || !custodyText(r.FirstAttempt, true) || !custodyText(r.ServerRevision, true) || !custodyEvidence(r.Acquisition, false) {
			return ErrCorrupt
		}
		if _, ok := resources[r.Key]; ok {
			return ErrCorrupt
		}
		resources[r.Key] = r
		id := custodyIdentity(r.Coordinate)
		if id != "" && identities[id] {
			return ErrCorrupt
		}
		if id != "" {
			identities[id] = true
		}
		if !slices.Contains([]ResourceStatus{ResourceReserved, ResourceOwned, ResourceDisabled, ResourceRetained, ResourceRemoved, ResourceNotApplied}, r.Status) {
			return ErrCorrupt
		}
		for _, n := range r.Namespaces {
			if claim, ok := claims[n]; !ok || claim.Origin != r.Origin {
				return ErrCorrupt
			}
		}
		for _, o := range r.Observations {
			if !custodyObservation(o) || o.Coordinate != r.Coordinate {
				return ErrCorrupt
			}
		}
		if r.Status != ResourceReserved && r.Status != ResourceNotApplied {
			if !custodyCoordinate(r.Coordinate, false, "") || r.FirstAttempt == "" || !custodyDigest(r.ManagedHash) || !custodyEvidence(r.Acquisition, true) || len(r.Observations) == 0 {
				return ErrCorrupt
			}
		}
		var last *Observation
		for i := range r.Observations {
			if r.Observations[i].Presence == PresencePresent {
				last = &r.Observations[i]
			}
		}
		if last != nil && (r.ManagedHash != last.ManagedHash || r.ServerRevision != last.ServerRevision) {
			return ErrCorrupt
		}
	}
	for _, r := range d.Resources {
		for _, dep := range r.Dependencies {
			if _, ok := resources[dep]; !ok {
				return ErrCorrupt
			}
		}
	}
	for _, c := range d.Constructions {
		if !custodyText(c.ID, false) || !custodyDigest(c.Preview) || !custodyDigest(c.Approval) || !custodyBase(c.Base) || c.Mode != c.Plan.Mode {
			return ErrCorrupt
		}
		if _, ok := constructs[c.ID]; ok {
			return ErrCorrupt
		}
		p, e := NewPlan(c.Plan)
		if e != nil || p.hash != c.Input || p.input.Binding != d.Binding || !slices.Equal(p.names, c.Namespaces) || len(c.Steps) != len(c.Plan.Steps) {
			return ErrCorrupt
		}
		if a, ok := consumed[c.Approval]; !ok || a.ConstructionID != c.ID {
			return ErrCorrupt
		}
		for _, cl := range p.claims {
			existing, ok := claims[cl.Namespace]
			if !ok || existing.Origin != cl.Origin || existing.WireName != cl.WireName {
				return ErrCorrupt
			}
		}
		for _, r := range c.Plan.Resources {
			actual, ok := resources[r.Key]
			if !ok || actual.Origin != r.Origin || actual.Authored.hash != r.Spec.hash || !slices.Equal(actual.Dependencies, r.Dependencies) || !slices.Equal(actual.Namespaces, r.Namespaces) || c.Mode != ModeApply && actual.Coordinate != r.Coordinate {
				return ErrCorrupt
			}
		}
		settled := true
		for i, s := range c.Steps {
			ps := c.Plan.Steps[i]
			pr := planResource(c.Plan, ps.ResourceKey)
			actual := resources[ps.ResourceKey]
			if pr == nil || !custodyStepCoordinates(c.Mode, *pr, s, actual, resources) {
				return ErrCorrupt
			}
			if c.Mode == ModeApply {
				if acquisitions[pr.Key] {
					return ErrCorrupt
				}
				acquisitions[pr.Key] = true
			}
			if s.Number != ps.Number || !custodyEvidence(s.Evidence, false) || !slices.Contains([]StepStatus{StepPlanned, StepIntent, StepResponse, StepVerified, StepNotApplied, StepClassified}, s.Status) {
				return ErrCorrupt
			}
			if s.Status == StepPlanned || s.Status == StepClassified {
				if s.AttemptID != "" {
					return ErrCorrupt
				}
			} else {
				if !custodyText(s.AttemptID, false) {
					return ErrCorrupt
				}
				if _, ok := attempts[s.AttemptID]; ok {
					return ErrCorrupt
				}
				attempts[s.AttemptID] = ps.ResourceKey
			}
			if s.Status == StepIntent || s.Status == StepResponse {
				inflight++
			}
			if s.Status != StepVerified && s.Status != StepClassified {
				settled = false
			}
		}
		latestClass := map[string]CleanupReason{}
		for _, x := range c.Classifications {
			if !validCustodyClassification(x) || c.Mode == ModeApply {
				return ErrCorrupt
			}
			if r, ok := resources[x.ResourceKey]; !ok || planResource(c.Plan, x.ResourceKey) == nil || r.Coordinate != x.Coordinate {
				return ErrCorrupt
			}
			latestClass[x.ResourceKey] = x.Reason
		}
		for _, reason := range latestClass {
			if reason == CleanupUnresolved {
				settled = false
			}
		}
		if c.Complete && !settled {
			return ErrCorrupt
		}
		constructs[c.ID] = c
	}
	for _, r := range d.Reservations {
		c, ok := constructs[r.FirstConstruction]
		if !ok || c.Mode != ModeApply || c.Approval != r.Approval {
			return ErrCorrupt
		}
	}
	for _, a := range d.Consumed {
		if _, ok := constructs[a.ConstructionID]; !ok {
			return ErrCorrupt
		}
	}
	for _, r := range d.Resources {
		if !acquisitions[r.Key] || r.FirstAttempt != "" && attempts[r.FirstAttempt] != r.Key {
			return ErrCorrupt
		}
	}
	receipts := map[string]bool{}
	receipted := map[string]bool{}
	for _, r := range d.Receipts {
		c, ok := constructs[r.ConstructionID]
		a, approved := consumed[r.OperatorApproval]
		if !ok || !approved || a.ConstructionID != c.ID || receipts[r.ID] || !custodyText(r.ID, false) || !custodyText(r.InvalidatedBy, true) || r.BindingDigest != custodyBindingHash(d.Binding) || r.Input != c.Input || r.Mode != c.Mode || !custodyDigest(r.Preview) || !custodyDigest(r.Inventory) || !slices.Equal(r.Namespaces, c.Namespaces) || r.ObservedMS <= 0 || len(r.Members) != len(c.Plan.Resources) {
			return ErrCorrupt
		}
		receipts[r.ID] = true
		receipted[c.ID] = true
		if !validCustodyReceiptResult(r) {
			return ErrCorrupt
		}
		if !slices.Contains([]ReceiptResult{ReceiptApplyObserved, ReceiptDeactivated, ReceiptFullyRemoved, ReceiptIncomplete}, r.Result) || r.Mode == ModeApply && r.Result != ReceiptApplyObserved || r.Mode != ModeApply && r.Result == ReceiptApplyObserved {
			return ErrCorrupt
		}
		seen := map[string]bool{}
		for _, m := range r.Members {
			if !validCustodyMember(m) || seen[m.ResourceKey] || m.Coordinate != resources[m.ResourceKey].Coordinate {
				return ErrCorrupt
			}
			seen[m.ResourceKey] = true
			found := false
			for _, pr := range c.Plan.Resources {
				if pr.Key == m.ResourceKey {
					found = true
				}
			}
			if !found {
				return ErrCorrupt
			}
		}
	}
	for _, c := range d.Constructions {
		if c.Complete && !receipted[c.ID] {
			return ErrCorrupt
		}
	}
	if inflight > 1 || (inflight == 0) != (d.Pending == nil) {
		return ErrCorrupt
	}
	if p := d.Pending; p != nil {
		c, ok := constructs[p.ConstructionID]
		if !ok || c.Complete || p.BindingDigest != custodyBindingHash(d.Binding) || p.Input != c.Input || !custodyDigest(p.Preview) || !custodyDigest(p.OriginalApproval) || p.IntentHead.Generation >= d.Generation || !custodyDigest(p.IntentHead.Digest) || p.Method != custodyMethod(p.Coordinate.Kind, p.Operation) || !custodySpec(p.Desired) || !custodyDigest(p.PreInventory) || !custodyDigest(p.PreObservation) || !slices.Equal(p.Namespaces, c.Namespaces) || attempts[p.AttemptID] != p.ResourceKey {
			return ErrCorrupt
		}
		if a, ok := consumed[p.OriginalApproval]; !ok || a.ConstructionID != c.ID {
			return ErrCorrupt
		}
		found := false
		for i, s := range c.Steps {
			if s.AttemptID == p.AttemptID && s.Status == p.Phase && c.Plan.Steps[i].ResourceKey == p.ResourceKey && c.Plan.Steps[i].Operation == p.Operation {
				pr := planResource(c.Plan, p.ResourceKey)
				coordinate, valid := custodyHistoricalIntent(*pr, resources)
				if !valid || p.Coordinate != coordinate || p.Phase == StepIntent && s.Coordinate != p.Coordinate || p.Phase == StepResponse && (p.Returned == nil || s.Coordinate != p.Returned.Coordinate) {
					return ErrCorrupt
				}
				found = true
			}
		}
		if !found {
			return ErrCorrupt
		}
		if p.Phase != StepIntent && p.Phase != StepResponse || p.Phase == StepIntent && p.Returned != nil || p.Phase == StepResponse && p.Returned == nil {
			return ErrCorrupt
		}
		if p.Returned != nil && (!validCustodyResponse(*p.Returned) || !coordinateResolves(p.Coordinate, p.Returned.Coordinate)) {
			return ErrCorrupt
		}
	}
	return nil
}

// Historical resolution uses the recorded draft identity, not its current
// ownership status: legitimate later cleanup must not invalidate old attribution.
func custodyHistoricalIntent(pr PlannedResource, resources map[string]ResourceRecord) (Coordinate, bool) {
	coordinate := pr.Coordinate
	if pr.VersionFrom != "" {
		draft, ok := resources[pr.VersionFrom]
		if !ok || draft.Coordinate.Unassigned || draft.Coordinate.Version == "" {
			return Coordinate{}, false
		}
		coordinate.Version = draft.Coordinate.Version
	}
	return coordinate, custodyCoordinate(coordinate, true, "")
}

func custodyStepCoordinates(mode Mode, pr PlannedResource, step StepRecord, actual ResourceRecord, resources map[string]ResourceRecord) bool {
	if mode != ModeApply {
		return pr.Coordinate == actual.Coordinate && step.Coordinate == actual.Coordinate
	}
	// The sole creating step anchors FirstAttempt, caller coordinates, generated
	// IDs and the first observation. Keys alone are not custody attribution.
	if actual.FirstAttempt != step.AttemptID {
		return false
	}
	if step.Status == StepPlanned {
		return step.Coordinate == pr.Coordinate && actual.Coordinate == pr.Coordinate && actual.Status == ResourceReserved && actual.ManagedHash == "" && actual.ServerRevision == "" && len(actual.Acquisition) == 0 && len(actual.Observations) == 0
	}
	intent, ok := custodyHistoricalIntent(pr, resources)
	if !ok {
		return false
	}
	switch step.Status {
	case StepIntent, StepNotApplied:
		status := ResourceReserved
		if step.Status == StepNotApplied {
			status = ResourceNotApplied
		}
		return step.Coordinate == intent && actual.Coordinate == pr.Coordinate && actual.Status == status && actual.ManagedHash == "" && actual.ServerRevision == "" && len(actual.Acquisition) == 0 && len(actual.Observations) == 0
	case StepResponse:
		recorded := pr.Coordinate
		if pr.Coordinate.Unassigned {
			recorded = step.Coordinate
		}
		return coordinateResolves(intent, step.Coordinate) && actual.Coordinate == recorded && actual.Status == ResourceReserved && actual.ManagedHash == "" && actual.ServerRevision == "" && len(actual.Acquisition) == 0 && len(actual.Observations) == 0
	case StepVerified:
		return coordinateResolves(intent, step.Coordinate) && actual.Coordinate == step.Coordinate && actual.Status != ResourceReserved && actual.Status != ResourceNotApplied && len(actual.Observations) > 0 && actual.Observations[0].Presence == PresencePresent && slices.Equal(actual.Observations[0].Evidence, step.Evidence)
	}
	return false
}

func validCustodyResponse(r ResponseEvidence) bool {
	return custodyCoordinate(r.Coordinate, false, "") && custodyEvidence(r.Evidence, true) && (r.ManagedHash == nil || custodyDigest(*r.ManagedHash))
}
func validCustodyClassification(c CleanupClassification) bool {
	if !custodyText(c.ResourceKey, false) || !custodyCoordinate(c.Coordinate, false, "") || c.ExistingHead.Generation == 0 || !custodyDigest(c.ExistingHead.Digest) || !custodyDigest(c.Inventory) || !custodyEvidence(c.Evidence, true) {
		return false
	}
	if c.Observation != nil && (!custodyObservation(*c.Observation) || c.Observation.Coordinate != c.Coordinate || c.Observation.Inventory != c.Inventory) {
		return false
	}
	switch c.Reason {
	case CleanupAlreadyAbsent:
		return c.Observation != nil && c.Observation.Presence == PresenceAbsent
	case CleanupRetainedDependency, CleanupRetainedUnsupported:
		return c.Observation != nil && c.Observation.Presence == PresencePresent
	case CleanupUnresolved:
		return true
	}
	return false
}
func validCustodyMember(m ReceiptMember) bool {
	if !custodyText(m.ResourceKey, false) || !custodyCoordinate(m.Coordinate, false, "") || !custodyEvidence(m.Evidence, true) {
		return false
	}
	if m.Observation != nil && (!custodyObservation(*m.Observation) || m.Observation.Coordinate != m.Coordinate) {
		return false
	}
	switch m.Outcome {
	case OutcomeUnresolved:
		return true
	case OutcomeRemoved, OutcomeAlreadyAbsent:
		return m.Observation != nil && m.Observation.Presence == PresenceAbsent
	case OutcomePresent, OutcomePresentDisabled, OutcomeRetainedDependency, OutcomeRetainedUnsupported:
		return m.Observation != nil && m.Observation.Presence == PresencePresent
	}
	return false
}

func validCustodyReceiptResult(r Receipt) bool {
	allAbsent, disabled, unresolved := true, true, false
	for _, m := range r.Members {
		if !validCustodyMember(m) || m.Observation != nil && m.Observation.Inventory != r.Inventory {
			return false
		}
		if r.Mode == ModeApply && m.Outcome != OutcomePresent {
			return false
		}
		if r.Mode != ModeApply && m.Outcome == OutcomePresent {
			return false
		}
		if m.Outcome != OutcomeRemoved && m.Outcome != OutcomeAlreadyAbsent {
			allAbsent = false
		}
		if m.Outcome == OutcomeUnresolved {
			unresolved = true
		}
		if isConsumer(m.Coordinate.Kind) && m.Outcome != OutcomePresentDisabled && m.Outcome != OutcomeRemoved && m.Outcome != OutcomeAlreadyAbsent {
			disabled = false
		}
	}
	switch r.Mode {
	case ModeApply:
		return r.Result == ReceiptApplyObserved && !unresolved
	case ModeTeardown:
		if allAbsent {
			return r.Result == ReceiptFullyRemoved
		}
		return r.Result == ReceiptIncomplete
	case ModeDeactivate:
		if disabled && !unresolved {
			return r.Result == ReceiptDeactivated
		}
		return r.Result == ReceiptIncomplete
	}
	return false
}
