// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/fleet"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// fakeConstruct records ticks and emits one metric series + one log stream.
type fakeConstruct struct {
	kind    string
	ticks   int
	worlds  []*core.World
	labels  map[string]string // the labels map it writes (to assert clone-before-stamp)
	signals []core.SignalClass
}

func (f *fakeConstruct) Kind() string                { return f.kind }
func (f *fakeConstruct) Signals() []core.SignalClass { return f.signals }
func (f *fakeConstruct) Interval() time.Duration     { return 60 * time.Second }
func (f *fakeConstruct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	f.ticks++
	f.worlds = append(f.worlds, w)
	if w.Metrics != nil {
		f.labels = map[string]string{"cluster": "c1"}
		if err := w.Metrics.Write(ctx, []promrw.Series{{Name: "fake_metric_total", Labels: f.labels, Value: 1, T: now}}); err != nil {
			return err
		}
	}
	if w.Logs != nil {
		if err := w.Logs.Write(ctx, []loki.Stream{{Labels: map[string]string{"source": "fake"}, Lines: []loki.Line{{T: now, Body: "x"}}}}); err != nil {
			return err
		}
	}
	return nil
}

// fakeWorkload mints one request per tick and records ProjectBatch deliveries.
type fakeWorkload struct {
	name    string
	ticks   int
	batches [][]*ledger.Request
	world   *core.World
}

func (f *fakeWorkload) Kind() string { return "fake_workload" }
func (f *fakeWorkload) Name() string { return f.name }
func (f *fakeWorkload) Signals() []core.SignalClass {
	return []core.SignalClass{core.Metrics, core.Traces, core.Logs}
}
func (f *fakeWorkload) Interval() time.Duration { return 60 * time.Second }
func (f *fakeWorkload) Minter() ledger.Minter   { return &fakeMinter{workload: f.name} }
func (f *fakeWorkload) Tick(ctx context.Context, now time.Time, w *core.World) error {
	f.ticks++
	f.world = w
	return nil
}
func (f *fakeWorkload) ProjectBatch(ctx context.Context, now time.Time, w *core.World, batch []*ledger.Request) error {
	f.batches = append(f.batches, batch)
	if w.Traces != nil {
		_ = w.Traces.Write(ctx, []otlp.Resource{{Attrs: map[string]any{"service.name": f.name}}})
	}
	return nil
}

type fakeMinter struct{ workload string }

func (m *fakeMinter) Workload() string { return m.workload }
func (m *fakeMinter) Mint(now time.Time, tickSec float64, _ *shape.Engine) []*ledger.Request {
	return []*ledger.Request{{Correlation: ledger.NewCorrelation(), Workload: m.workload, Start: now}}
}

// buildTestResolved fabricates a Resolved with one substrate construct, one
// blueprint-scoped construct, and one workload, bypassing YAML.
func buildTestResolved(name string) *blueprint.Resolved {
	cl := &fixture.Cluster{Name: name + "-cluster", Env: &fixture.Env{Name: "prod", Weight: 1}}
	return &blueprint.Resolved{
		Name:  name,
		Label: name,
		Constructs: []blueprint.ConstructInstance{
			{Kind: "fake_substrate", Name: "sub1", Config: &struct{}{}, Fixtures: &fixture.Set{Seed: name, Cluster: cl}},
			{Kind: "fake_scoped", Name: "sc1", Config: &struct{}{}, Fixtures: &fixture.Set{Seed: name}},
		},
		Workloads: []blueprint.WorkloadInstance{
			{Kind: "fake_workload", Name: name + "-api", Config: &struct{}{}, Replicas: 2,
				Env: cl.Env, Cluster: cl},
		},
	}
}

