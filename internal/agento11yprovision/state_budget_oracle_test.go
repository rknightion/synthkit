// SPDX-License-Identifier: AGPL-3.0-only

//go:build darwin || linux

package agento11yprovision

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"
)

// The custodyBudget differential oracle. custodyBudgetReference is the
// pre-incremental function from dea5f12, kept verbatim apart from its name and
// one trace line that records each compared size; it is the decision oracle.
// custodyBudgetMutant is the production function with switchable seeded faults,
// one per class the oracle must catch. Boundary documents are the real
// Bootstrap/Start/Intent/Response/Observe/Classify/Finish snapshots (the exact
// inputs custodyBudget receives at state_file.go's call sites) plus constructed
// variants, each padded by an inert reservation so that one reference
// checkpoint lands exactly on limit-1, limit and limit+1.

type budgetMutant int

const (
	mutNone         budgetMutant = iota
	mutPeak                      // (i) no check while pending is set
	mutCreditEarly               // (ii) alternative credit leaks into earlier checks
	mutPendingNull               // (iii) an empty pending slot sized 0, not null
	mutComma                     // (iii) every append adds a comma
	mutGeneration                // (iii) envelope measured before the generation widens
	mutRawLen                    // (iv) RawMessage sized by len(raw)
	mutCheckFirst                // (v) consumed records and a check before structural tests
	mutEntryPending              // (vi) pending on entry (Intent call site) assumed null
	mutInvalidated               // receipt InvalidatedBy growth omitted
	mutPrecedence                // steps ErrCorrupt before count ErrCapacity
	mutAlias                     // (vii) writes through the caller's Document
	mutNilSlice                  // (iii) nil slice sized as [] (helper level)
)

var budgetMutantNames = map[budgetMutant]string{mutPeak: "peak", mutCreditEarly: "credit-early", mutPendingNull: "pending-null", mutComma: "comma", mutGeneration: "generation", mutRawLen: "raw-len", mutCheckFirst: "check-first", mutEntryPending: "entry-pending", mutInvalidated: "invalidated-by", mutPrecedence: "precedence", mutAlias: "alias", mutNilSlice: "nil-slice"}

func mutantAppendLen[T any](m budgetMutant, s []T, n int) int {
	switch {
	case m == mutComma:
		return n + 1
	case m == mutNilSlice && s == nil:
		return n
	}
	return custodyAppendLen(s, n)
}
func mutantPendingLen(m budgetMutant, p *pendingWire) (int, error) {
	if m == mutPendingNull && p == nil {
		return 0, nil
	}
	if m == mutRawLen && p != nil {
		v := *p
		raw := v.Desired
		v.Desired = nil
		n, err := custodyWireLen(v)
		return n - len("null") + len(raw), err
	}
	return custodyPendingLen(p)
}
func mutantResourceLen(m budgetMutant, r *resourceWire) (int, error) {
	if m == mutRawLen {
		v := *r
		v.Observations = nil
		raw := v.Authored
		v.Authored = nil
		n, err := custodyWireLen(v)
		return n - len("null") + len(raw), err
	}
	return custodyResourceLen(r)
}

type budgetCase struct {
	name, construction string
	d                  Document
	pad                bool // build limit-1/limit/limit+1 documents at every checkpoint
}

func budgetSnapshotCase(name string, snap Snapshot) budgetCase {
	d := *snap.Document
	return budgetCase{name, d.Constructions[len(d.Constructions)-1].ID, d, false}
}

// budgetStep is proofIntent plus proofObserveStep with every intermediate
// snapshot recorded. Evidence is short so the flows stay inside both limits;
// the budget's own reservations are worst-case regardless.
func budgetStep(t *testing.T, s *LockedState, snap Snapshot, a *Authorization, step uint32, label string, add func(string, Snapshot)) Snapshot {
	t.Helper()
	ctx := context.Background()
	inv := proofDigest('1')
	snap = proofIntent(t, s, snap, a, step, inv)
	add(fmt.Sprintf("%s-%d-intent", label, step), snap)
	p := snap.Document.Pending
	g, e := s.ClaimDispatch(ctx, *snap.Base.Head, a, p.AttemptID)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = g.Context().Err() }()
	<-done
	if e = s.ReleaseDispatch(g); e != nil {
		t.Fatal(e)
	}
	coord := p.Coordinate
	if coord.Unassigned {
		coord.Unassigned = false
		coord.ID = "generated-" + p.ResourceKey
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
	response := ResponseEvidence{Coordinate: coord, Evidence: []Evidence{{"response", proofDigest('e')}}}
	if presence == PresencePresent {
		response.ManagedHash = &hash
	}
	number := findStep(snap.Document, p.AttemptID)
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeResponse, ExpectedInventory: inv, ConstructionID: p.ConstructionID, AttemptID: p.AttemptID, Step: number, Response: &response})
	add(fmt.Sprintf("%s-%d-response", label, step), snap)
	obs := Observation{Coordinate: coord, Presence: presence, ManagedHash: hash, ExactRead: proofDigest('2'), Inventory: inv, Evidence: []Evidence{{"read-back", proofDigest('f')}}}
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeObserve, ExpectedInventory: inv, ConstructionID: p.ConstructionID, AttemptID: p.AttemptID, Step: number, Observation: &obs})
	add(fmt.Sprintf("%s-%d-observe", label, step), snap)
	return snap
}
func budgetFinish(t *testing.T, s *LockedState, snap Snapshot, a *Authorization, p Plan, pv Preview, id string, outcomes map[string]MemberOutcome, result ReceiptResult) Snapshot {
	t.Helper()
	members := []ReceiptMember{}
	for _, pr := range p.input.Resources {
		r := findResource(snap.Document, pr.Key)
		o := Observation{r.Coordinate, PresencePresent, r.ManagedHash, proofDigest('2'), proofDigest('1'), r.ServerRevision, []Evidence{{"closing-read", proofDigest('e')}}}
		if outcomes[r.Key] == OutcomeRemoved {
			o.Presence, o.ManagedHash, o.ServerRevision = PresenceAbsent, "", ""
		}
		members = append(members, ReceiptMember{r.Key, r.Coordinate, outcomes[r.Key], &o, []Evidence{{"decision", proofDigest('f')}}})
	}
	c := snap.Document.Constructions[len(snap.Document.Constructions)-1]
	receipt := Receipt{ID: id, ConstructionID: c.ID, Mode: p.input.Mode, Result: result, BindingDigest: custodyBindingHash(p.input.Binding), Input: p.Digest(), Preview: pv.Digest(), Inventory: proofDigest('1'), Namespaces: []string{"planner", "source"}, Members: members, OperatorApproval: a.approval.hash, ObservedMS: time.Now().UnixMilli()}
	return proofCommit(t, s, snap, a, Change{Kind: ChangeFinish, ExpectedInventory: proofDigest('1'), ConstructionID: c.ID, Receipt: &receipt})
}

