// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"fmt"
	"math"
	"time"
)

// GPUClocks are MHz. Caps are explicit synthetic scenario inputs, not a vendor curve.
type GPUClocks struct{ SM, Memory float64 }

// GPUConstraintDemand must cover every GPU, including unavailable GPUs.
type GPUConstraintDemand struct {
	GPUKey, DriverVersion          string
	Requested                      GPUClocks
	UnconstrainedPowerW            float64
	ThermalClockCap, PowerClockCap *GPUClocks
}

// GPUConstraintProfile contains captured bounds for one exact model/driver pair.
// It does not establish a memory thermal limit or a clock/power transfer function.
type GPUConstraintProfile struct {
	Model, DriverVersion                            string
	MaxClocks                                       GPUClocks
	MinPowerW, MaxPowerW                            float64
	SlowdownTempC, ShutdownTempC, MaxOperatingTempC float64
	Source                                          GPUFieldSource
}

// LookupGPUConstraintProfile never inherits model-wide facts for another driver.
func LookupGPUConstraintProfile(model, driver string) *GPUConstraintProfile {
	if model != "h100_sxm_80gb" || driver != "595.91.07" {
		return nil
	}
	return &GPUConstraintProfile{
		Model: model, DriverVersion: driver, MaxClocks: GPUClocks{SM: 1980, Memory: 2619},
		MinPowerW: 200, MaxPowerW: 700, SlowdownTempC: 89, ShutdownTempC: 95, MaxOperatingTempC: 87,
		Source: GPUFieldSource{URL: "https://docs.nvidia.com/deploy/nvidia-smi/index.html", Revision: "H100 SXM driver 595.91.07 capture 2026-10-03; DCGM 4.6.1; exporter 4.8.4; accepted contract rev3; documentation current/unversioned", SHA256: "403b1ef416c0de549677eef77ba1c9175d5fcf3eb88270ebb98b88092315bd4d", Section: "captured nvidia-smi query: clocks, power limits and core temperature/T.Limit; SHA256 identifies capture, not documentation"},
	}
}

type GPUConstraintPoint struct {
	GPUKey              string
	Available           bool
	Requested           GPUClocks
	UnconstrainedPowerW float64
	Profile             *GPUConstraintProfile
	// Achieved is absent when unavailable or the exact profile is unsupported.
	Achieved                   *GPUClocks
	ThermalActive, PowerActive bool
}

type GPUConstraintSnapshot struct {
	At   time.Time
	GPUs []GPUConstraintPoint
}

// GPUConstraintPoints validates through the existing pure physics seam and adds
// causal clock constraints without altering its output or allocation state.
func GPUConstraintPoints(t *GPUTopology, a GPUSnapshot, demands []GPUConstraintDemand) (GPUConstraintSnapshot, error) {
	physical, err := GPUOperatingPoints(t, a)
	if err != nil {
		return GPUConstraintSnapshot{}, err
	}
	bad := func(reason string) (GPUConstraintSnapshot, error) {
		return GPUConstraintSnapshot{}, fmt.Errorf("GPU constraint snapshot: %s", reason)
	}
	byKey := make(map[string]GPUConstraintDemand, len(demands))
	for _, d := range demands {
		g, ok := t.GPU(d.GPUKey)
		if !ok {
			return bad("unknown GPU demand key")
		}
		if _, dup := byKey[d.GPUKey]; dup {
			return bad("duplicate GPU demand key")
		}
		if !gpuFinite(d.Requested.SM, d.Requested.Memory, d.UnconstrainedPowerW) || d.Requested.SM < 0 || d.Requested.Memory < 0 || d.UnconstrainedPowerW < 0 {
			return bad("invalid request")
		}
		profile := LookupGPUConstraintProfile(g.Model, d.DriverVersion)
		if profile != nil && (d.Requested.SM > profile.MaxClocks.SM || d.Requested.Memory > profile.MaxClocks.Memory) {
			return bad("request exceeds captured clock envelope")
		}
		for _, cap := range []*GPUClocks{d.ThermalClockCap, d.PowerClockCap} {
			if cap != nil && (!gpuFinite(cap.SM, cap.Memory) || cap.SM < 0 || cap.Memory < 0 || cap.SM > d.Requested.SM || cap.Memory > d.Requested.Memory) {
				return bad("invalid clock cap")
			}
		}
		byKey[d.GPUKey] = d
	}
	if len(byKey) != len(physical.GPUs) {
		return bad("incomplete GPU demand coverage")
	}
	out := GPUConstraintSnapshot{At: physical.At}
	for _, p := range physical.GPUs {
		d := byKey[p.GPUKey]
		g, _ := t.GPU(p.GPUKey)
		profile := LookupGPUConstraintProfile(g.Model, d.DriverVersion)
		point := GPUConstraintPoint{GPUKey: p.GPUKey, Available: p.Available, Requested: d.Requested, UnconstrainedPowerW: d.UnconstrainedPowerW, Profile: profile}
		if profile != nil {
			if p.PowerLimitW < profile.MinPowerW || p.PowerLimitW > profile.MaxPowerW {
				return bad("configured power budget outside captured range")
			}
			if p.Available {
				clocks := d.Requested
				reducing := func(cap *GPUClocks) bool {
					return cap != nil && (cap.SM < d.Requested.SM || cap.Memory < d.Requested.Memory)
				}
				point.ThermalActive = p.TemperatureC > profile.SlowdownTempC && reducing(d.ThermalClockCap)
				point.PowerActive = d.UnconstrainedPowerW > p.PowerLimitW && reducing(d.PowerClockCap)
				for _, constraint := range []struct {
					active bool
					cap    *GPUClocks
				}{{point.ThermalActive, d.ThermalClockCap}, {point.PowerActive, d.PowerClockCap}} {
					if constraint.active {
						clocks.SM = math.Min(clocks.SM, constraint.cap.SM)
						clocks.Memory = math.Min(clocks.Memory, constraint.cap.Memory)
					}
				}
				point.Achieved = &clocks
			}
		}
		out.GPUs = append(out.GPUs, point)
	}
	return out, nil
}
