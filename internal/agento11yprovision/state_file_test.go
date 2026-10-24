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
	"runtime"
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

// A caller context that ends mid-publication leaves the commit outcome unknown
// and poisons the store, and the cancellation stays visible in the error chain,
// so an interrupted commit is not mistaken for a durability fault. The edge
// hook ends the context at an exact publication stage, after the entry checks.
func TestFoundationInterruptedPublicationKeepsCause(t *testing.T) {
	for _, stage := range []string{"state.json-file-sync", "state.json-dir-sync"} {
		t.Run(stage, func(t *testing.T) {
			s := proofStore(t, proofDirectory(t), false)
			snap, a, p, _ := proofBootstrap(t, s)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reached := false
			s.edges.after = func(at string) error {
				if at == stage {
					cancel()
					reached = true
				}
				return nil
			}
			_, e := s.Commit(ctx, *snap.Base.Head, a, Change{Kind: ChangeIntent, ExpectedInventory: proofDigest('1'), ConstructionID: "initial", AttemptID: "interrupted", Step: 1, Intent: &IntentEvidence{p.input.Resources[0].Spec, proofDigest('2'), []Coordinate{}}})
			if !reached {
				t.Fatalf("interruption edge %s not reached: %v", stage, e)
			}
			if !errors.Is(e, ErrOutcomeUnknown) {
				t.Fatalf("interrupted publication lost its unknown outcome: %v", e)
			}
			if !errors.Is(e, context.Canceled) {
				t.Fatalf("interrupted publication dropped its cause: %v", e)
			}
			if _, e = s.Authorize(context.Background(), a.approval, a.preview, proofDigest('1')); !errors.Is(e, ErrOutcomeUnknown) {
				t.Fatalf("interrupted publication did not poison authority: %v", e)
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

// D62 is a state-only resource contract, not vendor/HTTP acceptance. These
// fixtures use only published B methods and real files, never C test helpers.
const proofStateV2Revision = "agento11yprovision-state-3264k-v2"
const proofStateV2Bytes = 3342336

func proofStateStore(t *testing.T, dir string, binding Binding) *LockedState {
	t.Helper()
	s, e := AcquireState(context.Background(), Location{Directory: dir}, binding)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	})
	return s
}
func proofStateAuthorize(t *testing.T, s *LockedState, p Plan, mode Mode, base Base, id, construction string) (*Authorization, Preview) {
	t.Helper()
	pv, e := NewPreview(p, mode, base, proofDigest('1'))
	if e != nil {
		t.Fatal(e)
	}
	raw := proofApprovalJSON(t, pv, id, construction, time.Now().Add(-time.Second), time.Now().Add(10*time.Minute), nil)
	var doc, view map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil || json.Unmarshal(pv.JSON(), &view) != nil {
		t.Fatal("approval fixture")
	}
	doc["binding"] = view["binding"]
	raw, e = json.Marshal(doc)
	if e != nil {
		t.Fatal(e)
	}
	approval, e := DecodeApproval(raw)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Authorize(context.Background(), approval, pv, proofDigest('1'))
	if e != nil {
		t.Fatal(e)
	}
	return a, pv
}
func proofStateEvidence() []Evidence {
	es := make([]Evidence, 16)
	for i := range es {
		id := strings.Repeat("<", 252)
		for bit := 0; bit < 4; bit++ {
			if i&(1<<bit) == 0 {
				id += "<"
			} else {
				id += ">"
			}
		}
		es[i] = Evidence{id, proofDigest('e')}
	}
	return es
}
func proofStatePlan(t *testing.T, all bool) Plan {
	t.Helper()
	in := proofInput(t)
	in.Binding.ContractRevision = proofStateV2Revision
	o := in.Targets[0]
	spec := func(raw string) AuthoredSpec {
		s, e := NewAuthoredSpec([]byte(raw))
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	in.Resources = []PlannedResource{
		{Key: "e", Origin: o, Coordinate: Coordinate{Kind: KindEvaluator, ID: "e-1", Version: "1"}, Spec: spec(`{"evaluator_id":"e-1","version":"1","kind":"regex","config":{"pattern":"fiction"},"output_keys":[{"key":"p","type":"bool"}]}`), Namespaces: []string{"planner"}},
		{Key: "s", Origin: o, Coordinate: Coordinate{Kind: KindSuite, ID: "s-1"}, Spec: spec(`{"suite_id":"s-1","name":"fiction"}`), Namespaces: []string{"planner"}},
		{Key: "d", Origin: o, Coordinate: Coordinate{Kind: KindDraft, ParentID: "s-1", Unassigned: true}, Spec: spec(`{}`), Dependencies: []string{"s"}, Namespaces: []string{"planner"}},
		{Key: "k", Origin: o, Coordinate: Coordinate{Kind: KindCase, ID: "case-1", ParentID: "s-1"}, VersionFrom: "d", Spec: spec(`{"test_case_id":"case-1","input":{"text":"fiction"}}`), Dependencies: []string{"s", "d"}, Namespaces: []string{"planner"}},
		{Key: "b", Origin: o, Coordinate: Coordinate{Kind: KindBindings, ParentID: "s-1"}, VersionFrom: "d", Spec: spec(`{"bindings":[{"binding_id":"b","evaluator_id":"e","evaluator_version":"1","input_mappings":{"response":"test_case.input.text"}}]}`), Dependencies: []string{"s", "d", "e"}, Namespaces: []string{"planner"}},
		{Key: "p", Origin: o, Coordinate: Coordinate{Kind: KindPublication, ParentID: "s-1"}, VersionFrom: "d", Spec: spec(`{"cases":[{"test_case_id":"case-1","input":{"text":"fiction"}}],"bindings":[{"binding_id":"b","evaluator_id":"e","evaluator_version":"1","input_mappings":{"response":"test_case.input.text"}}]}`), Dependencies: []string{"s", "d", "e", "k", "b"}, Namespaces: []string{"planner"}},
	}
	if all {
		in.Resources = append(in.Resources,
			PlannedResource{Key: "c", Origin: o, Coordinate: Coordinate{Kind: KindCollection, Unassigned: true}, Spec: spec(`{"name":"fiction"}`), Namespaces: []string{"planner"}},
			PlannedResource{Key: "r", Origin: o, Coordinate: Coordinate{Kind: KindRule, ID: "r-1"}, Spec: spec(`{"rule_id":"r-1","enabled":true,"selector":"user_visible_turn","match":{"agent_name":"Planner"},"evaluator_ids":["e"]}`), Dependencies: []string{"e"}, Namespaces: []string{"planner"}},
			PlannedResource{Key: "h", Origin: o, Coordinate: Coordinate{Kind: KindHook, ID: "h-1"}, Spec: spec(`{"rule_id":"h-1","enabled":true,"phase":"preflight","match":{"agent_name":"Planner"},"evaluator_ids":["e"]}`), Dependencies: []string{"e"}, Namespaces: []string{"planner"}},
			PlannedResource{Key: "a", Origin: o, Coordinate: Coordinate{Kind: KindAction, ParentID: "r-1", Unassigned: true}, Spec: spec(`{"condition":{"kind":"all_evaluators_pass"},"action_config":{"kind":"add_to_collection","collection_ids":["c"]},"enabled":true}`), Dependencies: []string{"r", "c"}, Namespaces: []string{"planner"}},
		)
	}
	in.Steps = nil
	for i, r := range in.Resources {
		op := OpCreate
		if r.Coordinate.Kind == KindBindings {
			op = OpInitialBindings
		}
		if r.Coordinate.Kind == KindPublication {
			op = OpPublish
		}
		in.Steps = append(in.Steps, PlannedStep{uint32(i + 1), r.Key, op})
	}
	return mustPlan(t, in)
}

func proofStateStep(t *testing.T, s *LockedState, snap Snapshot, a *Authorization, step uint32) Snapshot {
	t.Helper()
	ctx := context.Background()
	c := snap.Document.Constructions[len(snap.Document.Constructions)-1]
	ps := c.Plan.Steps[step-1]
	pr := planResource(c.Plan, ps.ResourceKey)
	deps := []Coordinate{}
	for _, key := range pr.Dependencies {
		deps = append(deps, findResource(snap.Document, key).Coordinate)
	}
	// Foundation accepts opaque bounded Desired. Faithful typed substitution is
	// C's separate responsibility; this is the maximum B result-slot contract.
	desired, e := NewAuthoredSpec([]byte(`{"x":"` + strings.Repeat("a", 65536-8) + `"}`))
	if e != nil {
		t.Fatal(e)
	}
	attempt := strings.Repeat("<", 248) + fmt.Sprintf("%08d", snap.Base.Head.Generation)
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeIntent, ExpectedInventory: proofDigest('1'), ConstructionID: c.ID, AttemptID: attempt, Step: step, Intent: &IntentEvidence{desired, proofDigest('2'), deps}})
	pending := snap.Document.Pending
	if len(pending.Desired.JSON()) != 65536 {
		t.Fatal("maximum Desired lost")
	}
	g, e := s.ClaimDispatch(ctx, *snap.Base.Head, a, attempt)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimDispatch(ctx, *snap.Base.Head, a, attempt); e == nil {
		t.Fatal("grant reused")
	}
	if e = s.Close(); !errors.Is(e, ErrBusy) {
		t.Fatal("outstanding sender not held", e)
	}
	joined := make(chan struct{})
	go func() { defer close(joined); _ = g.Context().Err() }()
	<-joined
	coordinate := pending.Coordinate
	if coordinate.Unassigned {
		coordinate.Unassigned = false
		coordinate.ID = strings.Repeat("<", 256)
		if coordinate.Kind == KindDraft {
			coordinate.Version = coordinate.ID
		}
	}
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeResponse, ExpectedInventory: proofDigest('1'), ConstructionID: c.ID, AttemptID: attempt, Step: step, Response: &ResponseEvidence{Coordinate: coordinate, Evidence: proofStateEvidence()}})
	if e = s.ReleaseDispatch(g); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimDispatch(ctx, *snap.Base.Head, a, attempt); e == nil {
		t.Fatal("released grant replay")
	}
	obs := Observation{coordinate, PresencePresent, proofDigest('a'), proofDigest('2'), proofDigest('1'), strings.Repeat("<", 256), proofStateEvidence()}
	if ps.Operation == OpDisable {
		obs.ManagedHash = proofDigest('b')
	}
	if ps.Operation == OpDelete {
		obs.Presence = PresenceAbsent
		obs.ManagedHash = ""
		obs.ServerRevision = ""
	}
	return proofCommit(t, s, snap, a, Change{Kind: ChangeObserve, ExpectedInventory: proofDigest('1'), ConstructionID: c.ID, AttemptID: attempt, Step: step, Observation: &obs})
}
func proofStateFinish(t *testing.T, s *LockedState, snap Snapshot, a *Authorization, p Plan, pv Preview, id string, outcomes map[string]MemberOutcome, result ReceiptResult) Snapshot {
	t.Helper()
	members := []ReceiptMember{}
	for _, pr := range p.input.Resources {
		r := findResource(snap.Document, pr.Key)
		o := Observation{r.Coordinate, PresencePresent, r.ManagedHash, proofDigest('2'), proofDigest('1'), r.ServerRevision, proofStateEvidence()}
		if outcomes[r.Key] == OutcomeRemoved {
			o.Presence = PresenceAbsent
			o.ManagedHash = ""
			o.ServerRevision = ""
		}
		members = append(members, ReceiptMember{r.Key, r.Coordinate, outcomes[r.Key], &o, proofStateEvidence()})
	}
	c := snap.Document.Constructions[len(snap.Document.Constructions)-1]
	receipt := Receipt{ID: id, ConstructionID: c.ID, Mode: p.input.Mode, Result: result, BindingDigest: custodyBindingHash(p.input.Binding), Input: p.Digest(), Preview: pv.Digest(), Inventory: proofDigest('1'), Namespaces: []string{"planner", "source"}, Members: members, OperatorApproval: a.approval.hash, ObservedMS: time.Now().UnixMilli()}
	return proofCommit(t, s, snap, a, Change{Kind: ChangeFinish, ExpectedInventory: proofDigest('1'), ConstructionID: c.ID, Receipt: &receipt})
}
func TestFoundationStateV2Footprints(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprintf("all-ten=%t", all), func(t *testing.T) {
			p := proofStatePlan(t, all)
			dir := proofDirectory(t)
			s := proofStateStore(t, dir, p.input.Binding)
			a, pv := proofStateAuthorize(t, s, p, ModeApply, Base{Pristine: true}, "v2-apply", "")
			snap, e := s.Bootstrap(context.Background(), a)
			if e != nil {
				t.Fatalf("v2 public Bootstrap resources=%d: %v", len(p.input.Resources), e)
			}
			for i := range p.input.Steps {
				snap = proofStateStep(t, s, snap, a, uint32(i+1))
			}
			outcomes := map[string]MemberOutcome{}
			for _, r := range snap.Document.Resources {
				outcomes[r.Key] = OutcomePresent
			}
			snap = proofStateFinish(t, s, snap, a, p, pv, "apply-receipt", outcomes, ReceiptApplyObserved)
			raw, e := os.ReadFile(filepath.Join(dir, "state.json"))
			if e != nil {
				t.Fatal(e)
			}
			if len(raw) <= 786432 {
				t.Fatal("v2 positive did not cross legacy boundary")
			}
			t.Logf("actual completed apply resources=%d bytes=%d", len(snap.Document.Resources), len(raw))
			if all {
				steps := []PlannedStep{{1, "r", OpDisable}, {2, "h", OpDisable}, {3, "a", OpDisable}, {4, "a", OpDelete}, {5, "h", OpDelete}, {6, "r", OpDelete}, {7, "c", OpDelete}, {8, "e", OpDelete}}
				cp := proofCleanupPlan(t, snap, ModeTeardown, steps)
				ca, cv := proofStateAuthorize(t, s, cp, ModeTeardown, snap.Base, "v2-cleanup-approval", "")
				snap = proofCommit(t, s, snap, ca, Change{Kind: ChangeStart, ConstructionID: "v2-cleanup", ExpectedInventory: proofDigest('1')})
				for i := uint32(1); i <= 6; i++ {
					snap = proofStateStep(t, s, snap, ca, i)
				}
				for _, pr := range cp.input.Resources {
					r := findResource(snap.Document, pr.Key)
					if r.Status == ResourceRemoved {
						outcomes[r.Key] = OutcomeRemoved
						continue
					}
					reason := CleanupRetainedUnsupported
					outcomes[r.Key] = OutcomeRetainedUnsupported
					if r.Key == "e" || r.Key == "c" {
						reason = CleanupRetainedDependency
						outcomes[r.Key] = OutcomeRetainedDependency
					}
					o := Observation{r.Coordinate, PresencePresent, r.ManagedHash, proofDigest('2'), proofDigest('1'), r.ServerRevision, proofStateEvidence()}
					cl := CleanupClassification{r.Key, r.Coordinate, *snap.Base.Head, reason, &o, proofDigest('1'), proofStateEvidence()}
					var err error
					snap, err = s.Commit(context.Background(), *snap.Base.Head, ca, Change{Kind: ChangeClassify, ConstructionID: "v2-cleanup", ExpectedInventory: proofDigest('1'), Classification: &cl})
					if err != nil {
						t.Fatalf("v2 classify %s with retained maximum history: %v", r.Key, err)
					}
				}
				snap = proofStateFinish(t, s, snap, ca, cp, cv, "cleanup-receipt", outcomes, ReceiptIncomplete)
				if len(snap.Document.Receipts) != 2 || snap.Document.Receipts[0].InvalidatedBy == "" || len(snap.Document.Reservations) != 2 || len(snap.Document.Resources) != 10 {
					t.Fatal("history omitted or receipt revived")
				}
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			reopened := proofStateStore(t, dir, p.input.Binding)
			loaded, e := reopened.Load(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if *loaded.Base.Head != *snap.Base.Head {
				t.Fatal("v2 persisted head changed")
			}
			for _, r := range loaded.Document.Resources {
				if len(r.Acquisition) != 16 || len(r.Observations) == 0 || len(r.ServerRevision) != 256 {
					t.Fatal("maximum evidence/history lost")
				}
			}
			if all {
				raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("actual retained cleanup bytes=%d classifications=%d observations=%d", len(raw), len(loaded.Document.Constructions[1].Classifications), len(loaded.Document.Resources[0].Observations))
			}
		})
	}
}