// budgetFlow runs a real three-resource apply (evaluator, unassigned
// collection, rule) and a teardown (rule disable+delete, evaluator delete, then
// the collection: classified, or with tail deleted too) through the public
// LockedState, recording every snapshot. Bootstrap, ChangeStart, ChangeIntent
// and ChangeClassify snapshots are exactly the documents those call sites hand
// custodyBudget.
func budgetFlow(t *testing.T, revision string, tail bool) []budgetCase {
	t.Helper()
	in := proofInput(t)
	in.Binding.ContractRevision = revision
	in.Resources = in.Resources[:3]
	in.Steps = in.Steps[:3]
	p := mustPlan(t, in)
	s := proofStateStore(t, proofDirectory(t), in.Binding)
	cases := []budgetCase{}
	add := func(name string, snap Snapshot) { cases = append(cases, budgetSnapshotCase(name, snap)) }
	a, pv := proofStateAuthorize(t, s, p, ModeApply, Base{Pristine: true}, "budget-apply", "")
	snap, e := s.Bootstrap(context.Background(), a)
	if e != nil {
		t.Fatalf("bootstrap %s: %v", revision, e)
	}
	add("bootstrap", snap)
	for i := range p.input.Steps {
		snap = budgetStep(t, s, snap, a, uint32(i+1), "apply", add)
	}
	snap = budgetFinish(t, s, snap, a, p, pv, "budget-apply-receipt", map[string]MemberOutcome{"e": OutcomePresent, "c": OutcomePresent, "r": OutcomePresent}, ReceiptApplyObserved)
	add("apply-finish", snap)
	steps := []PlannedStep{{1, "r", OpDisable}, {2, "r", OpDelete}, {3, "e", OpDelete}}
	if tail {
		// A full step after the credited disable+delete pair, then a fourth.
		steps = append(steps, PlannedStep{4, "c", OpDelete})
	}
	cp := proofCleanupPlan(t, snap, ModeTeardown, steps)
	ca, cv := proofStateAuthorize(t, s, cp, ModeTeardown, snap.Base, "budget-cleanup-approval", "")
	snap = proofCommit(t, s, snap, ca, Change{Kind: ChangeStart, ConstructionID: "budget-cleanup", ExpectedInventory: proofDigest('1')})
	add("start", snap)
	snap = budgetStep(t, s, snap, ca, 1, "teardown", add)
	snap = budgetStep(t, s, snap, ca, 2, "teardown", add)
	snap = proofClassify(t, s, snap, ca, "budget-cleanup", "c", CleanupRetainedUnsupported, proofDigest('1'))
	add("classify", snap)
	snap = budgetStep(t, s, snap, ca, 3, "teardown", add)
	if tail {
		return cases
	}
	snap = budgetFinish(t, s, snap, ca, cp, cv, "budget-cleanup-receipt", map[string]MemberOutcome{"e": OutcomeRemoved, "c": OutcomeRetainedUnsupported, "r": OutcomeRemoved}, ReceiptIncomplete)
	add("teardown-finish", snap)
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	return cases
}

