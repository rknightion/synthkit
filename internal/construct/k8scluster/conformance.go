// SPDX-License-Identifier: AGPL-3.0-only

// conformance.go — k8s-monitoring conformance / discovery series (I22).
// Gated on fx.Cluster.K8sMonitoring.Enabled. Emits:
//   - grafana_kubernetes_monitoring_build_info (chart version)
//   - alloy_build_info (version = AlloyVersion verbatim — MUST start with "v")
//   - opencost_build_info + full OpenCost series set
//   - kepler_exporter_build_info + Kepler series set
package k8scluster

import (
	"fmt"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	opencostVersion  = "1.120.3"
	opencostRevision = "85c87a3"
	keplerVersion    = "release-0.7.12"
	keplerRevision   = "a1b2c3d"

	opencostCPUHourlyCost = 0.067
	opencostRAMHourlyCost = 0.0085
)

func emitConformance(
	st *state.State,
	cluster string,
	cl *fixture.Cluster,
	nodes []fixture.Node,
	replicas int,
	tickSec, scale float64,
	km fixture.K8sMonitoring,
	w *core.World,
) {
	// ── grafana_kubernetes_monitoring_build_info ────────────────────────────────
	st.Set("grafana_kubernetes_monitoring_build_info", merge(k8sBase(cluster), map[string]string{
		"job":       jobK8sMonitoringTelemetry,
		"instance":  "grafana-k8s-monitoring",
		"namespace": "monitoring",
		"version":   km.ChartVersion,
	}), 1)

	// ── alloy_build_info ────────────────────────────────────────────────────────
	if km.Alloy {
		// AlloyVersion is canonical "v"-prefixed form (I22). Emit verbatim.
		alloyInstance := "10.1.30.200:12345"
		if len(nodes) > 0 {
			// Use a stable synthetic pod IP for alloy (first node subnet)
			alloyInstance = "10.0.1.100:12345"
		}
		st.Set("alloy_build_info", merge(k8sBase(cluster), map[string]string{
			"job":       jobAlloy,
			"instance":  alloyInstance,
			"namespace": "monitoring",
			"version":   km.AlloyVersion, // VERBATIM — must start with "v"
			"revision":  "1e2007e",
			"goversion": "go1.26.3",
		}), 1)
	}

	// ── OpenCost ────────────────────────────────────────────────────────────────
	if km.OpenCost {
		emitOpenCost(st, cluster, cl, nodes, replicas, tickSec, scale, w)
	}

	// ── Kepler ──────────────────────────────────────────────────────────────────
	if km.Kepler {
		emitKepler(st, cluster, cl, nodes, replicas, tickSec, scale, w)
	}
}

