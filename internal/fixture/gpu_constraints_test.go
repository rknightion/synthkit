// SPDX-License-Identifier: AGPL-3.0-only

package fixture_test

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/fixture"
)

func constraintInputs(t *testing.T, temperature float64) (*fixture.GPUTopology, fixture.GPUSnapshot, []fixture.GPUConstraintDemand) {
	t.Helper()
	top := gpuBuild(t, "constraint-proof", gpuSpec(t))
	for i := range top.Nodes {
		n := top.Nodes[i]
		n.Operating.AmbientTempC = temperature
		n.Operating.FullPowerRiseC = 0
		n.Operating.CoolingFaultRiseC = 0
		for j := range n.GPUs {
			n.GPUs[j].Model = "h100_sxm_80gb"
		}
	}
	a := fixture.GPUAllocationPlan(top, time.Unix(0, 0), nil)
	var demands []fixture.GPUConstraintDemand
	for _, x := range a.Allocations {
		demands = append(demands, fixture.GPUConstraintDemand{GPUKey: x.GPUKey, DriverVersion: "595.91.07", Requested: fixture.GPUClocks{SM: 1980, Memory: 2619}, UnconstrainedPowerW: 700})
	}
	return top, a, demands
}

// Catches boundary activation, cap-only activation and loss of concurrent causes.
func TestGPUConstraintCausalBoundary(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		temp, power            float64
		thermal, powerCap      *fixture.GPUClocks
		wantThermal, wantPower bool
		want                   fixture.GPUClocks
	}{
		{"boundary", 89, 700, &fixture.GPUClocks{SM: 1500, Memory: 2000}, &fixture.GPUClocks{SM: 1700, Memory: 1800}, false, false, fixture.GPUClocks{SM: 1980, Memory: 2619}},
		{"thermal", 90, 700, &fixture.GPUClocks{SM: 1500, Memory: 2000}, nil, true, false, fixture.GPUClocks{SM: 1500, Memory: 2000}},
		{"power", 89, 701, nil, &fixture.GPUClocks{SM: 1700, Memory: 1800}, false, true, fixture.GPUClocks{SM: 1700, Memory: 1800}},
		{"both", 90, 701, &fixture.GPUClocks{SM: 1500, Memory: 2000}, &fixture.GPUClocks{SM: 1700, Memory: 1800}, true, true, fixture.GPUClocks{SM: 1500, Memory: 1800}},
		{"crossing-without-caps", 90, 701, nil, nil, false, false, fixture.GPUClocks{SM: 1980, Memory: 2619}},
		{"nonreducing-caps", 90, 701, &fixture.GPUClocks{SM: 1980, Memory: 2619}, &fixture.GPUClocks{SM: 1980, Memory: 2619}, false, false, fixture.GPUClocks{SM: 1980, Memory: 2619}},
		{"operating-reference", 87, 700, &fixture.GPUClocks{SM: 1500, Memory: 2000}, nil, false, false, fixture.GPUClocks{SM: 1980, Memory: 2619}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			top, a, d := constraintInputs(t, tc.temp)
			for i := range d {
				d[i].UnconstrainedPowerW = tc.power
				d[i].ThermalClockCap = tc.thermal
				d[i].PowerClockCap = tc.powerCap
			}
			before, err := fixture.GPUOperatingPoints(top, a)
			if err != nil {
				t.Fatal(err)
			}
			got, err := fixture.GPUConstraintPoints(top, a, d)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.GPUs) != len(d) {
				t.Fatal("constraint coverage missing")
			}
			for _, p := range got.GPUs {
				if p.ThermalActive != tc.wantThermal || p.PowerActive != tc.wantPower || p.Achieved == nil || *p.Achieved != tc.want {
					t.Fatalf("unexpected causal point: %+v", p)
				}
			}
			after, err := fixture.GPUOperatingPoints(top, a)
			if err != nil || !reflect.DeepEqual(gpuJSON(t, before), gpuJSON(t, after)) {
				t.Fatal("legacy physics changed")
			}
		})
	}
}

// Catches using the product maximum instead of the configured power budget.
func TestGPUConstraintConfiguredBudget(t *testing.T) {
	for _, demand := range []float64{500, 501} {
		top, a, d := constraintInputs(t, 80)
		budget := 500.0
		for _, n := range top.Nodes {
			n.Operating.PowerLimitW = &budget
		}
		for i := range d {
			d[i].UnconstrainedPowerW = demand
			d[i].PowerClockCap = &fixture.GPUClocks{SM: 1800, Memory: 2500}
		}
		got, err := fixture.GPUConstraintPoints(top, a, d)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.GPUs) != len(d) {
			t.Fatal("constraint coverage missing")
		}
		for _, point := range got.GPUs {
			want := fixture.GPUClocks{SM: 1980, Memory: 2619}
			if demand > 500 {
				want = fixture.GPUClocks{SM: 1800, Memory: 2500}
			}
			if point.PowerActive != (demand > 500) || point.ThermalActive || point.Achieved == nil || *point.Achieved != want {
				t.Fatalf("configured budget ignored: %+v", point)
			}
		}
	}
}