func budgetClone(t *testing.T, d Document) Document {
	t.Helper()
	out, e := documentFromWire(documentToWire(d))
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func budgetFind(t *testing.T, cases []budgetCase, name string) budgetCase {
	t.Helper()
	for _, c := range cases {
		if c.name == name {
			return c
		}
	}
	t.Fatalf("no flow snapshot %s", name)
	return budgetCase{}
}

// budgetVariants builds constructed documents from flow snapshots: structural
// faults behind admitted steps (a step left in StepIntent with no pending; a
// step naming a missing resource), non-canonical RawMessage that the encoder
// compacts and escapes, and error-precedence and generation edges.
func budgetVariants(t *testing.T, cases []budgetCase) []budgetCase {
	t.Helper()
	out := []budgetCase{}
	start := budgetFind(t, cases, "start")
	{
		d := budgetClone(t, start.d)
		c := findConstruction(&d, start.construction)
		c.Steps[len(c.Steps)-1].Status = StepIntent // remaining tail with no pending: ErrCorrupt
		out = append(out, budgetCase{"start-tail-intent-without-pending", start.construction, d, true})
	}
	{
		d := budgetClone(t, start.d)
		c := findConstruction(&d, start.construction)
		c.Plan.Steps[len(c.Plan.Steps)-1].ResourceKey = "missing" // remaining tail naming no resource: ErrCorrupt
		out = append(out, budgetCase{"start-tail-missing-resource", start.construction, d, true})
	}
	{
		// Start invalidates overlapping receipts itself, so reachable inputs
		// never exercise the reservation for a later invalidation.
		d := budgetClone(t, start.d)
		for i := range d.Receipts {
			d.Receipts[i].InvalidatedBy = ""
		}
		out = append(out, budgetCase{"start-receipt-not-invalidated", start.construction, d, true})
	}
	boot := budgetFind(t, cases, "bootstrap")
	{
		d := budgetClone(t, boot.d)
		c := findConstruction(&d, boot.construction)
		c.Plan.Steps[1].ResourceKey = "missing"
		out = append(out, budgetCase{"bootstrap-tail-missing-resource", boot.construction, d, true})
	}
	// The first remaining step faulty: the envelope check is the running
	// maximum, so a check slipped before the structural test changes the answer.
	{
		d := budgetClone(t, boot.d)
		findConstruction(&d, boot.construction).Plan.Steps[0].ResourceKey = "missing"
		out = append(out, budgetCase{"bootstrap-head-missing-resource", boot.construction, d, true})
	}
	{
		d := budgetClone(t, boot.d)
		findConstruction(&d, boot.construction).Steps[0].Status = StepIntent
		out = append(out, budgetCase{"bootstrap-head-intent-without-pending", boot.construction, d, true})
	}
	for _, name := range []string{"apply-2-intent"} {
		src := budgetFind(t, cases, name)
		d := budgetClone(t, src.d)
		raw := AuthoredSpec{raw: []byte("{ \"x\" : \"<<<< &>\" ,\n \"y\" : [ 1 , 2 ] }")}
		for i := range d.Resources {
			d.Resources[i].Authored = raw
		}
		if d.Pending != nil {
			d.Pending.Desired = raw
		}
		out = append(out, budgetCase{name + "-noncanonical-raw", src.construction, d, true})
	}
	{
		d := budgetClone(t, boot.d)
		d.Binding.ContractRevision = "" // limit 0 wins over the count cap
		for range custodyMaxResources + 1 {
			d.Resources = append(d.Resources, d.Resources[0])
		}
		out = append(out, budgetCase{"precedence-invalid-over-count", boot.construction, d, false})
	}
	{
		d := budgetClone(t, boot.d)
		for range custodyMaxResources + 1 - len(d.Resources) {
			d.Resources = append(d.Resources, d.Resources[0])
		}
		out = append(out, budgetCase{"precedence-count-over-missing-construction", "absent", d, false})
	}
	{
		d := budgetClone(t, boot.d)
		for range custodyMaxClaims + 1 - len(d.Reservations) {
			d.Reservations = append(d.Reservations, d.Reservations[0])
		}
		out = append(out, budgetCase{"precedence-claims-over-steps", boot.construction, d, false})
		c := findConstruction(&out[len(out)-1].d, boot.construction)
		for range custodyMaxSteps + 1 - len(c.Steps) {
			c.Steps = append(c.Steps, c.Steps[0])
		}
	}
	{
		d := budgetClone(t, start.d)
		c := findConstruction(&d, start.construction)
		for range custodyMaxSteps + 1 - len(c.Steps) {
			c.Steps = append(c.Steps, c.Steps[0])
		}
		d.Generation = math.MaxUint64 // steps ErrCorrupt wins over generation
		out = append(out, budgetCase{"precedence-steps-over-generation", start.construction, d, false})
	}
	// Start: three planned steps (12 commits), three plan resources, Finish.
	for _, delta := range []uint64{0, 1} {
		d := budgetClone(t, start.d)
		d.Generation = math.MaxUint64 - 16 + delta
		out = append(out, budgetCase{fmt.Sprintf("generation-edge+%d", delta), start.construction, d, false})
	}
	return out
}

func budgetRaceBuild() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, s := range info.Settings {
		if s.Key == "-race" {
			return s.Value == "true"
		}
	}
	return false
}

func budgetEncoded(d Document) string {
	raw, err := json.Marshal(documentToWire(d))
	if err != nil {
		return "encode error: " + err.Error()
	}
	return string(raw)
}

// budgetPadded appends one inert reservation whose namespace adds exactly n
// encoded bytes over the empty-namespace pad at every checkpoint.
func budgetPadded(d Document, pad string) Document {
	d.Reservations = append(slices.Clone(d.Reservations), Reservation{Namespace: pad})
	return d
}

type budgetRun struct {
	name, construction string
	d                  Document
	want               error
}

func budgetCall(f func() error) (err error, panicked any) {
	defer func() { panicked = recover() }()
	return f(), nil
}

