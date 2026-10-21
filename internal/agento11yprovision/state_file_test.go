// SPDX-License-Identifier: AGPL-3.0-only

//go:build darwin || linux

package agento11yprovision

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func proofDirectory(t *testing.T) string {
	t.Helper()
	p, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(p, 0700); e != nil {
		t.Fatal(e)
	}
	return p
}
func proofStore(t *testing.T, path string, enrolled bool) *LockedState {
	t.Helper()
	s, e := AcquireState(context.Background(), Location{path, enrolled}, proofBinding())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := s.Close(); e != nil {
			t.Errorf("close: %v", e)
		}
	})
	return s
}
func proofAuthorize(t *testing.T, s *LockedState, p Plan, mode Mode, base Base, inventory Digest, id, construction string, expires time.Time) (*Authorization, Preview) {
	t.Helper()
	pv, e := NewPreview(p, mode, base, inventory)
	if e != nil {
		t.Fatal(e)
	}
	raw := proofApprovalJSON(t, pv, id, construction, time.Now().Add(-time.Second), expires, nil)
	a, e := DecodeApproval(raw)
	if e != nil {
		t.Fatal(e)
	}
	auth, e := s.Authorize(context.Background(), a, pv, inventory)
	if e != nil {
		t.Fatal(e)
	}
	return auth, pv
}
func proofBootstrap(t *testing.T, s *LockedState) (Snapshot, *Authorization, Plan, Preview) {
	return proofBootstrapInput(t, s, proofInput(t))
}
func proofBootstrapInput(t *testing.T, s *LockedState, input PlanInput) (Snapshot, *Authorization, Plan, Preview) {
	t.Helper()
	p, e := NewPlan(input)
	if e != nil {
		t.Fatal(e)
	}
	auth, pv := proofAuthorize(t, s, p, ModeApply, Base{Pristine: true}, proofDigest('1'), "initial", "", time.Now().Add(time.Minute))
	snap, e := s.Bootstrap(context.Background(), auth)
	if e != nil {
		t.Fatal(e)
	}
	return snap, auth, p, pv
}
func proofCommit(t *testing.T, s *LockedState, snap Snapshot, a *Authorization, x Change) Snapshot {
	t.Helper()
	out, e := s.Commit(context.Background(), *snap.Base.Head, a, x)
	if e != nil {
		t.Fatalf("%s: %v", x.Kind, e)
	}
	return out
}
func proofIntent(t *testing.T, s *LockedState, snap Snapshot, a *Authorization, step uint32, inventory Digest) Snapshot {
	t.Helper()
	c := snap.Document.Constructions[len(snap.Document.Constructions)-1]
	ps := c.Plan.Steps[step-1]
	pr := planResource(c.Plan, ps.ResourceKey)
	deps := []Coordinate{}
	for _, key := range pr.Dependencies {
		deps = append(deps, findResource(snap.Document, key).Coordinate)
	}
	desired := pr.Spec
	if ps.Operation == OpDisable {
		var e error
		desired, e = NewAuthoredSpec([]byte(`{"enabled":false}`))
		if e != nil {
			t.Fatal(e)
		}
	}
	if ps.Operation == OpDelete {
		var e error
		desired, e = NewAuthoredSpec([]byte(`{}`))
		if e != nil {
			t.Fatal(e)
		}
	}
	return proofCommit(t, s, snap, a, Change{Kind: ChangeIntent, ExpectedInventory: inventory, ConstructionID: c.ID, AttemptID: fmt.Sprintf("attempt-%s-%d", c.ID, step), Step: step, Intent: &IntentEvidence{desired, proofDigest('2'), deps}})
}
func proofObserveStep(t *testing.T, s *LockedState, snap Snapshot, a *Authorization, inventory, next Digest) Snapshot {
	t.Helper()
	p := snap.Document.Pending
	ctx := context.Background()
	g, e := s.ClaimDispatch(ctx, *snap.Base.Head, a, p.AttemptID)
	if e != nil {
		t.Fatal(e)
	}
	// Actual local work joins before Release; no HTTP/backend outcome is alleged.
	done := make(chan struct{})
	go func() { defer close(done); _ = g.Context().Err() }()
	<-done
	if e = s.ReleaseDispatch(g); e != nil {
		t.Fatal(e)
	}
	coord := p.Coordinate
	if coord.Unassigned {
		coord.Unassigned = false
		coord.ID = fmt.Sprintf("generated-%s", p.ResourceKey)
		if coord.Kind == KindDraft {
			coord.ID = "v1"
			coord.Version = "v1"
		}
	}
	hash := proofDigest('a')
	presence := PresencePresent
	if p.Operation == OpDisable {
		hash = proofDigest('b')
	}
	if p.Operation == OpDelete {
		hash = ""
		presence = PresenceAbsent
	}
	ev := []Evidence{{"response", proofDigest('e')}}
	response := ResponseEvidence{Coordinate: coord, Evidence: ev}
	if presence == PresencePresent {
		response.ManagedHash = &hash
	}
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeResponse, ExpectedInventory: inventory, ConstructionID: p.ConstructionID, AttemptID: p.AttemptID, Step: findStep(snap.Document, p.AttemptID), Response: &response})
	obs := Observation{Coordinate: coord, Presence: presence, ManagedHash: hash, ExactRead: proofDigest('2'), Inventory: next, Evidence: []Evidence{{"read-back", proofDigest('f')}}}
	return proofCommit(t, s, snap, a, Change{Kind: ChangeObserve, ExpectedInventory: inventory, ConstructionID: p.ConstructionID, AttemptID: p.AttemptID, Step: findStep(snap.Document, p.AttemptID), Observation: &obs})
}
func findStep(d *Document, attempt string) uint32 {
	for _, c := range d.Constructions {
		for _, s := range c.Steps {
			if s.AttemptID == attempt {
				return s.Number
			}
		}
	}
	return 0
}
func proofApply(t *testing.T, s *LockedState) (Snapshot, Plan) {
	return proofApplyInput(t, s, proofInput(t))
}
func proofApplyInput(t *testing.T, s *LockedState, input PlanInput) (Snapshot, Plan) {
	t.Helper()
	snap, a, p, pv := proofBootstrapInput(t, s, input)
	inv := proofDigest('1')
	for i := range p.input.Steps {
		snap = proofIntent(t, s, snap, a, uint32(i+1), inv)
		next := proofDigest(byte('3' + i))
		snap = proofObserveStep(t, s, snap, a, inv, next)
		inv = next
	}
	c := snap.Document.Constructions[0]
	members := []ReceiptMember{}
	for _, pr := range p.input.Resources {
		r := findResource(snap.Document, pr.Key)
		o := Observation{Coordinate: r.Coordinate, Presence: PresencePresent, ManagedHash: r.ManagedHash, ExactRead: proofDigest('8'), Inventory: inv, Evidence: []Evidence{{"final-read", proofDigest('f')}}}
		members = append(members, ReceiptMember{r.Key, r.Coordinate, OutcomePresent, &o, []Evidence{{"closure", proofDigest('e')}}})
	}
	receipt := Receipt{ID: "apply-receipt", ConstructionID: c.ID, Mode: ModeApply, Result: ReceiptApplyObserved, BindingDigest: custodyBindingHash(proofBinding()), Input: p.Digest(), Preview: pv.Digest(), Inventory: inv, Namespaces: []string{"planner", "source"}, Members: members, OperatorApproval: snap.Document.Consumed[0].Digest, ObservedMS: time.Now().UnixMilli()}
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeFinish, ExpectedInventory: inv, ConstructionID: c.ID, Receipt: &receipt})
	return snap, p
}

