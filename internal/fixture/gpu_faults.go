// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"fmt"
	"github.com/rknightion/synthkit/internal/failuremode"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	AxisGPU             failuremode.Axis = "gpu"
	AxisGPUNode         failuremode.Axis = "gpu_node"
	AxisGPURack         failuremode.Axis = "gpu_rack"
	AxisGPUTray         failuremode.Axis = "gpu_tray"
	AxisNVLinkDomain    failuremode.Axis = "nvlink_domain"
	AxisNVLinkPartition failuremode.Axis = "nvlink_partition"
	AxisNVLinkSwitch    failuremode.Axis = "nvlink_switch"
	AxisNVLinkPort      failuremode.Axis = "nvlink_port"
	AxisGPUFabric       failuremode.Axis = "gpu_fabric"
	AxisGPUSwitch       failuremode.Axis = "gpu_switch"
	AxisGPUPort         failuremode.Axis = "gpu_port"
	AxisGPUDevice       failuremode.Axis = "gpu_device"
	AxisGPUStorage      failuremode.Axis = "gpu_storage"
	AxisGPUScheduler    failuremode.Axis = "gpu_scheduler"
	AxisGPUProject      failuremode.Axis = "gpu_project"
	AxisGPUWorkload     failuremode.Axis = "gpu_workload"
)

var gpuContracts = []GPUFailureContract{
	{"gpu_xid_burst", []failuremode.Axis{AxisGPU, AxisGPUNode, AxisGPURack, AxisGPUTray}, []string{"dcgm", "host", "k8s_cluster"}, "nonfatal event"},
	{"gpu_fallen_off_bus", []failuremode.Axis{AxisGPU, AxisGPUNode, AxisGPUTray}, []string{"dcgm", "host", "k8s_cluster", "gpuoperator", "runai", "slurm", "mission_control", "inference_serving"}, "unavailable"},
	{"gpu_node_drain", []failuremode.Axis{AxisGPUNode, AxisGPURack, AxisGPUTray}, []string{"dcgm", "k8s_cluster", "gpuoperator", "bcm", "redfish", "runai", "slurm", "mission_control", "inference_serving"}, "unavailable"},
	{"gpu_operand_unavailable", []failuremode.Axis{AxisGPUNode}, []string{"gpuoperator", "k8s_cluster", "dcgm", "runai", "inference_serving"}, "unavailable"},
	{"gpu_preemption_storm", []failuremode.Axis{AxisGPUScheduler, AxisGPUProject, AxisGPUWorkload}, []string{"runai", "slurm", "dcgm", "k8s_cluster", "inference_serving", "mission_control"}, "preemption"},
	{"gpu_quota_exhaustion", []failuremode.Axis{AxisGPUProject}, []string{"runai", "slurm", "dcgm", "k8s_cluster", "inference_serving"}, "quota"},
	{"gpu_backend_congestion", []failuremode.Axis{AxisGPUFabric, AxisGPUSwitch, AxisGPUPort}, []string{"spectrumx", "dcgm", "runai", "slurm", "inference_serving"}, "slowdown"},
	{"gpu_storage_latency", []failuremode.Axis{AxisGPUStorage}, []string{"vast", "dcgm", "runai", "slurm", "inference_serving"}, "slowdown"},
	{"gpu_cooling_fault", []failuremode.Axis{AxisGPURack, AxisGPUDevice}, []string{"rack_facility", "snmp_exporter", "bcm", "dcgm", "redfish", "runai", "inference_serving"}, "cooling"},
	{"gpu_leak_detected", []failuremode.Axis{AxisGPURack, AxisGPUDevice}, []string{"rack_facility", "snmp_exporter", "bcm", "mission_control"}, "health event"},
	{"gpu_nvlink_degraded", []failuremode.Axis{AxisNVLinkDomain, AxisNVLinkPartition}, []string{"nvlink", "dcgm", "runai", "slurm", "inference_serving"}, "slowdown"},
	{"gpu_nvswitch_tray_failure", []failuremode.Axis{AxisGPUTray}, []string{"nvlink", "dcgm", "mission_control", "runai", "slurm", "inference_serving"}, "slowdown"},
	{"gpu_nvlink_error_burst", []failuremode.Axis{AxisGPU, AxisNVLinkDomain, AxisNVLinkPartition, AxisNVLinkSwitch, AxisNVLinkPort}, []string{"nvlink", "dcgm"}, "confirmed edge errors"},
}