// budgetBoundaries runs the reference on every case and, for padded cases,
// traces it (with an empty pad reservation) and returns, for every checkpoint
// the reference reached, the
// documents whose reference size there is limit-1, limit and limit+1, with the
// reference decision for each. Each constructed size is re-traced and checked.
func budgetBoundaries(t *testing.T, cases []budgetCase, pad string) []budgetRun {
	t.Helper()
	runs := []budgetRun{}
	for _, bc := range cases {
		want, p := budgetCall(func() error { return custodyBudgetReference(bc.d, bc.construction, nil) })
		if p != nil {
			t.Fatalf("%s: reference panicked: %v", bc.name, p)
		}
		runs = append(runs, budgetRun{bc.name + "/unpadded", bc.construction, bc.d, want})
		if !bc.pad {
			continue
		}
		base := budgetPadded(bc.d, "")
		trace := []int{}
		want, p = budgetCall(func() error {
			return custodyBudgetReference(base, bc.construction, func(n int) { trace = append(trace, n) })
		})
		if p != nil {
			t.Fatalf("%s: reference panicked: %v", bc.name, p)
		}
		runs = append(runs, budgetRun{bc.name + "/pad0", bc.construction, base, want})
		limit := custodyStateLimit(bc.d.Binding)
		tested, dominated := 0, 0
		for k, size := range trace {
			for _, delta := range []int{-1, 0, 1} {
				n := limit + delta - size
				if n < 0 || n > len(pad) {
					continue
				}
				// A checkpoint after a larger earlier one (the pending peak) can
				// never decide under uniform padding: the earlier one refuses first.
				if slices.Max(append([]int{math.MinInt}, trace[:k]...))+n > limit {
					dominated++
					continue
				}
				tested++
				d := budgetPadded(bc.d, pad[:n])
				got := []int{}
				want, p := budgetCall(func() error { return custodyBudgetReference(d, bc.construction, func(n int) { got = append(got, n) }) })
				if p != nil || len(got) <= k || got[k] != limit+delta {
					t.Fatalf("%s checkpoint %d delta %d: constructed size not exact (%v, panic %v)", bc.name, k, delta, got, p)
				}
				runs = append(runs, budgetRun{fmt.Sprintf("%s/checkpoint-%d/limit%+d", bc.name, k, delta), bc.construction, d, want})
			}
		}
		t.Logf("%s: limit=%d checkpoints=%d boundary documents=%d dominated=%d reference=%v", bc.name, limit, len(trace), tested, dominated, want)
	}
	return runs
}

func TestCustodyBudgetAppendLen(t *testing.T) {
	element := consumedWire{"id<", proofDigest('f'), "c", baseWire{Head: &headWire{math.MaxUint64, proofDigest('f')}}}
	n, err := custodyWireLen(element)
	if err != nil {
		t.Fatal(err)
	}
	check := func(f func([]consumedWire, int) int) error {
		for _, s := range [][]consumedWire{nil, {}, {element}, {element, element}} {
			before, _ := json.Marshal(s)
			after, _ := json.Marshal(append(slices.Clone(s), element))
			if s == nil {
				after, _ = json.Marshal(append([]consumedWire(nil), element))
			}
			if got := len(before) + f(s, n); got != len(after) {
				return fmt.Errorf("len=%d nil=%t: got %d, encoder %d", len(s), s == nil, got, len(after))
			}
		}
		return nil
	}
	if err := check(custodyAppendLen[consumedWire]); err != nil {
		t.Fatal(err)
	}
	for _, m := range []budgetMutant{mutNilSlice, mutComma} {
		if check(func(s []consumedWire, n int) int { return mutantAppendLen(m, s, n) }) == nil {
			t.Fatalf("append-length mutant %s not caught", budgetMutantNames[m])
		}
	}
	for _, p := range []*pendingWire{nil, {AttemptID: "<", Desired: json.RawMessage(`{ "a" : "<" }`)}} {
		got, _ := custodyPendingLen(p)
		raw, _ := json.Marshal(p)
		if got != len(raw) {
			t.Fatalf("pending length %d, encoder %d", got, len(raw))
		}
	}
}

// Decisions (error identity) of the incremental custodyBudget equal the
// reference on every boundary document at both state limits, it never writes
// through its input, and every seeded fault class disagrees somewhere.
func TestCustodyBudgetDifferentialOracle(t *testing.T) {
	began := time.Now()
	legacy := budgetFlow(t, proofBinding().ContractRevision, false)
	v2 := budgetFlow(t, proofStateV2Revision, true)
	if custodyStateLimit(legacy[0].d.Binding) != 786432 || custodyStateLimit(v2[0].d.Binding) != 3342336 {
		t.Fatal("limits moved")
	}
	// Every snapshot and variant is compared unpadded. Boundary
	// padding covers each call site's input: an encode at the v2 limit costs
	// 4.25x, so there it covers Bootstrap, the four-step Start (credited pair,
	// a full step, a tail) and that Start's corrupt-tail variant.
	padded := map[string]bool{"bootstrap": true, "apply-2-intent": true, "start": true, "teardown-2-intent": true, "classify": true}
	cases := []budgetCase{}
	for _, c := range legacy {
		c.pad = padded[c.name]
		cases = append(cases, c)
	}
	cases = append(cases, budgetVariants(t, legacy)...)
	// Under -race an encode costs about 16x and the function has no
	// concurrency to observe; the full matrix and the mutants run in the plain
	// test leg. The race leg keeps every unpadded comparison and the
	// smallest legacy Intent boundary set.
	race := budgetRaceBuild()
	for i, c := range append(slices.Clone(v2), budgetVariants(t, v2)[:2]...) {
		c.pad = c.name == "bootstrap" || c.name == "start" || (i >= len(v2) && c.name == "start-tail-intent-without-pending")
		c.name = "v2/" + c.name
		cases = append(cases, c)
	}
	if race {
		for i := range cases {
			cases[i].pad = cases[i].name == "apply-3-intent"
		}
	}
	flows := time.Since(began)
	runs := budgetBoundaries(t, cases, strings.Repeat("a", 3342336))
	t.Logf("flows %v, boundary construction and reference %v", flows, time.Since(began)-flows)
	seen := map[error]int{}
	for _, r := range runs {
		seen[r.want]++
	}
	t.Logf("boundary documents=%d decisions=%v", len(runs), seen)
	if seen[nil] == 0 || seen[ErrCapacity] == 0 || seen[ErrCorrupt] == 0 || seen[ErrInvalid] == 0 {
		t.Fatal("boundary set lacks a decision class")
	}
	for _, r := range runs {
		before := budgetEncoded(r.d)
		got, p := budgetCall(func() error { return custodyBudget(r.d, r.construction) })
		if p != nil || got != r.want {
			t.Fatalf("%s: custodyBudget=%v (panic %v), reference=%v", r.name, got, p, r.want)
		}
		if strings.HasSuffix(r.name, "/unpadded") && budgetEncoded(r.d) != before {
			t.Fatalf("%s: custodyBudget wrote through its input", r.name)
		}
		if race {
			continue
		}
		if got, _ = budgetCall(func() error { return custodyBudgetMutant(r.d, r.construction, mutNone) }); got != r.want {
			t.Fatalf("%s: unmutated copy=%v, reference=%v", r.name, got, r.want)
		}
	}
	if race {
		t.Logf("race build: reduced boundary set; mutants run in the plain leg")
		return
	}
	for m := mutPeak; m < mutAlias; m++ {
		caught := ""
		for _, r := range runs {
			if got, p := budgetCall(func() error { return custodyBudgetMutant(r.d, r.construction, m) }); p != nil || got != r.want {
				caught = fmt.Sprintf("%s: mutant=%v reference=%v", r.name, got, r.want)
				break
			}
		}
		if caught == "" {
			t.Fatalf("mutant %s not caught", budgetMutantNames[m])
		}
		t.Logf("mutant %s caught at %s", budgetMutantNames[m], caught)
	}
	// Aliasing last: it corrupts the document it is given, so use a clone.
	src := budgetFind(t, legacy, "start")
	d := budgetClone(t, src.d)
	before := budgetEncoded(d)
	if e := custodyBudgetMutant(d, src.construction, mutAlias); e != nil || budgetEncoded(d) == before {
		t.Fatalf("mutant alias not caught (%v)", e)
	}
}

