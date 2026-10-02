// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"math"
	"slices"
	"sort"
	"strconv"
	"time"
)

type GPUFailureEval func(
	at time.Time,
	mode, target string,
) (active bool, intensity float64)

type GPUProject struct {
	Key, Name, Department, Partition string
	GPUQuota                         int
}

type GPUWorkerPlacement struct {
	Key, Name, WorkloadKey, NodeKey string
	GPUKeys                         []string
	Pod                             *PodIdentity
}

type GPUWorkloadPlan struct {
	Key, Name, Scheduler, Project, Role       string
	Priority                                  int
	Cycle, PendingFor, RunningMin, RunningMax time.Duration
	Demand                                    GPUDemandSpec
	Workers                                   []GPUWorkerPlacement
}

type GPUSchedulerPlan struct {
	Key, Name, Kind string
	PoolNames       []string
	Projects        []GPUProject
	Workloads       []GPUWorkloadPlan
}

type GPUAllocation struct {
	GPUKey, State, WorkloadKey, WorkerKey string
	Pod                                   *PodIdentity
}

type GPUWorkloadStatus struct {
	WorkloadKey, ExecutionID, Phase string
	CycleOrdinal                    int64
	GPUKeys, VictimGPUKeys          []string
	RetryPending                    bool
	CauseTargets                    []string
}

type GPUFault struct {
	Mode, Target string
	Intensity    float64
}

type GPUSnapshot struct {
	origin      *GPUTopology // set only by GPUAllocationPlan; resolution-lifetime provenance, excluded from serialization
	At          time.Time
	Bucket      int64
	Allocations []GPUAllocation
	Workloads   []GPUWorkloadStatus
	Faults      []GPUFault
}

type GPUOperatingPoint struct {
	GPUKey                            string
	Available                         bool
	DemandUtilization, Utilization    float64 // fractions in [0,1]
	AdvertisedMemoryBytes             uint64
	MemoryAccountingLimitBytes        uint64
	MemoryUsedBytes                   uint64
	PowerLimitW, PowerW, TemperatureC float64
	CoolingDerate                     float64 // synthetic factor, not a sourced hardware throttle code
	VendorThermalEnvelopeKnown        bool
}

type GPUNodeOperatingPoint struct {
	NodeKey                         string
	GPUWatts, OverheadWatts, PowerW float64
	LoadFraction                    float64
}

type GPURackOperatingPoint struct {
	RackKey                                                    string
	NodeWatts, DeclaredDeviceWatts, FixedOverheadWatts, PowerW float64
	LiquidHeatW                                                float64
	SupplyTempC, ReturnTempC, FlowLPM                          *float64
}

type GPUWorkloadOperatingPoint struct {
	WorkloadKey                             string
	DemandUtilization, EffectiveUtilization float64
	RequestsPerSecond                       float64
	MemoryUsedBytes                         uint64
}

type NVLinkErrorRate struct {
	LinkKey, GPUKey, PortKey string
	ErrorsPerSecond          float64
}

type GPUPhysicalSnapshot struct {
	At           time.Time
	GPUs         []GPUOperatingPoint
	Nodes        []GPUNodeOperatingPoint
	Racks        []GPURackOperatingPoint
	Workloads    []GPUWorkloadOperatingPoint
	NVLinkErrors []NVLinkErrorRate
}

