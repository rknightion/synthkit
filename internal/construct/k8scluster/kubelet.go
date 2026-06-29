// SPDX-License-Identifier: AGPL-3.0-only

// kubelet.go — kubelet + kubelet-resource emission for k8scluster.
// job="integrations/kubernetes/kubelet" + job="integrations/kubernetes/resources" +
// job="integrations/kubernetes/probes".
// The 22-name allow-list from the extract is emitted exactly; plus 4 additional families
// confirmed present on a live reference cluster capture (the live-reference audit):
//   - kubernetes_build_info (per-node)
//   - volume_manager_total_volumes (per-node)
//   - storage_operation_duration_seconds_count (per-node, cumulative)
//   - prober_probe_total + prober_probe_duration_seconds_{bucket,count,sum} (pod-scoped)
package k8scluster

import (
	"fmt"
	"strings"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
	statelib "github.com/rknightion/synthkit/internal/state"
)

// jobProbes is the scrape job for kubelet prober metrics.
const jobProbes = "integrations/kubernetes/probes"

// proberHistoBounds are the default seconds histogram bounds for prober_probe_duration_seconds.
var proberHistoBounds = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// storageOps are the storage operation_name values emitted by the EBS CSI driver on a live cluster
// (live reference recon 2026-06-16). volume_attach is a legacy in-tree op absent from the CSI path.
var storageOps = []string{"volume_mount", "volume_unmount", "unmount_device", "verify_controller_attached_volume", "volume_apply_access_control"}

// probeTypes are the three probe types present on real kubelet prober series.
var probeTypes = []string{"readiness", "liveness", "startup"}

// volPluginStates are the two state values for volume_manager_total_volumes.
var volPluginStates = []string{"actual_state_of_world", "desired_state_of_world"}

// cgroupManagerOps are the operation_type values on kubelet_cgroup_manager_duration_seconds
// (the live-reference audit §kubelet_cgroup_manager_*).
var cgroupManagerOps = []string{"create", "update", "destroy"}

// podWorkerOps are the operation_type values on kubelet_pod_worker_duration_seconds
// (the live-reference audit §kubelet_pod_worker_*).
var podWorkerOps = []string{"sync", "terminating"}

// containerStates are the container_state values on kubelet_running_containers
// (the live-reference audit §kubelet_running_containers).
var containerStates = []string{"running", "created", "exited"}

// kubeletVersionInfo derives major, minor, goVersion, buildDate, gitCommit from a KubeletVersion
// string (e.g. "v1.31.2-eks-f69f56f"). Returns plausible synthetic constants for build metadata.
func kubeletVersionInfo(kv string) (major, minor, goVersion, buildDate, gitCommit string) {
	// strip leading "v"
	v := strings.TrimPrefix(kv, "v")
	// parse major/minor from "1.31.2-eks-f69f56f" → "1", "31"
	parts := strings.SplitN(v, ".", 3)
	if len(parts) >= 2 {
		major = parts[0]
		// minor may have a suffix like "31" or "31+" — strip non-digit suffix
		minorRaw := parts[1]
		minor = minorRaw
	}
	// Derive a deterministic git commit from the version string (last "-" segment, else hash).
	if idx := strings.LastIndex(kv, "-"); idx >= 0 {
		gitCommit = kv[idx+1:]
	} else {
		gitCommit = fmt.Sprintf("%07x", fnv1a32(kv))
	}
	buildDate = "2024-11-15T00:00:00Z"
	goVersion = "go1.22.8"
	return
}