// custodyBudgetReference: dea5f12 internal/agento11yprovision/state_file.go:756-976, verbatim
// except the name and the trace line.
func custodyBudgetReference(d Document, construction string, trace func(int)) error {
	limit := custodyStateLimit(d.Binding)
	if limit == 0 {
		return ErrInvalid
	}
	if len(d.Resources) > custodyMaxResources || len(d.Reservations) > custodyMaxClaims {
		return ErrCapacity
	}
	w := documentToWire(d)
	original := findConstruction(&d, construction)
	var c *constructionWire
	for i := range w.Constructions {
		if w.Constructions[i].ID == construction {
			c = &w.Constructions[i]
		}
	}
	if c == nil || original == nil || len(c.Steps) > custodyMaxSteps {
		return ErrCorrupt
	}
	// Reserve generation space as well as bytes before granting any dispatch.
	// A response followed by recovery and a fresh suffix Start is the longest
	// remaining path for one attempt; repeated local passes recheck admission.
	commits := uint64(1) // Finish
	for _, step := range c.Steps {
		switch step.Status {
		case StepPlanned:
			commits += 4
		case StepIntent:
			commits += 3
		case StepResponse:
			commits += 2
		}
	}
	if c.Mode != ModeApply {
		commits += uint64(len(c.Plan.Resources))
	}
	if d.Generation > math.MaxUint64-commits {
		return ErrCapacity
	}
	text := strings.Repeat("<", 256)
	hash := Digest(strings.Repeat("f", 64))
	maxHead := headWire{math.MaxUint64, hash}
	w.Generation = math.MaxUint64
	es := make([]evidenceWire, 16)
	for i := range es {
		es[i] = evidenceWire{text, hash}
	}
	// Authored canonical JSON is already bounded by its encoded bytes.
	desired := json.RawMessage(`{"x":"` + strings.Repeat("a", custodySpecBytes-8) + `"}`)
	resources := map[string]*resourceWire{}
	for i := range w.Resources {
		resources[w.Resources[i].Key] = &w.Resources[i]
	}
	alternativeCredit := 0
	check := func() error {
		raw, err := json.Marshal(w)
		if err != nil {
			return ErrCorrupt
		}
		if trace != nil {
			trace(len(raw) - alternativeCredit) // oracle trace only; no decision change
		}
		if len(raw)-alternativeCredit > limit {
			return ErrCapacity
		}
		return nil
	}
	observation := func(coordinate coordinateWire) observationWire {
		return observationWire{coordinate, PresencePresent, hash, hash, hash, text, es}
	}
	resolved := func(coordinate coordinateWire) coordinateWire {
		if coordinate.Unassigned {
			coordinate.Unassigned = false
			coordinate.ID = text
			if coordinate.Kind == KindDraft {
				coordinate.Version = text
			}
		}
		return coordinate
	}
	// Future intents may irreversibly invalidate an existing receipt. The
	// invalidation identifier is independently bounded, not the original ID.
	for i := range w.Receipts {
		if w.Receipts[i].InvalidatedBy == "" && overlapCustody(w.Receipts[i].Namespaces, c.Namespaces) {
			w.Receipts[i].InvalidatedBy = text
		}
	}
	// A cleanup classification is an alternative to never-attempted mutations.
	// Reserve its metadata too; share the already-reserved observation/step
	// slots with the mutation path. Repeated local passes require new admission.
	classAlternatives := map[string]int{}
	if c.Mode != ModeApply {
		latestClass := map[string]CleanupReason{}
		for _, cl := range original.Classifications {
			latestClass[cl.ResourceKey] = cl.Reason
		}
		for _, pr := range c.Plan.Resources {
			eligible, remaining := true, 0
			for i, step := range c.Steps {
				if c.Plan.Steps[i].ResourceKey != pr.Key {
					continue
				}
				if step.Status != StepPlanned && step.Status != StepClassified {
					eligible = false
				}
				if step.Status == StepPlanned {
					remaining++
				}
			}
			if !eligible {
				continue
			}
			// Only the latest settled classification consumes this future slot.
			// All steps for it must already be classified (or it has no steps).
			// Unresolved still needs settlement; planned mutations still need
			// their alternative. A repeat local classification is admitted anew
			// by Commit before publication, not promised by an earlier grant.
			if reason := latestClass[pr.Key]; remaining == 0 && reason != "" && reason != CleanupUnresolved {
				continue
			}
			r := resources[pr.Key]
			o := observation(r.Coordinate)
			c.Classifications = append(c.Classifications, classificationWire{pr.Key, r.Coordinate, maxHead, CleanupRetainedUnsupported, &o, hash, es})
			if remaining == 0 {
				r.Observations = append(r.Observations, o)
			}
			if remaining == 2 {
				classAlternatives[pr.Key] = len(r.Observations)
			}
		}
	}
	if err := check(); err != nil {
		return err
	}
	for i := range c.Steps {
		step := &c.Steps[i]
		if step.Status != StepPlanned && step.Status != StepIntent && step.Status != StepResponse {
			continue
		}
		ps := c.Plan.Steps[i]
		pr := planResource(original.Plan, ps.ResourceKey)
		r := resources[ps.ResourceKey]
		if pr == nil || r == nil {
			return ErrCorrupt
		}
		// Allow independent recovery attribution plus a fresh suffix approval.
		// Their documents are not stored, only these bounded consumed records.
		for range 2 {
			w.Consumed = append(w.Consumed, consumedWire{text, hash, c.ID, baseWire{Head: &maxHead}})
		}
		if step.Status == StepPlanned {
			coordinate := coordinateWire(pr.Coordinate)
			if pr.VersionFrom != "" {
				coordinate.Version = resources[pr.VersionFrom].Coordinate.Version
			}
			step.AttemptID, step.Status, step.Coordinate = text, StepIntent, coordinate
			if r.FirstAttempt == "" {
				r.FirstAttempt = text
			}
			w.Pending = &pendingWire{step.AttemptID, c.ID, pr.Key, hash, c.Input, hash, hash, maxHead, c.Namespaces, coordinate, ps.Operation, custodyMethod(coordinate.Kind, ps.Operation), desired, hash, hash, StepIntent, nil}
			if err := check(); err != nil {
				return err
			}
		}
		if w.Pending == nil || w.Pending.ResourceKey != pr.Key {
			return ErrCorrupt
		}
		p := w.Pending
		if step.Status == StepIntent {
			coordinate := resolved(p.Coordinate)
			p.Returned = &responseWire{coordinate, es, &hash}
			p.Phase, step.Status, step.Coordinate = StepResponse, StepResponse, coordinate
			if p.Coordinate.Unassigned {
				r.Coordinate = coordinate
			}
			if err := check(); err != nil {
				return err
			}
		}
		o := observation(p.Returned.Coordinate)
		if ps.Operation == OpDelete {
			o.Presence, o.ManagedHash, o.ServerRevision = PresenceAbsent, "", ""
		} else {
			r.ManagedHash, r.ServerRevision = hash, text
		}
		if len(r.Acquisition) == 0 {
			r.Acquisition = es
		}
		r.Coordinate = o.Coordinate
		r.Status = ResourceNotApplied // longest status, including recovery outcome
		r.Observations = append(r.Observations, o)
		step.Status, step.Coordinate, step.Evidence = StepNotApplied, o.Coordinate, es
		w.Pending = nil
		if err := check(); err != nil {
			return err
		}
	}
	// Cleanup has at most disable+delete per resource. The classification
	// alternative appends ONE observation, not both mutation observations. The
	// larger of those two bounds its observation; its metadata contains another
	// full observation plus evidence, so also covers the smaller mutation slot.
	// Deduct only that duplicate virtual slot at the final-stage comparison.
	// No persisted history or actual incoming result is removed or discounted.
	for key, first := range classAlternatives {
		obs := resources[key].Observations
		one, err1 := json.Marshal(obs[first])
		two, err2 := json.Marshal(obs[first+1])
		if err1 != nil || err2 != nil {
			return ErrCorrupt
		}
		alternativeCredit += min(len(one), len(two)) + 1
	}
	// A receipt has independent member/read evidence. Never count it as a
	// small identifier-only checkpoint or retain an extra pending reservation.
	members := []memberWire{}
	for _, pr := range c.Plan.Resources {
		r := resources[pr.Key]
		o := observation(r.Coordinate)
		members = append(members, memberWire{r.Key, r.Coordinate, OutcomeRetainedUnsupported, &o, es})
	}
	w.Receipts = append(w.Receipts, receiptWire{text, c.ID, c.Mode, ReceiptApplyObserved, hash, c.Input, hash, hash, c.Namespaces, members, hash, math.MaxInt64, text})
	return check()
}