func gpuHash(seed string, parts ...string) uint64 {
	v, _ := strconv.ParseUint(Sum(seed, parts...)[:16], 16, 64)
	return v
}
func gpuFloorDiv(a, b int64) int64 {
	q := a / b
	if a%b < 0 {
		q--
	}
	return q
}
func gpuAllWorkloads(t *GPUTopology) []GPUWorkloadPlan {
	var out []GPUWorkloadPlan
	if t != nil {
		for _, s := range t.Schedulers {
			out = append(out, s.Workloads...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
func gpuWorkloadKeys(w GPUWorkloadPlan) []string {
	var out []string
	for _, worker := range w.Workers {
		out = append(out, worker.GPUKeys...)
	}
	return gpuUnique(out)
}
func GPUAllocationPlan(t *GPUTopology, now time.Time, eval GPUFailureEval) GPUSnapshot {
	out := GPUSnapshot{origin: t, At: now.UTC(), Bucket: gpuFloorDiv(now.UTC().Unix(), 60)}
	if t == nil {
		return out
	}
	out.Faults = GPUFaults(t, now, eval)
	workloads := gpuAllWorkloads(t)
	candidate := map[string]bool{}
	statuses := map[string]GPUWorkloadStatus{}
	quotas := map[string]int{}
	for _, s := range t.Schedulers {
		for _, p := range s.Projects {
			quotas[p.Key] = p.GPUQuota
		}
	}
	for _, w := range workloads {
		c := int64(w.Cycle / time.Minute)
		phase := int64(gpuHash(t.Seed, "allocation_phase", w.Key) % uint64(c))
		ordinal := gpuFloorDiv(out.Bucket+phase, c)
		pos := out.Bucket + phase - ordinal*c
		lo, hi := int64(w.RunningMin/time.Minute), int64(w.RunningMax/time.Minute)
		running := lo + int64(gpuHash(t.Seed, "allocation_duration", w.Key, strconv.FormatInt(ordinal, 10))%uint64(hi-lo+1))
		pending := int64(w.PendingFor / time.Minute)
		st := GPUWorkloadStatus{WorkloadKey: w.Key, ExecutionID: GPUExecutionID(t.Seed, w.Key, ordinal), CycleOrdinal: ordinal, Phase: "pending"}
		if pos >= pending && pos < pending+running {
			candidate[w.Key] = true
		} else if pos == pending+running {
			st.Phase = "completed"
		}
		statuses[w.Key] = st
	}
	sort.Slice(workloads, func(i, j int) bool {
		if workloads[i].Priority == workloads[j].Priority {
			return workloads[i].Key < workloads[j].Key
		}
		return workloads[i].Priority > workloads[j].Priority
	})
	admit := func(excluded, unavailable map[string]bool, limits map[string]int) map[string]bool {
		admitted := map[string]bool{}
		held := map[string]bool{}
		used := map[string]int{}
		for _, w := range workloads {
			if !candidate[w.Key] || excluded[w.Key] {
				continue
			}
			keys := gpuWorkloadKeys(w)
			pk := "project:" + w.Scheduler + "/" + w.Project
			if used[pk]+len(keys) > limits[pk] {
				continue
			}
			ok := true
			for _, key := range keys {
				if held[key] || unavailable[key] {
					ok = false
				}
			}
			if ok {
				admitted[w.Key] = true
				used[pk] += len(keys)
				for _, key := range keys {
					held[key] = true
				}
			}
		}
		return admitted
	}
	baseline := admit(nil, nil, quotas)
	unavailable := map[string]bool{}
	excluded := map[string]bool{}
	for _, fault := range out.Faults {
		if fault.Mode == "gpu_fallen_off_bus" || fault.Mode == "gpu_node_drain" || fault.Mode == "gpu_operand_unavailable" {
			for _, n := range t.Nodes {
				for _, g := range n.GPUs {
					if gpuAffectsGPU(t, fault.Mode, fault.Target, g) {
						unavailable[g.Key] = true
					}
				}
			}
			for _, w := range workloads {
				if !baseline[w.Key] {
					continue
				}
				hit := false
				for _, key := range gpuWorkloadKeys(w) {
					g, _ := t.GPU(key)
					if gpuAffectsGPU(t, fault.Mode, fault.Target, g) {
						hit = true
					}
				}
				if hit {
					excluded[w.Key] = true
					st := statuses[w.Key]
					st.Phase = "failed"
					st.RetryPending = true
					st.VictimGPUKeys = gpuWorkloadKeys(w)
					st.CauseTargets = append(st.CauseTargets, fault.Target)
					statuses[w.Key] = st
				}
			}
		}
		if fault.Mode == "gpu_quota_exhaustion" {
			quota := 0
			for _, s := range t.Schedulers {
				for _, p := range s.Projects {
					if p.Key == fault.Target {
						quota = p.GPUQuota
					}
				}
			}
			effective := int(math.Floor(float64(quota) * (1 - fault.Intensity)))
			if effective < quotas[fault.Target] {
				quotas[fault.Target] = effective
			}
		}
		if fault.Mode == "gpu_preemption_storm" {
			var victims []GPUWorkloadPlan
			for _, s := range t.Schedulers {
				for _, w := range s.Workloads {
					if baseline[w.Key] && gpuWorkloadTarget(s, w, fault.Target) {
						victims = append(victims, w)
					}
				}
			}
			sort.Slice(victims, func(i, j int) bool {
				if victims[i].Priority == victims[j].Priority {
					return victims[i].Key < victims[j].Key
				}
				return victims[i].Priority < victims[j].Priority
			})
			count := int(math.Ceil(fault.Intensity * float64(len(victims))))
			for _, w := range victims[:count] {
				excluded[w.Key] = true
				st := statuses[w.Key]
				if st.Phase != "failed" {
					st.Phase = "preempted"
				}
				st.VictimGPUKeys = gpuWorkloadKeys(w)
				st.CauseTargets = append(st.CauseTargets, fault.Target)
				statuses[w.Key] = st
			}
		}
	}
	final := admit(excluded, unavailable, quotas)
	alloc := map[string]GPUAllocation{}
	for _, n := range t.Nodes {
		for _, g := range n.GPUs {
			state := "idle"
			if unavailable[g.Key] {
				state = "unavailable"
			}
			alloc[g.Key] = GPUAllocation{GPUKey: g.Key, State: state}
		}
	}
	for _, w := range workloads {
		st := statuses[w.Key]
		if final[w.Key] {
			st.Phase = "running"
			st.GPUKeys = gpuWorkloadKeys(w)
			for _, worker := range w.Workers {
				for _, key := range worker.GPUKeys {
					alloc[key] = GPUAllocation{GPUKey: key, State: "held", WorkloadKey: w.Key, WorkerKey: worker.Key, Pod: worker.Pod}
				}
			}
		}
		if baseline[w.Key] && !final[w.Key] && !excluded[w.Key] {
			for _, f := range out.Faults {
				if f.Mode == "gpu_quota_exhaustion" && f.Target == "project:"+w.Scheduler+"/"+w.Project {
					st.VictimGPUKeys = gpuWorkloadKeys(w)
					st.CauseTargets = append(st.CauseTargets, f.Target)
				}
			}
		}
		st.CauseTargets = gpuUnique(st.CauseTargets)
		statuses[w.Key] = st
	}
	for _, key := range gpuSortedMapKeys(alloc) {
		out.Allocations = append(out.Allocations, alloc[key])
	}
	for _, key := range gpuSortedMapKeys(statuses) {
		out.Workloads = append(out.Workloads, statuses[key])
	}
	return out
}
func GPUClusterView(cluster *Cluster, snapshot GPUSnapshot) Cluster {
	if cluster == nil {
		return Cluster{}
	}
	out := *cluster
	out.Workloads = slices.Clone(cluster.Workloads)
	out.SubstrateWorkloads = slices.Clone(cluster.SubstrateWorkloads)
	status := map[string]string{}
	workerWorkload := map[string]string{}
	if cluster.GPU != nil {
		for _, w := range gpuAllWorkloads(cluster.GPU) {
			for _, worker := range w.Workers {
				workerWorkload[worker.Key] = w.Key
			}
		}
	}
	for _, st := range snapshot.Workloads {
		status[st.WorkloadKey] = st.Phase
	}
	for i := range out.SubstrateWorkloads {
		w := &out.SubstrateWorkloads[i]
		if w.GPUWorkerKey != "" {
			w.PodNames = slices.Clone(w.PodNames)
			w.NodeIdx = slices.Clone(w.NodeIdx)
			w.GPUPhase = status[workerWorkload[w.GPUWorkerKey]]
		}
	}
	return out
}