func emitOpenCost(
	st *state.State,
	cluster string,
	cl *fixture.Cluster,
	nodes []fixture.Node,
	replicas int,
	tickSec, scale float64,
	w *core.World,
) {
	if len(nodes) == 0 {
		return
	}
	_ = scale
	region := cl.Cloud.Region

	opencostInstance := nodes[0].Hostname
	ocBase := func() map[string]string {
		return merge(k8sBase(cluster), map[string]string{
			"job":      jobOpenCost,
			"instance": opencostInstance,
		})
	}

	st.Set("opencost_build_info", merge(ocBase(), map[string]string{
		"version": opencostVersion, "revision": opencostRevision,
	}), 1)

	// the live-reference audit §kubecost_cluster_info: 12 label keys. The 6 boolean-string
	// feature flags + version (= opencost version) join the 6 identity keys.
	st.Set("kubecost_cluster_info", merge(ocBase(), map[string]string{
		"provider": "AWS", "provisioner": "EKS",
		"region": region, "id": cluster, "name": cluster, "clusterprofile": "development",
		"errorreporting":    "true",
		"logcollection":     "true",
		"productanalytics":  "true",
		"remotereadenabled": "false",
		"valuesreporting":   "true",
		"version":           opencostVersion,
	}), 1)

	// enriched: provisioner_name=EKS matches live reference cluster (audit finding #4).
	st.Set("kubecost_cluster_management_cost", merge(ocBase(), map[string]string{
		"provisioner_name": "EKS",
	}), 0.10)

	totalMem := 0.0
	for _, n := range nodes {
		totalMem += memBytesForNode(n)
	}
	factor := 0.5
	if cl.Env != nil {
		// approximate factor without shape engine
		factor = cl.Env.Weight * 0.5
	}
	st.Set("kubecost_cluster_memory_working_set_bytes", ocBase(), totalMem*0.6*(0.5+factor*0.5))

	// enriched: handler label added (/metrics and /healthz alternate per live reference cluster).
	// The series is a counter so we emit two label-sets (one per handler), each accumulating
	// half the rate.
	for _, handler := range []string{"/metrics", "/healthz"} {
		st.Add("kubecost_http_requests_total", merge(ocBase(), map[string]string{
			"code": "200", "method": "GET", "handler": handler,
		}), tickSec/60*3.5) // ≈7 req/min total across both handlers
	}

	// enriched: ingress_ip, namespace, service_name, uid (audit finding — LB row).
	// Deterministic from the cluster name so the same blueprint always produces the same series.
	lbUID := podUID(cluster, "envoy-gateway-system", cluster+"-lb")
	lbIP := lbIngressIP(cluster)
	st.Set("kubecost_load_balancer_cost", merge(ocBase(), map[string]string{
		"namespace":    "envoy-gateway-system",
		"service_name": cluster + "-lb",
		"ingress_ip":   lbIP,
		"uid":          lbUID,
	}), 0.025)

	st.Set("kubecost_network_internet_egress_cost", ocBase(), 0.005)
	st.Set("kubecost_network_region_egress_cost", ocBase(), 0.001)
	st.Set("kubecost_network_zone_egress_cost", ocBase(), 0.0001)

	// enriched: node-cost series now carry arch, instance_type, provider_id, region, uid.
	for i, n := range nodes {
		node := n.Hostname
		uid := n.UID
		if uid == "" {
			// Graceful fallback for test fixtures that don't set UID (deterministic).
			uid = podUID(cluster, "node", node)
		}
		nodeLbls := merge(ocBase(), map[string]string{
			"node":          node,
			"instance_type": n.InstanceType,
			"arch":          fixture.LookupInstanceSpec(n.InstanceType).KubeArch(),
			"provider_id":   nodeProviderID(n, region, i),
			"region":        region,
			"uid":           uid,
		})
		cpuHr := opencostCPUHourlyCost * float64(vcpusForNode(n))
		ramHr := opencostRAMHourlyCost * memBytesForNode(n) / (1024 * 1024 * 1024)
		st.Set("node_cpu_hourly_cost", nodeLbls, cpuHr)
		st.Set("node_ram_hourly_cost", nodeLbls, ramHr)
		st.Set("node_total_hourly_cost", nodeLbls, cpuHr+ramHr)
		st.Set("node_gpu_count", nodeLbls, 0)
		st.Set("node_gpu_hourly_cost", nodeLbls, 0)
		st.Set("kubecost_node_is_spot", nodeLbls, 0)
	}

	ocWlByName := podWorkloadByName(cl)
	for ns, deploys := range workloadDeployments(cl) {
		for _, deploy := range deploys {
			fwl := ocWlByName[deploy]
			reps := substrateReps(fwl, replicas, len(nodes))
			container := deploy
			if fwl != nil && fwl.Container != "" {
				container = fwl.Container
			}
			for ri := 0; ri < reps; ri++ {
				var pod string
				var nodeIdx int
				if fwl != nil && ri < len(fwl.PodNames) {
					pod = fwl.PodNames[ri]
				} else {
					pod = synthPodName(deploy, ri)
				}
				if fwl != nil && ri < len(fwl.NodeIdx) {
					nodeIdx = fwl.NodeIdx[ri]
				} else {
					nodeIdx = nodeAssignment(ns, deploy, ri, len(nodes))
				}
				if nodeIdx >= len(nodes) {
					nodeIdx = 0
				}
				node := nodes[nodeIdx].Hostname
				// enriched: uid matches the pod uid that KSM emits (same derivation).
				uid := podUID(cluster, ns, pod)
				allocLbls := merge(ocBase(), map[string]string{
					"namespace": ns, "pod": pod, "container": container, "node": node,
					"uid": uid,
				})
				// OpenCost allocation must track the SAME per-workload request basis as the KSM
				// kube_pod_container_resource_requests series (resolveCPURequest/resolveMemRequest),
				// so the efficiency dashboard's CPU/memory-waste panels stay internally consistent
				// (and honor any blueprint resources: override). Default basis is unchanged when the
				// workload has no override and is in the default size class.
				st.Set("container_cpu_allocation", allocLbls, resolveCPURequest(fwl, deploy))
				st.Set("container_memory_allocation_bytes", allocLbls, resolveMemRequest(fwl, deploy))
				st.Set("container_gpu_allocation", allocLbls, 0)
			}

			// enriched: uid + representative label_* bag (audit finding).
			deployUID := podUID(cluster, ns, deploy+"-deploy")
			st.Set("deployment_match_labels", merge(ocBase(), map[string]string{
				"namespace": ns, "deployment": deploy,
				"uid":                               deployUID,
				"label_app_kubernetes_io_name":      deploy,
				"label_app_kubernetes_io_component": "app",
			}), 1)

			svcUID := podUID(cluster, ns, deploy+"-svc")
			st.Set("service_selector_labels", merge(ocBase(), map[string]string{
				"namespace": ns, "service": deploy,
				"uid":                               svcUID,
				"label_app_kubernetes_io_name":      deploy,
				"label_app_kubernetes_io_component": "app",
			}), 1)
		}
	}

	// statefulSet_match_labels — new family (audit: present on real cluster, was missing).
	// One series per stateful workload (mirrors KSM's statefulSetsForCluster).
	for _, ss := range statefulSetsForCluster(cl, replicas) {
		ssUID := podUID(cluster, ss.ns, ss.name+"-ss")
		st.Set("statefulSet_match_labels", merge(ocBase(), map[string]string{
			"namespace":                         ss.ns,
			"statefulSet":                       ss.name,
			"uid":                               ssUID,
			"label_app_kubernetes_io_name":      ss.name,
			"label_app_kubernetes_io_component": "database",
		}), 1)
	}

	// ── PV / PVC cost (live-reference audit: pv_hourly_cost + pod_pvc_allocation) ──
	// Reads the SAME volumesForCluster source as KSM (emitKSMStorage) + kubelet_volume_stats, so
	// persistentvolume/persistentvolumeclaim names join across all three constructs.
	for _, vol := range volumesForCluster(cl) {
		gib := vol.capacityBytes / (1024 * 1024 * 1024)
		// pv_hourly_cost: provider_id is the backing EBS volume id (real OpenCost convention).
		st.Set("pv_hourly_cost", merge(ocBase(), map[string]string{
			"persistentvolume": vol.pv,
			"provider_id":      vol.volumeID,
			"volumename":       vol.pv,
			"uid":              vol.uid,
		}), 0.0001*gib)

		// pod_pvc_allocation: pod→PV→PVC join. uid = the PVC uid (matches KSM PVC identity).
		st.Set("pod_pvc_allocation", merge(ocBase(), map[string]string{
			"namespace":             vol.ns,
			"persistentvolume":      vol.pv,
			"persistentvolumeclaim": vol.pvc,
			"pod":                   vol.pod,
			"uid":                   vol.uid,
		}), 1)
	}

	_ = w
}