// Boundary fixtures retain the real public prefix, then append deliberately
// constructed, fully validated historical observations. They test untrusted
// on-disk input/admission, not a claim that a backend produced those observations.
func proofStateHistory(t *testing.T, kind ResourceKind, classification string, revision ...string) (Document, Plan) {
	t.Helper()
	in := proofInput(t)
	in.Binding.ContractRevision = proofStateV2Revision
	if len(revision) > 0 {
		in.Binding.ContractRevision = revision[0]
	}
	in.Resources = in.Resources[:1]
	in.Resources[0].Key = "member"
	in.Resources[0].Coordinate = Coordinate{Kind: kind, ID: "member"}
	if kind == KindCollection {
		in.Resources[0].Coordinate = Coordinate{Kind: kind, Unassigned: true}
	}
	in.Steps = []PlannedStep{{1, "member", OpCreate}}
	p := mustPlan(t, in)
	s := proofStateStore(t, proofDirectory(t), in.Binding)
	a, pv := proofStateAuthorize(t, s, p, ModeApply, Base{Pristine: true}, "seed-apply", "")
	snap, e := s.Bootstrap(context.Background(), a)
	if e != nil {
		t.Fatal(e)
	}
	snap = proofStateStep(t, s, snap, a, 1)
	snap = proofStateFinish(t, s, snap, a, p, pv, "seed-receipt", map[string]MemberOutcome{"member": OutcomePresent}, ReceiptApplyObserved)
	steps := []PlannedStep{}
	if kind == KindCollection {
		steps = append(steps, PlannedStep{1, "member", OpDelete})
	}
	cp := proofCleanupPlan(t, snap, ModeTeardown, steps)
	ca, _ := proofStateAuthorize(t, s, cp, ModeTeardown, snap.Base, "seed-cleanup", "")
	snap = proofCommit(t, s, snap, ca, Change{Kind: ChangeStart, ConstructionID: "cleanup", ExpectedInventory: proofDigest('1')})
	settle := func(unresolved bool) {
		r := snap.Document.Resources[0]
		o := &Observation{r.Coordinate, PresencePresent, r.ManagedHash, proofDigest('2'), proofDigest('1'), r.ServerRevision, proofStateEvidence()}
		reason := CleanupRetainedDependency
		if unresolved {
			reason = CleanupUnresolved
			o = nil
		}
		cl := CleanupClassification{r.Key, r.Coordinate, *snap.Base.Head, reason, o, proofDigest('1'), proofStateEvidence()}
		snap = proofCommit(t, s, snap, ca, Change{Kind: ChangeClassify, ConstructionID: "cleanup", ExpectedInventory: proofDigest('1'), Classification: &cl})
	}
	if classification != "none" {
		settle(false)
	}
	if classification == "unresolved" || classification == "resettled" {
		settle(true)
	}
	if classification == "resettled" {
		settle(false)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	d := *snap.Document
	d.Generation = 100 // comfortably after the retained prefix; fixed-width boundary fixture
	return d, cp
}
func proofStateRaw(t *testing.T, d Document) []byte {
	t.Helper()
	w := documentToWire(d)
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
	return raw
}
func proofStatePad(t *testing.T, d Document, size int) Document {
	t.Helper()
	copy, e := documentFromWire(documentToWire(d))
	if e != nil {
		t.Fatal(e)
	}
	d = copy
	gap := size - len(proofStateRaw(t, d))
	r := &d.Resources[0]
	o := r.Observations[len(r.Observations)-1]
	o.Evidence = make([]Evidence, 16)
	for i := range o.Evidence {
		o.Evidence[i] = Evidence{fmt.Sprintf("p%02d", i), proofDigest('e')}
	}
	b, _ := json.Marshal(observationToWire(o))
	base := len(b) + 1
	count := (gap + base + 16000 - 1) / (base + 16000)
	if count <= 0 || gap < count*base {
		t.Fatal("padding target cannot hold legal historical records", gap)
	}
	payload := gap - count*base
	for range count {
		next := o
		next.Evidence = append([]Evidence{}, o.Evidence...)
		for i := range next.Evidence {
			n := min(payload, 1000)
			payload -= n
			// Each suffix has <=171 UTF-8 bytes, with exactly n encoded bytes.
			next.Evidence[i].ID += strings.Repeat("<", n/6) + strings.Repeat("a", n%6)
		}
		r.Observations = append(r.Observations, next)
	}
	if payload != 0 || len(proofStateRaw(t, d)) != size {
		t.Fatal("boundary padding not exact")
	}
	if err := validateCustodyDocument(d); err != nil {
		t.Fatal("invalid history fixture", err)
	}
	return d
}
func proofStateInstall(t *testing.T, dir string, d Document) {
	t.Helper()
	// A bound marker is installed through the public Bootstrap first. Only the
	// checksummed state fixture is substituted, like the existing corruption tests.
	in := d.Constructions[0].Plan
	in.Resources = in.Resources[:1]
	in.Steps = []PlannedStep{{1, in.Resources[0].Key, OpCreate}}
	p := mustPlan(t, in)
	s := proofStateStore(t, dir, d.Binding)
	a, _ := proofStateAuthorize(t, s, p, ModeApply, Base{Pristine: true}, "marker-seed", "")
	if _, e := s.Bootstrap(context.Background(), a); e != nil {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "state.json"), proofStateRaw(t, d), 0600); e != nil {
		t.Fatal(e)
	}
}