func TestFoundationPersistedSmallPlanAndCodec(t *testing.T) {
	dir := proofDirectory(t)
	s := proofStore(t, dir, false)
	snap, p := proofApply(t, s)
	if len(snap.Document.Reservations) != 2 || snap.Document.Reservations[1].Namespace != "source" {
		t.Fatal("target authority leaked or omitted source")
	}
	if len(snap.Document.Resources) != 4 || !snap.Document.Constructions[0].Complete {
		t.Fatal("small plan incomplete")
	}
	raw, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("complete four-resource state: %d bytes", len(raw))
	if !bytes.Contains(raw, []byte(`"number":1e2`)) || bytes.Contains(raw, []byte(`"authored":{}`)) {
		t.Fatal("opaque object/number lost on actual wire")
	}
	snap.Document.Constructions[0].Plan.Declarations[0].Declaration.Name = "mutated"
	snap.Document.Resources[0].Dependencies = append(snap.Document.Resources[0].Dependencies, "mutated")
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s2 := proofStore(t, dir, true)
	loaded, e := s2.Load(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	a := loaded.Document.Constructions[0].Plan.Declarations[0].Declaration
	if a.Name != "Planner" || a.Service.Namespace != "" || a.Models == nil || a.ProjectedChildNames == nil || a.HookPolicy.Phases == nil || len(a.Models) != 0 {
		t.Fatal("core optional normalization/aliasing")
	}
	if string(loaded.Document.Resources[0].Authored.JSON()) != string(p.input.Resources[0].Spec.JSON()) {
		t.Fatal("authored reconstruction lost object")
	}
	bad := *loaded.Document
	bad.Reservations = nil
	data, e := custodyDocumentBytes(bad)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = custodyDocumentDecode(data); e == nil {
		t.Fatal("history deletion accepted even with checksum")
	}
	// Real disk parser controls: each rewrite starts from the independently retained
	// valid bytes; no successful check relies on default marshaling opaque records.
	if e = s2.Close(); e != nil {
		t.Fatal(e)
	}
	// Recompute the private checksum for the null control: otherwise a hash
	// mismatch alone could mask a missing required/null-field check.
	nullWire := documentToWire(*loaded.Document)
	nullWire.Constructions[0].Plan.Declarations[0].Declaration.Models = nil
	nullWire.Checksum = ""
	preNull, _ := json.Marshal(nullWire)
	nullWire.Checksum = custodyHash("state", preNull)
	nullRaw, _ := json.Marshal(nullWire)
	variants := [][]byte{append(append([]byte{}, raw...), []byte(` {}`)...), bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":1,"schema":1`), 1), bytes.Replace(raw, []byte(`"resources":[`), []byte(`"unknown":1,"resources":[`), 1), nullRaw, bytes.Replace(raw, []byte(`"authored":{"array":[],"number":1e2,"object":{},"pattern":"fictional"}`), []byte(`"authored":{}`), 1)}
	for i, data := range variants {
		if bytes.Equal(raw, data) {
			t.Fatalf("parser control %d not applied", i)
		}
		if e = os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); e != nil {
			t.Fatal(e)
		}
		st := proofStore(t, dir, true)
		if _, e = st.Load(context.Background()); e == nil {
			t.Fatalf("disk corruption %d accepted", i)
		}
		st.Close()
	}
	os.WriteFile(filepath.Join(dir, "state.json"), raw, 0600)
}

// The review boundary used legal HTML-escaped evidence, not raw response bodies.
func proofMaxEvidence() []Evidence {
	es := []Evidence{}
	for i := 0; i < 16; i++ {
		es = append(es, Evidence{strings.Repeat("<", 254) + fmt.Sprintf("%02d", i), proofDigest('e')})
	}
	return es
}

func TestFoundationResultCapacityReservedBeforeDispatch(t *testing.T) {
	for _, size := range []int{23000, 64} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			dir := proofDirectory(t)
			s := proofStore(t, dir, false)
			in := proofInput(t)
			spec, e := NewAuthoredSpec([]byte(`{"x":"` + strings.Repeat("a", size) + `"}`))
			if e != nil {
				t.Fatal(e)
			}
			for i := range in.Resources {
				in.Resources[i].Spec = spec
			}
			p := mustPlan(t, in)
			a, pv := proofAuthorize(t, s, p, ModeApply, Base{Pristine: true}, proofDigest('1'), "capacity", "", time.Now().Add(time.Minute))
			snap, e := s.Bootstrap(context.Background(), a)
			if errors.Is(e, ErrCapacity) && size == 23000 {
				fresh, err := s.Load(context.Background())
				if err != nil || !fresh.Base.Pristine {
					t.Fatal("capacity refusal enrolled state", err)
				}
				t.Log("large plan refused before enrollment/dispatch")
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			inv := proofDigest('1')
			for i := range in.Steps {
				snap = proofIntent(t, s, snap, a, uint32(i+1), inv)
				pending := snap.Document.Pending
				g, err := s.ClaimDispatch(context.Background(), *snap.Base.Head, a, pending.AttemptID)
				if err != nil {
					t.Fatal(err)
				}
				joined := make(chan struct{})
				go func() { defer close(joined); _ = g.Context().Err() }()
				<-joined
				if err = s.ReleaseDispatch(g); err != nil {
					t.Fatal(err)
				}
				coord := pending.Coordinate
				if coord.Unassigned {
					coord.Unassigned = false
					coord.ID = strings.Repeat("<", 256)
				}
				snap = proofCommit(t, s, snap, a, Change{Kind: ChangeResponse, ExpectedInventory: inv, ConstructionID: "capacity", AttemptID: pending.AttemptID, Step: uint32(i + 1), Response: &ResponseEvidence{Coordinate: coord, Evidence: proofMaxEvidence()}})
				obs := Observation{coord, PresencePresent, proofDigest('a'), proofDigest('2'), inv, strings.Repeat("<", 256), proofMaxEvidence()}
				snap = proofCommit(t, s, snap, a, Change{Kind: ChangeObserve, ExpectedInventory: inv, ConstructionID: "capacity", AttemptID: pending.AttemptID, Step: uint32(i + 1), Observation: &obs})
			}
			members := []ReceiptMember{}
			for _, r := range snap.Document.Resources {
				o := Observation{r.Coordinate, PresencePresent, r.ManagedHash, proofDigest('2'), inv, r.ServerRevision, proofMaxEvidence()}
				members = append(members, ReceiptMember{r.Key, r.Coordinate, OutcomePresent, &o, proofMaxEvidence()})
			}
			r := Receipt{ID: strings.Repeat("<", 256), ConstructionID: "capacity", Mode: ModeApply, Result: ReceiptApplyObserved, BindingDigest: custodyBindingHash(proofBinding()), Input: p.Digest(), Preview: pv.Digest(), Inventory: inv, Namespaces: []string{"planner", "source"}, Members: members, OperatorApproval: a.approval.hash, ObservedMS: time.Now().UnixMilli()}
			snap = proofCommit(t, s, snap, a, Change{Kind: ChangeFinish, ExpectedInventory: inv, ConstructionID: "capacity", Receipt: &r})
			if len(snap.Document.Resources) != 4 || len(snap.Document.Resources[0].Acquisition) != 16 || len(snap.Document.Reservations) != 2 || !snap.Document.Constructions[0].Complete {
				t.Fatal("capacity consumed or pruned history")
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			again := proofStore(t, dir, true)
			if _, e = again.Load(context.Background()); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestFoundationContradictoryCoordinatesRejectLoad(t *testing.T) {
	dir := proofDirectory(t)
	s := proofStore(t, dir, false)
	snap, _ := proofApply(t, s)
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	for _, variant := range []string{"resource-and-observations", "plan", "step", "receipt", "target-origin", "first-attempt"} {
		t.Run(variant, func(t *testing.T) {
			w := documentToWire(*snap.Document)
			switch variant {
			case "resource-and-observations":
				w.Resources[0].Coordinate.ID = "foreign-e"
				for i := range w.Resources[0].Observations {
					w.Resources[0].Observations[i].Coordinate.ID = "foreign-e"
				}
			case "plan":
				w.Constructions[0].Plan.Resources[0].Coordinate.ID = "foreign-e"
			case "target-origin":
				w.Constructions[0].Plan.Resources[0].Origin = w.Constructions[0].Plan.Targets[1]
				w.Resources[0].Origin = w.Constructions[0].Plan.Targets[1]
			case "step":
				w.Constructions[0].Steps[0].Coordinate.ID = "foreign-e"
			case "receipt":
				w.Receipts[0].Members[0].Coordinate.ID = "foreign-e"
				w.Receipts[0].Members[0].Observation.Coordinate.ID = "foreign-e"
			case "first-attempt":
				w.Resources[0].FirstAttempt = w.Constructions[0].Steps[2].AttemptID
			}
			// Rebind the plan digest and receipt's input too: a stale digest must
			// not mask the cross-record identity/provenance contradiction.
			planBytes, err := json.Marshal(w.Constructions[0].Plan)
			if err != nil {
				t.Fatal(err)
			}
			w.Constructions[0].Input = custodyHash("plan", planBytes)
			w.Receipts[0].Input = w.Constructions[0].Input
			w.Checksum = ""
			pre, err := json.Marshal(w)
			if err != nil {
				t.Fatal(err)
			}
			w.Checksum = custodyHash("state", pre)
			raw, err := json.Marshal(w)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, "state.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			st := proofStore(t, dir, true)
			defer st.Close()
			if loaded, err := st.Load(context.Background()); !errors.Is(err, ErrCorrupt) || loaded.Document != nil {
				t.Fatalf("contradictory %s returned usable Load: %v", variant, err)
			}
		})
	}
}

func TestFoundationAttemptCoordinatesRejectLoad(t *testing.T) {
	for _, variant := range []string{"pending", "acquisition-attempt"} {
		t.Run(variant, func(t *testing.T) {
			dir := proofDirectory(t)
			s := proofStore(t, dir, false)
			var snap Snapshot
			if variant == "pending" {
				initial, a, _, _ := proofBootstrap(t, s)
				snap = proofIntent(t, s, initial, a, 1, proofDigest('1'))
			} else {
				initial, _ := proofApply(t, s)
				p := proofCleanupPlan(t, initial, ModeDeactivate, []PlannedStep{{1, "r", OpDisable}})
				a, _ := proofAuthorize(t, s, p, ModeDeactivate, initial.Base, proofDigest('6'), "disable-approval", "", time.Now().Add(time.Minute))
				snap = proofCommit(t, s, initial, a, Change{Kind: ChangeStart, ExpectedInventory: proofDigest('6'), ConstructionID: "disable"})
				snap = proofIntent(t, s, snap, a, 1, proofDigest('6'))
				snap = proofObserveStep(t, s, snap, a, proofDigest('6'), proofDigest('7'))
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			w := documentToWire(*snap.Document)
			if variant == "pending" {
				w.Pending.Coordinate.ID = "foreign-e"
			} else {
				w.Resources[2].FirstAttempt = w.Constructions[1].Steps[0].AttemptID
			}
			w.Checksum = ""
			pre, e := json.Marshal(w)
			if e != nil {
				t.Fatal(e)
			}
			w.Checksum = custodyHash("state", pre)
			raw, e := json.Marshal(w)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(dir, "state.json"), raw, 0600); e != nil {
				t.Fatal(e)
			}
			again := proofStore(t, dir, true)
			if loaded, e := again.Load(context.Background()); !errors.Is(e, ErrCorrupt) || loaded.Document != nil {
				t.Fatalf("contradictory %s attribution loaded: %v", variant, e)
			}
		})
	}
}

func TestFoundationFreshVersionResolutionPersists(t *testing.T) {
	// Opaque fictional authored objects exercise structural references only, not
	// backend suite admission/publication or vendor payload compatibility.
	s := proofStore(t, proofDirectory(t), false)
	in := proofInput(t)
	origin := in.Targets[0]
	spec := in.Resources[0].Spec
	in.Resources = []PlannedResource{
		{Key: "suite", Origin: origin, Coordinate: Coordinate{Kind: KindSuite, ID: "s-1"}, Spec: spec, Namespaces: []string{"planner"}},
		{Key: "draft", Origin: origin, Coordinate: Coordinate{Kind: KindDraft, ParentID: "s-1", Unassigned: true}, Spec: spec, Dependencies: []string{"suite"}, Namespaces: []string{"planner"}},
		{Key: "case", VersionFrom: "draft", Origin: origin, Coordinate: Coordinate{Kind: KindCase, ID: "case-1", ParentID: "s-1"}, Spec: spec, Dependencies: []string{"suite", "draft"}, Namespaces: []string{"planner"}},
		{Key: "bindings", VersionFrom: "draft", Origin: origin, Coordinate: Coordinate{Kind: KindBindings, ParentID: "s-1"}, Spec: spec, Dependencies: []string{"suite", "draft"}, Namespaces: []string{"planner"}},
		{Key: "publication", VersionFrom: "draft", Origin: origin, Coordinate: Coordinate{Kind: KindPublication, ParentID: "s-1"}, Spec: spec, Dependencies: []string{"suite", "draft", "case", "bindings"}, Namespaces: []string{"planner"}},
	}
	in.Steps = []PlannedStep{{1, "suite", OpCreate}, {2, "draft", OpCreate}, {3, "case", OpCreate}, {4, "bindings", OpInitialBindings}, {5, "publication", OpPublish}}
	snap, _ := proofApplyInput(t, s, in)
	for _, key := range []string{"draft", "case", "bindings", "publication"} {
		r := findResource(snap.Document, key)
		if r.Coordinate.Version != "v1" || r.Coordinate.ParentID != "s-1" {
			t.Fatal("generated version/parent projection lost", key)
		}
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	again := proofStore(t, s.location.Directory, true)
	if _, e := again.Load(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestFoundationSnapshotHeadCannotMutateAuthority(t *testing.T) {
	s := proofStore(t, proofDirectory(t), false)
	snap, a, _, _ := proofBootstrap(t, s)
	snap.Base.Head.Generation++
	fresh, e := s.Load(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	// Public snapshot edits cannot rewrite the capability's expected chain.
	_ = proofIntent(t, s, fresh, a, 1, proofDigest('1'))
}

func TestFoundationGrantExpiryJoinAndEvidence(t *testing.T) {
	s := proofStore(t, proofDirectory(t), false)
	p, e := NewPlan(proofInput(t))
	if e != nil {
		t.Fatal(e)
	}
	auth, _ := proofAuthorize(t, s, p, ModeApply, Base{Pristine: true}, proofDigest('1'), "short", "", time.Now().Add(2*time.Second))
	snap, e := s.Bootstrap(context.Background(), auth)
	if e != nil {
		t.Fatal(e)
	}
	snap = proofIntent(t, s, snap, auth, 1, proofDigest('1'))
	pending := snap.Document.Pending
	grant, e := s.ClaimDispatch(context.Background(), *snap.Base.Head, auth, pending.AttemptID)
	if e != nil {
		t.Fatal(e)
	}
	deadline, ok := grant.Context().Deadline()
	if !ok || deadline.UnixMilli() != auth.approval.document.ExpiresMS {
		t.Fatal("grant deadline not checked approval window")
	}
	copied := *grant
	if copied.Context().Err() == nil || s.ReleaseDispatch(&copied) == nil {
		t.Fatal("copied grant accepted")
	}
	cancelled := make(chan struct{})
	allowJoin := make(chan struct{})
	joined := make(chan struct{})
	go func() { defer close(joined); <-grant.Context().Done(); close(cancelled); <-allowJoin }()
	<-cancelled
	if e = s.Close(); !errors.Is(e, ErrBusy) {
		t.Fatalf("expired request released lock: %v", e)
	}
	probe := proofChild(t, s.location.Directory, "probe")
	line := proofLine(t, probe)
	if line != "LOCKED" {
		t.Fatalf("request expiry released cross-process custody: %s", line)
	}
	probe.wait(t)
	close(allowJoin)
	<-joined
	if e = s.ReleaseDispatch(grant); e != nil {
		t.Fatal(e)
	}
	if e = s.ReleaseDispatch(grant); e == nil {
		t.Fatal("released grant reused")
	}
	// Preserve response/read-back using a live commit context AFTER request expiry.
	snap = proofCommit(t, s, snap, auth, Change{Kind: ChangeResponse, ExpectedInventory: proofDigest('1'), ConstructionID: pending.ConstructionID, AttemptID: pending.AttemptID, Step: 1, Response: &ResponseEvidence{Coordinate: pending.Coordinate, Evidence: []Evidence{{"attributable", proofDigest('e')}}}})
	obs := Observation{pending.Coordinate, PresencePresent, proofDigest('a'), proofDigest('2'), proofDigest('3'), "", []Evidence{{"late-read", proofDigest('f')}}}
	snap = proofCommit(t, s, snap, auth, Change{Kind: ChangeObserve, ExpectedInventory: proofDigest('1'), ConstructionID: pending.ConstructionID, AttemptID: pending.AttemptID, Step: 1, Observation: &obs})
	if _, e = s.Commit(context.Background(), *snap.Base.Head, auth, Change{Kind: ChangeIntent, ExpectedInventory: proofDigest('3'), ConstructionID: pending.ConstructionID, AttemptID: "late", Step: 2, Intent: &IntentEvidence{p.input.Resources[1].Spec, proofDigest('2'), []Coordinate{}}}); !errors.Is(e, ErrExpired) {
		t.Fatalf("new expired intent: %v", e)
	}
}

type proofProcess struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	in     ioWriteCloser
	reader *bufio.Reader
	stderr bytes.Buffer
}
type ioWriteCloser interface {
	Write([]byte) (int, error)
	Close() error
}

func proofChild(t *testing.T, dir, mode string) *proofProcess {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	p := &proofProcess{cancel: cancel}
	p.cmd = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFoundationProcessHelper$", "-test.timeout=4s")
	// Preserve race detection/options, but remove its post-exit grace sleep from
	// these hard-five-second helpers (as in the existing HA process proofs).
	p.cmd.Env = append(os.Environ(), "STATEFOUND_CHILD="+mode, "STATEFOUND_DIRECTORY="+dir, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
	p.cmd.Stderr = &p.stderr
	out, e := p.cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	in, e := p.cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	p.in = in
	p.reader = bufio.NewReader(out)
	if e = p.cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cancel(); p.cmd.Process.Kill() })
	return p
}
func proofLine(t *testing.T, p *proofProcess) string {
	t.Helper()
	s, e := p.reader.ReadString('\n')
	if e != nil {
		t.Fatalf("child pipe: %v %s", e, p.stderr.String())
	}
	return strings.TrimSpace(s)
}
func (p *proofProcess) wait(t *testing.T) {
	t.Helper()
	if e := p.cmd.Wait(); e != nil {
		t.Fatalf("child: %v %s", e, p.stderr.String())
	}
	p.cancel()
}
func TestFoundationProcessHelper(t *testing.T) {
	mode := os.Getenv("STATEFOUND_CHILD")
	if mode == "" {
		return
	}
	dir := os.Getenv("STATEFOUND_DIRECTORY")
	switch mode {
	case "probe":
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		edges := stateEdges{}
		if os.Getenv("STATEFOUND_FLOCK_CONTROL") == "1" {
			edges.flock = func(int) error { return nil }
		}
		s, e := acquireCustody(ctx, Location{dir, false}, proofBinding(), edges)
		if errors.Is(e, ErrLockTimeout) {
			fmt.Println("LOCKED")
			return
		}
		if e != nil {
			t.Fatal(e)
		}
		fmt.Println("ACQUIRED")
		s.Close()
	case "load":
		s := proofStore(t, dir, true)
		snap, e := s.Load(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		fmt.Printf("LOAD %d %t %d\n", snap.Base.Head.Generation, snap.Document.Pending != nil, len(snap.Document.Reservations))
		if string(snap.Document.Resources[0].Authored.JSON()) != `{"array":[],"number":1e2,"object":{},"pattern":"fictional"}` {
			t.Fatal("new-process opaque codec loss")
		}
		core := snap.Document.Constructions[0].Plan.Declarations[1].Declaration
		if core.Name != "Source" || core.Service.Namespace != "" || core.Models == nil || core.ProjectedChildNames == nil || core.HookPolicy.Phases == nil || len(core.Models) != 0 {
			t.Fatal("new-process optional core identity/model corruption")
		}
	case "hold":
		s := proofStore(t, dir, false)
		snap, a, _, _ := proofBootstrap(t, s)
		fmt.Printf("HELD %d\n", s.lockID[1])
		reader := bufio.NewReader(os.Stdin)
		if _, e := reader.ReadString('\n'); e != nil {
			t.Fatal(e)
		}
		snap = proofIntent(t, s, snap, a, 1, proofDigest('1'))
		fmt.Printf("PENDING %d %d\n", snap.Base.Head.Generation, s.lockID[1])
		if _, e := reader.ReadString('\n'); e != nil {
			t.Fatal(e)
		}
		if e := s.Close(); e != nil {
			t.Fatal(e)
		}
		fmt.Println("CLOSED")
	case "crash-file-sync", "crash-rename", "crash-dir-sync":
		s := proofStore(t, dir, false)
		snap, a, _, _ := proofBootstrap(t, s)
		stage := "state.json-" + strings.TrimPrefix(mode, "crash-")
		s.edges.after = func(at string) error {
			if at == stage {
				fmt.Println("ACK " + stage)
				os.Exit(23)
			}
			return nil
		}
		_ = proofIntent(t, s, snap, a, 1, proofDigest('1'))
		t.Fatal("crash edge not reached")
	case "crash-enrollment":
		s := proofStore(t, dir, false)
		s.edges.after = func(at string) error {
			if at == "enrollment.json-dir-sync" {
				fmt.Println("ACK enrollment.json-dir-sync")
				os.Exit(23)
			}
			return nil
		}
		_, _, _, _ = proofBootstrap(t, s)
		t.Fatal("enrollment edge not reached")
	default:
		t.Fatal("unknown child mode")
	}
}
func TestFoundationProcessLockAndReload(t *testing.T) {
	dir := proofDirectory(t)
	holder := proofChild(t, dir, "hold")
	held := proofLine(t, holder)
	if !strings.HasPrefix(held, "HELD ") {
		t.Fatal(held)
	}
	probe := proofChild(t, dir, "probe")
	if got := proofLine(t, probe); got != "LOCKED" {
		t.Fatalf("second native process bypassed held flock: %s", got)
	}
	probe.wait(t)
	fmt.Fprintln(holder.in, "intent")
	pending := proofLine(t, holder)
	if pending != "PENDING 2 "+strings.TrimPrefix(held, "HELD ") {
		t.Fatalf("lock inode replaced during atomic state rename: %s", pending)
	}
	probe = proofChild(t, dir, "probe")
	if got := proofLine(t, probe); got != "LOCKED" {
		t.Fatalf("durable pending released lock: %s", got)
	}
	probe.wait(t)
	fmt.Fprintln(holder.in, "close")
	if got := proofLine(t, holder); got != "CLOSED" {
		t.Fatal(got)
	}
	holder.wait(t)
	load := proofChild(t, dir, "load")
	if got := proofLine(t, load); got != "LOAD 2 true 2" {
		t.Fatalf("third process lost actual disk custody: %s", got)
	}
	load.wait(t)
}

func TestFoundationDurabilityFailuresPoison(t *testing.T) {
	for _, stage := range []string{"state.json-write", "state.json-file-sync", "state.json-rename", "state.json-dir-sync"} {
		t.Run(stage, func(t *testing.T) {
			dir := proofDirectory(t)
			s := proofStore(t, dir, false)
			snap, a, p, _ := proofBootstrap(t, s)
			s.edges.sync = func(fd int, at string) error {
				if at == stage {
					return unix.EIO
				}
				return unix.Fsync(fd)
			}
			s.edges.after = func(at string) error {
				if at == stage && (stage == "state.json-write" || stage == "state.json-rename") {
					return unix.EIO
				}
				return nil
			}
			_, e := s.Commit(context.Background(), *snap.Base.Head, a, Change{Kind: ChangeIntent, ExpectedInventory: proofDigest('1'), ConstructionID: "initial", AttemptID: "ambiguous", Step: 1, Intent: &IntentEvidence{p.input.Resources[0].Spec, proofDigest('2'), []Coordinate{}}})
			if !errors.Is(e, ErrOutcomeUnknown) {
				t.Fatalf("required durability edge not observed: %v", e)
			}
			if _, e = s.Authorize(context.Background(), a.approval, a.preview, proofDigest('1')); !errors.Is(e, ErrOutcomeUnknown) {
				t.Fatalf("poison permitted authority: %v", e)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			next := proofStore(t, dir, true)
			loaded, e := next.Load(context.Background())
			if stage == "state.json-file-sync" || stage == "state.json-write" {
				if !errors.Is(e, ErrOutcomeUnknown) {
					t.Fatalf("orphan temp reset: %v", e)
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				if loaded.Document.Pending == nil || len(loaded.Document.Reservations) != 2 {
					t.Fatal("rename uncertainty lost intent/history")
				}
			}
		})
	}
}

func TestFoundationUnsupportedDurabilityPoisons(t *testing.T) {
	s := proofStore(t, proofDirectory(t), false)
	snap, a, p, _ := proofBootstrap(t, s)
	s.edges.sync = func(fd int, stage string) error {
		if stage == "state.json-dir-sync" {
			return unix.EINVAL
		}
		return unix.Fsync(fd)
	}
	_, e := s.Commit(context.Background(), *snap.Base.Head, a, Change{Kind: ChangeIntent, ExpectedInventory: proofDigest('1'), ConstructionID: "initial", AttemptID: "unsupported", Step: 1, Intent: &IntentEvidence{p.input.Resources[0].Spec, proofDigest('2'), []Coordinate{}}})
	if !errors.Is(e, ErrUnsupported) || !errors.Is(e, ErrOutcomeUnknown) {
		t.Fatalf("unsupported durability lost explicit/ambiguous classification: %v", e)
	}
	if _, e = s.ClaimDispatch(context.Background(), *snap.Base.Head, a, "unsupported"); !errors.Is(e, ErrOutcomeUnknown) {
		t.Fatal("unsupported fsync admitted dispatch")
	}
}

func TestFoundationPrivatePathsAndEnrollment(t *testing.T) {
	dir := proofDirectory(t)
	s := proofStore(t, dir, false)
	snap, _, _, _ := proofBootstrap(t, s)
	s.Close()
	raw, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(dir, "state.json")); e != nil {
		t.Fatal(e)
	}
	s = proofStore(t, dir, true)
	if _, e = s.Load(context.Background()); !errors.Is(e, ErrEnrolledMissing) {
		t.Fatalf("missing enrollment reset: %v", e)
	}
	s.Close()
	os.WriteFile(filepath.Join(dir, "state.json"), raw, 0600)
	parent := proofDirectory(t)
	alias := filepath.Join(parent, "alias")
	if e = os.Symlink(dir, alias); e != nil {
		t.Fatal(e)
	}
	if st, e := AcquireState(context.Background(), Location{alias, true}, proofBinding()); e == nil {
		st.Close()
		t.Fatal("symlink ancestor followed")
	}
	if e = os.Chmod(filepath.Join(dir, "state.json"), 0644); e != nil {
		t.Fatal(e)
	}
	s = proofStore(t, dir, true)
	if _, e = s.Load(context.Background()); e == nil {
		t.Fatal("public state mode accepted")
	}
	s.Close()
	os.Chmod(filepath.Join(dir, "state.json"), 0600)
	if e = os.Link(filepath.Join(dir, "state.json"), filepath.Join(parent, "hardlink")); e != nil {
		t.Fatal(e)
	}
	s = proofStore(t, dir, true)
	if _, e = s.Load(context.Background()); e == nil {
		t.Fatal("hardlinked state accepted")
	}
	s.Close()
	os.Remove(filepath.Join(parent, "hardlink"))
	statePath := filepath.Join(dir, "state.json")
	savedState := filepath.Join(parent, "saved-state")
	if e = os.Rename(statePath, savedState); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(savedState, statePath); e != nil {
		t.Fatal(e)
	}
	s = proofStore(t, dir, true)
	if _, e = s.Load(context.Background()); e == nil {
		t.Fatal("symlinked state followed")
	}
	s.Close()
	os.Remove(statePath)
	if e = os.Rename(savedState, statePath); e != nil {
		t.Fatal(e)
	}
	marker := filepath.Join(dir, "enrollment.json")
	savedMarker := filepath.Join(parent, "saved-marker")
	if e = os.Rename(marker, savedMarker); e != nil {
		t.Fatal(e)
	}
	s = proofStore(t, dir, false)
	if _, e = s.Load(context.Background()); !errors.Is(e, ErrCorrupt) {
		t.Fatalf("unbound nonempty state accepted: %v", e)
	}
	s.Close()
	if e = os.Rename(savedMarker, marker); e != nil {
		t.Fatal(e)
	}
	lockDir := proofDirectory(t)
	realLock := filepath.Join(parent, "real-lock")
	if e = os.WriteFile(realLock, nil, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(realLock, filepath.Join(lockDir, "lock")); e != nil {
		t.Fatal(e)
	}
	if st, e := AcquireState(context.Background(), Location{lockDir, false}, proofBinding()); e == nil {
		st.Close()
		t.Fatal("symlinked lock followed")
	}
	os.Remove(filepath.Join(lockDir, "lock"))
	if e = os.Link(realLock, filepath.Join(lockDir, "lock")); e != nil {
		t.Fatal(e)
	}
	if st, e := AcquireState(context.Background(), Location{lockDir, false}, proofBinding()); e == nil {
		st.Close()
		t.Fatal("hardlinked lock accepted")
	}
	s = proofStore(t, dir, true)
	a, pv := proofAuthorize(t, s, mustPlan(t, snap.Document.Constructions[0].Plan), ModeRecover, snap.Base, proofDigest('1'), "recovery-prefix", "initial", time.Now().Add(time.Minute))
	_ = pv
	// Stale CAS must fail even under a matching fresh recovery approval.
	bad := *snap.Base.Head
	bad.Generation++
	if _, e = s.Commit(context.Background(), bad, a, Change{Kind: ChangeStart, ExpectedInventory: proofDigest('1'), ConstructionID: "initial"}); !errors.Is(e, ErrConflict) {
		t.Fatalf("stale generation: %v", e)
	}
	if os.Geteuid() != 0 {
		t.Log("foreign-uid rejection unverified: no privilege to create foreign-owned fixture")
	} else {
		if e = os.Chown(statePath, 42, -1); e != nil {
			t.Fatal(e)
		}
		if _, e = s.Load(context.Background()); e == nil {
			t.Error("foreign-owned state accepted")
		}
		if e = os.Chown(statePath, 0, -1); e != nil {
			t.Fatal(e)
		}
	}
}
func mustPlan(t *testing.T, in PlanInput) Plan {
	t.Helper()
	p, e := NewPlan(in)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestFoundationCapacityRejectsBeforeIntent(t *testing.T) {
	s := proofStore(t, proofDirectory(t), false)
	snap, a, p, _ := proofBootstrap(t, s)
	// Full projected history (not only current JSON) determines admission.
	large := proofInput(t)
	large.Resources = append(large.Resources, large.Resources...)
	if _, e := NewPlan(large); e == nil {
		t.Fatal("duplicate resource plan accepted")
	}
	data := bytes.Repeat([]byte("x"), custodyMaxBytes+1)
	if e := os.WriteFile(filepath.Join(s.location.Directory, "unexpected"), data, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Commit(context.Background(), *snap.Base.Head, a, Change{Kind: ChangeIntent, ExpectedInventory: proofDigest('1'), ConstructionID: "initial", AttemptID: "over", Step: 1, Intent: &IntentEvidence{p.input.Resources[0].Spec, proofDigest('2'), []Coordinate{}}}); e == nil {
		t.Fatal("unbound storage admitted intent")
	}
	os.Remove(filepath.Join(s.location.Directory, "unexpected"))
	// Cap the actual immutable authored bytes, not a reflected {} proxy.
	huge := proofInput(t)
	big, err := NewAuthoredSpec([]byte(`{"x":"` + strings.Repeat("a", 60000) + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	for i := range huge.Resources {
		huge.Resources[i].Spec = big
	}
	plan := mustPlan(t, huge)
	pv, err := NewPreview(plan, ModeApply, Base{Pristine: true}, proofDigest('1'))
	if err != nil {
		t.Fatal(err)
	}
	s2 := proofStore(t, proofDirectory(t), false)
	raw := proofApprovalJSON(t, pv, "large", "", time.Now().Add(-time.Second), time.Now().Add(time.Minute), nil)
	approval, err := DecodeApproval(raw)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := s2.Authorize(context.Background(), approval, pv, proofDigest('1'))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s2.Bootstrap(context.Background(), auth); !errors.Is(err, ErrCapacity) {
		t.Fatalf("projected growth not budgeted: %v", err)
	}
	empty, err := s2.Load(context.Background())
	if err != nil || !empty.Base.Pristine {
		t.Fatal("capacity refusal wrote enrollment")
	}
	t.Run("generation", func(t *testing.T) {
		dir := proofDirectory(t)
		st := proofStore(t, dir, false)
		initial, _, p, _ := proofBootstrap(t, st)
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		// A correctly checksummed near-overflow head must reserve result commits
		// too; no admitted request may exhaust the generation counter afterward.
		initial.Document.Generation = math.MaxUint64 - 1
		raw, err := custodyDocumentBytes(*initial.Document)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "state.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		again := proofStore(t, dir, true)
		loaded, err := again.Load(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		a, _ := proofAuthorize(t, again, p, ModeRecover, loaded.Base, proofDigest('1'), "overflow-suffix", "initial", time.Now().Add(time.Minute))
		if _, err = again.Commit(context.Background(), *loaded.Base.Head, a, Change{Kind: ChangeStart, ExpectedInventory: proofDigest('1'), ConstructionID: "initial"}); !errors.Is(err, ErrCapacity) {
			t.Fatalf("result generations not reserved before Start: %v", err)
		}
		fresh, err := again.Load(context.Background())
		if err != nil || *fresh.Base.Head != *loaded.Base.Head {
			t.Fatal("capacity refusal changed durable head", err)
		}
	})
}

func proofCleanupPlan(t *testing.T, snap Snapshot, mode Mode, steps []PlannedStep) Plan {
	t.Helper()
	in := snap.Document.Constructions[0].Plan
	in.Mode = mode
	in.Steps = steps
	in.Resources = append([]PlannedResource{}, in.Resources...)
	for i := range in.Resources {
		r := findResource(snap.Document, in.Resources[i].Key)
		in.Resources[i].Coordinate = r.Coordinate
		in.Resources[i].VersionFrom = ""
	}
	return mustPlan(t, in)
}
func proofCleanupReceipt(t *testing.T, snap Snapshot, p Plan, pv Preview, a *Authorization, id, construction string, inv Digest, outcomes map[string]MemberOutcome, result ReceiptResult) Receipt {
	t.Helper()
	members := []ReceiptMember{}
	for _, pr := range p.input.Resources {
		r := findResource(snap.Document, pr.Key)
		outcome := outcomes[pr.Key]
		presence := PresencePresent
		hash := r.ManagedHash
		revision := r.ServerRevision
		if outcome == OutcomeRemoved || outcome == OutcomeAlreadyAbsent {
			presence = PresenceAbsent
			hash = ""
			revision = ""
		}
		o := &Observation{r.Coordinate, presence, hash, proofDigest('8'), inv, revision, []Evidence{{"closing-read", proofDigest('e')}}}
		if outcome == OutcomeUnresolved {
			o = nil
		}
		members = append(members, ReceiptMember{r.Key, r.Coordinate, outcome, o, []Evidence{{"decision", proofDigest('f')}}})
	}
	return Receipt{ID: id, ConstructionID: construction, Mode: p.input.Mode, Result: result, BindingDigest: custodyBindingHash(proofBinding()), Input: p.Digest(), Preview: pv.Digest(), Inventory: inv, Namespaces: []string{"planner", "source"}, Members: members, OperatorApproval: a.approval.hash, ObservedMS: time.Now().UnixMilli()}
}
func proofClassify(t *testing.T, s *LockedState, snap Snapshot, a *Authorization, construction, key string, reason CleanupReason, inv Digest) Snapshot {
	t.Helper()
	r := findResource(snap.Document, key)
	var obs *Observation
	if reason != CleanupUnresolved {
		o := Observation{r.Coordinate, PresencePresent, r.ManagedHash, proofDigest('8'), inv, r.ServerRevision, []Evidence{{"local-read", proofDigest('e')}}}
		if reason == CleanupAlreadyAbsent {
			o.Presence = PresenceAbsent
			o.ManagedHash = ""
			o.ServerRevision = ""
		}
		obs = &o
	}
	cl := &CleanupClassification{key, r.Coordinate, *snap.Base.Head, reason, obs, inv, []Evidence{{"reviewed-disposition", proofDigest('f')}}}
	return proofCommit(t, s, snap, a, Change{Kind: ChangeClassify, ExpectedInventory: inv, ConstructionID: construction, Classification: cl})
}

func TestFoundationDeleteRequiresWholeHistoricalConsumerClosure(t *testing.T) {
	s := proofStore(t, proofDirectory(t), false)
	snap, _ := proofApply(t, s)
	inv := proofDigest('6')
	// No earlier planned step can mask this check: the first requested delete is
	// blocked by enabled consumers in historical custody, not by a step counter.
	p := proofCleanupPlan(t, snap, ModeTeardown, []PlannedStep{{1, "c", OpDelete}})
	a, _ := proofAuthorize(t, s, p, ModeTeardown, snap.Base, inv, "unsafe-delete-approval", "", time.Now().Add(time.Minute))
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeStart, ExpectedInventory: inv, ConstructionID: "unsafe-delete"})
	desired, e := NewAuthoredSpec([]byte(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Commit(context.Background(), *snap.Base.Head, a, Change{Kind: ChangeIntent, ExpectedInventory: inv, ConstructionID: "unsafe-delete", AttemptID: "unsafe-delete-1", Step: 1, Intent: &IntentEvidence{desired, proofDigest('2'), []Coordinate{}}}); !errors.Is(e, ErrConflict) {
		t.Fatalf("enabled historical consumer closure did not fence first delete: %v", e)
	}
	check, e := s.Load(context.Background())
	if e != nil || check.Document.Pending != nil || *check.Base.Head != *snap.Base.Head {
		t.Fatal("refused prerequisite wrote an intent")
	}
}

func TestFoundationCleanupOperationOrderAndHistoricalCustody(t *testing.T) {
	s := proofStore(t, proofDirectory(t), false)
	snap, original := proofApply(t, s)
	inv := proofDigest('6')
	// Creation order is deliberately an INVALID cleanup order.
	bad := []PlannedStep{{1, "e", OpDelete}, {2, "c", OpDelete}, {3, "r", OpDisable}, {4, "a", OpDisable}, {5, "r", OpDelete}, {6, "a", OpDelete}}
	in := snap.Document.Constructions[0].Plan
	in.Mode = ModeTeardown
	in.Resources = append([]PlannedResource{}, in.Resources...)
	for i := range in.Resources {
		in.Resources[i].Coordinate = findResource(snap.Document, in.Resources[i].Key).Coordinate
	}
	in.Steps = bad
	if _, e := NewPlan(in); e == nil {
		t.Fatal("creation graph order admitted for cleanup")
	}
	in.Steps = []PlannedStep{{1, "r", OpDisable}, {2, "a", OpDisable}, {3, "e", OpDelete}, {4, "c", OpDelete}, {5, "r", OpDelete}, {6, "a", OpDelete}}
	if _, e := NewPlan(in); e == nil {
		t.Fatal("disable phase alone hid dependency-first delete order")
	}
	steps := []PlannedStep{{1, "r", OpDisable}, {2, "a", OpDisable}, {3, "a", OpDelete}, {4, "r", OpDelete}, {5, "c", OpDelete}, {6, "e", OpDelete}}
	p := proofCleanupPlan(t, snap, ModeTeardown, steps)
	a, pv := proofAuthorize(t, s, p, ModeTeardown, snap.Base, inv, "cleanup-approval", "", time.Now().Add(time.Minute))
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeStart, ExpectedInventory: inv, ConstructionID: "cleanup"})
	if snap.Document.Receipts[0].InvalidatedBy != "cleanup" {
		t.Fatal("cleanup did not irreversibly invalidate receipt")
	}
	// Cannot jump directly to a delete while another consumer is still enabled.
	r := findResource(snap.Document, "a")
	deps := []Coordinate{findResource(snap.Document, "r").Coordinate, findResource(snap.Document, "c").Coordinate}
	if _, e := s.Commit(context.Background(), *snap.Base.Head, a, Change{Kind: ChangeIntent, ExpectedInventory: inv, ConstructionID: "cleanup", AttemptID: "early-delete", Step: 3, Intent: &IntentEvidence{r.Authored, proofDigest('2'), deps}}); e == nil {
		t.Fatal("whole-consumer disable bypassed")
	}
	for i := range steps {
		snap = proofIntent(t, s, snap, a, uint32(i+1), inv)
		next := proofDigest(byte('a' + i))
		snap = proofObserveStep(t, s, snap, a, inv, next)
		inv = next
	}
	outcomes := map[string]MemberOutcome{"e": OutcomeRemoved, "c": OutcomeRemoved, "r": OutcomeRemoved, "a": OutcomeRemoved}
	receipt := proofCleanupReceipt(t, snap, p, pv, a, "removed", "cleanup", inv, outcomes, ReceiptFullyRemoved)
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeFinish, ExpectedInventory: inv, ConstructionID: "cleanup", Receipt: &receipt})
	if len(snap.Document.Reservations) != 2 {
		t.Fatal("cleanup released real-only history")
	}
	for _, r := range snap.Document.Resources {
		if r.Status != ResourceRemoved || r.Authored.Digest() != original.input.Resources[0].Spec.Digest() || len(r.Acquisition) == 0 || r.FirstAttempt == "" || r.ManagedHash == "" || len(r.Observations) < 2 {
			t.Fatalf("lost acquisition/tombstone: %+v", r)
		}
	}
	if snap.Document.Resources[2].ManagedHash != proofDigest('b') {
		t.Fatal("disable did not advance LAST PRESENT hash before absence")
	}
	// An invalidated receipt cannot be revived by a new unchanged apply pass.
	noop, _ := proofAuthorize(t, s, original, ModeApply, snap.Base, inv, "noop-after-cleanup", "initial", time.Now().Add(time.Minute))
	if _, e := s.Commit(context.Background(), *snap.Base.Head, noop, Change{Kind: ChangeStart, ExpectedInventory: inv, ConstructionID: "initial"}); e == nil {
		t.Fatal("invalidated receipt revived")
	}
	if _, e := s.Load(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestFoundationCleanupLocalAbsenceRetentionAndNoop(t *testing.T) {
	s := proofStore(t, proofDirectory(t), false)
	input := proofInput(t)
	input.Resources = append(input.Resources, PlannedResource{Key: "suite", Origin: input.Targets[0], Coordinate: Coordinate{Kind: KindSuite, ID: "suite-1"}, Spec: input.Resources[0].Spec, Dependencies: []string{"e"}, Namespaces: []string{"planner"}})
	input.Steps = append(input.Steps, PlannedStep{5, "suite", OpCreate})
	snap, original := proofApplyInput(t, s, input)
	inv := proofDigest('7')
	// Unchanged apply appends a new live-pass receipt, never rewriting acquisition.
	old := snap.Document.Constructions[0]
	a, pv := proofAuthorize(t, s, original, ModeApply, snap.Base, inv, "noop-approval", "initial", time.Now().Add(time.Minute))
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeStart, ExpectedInventory: inv, ConstructionID: "initial"})
	outcomes := map[string]MemberOutcome{}
	for _, r := range snap.Document.Resources {
		outcomes[r.Key] = OutcomePresent
	}
	rec := proofCleanupReceipt(t, snap, original, pv, a, "noop-receipt", "initial", inv, outcomes, ReceiptApplyObserved)
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeFinish, ExpectedInventory: inv, ConstructionID: "initial", Receipt: &rec})
	if len(snap.Document.Receipts) != 2 || snap.Document.Constructions[0].Preview != old.Preview || snap.Document.Constructions[0].Approval != old.Approval {
		t.Fatal("noop overwrote original authority")
	}
	// Cleanup's already_absent/local retention decisions have NO dispatch or fake
	// attempted step. The suite keeps a real retained dependency on its evaluator.
	p := proofCleanupPlan(t, snap, ModeTeardown, []PlannedStep{{1, "c", OpDelete}})
	a, pv = proofAuthorize(t, s, p, ModeTeardown, snap.Base, inv, "retain-approval", "", time.Now().Add(time.Minute))
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeStart, ExpectedInventory: inv, ConstructionID: "retained"})
	for _, key := range []string{"e", "r", "a", "suite"} {
		reason := CleanupRetainedDependency
		if key == "suite" {
			reason = CleanupRetainedUnsupported
		}
		snap = proofClassify(t, s, snap, a, "retained", key, reason, inv)
	}
	snap = proofClassify(t, s, snap, a, "retained", "c", CleanupAlreadyAbsent, inv)
	c := snap.Document.Constructions[1]
	if c.Steps[0].Status != StepClassified || c.Steps[0].AttemptID != "" || snap.Document.Pending != nil {
		t.Fatal("local absence fabricated remote attempt")
	}
	if _, e := s.ClaimDispatch(context.Background(), *snap.Base.Head, a, "fabricated"); e == nil {
		t.Fatal("classification produced dispatch permission")
	}
	outcomes = map[string]MemberOutcome{"e": OutcomeRetainedDependency, "r": OutcomeRetainedDependency, "a": OutcomeRetainedDependency, "suite": OutcomeRetainedUnsupported, "c": OutcomeAlreadyAbsent}
	rec = proofCleanupReceipt(t, snap, p, pv, a, "incomplete", "retained", inv, outcomes, ReceiptFullyRemoved)
	if _, e := s.Commit(context.Background(), *snap.Base.Head, a, Change{Kind: ChangeFinish, ExpectedInventory: inv, ConstructionID: "retained", Receipt: &rec}); e == nil {
		t.Fatal("retained configuration labelled full teardown")
	}
	rec.Result = ReceiptIncomplete
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeFinish, ExpectedInventory: inv, ConstructionID: "retained", Receipt: &rec})
	bad := *snap.Document
	bad.Receipts = append([]Receipt{}, bad.Receipts...)
	bad.Receipts[len(bad.Receipts)-1].Result = ReceiptFullyRemoved
	raw, e := custodyDocumentBytes(bad)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = custodyDocumentDecode(raw); e == nil {
		t.Fatal("well-checksummed false teardown receipt accepted")
	}
	if !snap.Document.Constructions[1].Complete || snap.Document.Resources[1].Status != ResourceRemoved || len(snap.Document.Reservations) != 2 {
		t.Fatal("settled incomplete teardown lost history")
	}
}

func proofRecoveryJSON(t *testing.T, pv Preview, id string, snap Snapshot, coordinate Coordinate, disposition Disposition) []byte {
	t.Helper()
	p := snap.Document.Pending
	coord := map[string]any{"kind": coordinate.Kind, "id": coordinate.ID, "parent_id": coordinate.ParentID, "version": coordinate.Version, "unassigned": coordinate.Unassigned}
	recovery := map[string]any{"attempt_id": p.AttemptID, "original_input": p.Input, "original_preview": p.Preview, "original_approval": p.OriginalApproval, "resource_key": p.ResourceKey, "coordinate": coord, "operation": p.Operation, "authored_hash": findResource(snap.Document, p.ResourceKey).Authored.Digest(), "expected_managed_hash": proofDigest('a'), "exact_read": proofDigest('2'), "inventory": proofDigest('9'), "attribution": []any{map[string]any{"id": "original-request-association", "digest": proofDigest('f')}}, "disposition": disposition}
	return proofApprovalJSON(t, pv, id, p.ConstructionID, time.Now().Add(-time.Second), time.Now().Add(time.Minute), recovery)
}
func TestFoundationRestartRecoveryIsNotReplay(t *testing.T) {
	dir := proofDirectory(t)
	s := proofStore(t, dir, false)
	snap, a, p, _ := proofBootstrap(t, s)
	snap = proofIntent(t, s, snap, a, 1, proofDigest('1'))
	pending := snap.Document.Pending
	// May have dispatched; claim is consumed even though no backend is part of B.
	g, e := s.ClaimDispatch(context.Background(), *snap.Base.Head, a, pending.AttemptID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ReleaseDispatch(g); e != nil {
		t.Fatal(e)
	}
	s.Close()
	next := proofStore(t, dir, true)
	snap, e = next.Load(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = next.ClaimDispatch(context.Background(), *snap.Base.Head, a, pending.AttemptID); e == nil {
		t.Fatal("restart replayed original dispatch")
	}
	pv, e := NewPreview(p, ModeRecover, snap.Base, proofDigest('9'))
	if e != nil {
		t.Fatal(e)
	}
	raw := proofRecoveryJSON(t, pv, "recover", snap, pending.Coordinate, DispositionObserved)
	// Same namespace/name/spec does not rebind the caller-assigned exact ID.
	var object map[string]any
	if e = json.Unmarshal(raw, &object); e != nil {
		t.Fatal(e)
	}
	object["recovery"].(map[string]any)["coordinate"].(map[string]any)["id"] = "foreign"
	wrong, _ := json.Marshal(object)
	bad, e := DecodeApproval(wrong)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = next.Authorize(context.Background(), bad, pv, proofDigest('9')); e == nil {
		t.Fatal("foreign recovery coordinate accepted")
	}
	approval, e := DecodeApproval(raw)
	if e != nil {
		t.Fatal(e)
	}
	auth, e := next.Authorize(context.Background(), approval, pv, proofDigest('9'))
	if e != nil {
		t.Fatal(e)
	}
	obs := &Observation{pending.Coordinate, PresencePresent, proofDigest('a'), proofDigest('2'), proofDigest('9'), "", []Evidence{{"attributed-read", proofDigest('f')}}}
	snap = proofCommit(t, next, snap, auth, Change{Kind: ChangeResolve, ExpectedInventory: proofDigest('9'), ConstructionID: "initial", AttemptID: pending.AttemptID, Step: 1, Observation: obs})
	if snap.Document.Pending != nil || snap.Document.Resources[0].Status != ResourceOwned || snap.Document.Constructions[0].Complete {
		t.Fatal("recovery fabricated completion or lost original custody")
	}
	// Fresh preview/approval required to continue only never-attempted suffix.
	suffix, _ := proofAuthorize(t, next, p, ModeRecover, snap.Base, proofDigest('9'), "suffix", "initial", time.Now().Add(time.Minute))
	snap = proofCommit(t, next, snap, suffix, Change{Kind: ChangeStart, ExpectedInventory: proofDigest('9'), ConstructionID: "initial"})
	if _, e = next.Commit(context.Background(), *snap.Base.Head, suffix, Change{Kind: ChangeIntent, ExpectedInventory: proofDigest('9'), ConstructionID: "initial", AttemptID: "retry", Step: 1, Intent: &IntentEvidence{p.input.Resources[0].Spec, proofDigest('2'), []Coordinate{}}}); e == nil {
		t.Fatal("recovery resent completed step")
	}
	snap = proofIntent(t, next, snap, suffix, 2, proofDigest('9'))
	if snap.Document.Pending.ResourceKey != "c" || len(snap.Document.Consumed) != 3 {
		t.Fatal("suffix changed plan or lost consumed approval")
	}
	// A pending slot is indispensable, even with a correctly recomputed checksum.
	broken := *snap.Document
	broken.Pending = nil
	b, e := custodyDocumentBytes(broken)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = custodyDocumentDecode(b); e == nil {
		t.Fatal("missing pending intent accepted")
	}
}

func TestFoundationProcessCrashStages(t *testing.T) {
	for _, mode := range []string{"crash-file-sync", "crash-rename", "crash-dir-sync", "crash-enrollment"} {
		t.Run(mode, func(t *testing.T) {
			dir := proofDirectory(t)
			child := proofChild(t, dir, mode)
			ack := proofLine(t, child)
			if !strings.HasPrefix(ack, "ACK ") {
				t.Fatal(ack)
			}
			e := child.cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 23 {
				t.Fatalf("expected acknowledged bounded crash: %v", e)
			}
			child.cancel()
			// Actual process death releases flock; disk, not the old heap, owns custody.
			s := proofStore(t, dir, true)
			snap, e := s.Load(context.Background())
			switch mode {
			case "crash-file-sync":
				if !errors.Is(e, ErrOutcomeUnknown) {
					t.Fatalf("orphan temp treated pristine: %v", e)
				}
			case "crash-enrollment":
				if !errors.Is(e, ErrEnrolledMissing) {
					t.Fatalf("partial enrollment reset: %v", e)
				}
			default:
				if e != nil {
					t.Fatal(e)
				}
				if snap.Document.Pending == nil || snap.Base.Head.Generation != 2 || len(snap.Document.Reservations) != 2 {
					t.Fatal("post-rename crash lost durable intent/history")
				}
			}
		})
	}
}

func TestFoundationUnresolvedClassificationNeedsFreshRecovery(t *testing.T) {
	s := proofStore(t, proofDirectory(t), false)
	snap, _ := proofApply(t, s)
	inv := proofDigest('6')
	p := proofCleanupPlan(t, snap, ModeTeardown, nil)
	a, pv := proofAuthorize(t, s, p, ModeTeardown, snap.Base, inv, "uncertain-approval", "", time.Now().Add(time.Minute))
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeStart, ExpectedInventory: inv, ConstructionID: "uncertain"})
	for _, key := range []string{"c", "r", "a"} {
		snap = proofClassify(t, s, snap, a, "uncertain", key, CleanupRetainedDependency, inv)
	}
	snap = proofClassify(t, s, snap, a, "uncertain", "e", CleanupUnresolved, inv)
	outcomes := map[string]MemberOutcome{"c": OutcomeRetainedDependency, "r": OutcomeRetainedDependency, "a": OutcomeRetainedDependency, "e": OutcomeUnresolved}
	rec := proofCleanupReceipt(t, snap, p, pv, a, "uncertain-receipt", "uncertain", inv, outcomes, ReceiptIncomplete)
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeFinish, ExpectedInventory: inv, ConstructionID: "uncertain", Receipt: &rec})
	if snap.Document.Constructions[1].Complete {
		t.Fatal("unknown local observation fabricated completion")
	}
	renewed, renewPreview := proofAuthorize(t, s, p, ModeRecover, snap.Base, inv, "renewed", "uncertain", time.Now().Add(time.Minute))
	snap = proofCommit(t, s, snap, renewed, Change{Kind: ChangeStart, ExpectedInventory: inv, ConstructionID: "uncertain"})
	snap = proofClassify(t, s, snap, renewed, "uncertain", "e", CleanupAlreadyAbsent, inv)
	outcomes["e"] = OutcomeAlreadyAbsent
	rec = proofCleanupReceipt(t, snap, p, renewPreview, renewed, "settled-receipt", "uncertain", inv, outcomes, ReceiptIncomplete)
	snap = proofCommit(t, s, snap, renewed, Change{Kind: ChangeFinish, ExpectedInventory: inv, ConstructionID: "uncertain", Receipt: &rec})
	if !snap.Document.Constructions[1].Complete || len(snap.Document.Constructions[1].Classifications) != 5 {
		t.Fatal("recovery erased prior uncertainty or failed to settle explicit observation")
	}
}

func TestFoundationDeactivationIsNotFullRemoval(t *testing.T) {
	s := proofStore(t, proofDirectory(t), false)
	snap, _ := proofApply(t, s)
	inv := proofDigest('6')
	p := proofCleanupPlan(t, snap, ModeDeactivate, []PlannedStep{{1, "r", OpDisable}, {2, "a", OpDisable}})
	a, pv := proofAuthorize(t, s, p, ModeDeactivate, snap.Base, inv, "deactivate-approval", "", time.Now().Add(time.Minute))
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeStart, ExpectedInventory: inv, ConstructionID: "deactivated"})
	for i := range p.input.Steps {
		snap = proofIntent(t, s, snap, a, uint32(i+1), inv)
		if string(snap.Document.Pending.Desired.JSON()) != `{"enabled":false}` {
			t.Fatal("fixture did not store exact disable intent")
		}
		next := proofDigest(byte('a' + i))
		snap = proofObserveStep(t, s, snap, a, inv, next)
		inv = next
	}
	for _, key := range []string{"e", "c"} {
		snap = proofClassify(t, s, snap, a, "deactivated", key, CleanupRetainedDependency, inv)
	}
	rec := proofCleanupReceipt(t, snap, p, pv, a, "deactivation-receipt", "deactivated", inv, map[string]MemberOutcome{"r": OutcomePresentDisabled, "a": OutcomePresentDisabled, "e": OutcomeRetainedDependency, "c": OutcomeRetainedDependency}, ReceiptDeactivated)
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeFinish, ExpectedInventory: inv, ConstructionID: "deactivated", Receipt: &rec})
	last := snap.Document.Receipts[len(snap.Document.Receipts)-1]
	if last.Mode != ModeDeactivate || last.Result != ReceiptDeactivated || last.Result == ReceiptApplyObserved || len(snap.Document.Reservations) != 2 {
		t.Fatal("deactivation relabelled full/execution authority")
	}
}