// fnv1a32 returns a simple FNV-1a 32-bit hash of s (for deterministic synthetic values).
func fnv1a32(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// kubeletAllowList22 is the exact 22-name allow-list from the extract §2.3.
var kubeletAllowList22 = []string{
	"kubelet_certificate_manager_server_ttl_seconds",
	"kubelet_cgroup_manager_duration_seconds_bucket",
	"kubelet_cgroup_manager_duration_seconds_count",
	"kubelet_node_name",
	"kubelet_pleg_relist_duration_seconds_bucket",
	"kubelet_pleg_relist_duration_seconds_count",
	"kubelet_pleg_relist_interval_seconds_bucket",
	"kubelet_pod_start_duration_seconds_bucket",
	"kubelet_pod_start_duration_seconds_count",
	"kubelet_pod_worker_duration_seconds_bucket",
	"kubelet_pod_worker_duration_seconds_count",
	"kubelet_running_containers",
	"kubelet_running_pods",
	"kubelet_runtime_operations_errors_total",
	"kubelet_runtime_operations_total",
	"kubelet_server_expiration_renew_errors",
	"kubelet_volume_stats_available_bytes",
	"kubelet_volume_stats_capacity_bytes",
	"kubelet_volume_stats_inodes",
	"kubelet_volume_stats_inodes_free",
	"kubelet_volume_stats_inodes_used",
	"kubelet_volume_stats_used_bytes",
}

func emitKubelet(
	st *state.State,
	cluster string,
	cl *fixture.Cluster,
	nodes []fixture.Node,
	replicas int,
	tickSec, scale float64,
	w *core.World,
) {
	totalAppPods := len(workloadDeployments(cl)) * replicas
	perNode := totalAppPods / len(nodes)
	if perNode < 1 {
		perNode = 1
	}

	vols := volumesForCluster(cl)
	plat := platformOr(cl.Platform)

	for ni, n := range nodes {
		node := n.Hostname
		kubBase := kubeletLabels(cluster, node)

		totalPodsThisNode := perNode + (ni % 2)

		// kubelet_running_pods (in allow-list)
		st.Set("kubelet_running_pods", merge(kubBase, map[string]string{"node": node}), float64(totalPodsThisNode))
		// kubelet_running_containers (in allow-list) — fanned over container_state
		// (the live-reference audit §kubelet_running_containers). Steady-state weight: most running.
		for _, cs := range containerStates {
			val := 0.0
			switch cs {
			case "running":
				val = float64(totalPodsThisNode + 2)
			case "created":
				val = 1
			case "exited":
				val = 2
			}
			st.Set("kubelet_running_containers", merge(kubBase, map[string]string{
				"node": node, "container_state": cs,
			}), val)
		}
		// kubelet_node_name (in allow-list) — node hostname carried in exported_node label
		// (the live-reference audit §kubelet_node_name).
		st.Set("kubelet_node_name", merge(kubBase, map[string]string{
			"node": node, "exported_node": node,
		}), 1)
		// kubelet_server_expiration_renew_errors (in allow-list, counter)
		st.Add("kubelet_server_expiration_renew_errors", kubBase, 0)
		// kubelet_certificate_manager_server_ttl_seconds (in allow-list)
		st.Set("kubelet_certificate_manager_server_ttl_seconds", kubBase, 7*86400)

		// kubelet_runtime_operations_total + _errors_total (in allow-list, counters)
		for _, opType := range runtimeOps {
			opLbls := merge(kubBase, map[string]string{
				"node":           node,
				"operation_type": opType,
			})
			st.Add("kubelet_runtime_operations_total", opLbls, float64(1+w.Shape.IntN(5))*scale)
			st.Add("kubelet_runtime_operations_errors_total", opLbls, 0)
		}

		// Histograms. cgroup_manager and pod_worker carry operation_type and are fanned over
		// their real operation_type values (the live-reference audit §kubelet_cgroup_manager_*
		// / §kubelet_pod_worker_*); the other three carry only le.
		hb := kubeletHistoBounds
		for _, h := range []struct {
			name string
			mean float64
		}{
			{"kubelet_pleg_relist_duration_seconds", 0.04},
			{"kubelet_pleg_relist_interval_seconds", 1.0},
			{"kubelet_pod_start_duration_seconds", 0.5},
		} {
			samples := 3 + w.Shape.IntN(5)
			for s := 0; s < samples; s++ {
				st.Observe(h.name, kubBase, hb, statelib.LEBare, h.mean*(0.5+w.Shape.Float64()))
			}
		}
		// operation_type-fanned histograms.
		for _, h := range []struct {
			name string
			mean float64
			ops  []string
		}{
			{"kubelet_cgroup_manager_duration_seconds", 0.01, cgroupManagerOps},
			{"kubelet_pod_worker_duration_seconds", 0.3, podWorkerOps},
		} {
			for _, op := range h.ops {
				opLbls := merge(kubBase, map[string]string{"operation_type": op})
				samples := 3 + w.Shape.IntN(5)
				for s := 0; s < samples; s++ {
					st.Observe(h.name, opLbls, hb, statelib.LEBare, h.mean*(0.5+w.Shape.Float64()))
				}
			}
		}

		// kubelet_volume_stats_* (in allow-list: capacity, used, available, inodes, inodes_free, inodes_used)
		for vi, vol := range vols {
			volNodeIdx := vi % len(nodes)
			if volNodeIdx != ni {
				continue // bind each volume to one node
			}
			volLbls := merge(kubBase, map[string]string{
				"node":                  node,
				"namespace":             vol.ns,
				"persistentvolumeclaim": vol.pvc,
			})
			// Capacity comes from the shared volEntry so kubelet_volume_stats_capacity_bytes
			// agrees with kube_persistentvolumeclaim_resource_requests_storage_bytes (same volume).
			capacity := vol.capacityBytes
			used := capacity * (0.3 + w.Shape.Float64()*0.4)
			avail := capacity - used
			if avail < 0 {
				avail = 0
			}
			inodes := 6_553_600.0
			inodesUsed := inodes * (used / capacity) * 0.3
			st.Set("kubelet_volume_stats_capacity_bytes", volLbls, capacity)
			st.Set("kubelet_volume_stats_used_bytes", volLbls, used)
			st.Set("kubelet_volume_stats_available_bytes", volLbls, avail)
			st.Set("kubelet_volume_stats_inodes", volLbls, inodes)
			st.Set("kubelet_volume_stats_inodes_used", volLbls, inodesUsed)
			st.Set("kubelet_volume_stats_inodes_free", volLbls, inodes-inodesUsed)
		}

		// ── New families: confirmed on a live reference cluster (the live-reference audit) ──

		// kubernetes_build_info (cardinality 3 — one per node)
		emitKubernetesBuildInfo(st, kubBase, n, plat)

		// volume_manager_total_volumes (per node)
		emitVolumeManagerTotalVolumes(st, kubBase)

		// storage_operation_duration_seconds_count (per node, cumulative)
		emitStorageOperationDuration(st, kubBase, scale, w)
	}

	// prober_* (pod-scoped) — gated by KubeletProbes; off by default.
	if cl.K8sMonitoring.ControlPlane.KubeletProbes {
		emitProberMetrics(st, cluster, cl, nodes, replicas, w)
	}
}

// emitKubernetesBuildInfo emits kubernetes_build_info (value 1) per node.
// Labels: build_date, compiler="gc", git_commit, git_tree_state="clean",
// git_version (= kubelet version), go_version, major, minor, platform="linux/<arch>".
// Source: the live-reference audit §kubernetes_build_info.
func emitKubernetesBuildInfo(st *state.State, kubBase map[string]string, n fixture.Node, plat fixture.Platform) {
	major, minor, goVersion, buildDate, gitCommit := kubeletVersionInfo(plat.KubeletVersion)
	arch := fixture.LookupInstanceSpec(n.InstanceType).KubeArch()
	lbls := merge(kubBase, map[string]string{
		"build_date":     buildDate,
		"compiler":       "gc",
		"git_commit":     gitCommit,
		"git_tree_state": "clean",
		"git_version":    plat.KubeletVersion,
		"go_version":     goVersion,
		"major":          major,
		"minor":          minor,
		"platform":       "linux/" + arch,
	})
	st.Set("kubernetes_build_info", lbls, 1)
}

// emitVolumeManagerTotalVolumes emits volume_manager_total_volumes per node with
// plugin_name="kubernetes.io/csi" and state ∈ {actual_state_of_world, desired_state_of_world}.
// Source: the live-reference audit §volume_manager_total_volumes.
func emitVolumeManagerTotalVolumes(st *state.State, kubBase map[string]string) {
	for _, volState := range volPluginStates {
		lbls := merge(kubBase, map[string]string{
			"plugin_name": "kubernetes.io/csi",
			"state":       volState,
		})
		// Small int: actual ≤ desired (2 actual, 3 desired — realistic baseline)
		val := 2.0
		if volState == "desired_state_of_world" {
			val = 3.0
		}
		st.Set("volume_manager_total_volumes", lbls, val)
	}
}

// emitStorageOperationDuration emits storage_operation_duration_seconds_count per node,
// cumulative counter. Labels: operation_name, status="success", volume_plugin="kubernetes.io/csi",
// migrated="false". Source: the live-reference audit §storage_operation_duration_seconds_count.
func emitStorageOperationDuration(st *state.State, kubBase map[string]string, scale float64, w *core.World) {
	for _, op := range storageOps {
		lbls := merge(kubBase, map[string]string{
			"operation_name": op,
			"status":         "success",
			"volume_plugin":  "kubernetes.io/csi",
			"migrated":       "false",
		})
		st.Add("storage_operation_duration_seconds_count", lbls, float64(1+w.Shape.IntN(3))*scale)
	}
}

// emitProberMetrics emits the prober_* family under job="integrations/kubernetes/probes".
// Families: prober_probe_total (counter) + prober_probe_duration_seconds_{bucket,count,sum}
// (histogram). Labels per the live-reference audit §prober_probe_total / §prober_probe_duration_seconds.
// Iterates the cluster's declared workload pods (same pod iteration as KSM/cAdvisor), capped
// to the declared replica count to bound cardinality.
func emitProberMetrics(
	st *state.State,
	cluster string,
	cl *fixture.Cluster,
	nodes []fixture.Node,
	replicas int,
	w *core.World,
) {
	wlByName := podWorkloadByName(cl)

	for ns, deploys := range workloadDeployments(cl) {
		for _, deploy := range deploys {
			fwl := wlByName[deploy]
			reps := substrateReps(fwl, replicas, len(nodes))
			container := deploy
			if fwl != nil && fwl.Container != "" {
				container = fwl.Container
			}

			for ri := 0; ri < reps; ri++ {
				var pod string
				if fwl != nil && ri < len(fwl.PodNames) {
					pod = fwl.PodNames[ri]
				} else {
					pod = synthPodName(deploy, ri)
				}
				uid := podUID(cluster, ns, pod)

				probeBase := merge(k8sBase(cluster), map[string]string{
					"job":      jobProbes,
					"instance": nodes[ri%len(nodes)].Hostname,
				})

				for _, pt := range probeTypes {
					podLbls := merge(probeBase, map[string]string{
						"container":  container,
						"namespace":  ns,
						"pod":        pod,
						"probe_type": pt,
					})

					// prober_probe_total (counter): + pod_uid + result
					totalLbls := merge(podLbls, map[string]string{
						"pod_uid": uid,
						"result":  "successful",
					})
					st.Add("prober_probe_total", totalLbls, float64(1+w.Shape.IntN(3)))

					// prober_probe_duration_seconds histogram (bucket + count + sum)
					st.Observe("prober_probe_duration_seconds", podLbls, proberHistoBounds, statelib.LEBare, 0.02*(0.5+w.Shape.Float64()))
				}
			}
		}
	}
}

// emitKubeletResources emits node_cpu_usage_seconds_total and node_memory_working_set_bytes
// under job="integrations/kubernetes/resources" (extract §3.5).
func emitKubeletResources(
	st *state.State,
	cluster string,
	nodes []fixture.Node,
	factor, tickSec float64,
) {
	for ni, n := range nodes {
		node := n.Hostname
		resBase := merge(k8sBase(cluster), map[string]string{
			"job":      jobKubeletResource,
			"instance": node,
			"node":     node,
		})
		busyFrac := nodeCPUPercent(ni, factor) / 100.0
		vcpus := float64(vcpusForNode(n))
		st.Add("node_cpu_usage_seconds_total", resBase, tickSec*busyFrac*vcpus*0.95)

		memTotal := memBytesForNode(n)
		memAvail := (4 + (1-factor)*8) * 1024 * 1024 * 1024
		ws := memTotal - memAvail
		if ws < 256*1024*1024 {
			ws = 256 * 1024 * 1024
		}
		st.Set("node_memory_working_set_bytes", resBase, ws)
	}
}
