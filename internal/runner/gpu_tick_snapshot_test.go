// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core"
)

// Catches a control update changing another consumer's answer in the same master
// tick, including a cycle whose ledger is empty. All entrypoints must prepare it.
func TestTickSnapshotControlConsistency(t *testing.T) {
	for _, entry := range []string{"master", "once", "live"} {
		t.Run(entry, func(t *testing.T) {
			r := newTestRunnerWithBlueprint(t, "starter", []string{"starter-api"})
			bp := r.bps[0]
			now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
			st := control.DefaultState()
			st.SetFailure("latency_spike", control.FailureSetting{Enabled: true, Intensity: 0.4, Scope: "starter-api"})
			r.ApplyControl(st)
			advance := func(at time.Time) {
				t.Helper()
				var err error
				switch entry {
				case "master":
					err = r.MasterTick(context.Background(), at)
				case "once":
					err = r.RunOnce(context.Background(), at)
				case "live":
					err = r.masterTickOne(context.Background(), bp, at)
					r.tickBlueprintInstances(context.Background(), bp, at)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			advance(now)
			if bp.ledger.Len() != 0 {
				t.Fatal("test requires empty ledger")
			}
			firstActive, firstIntensity := bp.eng.Eval(now, "latency_spike", "starter-api")
			if !firstActive || firstIntensity != 0.4 {
				t.Fatalf("initial consumer: %v %g", firstActive, firstIntensity)
			}
			next := control.DefaultState()
			r.ApplyControl(next)
			secondActive, secondIntensity := bp.eng.Eval(now, "latency_spike", "starter-api")
			if secondActive != firstActive || secondIntensity != firstIntensity {
				t.Fatal("same-tick consumers saw different control revisions")
			}
			advance(now.Add(5 * time.Second))
			if active, _ := bp.eng.Eval(now.Add(5*time.Second), "latency_spike", "starter-api"); active {
				t.Fatal("next tick did not see control update")
			}
		})
	}
}

// Catches runtime windows being evaluated at per-call wall clock instead of the
// master timestamp, and ensures the following tick observes the window ending.
func TestTickSnapshotRuntimeUsesTickTime(t *testing.T) {
	r := newTestRunnerWithBlueprint(t, "starter", []string{"starter-api"})
	now := time.Date(2026, 9, 27, 12, 0, 5, 0, time.UTC)
	st := control.DefaultState()
	st.RuntimeIncidents = []control.RuntimeIncident{{ID: "rt-fixed", Blueprint: "starter", Mode: "latency_spike", Target: "starter-api", At: now.Add(-5 * time.Second).Format(time.RFC3339), For: "10s", Intensity: 0.6}}
	r.ApplyControl(st)
	if err := r.MasterTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	bp := r.bps[0]
	for _, callTime := range []time.Time{now, now.Add(time.Minute)} {
		if active, intensity := bp.eng.Eval(callTime, "latency_spike", "starter-api"); !active || intensity != 0.6 {
			t.Fatalf("same tick lost runtime window: %v %g", active, intensity)
		}
	}
	if err := r.MasterTick(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if active, _ := bp.eng.Eval(now.Add(time.Minute), "latency_spike", "starter-api"); active {
		t.Fatal("next tick retained expired runtime window")
	}
}

// snapshotConsumer evaluates through its real Tick World, not a post-cycle query.
type snapshotConsumer struct {
	fakeConstruct
	active []bool
}

func (c *snapshotConsumer) Tick(ctx context.Context, now time.Time, world *core.World) error {
	active, _ := world.Shape.Eval(now, "latency_spike", "starter-api")
	c.active = append(c.active, active)
	return c.fakeConstruct.Tick(ctx, now, world)
}

// Catches a previously disabled blueprint being admitted only to the metric
// stage, where it would read a stale prior-cycle snapshot.
func TestRunOnceSnapshotEligibilityInterleaving(t *testing.T) {
	r := newTestRunnerWithBlueprint(t, "starter", []string{"starter-api"})
	probe := &snapshotConsumer{fakeConstruct: fakeConstruct{kind: "inc_fake_substrate"}}
	r.bps[0].constructs[0].construct = probe
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	initial := control.DefaultState()
	initial.SetFailure("latency_spike", control.FailureSetting{Enabled: true, Intensity: 0.4, Scope: "starter-api"})
	r.ApplyControl(initial)
	if err := r.RunOnce(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(probe.active) != 1 || !probe.active[0] {
		t.Fatal("prior-cycle failure missing")
	}
	r.reg.RegisterWorkload(withWorkloadMetricProducers(core.WorkloadReg{
		Kind: "fake_workload", Scope: core.ScopeBlueprint, NewConfig: func() any { return &struct{}{} },
		Build: func(_ any, b core.Binding) (core.Workload, error) { return &fakeWorkload{name: b.Name}, nil },
	}, producerPromRW, producerOTLPNative))
	if err := r.AddBlueprint(&blueprint.Resolved{Name: "other", Label: "other", Timezone: "UTC", Workloads: []blueprint.WorkloadInstance{{Kind: "fake_workload", Name: "other-job", Config: &struct{}{}}}}); err != nil {
		t.Fatal(err)
	}
	disabled := control.DefaultState()
	disabled.DisabledBlueprints = []string{"starter"}
	r.ApplyControl(disabled)
	updated := false
	r.SetTickObserver(func(ctx context.Context, bp, kind, name string, fn func(context.Context) error) error {
		if bp == "other" && kind == "fake_workload" && !updated {
			updated = true
			r.ApplyControl(control.DefaultState())
		}
		return fn(ctx)
	})
	if err := r.RunOnce(context.Background(), now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("interleaving did not execute")
	}
	if len(probe.active) != 1 {
		t.Fatalf("skipped blueprint admitted to metric-only stage with stale state: %v", probe.active)
	}
	if err := r.RunOnce(context.Background(), now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(probe.active) != 2 || probe.active[1] {
		t.Fatalf("next eligible cycle did not prepare updated control: %v", probe.active)
	}
}