// Catches driver/model leakage, fabricated availability and cooling-only activation.
func TestGPUConstraintFailClosed(t *testing.T) {
	for _, scenario := range []string{"driver", "model", "unavailable", "cooling"} {
		t.Run(scenario, func(t *testing.T) {
			top, a, d := constraintInputs(t, 90)
			for i := range d {
				d[i].ThermalClockCap = &fixture.GPUClocks{SM: 1000, Memory: 2000}
				d[i].PowerClockCap = &fixture.GPUClocks{SM: 1000, Memory: 2000}
				d[i].UnconstrainedPowerW = 701
			}
			switch scenario {
			case "driver":
				for i := range d {
					d[i].DriverVersion = "595.91.08"
				}
			case "model":
				for i := range top.Nodes {
					for j := range top.Nodes[i].GPUs {
						top.Nodes[i].GPUs[j].Model = "h100_pcie_80gb"
					}
				}
			case "unavailable":
				a = fixture.GPUAllocationPlan(top, a.At, func(_ time.Time, mode, key string) (bool, float64) { return mode == "gpu_node_drain", 1 })
			case "cooling":
				for i := range top.Nodes {
					top.Nodes[i].Operating.AmbientTempC = 80
				}
				a = fixture.GPUAllocationPlan(top, a.At, func(_ time.Time, mode, key string) (bool, float64) { return mode == "gpu_cooling_fault", 1 })
				for i := range d {
					d[i].UnconstrainedPowerW = 700
				}
			}
			got, err := fixture.GPUConstraintPoints(top, a, d)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.GPUs) != len(d) {
				t.Fatal("constraint coverage missing")
			}
			for _, p := range got.GPUs {
				if p.ThermalActive || p.PowerActive {
					t.Fatalf("unsupported/unavailable/cooling-only activation: %+v", p)
				}
				if scenario == "cooling" {
					if p.Achieved == nil {
						t.Fatal("supported available clocks absent")
					}
				} else if p.Achieved != nil {
					t.Fatal("unavailable or unsupported clocks fabricated")
				}
				if (scenario == "driver" || scenario == "model") && p.Profile != nil {
					t.Fatal("profile leaked")
				}
				if scenario == "unavailable" && p.Available {
					t.Fatal("drain did not reach allocation boundary")
				}
			}
		})
	}
}

// Catches malformed demand admission and provenance bypass through the new seam.
func TestGPUConstraintValidationAndProvenance(t *testing.T) {
	for _, scenario := range []string{"nan", "negative", "sm-over", "memory-over", "cap-over", "cap-negative", "cap-inf", "power-inf", "missing", "duplicate", "unknown", "origin", "budget-floor"} {
		t.Run(scenario, func(t *testing.T) {
			top, a, d := constraintInputs(t, 89)
			switch scenario {
			case "nan":
				d[0].Requested.SM = math.NaN()
			case "negative":
				d[0].UnconstrainedPowerW = -1
			case "sm-over":
				d[0].Requested.SM = 1981
			case "memory-over":
				d[0].Requested.Memory = 2620
			case "cap-over":
				d[0].PowerClockCap = &fixture.GPUClocks{SM: 1981, Memory: 2000}
			case "cap-negative":
				d[0].ThermalClockCap = &fixture.GPUClocks{SM: -1, Memory: 2000}
			case "cap-inf":
				d[0].ThermalClockCap = &fixture.GPUClocks{SM: math.Inf(1), Memory: 2000}
			case "power-inf":
				d[0].UnconstrainedPowerW = math.Inf(1)
			case "missing":
				d = d[1:]
			case "duplicate":
				d = append(d, d[0])
			case "unknown":
				d[0].GPUKey = "absent"
			case "origin":
				top, _, _ = constraintInputs(t, 89)
			case "budget-floor":
				v := 199.0
				top.Nodes[0].Operating.PowerLimitW = &v
			}
			if _, err := fixture.GPUConstraintPoints(top, a, d); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	profile := fixture.LookupGPUConstraintProfile("h100_sxm_80gb", "595.91.07")
	if profile == nil || profile.Source.SHA256 != "403b1ef416c0de549677eef77ba1c9175d5fcf3eb88270ebb98b88092315bd4d" || !strings.Contains(profile.Source.Revision, "DCGM 4.6.1") || profile.SlowdownTempC != 89 || profile.ShutdownTempC != 95 || profile.MaxOperatingTempC != 87 {
		t.Fatal("captured profile provenance/bounds missing")
	}
	profile.MaxClocks.SM = 1
	if fixture.LookupGPUConstraintProfile("h100_sxm_80gb", "595.91.07").MaxClocks.SM != 1980 {
		t.Fatal("caller mutation leaked")
	}
}
