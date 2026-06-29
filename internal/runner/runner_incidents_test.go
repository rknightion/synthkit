// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// incTestRegistry builds a registry that declares latency_spike + error_burst on AxisWorkload,
// plus a minimal substrate construct so AddBlueprint succeeds.
func incTestRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg := core.NewRegistry()
	reg.RegisterConstruct(core.ConstructReg{
		Kind: "inc_fake_substrate", Doc: "t", Scope: core.ScopeSubstrate,
		FailureModes: []failuremode.Mode{
			{Name: "latency_spike", Axis: failuremode.AxisWorkload, Help: "spike latency"},
			{Name: "error_burst", Axis: failuremode.AxisWorkload, Help: "burst errors"},
		},
		NewConfig: func() any { return &struct{}{} },
		Build: func(cfg any, fx *fixture.Set) (core.Construct, error) {
			return &fakeConstruct{kind: "inc_fake_substrate"}, nil
		},
	})
	return reg
}

// newTestRunnerWithBlueprint builds a Runner with one blueprint named bp and one target
// per element of targets (all AxisWorkload). The blueprint has no declared incidents.
func newTestRunnerWithBlueprint(t *testing.T, bp string, targets []string) *Runner {
	t.Helper()
	reg := incTestRegistry(t)
	r := New(Sinks{}, reg, Options{})

	bpTargets := make([]blueprint.Target, len(targets))
	for i, name := range targets {
		bpTargets[i] = blueprint.Target{Name: name, Axis: failuremode.AxisWorkload}
	}

	cl := &fixture.Cluster{Name: bp + "-cluster", Env: &fixture.Env{Name: "prod", Weight: 1}}
	res := &blueprint.Resolved{
		Name:     bp,
		Label:    bp,
		Timezone: "UTC",
		Targets:  bpTargets,
		Constructs: []blueprint.ConstructInstance{
			{Kind: "inc_fake_substrate", Name: "sub1", Config: &struct{}{}, Fixtures: &fixture.Set{Seed: bp, Cluster: cl}},
		},
	}
	if err := r.AddBlueprint(res); err != nil {
		t.Fatalf("AddBlueprint: %v", err)
	}
	// Overwrite bpRuntime.targets so the production code (ValidateRuntimeIncident, ControlIncidents)
	// can see them — AddBlueprint only populates bp.targets from res.Targets (already set above).
	return r
}

// newTestRunnerWithDeclaredIncident builds a Runner where blueprint bp has spec as a declared
// incident (in both the shape engine and bp.incidents), plus the given targets.
func newTestRunnerWithDeclaredIncident(t *testing.T, bp, spec string, targets []string) *Runner {
	t.Helper()
	// Use a distinct kind name per call to avoid double-registration conflicts.
	kindName := "inc_fake_decl_" + bp
	reg := core.NewRegistry()
	reg.RegisterConstruct(core.ConstructReg{
		Kind: kindName, Doc: "t", Scope: core.ScopeSubstrate,
		FailureModes: []failuremode.Mode{
			{Name: "latency_spike", Axis: failuremode.AxisWorkload, Help: "spike latency"},
			{Name: "error_burst", Axis: failuremode.AxisWorkload, Help: "burst errors"},
		},
		NewConfig: func() any { return &struct{}{} },
		Build: func(cfg any, fx *fixture.Set) (core.Construct, error) {
			return &fakeConstruct{kind: kindName}, nil
		},
	})
	r := New(Sinks{}, reg, Options{})

	bpTargets := make([]blueprint.Target, len(targets))
	for i, name := range targets {
		bpTargets[i] = blueprint.Target{Name: name, Axis: failuremode.AxisWorkload}
	}

	cl := &fixture.Cluster{Name: bp + "-cluster", Env: &fixture.Env{Name: "prod", Weight: 1}}
	res := &blueprint.Resolved{
		Name:      bp,
		Label:     bp,
		Timezone:  "UTC",
		Targets:   bpTargets,
		Incidents: []string{spec},
		Constructs: []blueprint.ConstructInstance{
			{Kind: kindName, Name: "sub1", Config: &struct{}{}, Fixtures: &fixture.Set{Seed: bp, Cluster: cl}},
		},
	}
	if err := r.AddBlueprint(res); err != nil {
		t.Fatalf("AddBlueprint: %v", err)
	}
	return r
}