func testRegistry(subs, scs *[]*fakeConstruct, wls *[]*fakeWorkload) *core.Registry {
	reg := core.NewRegistry()
	reg.RegisterConstruct(core.ConstructReg{
		Kind: "fake_substrate", Doc: "t", Scope: core.ScopeSubstrate,
		NewConfig: func() any { return &struct{}{} },
		Build: func(cfg any, fx *fixture.Set) (core.Construct, error) {
			c := &fakeConstruct{kind: "fake_substrate", signals: []core.SignalClass{core.Metrics, core.Logs}}
			*subs = append(*subs, c)
			return c, nil
		},
	})
	reg.RegisterConstruct(core.ConstructReg{
		Kind: "fake_scoped", Doc: "t", Scope: core.ScopeBlueprint,
		NewConfig: func() any { return &struct{}{} },
		Build: func(cfg any, fx *fixture.Set) (core.Construct, error) {
			c := &fakeConstruct{kind: "fake_scoped", signals: []core.SignalClass{core.Metrics}}
			*scs = append(*scs, c)
			return c, nil
		},
	})
	reg.RegisterWorkload(core.WorkloadReg{
		Kind: "fake_workload", Doc: "t",
		NewConfig: func() any { return &struct{}{} },
		Build: func(cfg any, b core.Binding) (core.Workload, error) {
			w := &fakeWorkload{name: b.Name}
			*wls = append(*wls, w)
			return w, nil
		},
	})
	return reg
}

func newTestRunner(t *testing.T) (*Runner, *coretest.MetricCapture, *coretest.LogCapture, *coretest.TraceCapture, *[]*fakeConstruct, *[]*fakeConstruct, *[]*fakeWorkload) {
	t.Helper()
	subs, scs, wls := &[]*fakeConstruct{}, &[]*fakeConstruct{}, &[]*fakeWorkload{}
	mc, lc, tc := &coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{}
	r := New(Sinks{Metrics: mc, Logs: lc, Traces: tc}, testRegistry(subs, scs, wls), Options{})
	return r, mc, lc, tc, subs, scs, wls
}

// TestTickObserverWrapsEveryTick proves the self-obs tick seam wraps each instance Tick exactly
// once, runs fn exactly once (constructs still tick), propagates fn's error unchanged, and that the
// gauge accessors report sane values. nil-observer behaviour is covered by every other test here.
func TestTickObserverWrapsEveryTick(t *testing.T) {
	r, _, _, _, subs, scs, _ := newTestRunner(t)
	if err := r.AddBlueprint(buildTestResolved("alpha")); err != nil {
		t.Fatalf("AddBlueprint: %v", err)
	}

	counts := map[string]int{}
	r.SetTickObserver(func(ctx context.Context, blueprint, kind, name string, fn func(context.Context) error) error {
		counts[name]++
		return fn(ctx) // MUST run fn and return its error unchanged
	})

	if err := r.RunOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Each construct wrapped exactly once, and fn actually ran (the construct ticked).
	if counts["sub1"] != 1 || counts["sc1"] != 1 {
		t.Fatalf("construct ticks not wrapped exactly once: %v", counts)
	}
	if (*subs)[0].ticks != 1 || (*scs)[0].ticks != 1 {
		t.Fatalf("observeTick did not run fn exactly once: sub=%d sc=%d", (*subs)[0].ticks, (*scs)[0].ticks)
	}
	// The workload is wrapped at least once (its RunOnce Tick; plus its MasterTick ProjectBatch when
	// it minted requests).
	if counts["alpha-api"] < 1 {
		t.Fatalf("workload tick not wrapped: %v", counts)
	}

	// Gauge accessors.
	if got := r.BlueprintCount(); got != 1 {
		t.Errorf("BlueprintCount = %d, want 1", got)
	}
	if got := r.VolumeMultiplier(); got != 1.0 {
		t.Errorf("VolumeMultiplier = %v, want 1.0 (default)", got)
	}
	if r.LedgerSize() < 0 {
		t.Errorf("LedgerSize = %d, want ≥0", r.LedgerSize())
	}
}

func TestAddBlueprintBuildsInstances(t *testing.T) {
	r, _, _, _, subs, scs, wls := newTestRunner(t)
	if err := r.AddBlueprint(buildTestResolved("alpha")); err != nil {
		t.Fatalf("AddBlueprint: %v", err)
	}
	if len(*subs) != 1 || len(*scs) != 1 || len(*wls) != 1 {
		t.Fatalf("instances built: %d/%d/%d, want 1/1/1", len(*subs), len(*scs), len(*wls))
	}
}

