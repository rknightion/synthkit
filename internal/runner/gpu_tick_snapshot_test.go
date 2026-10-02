// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/control"
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