// ---- Task 4 test ----

func TestRuntimeSpecsFor(t *testing.T) {
	all := []control.RuntimeIncident{
		{ID: "a", Blueprint: "starter", Mode: "latency_spike", Target: "starter-api", At: "15:04", For: "30m", Intensity: 0.8},
		{ID: "b", Blueprint: "initech", Mode: "error_burst", Target: "", At: "everyt", For: "2m", Intensity: 1},
		{ID: "c", Blueprint: "starter", Mode: "error_burst", Target: "prod", At: "2026-06-22T15:00", For: "45m", Intensity: 0.5},
	}
	got := runtimeSpecsFor("starter", all)
	want := []string{
		"latency_spike@15:04/30m#0.8@starter-api",
		"error_burst@2026-06-22T15:00/45m#0.5@prod",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d specs, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("spec[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// ---- Task 5 test ----

func TestLiveClosureRuntimeIncident(t *testing.T) {
	r := newTestRunnerWithBlueprint(t, "starter", []string{"starter-api"})
	r.ApplyControl(control.State{
		VolumeMultiplier: 1,
		RuntimeIncidents: []control.RuntimeIncident{
			{ID: "rt-1", Blueprint: "starter", Mode: "latency_spike", Target: "starter-api", At: "00:00", For: "23h59m", Intensity: 0.6},
		},
	})
	bp := r.bps[0]
	got := bp.eng.Live("latency_spike")
	found := false
	for _, lf := range got {
		if lf.Enabled && lf.Scope == "starter-api" && lf.Intensity == 0.6 {
			found = true
		}
	}
	if !found {
		t.Fatalf("Live closure should surface the active runtime incident; got %+v", got)
	}
	// A mode with no runtime incident yields nothing extra from this source.
	if lf := bp.eng.Live("error_burst"); len(lf) != 0 {
		t.Fatalf("unrelated mode should have no live failures, got %+v", lf)
	}
}

// ---- Task 6 tests ----

func TestRunnerImplementsIncidentSource(t *testing.T) {
	var _ control.IncidentSource = (*Runner)(nil)
}

func TestControlIncidentsDeclared(t *testing.T) {
	r := newTestRunnerWithDeclaredIncident(t, "starter", "latency_spike@00:00/23h59m#0.7@starter-api", []string{"starter-api"})
	infos := r.ControlIncidents()
	if len(infos) != 1 {
		t.Fatalf("want 1 declared incident, got %d: %+v", len(infos), infos)
	}
	got := infos[0]
	if got.Source != "declared" || got.Blueprint != "starter" || got.Mode != "latency_spike" || got.Target != "starter-api" {
		t.Fatalf("declared incident fields wrong: %+v", got)
	}
	if got.ScheduleSpec != "latency_spike@00:00/23h59m#0.7@starter-api" {
		t.Fatalf("schedule_spec = %q", got.ScheduleSpec)
	}
	if !got.ActiveNow {
		t.Fatal("a daily window covering the whole day should be active_now")
	}
}

func TestValidateRuntimeIncident(t *testing.T) {
	r := newTestRunnerWithBlueprint(t, "starter", []string{"starter-api"})
	ok := control.RuntimeIncident{Blueprint: "starter", Mode: "latency_spike", Target: "starter-api", At: "15:04", For: "30m", Intensity: 0.5}
	if err := r.ValidateRuntimeIncident(ok); err != nil {
		t.Fatalf("valid incident rejected: %v", err)
	}
	bad := []control.RuntimeIncident{
		{Blueprint: "nope", Mode: "latency_spike", Target: "starter-api", At: "15:04", For: "30m"},
		{Blueprint: "starter", Mode: "no_such_mode", Target: "starter-api", At: "15:04", For: "30m"},
		{Blueprint: "starter", Mode: "latency_spike", Target: "ghost", At: "15:04", For: "30m"},
		{Blueprint: "starter", Mode: "latency_spike", Target: "starter-api", At: "nonsense", For: "30m"},
		{Blueprint: "starter", Mode: "latency_spike", Target: "starter-api", At: "15:04", For: "xyz"},
	}
	for i, b := range bad {
		if err := r.ValidateRuntimeIncident(b); err == nil {
			t.Errorf("bad incident #%d should be rejected: %+v", i, b)
		}
	}
}

// TestControlIncidentsIsolation verifies that a declared incident whose schedule window is CLOSED
// reports active_now=false even when an ad-hoc failure of the same mode+scope is enabled via
// control.State. This guards against the bug where bp.eng.Active() unions the Live closure (which
// includes ad-hoc failures) and falsely reports the declared window as active.
func TestControlIncidentsIsolation(t *testing.T) {
	// Use a past absolute window so it is definitively closed.
	closedSpec := "latency_spike@2020-01-01T02:00/1h#0.7@starter-api"
	r := newTestRunnerWithDeclaredIncident(t, "starter", closedSpec, []string{"starter-api"})

	// Enable an ad-hoc failure for the same mode+scope so bp.eng.Live would return it.
	r.ApplyControl(control.State{
		VolumeMultiplier: 1,
		Failures: map[string]control.FailureSetting{
			"latency_spike": {Enabled: true, Intensity: 1.0, Scope: "starter-api"},
		},
	})

	infos := r.ControlIncidents()
	if len(infos) != 1 {
		t.Fatalf("want 1 declared incident, got %d: %+v", len(infos), infos)
	}
	if infos[0].ActiveNow {
		t.Errorf("declared incident with closed window should report active_now=false even when ad-hoc failure is enabled for the same mode+scope")
	}
}

// TestRuntimeIncidentActiveNowIsolation verifies that a runtime incident whose window is CLOSED
// reports active_now=false even when another runtime incident of the same mode+scope is active.
// This guards against the bug where rt.Active() checked all windows in rtEng, not just this one.
func TestRuntimeIncidentActiveNowIsolation(t *testing.T) {
	r := newTestRunnerWithBlueprint(t, "starter", []string{"starter-api"})

	// Two runtime incidents: one closed (past absolute), one open (full-day daily).
	r.ApplyControl(control.State{
		VolumeMultiplier: 1,
		RuntimeIncidents: []control.RuntimeIncident{
			{ID: "closed", Blueprint: "starter", Mode: "latency_spike", Target: "starter-api", At: "2020-01-01T02:00", For: "1h", Intensity: 0.5},
			{ID: "open", Blueprint: "starter", Mode: "latency_spike", Target: "starter-api", At: "00:00", For: "23h59m", Intensity: 0.8},
		},
	})

	infos := r.ControlIncidents()
	if len(infos) != 2 {
		t.Fatalf("want 2 runtime incidents, got %d: %+v", len(infos), infos)
	}
	// Find each by ID.
	var closedInfo, openInfo *control.IncidentInfo
	for i := range infos {
		switch infos[i].ID {
		case "closed":
			closedInfo = &infos[i]
		case "open":
			openInfo = &infos[i]
		}
	}
	if closedInfo == nil || openInfo == nil {
		t.Fatalf("could not find expected incident IDs in %+v", infos)
	}
	if closedInfo.ActiveNow {
		t.Errorf("closed runtime incident should report active_now=false, got true")
	}
	if !openInfo.ActiveNow {
		t.Errorf("open runtime incident should report active_now=true, got false")
	}
}

// Prevent unused import warnings during incremental implementation.
var _ = time.Now
var _ = shape.New