func TestScopedWriterStampsBlueprintLabelWithClone(t *testing.T) {
	r, mc, _, _, subs, scs, _ := newTestRunner(t)
	if err := r.AddBlueprint(buildTestResolved("alpha")); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := r.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	var stamped, unstamped int
	for _, s := range mc.All() {
		if s.Name != "fake_metric_total" {
			continue
		}
		if bp, ok := s.Labels["blueprint"]; ok {
			if bp != "alpha" {
				t.Fatalf("blueprint label = %q", bp)
			}
			stamped++
		} else {
			unstamped++
		}
	}
	if stamped != 1 || unstamped != 1 {
		t.Fatalf("stamped/unstamped = %d/%d, want 1/1 (scoped stamps, substrate never)", stamped, unstamped)
	}
	// Clone-before-stamp: the construct's own label map must NOT have been mutated.
	if _, leaked := (*scs)[0].labels["blueprint"]; leaked {
		t.Fatalf("scoped writer mutated the construct's label map (must clone)")
	}
	if _, leaked := (*subs)[0].labels["blueprint"]; leaked {
		t.Fatalf("substrate writer mutated labels")
	}
}

func TestConstructWorldHasNilLedgerWorkloadHasLedger(t *testing.T) {
	r, _, _, _, subs, _, wls := newTestRunner(t)
	_ = r.AddBlueprint(buildTestResolved("alpha"))
	_ = r.RunOnce(context.Background(), time.Now())
	if (*subs)[0].worlds[0].Ledger != nil {
		t.Fatalf("construct World must have nil Ledger")
	}
	if (*wls)[0].world.Ledger == nil {
		t.Fatalf("workload World must carry the blueprint ledger")
	}
}

func TestSignalDeclarationGatesWriters(t *testing.T) {
	r, _, _, _, subs, scs, _ := newTestRunner(t)
	_ = r.AddBlueprint(buildTestResolved("alpha"))
	_ = r.RunOnce(context.Background(), time.Now())
	// fake_scoped declares Metrics only → Logs/Traces writers must be nil.
	w := (*scs)[0].worlds[0]
	if w.Metrics == nil || w.Logs != nil || w.Traces != nil {
		t.Fatalf("writer gating wrong for metrics-only construct: %+v", w)
	}
	ws := (*subs)[0].worlds[0]
	if ws.Metrics == nil || ws.Logs == nil {
		t.Fatalf("substrate declares Metrics+Logs; writers missing")
	}
}

func TestMasterTickDispatchesOwnBatchOnly(t *testing.T) {
	r, _, _, _, _, _, wls := newTestRunner(t)
	_ = r.AddBlueprint(buildTestResolved("alpha"))
	_ = r.AddBlueprint(buildTestResolved("beta"))
	now := time.Now()
	if err := r.MasterTick(context.Background(), now); err != nil {
		t.Fatalf("MasterTick: %v", err)
	}
	if len(*wls) != 2 {
		t.Fatalf("workloads: %d", len(*wls))
	}
	for _, w := range *wls {
		if len(w.batches) != 1 || len(w.batches[0]) != 1 {
			t.Fatalf("workload %q got %d batches", w.name, len(w.batches))
		}
		if got := w.batches[0][0].Workload; got != w.name {
			t.Fatalf("workload %q received foreign request for %q", w.name, got)
		}
	}
}

func TestTraceStampingOnScopedWorkload(t *testing.T) {
	r, _, _, tc, _, _, _ := newTestRunner(t)
	_ = r.AddBlueprint(buildTestResolved("alpha"))
	_ = r.MasterTick(context.Background(), time.Now())
	// Delivery is decoupled from the tick (I35): MasterTick enqueues; Flush ships synchronously.
	_ = r.Flush(context.Background())
	if len(tc.Resources) != 1 {
		t.Fatalf("traces: %d", len(tc.Resources))
	}
	if tc.Resources[0].Attrs["blueprint"] != "alpha" {
		t.Fatalf("workload trace resource missing blueprint attr: %+v", tc.Resources[0].Attrs)
	}
}

