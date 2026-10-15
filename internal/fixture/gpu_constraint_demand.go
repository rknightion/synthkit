// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"fmt"
	"math"

	"github.com/rknightion/synthkit/internal/failuremode"
)

// GPUConstraintOperatingSpec declares synthetic demand and controller caps, not
// an inferred vendor clock/power curve. It belongs to the shared physical pool.
type GPUConstraintOperatingSpec struct {
	DriverVersion           string  `yaml:"driver_version"`             // Exact captured driver profile; currently 595.91.07.
	RequestedSMClockMHz     float64 `yaml:"requested_sm_clock_mhz"`     // Explicit requested SM clock, capped by the sourced profile.
	RequestedMemoryClockMHz float64 `yaml:"requested_memory_clock_mhz"` // Explicit requested memory clock, capped by the sourced profile.
	// FullDemandPowerW is requested power at full workload demand, before caps.
	FullDemandPowerW float64 `yaml:"full_demand_power_w"` // Unconstrained requested watts at full workload demand.
	// PowerFaultDemandW replaces full-demand power during gpu_power_constraint.
	PowerFaultDemandW float64 `yaml:"power_fault_demand_w"` // Full-demand watts during the shared power-constraint incident.
	// These explicit controller setpoints do not assert a hardware response curve.
	SoftwareThermalClockCap *GPUClocks `yaml:"software_thermal_clock_cap"` // Explicit software thermal controller setpoint; no inferred response curve.
	PowerClockCap           *GPUClocks `yaml:"power_clock_cap"`            // Explicit power-budget controller setpoint.
}

const GPUPowerConstraintMode = "gpu_power_constraint"

// GPUConstraintFailureModes is additive: legacy failure-mode inventory stays
// unchanged. Consumers explicitly register this shared demand-source incident.
func GPUConstraintFailureModes() []failuremode.Mode {
	return []failuremode.Mode{
		{Name: GPUPowerConstraintMode, Axis: AxisGPU, Help: "Declared workload demand exceeds the configured GPU power budget"},
		{Name: GPUPowerConstraintMode, Axis: AxisGPUNode, Help: "Declared workload demand exceeds the configured GPU power budget"},
	}
}

func (s *GPUSelection) CoversConstraintTarget(mode, key string) bool {
	if s == nil || s.Topology == nil || mode != GPUPowerConstraintMode {
		return false
	}
	x, ok := s.Topology.Target(key)
	if !ok || (x.Axis != AxisGPU && x.Axis != AxisGPUNode) {
		return false
	}
	for _, n := range s.Nodes() {
		if n.Operating.Constraints == nil {
			continue
		}
		if key == n.Key {
			return true
		}
		for _, g := range n.GPUs {
			if key == g.Key {
				return true
			}
		}
	}
	return false
}

func validateGPUConstraintOperating(o GPUOperatingSpec, model string) error {
	spec := o.Constraints
	if spec == nil {
		return nil
	}
	p := LookupGPUConstraintProfile(model, spec.DriverVersion)
	if p == nil {
		return fmt.Errorf("unsupported GPU constraint model/driver")
	}
	requested := GPUClocks{SM: spec.RequestedSMClockMHz, Memory: spec.RequestedMemoryClockMHz}
	if !gpuFinite(requested.SM, requested.Memory, spec.FullDemandPowerW, spec.PowerFaultDemandW) || requested.SM <= 0 || requested.Memory <= 0 || requested.SM > p.MaxClocks.SM || requested.Memory > p.MaxClocks.Memory {
		return fmt.Errorf("invalid requested GPU clocks or demand")
	}
	limit := p.MaxPowerW
	if o.PowerLimitW != nil {
		limit = *o.PowerLimitW
	}
	if limit < p.MinPowerW || limit > p.MaxPowerW || spec.FullDemandPowerW < limit*o.IdlePowerFraction || spec.PowerFaultDemandW < spec.FullDemandPowerW {
		return fmt.Errorf("invalid GPU power budget or unconstrained demand")
	}
	for _, cap := range []*GPUClocks{spec.SoftwareThermalClockCap, spec.PowerClockCap} {
		if cap != nil && (!gpuFinite(cap.SM, cap.Memory) || cap.SM < 0 || cap.Memory < 0 || cap.SM > requested.SM || cap.Memory > requested.Memory) {
			return fmt.Errorf("invalid declared GPU controller cap")
		}
	}
	return nil
}

// GPUConstraintDemands derives all requests from the same allocation and shared
// operating declarations used by other GPU consumers. Actual draw remains the
// existing budget-bounded operating model; demand is the hypothetical uncapped
// request. Cooling/congestion may reduce actual draw below that request.
func GPUConstraintDemands(top *GPUTopology, allocation GPUSnapshot, eval GPUFailureEval) ([]GPUConstraintDemand, error) {
	physical, err := GPUOperatingPoints(top, allocation)
	if err != nil {
		return nil, err
	}
	var demands []GPUConstraintDemand
	for _, point := range physical.GPUs {
		gpu, _ := top.GPU(point.GPUKey)
		node, _ := top.Node(gpu.NodeKey)
		d := GPUConstraintDemand{GPUKey: gpu.Key}
		spec := node.Operating.Constraints
		if spec == nil {
			demands = append(demands, d)
			continue
		}
		if err := validateGPUConstraintOperating(node.Operating, gpu.Model); err != nil {
			return nil, err
		}
		full := spec.FullDemandPowerW
		if eval != nil {
			for _, key := range []string{gpu.Key, node.Key} {
				active, intensity := eval(allocation.At, GPUPowerConstraintMode, key)
				if active && gpuFinite(intensity) && intensity > 0 {
					full = math.Max(full, spec.FullDemandPowerW+(spec.PowerFaultDemandW-spec.FullDemandPowerW)*math.Min(1, intensity))
				}
			}
		}
		idle := point.PowerLimitW * node.Operating.IdlePowerFraction
		d.DriverVersion = spec.DriverVersion
		d.Requested = GPUClocks{SM: spec.RequestedSMClockMHz, Memory: spec.RequestedMemoryClockMHz}
		d.UnconstrainedPowerW = idle + (full-idle)*point.DemandUtilization
		d.ThermalClockCap, d.PowerClockCap = spec.SoftwareThermalClockCap, spec.PowerClockCap
		demands = append(demands, d)
	}
	return demands, nil
}
