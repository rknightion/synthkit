// SPDX-License-Identifier: AGPL-3.0-only

package fixture_test

import (
	"bytes"
	"github.com/rknightion/synthkit/internal/fixture"
	"testing"
	"time"
)

func gpuRunning(t *testing.T, top *fixture.GPUTopology) time.Time {
	t.Helper()
	start := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 24*60; i++ {
		at := start.Add(time.Duration(i) * time.Minute)
		a := fixture.GPUAllocationPlan(top, at, nil)
		running := 0
		for _, w := range a.Workloads {
			if w.Phase == "running" {
				running++
			}
		}
		if running == 2 {
			return at
		}
	}
	t.Fatal("no overlapping running point in one day")
	return start
}
func gpuFault(mode, key string, intensity float64) fixture.GPUFailureEval {
	return func(_ time.Time, m, k string) (bool, float64) { return m == mode && k == key, intensity }
}
func TestGPUAllocationLifecycleFaultBranches(t *testing.T) {
	top := gpuBuild(t, "seed-a", gpuSpec(t))
	at := gpuRunning(t, top)
	base := fixture.GPUAllocationPlan(top, at, nil)
	other := gpuBuild(t, "seed-a", gpuSpec(t))
	if !bytes.Equal(gpuJSON(t, base), gpuJSON(t, fixture.GPUAllocationPlan(other, at, nil))) {
		t.Fatal("independent allocation plans differ")
	}
	if fixture.GPUAllocationPlan(top, time.Unix(-1, 0), nil).Bucket != -1 {
		t.Fatal("negative bucket truncated instead of floored")
	}
	tests := []struct{ name, mode, target, phase string }{{"fatal", "gpu_fallen_off_bus", "gpu:nvl-node-0000/2", "failed"}, {"preemption", "gpu_preemption_storm", "gpuworkload:scheduler-a/training-a", "preempted"}, {"quota", "gpu_quota_exhaustion", "project:scheduler-a/research", "pending"}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := fixture.GPUAllocationPlan(top, at, gpuFault(tc.mode, tc.target, 1))
			for _, w := range a.Workloads {
				if w.WorkloadKey == "gpuworkload:scheduler-a/training-a" {
					if w.Phase != tc.phase || len(w.GPUKeys) != 0 {
						t.Fatalf("unexpected victim phase/holdings: %+v", w)
					}
					if tc.phase == "failed" && (!w.RetryPending || len(w.VictimGPUKeys) != 8 || len(w.CauseTargets) != 1) {
						t.Fatal("baseline victim lineage lost")
					}
				}
			}
			for _, x := range a.Allocations {
				if x.WorkloadKey == "gpuworkload:scheduler-a/training-a" || x.GPUKey == "gpu:nvl-node-0000/0" && x.Pod != nil {
					t.Fatal("released job kept ownership/pod attribution")
				}
				if tc.name == "fatal" && x.GPUKey == tc.target && x.State != "unavailable" {
					t.Fatal("fatal GPU not unavailable")
				}
			}
			p, err := fixture.GPUOperatingPoints(top, a)
			if err != nil {
				t.Fatal(err)
			}
			for _, g := range p.GPUs {
				if g.GPUKey == "gpu:nvl-node-0000/0" && (g.MemoryUsedBytes != 0 || g.Utilization != 0 || g.PowerW <= 0) {
					t.Fatal("release did not retain idle watts while removing memory/load")
				}
			}
			if !bytes.Equal(gpuJSON(t, base), gpuJSON(t, fixture.GPUAllocationPlan(top, at, gpuFault(tc.mode, tc.target, 0)))) {
				t.Fatal("zero intensity changed baseline")
			}
		})
	}
	xid := fixture.GPUAllocationPlan(top, at, gpuFault("gpu_xid_burst", "gpu:nvl-node-0000/2", 1))
	for _, w := range xid.Workloads {
		if w.Phase != "running" {
			t.Fatal("Xid-only event made allocation fatal")
		}
	}
	calls := map[string]int{}
	fixture.GPUFaults(top, at, func(_ time.Time, m, k string) (bool, float64) { calls[m+"/"+k]++; return false, 0 })
	for pair, n := range calls {
		if n != 1 {
			t.Fatalf("pair %s evaluated %d times", pair, n)
		}
	}
}
func TestGPUClusterViewImmutablePlacement(t *testing.T) {
	cl := &fixture.Cluster{Name: "compute-user", Type: "baremetal", Seed: "seed-a"}
	top, err := fixture.BuildGPUTopology("seed-a", gpuSpec(t), map[string]*fixture.Cluster{cl.Name: cl}, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := gpuJSON(t, cl.SubstrateWorkloads)
	view := fixture.GPUClusterView(cl, fixture.GPUAllocationPlan(top, gpuRunning(t, top), nil))
	for i := range view.SubstrateWorkloads {
		w := &view.SubstrateWorkloads[i]
		if w.GPUWorkerKey != "" {
			if w.GPUPhase != "running" || len(w.NodeIdx) != 1 || len(w.PodNames) != 1 {
				t.Fatal("worker placement/phase absent")
			}
			w.NodeIdx[0] = -1
			w.PodNames[0] = "changed"
		}
	}
	if !bytes.Equal(before, gpuJSON(t, cl.SubstrateWorkloads)) {
		t.Fatal("view mutated shared templates")
	}
	if len(fixture.LiveNodes(cl, func(string, int) int { return 1000 })) != 20 {
		t.Fatal("GPU/static nodes scaled with replicas")
	}
}