// TestSpanMetricsGatedPerBlueprintFromControl proves the runner sets World.EmitSpanMetrics
// per-tick from the control snapshot, keyed by blueprint name: default OFF (blueprint not in the
// opt-in list, incl. a nil snapshot) → false; opted in → true. The fakeWorkload records the World
// it was ticked with, so we assert the field the runner stamped right before the Tick.
func TestSpanMetricsGatedPerBlueprintFromControl(t *testing.T) {
	r, _, _, _, _, _, wls := newTestRunner(t)
	if err := r.AddBlueprint(buildTestResolved("alpha")); err != nil {
		t.Fatalf("AddBlueprint: %v", err)
	}

	// (a) default: no control applied (nil snapshot) → default OFF.
	if err := r.RunOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(*wls) != 1 {
		t.Fatalf("expected 1 workload, got %d", len(*wls))
	}
	if (*wls)[0].world.EmitSpanMetrics {
		t.Fatal("default (blueprint not opted in) must leave World.EmitSpanMetrics OFF")
	}

	// (b) opt the blueprint in → ON.
	r.ApplyControl(control.State{VolumeMultiplier: 1.0, SpanMetricsBlueprints: []string{"alpha"}})
	if err := r.RunOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !(*wls)[0].world.EmitSpanMetrics {
		t.Fatal("opted-in blueprint must have World.EmitSpanMetrics ON")
	}

	// (c) a control snapshot WITHOUT the blueprint → back OFF (opt-IN polarity, not sticky).
	r.ApplyControl(control.State{VolumeMultiplier: 1.0, SpanMetricsBlueprints: []string{"beta"}})
	if err := r.RunOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if (*wls)[0].world.EmitSpanMetrics {
		t.Fatal("blueprint not in the opt-in list must be OFF again")
	}
}

func TestDuplicateBlueprintRejected(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	if err := r.AddBlueprint(buildTestResolved("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := r.AddBlueprint(buildTestResolved("alpha")); err == nil || !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("duplicate blueprint not rejected: %v", err)
	}
}

func TestUnknownKindFailsAdd(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	res := buildTestResolved("alpha")
	res.Constructs[0].Kind = "ghost"
	if err := r.AddBlueprint(res); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("unknown kind not rejected: %v", err)
	}
}

// resolvedWithIncidentModel returns a Resolved carrying a target inventory + one scenario, so the
// runner's Live closure / ControlSchema can be exercised without the YAML loader. The kinds match
// the test registry (fake_substrate / fake_workload) so AddBlueprint builds successfully.
func resolvedWithIncidentModel(name string) *blueprint.Resolved {
	cl := &fixture.Cluster{Name: name + "-cluster", Env: &fixture.Env{Name: "prod", Weight: 1}}
	res := &blueprint.Resolved{
		Name:  name,
		Label: name,
		Constructs: []blueprint.ConstructInstance{
			{Kind: "fake_substrate", Name: "sub1", Config: &struct{}{}, Fixtures: &fixture.Set{Seed: name, Cluster: cl}},
		},
		Workloads: []blueprint.WorkloadInstance{
			{Kind: "fake_workload", Name: "api", Config: &struct{}{}, Replicas: 4, Env: cl.Env, Cluster: cl},
		},
		Targets: []blueprint.Target{
			{Name: "api", Axis: failuremode.AxisWorkload, Scalable: &blueprint.ScaleBounds{Dimension: "replicas", Default: 4, Min: 1, Max: 50}},
			{Name: "app-db", Axis: failuremode.AxisDatabase},
		},
		Scenarios: []blueprint.ResolvedScenario{
			{Name: "db_storm", Title: "DB storm", Summary: "connections saturate",
				Effects: []blueprint.EffectDecl{{Mode: "connection_saturation", Target: "database:*", Intensity: 0.6}}},
		},
	}
	return res
}

// TestLiveClosureExpandsScenariosAndWildcards proves the per-blueprint Live closure unions ad-hoc
// failures with active-scenario effects and expands "<axis>:*" scopes against the target inventory.
func TestLiveClosureExpandsScenariosAndWildcards(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	if err := r.AddBlueprint(resolvedWithIncidentModel("t")); err != nil {
		t.Fatalf("AddBlueprint: %v", err)
	}
	r.ApplyControl(control.State{
		VolumeMultiplier: 1.0,
		ActiveScenarios:  []string{"t/db_storm"},
		Failures:         map[string]control.FailureSetting{"latency_spike": {Enabled: true, Intensity: 0.4, Scope: "workload:*"}},
	})
	eng := r.bps[0].eng

	// Scenario effect "connection_saturation@database:*" → expands to the single database target.
	got := eng.Live("connection_saturation")
	if len(got) != 1 || !got[0].Enabled || got[0].Intensity != 0.6 || got[0].Scope != "app-db" {
		t.Fatalf("connection_saturation Live = %+v, want one {Enabled,0.6,app-db}", got)
	}
	// Ad-hoc failure with a "workload:*" wildcard scope → expands to the single workload target.
	got = eng.Live("latency_spike")
	if len(got) != 1 || got[0].Scope != "api" || got[0].Intensity != 0.4 {
		t.Fatalf("latency_spike Live = %+v, want one {0.4,api}", got)
	}
	// A mode no source names → no live failures.
	if g := eng.Live("oom_kill"); len(g) != 0 {
		t.Fatalf("oom_kill Live = %+v, want none", g)
	}
}