func GPUFailureContracts() []GPUFailureContract {
	out := slices.Clone(gpuContracts)
	for i := range out {
		out[i].Axes = slices.Clone(out[i].Axes)
		out[i].Consumers = slices.Clone(out[i].Consumers)
	}
	return out
}
func GPUFailureModes(kind string) []failuremode.Mode {
	var out []failuremode.Mode
	for _, c := range gpuContracts {
		if slices.Contains(c.Consumers, kind) {
			for _, axis := range c.Axes {
				out = append(out, failuremode.Mode{Name: c.Name, Axis: axis, Help: c.Effect})
			}
		}
	}
	return out
}
func gpuBuildTargets(t *GPUTopology) error {
	seen := map[string]bool{}
	add := func(key, kind string, axis failuremode.Axis, parents ...string) error {
		if key == "" {
			return nil
		}
		if seen[key] {
			return fmt.Errorf("gpu_compute: duplicate generated target %q", key)
		}
		seen[key] = true
		var p []string
		for _, x := range parents {
			if x != "" {
				p = append(p, x)
			}
		}
		t.Targets = append(t.Targets, GPUTarget{Key: key, Kind: kind, Axis: axis, ParentKeys: gpuUnique(p)})
		return nil
	}
	for _, r := range t.Racks {
		if err := add(r.Key, r.Cooling+"_rack", AxisGPURack); err != nil {
			return err
		}
	}
	for _, tr := range t.Trays {
		parents := []string{tr.RackKey}
		if tr.Kind == "nvswitch" {
			for _, d := range t.Domains {
				if d.RackKey == tr.RackKey {
					parents = append(parents, d.Key)
				}
			}
		}
		if err := add(tr.Key, tr.Kind+"_tray", AxisGPUTray, parents...); err != nil {
			return err
		}
	}
	for _, n := range t.Nodes {
		if err := add(n.Key, "node", AxisGPUNode, n.TrayKey, n.RackKey); err != nil {
			return err
		}
		if err := add(n.BMC.Key, "bmc", AxisGPUDevice, n.Key, n.TrayKey, n.RackKey); err != nil {
			return err
		}
		for _, g := range n.GPUs {
			if err := add(g.Key, "gpu", AxisGPU, g.NodeKey, g.DomainKey, g.PartitionKey); err != nil {
				return err
			}
		}
	}
	for _, f := range t.Fabrics {
		if err := add(f.Key, f.Role+"_fabric", AxisGPUFabric); err != nil {
			return err
		}
		for _, d := range f.Devices {
			if err := add(d.Key, f.Role+"_switch", AxisGPUSwitch, f.Key); err != nil {
				return err
			}
		}
		for _, p := range f.Ports {
			if err := add(p.Key, f.Role+"_port", AxisGPUPort, p.DeviceKey); err != nil {
				return err
			}
		}
	}
	for _, st := range t.StorageClusters {
		if err := add(st.Key, "storage", AxisGPUStorage); err != nil {
			return err
		}
		for _, d := range st.Devices {
			if err := add(d.Key, d.Kind, AxisGPUDevice, st.Key); err != nil {
				return err
			}
		}
	}
	for _, d := range t.devices {
		axis := AxisGPUDevice
		if d.Kind == "nvswitch" {
			axis = AxisNVLinkSwitch
		}
		if err := add(d.Key, d.Kind, axis, d.TrayKey, d.RackKey); err != nil {
			return err
		}
	}
	for _, d := range t.Domains {
		if err := add(d.Key, "domain", AxisNVLinkDomain, d.RackKey); err != nil {
			return err
		}
		for _, p := range d.Partitions {
			if err := add(p.Key, "partition", AxisNVLinkPartition, d.Key); err != nil {
				return err
			}
		}
		for _, p := range d.Ports {
			if err := add(p.Key, "nvport", AxisNVLinkPort, p.DeviceKey); err != nil {
				return err
			}
		}
	}
	for _, s := range t.Schedulers {
		if err := add(s.Key, "scheduler", AxisGPUScheduler); err != nil {
			return err
		}
		for _, p := range s.Projects {
			if err := add(p.Key, "project", AxisGPUProject, s.Key); err != nil {
				return err
			}
		}
		for _, w := range s.Workloads {
			if err := add(w.Key, "workload", AxisGPUWorkload, "project:"+s.Name+"/"+w.Project); err != nil {
				return err
			}
		}
	}
	sort.Slice(t.Targets, func(i, j int) bool { return t.Targets[i].Key < t.Targets[j].Key })
	return nil
}
func gpuModeTarget(t *GPUTopology, mode string, x GPUTarget) bool {
	var contract *GPUFailureContract
	for i := range gpuContracts {
		if gpuContracts[i].Name == mode {
			contract = &gpuContracts[i]
		}
	}
	if contract == nil || !slices.Contains(contract.Axes, x.Axis) {
		return false
	}
	switch mode {
	case "gpu_xid_burst", "gpu_fallen_off_bus", "gpu_node_drain":
		if x.Axis == AxisGPUTray {
			return x.Kind == "compute_tray"
		}
	case "gpu_backend_congestion":
		return strings.HasPrefix(x.Kind, "backend_")
	case "gpu_cooling_fault":
		if x.Axis == AxisGPUDevice {
			d, ok := gpuDevice(t, x.Key)
			if !ok || d.Kind != "cdu" {
				return false
			}
			r, _ := t.Target(d.RackKey)
			return r.Kind == "liquid_rack"
		}
	case "gpu_leak_detected":
		if x.Axis == AxisGPURack {
			return x.Kind == "liquid_rack"
		}
		d, ok := gpuDevice(t, x.Key)
		if !ok || d.Kind != "cdu" {
			return false
		}
		r, _ := t.Target(d.RackKey)
		return r.Kind == "liquid_rack"
	case "gpu_nvswitch_tray_failure":
		return x.Kind == "nvswitch_tray"
	case "gpu_nvlink_error_burst": // Every error target must reach a confirmed physical edge.
		for _, d := range t.Domains {
			for _, l := range d.Links {
				if gpuEdgeAffected(t, l, x.Key) {
					return true
				}
			}
		}
		return false
	}
	return true
}
func gpuEdgeAffected(t *GPUTopology, l NVLinkLink, target string) bool {
	return l.GPUKey == target || l.SwitchPortKey == target || slices.Contains(t.GPUParentTargets(l.GPUKey), target) || slices.Contains(t.GPUParentTargets(l.SwitchPortKey), target)
}
func gpuWorkloadTarget(s GPUSchedulerPlan, w GPUWorkloadPlan, target string) bool {
	return target == s.Key || target == w.Key || target == "project:"+s.Name+"/"+w.Project
}
func gpuAffectsGPU(t *GPUTopology, mode, target string, g GPU) bool {
	if mode == "gpu_backend_congestion" {
		n, _ := t.Node(g.NodeKey)
		for _, nic := range n.NICs {
			for _, f := range t.Fabrics {
				if f.Key == nic.FabricKey && f.Role == "backend" && (target == nic.FabricKey || target == nic.SwitchKey || target == nic.PortKey) {
					return true
				}
			}
		}
		return false
	}
	if mode == "gpu_storage_latency" {
		n, _ := t.Node(g.NodeKey)
		return n.StorageClient != nil && n.StorageClient.StorageClusterKey == target
	}
	if mode == "gpu_preemption_storm" || mode == "gpu_quota_exhaustion" {
		for _, s := range t.Schedulers {
			for _, w := range s.Workloads {
				if gpuWorkloadTarget(s, w, target) {
					for _, worker := range w.Workers {
						if slices.Contains(worker.GPUKeys, g.Key) {
							return true
						}
					}
				}
			}
		}
		return false
	}
	if mode == "gpu_cooling_fault" || mode == "gpu_leak_detected" {
		if d, ok := gpuDevice(t, target); ok && d.Kind == "cdu" {
			target = d.RackKey
		}
	}
	if mode == "gpu_nvswitch_tray_failure" {
		x, _ := t.Target(target)
		for _, d := range t.Domains {
			if slices.Contains(x.ParentKeys, d.Key) {
				return slices.Contains(d.GPUKeys, g.Key)
			}
		}
		return false
	}
	if mode == "gpu_nvlink_error_burst" {
		for _, l := range t.NVLinkForGPU(g.Key) {
			if gpuEdgeAffected(t, l, target) {
				return true
			}
		}
		return false
	}
	return target == g.Key || slices.Contains(t.GPUParentTargets(g.Key), target)
}
func (s *GPUSelection) CoversTarget(kind, mode, key string) bool {
	if s == nil || s.Topology == nil {
		return false
	}
	assigned := false
	for _, c := range gpuContracts {
		if c.Name == mode && slices.Contains(c.Consumers, kind) {
			assigned = true
		}
	}
	x, ok := s.Topology.Target(key)
	if !assigned || !ok || !gpuModeTarget(s.Topology, mode, x) {
		return false
	}
	if kind == "spectrumx" {
		for _, f := range s.Fabrics() {
			if x.Key == f.Key {
				return true
			}
			for _, d := range f.Devices {
				if key == d.Key {
					return true
				}
			}
			for _, p := range f.Ports {
				if key == p.Key {
					return true
				}
			}
		}
		return false
	}
	if kind == "snmp_exporter" || kind == "rack_facility" {
		for _, d := range s.Devices() {
			if d.Collection != nil && (key == d.Key || key == d.RackKey) {
				return true
			}
		}
		return false
	}
	if kind == "nvlink" {
		for _, d := range s.Domains() {
			if mode == "gpu_nvlink_error_burst" {
				for _, l := range d.Links {
					if gpuEdgeAffected(s.Topology, l, key) {
						return true
					}
				}
			} else if key == d.Key || key == d.RackKey || slices.Contains(d.SwitchKeys, key) {
				return true
			} else {
				for _, p := range d.Partitions {
					if p.Key == key {
						return true
					}
				}
				for _, sw := range d.SwitchKeys {
					if slices.Contains(s.Topology.GPUParentTargets(sw), key) {
						return true
					}
				}
			}
		}
		return false
	}
	for _, n := range s.Nodes() {
		if (kind == "gpuoperator" || kind == "networkoperator" || kind == "k8s_cluster") && n.KubernetesCluster == "" {
			continue
		}
		if kind == "host" && n.KubernetesCluster != "" {
			continue
		}
		for _, g := range n.GPUs {
			if gpuAffectsGPU(s.Topology, mode, key, g) {
				return true
			}
		}
	}
	return false
}
func (s *GPUSelection) Targets(kind string) []GPUTarget {
	var out []GPUTarget
	if s == nil || s.Topology == nil {
		return out
	}
	for _, x := range s.Topology.Targets {
		for _, c := range gpuContracts {
			if s.CoversTarget(kind, c.Name, x.Key) {
				out = append(out, x)
				break
			}
		}
	}
	return out
}
func GPUFaults(t *GPUTopology, at time.Time, eval GPUFailureEval) []GPUFault {
	var out []GPUFault
	if t == nil || eval == nil {
		return out
	}
	for _, c := range gpuContracts {
		for _, x := range t.Targets {
			if !gpuModeTarget(t, c.Name, x) {
				continue
			}
			active, i := eval(at, c.Name, x.Key)
			if active && gpuFinite(i) && i > 0 {
				out = append(out, GPUFault{Mode: c.Name, Target: x.Key, Intensity: math.Min(1, i)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Mode == out[j].Mode {
			return out[i].Target < out[j].Target
		}
		return out[i].Mode < out[j].Mode
	})
	return out
}
