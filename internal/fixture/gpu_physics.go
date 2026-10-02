// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"fmt"
	"math"
	"slices"
	"time"
)

func GPUOperatingPoints(t *GPUTopology, a GPUSnapshot) (GPUPhysicalSnapshot, error) {
	out := GPUPhysicalSnapshot{At: a.At}
	bad := func(reason string) (GPUPhysicalSnapshot, error) {
		return GPUPhysicalSnapshot{}, fmt.Errorf("GPU operating snapshot: %s", reason)
	}
	// Origin is resolution-lifetime provenance, not inferred from matching namespaces.
	if t == nil || a.origin == nil || a.origin != t {
		return bad("snapshot origin differs from topology")
	}
	if a.Bucket != gpuFloorDiv(a.At.UTC().Unix(), 60) {
		return bad("inconsistent bucket/time")
	}
	plans := map[string]GPUWorkloadPlan{}
	for _, w := range gpuAllWorkloads(t) {
		plans[w.Key] = w
	}
	statuses := map[string]GPUWorkloadStatus{}
	for _, st := range a.Workloads {
		if _, ok := plans[st.WorkloadKey]; !ok {
			return bad("unknown workload key")
		}
		if _, dup := statuses[st.WorkloadKey]; dup {
			return bad("duplicate workload key")
		}
		statuses[st.WorkloadKey] = st
	}
	if len(statuses) != len(plans) {
		return bad("missing workload status")
	}
	allocations := map[string]GPUAllocation{}
	heldKeys := map[string][]string{}
	for _, x := range a.Allocations {
		g, ok := t.GPU(x.GPUKey)
		if !ok {
			return bad("unknown GPU key")
		}
		if _, dup := allocations[x.GPUKey]; dup {
			return bad("duplicate GPU key")
		}
		if x.State == "held" {
			w, ok := plans[x.WorkloadKey]
			if !ok || statuses[w.Key].Phase != "running" {
				return bad("inconsistent held workload")
			}
			found := false
			for _, worker := range w.Workers {
				if worker.Key == x.WorkerKey && slices.Contains(worker.GPUKeys, g.Key) {
					found = true
					if (worker.Pod == nil) != (x.Pod == nil) || (worker.Pod != nil && *worker.Pod != *x.Pod) {
						return bad("inconsistent pod identity")
					}
				}
			}
			if !found {
				return bad("inconsistent worker/GPU key")
			}
			heldKeys[w.Key] = append(heldKeys[w.Key], g.Key)
		} else if (x.State != "idle" && x.State != "unavailable") || x.WorkloadKey != "" || x.WorkerKey != "" || x.Pod != nil {
			return bad("inconsistent unheld GPU")
		}
		allocations[x.GPUKey] = x
	}
	count := 0
	for _, n := range t.Nodes {
		count += len(n.GPUs)
	}
	if len(allocations) != count {
		return bad("missing GPU allocations")
	}
	for key, st := range statuses {
		keys := gpuUnique(heldKeys[key])
		if !slices.Equal(keys, gpuUnique(st.GPUKeys)) {
			return bad("inconsistent workload holdings")
		}
		if st.Phase == "running" && !slices.Equal(keys, gpuWorkloadKeys(plans[key])) {
			return bad("partial atomic workload holding")
		}
	}
	for _, f := range a.Faults {
		x, ok := t.Target(f.Target)
		if !ok || !gpuModeTarget(t, f.Mode, x) || !gpuFinite(f.Intensity) || f.Intensity <= 0 || f.Intensity > 1 {
			return bad("invalid captured shared fault")
		}
	}
	intensity := func(mode string, g GPU) float64 {
		i := 0.0
		for _, f := range a.Faults {
			if f.Mode == mode && gpuAffectsGPU(t, mode, f.Target, g) {
				i = math.Max(i, f.Intensity)
			}
		}
		return i
	}
	rackCooling := func(key string) float64 {
		i := 0.0
		for _, f := range a.Faults {
			if f.Mode != "gpu_cooling_fault" {
				continue
			}
			target := f.Target
			if d, ok := gpuDevice(t, target); ok {
				target = d.RackKey
			}
			if target == key {
				i = math.Max(i, f.Intensity)
			}
		}
		return i
	}
	workPoints := map[string]GPUWorkloadOperatingPoint{}
	holdCount := map[string]int{}
	for key := range plans {
		workPoints[key] = GPUWorkloadOperatingPoint{WorkloadKey: key}
	}
	for _, n := range t.Nodes {
		o := n.Operating
		np := GPUNodeOperatingPoint{NodeKey: n.Key}
		for _, g := range n.GPUs {
			m, ok := LookupGPUModel(g.Model)
			if !ok || m.MaxPowerW == nil {
				return bad("missing model power source")
			}
			if err := gpuOperatingValid(o, m); err != nil {
				return bad(err.Error())
			}
			limit := *m.MaxPowerW
			if o.PowerLimitW != nil {
				limit = *o.PowerLimitW
			}
			x := allocations[g.Key]
			ic := rackCooling(n.RackKey)
			p := GPUOperatingPoint{GPUKey: g.Key, Available: x.State != "unavailable", AdvertisedMemoryBytes: uint64(math.Round(m.AdvertisedMemoryGB * 1e9)), PowerLimitW: limit, CoolingDerate: 1 - o.CoolingPerfLoss*ic,
				VendorThermalEnvelopeKnown: m.SlowdownTempC != nil && m.ShutdownTempC != nil && m.MaxOperatingTempC != nil}
			p.MemoryAccountingLimitBytes = p.AdvertisedMemoryBytes
			if g.UsableMemory != nil {
				p.MemoryAccountingLimitBytes = g.UsableMemory.Bytes
			}
			if x.State == "held" {
				w := plans[x.WorkloadKey]
				d := w.Demand
				demand := d.UtilizationBase
				if d.UtilizationAmplitude != 0 {
					period, err := time.ParseDuration(d.Period)
					if err != nil || period <= 0 {
						return bad("invalid demand period")
					}
					phase := float64(gpuHash(t.Seed, "demand_phase", w.Key)>>11) / float64(uint64(1)<<53)
					demand += d.UtilizationAmplitude * math.Sin(2*math.Pi*((float64(a.Bucket)*60+30)/period.Seconds()+phase))
				}
				p.DemandUtilization = demand
				ib := intensity("gpu_backend_congestion", g)
				is := intensity("gpu_storage_latency", g)
				in := math.Max(intensity("gpu_nvlink_degraded", g), intensity("gpu_nvswitch_tray_failure", g))
				p.Utilization = demand * p.CoolingDerate * (1 - o.CongestionPerfLoss*ib) * (1 - o.StoragePerfLoss*is) * (1 - o.CongestionPerfLoss*in)
				p.MemoryUsedBytes = uint64(math.Floor(float64(p.MemoryAccountingLimitBytes) * (d.MemoryReservedFraction + d.MemoryDynamicFraction*demand)))
				wp := workPoints[w.Key]
				wp.DemandUtilization += demand
				wp.EffectiveUtilization += p.Utilization
				wp.RequestsPerSecond += d.RequestsPerSecondAtFullUtilization * p.Utilization
				wp.MemoryUsedBytes += p.MemoryUsedBytes
				workPoints[w.Key] = wp
				holdCount[w.Key]++
			}
			p.PowerW = limit * (o.IdlePowerFraction + (1-o.IdlePowerFraction)*p.Utilization)
			p.TemperatureC = o.AmbientTempC + o.FullPowerRiseC*(p.PowerW/limit) + o.CoolingFaultRiseC*ic
			if !gpuFinite(p.PowerW, p.TemperatureC, p.Utilization, p.DemandUtilization) {
				return bad("nonfinite operating point")
			}
			np.GPUWatts += p.PowerW
			np.LoadFraction = math.Max(np.LoadFraction, p.Utilization)
			out.GPUs = append(out.GPUs, p)
		}
		hw := LookupNodeSpec(*n.Node)
		np.OverheadWatts = o.NodeBasePowerW + o.NodeDynamicPerCoreW*float64(hw.VCPU)*np.LoadFraction + o.NodeDynamicPerNICW*float64(len(n.NICs))*np.LoadFraction
		np.PowerW = np.GPUWatts + np.OverheadWatts
		out.Nodes = append(out.Nodes, np)
	}
	devices := []GPUDevice{}
	devices = append(devices, t.devices...)
	for _, n := range t.Nodes {
		devices = append(devices, n.BMC)
	}
	for _, r := range t.Racks {
		p := GPURackOperatingPoint{RackKey: r.Key, FixedOverheadWatts: r.Physics.FixedOverheadW}
		rise := 0.0
		for _, np := range out.Nodes {
			n, _ := t.Node(np.NodeKey)
			if n.RackKey == r.Key {
				p.NodeWatts += np.PowerW
				rise = math.Max(rise, n.Operating.CoolingFaultRiseC)
			}
		}
		for _, d := range devices {
			if d.RackKey == r.Key && d.StaticPowerW != nil {
				p.DeclaredDeviceWatts += *d.StaticPowerW
			}
		}
		p.PowerW = p.NodeWatts + p.DeclaredDeviceWatts + p.FixedOverheadWatts
		p.LiquidHeatW = p.PowerW * r.Physics.LiquidHeatFraction
		if r.Cooling == "liquid" {
			ic := rackCooling(r.Key)
			flow := r.Physics.FlowLPM * (1 - 0.5*ic)
			supply := r.Physics.SupplyTempC + rise*ic
			ret := supply + p.LiquidHeatW/(flow*r.Physics.FluidDensityKgPerL/60*r.Physics.FluidSpecificHeatJPerKgC)
			if !gpuFinite(flow, supply, ret) || flow <= 0 {
				return bad("invalid liquid thermal arithmetic")
			}
			p.FlowLPM = &flow
			p.SupplyTempC = &supply
			p.ReturnTempC = &ret
		}
		out.Racks = append(out.Racks, p)
	}
	for _, key := range gpuSortedMapKeys(workPoints) {
		p := workPoints[key]
		if holdCount[key] > 0 {
			p.DemandUtilization /= float64(holdCount[key])
			p.EffectiveUtilization /= float64(holdCount[key])
		}
		out.Workloads = append(out.Workloads, p)
	}
	for _, d := range t.Domains {
		for _, l := range d.Links {
			g, _ := t.GPU(l.GPUKey)
			n, _ := t.Node(g.NodeKey)
			i := 0.0
			for _, f := range a.Faults {
				if f.Mode == "gpu_nvlink_error_burst" && gpuEdgeAffected(t, l, f.Target) {
					i = math.Max(i, f.Intensity)
				}
			}
			out.NVLinkErrors = append(out.NVLinkErrors, NVLinkErrorRate{LinkKey: l.Key, GPUKey: l.GPUKey, PortKey: l.SwitchPortKey, ErrorsPerSecond: n.Operating.NVLinkErrorsPerSecond * i})
		}
	}
	return out, nil
}