// lbIngressIP returns a deterministic synthetic load-balancer ingress IP for a cluster.
// The IP is stable across ticks (same blueprint → same IP) and falls in the 100.64/10
// RFC 6598 shared-address space (carrier-grade NAT range) — the same range visible in the
// live reference cluster capture.
func lbIngressIP(cluster string) string {
	h := 0
	for _, c := range cluster {
		h = (h*31 + int(c)) & 0x7fffffff
	}
	return fmt.Sprintf("100.%d.%d.%d", 64+(h%60), (h>>8)&0xff, 1+((h>>16)&0xfe))
}

func emitKepler(
	st *state.State,
	cluster string,
	cl *fixture.Cluster,
	nodes []fixture.Node,
	replicas int,
	tickSec, scale float64,
	w *core.World,
) {
	_ = w
	for _, n := range nodes {
		node := n.Hostname
		kepBase := merge(k8sBase(cluster), map[string]string{
			"job":      jobKepler,
			"instance": node,
		})
		st.Set("kepler_exporter_build_info", merge(kepBase, map[string]string{
			"version": keplerVersion, "revision": keplerRevision,
		}), 1)

		pkgWatts := 50.0 + float64(len(node)%10)
		pkgJ := pkgWatts * tickSec
		st.Add("kepler_node_package_joules_total", kepBase, pkgJ*scale)
		st.Add("kepler_node_core_joules_total", kepBase, pkgJ*0.65*scale)
		st.Add("kepler_node_dram_joules_total", kepBase, pkgJ*0.20*scale)
		st.Add("kepler_node_platform_joules_total", kepBase, pkgJ*scale)
	}

	kepWlByName := podWorkloadByName(cl)
	for ns, deploys := range workloadDeployments(cl) {
		for _, deploy := range deploys {
			fwl := kepWlByName[deploy]
			reps := substrateReps(fwl, replicas, len(nodes))
			container := deploy
			if fwl != nil && fwl.Container != "" {
				container = fwl.Container
			}
			for ri := 0; ri < reps; ri++ {
				var pod string
				var nodeIdx int
				if fwl != nil && ri < len(fwl.PodNames) {
					pod = fwl.PodNames[ri]
				} else {
					pod = synthPodName(deploy, ri)
				}
				if fwl != nil && ri < len(fwl.NodeIdx) {
					nodeIdx = fwl.NodeIdx[ri]
				} else {
					nodeIdx = nodeAssignment(ns, deploy, ri, len(nodes))
				}
				if nodeIdx >= len(nodes) {
					nodeIdx = 0
				}
				node := nodes[nodeIdx].Hostname
				ctrLbls := merge(k8sBase(cluster), map[string]string{
					"job":       jobKepler,
					"instance":  node,
					"namespace": ns,
					"pod":       pod,
					"container": container,
					"node":      node,
				})
				// Power proxy scales with the workload's resolved memory limit (honors a blueprint
				// resources: override), keeping Kepler estimates consistent with the KSM limits.
				ctrWatts := resolveMemLimit(fwl, deploy) / (128 * 1024 * 1024) * 2.0
				ctrJ := ctrWatts * tickSec
				st.Add("kepler_container_joules_total", ctrLbls, ctrJ*scale)
				st.Add("kepler_container_core_joules_total", ctrLbls, ctrJ*0.7*scale)
			}
		}
	}
}