// TestLiveClosureInactiveScenarioIsSilent proves an un-activated scenario contributes nothing.
func TestLiveClosureInactiveScenarioIsSilent(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	_ = r.AddBlueprint(resolvedWithIncidentModel("t"))
	r.ApplyControl(control.State{VolumeMultiplier: 1.0}) // no active scenarios
	if g := r.bps[0].eng.Live("connection_saturation"); len(g) != 0 {
		t.Fatalf("inactive scenario must be silent, got %+v", g)
	}
}

// TestApplyControlScalingPropagates proves ApplyControl pushes the scaling map into each blueprint's
// scale.Source, where constructs/workloads read it via w.Scaling.
func TestApplyControlScalingPropagates(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	_ = r.AddBlueprint(resolvedWithIncidentModel("t"))
	// Scaling keys are qualified "blueprint/name"; ApplyControl strips the blueprint prefix so the
	// per-blueprint scale.Source sees only its own targets by bare name (constructs stay blueprint-unaware).
	r.ApplyControl(control.State{VolumeMultiplier: 1.0, Scaling: map[string]int{"t/api": 9, "other-bp/api": 1}})
	if got := r.bps[0].scale.Count("api", 2); got != 9 {
		t.Fatalf("scale override not propagated: Count(api,2)=%d want 9", got)
	}
	if got := r.bps[0].scale.Count("other", 3); got != 3 {
		t.Fatalf("unset key must fall back to default: got %d want 3", got)
	}
}

// TestControlSchemaShape proves ControlSchema reports blueprints, targets (with current scaling),
// and scenarios (with activation state).
func TestControlSchemaShape(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	_ = r.AddBlueprint(resolvedWithIncidentModel("t"))
	r.ApplyControl(control.State{VolumeMultiplier: 1.0, ActiveScenarios: []string{"t/db_storm"}, Scaling: map[string]int{"t/api": 9}})

	sc := r.ControlSchema()
	if !slices.Contains(sc.Blueprints, "t") {
		t.Fatalf("Blueprints missing t: %+v", sc.Blueprints)
	}
	byTarget := map[string]control.TargetInfo{}
	for _, ti := range sc.Targets {
		byTarget[ti.Name] = ti
	}
	api := byTarget["api"]
	if api.Blueprint != "t" {
		t.Fatalf("api target must carry owning blueprint t: %+v", api)
	}
	if api.Scalable == nil || api.Scalable.Dimension != "replicas" || api.Scalable.Default != 4 || api.Scalable.Current != 9 {
		t.Fatalf("api target wrong: %+v", api)
	}
	if db, ok := byTarget["app-db"]; !ok || db.Scalable != nil {
		t.Fatalf("app-db must be present and non-scalable: %+v", db)
	}
	if len(sc.Scenarios) != 1 || sc.Scenarios[0].Blueprint != "t" || sc.Scenarios[0].Name != "db_storm" || !sc.Scenarios[0].Active {
		t.Fatalf("scenario info wrong: %+v", sc.Scenarios)
	}
}