// Independent frozen-wire maximum for the one-member, no-pending fixtures:
// one next recovery Start, any still-required local classification, then receipt.
// This does not invoke custodyBudget or alter its cap to find a green boundary.
func proofStateRemainingBytes(t *testing.T, d Document, futureClass bool) int {
	t.Helper()
	w := documentToWire(d)
	c := &w.Constructions[len(w.Constructions)-1]
	h := proofDigest('f')
	text := strings.Repeat("<", 256)
	w.Consumed = append(w.Consumed, consumedWire{"boundary", h, c.ID, baseWire{Head: &headWire{d.Generation, h}}})
	w.Generation = math.MaxUint64
	r := &w.Resources[0]
	o := observationWire{r.Coordinate, PresencePresent, h, h, h, text, evidenceToWire(proofStateEvidence())}
	if futureClass {
		c.Classifications = append(c.Classifications, classificationWire{r.Key, r.Coordinate, headWire{math.MaxUint64, h}, CleanupRetainedUnsupported, &o, h, evidenceToWire(proofStateEvidence())})
		r.Observations = append(r.Observations, o)
	}
	member := memberWire{r.Key, r.Coordinate, OutcomeRetainedUnsupported, &o, evidenceToWire(proofStateEvidence())}
	w.Receipts = append(w.Receipts, receiptWire{text, c.ID, c.Mode, ReceiptApplyObserved, h, c.Input, h, h, c.Namespaces, []memberWire{member}, h, math.MaxInt64, text})
	raw, e := json.Marshal(w)
	if e != nil {
		t.Fatal(e)
	}
	return len(raw)
}
func TestFoundationStateV2ForecastBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name           string
		kind           ResourceKind
		classification string
		future         bool
		delta          int
	}{
		{"settled-below", KindSuite, "settled", false, -1},
		{"settled-at", KindSuite, "settled", false, 0},
		{"settled-above", KindSuite, "settled", false, 1},
		{"unresolved-at", KindSuite, "unresolved", true, 0},
		{"unresolved-above", KindSuite, "unresolved", true, 1},
		{"unresolved-unpersistable", KindSuite, "unresolved", true, 4096},
		{"never-classified-above", KindSuite, "none", true, 1},
		{"latest-resettled", KindSuite, "resettled", false, 0},
		{"classified-delete", KindCollection, "settled", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, p := proofStateHistory(t, tc.kind, tc.classification)
			want := len(proofStateRaw(t, d)) + proofStateV2Bytes + tc.delta - proofStateRemainingBytes(t, d, tc.future)
			d = proofStatePad(t, d, want)
			if proofStateRemainingBytes(t, d, tc.future) != proofStateV2Bytes+tc.delta {
				t.Fatal("incorrect independently sized forecast")
			}
			dir := proofDirectory(t)
			proofStateInstall(t, dir, d)
			s := proofStateStore(t, dir, d.Binding)
			before, e := s.Load(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			priorBytes := proofStateRaw(t, *before.Document)
			a, pv := proofStateAuthorize(t, s, p, ModeRecover, before.Base, "boundary", "cleanup")
			snap, e := s.Commit(context.Background(), *before.Base.Head, a, Change{Kind: ChangeStart, ConstructionID: "cleanup", ExpectedInventory: proofDigest('1')})
			if tc.delta > 0 {
				if e == nil && tc.delta == 4096 {
					// Unsafe blanket-skipping mutant: Start may fit, but its bounded
					// still-required local result cannot finish. Show the real refusal.
					r := snap.Document.Resources[0]
					o := Observation{r.Coordinate, PresencePresent, r.ManagedHash, proofDigest('2'), proofDigest('1'), r.ServerRevision, proofStateEvidence()}
					cl := CleanupClassification{r.Key, r.Coordinate, *snap.Base.Head, CleanupRetainedDependency, &o, proofDigest('1'), proofStateEvidence()}
					_, resultErr := s.Commit(context.Background(), *snap.Base.Head, a, Change{Kind: ChangeClassify, ConstructionID: "cleanup", ExpectedInventory: proofDigest('1'), Classification: &cl})
					t.Fatalf("over-bound continuation admitted; bounded local result: %v", resultErr)
				}
				if !errors.Is(e, ErrCapacity) {
					t.Fatalf("over-bound remaining completion admitted: %v", e)
				}
				after, err := s.Load(context.Background())
				if err != nil || *after.Base.Head != *before.Base.Head || len(after.Document.Consumed) != len(before.Document.Consumed) {
					t.Fatal("refusal consumed authority/history", err)
				}
				raw, _ := os.ReadFile(filepath.Join(dir, "state.json"))
				if !bytes.Equal(raw, priorBytes) {
					t.Fatal("refusal wrote state")
				}
				return
			}
			if e != nil {
				t.Fatal("within-bound continuation refused", e)
			}
			if tc.name == "settled-at" {
				r := snap.Document.Resources[0]
				o := Observation{r.Coordinate, PresencePresent, r.ManagedHash, proofDigest('2'), proofDigest('1'), r.ServerRevision, proofStateEvidence()}
				cl := CleanupClassification{r.Key, r.Coordinate, *snap.Base.Head, CleanupRetainedDependency, &o, proofDigest('1'), proofStateEvidence()}
				if _, err := s.Commit(context.Background(), *snap.Base.Head, a, Change{Kind: ChangeClassify, ConstructionID: "cleanup", ExpectedInventory: proofDigest('1'), Classification: &cl}); !errors.Is(err, ErrCapacity) {
					t.Fatal("repeat classification bypassed new admission", err)
				}
				after, err := s.Load(context.Background())
				if err != nil || *after.Base.Head != *snap.Base.Head || len(after.Document.Constructions[1].Classifications) != len(snap.Document.Constructions[1].Classifications) {
					t.Fatal("repeat refusal changed history", err)
				}
			}
			if tc.future {
				r := snap.Document.Resources[0]
				o := Observation{r.Coordinate, PresencePresent, r.ManagedHash, proofDigest('2'), proofDigest('1'), r.ServerRevision, proofStateEvidence()}
				cl := CleanupClassification{r.Key, r.Coordinate, *snap.Base.Head, CleanupRetainedDependency, &o, proofDigest('1'), proofStateEvidence()}
				snap = proofCommit(t, s, snap, a, Change{Kind: ChangeClassify, ConstructionID: "cleanup", ExpectedInventory: proofDigest('1'), Classification: &cl})
			}
			snap = proofStateFinish(t, s, snap, a, p, pv, strings.Repeat("<", 256), map[string]MemberOutcome{"member": OutcomeRetainedDependency}, ReceiptIncomplete)
			if len(snap.Document.Receipts) != 2 || len(snap.Document.Resources[0].Observations) < len(d.Resources[0].Observations) {
				t.Fatal("completion pruned retained history")
			}
			raw, _ := os.ReadFile(filepath.Join(dir, "state.json"))
			t.Logf("actual admitted forecast=%d final=%d observations=%d", proofStateV2Bytes+tc.delta, len(raw), len(snap.Document.Resources[0].Observations))
		})
	}
}

