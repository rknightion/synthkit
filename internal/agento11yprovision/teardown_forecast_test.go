// SPDX-License-Identifier: AGPL-3.0-only

//go:build darwin || linux

package agento11yprovision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// SKT-0153.03.04.02: an apply, no-op apply or deactivate construction whose
// teardown custodyBudget would refuse is refused before its first request
// (previewEngine) and again at the Bootstrap / ChangeStart admission. These
// B-level flows drive every backend- and approval-supplied value at its
// encoded maximum (256 escape-heavy bytes, 16 evidence entries, a 64 KiB
// desired body), so a forecast that under-counts any of them is caught.

// forecastText returns a distinct maximum-length, escape-heavy identifier.
// proofStateStep's attempt IDs are 248 '<' plus the generation; these never collide with them.
func forecastText(n int) string { return strings.Repeat("<", 247) + ">" + fmt.Sprintf("%08d", n) }

var forecastSeq int

func forecastID() string { forecastSeq++; return forecastText(forecastSeq) }

// forecastInput is the v2 foundation plan (all ten families from ten
// resources, else the six suite-side records) plus extra evaluators, each
// extra spec padded to pad bytes (2,500 is the frozen per-spec cap). ePad > 0
// pads the base evaluator's own spec to that many bytes.
func forecastInput(t *testing.T, resources, pad, ePad int) PlanInput {
	t.Helper()
	base := proofStatePlan(t, resources >= 10).input
	in := base
	in.Resources = append([]PlannedResource{}, base.Resources...)
	o := in.Targets[0]
	padded := func(id string, n int) AuthoredSpec {
		raw := `{"evaluator_id":"` + id + `","version":"1","kind":"regex","config":{"pattern":"fiction"},"output_keys":[{"key":"p","type":"bool"}],"description":"`
		raw += strings.Repeat("d", max(0, n-len(raw)-2)) + `"}`
		s, e := NewAuthoredSpec([]byte(raw))
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	if ePad > 0 {
		in.Resources[0].Spec = padded(in.Resources[0].Coordinate.ID, ePad)
	}
	for i := 0; len(in.Resources) < resources; i++ {
		id := fmt.Sprintf("x-%02d", i)
		in.Resources = append(in.Resources, PlannedResource{Key: fmt.Sprintf("x%02d", i), Origin: o, Coordinate: Coordinate{Kind: KindEvaluator, ID: id, Version: "1"}, Spec: padded(id, pad), Namespaces: []string{"planner"}})
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
	return in
}

// forecastAuthorize is proofStateAuthorize with an optional recovery block.
func forecastAuthorize(t *testing.T, s *LockedState, p Plan, mode Mode, base Base, construction string, recovery any) (*Authorization, Preview) {
	t.Helper()
	pv, e := NewPreview(p, mode, base, proofDigest('1'))
	if e != nil {
		t.Fatal(e)
	}
	raw := proofApprovalJSON(t, pv, forecastID(), construction, time.Now().Add(-time.Second), time.Now().Add(10*time.Minute), recovery)
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
		t.Fatal(proofStateOperationError(s, base, "forecastAuthorize/Authorize", mode, 0, e))
	}
	return a, pv
}

// forecastLose commits a maximum intent, claims and joins its dispatch, and
// loses the response: the pending attempt stays at StepIntent.
func forecastLose(t *testing.T, s *LockedState, snap Snapshot, a *Authorization, step uint32) Snapshot {
	t.Helper()
	c := findConstruction(snap.Document, a.construction)
	pr := planResource(c.Plan, c.Plan.Steps[step-1].ResourceKey)
	deps := []Coordinate{}
	for _, key := range pr.Dependencies {
		deps = append(deps, findResource(snap.Document, key).Coordinate)
	}
	desired, e := NewAuthoredSpec([]byte(`{"x":"` + strings.Repeat("a", 65536-8) + `"}`))
	if e != nil {
		t.Fatal(e)
	}
	attempt := forecastID()
	snap = proofCommit(t, s, snap, a, Change{Kind: ChangeIntent, ExpectedInventory: proofDigest('1'), ConstructionID: c.ID, AttemptID: attempt, Step: step, Intent: &IntentEvidence{desired, proofDigest('2'), deps}})
	g, e := s.ClaimDispatch(context.Background(), *snap.Base.Head, a, attempt)
	if e != nil {
		t.Fatal(proofStateOperationError(s, snap.Base, "forecastLose/ClaimDispatch", a.preview.mode, step, e))
	}
	joined := make(chan struct{})
	go func() { defer close(joined); _ = g.Context().Err() }()
	<-joined
	if e = s.ReleaseDispatch(g); e != nil {
		t.Fatal(e)
	}
	return snap
}

// forecastRecover settles the lost attempt with maximum attribution and a
// maximum observation, then Starts the fresh suffix approval. Both are the
// two consumed records the forecast reserves per unsettled step. The suffix
// Start is returned with its error: refusing it would strand the construction.
func forecastRecover(t *testing.T, s *LockedState, snap Snapshot, p Plan) (Snapshot, *Authorization, Preview, error) {
	t.Helper()
	pending := snap.Document.Pending
	coordinate := pending.Coordinate
	if coordinate.Unassigned {
		coordinate.Unassigned, coordinate.ID = false, strings.Repeat("<", 256)
		if coordinate.Kind == KindDraft {
			coordinate.Version = coordinate.ID
		}
	}
	attribution := []any{}
	for _, ev := range proofStateEvidence() {
		attribution = append(attribution, map[string]any{"id": ev.ID, "digest": ev.Digest})
	}
	coord := map[string]any{"kind": coordinate.Kind, "id": coordinate.ID, "parent_id": coordinate.ParentID, "version": coordinate.Version, "unassigned": false}
	recovery := map[string]any{"attempt_id": pending.AttemptID, "original_input": pending.Input, "original_preview": pending.Preview, "original_approval": pending.OriginalApproval, "resource_key": pending.ResourceKey, "coordinate": coord, "operation": pending.Operation, "authored_hash": findResource(snap.Document, pending.ResourceKey).Authored.Digest(), "expected_managed_hash": proofDigest('a'), "exact_read": proofDigest('2'), "inventory": proofDigest('1'), "attribution": attribution, "disposition": DispositionObserved}
	ra, _ := forecastAuthorize(t, s, p, ModeRecover, snap.Base, pending.ConstructionID, recovery)
	obs := Observation{coordinate, PresencePresent, proofDigest('a'), proofDigest('2'), proofDigest('1'), strings.Repeat("<", 256), proofStateEvidence()}
	snap = proofCommit(t, s, snap, ra, Change{Kind: ChangeResolve, ExpectedInventory: proofDigest('1'), ConstructionID: pending.ConstructionID, AttemptID: pending.AttemptID, Step: findStep(snap.Document, pending.AttemptID), Observation: &obs})
	// The pre-request forecast for the suffix runs on the settled state too,
	// and is admitted inside the per-step reserve.
	if e := custodyPreviewForecast(snap, p.input.Binding, p, ModeRecover); e != nil {
		t.Fatalf("suffix recovery preview refused inside its reserve: %v", e)
	}
	suffix, sv := forecastAuthorize(t, s, p, ModeRecover, snap.Base, pending.ConstructionID, nil)
	next, e := s.Commit(context.Background(), *snap.Base.Head, suffix, Change{Kind: ChangeStart, ExpectedInventory: proofDigest('1'), ConstructionID: pending.ConstructionID})
	if e != nil {
		return snap, suffix, sv, e
	}
	return next, suffix, sv, nil
}

func forecastStateBytes(t *testing.T, dir string) int {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if errors.Is(e, os.ErrNotExist) {
		return 0
	}
	if e != nil {
		t.Fatal(e)
	}
	return len(raw)
}

// forecastTeardown runs the canonical full teardown at maximum values: every
// Start, intent, result, classification and finish must be admitted.
func forecastTeardown(t *testing.T, s *LockedState, snap Snapshot) Snapshot {
	t.Helper()
	steps := []PlannedStep{}
	add := func(key string, op Operation) { steps = append(steps, PlannedStep{uint32(len(steps) + 1), key, op}) }
	resources := snap.Document.Constructions[0].Plan.Resources
	for _, r := range resources {
		if isConsumer(r.Coordinate.Kind) {
			add(r.Key, OpDisable)
		}
	}
	deleted := map[string]bool{}
	for _, kind := range []ResourceKind{KindAction, KindHook, KindRule, KindEvaluator} {
		for _, r := range resources {
			// The suite's bindings retain "e"; every other evaluator is deletable.
			if r.Coordinate.Kind == kind && r.Key != "e" {
				add(r.Key, OpDelete)
				deleted[r.Key] = true
			}
		}
	}
	cp := proofCleanupPlan(t, snap, ModeTeardown, steps)
	id := forecastID()
	ca, cv := forecastAuthorize(t, s, cp, ModeTeardown, snap.Base, "", nil)
	snap, e := s.Commit(context.Background(), *snap.Base.Head, ca, Change{Kind: ChangeStart, ConstructionID: id, ExpectedInventory: proofDigest('1')})
	if e != nil {
		t.Fatalf("teardown Start refused after an admitted construction: %v", e)
	}
	outcomes := map[string]MemberOutcome{}
	for _, pr := range cp.input.Resources {
		if deleted[pr.Key] {
			continue
		}
		r := findResource(snap.Document, pr.Key)
		reason, outcome := CleanupRetainedUnsupported, OutcomeRetainedUnsupported
		if r.Key == "e" || r.Key == "c" {
			reason, outcome = CleanupRetainedDependency, OutcomeRetainedDependency
		}
		outcomes[r.Key] = outcome
		o := Observation{r.Coordinate, PresencePresent, r.ManagedHash, proofDigest('2'), proofDigest('1'), r.ServerRevision, proofStateEvidence()}
		cl := CleanupClassification{r.Key, r.Coordinate, *snap.Base.Head, reason, &o, proofDigest('1'), proofStateEvidence()}
		if snap, e = s.Commit(context.Background(), *snap.Base.Head, ca, Change{Kind: ChangeClassify, ConstructionID: id, ExpectedInventory: proofDigest('1'), Classification: &cl}); e != nil {
			t.Fatalf("teardown classify %s: %v", r.Key, e)
		}
	}
	for i := range cp.input.Steps {
		snap = proofStateStep(t, s, snap, ca, uint32(i+1))
		outcomes[cp.input.Steps[i].ResourceKey] = OutcomeRemoved
	}
	return proofStateFinish(t, s, snap, ca, cp, cv, forecastID(), outcomes, ReceiptIncomplete)
}

type forecastRun struct {
	stage             string // where the first refusal happened, "" if none
	noops             int    // admitted no-ops
	applied, tornDown int    // state bytes
	recoveries        int
}

// forecastFlow: Bootstrap the plan; lose responses at the listed apply steps
// (each followed by a recovery and a fresh suffix Start); finish; then up to
// noops no-op re-applies; then the canonical teardown, always. The pre-request
// preview forecast is consulted at every Start: it is never laxer than B.
func forecastFlow(t *testing.T, in PlanInput, noops int, lost []uint32) forecastRun {
	t.Helper()
	p := mustPlan(t, in)
	dir := proofDirectory(t)
	s := proofStateStore(t, dir, p.input.Binding)
	preview := func(snap Snapshot, mode Mode) error { return custodyPreviewForecast(snap, p.input.Binding, p, mode) }
	early := preview(Snapshot{Base: Base{Pristine: true}}, ModeApply)
	a, pv := proofStateAuthorize(t, s, p, ModeApply, Base{Pristine: true}, custodyForecastValues(false).id, "")
	snap, e := s.Bootstrap(context.Background(), a)
	if e != nil {
		if !errors.Is(e, ErrCapacity) {
			t.Fatalf("bootstrap: %v", e)
		}
		if !errors.Is(early, ErrCapacity) {
			t.Fatal("Bootstrap refused what the pre-request forecast admitted")
		}
		if forecastStateBytes(t, dir) != 0 {
			t.Fatal("refused bootstrap wrote state")
		}
		return forecastRun{stage: "apply"}
	}
	run := forecastRun{}
	for i := range p.input.Steps {
		step := uint32(i + 1)
		if len(lost) > 0 && lost[0] == step {
			lost = lost[1:]
			snap = forecastLose(t, s, snap, a, step)
			if snap, a, pv, e = forecastRecover(t, s, snap, p); e != nil {
				t.Fatalf("suffix recovery Start refused after an admitted apply (stranded): %v", e)
			}
			run.recoveries++
			continue
		}
		snap = proofStateStep(t, s, snap, a, step)
	}
	outcomes := map[string]MemberOutcome{}
	for _, r := range p.input.Resources {
		outcomes[r.Key] = OutcomePresent
	}
	snap = proofStateFinish(t, s, snap, a, p, pv, forecastID(), outcomes, ReceiptApplyObserved)
	run.applied = forecastStateBytes(t, dir)
	for run.noops < noops {
		early := preview(snap, ModeApply)
		na, nv := forecastAuthorize(t, s, p, ModeApply, snap.Base, snap.Document.Constructions[0].ID, nil)
		head := *snap.Base.Head
		next, e := s.Commit(context.Background(), head, na, Change{Kind: ChangeStart, ExpectedInventory: proofDigest('1'), ConstructionID: snap.Document.Constructions[0].ID})
		if e != nil {
			if !errors.Is(e, ErrCapacity) || !errors.Is(early, ErrCapacity) {
				t.Fatalf("no-op Start: %v (pre-request forecast %v)", e, early)
			}
			loaded, le := s.Load(context.Background())
			if le != nil || *loaded.Base.Head != head {
				t.Fatal("refused no-op changed state")
			}
			run.stage = fmt.Sprintf("noop%d", run.noops+1)
			break
		}
		snap = proofStateFinish(t, s, next, na, p, nv, forecastID(), outcomes, ReceiptApplyObserved)
		run.noops++
	}
	snap = forecastTeardown(t, s, snap)
	run.tornDown = forecastStateBytes(t, dir)
	if limit := custodyStateLimit(p.input.Binding); run.tornDown > limit {
		t.Fatalf("teardown state %d exceeds %d", run.tornDown, limit)
	}
	return run
}

// forecastMax is the measured admitted maximum at pristine state with every
// backend and approval value at its maximum, for the ten-family mix with each
// extra evaluator spec at the frozen 2,500-byte cap: admitted at 10, refused
// at 11.
const forecastMax = 10

// forecastEdge is the largest base-evaluator spec the ten-resource apply still
// admits: the forecast is then within one byte of refusing.
func forecastEdge(t *testing.T) int {
	t.Helper()
	admits := func(n int) bool {
		p := mustPlan(t, forecastInput(t, forecastMax, 2500, n))
		return custodyPreviewForecast(Snapshot{Base: Base{Pristine: true}}, p.input.Binding, p, ModeApply) == nil
	}
	lo, hi := 200, custodySpecBytes
	if !admits(lo) {
		t.Fatal("ten-resource apply refused before padding")
	}
	if admits(hi) {
		return hi
	}
	for hi-lo > 1 {
		if mid := (lo + hi) / 2; admits(mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// Done-when 2: over 6-22 resources, 0-3 planned no-ops and apply-phase
// recovery (one lost response; repeated recoveries with lost responses in the
// suffix), every admitted construction is followed by an admitted teardown
// whose every commit succeeds. The edge rows put the forecast within a byte of
// refusing, so any under-count is a teardown ErrCapacity here.
func TestTeardownForecastAdmittedConstructionsTearDown(t *testing.T) {
	type scenario struct {
		name string
		lost []uint32
	}
	scenarios := []scenario{{"no-loss", nil}, {"one-recovery", []uint32{3}}, {"repeated-suffix-loss", []uint32{1, 3, 7}}}
	sizes := []int{6, 8, 10, 11, 12, 14, 16, 18, 20, 22}
	if budgetRaceBuild() {
		// The race tier keeps the boundary and the strongest recovery shape;
		// the plain tier runs the whole matrix.
		sizes, scenarios = []int{8, 10, 11}, scenarios[2:]
	}
	edge := forecastEdge(t)
	t.Logf("forecast edge base-evaluator spec=%d bytes", edge)
	refused, admitted, noops := 0, 0, 0
	for _, sc := range scenarios {
		for _, n := range append(sizes, -1) {
			in, label := forecastInput(t, n, 2500, 0), fmt.Sprintf("n%d", n)
			if n < 0 {
				in, label = forecastInput(t, forecastMax, 2500, edge), fmt.Sprintf("edge%d", edge)
			}
			t.Run(sc.name+"/"+label, func(t *testing.T) {
				r := forecastFlow(t, in, 3, sc.lost)
				t.Logf("forecast scenario=%s case=%s resources=%d refused=%q noops=%d recoveries=%d applied=%d torn_down=%d", sc.name, label, len(in.Resources), r.stage, r.noops, r.recoveries, r.applied, r.tornDown)
				if r.stage == "apply" {
					refused++
					if len(in.Resources) <= forecastMax {
						t.Fatalf("apply of %d resources refused; measured maximum is %d", len(in.Resources), forecastMax)
					}
					return
				}
				if len(in.Resources) > forecastMax {
					t.Fatalf("apply of %d resources admitted above the measured maximum %d", len(in.Resources), forecastMax)
				}
				admitted++
				noops += r.noops
				if r.stage != "" {
					refused++
				}
			})
		}
	}
	if refused == 0 || admitted == 0 || noops == 0 {
		t.Fatalf("vacuous matrix: admitted=%d refused=%d noops=%d", admitted, refused, noops)
	}
}

// Pin the original maximum-ID counterexample, independently of the dynamic
// admitted-edge search above. Attribute its canonical material before asserting
// the real preview/Bootstrap admission contract.
func TestTeardownForecastCanonicalStart(t *testing.T) {
	p := mustPlan(t, forecastInput(t, forecastMax, 2500, 31600))
	dir := proofDirectory(t)
	s := proofStateStore(t, dir, p.input.Binding)
	id := custodyForecastValues(false).id
	a, _ := proofStateAuthorize(t, s, p, ModeApply, Base{Pristine: true}, id, "")
	d := emptyCustody(p.input.Binding)
	if e := startCustody(&d, a, id); e != nil {
		t.Fatal(e)
	}
	d.Generation = 1
	raw, e := custodyDocumentBytes(d)
	if e != nil {
		t.Fatal(e)
	}
	canonical, e := custodyDocumentDecode(raw)
	if e != nil {
		t.Fatal(e)
	}
	before, _ := json.Marshal(documentToWire(d))
	after, _ := json.Marshal(documentToWire(canonical))
	// Compare every serialized field, not just total lengths. Removing only
	// the checksum must reproduce the pre-codec material byte for byte.
	without := canonical
	without.Checksum = ""
	stripped, _ := json.Marshal(documentToWire(without))
	if string(before) != string(stripped) || string(after) != string(raw) {
		t.Fatal("canonical Start changed material other than its checksum")
	}
	checksumField := `,"checksum":"` + string(canonical.Checksum) + `"`
	if !custodyDigest(canonical.Checksum) || len(after)-len(before) != len(checksumField) {
		t.Fatal("canonical Start delta is not exactly its valid checksum field")
	}
	boundBefore, e1 := custodyForecastBound(d, id)
	boundAfter, e2 := custodyForecastBound(canonical, id)
	if e1 != nil || e2 != nil || boundAfter-boundBefore != len(checksumField) {
		t.Fatalf("forecast material delta: %d -> %d (%v, %v)", boundBefore, boundAfter, e1, e2)
	}
	if custodyBudget(d, id) != nil || custodyBudget(canonical, id) != nil {
		t.Fatal("counterexample is not confined to future teardown capacity")
	}
	if custodyTeardownForecast(d, id) != nil || !errors.Is(custodyTeardownForecast(canonical, id), ErrCapacity) {
		t.Fatal("counterexample no longer crosses the canonical forecast boundary")
	}
	t.Logf("canonical Start bytes=%d -> %d; only checksum field=%d bytes; bound=%d -> %d; exact forecast admit -> refuse", len(before), len(after), len(checksumField), boundBefore, boundAfter)
	if _, e := s.Bootstrap(context.Background(), a); !errors.Is(e, ErrCapacity) {
		t.Fatalf("canonical Bootstrap: %v", e)
	}
	if loaded, e := s.Load(context.Background()); e != nil || !loaded.Base.Pristine || forecastStateBytes(t, dir) != 0 {
		t.Fatal("refused canonical Bootstrap changed pristine state")
	}
	if e := custodyPreviewForecast(Snapshot{Base: Base{Pristine: true}}, p.input.Binding, p, ModeApply); !errors.Is(e, ErrCapacity) {
		t.Fatalf("preview admits canonical Bootstrap refusal: %v", e)
	}
}

func TestTeardownForecastMeasuredMaximum(t *testing.T) {
	for _, pad := range []int{0, 2500} {
		highest := 0
		for n := 10; n <= 22; n++ {
			p := mustPlan(t, forecastInput(t, n, pad, 0))
			if custodyPreviewForecast(Snapshot{Base: Base{Pristine: true}}, p.input.Binding, p, ModeApply) == nil {
				highest = n
			}
		}
		t.Logf("forecast measured maximum pad=%d resources=%d", pad, highest)
		if highest != forecastMax {
			t.Fatalf("pad=%d measured maximum %d, recorded %d", pad, highest, forecastMax)
		}
	}
}

// A deactivation is a construction too: at the measured maximum with every
// value at its maximum, its own end document leaves no teardown room, so its
// Start is refused with no state change and the teardown stays admitted.
func TestTeardownForecastDeactivateChecksItself(t *testing.T) {
	p := mustPlan(t, forecastInput(t, forecastMax, 2500, 0))
	dir := proofDirectory(t)
	s := proofStateStore(t, dir, p.input.Binding)
	a, pv := forecastAuthorize(t, s, p, ModeApply, Base{Pristine: true}, "", nil)
	snap, e := s.Bootstrap(context.Background(), a)
	if e != nil {
		t.Fatal(e)
	}
	for i := range p.input.Steps {
		snap = proofStateStep(t, s, snap, a, uint32(i+1))
	}
	outcomes := map[string]MemberOutcome{}
	for _, r := range p.input.Resources {
		outcomes[r.Key] = OutcomePresent
	}
	snap = proofStateFinish(t, s, snap, a, p, pv, forecastID(), outcomes, ReceiptApplyObserved)
	dp := proofCleanupPlan(t, snap, ModeDeactivate, []PlannedStep{{1, "r", OpDisable}, {2, "h", OpDisable}, {3, "a", OpDisable}})
	if e = custodyPreviewForecast(snap, p.input.Binding, dp, ModeDeactivate); !errors.Is(e, ErrCapacity) {
		t.Fatalf("pre-request deactivate forecast: %v", e)
	}
	da, _ := forecastAuthorize(t, s, dp, ModeDeactivate, snap.Base, "", nil)
	head := *snap.Base.Head
	if _, e = s.Commit(context.Background(), head, da, Change{Kind: ChangeStart, ConstructionID: forecastID(), ExpectedInventory: proofDigest('1')}); !errors.Is(e, ErrCapacity) {
		t.Fatalf("deactivate Start: %v", e)
	}
	if loaded, le := s.Load(context.Background()); le != nil || *loaded.Base.Head != head {
		t.Fatal("refused deactivation changed state")
	}
	forecastTeardown(t, s, snap)
}

// forecastTransport counts every request the engine client sends and answers
// exact reads of the never-created extra evaluators with a 404 observation,
// which the shared fixture's handler has no route for.
type forecastTransport struct {
	mu       sync.Mutex
	next     http.RoundTripper
	requests int
}

func (f *forecastTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.requests++
	f.mu.Unlock()
	if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/evaluators/eval-extra-") {
		return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{"Content-Type": {"text/plain; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader("404 page not found\n")), ContentLength: 19, Request: r}, nil
	}
	return f.next.RoundTrip(r)
}

// forecastEngine is the TLS engine fixture (all ten families) with extra
// evaluators appended, as the capacity sweep built its 20-resource set.
func forecastEngine(t *testing.T, resources int) (*engineTLSFixture, *forecastTransport) {
	t.Helper()
	f := newEngineTLS(t, true)
	tr := &forecastTransport{next: f.server.Client().Transport}
	c, e := NewClient(ClientOptions{BaseURL: f.server.URL + "/sub", Token: "fixture-token", ExpectedTenantID: "tenant", HTTPClient: &http.Client{Transport: tr}, Timeout: time.Second})
	if e != nil {
		t.Fatal(e)
	}
	f.client = c
	a := f.input.Targets[0]
	for i := 0; len(f.input.Resources) < resources; i++ {
		id := fmt.Sprintf("eval-extra-%02d", i)
		s, e := NewAuthoredSpec([]byte(`{"evaluator_id":"` + id + `","version":"1","kind":"regex","description":" Fictional check ","config":{"pattern":" fiction ","target":"response","reject":false},"output_keys":[{"key":" passes ","type":"bool","pass_value":false}]}`))
		if e != nil {
			t.Fatal(e)
		}
		f.input.Resources = append(f.input.Resources[:1+i], append([]PlannedResource{{Key: fmt.Sprintf("x%02d", i), Origin: a, Coordinate: Coordinate{Kind: KindEvaluator, ID: id, Version: "1"}, Spec: s, Namespaces: []string{"planner"}}}, f.input.Resources[1+i:]...)...)
	}
	f.input.Steps = nil
	for i, r := range f.input.Resources {
		op := OpCreate
		if r.Coordinate.Kind == KindBindings {
			op = OpInitialBindings
		}
		if r.Coordinate.Kind == KindPublication {
			op = OpPublish
		}
		f.input.Steps = append(f.input.Steps, PlannedStep{Number: uint32(i + 1), ResourceKey: r.Key, Operation: op})
	}
	return f, tr
}

// Done-when 1: the 20-resource set the capacity sweep applied (and whose
// teardown was then refused) is refused by previewEngine after zero requests,
// and, with a preview built around it, by Bootstrap before any mutation. The
// first set above the measured maximum is refused the same way; the maximum
// itself is TestEngineAllFamiliesApplyNoopCleanupTLS (apply, no-op, teardown).
func TestTeardownForecastRefusesBeforeRequestsTLS(t *testing.T) {
	for _, n := range []int{forecastMax + 1, 20} {
		t.Run(fmt.Sprintf("n%d", n), func(t *testing.T) {
			f, tr := forecastEngine(t, n)
			p := f.plan()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, e := previewEngine(ctx, f.client, f.store, p, ModeApply); !errors.Is(e, ErrCapacity) || tr.requests != 0 || len(f.calls) != 0 {
				t.Fatalf("stranding apply of %d resources not refused before requests: %v requests=%d", n, e, tr.requests)
			}
			s := engineSnapshot(t, f)
			inv, e := engineInventoryRead(ctx, f.client, p, s)
			if e != nil {
				t.Fatal(e)
			}
			pv, e := NewPreview(p.plan, ModeApply, s.Base, inv.Digest)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = applyEngine(ctx, f.client, f.store, p, pv, engineApproval(t, pv, "", nil)); !errors.Is(e, ErrCapacity) || len(f.writes) != 0 {
				t.Fatalf("Bootstrap admitted a stranding apply: %v writes=%v", e, f.writes)
			}
			if d, e := f.persistedDocument(); e != nil || d != nil {
				t.Fatalf("refused apply persisted state: %v", e)
			}
		})
	}
}

// Exercise the same canonical-material boundary through the real TLS engine
// preview. Search using Bootstrap's codec, not the preview under test; URL and
// typed-spec lengths differ from the fixed foundation counterexample.
func TestTeardownForecastCanonicalStartBeforeRequestsTLS(t *testing.T) {
	f, tr := forecastEngine(t, forecastMax)
	var spec map[string]json.RawMessage
	if e := json.Unmarshal(f.input.Resources[0].Spec.JSON(), &spec); e != nil {
		t.Fatal(e)
	}
	id := custodyForecastValues(false).id
	at := func(pad int) (enginePlan, *Authorization, Document, Document) {
		spec["description"], _ = json.Marshal(strings.Repeat("d", pad))
		raw, _ := json.Marshal(spec)
		var e error
		f.input.Resources[0].Spec, e = NewAuthoredSpec(raw)
		if e != nil {
			t.Fatal(e)
		}
		p := f.plan()
		a, _ := proofStateAuthorize(t, f.store, p.plan, ModeApply, Base{Pristine: true}, id, "")
		d := emptyCustody(p.plan.input.Binding)
		if e := startCustody(&d, a, id); e != nil {
			t.Fatal(e)
		}
		d.Generation = 1
		raw, e = custodyDocumentBytes(d)
		if e != nil {
			t.Fatal(e)
		}
		canonical, e := custodyDocumentDecode(raw)
		if e != nil {
			t.Fatal(e)
		}
		return p, a, d, canonical
	}
	admits := func(pad int) bool {
		_, _, _, d := at(pad)
		e := custodyTeardownForecast(d, id)
		if e != nil && !errors.Is(e, ErrCapacity) {
			t.Fatal(e)
		}
		return e == nil
	}
	lo, hi := 1, 50000
	if !admits(lo) || admits(hi) {
		t.Fatal("canonical admission boundary not bracketed")
	}
	for hi-lo > 1 {
		if mid := (lo + hi) / 2; admits(mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	p, a, before, canonical := at(hi)
	if custodyTeardownForecast(before, id) != nil || custodyBudget(canonical, id) != nil {
		t.Fatal("TLS counterexample must isolate missing canonical forecast material")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, e := f.store.Bootstrap(ctx, a); !errors.Is(e, ErrCapacity) {
		t.Fatalf("canonical Bootstrap at description=%d: %v", hi, e)
	}
	if _, e := previewEngine(ctx, f.client, f.store, p, ModeApply); !errors.Is(e, ErrCapacity) || tr.requests != 0 || len(f.calls) != 0 {
		t.Fatalf("canonical boundary not refused before requests: %v requests=%d calls=%v", e, tr.requests, f.calls)
	}
	if loaded := engineSnapshot(t, f); !loaded.Base.Pristine || len(f.writes) != 0 {
		t.Fatal("canonical refusal changed pristine state or backend")
	}
	t.Logf("canonical TLS boundary description=%d; preview and Bootstrap refuse with zero requests", hi)
}

// The fast path's bound must dominate every size custodyBudget compares on
// the forecast teardown document, so its admissions are custodyBudget's own.
// Checked against the differential oracle's traced reference on documents at
// pristine, mid-apply, completed-apply and edge states, with maximum and with
// short backend values.
func TestTeardownForecastBoundDominatesBudget(t *testing.T) {
	type doc struct {
		name         string
		d            Document
		construction string
	}
	docs := []doc{}
	for _, in := range []PlanInput{forecastInput(t, 6, 2500, 0), forecastInput(t, forecastMax, 2500, 0), forecastInput(t, forecastMax, 2500, forecastEdge(t)), forecastInput(t, forecastMax+1, 2500, 0)} {
		p := mustPlan(t, in)
		s := proofStateStore(t, proofDirectory(t), p.input.Binding)
		a, pv := proofStateAuthorize(t, s, p, ModeApply, Base{Pristine: true}, custodyForecastValues(false).id, "")
		snap, e := s.Bootstrap(context.Background(), a)
		if e != nil {
			// Above the maximum: size the would-be Bootstrap document directly.
			d := emptyCustody(p.input.Binding)
			b := &Authorization{approval: Approval{document: ApprovalDocument{ApprovalID: forecastID()}, hash: proofDigest('a')}, preview: Preview{plan: p, mode: ModeApply, hash: proofDigest('b')}, chain: Base{Pristine: true}}
			if e = startCustody(&d, b, b.approval.document.ApprovalID); e != nil {
				t.Fatal(e)
			}
			docs = append(docs, doc{fmt.Sprintf("n%d-refused-bootstrap", len(in.Resources)), d, b.approval.document.ApprovalID})
			continue
		}
		id := snap.Document.Constructions[0].ID
		docs = append(docs, doc{fmt.Sprintf("n%d-bootstrap", len(in.Resources)), *snap.Document, id})
		for i := range p.input.Steps {
			snap = proofStateStep(t, s, snap, a, uint32(i+1))
			if i == 3 {
				docs = append(docs, doc{fmt.Sprintf("n%d-mid", len(in.Resources)), *snap.Document, id})
			}
		}
		outcomes := map[string]MemberOutcome{}
		for _, r := range p.input.Resources {
			outcomes[r.Key] = OutcomePresent
		}
		snap = proofStateFinish(t, s, snap, a, p, pv, forecastID(), outcomes, ReceiptApplyObserved)
		docs = append(docs, doc{fmt.Sprintf("n%d-complete", len(in.Resources)), *snap.Document, id})
	}
	short, _ := proofApply(t, proofStore(t, proofDirectory(t), false))
	docs = append(docs, doc{"short-values-complete", *short.Document, short.Document.Constructions[0].ID})
	for _, x := range docs {
		td, id, e := custodyForecastDocument(x.d, x.construction, custodyForecastValues(false))
		if e != nil {
			t.Fatalf("%s: %v", x.name, e)
		}
		bound, e := custodyForecastBound(x.d, x.construction)
		if e != nil {
			t.Fatalf("%s bound: %v", x.name, e)
		}
		peak := 0
		want := custodyBudgetReference(td, id, func(n int) { peak = max(peak, n) })
		limit := custodyStateLimit(td.Binding)
		t.Logf("forecast bound %s: bound=%d peak=%d limit=%d reference=%v", x.name, bound, peak, limit, want)
		if peak == 0 || bound < peak {
			t.Fatalf("%s: bound %d does not dominate the traced peak %d", x.name, bound, peak)
		}
		// Tight as well as sound where the reference traced the whole protocol.
		if want == nil && bound-peak > 32<<10 {
			t.Fatalf("%s: bound %d is %d bytes above the traced peak", x.name, bound, bound-peak)
		}
		if bound <= limit && want != nil {
			t.Fatalf("%s: bound admits what custodyBudget refuses: %v", x.name, want)
		}
		if got := custodyBudget(td, id); !errors.Is(got, want) && !(got == nil && want == nil) {
			t.Fatalf("%s: custodyBudget %v, reference %v", x.name, got, want)
		}
	}
}

// A colliding legal identity must not shrink the maximum encoded envelope.
// Exercise both construction and consumed-approval collisions, past the old
// two-ASCII-character fallback's entire candidate set.
func TestTeardownForecastCollisionEncoding(t *testing.T) {
	m := custodyForecastValues(false)
	d := Document{Constructions: []Construction{{ID: m.id}}, Consumed: []ConsumedApproval{{ID: m.text}}}
	for range 700 {
		id := custodyForecastID(&d, m.id)
		raw, e := json.Marshal(id)
		if e != nil || !custodyText(id, false) || len(raw) != 2+6*256 {
			t.Fatalf("collision ID is not maximum encoded custodyText: bytes=%d error=%v", len(raw), e)
		}
		if findConstruction(&d, id) != nil || slices.ContainsFunc(d.Consumed, func(x ConsumedApproval) bool { return x.ID == id }) {
			t.Fatal("collision ID already recorded")
		}
		d.Consumed = append(d.Consumed, ConsumedApproval{ID: id})
	}
}

// Check the real Start boundary on checksummed near-overflow history: the
// current apply's reserve, the future teardown Start and its reserve must fit
// together, not merely fit independently at the original generation.
func TestTeardownForecastCombinedGenerationReserve(t *testing.T) {
	finish := func(store *LockedState, snap Snapshot, auth *Authorization, p Plan, pv Preview, outcomes map[string]MemberOutcome, result ReceiptResult) Snapshot {
		c := snap.Document.Constructions[len(snap.Document.Constructions)-1]
		r := proofCleanupReceipt(t, snap, p, pv, auth, forecastID(), c.ID, proofDigest('1'), outcomes, result)
		r.BindingDigest, r.Namespaces = custodyBindingHash(p.input.Binding), p.names
		return proofCommit(t, store, snap, auth, Change{Kind: ChangeFinish, ExpectedInventory: proofDigest('1'), ConstructionID: c.ID, Receipt: &r})
	}
	// proofStateStep's decimal-generation attempt ID exceeds 256 bytes at
	// this boundary. Use a bounded ID while driving the same public commits.
	step := func(store *LockedState, snap Snapshot, auth *Authorization, n uint32) Snapshot {
		snap = forecastLose(t, store, snap, auth, n)
		pending := *snap.Document.Pending
		co := pending.Coordinate
		snap = proofCommit(t, store, snap, auth, Change{Kind: ChangeResponse, ExpectedInventory: proofDigest('1'), ConstructionID: pending.ConstructionID, AttemptID: pending.AttemptID, Step: n, Response: &ResponseEvidence{Coordinate: co, Evidence: proofStateEvidence()}})
		o := Observation{co, PresencePresent, proofDigest('a'), proofDigest('2'), proofDigest('1'), strings.Repeat("<", 256), proofStateEvidence()}
		if pending.Operation == OpDelete {
			o.Presence, o.ManagedHash, o.ServerRevision = PresenceAbsent, "", ""
		}
		return proofCommit(t, store, snap, auth, Change{Kind: ChangeObserve, ExpectedInventory: proofDigest('1'), ConstructionID: pending.ConstructionID, AttemptID: pending.AttemptID, Step: n, Observation: &o})
	}
	in := proofInput(t)
	in.Binding.ContractRevision = proofStateV2Revision
	in.Targets = in.Targets[:1]
	in.Resources, in.Steps = in.Resources[:1], in.Steps[:1]
	p := mustPlan(t, in)
	s := proofStateStore(t, proofDirectory(t), in.Binding)
	a, pv := forecastAuthorize(t, s, p, ModeApply, Base{Pristine: true}, "", nil)
	snap, e := s.Bootstrap(context.Background(), a)
	if e != nil {
		t.Fatal(e)
	}
	snap = proofStateStep(t, s, snap, a, 1)
	snap = finish(s, snap, a, p, pv, map[string]MemberOutcome{"e": OutcomePresent}, ReceiptApplyObserved)
	history := *snap.Document
	in = proofInput(t)
	in.Binding = history.Binding
	in.Targets = in.Targets[1:2]
	in.Resources, in.Steps = in.Resources[:1], in.Steps[:1]
	in.Resources[0].Key, in.Resources[0].Coordinate.ID = "new", "new-evaluator"
	in.Resources[0].Origin, in.Resources[0].Namespaces = in.Targets[0], []string{"source"}
	in.Steps[0].ResourceKey = "new"
	nextPlan := mustPlan(t, in)
	// After Start: apply reserves 5, teardown Start costs 1, and teardown
	// reserves Finish + 2*4 steps + 2 classifications = 11: total 17.
	for _, remaining := range []uint64{13, 17, 18} {
		t.Run(fmt.Sprintf("remaining%d", remaining), func(t *testing.T) {
			d := history
			d.Generation = math.MaxUint64 - remaining
			dir := proofDirectory(t)
			proofStateInstall(t, dir, d)
			store := proofStateStore(t, dir, d.Binding)
			before, e := store.Load(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			previewErr := custodyPreviewForecast(before, d.Binding, nextPlan, ModeApply)
			auth, view := forecastAuthorize(t, store, nextPlan, ModeApply, before.Base, "", nil)
			id := forecastID()
			after, startErr := store.Commit(context.Background(), *before.Base.Head, auth, Change{Kind: ChangeStart, ConstructionID: id, ExpectedInventory: proofDigest('1')})
			if remaining < 18 {
				if !errors.Is(previewErr, ErrCapacity) || !errors.Is(startErr, ErrCapacity) {
					t.Fatalf("combined generation reserve admitted: preview=%v Start=%v", previewErr, startErr)
				}
				loaded, le := store.Load(context.Background())
				if le != nil || *loaded.Base.Head != *before.Base.Head {
					t.Fatal("refused Start changed state")
				}
				return
			}
			if previewErr != nil || startErr != nil {
				t.Fatalf("exact combined reserve refused: preview=%v Start=%v", previewErr, startErr)
			}
			future, tid, fe := custodyForecastDocument(*after.Document, id, custodyForecastValues(false))
			if fe != nil || future.Generation != math.MaxUint64-11 || custodyBudget(future, tid) != nil {
				t.Fatalf("future generation does not include apply reserve + teardown Start: generation=%d error=%v", future.Generation, fe)
			}
			after = step(store, after, auth, 1)
			after = finish(store, after, auth, nextPlan, view, map[string]MemberOutcome{"new": OutcomePresent}, ReceiptApplyObserved)
			cleanup := proofInput(t)
			cleanup.Binding, cleanup.Mode = d.Binding, ModeTeardown
			cleanup.Resources = append(slices.Clone(p.input.Resources), nextPlan.input.Resources...)
			for i := range cleanup.Resources {
				cleanup.Resources[i].Coordinate = findResource(after.Document, cleanup.Resources[i].Key).Coordinate
			}
			cleanup.Steps = []PlannedStep{{1, "e", OpDelete}, {2, "new", OpDelete}}
			cp := mustPlan(t, cleanup)
			ca, cv := forecastAuthorize(t, store, cp, ModeTeardown, after.Base, "", nil)
			cid := forecastID()
			after = proofCommit(t, store, after, ca, Change{Kind: ChangeStart, ConstructionID: cid, ExpectedInventory: proofDigest('1')})
			for i := range cp.input.Steps {
				after = step(store, after, ca, uint32(i+1))
			}
			finish(store, after, ca, cp, cv, map[string]MemberOutcome{"e": OutcomeRemoved, "new": OutcomeRemoved}, ReceiptFullyRemoved)
		})
	}
}

// BenchmarkTeardownForecast prices one Start check on the ten-family plan:
// at Bootstrap (fast path) and on a completed apply with short backend values,
// the no-op re-apply shape the engine tests take (exact path when the bound is
// above the limit).
func BenchmarkTeardownForecast(b *testing.B) {
	t := &testing.T{}
	p := mustPlan(t, forecastInput(t, forecastMax, 2500, 0))
	d := emptyCustody(p.input.Binding)
	a := &Authorization{approval: Approval{document: ApprovalDocument{ApprovalID: "bench"}, hash: proofDigest('a')}, preview: Preview{plan: p, mode: ModeApply, hash: proofDigest('b')}, chain: Base{Pristine: true}}
	if e := startCustody(&d, a, "bench"); e != nil {
		b.Fatal(e)
	}
	b.Run("bootstrap", func(b *testing.B) {
		for range b.N {
			if e := custodyTeardownForecast(d, "bench"); e != nil {
				b.Fatal(e)
			}
		}
	})
	short := d
	short.Resources = slices.Clone(d.Resources)
	short.Constructions = slices.Clone(d.Constructions)
	c := &short.Constructions[0]
	c.Steps = slices.Clone(c.Steps)
	for i := range c.Steps {
		r := findResource(&short, c.Plan.Steps[i].ResourceKey)
		co := r.Coordinate
		if co.Unassigned {
			co.Unassigned, co.ID = false, "generated-"+r.Key
			if co.Kind == KindDraft {
				co.Version = co.ID
			}
		}
		r.Coordinate, r.Status, r.ManagedHash, r.Acquisition = co, ResourceOwned, proofDigest('a'), []Evidence{{"response", proofDigest('e')}}
		r.Observations = []Observation{{co, PresencePresent, proofDigest('a'), proofDigest('2'), proofDigest('1'), "", []Evidence{{"read-back", proofDigest('f')}}}}
		c.Steps[i].Status, c.Steps[i].Coordinate, c.Steps[i].Evidence = StepVerified, co, []Evidence{{"read-back", proofDigest('f')}}
	}
	c.Complete = true
	exact, _ := custodyForecastBound(short, "bench")
	b.Run(fmt.Sprintf("complete-short-bound%d", exact), func(b *testing.B) {
		for range b.N {
			_ = custodyTeardownForecast(short, "bench")
		}
	})
}