// TestControlSchemaCarriesBlueprintMetadata proves ControlSchema surfaces the blueprint- and
// env-level metadata (parallel to Blueprints) for the operator UI.
func TestControlSchemaCarriesBlueprintMetadata(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	res := resolvedWithIncidentModel("t")
	res.Metadata = blueprint.Metadata{
		Description: "demo", Owner: "team-a", Category: "reference",
		Tags: []string{"aws", "eks"}, Links: map[string]string{"runbook": "https://x/rb"},
	}
	res.Environments = []blueprint.ResolvedEnvMeta{
		{Name: "prod", Metadata: blueprint.Metadata{Description: "primary", Tags: []string{"eu"}}},
		{Name: "staging"},
	}
	_ = r.AddBlueprint(res)
	r.ApplyControl(control.State{VolumeMultiplier: 1.0})

	sc := r.ControlSchema()
	var bm *control.BlueprintMetaInfo
	for i := range sc.BlueprintMeta {
		if sc.BlueprintMeta[i].Name == "t" {
			bm = &sc.BlueprintMeta[i]
		}
	}
	if bm == nil {
		t.Fatalf("BlueprintMeta missing blueprint t: %+v", sc.BlueprintMeta)
	}
	if bm.Description != "demo" || bm.Owner != "team-a" || bm.Category != "reference" {
		t.Fatalf("blueprint meta fields wrong: %+v", bm)
	}
	if len(bm.Tags) != 2 || bm.Links["runbook"] != "https://x/rb" {
		t.Fatalf("blueprint tags/links wrong: %+v", bm)
	}
	if len(bm.Environments) != 2 || bm.Environments[0].Name != "prod" || bm.Environments[0].Description != "primary" {
		t.Fatalf("env meta wrong: %+v", bm.Environments)
	}
	if bm.Environments[1].Name != "staging" || bm.Environments[1].Description != "" {
		t.Fatalf("staging env should have empty metadata: %+v", bm.Environments[1])
	}
}

// TestControlSchemaEnumeratesConstructsAndKinds proves ControlSchema reports every construct
// instance (blueprint-qualified, Enabled true by default) and the de-duplicated kind list.
func TestControlSchemaEnumeratesConstructsAndKinds(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	_ = r.AddBlueprint(resolvedWithIncidentModel("t"))
	r.ApplyControl(control.State{VolumeMultiplier: 1.0})

	sc := r.ControlSchema()
	if len(sc.Constructs) == 0 {
		t.Fatalf("Constructs empty: %+v", sc.Constructs)
	}
	if len(sc.Kinds) == 0 {
		t.Fatalf("Kinds empty: %+v", sc.Kinds)
	}
	for _, c := range sc.Constructs {
		if !c.Enabled {
			t.Fatalf("construct %+v should be enabled by default", c)
		}
	}
	if !slices.Contains(sc.Kinds, "fake_substrate") {
		t.Fatalf("Kinds missing fake_substrate: %+v", sc.Kinds)
	}
}

// TestConstructEnabledGate proves per-instance and per-kind disable both flip constructEnabled
// (and the schema's Enabled flag).
func TestConstructEnabledGate(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	_ = r.AddBlueprint(resolvedWithIncidentModel("t"))

	// Default: enabled.
	r.ApplyControl(control.State{VolumeMultiplier: 1.0})
	if !r.constructEnabled("t", "fake_substrate", "sub1") {
		t.Fatalf("construct must be enabled by default")
	}

	// Per-instance disable.
	key := control.ConstructKey("t", "fake_substrate", "sub1")
	r.ApplyControl(control.State{VolumeMultiplier: 1.0, DisabledConstructs: []string{key}})
	if r.constructEnabled("t", "fake_substrate", "sub1") {
		t.Fatalf("per-instance disable must flip constructEnabled to false")
	}

	// Per-kind disable.
	r.ApplyControl(control.State{VolumeMultiplier: 1.0, DisabledKinds: []string{"fake_substrate"}})
	if r.constructEnabled("t", "fake_substrate", "sub1") {
		t.Fatalf("per-kind disable must flip constructEnabled to false")
	}
	sc := r.ControlSchema()
	for _, c := range sc.Constructs {
		if c.Kind == "fake_substrate" && c.Enabled {
			t.Fatalf("kind-disabled construct must report Enabled=false: %+v", c)
		}
	}
}

// TestBlueprintSource proves the runner serves a loaded blueprint's raw YAML source and reports
// ok=false for an unknown name.
func TestBlueprintSource(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	res := resolvedWithIncidentModel("t")
	res.Source = "name: t\n"
	_ = r.AddBlueprint(res)

	src, ok := r.BlueprintSource("t")
	if !ok || src != "name: t\n" {
		t.Fatalf("BlueprintSource(t) = %q,%v, want raw YAML,true", src, ok)
	}
	if _, ok := r.BlueprintSource("ghost"); ok {
		t.Fatalf("BlueprintSource(ghost) must report ok=false")
	}
}

// fleetRegistry returns a registry with the REAL fleet_management construct registered (plus the
// fakes) so AddBlueprint can build a fleet_management instance and the runner can derive its roster.
func fleetRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg := core.NewRegistry()
	reg.RegisterConstruct(fleetmgmt.Registration())
	return reg
}

