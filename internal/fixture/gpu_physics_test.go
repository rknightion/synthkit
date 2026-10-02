// SPDX-License-Identifier: AGPL-3.0-only

package fixture_test

import (
	"encoding/json"
	"github.com/rknightion/synthkit/internal/fixture"
	"math"
	"testing"
	"time"
)

func gpuNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-want) > 1e-8*math.Max(1, math.Abs(want)) {
		t.Fatalf("got %g want %g", got, want)
	}
}
func TestGPUPhysicsOriginConstantDemandAndAggregates(t *testing.T) {
	spec := gpuSpec(t)
	for i := range spec.Schedulers[0].Workloads {
		w := &spec.Schedulers[0].Workloads[i]
		if w.Name == "training-a" {
			w.Role = "inference"
			w.Demand.UtilizationBase = 0.5
			w.Demand.UtilizationAmplitude = 0
			w.Demand.Period = ""
			w.Demand.RequestsPerSecondAtFullUtilization = 10
		}
	}
	watts := 100.0
	spec.Racks[1].FacilityDevices = append(spec.Racks[1].FacilityDevices, fixture.GPUDeviceSpec{Name: "test-pdu", Kind: "pdu", StaticPowerW: &watts})
	top := gpuBuild(t, "seed-a", spec)
	at := gpuRunning(t, top)
	a := fixture.GPUAllocationPlan(top, at, nil)
	other := gpuBuild(t, "seed-b", spec)
	if _, err := fixture.GPUOperatingPoints(other, a); err == nil {
		t.Fatal("same keys from different topology accepted")
	}
	same := gpuBuild(t, "seed-a", spec)
	if _, err := fixture.GPUOperatingPoints(same, a); err == nil {
		t.Fatal("identical reconstructed topology gained snapshot provenance")
	}
	var decoded fixture.GPUSnapshot
	if err := json.Unmarshal(gpuJSON(t, a), &decoded); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.GPUOperatingPoints(top, decoded); err == nil {
		t.Fatal("serialized/fabricated snapshot gained origin")
	}
	bad := a
	bad.Allocations = append([]fixture.GPUAllocation(nil), a.Allocations...)
	bad.Allocations[0].GPUKey = "gpu:absent/0"
	if _, err := fixture.GPUOperatingPoints(top, bad); err == nil {
		t.Fatal("unknown allocation key accepted")
	}
	node, _ := top.Node("node:nvl-node-0000")
	port := ""
	for _, nic := range node.NICs {
		if nic.Name == "eth1" {
			port = nic.PortKey
		}
	}
	a = fixture.GPUAllocationPlan(top, at, gpuFault("gpu_backend_congestion", port, 1))
	p, err := fixture.GPUOperatingPoints(top, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range p.Workloads {
		if w.WorkloadKey == "gpuworkload:scheduler-a/training-a" {
			gpuNear(t, w.DemandUtilization, 0.5)
			gpuNear(t, w.EffectiveUtilization, 0.35)
			gpuNear(t, w.RequestsPerSecond, 28)
			want := uint64(math.Floor(186e9*0.65)) * 8
			if w.MemoryUsedBytes != want {
				t.Fatalf("workload memory got %d want %d", w.MemoryUsedBytes, want)
			}
		}
	}
	for _, n := range p.Nodes {
		gn, _ := top.Node(n.NodeKey)
		sum, load := 0.0, 0.0
		for _, g := range p.GPUs {
			physical, _ := top.GPU(g.GPUKey)
			if physical.NodeKey == n.NodeKey {
				sum += g.PowerW
				load = math.Max(load, g.Utilization)
			}
		}
		gpuNear(t, n.GPUWatts, sum)
		gpuNear(t, n.PowerW, sum+250+2*float64(fixture.LookupNodeSpec(*gn.Node).VCPU)*load+10*float64(len(gn.NICs))*load)
	}
	for _, r := range p.Racks {
		sum := 0.0
		for _, n := range p.Nodes {
			gn, _ := top.Node(n.NodeKey)
			if gn.RackKey == r.RackKey {
				sum += n.PowerW
			}
		}
		if r.RackKey == "rack:site-a/nvl-a" {
			gpuNear(t, r.PowerW, sum+4000+100)
			gpuNear(t, r.LiquidHeatW, r.PowerW*0.9)
			gpuNear(t, *r.ReturnTempC, *r.SupplyTempC+r.LiquidHeatW/(180.0/60*4180))
		} else {
			gpuNear(t, r.PowerW, sum+200)
			if r.FlowLPM != nil || r.SupplyTempC != nil || r.ReturnTempC != nil || r.LiquidHeatW != 0 {
				t.Fatal("air rack got liquid quantities")
			}
		}
	}
}
func TestGPUPhysicsIdleUnavailableCoolingAndCollectionIsolation(t *testing.T) {
	top := gpuBuild(t, "seed-a", gpuSpec(t))
	at := gpuRunning(t, top)
	base := fixture.GPUAllocationPlan(top, at, nil)
	normal, err := fixture.GPUOperatingPoints(top, base)
	if err != nil {
		t.Fatal(err)
	}
	eval := func(_ time.Time, m, k string) (bool, float64) {
		return m == "gpu_cooling_fault" && k == "rack:site-a/nvl-a" || m == "gpu_fallen_off_bus" && k == "gpu:nvl-node-0000/0", 1
	}
	a := fixture.GPUAllocationPlan(top, at, eval)
	p, err := fixture.GPUOperatingPoints(top, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range p.GPUs {
		if g.GPUKey == "gpu:nvl-node-0000/0" || g.GPUKey == "gpu:nvl-node-0000/1" {
			gpuNear(t, g.Utilization, 0)
			gpuNear(t, g.PowerW, 1200*0.12)
			gpuNear(t, g.CoolingDerate, 0.7)
			gpuNear(t, g.TemperatureC, 25+40*0.12+15)
			if g.VendorThermalEnvelopeKnown {
				t.Fatal("synthetic temperature promoted to vendor envelope")
			}
		}
	}
	for _, w := range p.Workloads {
		if w.WorkloadKey == "gpuworkload:scheduler-a/training-a" && (w.DemandUtilization != 0 || w.EffectiveUtilization != 0 || w.RequestsPerSecond != 0 || w.MemoryUsedBytes != 0) {
			t.Fatal("released workload output nonzero")
		}
	}
	local := fixture.GPUAllocationPlan(top, at, gpuFault("test_collection_outage", "device:absent", 1))
	if len(local.Faults) != 0 {
		t.Fatal("local mode entered shared faults")
	}
	localPhysics, err := fixture.GPUOperatingPoints(top, local)
	if err != nil {
		t.Fatal(err)
	}
	if string(gpuJSON(t, normal)) != string(gpuJSON(t, localPhysics)) {
		t.Fatal("collection failure altered physical points")
	}
}
