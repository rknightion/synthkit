// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"os"
	"reflect"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func faultProfileSelection(t *testing.T, worst float64) *GPUSelection {
	t.Helper()
	data, err := os.ReadFile("../../e2e/fixtures/ai-factory-hgx.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		GPUCompute GPUTopologySpec `yaml:"gpu_compute"`
	}
	if err := yaml.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	for i := range d.GPUCompute.Pools {
		d.GPUCompute.Pools[i].Operating.AmbientTempC = 25
		d.GPUCompute.Pools[i].Operating.FullPowerRiseC = 40
		d.GPUCompute.Pools[i].Operating.CoolingFaultRiseC = worst - 65
	}
	cl := &Cluster{Name: "compute-cluster", Type: "baremetal", Seed: "fault-profile"}
	top, err := BuildGPUTopology("fault-profile", d.GPUCompute, map[string]*Cluster{cl.Name: cl}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var pools []string
	for _, p := range top.Pools {
		pools = append(pools, p.Name)
	}
	s, err := SelectGPUTopology(top, pools, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestConstraintDemandUsesSharedAllocationAndTarget(t *testing.T) {
	s := faultProfileSelection(t, 90)
	for _, node := range s.Nodes() {
		node.Operating.Constraints = &GPUConstraintOperatingSpec{
			DriverVersion: "595.91.07", RequestedSMClockMHz: 1980, RequestedMemoryClockMHz: 2619,
			FullDemandPowerW: 700, PowerFaultDemandW: 1400,
			PowerClockCap: &GPUClocks{SM: 1200, Memory: 2000},
		}
	}
	var a GPUSnapshot
	found := false
	for minute := 0; minute < 360; minute++ {
		a = GPUAllocationPlan(s.Topology, time.Unix(int64(minute)*60, 0), nil)
		if len(a.Allocations) > 0 && a.Allocations[0].State == "held" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("fixture has no held allocation in its full cycle")
	}
	before, err := GPUOperatingPoints(s.Topology, a)
	if err != nil {
		t.Fatal(err)
	}
	target := a.Allocations[0].GPUKey
	baseline, err := GPUConstraintDemands(s.Topology, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	basePoints, err := GPUConstraintPoints(s.Topology, a, baseline)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range basePoints.GPUs {
		if p.PowerActive {
			t.Fatal("normal demand activated limiter")
		}
	}
	eval := func(_ time.Time, mode, key string) (bool, float64) {
		return mode == GPUPowerConstraintMode && key == target, 1
	}
	demands, err := GPUConstraintDemands(s.Topology, a, eval)
	if err != nil {
		t.Fatal(err)
	}
	points, err := GPUConstraintPoints(s.Topology, a, demands)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range points.GPUs {
		if p.PowerActive != (p.GPUKey == target) {
			t.Fatalf("shared power incident target leaked or lost: %+v", p)
		}
	}
	after, err := GPUOperatingPoints(s.Topology, a)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("new demand projection changed legacy physical output")
	}
	if !s.CoversConstraintTarget(GPUPowerConstraintMode, target) || s.CoversConstraintTarget(GPUPowerConstraintMode, "missing") {
		t.Fatal("constraint target admission mismatch")
	}
}

func TestFaultProfileSeparatesOperatingAndShutdownBounds(t *testing.T) {
	s := faultProfileSelection(t, 90)
	if err := RequireGPUCapabilities(s, GPUCapabilities{ThermalEnvelope: true}); err == nil {
		t.Fatal("legacy operating-envelope check must still reject 90 C")
	}
	if err := RequireGPUFaultProfile(s, "595.91.07"); err != nil {
		t.Fatalf("captured fault profile must allow a 90 C scenario below shutdown: %v", err)
	}
	for _, worst := range []float64{95, 96} {
		if err := RequireGPUFaultProfile(faultProfileSelection(t, worst), "595.91.07"); err == nil {
			t.Fatalf("accepted shutdown boundary %.0f C", worst)
		}
	}
	if err := RequireGPUFaultProfile(s, "unknown"); err == nil {
		t.Fatal("unknown driver admitted")
	}
}