// resolvedWithFleet builds a Resolved declaring ONE fleet_management construct with the given
// collectors_per_os and an optional cluster, bypassing YAML.
func resolvedWithFleet(name string, perOS map[string]int, withCluster bool) (*blueprint.Resolved, *fixture.Set) {
	fx := &fixture.Set{Seed: name}
	if withCluster {
		fx.Cluster = &fixture.Cluster{Name: name + "-cluster", Env: &fixture.Env{Name: "prod", Weight: 1}}
	}
	return &blueprint.Resolved{
		Name:  name,
		Label: name,
		Constructs: []blueprint.ConstructInstance{
			{Kind: fleetmgmt.Kind, Name: "fleet1", Config: &fleetmgmt.Config{CollectorsPerOS: perOS}, Fixtures: fx},
		},
	}, fx
}

// TestFleetRosterDerivedForFleetBlueprint proves AddBlueprint derives the FM-registration roster
// for a fleet_management construct and that it is byte-identical to fleetmgmt.Roster for the same
// seed/cluster/perOS — i.e. the runner registers exactly the identities the construct emits.
func TestFleetRosterDerivedForFleetBlueprint(t *testing.T) {
	mc := &coretest.MetricCapture{}
	r := New(Sinks{Metrics: mc}, fleetRegistry(t), Options{})
	perOS := map[string]int{"linux": 3, "windows": 1, "darwin": 2}
	res, fx := resolvedWithFleet("fleeta", perOS, true)
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}

	got := r.bps[0].fleetRoster
	if len(got) == 0 {
		t.Fatalf("expected a non-empty fleet roster")
	}
	// Parity with the package-level Roster for identical inputs (the de-duplicated source of truth).
	want := fleetmgmt.Roster(fx.Seed, fx.Cluster.Name, perOS)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roster mismatch:\n got=%+v\nwant=%+v", got, want)
	}
	// Cluster fixture present ⇒ each collector carries the cluster identity.
	for _, c := range got {
		if c.Cluster != fx.Cluster.Name {
			t.Fatalf("collector %s cluster=%q, want %q", c.ID, c.Cluster, fx.Cluster.Name)
		}
	}
}

// TestFleetRosterClusterOmittedWhenStandalone proves a standalone fleet (no cluster fixture)
// derives a roster with empty cluster identity (I13: absent ⇒ omitted).
func TestFleetRosterClusterOmittedWhenStandalone(t *testing.T) {
	mc := &coretest.MetricCapture{}
	r := New(Sinks{Metrics: mc}, fleetRegistry(t), Options{})
	perOS := map[string]int{"linux": 2}
	res, _ := resolvedWithFleet("fleetb", perOS, false)
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	got := r.bps[0].fleetRoster
	want := fleetmgmt.Roster("fleetb", "", perOS)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("standalone roster mismatch:\n got=%+v\nwant=%+v", got, want)
	}
	for _, c := range got {
		if c.Cluster != "" {
			t.Fatalf("standalone collector %s must omit cluster, got %q", c.ID, c.Cluster)
		}
	}
}

// TestNoFleetRosterForNonFleetBlueprint proves a blueprint with no fleet_management construct
// derives no roster (so Run starts no controller for it).
func TestNoFleetRosterForNonFleetBlueprint(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	if err := r.AddBlueprint(buildTestResolved("plain")); err != nil {
		t.Fatal(err)
	}
	if len(r.bps[0].fleetRoster) != 0 {
		t.Fatalf("non-fleet blueprint must have empty roster, got %+v", r.bps[0].fleetRoster)
	}
}

// TestRunLaunchesFleetControllerTiedToCtx proves Run launches a DryRun fleet controller for a
// fleet blueprint when Options.Fleet.FMURL is set, and that the controller is tied to Run's ctx:
// cancelling ctx makes Run (which waits on the controller goroutine) return promptly. DryRun keeps
// the FM client from making real HTTP calls.
func TestRunLaunchesFleetControllerTiedToCtx(t *testing.T) {
	mc := &coretest.MetricCapture{}
	r := New(Sinks{Metrics: mc}, fleetRegistry(t), Options{
		Fleet: fleet.Config{FMURL: "https://fm.example", StackID: "1", Token: "tok", DryRun: true},
	})
	res, _ := resolvedWithFleet("fleetc", map[string]int{"linux": 1}, false)
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	// Give the goroutines a moment to register, then cancel — Run must drain (wg.Wait) and return.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
		// Run returned after the controller goroutine cleanly shut down on ctx cancel.
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel — fleet controller not tied to ctx/waitgroup")
	}
}