func TestFoundationStateV2ReadBoundariesAndBindings(t *testing.T) {
	var smallV2, smallOld []byte
	var v2 Binding
	for _, revision := range []string{proofStateV2Revision, "contract-1", proofStateV2Revision + "-unknown"} {
		t.Run(revision, func(t *testing.T) {
			d, _ := proofStateHistory(t, KindSuite, "settled", revision)
			limit := 786432
			if revision == proofStateV2Revision {
				limit = proofStateV2Bytes
				v2 = d.Binding
				smallV2 = proofStateRaw(t, d)
			}
			if revision == "contract-1" {
				smallOld = proofStateRaw(t, d)
			}
			for _, delta := range []int{-1, 0, 1} {
				t.Run(fmt.Sprint(delta), func(t *testing.T) {
					padded := proofStatePad(t, d, limit+delta)
					raw := proofStateRaw(t, padded)
					encoded, encErr := custodyDocumentBytes(padded)
					_, decErr := custodyDocumentDecode(raw)
					if delta > 0 {
						if !errors.Is(encErr, ErrCapacity) || !errors.Is(decErr, ErrCapacity) {
							t.Fatalf("embedded revision cap not enforced encode=%v decode=%v", encErr, decErr)
						}
					} else if encErr != nil || decErr != nil || !bytes.Equal(encoded, raw) {
						t.Fatalf("within-state boundary codec failed encode=%v decode=%v", encErr, decErr)
					}
					dir := proofDirectory(t)
					proofStateInstall(t, dir, padded)
					s := proofStateStore(t, dir, d.Binding)
					got, e := s.Load(context.Background())
					if delta > 0 {
						if !errors.Is(e, ErrCapacity) || got.Base.Pristine || got.Document != nil {
							t.Fatalf("oversize read usable/pristine: %v", e)
						}
					} else if e != nil || got.Base.Pristine || got.Document.Binding != d.Binding {
						t.Fatal("bounded state did not load", e)
					}
					after, e := os.ReadFile(filepath.Join(dir, "state.json"))
					if e != nil || !bytes.Equal(raw, after) {
						t.Fatal("read rejection rewrote history", e)
					}
				})
			}
		})
	}
	for _, revision := range []string{"", proofStateV2Revision + " "} {
		binding := v2
		binding.ContractRevision = revision
		if s, e := AcquireState(context.Background(), Location{Directory: proofDirectory(t)}, binding); e == nil {
			s.Close()
			t.Fatal("invalid binding acquired state")
		}
	}
	// Expected binding bounds OS reads even when the embedded revision is v2;
	// matching enrollment never substitutes for the embedded document binding.
	d, _ := proofStateHistory(t, KindSuite, "settled")
	dir := proofDirectory(t)
	proofStateInstall(t, dir, d)
	for _, tc := range []struct {
		name    string
		binding Binding
		raw     []byte
		want    error
	}{
		{"small-expected-mismatch", proofBinding(), smallV2, ErrBinding},
		{"large-expected-legacy", proofBinding(), proofStateRaw(t, proofStatePad(t, d, 786433)), ErrCapacity},
		{"embedded-mismatch", v2, smallOld, ErrBinding},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if e := os.WriteFile(filepath.Join(dir, "state.json"), tc.raw, 0600); e != nil {
				t.Fatal(e)
			}
			s := proofStateStore(t, dir, tc.binding)
			got, e := s.Load(context.Background())
			if !errors.Is(e, tc.want) || got.Base.Pristine || got.Document != nil {
				t.Fatal("binding mismatch became authority", e)
			}
			after, _ := os.ReadFile(filepath.Join(dir, "state.json"))
			if !bytes.Equal(after, tc.raw) {
				t.Fatal("mismatch relabelled history")
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestFoundationStateV2DoesNotEnlargeOtherInputs(t *testing.T) {
	spec, e := NewAuthoredSpec([]byte(`{"x":"` + strings.Repeat("a", 65536-8) + `"}`))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewAuthoredSpec([]byte(`{"x":"` + strings.Repeat("a", 65536-7) + `"}`)); !errors.Is(e, ErrCapacity) {
		t.Fatal("authored ceiling changed", e)
	}
	for _, revision := range []string{"contract-1", proofStateV2Revision} {
		in := proofInput(t)
		in.Binding.ContractRevision = revision
		in.Resources = nil
		in.Steps = nil
		for i := range 13 {
			key := fmt.Sprintf("r-%d", i)
			in.Resources = append(in.Resources, PlannedResource{Key: key, Origin: in.Targets[0], Coordinate: Coordinate{Kind: KindRule, ID: key}, Spec: spec, Namespaces: []string{"planner"}})
			in.Steps = append(in.Steps, PlannedStep{uint32(i + 1), key, OpCreate})
		}
		if _, e = NewPlan(in); !errors.Is(e, ErrCapacity) {
			t.Fatal("state revision enlarged plan ceiling", revision, e)
		}
	}
	p := proofStatePlan(t, false)
	pv, e := NewPreview(p, ModeApply, Base{Pristine: true}, proofDigest('1'))
	if e != nil {
		t.Fatal(e)
	}
	raw := proofApprovalJSON(t, pv, "bounded-approval", "", time.Now().Add(-time.Second), time.Now().Add(time.Minute), nil)
	if _, e = DecodeApproval(append(raw, bytes.Repeat([]byte(" "), 65536-len(raw))...)); e != nil {
		t.Fatal("at-bound approval refused", e)
	}
	if _, e = DecodeApproval(append(raw, bytes.Repeat([]byte(" "), 65537-len(raw))...)); !errors.Is(e, ErrApproval) {
		t.Fatal("state revision enlarged approval ceiling", e)
	}
}

func proofStateMaxInput(t *testing.T, maximumMode ...Mode) PlanInput {
	t.Helper()
	in := proofInput(t)
	in.Binding.ContractRevision = proofStateV2Revision
	in.Resources = nil
	in.Steps = nil
	template := in.Declarations[0]
	in.Declarations = nil
	in.Targets = nil
	spec, e := NewAuthoredSpec([]byte(`{"x":"` + strings.Repeat("a", 512-8) + `"}`))
	if e != nil {
		t.Fatal(e)
	}
	for i := range 256 {
		key := fmt.Sprintf("rule-%03d", i)
		declaration := template
		declaration.Origin = Origin{"blueprint", key, uint32(i)}
		declaration.Declaration.Name = fmt.Sprintf("Agent%03d", i)
		declaration.Declaration.Service.Name = fmt.Sprintf("agent%03d", i)
		in.Declarations = append(in.Declarations, declaration)
		in.Targets = append(in.Targets, declaration.Origin)
		in.Resources = append(in.Resources, PlannedResource{Key: key, Origin: declaration.Origin, Coordinate: Coordinate{Kind: KindRule, ID: key}, Spec: spec, Namespaces: []string{fmt.Sprintf("agent%03d", i)}})
		in.Steps = append(in.Steps, PlannedStep{uint32(i + 1), key, OpCreate})
	}
	measure := in
	if len(maximumMode) == 0 || maximumMode[0] == ModeTeardown {
		measure.Mode = ModeTeardown
		measure.Steps = nil
		for _, op := range []Operation{OpDisable, OpDelete} {
			for _, r := range in.Resources {
				measure.Steps = append(measure.Steps, PlannedStep{uint32(len(measure.Steps) + 1), r.Key, op})
			}
		}
	}
	raw, e := json.Marshal(planToWire(measure))
	if e != nil {
		t.Fatal(e)
	}
	extra := 786432 - len(raw)
	if extra < 0 {
		t.Fatal("max-input scaffold already exceeds plan cap")
	}
	for i := range in.Resources {
		size := 512 + extra/256
		if i < extra%256 {
			size++
		}
		in.Resources[i].Spec, e = NewAuthoredSpec([]byte(`{"x":"` + strings.Repeat("a", size-8) + `"}`))
		if e != nil {
			t.Fatal(e)
		}
	}
	return in
}

// An independently checksummed load fixture saturates resource/cleanup-step
// counts without pretending this historical apply was admitted by one grant.
func proofStateMaxHistory(t *testing.T) (Document, Plan) {
	t.Helper()
	p := mustPlan(t, proofStateMaxInput(t))
	d := emptyCustody(p.input.Binding)
	d.Generation = 1000
	d.Checksum = proofDigest('f')
	h := proofDigest('a')
	es := []Evidence{{"read", proofDigest('e')}}
	c := Construction{ID: "history", Input: p.Digest(), Preview: h, Approval: h, Mode: ModeApply, Base: Base{Pristine: true}, Namespaces: p.names, Plan: p.input, Steps: []StepRecord{}, Classifications: []CleanupClassification{}, Complete: true}
	members := []ReceiptMember{}
	for i, pr := range p.input.Resources {
		attempt := fmt.Sprintf("history-%03d", i)
		o := Observation{pr.Coordinate, PresencePresent, h, h, proofDigest('1'), "", es}
		d.Resources = append(d.Resources, ResourceRecord{pr.Key, pr.Origin, pr.Coordinate, attempt, pr.Spec, pr.Dependencies, pr.Namespaces, h, "", es, []Observation{o}, ResourceOwned})
		c.Steps = append(c.Steps, StepRecord{uint32(i + 1), attempt, StepVerified, pr.Coordinate, es})
		members = append(members, ReceiptMember{pr.Key, pr.Coordinate, OutcomePresent, &o, es})
	}
	for _, cl := range p.claims {
		cl.FirstConstruction = c.ID
		cl.Approval = h
		d.Reservations = append(d.Reservations, cl)
	}
	d.Constructions = append(d.Constructions, c)
	d.Consumed = append(d.Consumed, ConsumedApproval{"history", h, c.ID, Base{Pristine: true}})
	d.Receipts = append(d.Receipts, Receipt{"history-receipt", c.ID, ModeApply, ReceiptApplyObserved, custodyBindingHash(d.Binding), p.Digest(), h, proofDigest('1'), p.names, members, h, 1, ""})
	if e := validateCustodyDocument(d); e != nil {
		t.Fatal("maximum-history fixture invalid", e)
	}
	steps := []PlannedStep{}
	for _, op := range []Operation{OpDisable, OpDelete} {
		for _, pr := range p.input.Resources {
			steps = append(steps, PlannedStep{uint32(len(steps) + 1), pr.Key, op})
		}
	}
	return d, proofCleanupPlan(t, snapshotOf(d), ModeTeardown, steps)
}

// Invoked explicitly in a fresh plain test binary with external <=5s and Go
// <=4s bounds. Ordinary suites do not treat this helper's idle return as a
// resource measurement. GOMEMLIMIT is a soft GC target, not a hard RSS ceiling.
func TestFoundationStateV2ResourceProbe(t *testing.T) {
	mode := os.Getenv("SYNTHKIT_STATEV2_RESOURCE_PROBE")
	if mode == "" {
		return
	}
	var run func()
	switch mode {
	case "near-full":
		d, p := proofStateHistory(t, KindSuite, "settled")
		d = proofStatePad(t, d, len(proofStateRaw(t, d))+proofStateV2Bytes-proofStateRemainingBytes(t, d, false))
		dir := proofDirectory(t)
		proofStateInstall(t, dir, d)
		s := proofStateStore(t, dir, d.Binding)
		run = func() {
			before, e := s.Load(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			a, pv := proofStateAuthorize(t, s, p, ModeRecover, before.Base, "boundary", "cleanup")
			snap := proofCommit(t, s, before, a, Change{Kind: ChangeStart, ConstructionID: "cleanup", ExpectedInventory: proofDigest('1')})
			snap = proofStateFinish(t, s, snap, a, p, pv, strings.Repeat("<", 256), map[string]MemberOutcome{"member": OutcomeRetainedDependency}, ReceiptIncomplete)
			raw, _ := os.ReadFile(filepath.Join(dir, "state.json"))
			if len(raw) < proofStateV2Bytes-2048 || snap.Document.Pending != nil {
				t.Fatal("not an admitted near-full completion")
			}
			t.Logf("near-full stored_bytes=%d observations=%d", len(raw), len(snap.Document.Resources[0].Observations))
		}
	case "max-apply-rejected":
		in := proofStateMaxInput(t, ModeApply)
		s := proofStateStore(t, proofDirectory(t), in.Binding)
		run = func() {
			p := mustPlan(t, in)
			a, _ := proofStateAuthorize(t, s, p, ModeApply, Base{Pristine: true}, "max-input", "")
			if _, e := s.Bootstrap(context.Background(), a); !errors.Is(e, ErrCapacity) {
				t.Fatal("max apply did not reject projected result capacity", e)
			}
			after, e := s.Load(context.Background())
			if e != nil || !after.Base.Pristine {
				t.Fatal("rejected max apply enrolled", e)
			}
			raw, _ := json.Marshal(planToWire(p.input))
			t.Logf("max-apply resources=%d steps=%d claims=%d plan_bytes=%d", len(in.Resources), len(in.Steps), len(p.claims), len(raw))
		}
	case "max-cleanup-rejected":
		d, p := proofStateMaxHistory(t)
		dir := proofDirectory(t)
		proofStateInstall(t, dir, d)
		s := proofStateStore(t, dir, d.Binding)
		run = func() {
			before, e := s.Load(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			a, _ := proofStateAuthorize(t, s, p, ModeTeardown, before.Base, "max-cleanup-approval", "")
			if _, e = s.Commit(context.Background(), *before.Base.Head, a, Change{Kind: ChangeStart, ConstructionID: "max-cleanup", ExpectedInventory: proofDigest('1')}); !errors.Is(e, ErrCapacity) {
				t.Fatal("max cleanup did not reject", e)
			}
			after, e := s.Load(context.Background())
			if e != nil || *after.Base.Head != *before.Base.Head || len(after.Document.Consumed) != len(before.Document.Consumed) {
				t.Fatal("rejected max cleanup consumed authority", e)
			}
			raw, _ := json.Marshal(planToWire(p.input))
			t.Logf("max-cleanup resources=%d steps=%d claims=%d plan_bytes=%d old_state_bytes=%d", len(p.input.Resources), len(p.input.Steps), len(p.claims), len(raw), len(proofStateRaw(t, *before.Document)))
		}
	default:
		t.Fatal("unknown resource probe")
	}
	runtime.GC()
	var before, after runtime.MemStats
	var u0, u1 unix.Rusage
	runtime.ReadMemStats(&before)
	if e := unix.Getrusage(unix.RUSAGE_SELF, &u0); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	run()
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	if e := unix.Getrusage(unix.RUSAGE_SELF, &u1); e != nil {
		t.Fatal(e)
	}
	cpu := func(u unix.Rusage) int64 {
		return int64(u.Utime.Sec+u.Stime.Sec)*1000000 + int64(u.Utime.Usec+u.Stime.Usec)
	}
	rss := int64(u1.Maxrss)
	if runtime.GOOS == "linux" {
		rss *= 1024
	}
	t.Logf("RESOURCE mode=%s wall_us=%d cpu_us=%d alloc_bytes=%d heap_inuse_bytes=%d process_highwater_rss_bytes=%d", mode, elapsed.Microseconds(), cpu(u1)-cpu(u0), after.TotalAlloc-before.TotalAlloc, after.HeapInuse, rss)
}