// custodyBudgetMutant: the production custodyBudget with switchable seeded faults.
func custodyBudgetMutant(d Document, construction string, m budgetMutant) error {
	limit := custodyStateLimit(d.Binding)
	if limit == 0 {
		return ErrInvalid
	}
	counts := len(d.Resources) > custodyMaxResources || len(d.Reservations) > custodyMaxClaims
	if m != mutPrecedence && counts {
		return ErrCapacity
	}
	w := documentToWire(d)
	original := findConstruction(&d, construction)
	var c *constructionWire
	for i := range w.Constructions {
		if w.Constructions[i].ID == construction {
			c = &w.Constructions[i]
		}
	}
	if c == nil || original == nil || len(c.Steps) > custodyMaxSteps {
		return ErrCorrupt
	}
	if m == mutPrecedence && counts {
		return ErrCapacity
	}
	// Reserve generation space as well as bytes before granting any dispatch.
	// A response followed by recovery and a fresh suffix Start is the longest
	// remaining path for one attempt; repeated local passes recheck admission.
	commits := uint64(1) // Finish
	for _, step := range c.Steps {
		switch step.Status {
		case StepPlanned:
			commits += 4
		case StepIntent:
			commits += 3
		case StepResponse:
			commits += 2
		}
	}
	if c.Mode != ModeApply {
		commits += uint64(len(c.Plan.Resources))
	}
	if d.Generation > math.MaxUint64-commits {
		return ErrCapacity
	}
	text := strings.Repeat("<", 256)
	hash := Digest(strings.Repeat("f", 64))
	maxHead := headWire{math.MaxUint64, hash}
	w.Generation = math.MaxUint64
	es := make([]evidenceWire, 16)
	for i := range es {
		es[i] = evidenceWire{text, hash}
	}
	// Authored canonical JSON is already bounded by its encoded bytes.
	desired := json.RawMessage(`{"x":"` + strings.Repeat("a", custodySpecBytes-8) + `"}`)
	resources := map[string]*resourceWire{}
	for i := range w.Resources {
		resources[w.Resources[i].Key] = &w.Resources[i]
	}
	alternativeCredit := 0
	// size is always exactly len(json.Marshal(w)). The whole envelope is
	// encoded once; afterwards only the touched components (this step, its
	// resource without observations, pending, appended array elements) are
	// re-encoded and the length difference is applied. Each component is
	// encoded by json.Marshal itself, never sized from len(raw) of its parts.
	size := 0
	check := func() error {
		if size-alternativeCredit > limit {
			return ErrCapacity
		}
		return nil
	}
	observation := func(coordinate coordinateWire) observationWire {
		return observationWire{coordinate, PresencePresent, hash, hash, hash, text, es}
	}
	resolved := func(coordinate coordinateWire) coordinateWire {
		if coordinate.Unassigned {
			coordinate.Unassigned = false
			coordinate.ID = text
			if coordinate.Kind == KindDraft {
				coordinate.Version = text
			}
		}
		return coordinate
	}
	// Future intents may irreversibly invalidate an existing receipt. The
	// invalidation identifier is independently bounded, not the original ID.
	for i := range w.Receipts {
		if m != mutInvalidated && w.Receipts[i].InvalidatedBy == "" && overlapCustody(w.Receipts[i].Namespaces, c.Namespaces) {
			w.Receipts[i].InvalidatedBy = text
		}
	}
	// A cleanup classification is an alternative to never-attempted mutations.
	// Reserve its metadata too; share the already-reserved observation/step
	// slots with the mutation path. Repeated local passes require new admission.
	classAlternatives := map[string]int{}
	if c.Mode != ModeApply {
		latestClass := map[string]CleanupReason{}
		for _, cl := range original.Classifications {
			latestClass[cl.ResourceKey] = cl.Reason
		}
		for _, pr := range c.Plan.Resources {
			eligible, remaining := true, 0
			for i, step := range c.Steps {
				if c.Plan.Steps[i].ResourceKey != pr.Key {
					continue
				}
				if step.Status != StepPlanned && step.Status != StepClassified {
					eligible = false
				}
				if step.Status == StepPlanned {
					remaining++
				}
			}
			if !eligible {
				continue
			}
			// Only the latest settled classification consumes this future slot.
			// All steps for it must already be classified (or it has no steps).
			// Unresolved still needs settlement; planned mutations still need
			// their alternative. A repeat local classification is admitted anew
			// by Commit before publication, not promised by an earlier grant.
			if reason := latestClass[pr.Key]; remaining == 0 && reason != "" && reason != CleanupUnresolved {
				continue
			}
			r := resources[pr.Key]
			o := observation(r.Coordinate)
			c.Classifications = append(c.Classifications, classificationWire{pr.Key, r.Coordinate, maxHead, CleanupRetainedUnsupported, &o, hash, es})
			if remaining == 0 {
				r.Observations = append(r.Observations, o)
			}
			if remaining == 2 {
				classAlternatives[pr.Key] = len(r.Observations)
			}
		}
	}
	full, err := json.Marshal(w)
	if m == mutGeneration {
		g := w.Generation
		w.Generation = d.Generation
		full, err = json.Marshal(w)
		w.Generation = g
	}
	if err != nil {
		return ErrCorrupt
	}
	size = len(full)
	if err := check(); err != nil {
		return err
	}
	// Every value below was part of the encoded envelope or is built from
	// fixed bounded values, so its own encoding cannot fail; ErrCorrupt is
	// kept only as the conservative answer the whole-document encode gave.
	pendingLen, err := mutantPendingLen(m, w.Pending)
	if m == mutEntryPending {
		pendingLen = len("null")
	}
	if err != nil {
		return ErrCorrupt
	}
	consumedLen := -1 // the same bounded record every time; memoised per call
	for i := range c.Steps {
		step := &c.Steps[i]
		if step.Status != StepPlanned && step.Status != StepIntent && step.Status != StepResponse {
			continue
		}
		ps := c.Plan.Steps[i]
		pr := planResource(original.Plan, ps.ResourceKey)
		r := resources[ps.ResourceKey]
		if m == mutCheckFirst {
			for range 2 {
				record := consumedWire{text, hash, c.ID, baseWire{Head: &maxHead}}
				n, _ := custodyWireLen(record)
				size += mutantAppendLen(m, w.Consumed, n)
				w.Consumed = append(w.Consumed, record)
			}
			if err := check(); err != nil {
				return err
			}
		}
		if pr == nil || r == nil {
			return ErrCorrupt
		}
		stepLen, err1 := custodyWireLen(*step)
		resourceLen, err2 := mutantResourceLen(m, r)
		if err1 != nil || err2 != nil {
			return ErrCorrupt
		}
		// remeasure applies the encoded-length change of everything this step
		// may have touched since the last checkpoint.
		remeasure := func() error {
			s, err1 := custodyWireLen(*step)
			rl, err2 := mutantResourceLen(m, r)
			pl, err3 := mutantPendingLen(m, w.Pending)
			if err1 != nil || err2 != nil || err3 != nil {
				return ErrCorrupt
			}
			size += s - stepLen + rl - resourceLen + pl - pendingLen
			stepLen, resourceLen, pendingLen = s, rl, pl
			return nil
		}
		if m != mutCheckFirst {
			for range 2 {
				record := consumedWire{text, hash, c.ID, baseWire{Head: &maxHead}}
				if consumedLen < 0 {
					if consumedLen, err = custodyWireLen(record); err != nil {
						return ErrCorrupt
					}
				}
				size += mutantAppendLen(m, w.Consumed, consumedLen)
				w.Consumed = append(w.Consumed, record)
			}
		}
		if step.Status == StepPlanned {
			coordinate := coordinateWire(pr.Coordinate)
			if pr.VersionFrom != "" {
				coordinate.Version = resources[pr.VersionFrom].Coordinate.Version
			}
			step.AttemptID, step.Status, step.Coordinate = text, StepIntent, coordinate
			if r.FirstAttempt == "" {
				r.FirstAttempt = text
			}
			w.Pending = &pendingWire{step.AttemptID, c.ID, pr.Key, hash, c.Input, hash, hash, maxHead, c.Namespaces, coordinate, ps.Operation, custodyMethod(coordinate.Kind, ps.Operation), desired, hash, hash, StepIntent, nil}
			if err := remeasure(); err != nil {
				return err
			}
			if m != mutPeak {
				if err := check(); err != nil {
					return err
				}
			}
		}
		if w.Pending == nil || w.Pending.ResourceKey != pr.Key {
			return ErrCorrupt
		}
		p := w.Pending
		if step.Status == StepIntent {
			coordinate := resolved(p.Coordinate)
			p.Returned = &responseWire{coordinate, es, &hash}
			p.Phase, step.Status, step.Coordinate = StepResponse, StepResponse, coordinate
			if p.Coordinate.Unassigned {
				r.Coordinate = coordinate
			}
			if err := remeasure(); err != nil {
				return err
			}
			if m != mutPeak {
				if err := check(); err != nil {
					return err
				}
			}
		}
		o := observation(p.Returned.Coordinate)
		if ps.Operation == OpDelete {
			o.Presence, o.ManagedHash, o.ServerRevision = PresenceAbsent, "", ""
		} else {
			r.ManagedHash, r.ServerRevision = hash, text
		}
		if len(r.Acquisition) == 0 {
			r.Acquisition = es
		}
		r.Coordinate = o.Coordinate
		r.Status = ResourceNotApplied // longest status, including recovery outcome
		observationLen, err := custodyWireLen(o)
		if err != nil {
			return ErrCorrupt
		}
		size += mutantAppendLen(m, r.Observations, observationLen)
		r.Observations = append(r.Observations, o)
		step.Status, step.Coordinate, step.Evidence = StepNotApplied, o.Coordinate, es
		w.Pending = nil
		if err := remeasure(); err != nil {
			return err
		}
		if m == mutCreditEarly {
			alternativeCredit = 0
			for key, first := range classAlternatives {
				if obs := resources[key].Observations; len(obs) > first+1 {
					one, _ := json.Marshal(obs[first])
					two, _ := json.Marshal(obs[first+1])
					alternativeCredit += min(len(one), len(two)) + 1
				}
			}
		}
		if err := check(); err != nil {
			return err
		}
	}
	// Cleanup has at most disable+delete per resource. The classification
	// alternative appends ONE observation, not both mutation observations. The
	// larger of those two bounds its observation; its metadata contains another
	// full observation plus evidence, so also covers the smaller mutation slot.
	// Deduct only that duplicate virtual slot at the final-stage comparison.
	// No persisted history or actual incoming result is removed or discounted.
	if m == mutCreditEarly {
		alternativeCredit = 0
	}
	for key, first := range classAlternatives {
		obs := resources[key].Observations
		one, err1 := json.Marshal(obs[first])
		two, err2 := json.Marshal(obs[first+1])
		if err1 != nil || err2 != nil {
			return ErrCorrupt
		}
		alternativeCredit += min(len(one), len(two)) + 1
	}
	// A receipt has independent member/read evidence. Never count it as a
	// small identifier-only checkpoint or retain an extra pending reservation.
	members := []memberWire{}
	for _, pr := range c.Plan.Resources {
		r := resources[pr.Key]
		o := observation(r.Coordinate)
		members = append(members, memberWire{r.Key, r.Coordinate, OutcomeRetainedUnsupported, &o, es})
	}
	receipt := receiptWire{text, c.ID, c.Mode, ReceiptApplyObserved, hash, c.Input, hash, hash, c.Namespaces, members, hash, math.MaxInt64, text}
	receiptLen, err := custodyWireLen(receipt)
	if err != nil {
		return ErrCorrupt
	}
	size += mutantAppendLen(m, w.Receipts, receiptLen)
	w.Receipts = append(w.Receipts, receipt)
	if m == mutAlias && len(original.Steps) > 0 {
		original.Steps[0].AttemptID += "alias" // writes through the caller's Document
	}
	return check()
}