func TestSeriesBudgetTruncatesPerBlueprint(t *testing.T) {
	subs, scs, wls := &[]*fakeConstruct{}, &[]*fakeConstruct{}, &[]*fakeWorkload{}
	mc := &coretest.MetricCapture{}
	r := New(Sinks{Metrics: mc}, testRegistry(subs, scs, wls), Options{})
	res := buildTestResolved("alpha")
	res.SeriesBudget = 1 // each fake construct writes 1 series per Tick → second write within a tick window would exceed
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	_ = r.RunOnce(context.Background(), time.Now())
	// 2 constructs × 1 series, budget 1 per blueprint per tick cycle → only 1 series lands.
	if got := len(mc.All()); got != 1 {
		t.Fatalf("series after budget: %d, want 1", got)
	}
}

// metricWriterFunc adapts a func to core.MetricWriter for stamping-writer tests.
type metricWriterFunc func(context.Context, []promrw.Series) error

func (f metricWriterFunc) Write(ctx context.Context, b []promrw.Series) error { return f(ctx, b) }

// TestStampedMetricsPreservesExemplars guards the blueprint-scoped clone branch: the
// stamping writer rebuilds the Series struct field-by-field, so Exemplars are silently
// dropped unless explicitly carried through. Exemplars survive (never aliased, no clone),
// and the blueprint selector label is stamped.
func TestStampedMetricsPreservesExemplars(t *testing.T) {
	var got []promrw.Series
	w := &stampedMetrics{
		sink:   metricWriterFunc(func(_ context.Context, b []promrw.Series) error { got = b; return nil }),
		label:  "bp-x",
		bp:     "bp-x",
		budget: newSeriesBudget(0), // unlimited
	}
	in := []promrw.Series{{
		Name: "m", Labels: map[string]string{"job": "api"}, Value: 1, T: time.UnixMilli(1),
		Exemplars: []promrw.Exemplar{{Labels: map[string]string{"trace_id": "t1"}, Value: 0.5, T: time.UnixMilli(1)}},
	}}
	if err := w.Write(context.Background(), in); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(got) != 1 || len(got[0].Exemplars) != 1 || got[0].Exemplars[0].Labels["trace_id"] != "t1" {
		t.Fatalf("exemplars dropped by stamping writer: %+v", got)
	}
	if got[0].Labels[BlueprintLabel] != "bp-x" {
		t.Fatalf("blueprint label not stamped: %v", got[0].Labels)
	}
}

// TestStampedMetricsPreservesNative guards the same field-by-field clone branch for the
// native-histogram field: a Series carrying Native must survive the stamping writer with
// Native intact (not silently zeroed into a float sample). Sibling to the Exemplars guard.
func TestStampedMetricsPreservesNative(t *testing.T) {
	var got []promrw.Series
	w := &stampedMetrics{
		sink:   metricWriterFunc(func(_ context.Context, b []promrw.Series) error { got = b; return nil }),
		label:  "bp-x",
		bp:     "bp-x",
		budget: newSeriesBudget(0), // unlimited
	}
	in := []promrw.Series{{
		Name: "traces_spanmetrics_latency", Labels: map[string]string{"service": "checkout"},
		T: time.UnixMilli(1), Kind: promrw.KindHistogram,
		Native: &promrw.NativeHistogram{Schema: 3, Count: 7, Sum: 1.5, ZeroCount: 1},
	}}
	if err := w.Write(context.Background(), in); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 series, got %d", len(got))
	}
	if got[0].Native == nil {
		t.Fatalf("Native dropped by stamping writer: %+v", got[0])
	}
	if got[0].Native.Count != 7 || got[0].Native.Schema != 3 {
		t.Fatalf("Native corrupted: count=%d schema=%d", got[0].Native.Count, got[0].Native.Schema)
	}
	if got[0].Labels[BlueprintLabel] != "bp-x" {
		t.Fatalf("blueprint label not stamped: %v", got[0].Labels)
	}
}