func TestFoundationUnassignedNoncommitKeepsReservation(t *testing.T) {
	dir := proofDirectory(t)
	s := proofStore(t, dir, false)
	snap, a, p, _ := proofBootstrap(t, s)
	snap = proofIntent(t, s, snap, a, 1, proofDigest('1'))
	snap = proofObserveStep(t, s, snap, a, proofDigest('1'), proofDigest('3'))
	snap = proofIntent(t, s, snap, a, 2, proofDigest('3'))
	pending := snap.Document.Pending
	if !pending.Coordinate.Unassigned {
		t.Fatal("fixture must have no fabricated server ID")
	}
	s.Close()
	next := proofStore(t, dir, true)
	snap, e := next.Load(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	pv, e := NewPreview(p, ModeRecover, snap.Base, proofDigest('9'))
	if e != nil {
		t.Fatal(e)
	}
	// The operator supplies independent original-attempt/no-inflight evidence. The
	// file layer binds it; it does not infer noncommit from absence or cancellation.
	raw := proofRecoveryJSON(t, pv, "not-applied", snap, pending.Coordinate, DispositionNotApplied)
	approval, e := DecodeApproval(raw)
	if e != nil {
		t.Fatal(e)
	}
	auth, e := next.Authorize(context.Background(), approval, pv, proofDigest('9'))
	if e != nil {
		t.Fatal(e)
	}
	snap = proofCommit(t, next, snap, auth, Change{Kind: ChangeResolve, ExpectedInventory: proofDigest('9'), ConstructionID: "initial", AttemptID: pending.AttemptID, Step: 2})
	r := findResource(snap.Document, "c")
	if r.Status != ResourceNotApplied || !r.Coordinate.Unassigned || r.Coordinate.ID != "" || len(snap.Document.Reservations) != 2 || snap.Document.Pending != nil || snap.Document.Constructions[0].Complete {
		t.Fatal("not-applied discarded identity/history or fabricated completion")
	}
	// A separately reviewed, independent suffix may proceed; the failed create
	// remains a tombstone and cannot be retried or used as a dependency.
	suffix, suffixPreview := proofAuthorize(t, next, p, ModeRecover, snap.Base, proofDigest('9'), "independent-suffix", "initial", time.Now().Add(time.Minute))
	snap = proofCommit(t, next, snap, suffix, Change{Kind: ChangeStart, ExpectedInventory: proofDigest('9'), ConstructionID: "initial"})
	if _, e = next.Commit(context.Background(), *snap.Base.Head, suffix, Change{Kind: ChangeIntent, ExpectedInventory: proofDigest('9'), ConstructionID: "initial", AttemptID: "retry", Step: 2, Intent: &IntentEvidence{p.input.Resources[1].Spec, proofDigest('2'), []Coordinate{}}}); e == nil {
		t.Fatal("not-applied attempt retried")
	}
	snap = proofIntent(t, next, snap, suffix, 3, proofDigest('9'))
	snap = proofObserveStep(t, next, snap, suffix, proofDigest('9'), proofDigest('9'))
	deps := []Coordinate{findResource(snap.Document, "r").Coordinate, findResource(snap.Document, "c").Coordinate}
	if _, e = next.Commit(context.Background(), *snap.Base.Head, suffix, Change{Kind: ChangeIntent, ExpectedInventory: proofDigest('9'), ConstructionID: "initial", AttemptID: "ghost", Step: 4, Intent: &IntentEvidence{p.input.Resources[3].Spec, proofDigest('2'), deps}}); e == nil {
		t.Fatal("failed dependency admitted ghost work")
	}
	members := []ReceiptMember{}
	for _, resource := range snap.Document.Resources {
		coordinate := resource.Coordinate
		if coordinate.Unassigned {
			coordinate.Unassigned = false
			coordinate.ID = "fabricated-" + resource.Key
		}
		o := Observation{coordinate, PresencePresent, proofDigest('a'), proofDigest('2'), proofDigest('9'), "", []Evidence{{"claimed-read", proofDigest('e')}}}
		members = append(members, ReceiptMember{resource.Key, coordinate, OutcomePresent, &o, []Evidence{{"claimed-closure", proofDigest('e')}}})
	}
	receipt := Receipt{ID: "false-apply", ConstructionID: "initial", Mode: ModeApply, Result: ReceiptApplyObserved, Input: p.Digest(), Preview: suffixPreview.Digest(), OperatorApproval: suffix.approval.hash, BindingDigest: custodyBindingHash(proofBinding()), Inventory: proofDigest('9'), Namespaces: []string{"planner", "source"}, Members: members, ObservedMS: time.Now().UnixMilli()}
	if _, e = next.Commit(context.Background(), *snap.Base.Head, suffix, Change{Kind: ChangeFinish, ExpectedInventory: proofDigest('9'), ConstructionID: "initial", Receipt: &receipt}); e == nil {
		t.Fatal("incomplete apply fabricated success receipt")
	}
	loaded, e := next.Load(context.Background())
	if e != nil || loaded.Document.Pending != nil || loaded.Document.Constructions[0].Steps[3].Status != StepPlanned || loaded.Document.Constructions[0].Complete || len(loaded.Document.Receipts) != 0 || findResource(loaded.Document, "c").Status != ResourceNotApplied {
		t.Fatal("suffix changed failed/dependent custody", e)
	}
}

func TestFoundationAuthorityRejectionBeforeEnrollment(t *testing.T) {
	s := proofStore(t, proofDirectory(t), false)
	p := mustPlan(t, proofInput(t))
	pv, e := NewPreview(p, ModeApply, Base{Pristine: true}, proofDigest('1'))
	if e != nil {
		t.Fatal(e)
	}
	for _, test := range []string{"namespace", "destination", "future", "expired", "inventory"} {
		t.Run(test, func(t *testing.T) {
			raw := proofApprovalJSON(t, pv, "invalid-"+test, "", time.Now().Add(-time.Second), time.Now().Add(time.Minute), nil)
			var obj map[string]any
			json.Unmarshal(raw, &obj)
			switch test {
			case "namespace":
				obj["namespaces"] = []string{"context"}
			case "destination":
				obj["binding"].(map[string]any)["destination"].(map[string]any)["runtime_tenant"] = "foreign"
			case "future":
				obj["issued_ms"] = time.Now().Add(time.Second).UnixMilli()
			case "expired":
				obj["issued_ms"] = time.Now().Add(-time.Minute).UnixMilli()
				obj["expires_ms"] = time.Now().Add(-time.Second).UnixMilli()
			}
			raw, _ = json.Marshal(obj)
			a, e := DecodeApproval(raw)
			if e != nil {
				t.Fatal(e)
			}
			inv := proofDigest('1')
			if test == "inventory" {
				inv = proofDigest('2')
			}
			if _, e = s.Authorize(context.Background(), a, pv, inv); e == nil {
				t.Fatal("misbound authority accepted")
			}
			snap, e := s.Load(context.Background())
			if e != nil || !snap.Base.Pristine {
				t.Fatal("rejected approval enrolled destination")
			}
		})
	}
	snap, _, original, _ := proofBootstrap(t, s)
	fresh, e := NewPreview(original, ModeRecover, snap.Base, proofDigest('1'))
	if e != nil {
		t.Fatal(e)
	}
	raw := proofApprovalJSON(t, fresh, "initial", "initial", time.Now().Add(-time.Second), time.Now().Add(time.Minute), nil)
	a, e := DecodeApproval(raw)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authorize(context.Background(), a, fresh, proofDigest('1')); !errors.Is(e, ErrConsumed) {
		t.Fatalf("reused approval ID: %v", e)
	}
}

func TestFoundationPoisonCannotJoinGrant(t *testing.T) {
	s := proofStore(t, proofDirectory(t), false)
	snap, a, _, _ := proofBootstrap(t, s)
	snap = proofIntent(t, s, snap, a, 1, proofDigest('1'))
	pending := snap.Document.Pending
	grant, e := s.ClaimDispatch(context.Background(), *snap.Base.Head, a, pending.AttemptID)
	if e != nil {
		t.Fatal(e)
	}
	cancelled := make(chan struct{})
	allow := make(chan struct{})
	joined := make(chan struct{})
	go func() { defer close(joined); <-grant.Context().Done(); close(cancelled); <-allow }()
	s.edges.sync = func(fd int, stage string) error {
		if stage == "state.json-dir-sync" {
			return unix.EIO
		}
		return unix.Fsync(fd)
	}
	_, e = s.Commit(context.Background(), *snap.Base.Head, a, Change{Kind: ChangeResponse, ExpectedInventory: proofDigest('1'), ConstructionID: "initial", AttemptID: pending.AttemptID, Step: 1, Response: &ResponseEvidence{pending.Coordinate, []Evidence{{"received", proofDigest('e')}}, nil}})
	if !errors.Is(e, ErrOutcomeUnknown) {
		t.Fatalf("response durability uncertainty: %v", e)
	}
	<-cancelled
	if e = s.Close(); !errors.Is(e, ErrBusy) {
		t.Fatalf("poison released live sender: %v", e)
	}
	probe := proofChild(t, s.location.Directory, "probe")
	if line := proofLine(t, probe); line != "LOCKED" {
		t.Fatal("poison cancelled native flock")
	}
	probe.wait(t)
	close(allow)
	<-joined
	if e = s.ReleaseDispatch(grant); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
}
