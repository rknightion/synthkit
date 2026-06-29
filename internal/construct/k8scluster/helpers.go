// SPDX-License-Identifier: AGPL-3.0-only

// helpers.go — shared label-builder helpers and topology constants for the k8scluster
// package. No public API; all symbols used only within this package.
package k8scluster

import (
	"fmt"
	"strings"

	"github.com/rknightion/synthkit/internal/fixture"
)

// ── Job label constants (load-bearing for dashboard variable queries) ────────────

const (
	jobKSM                    = "integrations/kubernetes/kube-state-metrics"
	jobCAdvisor               = "integrations/kubernetes/cadvisor"
	jobKubelet                = "integrations/kubernetes/kubelet"
	jobKubeletResource        = "integrations/kubernetes/resources"
	jobNodeExporter           = "integrations/node_exporter" // ⚠ NO "kubernetes/" segment
	jobK8sMonitoringTelemetry = "integrations/kubernetes/kubernetes_monitoring_telemetry"
	jobAlloy                  = "integrations/alloy"
	jobOpenCost               = "integrations/opencost"
	jobKepler                 = "integrations/kepler"
)

// ksmInstance is the fixed KSM pod IP:port (stable synthetic — real pod IPs are dynamic).
const ksmInstance = "10.1.30.200:8080"

// k8sSource is required on ALL k8s-monitoring series.
const k8sSource = "kubernetes"

// clusterCreatedUnix is a frozen epoch for all *_created / *_start_time gauges (deterministic).
const clusterCreatedUnix = 1_748_736_000

// storageClass is the EBS-backed storage class for synthetic PVCs.
const storageClass = "gp3"

// cpuModes are the node-exporter cpu mode values (exact from the extract).
var cpuModes = []string{"idle", "iowait", "irq", "nice", "softirq", "steal", "system", "user"}

// nodeExporterDS is the DaemonSet name (and pod name prefix) for the node-exporter.
const nodeExporterDS = "grafana-k8s-monitoring-node-exporter"

// runtimeOps are the kubelet operation_type values.
var runtimeOps = []string{
	"container_start", "container_stop", "create_container", "pull_image", "remove_container",
}

// ── Label builders ────────────────────────────────────────────────────────────────

// k8sBase returns the three labels present on every k8s-monitoring series.
func k8sBase(cluster string) map[string]string {
	return map[string]string{
		"cluster":          cluster,
		"k8s_cluster_name": cluster,
		"source":           k8sSource,
	}
}

func ksmLabels(cluster string) map[string]string {
	m := k8sBase(cluster)
	m["job"] = jobKSM
	m["instance"] = ksmInstance
	return m
}

func cadvisorLabels(cluster, node string) map[string]string {
	m := k8sBase(cluster)
	m["job"] = jobCAdvisor
	m["instance"] = node
	return m
}

func kubeletLabels(cluster, node string) map[string]string {
	m := k8sBase(cluster)
	m["job"] = jobKubelet
	m["instance"] = node
	return m
}

// nodeExporterLabels returns the base labels for a node-exporter series, including the
// DaemonSet pod labels carried by Alloy relabelling (extract §2.4).
func nodeExporterLabels(cluster, node string, nodeIdx int) map[string]string {
	m := k8sBase(cluster)
	m["job"] = jobNodeExporter
	m["instance"] = node
	m["app"] = "node-exporter"
	m["component"] = "metrics"
	m["container"] = "node-exporter"
	m["pod"] = nodeExporterPodName(nodeIdx)
	m["namespace"] = "monitoring"
	m["workload"] = "DaemonSet/" + nodeExporterDS
	return m
}

// merge returns a new map containing all base labels plus extras (extras win on conflict).
func merge(base map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// ── Topology helpers ─────────────────────────────────────────────────────────────

// nodeExporterPodName returns a stable node-exporter DaemonSet pod name for node idx.
// Form: "grafana-k8s-monitoring-node-exporter-<suffix>".
func nodeExporterPodName(idx int) string {
	suffixes := []string{"w4q8q", "x9r2m", "k7p3n"}
	if idx < len(suffixes) {
		return nodeExporterDS + "-" + suffixes[idx]
	}
	return fmt.Sprintf("%s-%d", nodeExporterDS, idx)
}

// podUID returns a stable synthetic UID for a pod/object (bounded-deterministic, not a real UUID).
// The cluster is folded into the hash so UIDs are globally unique across clusters — real k8s assigns
// every object a distinct UUID even when names collide (e.g. a StatefulSet/addon "-0" pod has the
// same name in every cluster but a different UID). Deployment pod names are already cluster-distinct;
// this makes the UID derivation cluster-aware uniformly so an identity join keyed on uid never
// conflates two real pods from different clusters.
func podUID(cluster, ns, pod string) string {
	h := 0
	for _, c := range cluster + "/" + ns + pod {
		h = (h*31 + int(c)) & 0x7fffffff
	}
	return fmt.Sprintf("%08x-0000-4000-8000-%012x", h&0xffffffff, (h*0x1234567)&0xffffffffffff)
}

// replicaSetName derives the stable ReplicaSet name for a deployment.
func replicaSetName(deploy string) string {
	h := 0
	for _, c := range deploy {
		h = (h*31 + int(c)) & 0x7fffffff
	}
	return fmt.Sprintf("%s-%08x", deploy, h&0xffffffff)
}

// nodeAssignment returns a stable node index (0..len(nodes)-1) for a pod replica.
func nodeAssignment(ns, deploy string, ri, numNodes int) int {
	if numNodes <= 0 {
		return 0
	}
	h := 0
	for _, c := range ns + deploy {
		h = (h*31 + int(c)) & 0x7fffffff
	}
	return ((h + ri) & 0x7fffffff) % numNodes
}

// nodeInternalIP derives the internal IP from a "ip-10-x-y-z.<region>.compute.internal"
// hostname. Returns "" if the form does not match.
func nodeInternalIP(host string) string {
	core := host
	if i := strings.Index(core, "."); i >= 0 {
		core = core[:i]
	}
	core = strings.TrimPrefix(core, "ip-")
	return strings.ReplaceAll(core, "-", ".")
}

// imageRepo returns a container image repository string for a given deployment name.
// Uses the workload name directly.
func imageRepo(deploy string) string {
	return "ghcr.io/synthkit/" + deploy
}

// memoryForDeploy returns a realistic memory request in bytes for a deployment. Based
// on typical workload sizes.
func memoryForDeploy(deploy string) float64 {
	switch {
	case strings.HasSuffix(deploy, "-worker") || strings.HasSuffix(deploy, "-backend"):
		return 512 * 1024 * 1024
	case strings.HasSuffix(deploy, "-api") || strings.HasSuffix(deploy, "-gateway"):
		return 256 * 1024 * 1024
	default:
		return 128 * 1024 * 1024
	}
}

// ── Per-workload resource resolvers ───────────────────────────────────────────────
//
// PROBLEM these solve: every container used to carry a uniform 0.25-core request / 1.0-core
// limit and a usage that was independent of the request, so a live efficiency dashboard flagged
// EVERY container as CPU-over-requested. Real estates have a MIX. These resolvers give each
// workload a DETERMINISTIC size class (from the FNV hash of its deploy name, same approach as
// hex16/nodeAssignment/fnv32) so requests/limits and usage-vs-request vary realistically, while a
// blueprint-declared fixture.Workload.Resources block can pin any field exactly. A nil fixture
// (substrate pods: coredns/alloy/aws-node) is safe — defaults are keyed off the deploy name alone.

// cpuClass is one CPU size-class request/limit tuple (cores).
type cpuClass struct{ req, lim float64 }

// cpuClasses are the four deterministic CPU size classes, indexed by fnv32(deploy)%4.
var cpuClasses = []cpuClass{
	{0.1, 0.25}, // small
	{0.25, 0.5}, // medium
	{0.5, 1.0},  // large
	{1.0, 2.0},  // xlarge
}

// utilFracs are the three deterministic utilization targets (fraction of the CPU REQUEST that
// peak usage aims at), indexed by (fnv32(deploy)/4)%3: hot, normal, cold.
var utilFracs = []float64{0.75, 0.4, 0.15}

// sizeClass returns the deterministic CPU size class for a deploy name (fnv32(deploy)%4).
func sizeClass(deploy string) cpuClass {
	return cpuClasses[fnv32(deploy)%uint32(len(cpuClasses))]
}

// utilFrac returns the deterministic utilization target for a deploy name ((fnv32(deploy)/4)%3).
func utilFrac(deploy string) float64 {
	return utilFracs[(fnv32(deploy)/4)%uint32(len(utilFracs))]
}

// resolveCPURequest returns the workload's CPU request in cores: the blueprint override
// (fwl.Resources.CPURequest) if > 0, else the deterministic size-class request.
func resolveCPURequest(fwl *fixture.Workload, deploy string) float64 {
	if fwl != nil && fwl.Resources != nil && fwl.Resources.CPURequest > 0 {
		return fwl.Resources.CPURequest
	}
	return sizeClass(deploy).req
}

// resolveCPULimit returns the workload's CPU limit in cores: override if > 0, else size-class limit.
func resolveCPULimit(fwl *fixture.Workload, deploy string) float64 {
	if fwl != nil && fwl.Resources != nil && fwl.Resources.CPULimit > 0 {
		return fwl.Resources.CPULimit
	}
	return sizeClass(deploy).lim
}

// resolveMemRequest returns the workload's memory request in bytes: override if > 0, else
// memoryForDeploy(deploy)*0.5 (the existing request basis).
func resolveMemRequest(fwl *fixture.Workload, deploy string) float64 {
	if fwl != nil && fwl.Resources != nil && fwl.Resources.MemRequest > 0 {
		return fwl.Resources.MemRequest
	}
	return memoryForDeploy(deploy) * 0.5
}

// resolveMemLimit returns the workload's memory limit in bytes: override if > 0, else
// memoryForDeploy(deploy) (the existing limit basis).
func resolveMemLimit(fwl *fixture.Workload, deploy string) float64 {
	if fwl != nil && fwl.Resources != nil && fwl.Resources.MemLimit > 0 {
		return fwl.Resources.MemLimit
	}
	return memoryForDeploy(deploy)
}

// resolveCPUUsageBase returns the per-workload peak CPU usage target in cores: the override
// (fwl.Resources.CPUUsageBase) if > 0, else the resolved CPU request scaled by the workload's
// deterministic utilization fraction. The smallest combination (small req 0.1 * cold 0.15 = 0.015)
// stays above the cadvisor cpuDelta floor (0.005), so usage never clips for any workload.
func resolveCPUUsageBase(fwl *fixture.Workload, deploy string) float64 {
	if fwl != nil && fwl.Resources != nil && fwl.Resources.CPUUsageBase > 0 {
		return fwl.Resources.CPUUsageBase
	}
	return resolveCPURequest(fwl, deploy) * utilFrac(deploy)
}

// nodeProviderID returns the kube_node_info provider_id value for a Node, using the
// cluster's cloud region and cycling AZ by node index.
func nodeProviderID(n fixture.Node, region string, nodeIdx int) string {
	az := region + string(rune('a'+nodeIdx%3))
	return "aws:///" + az + "/" + n.InstanceID
}

// nodeCPUPercent returns the deterministic CPU utilisation model for a node (0–100).
// Mirrors the EC2 construct's model so the two lanes track together (I12).
func nodeCPUPercent(nodeIdx int, factor float64) float64 {
	pct := 22 + factor*50 + float64(nodeIdx%4)*4
	if pct < 1 {
		pct = 1
	}
	if pct > 99 {
		pct = 99
	}
	return pct
}

// vcpusForNode returns the vCPU count for a node by delegating to the embedded
// EC2 instance-type catalogue (fixture.LookupInstanceSpec). Returns the exact
// vCPU count for known types; falls back to a size-suffix ladder (e.g. 2 for
// any "*.large") and ultimately to 4 vCPU for completely unrecognised types.
func vcpusForNode(n fixture.Node) int {
	return fixture.LookupInstanceSpec(n.InstanceType).VCPU
}

// memBytesForNode returns the physical memory in bytes for a node by delegating
// to the embedded EC2 instance-type catalogue (fixture.LookupInstanceSpec).
// Returns the exact memory for known types; falls back to a size-suffix ladder
// (e.g. 8 GiB for any "*.large") and ultimately to 16 GiB for unrecognised types.
func memBytesForNode(n fixture.Node) float64 {
	return fixture.LookupInstanceSpec(n.InstanceType).MemBytes
}

// volEntry is the SINGLE shared PVC identity source for the cluster: kubelet_volume_stats
// (kubelet.go), the KSM PV/PVC family (ksm.go emitKSMStorage), and the OpenCost pv/pvc cost
// series (conformance.go emitOpenCost) all read it, so the persistentvolumeclaim /
// persistentvolume names join across the three constructs (this is the whole point).
type volEntry struct {
	ns, pvc string

	// PersistentVolume identity. pv is the bound PV name (form "pvc-<uuid>"), the value carried
	// on kube_persistentvolume_status_phase.persistentvolume, kube_persistentvolumeclaim_info.volumename,
	// and pv_hourly_cost.{persistentvolume,volumename}.
	pv string

	// capacityBytes is the requested/provisioned size — kube_persistentvolumeclaim_resource_requests_storage_bytes
	// and the kubelet_volume_stats_capacity_bytes value (so the two lanes agree).
	capacityBytes float64

	// storageClass is the PVC's storage class (kube_persistentvolumeclaim_info.storageclass).
	storageClass string

	// uid is the PVC's k8s UID (UUID form). volumeID is the backing EBS volume id (vol-…),
	// carried as pv_hourly_cost.provider_id (real OpenCost puts the EBS id there).
	uid      string
	volumeID string

	// pod is the owning pod name (the workload's first replica), used by
	// kube_pod_spec_volumes_persistentvolumeclaims_info and pod_pvc_allocation.
	pod string
}

// volSeed returns the deterministic, CLUSTER-SCOPED identity seed for a cluster's volumes. The
// blueprint Seed is folded with the cluster name so two clusters in one blueprint that declare the
// same workload + volume_claim get DISTINCT PV name / PVC uid / EBS volume id — real k8s/AWS assign
// every PVC/PV/EBS volume a globally-unique id (mirrors the cluster-folded podUID identity fix; this
// was the same class of collision one level down). The fallback (cluster name when Seed is unset) is
// already cluster-unique, so only the seeded branch needs folding; test fixtures stay deterministic.
func volSeed(cl *fixture.Cluster) string {
	if cl.Seed != "" {
		return cl.Seed + ":" + cl.Name
	}
	return cl.Name
}

// volumesForCluster returns the PVC volumes associated with a cluster's workloads, one per
// workload. Every identity (pv name, uid, backing EBS volume id, capacity) is derived
// deterministically from the cluster seed + pvc name so the join is stable across ticks and
// across the kubelet / KSM / OpenCost constructs.
func volumesForCluster(cl *fixture.Cluster) []volEntry {
	seed := volSeed(cl)
	var vols []volEntry
	for _, wl := range cl.Workloads {
		// PVCs are opt-in via declared VolumeClaims (stateless workloads have none). StatefulSet ⇒
		// one PVC per (template, ordinal) named "<vct>-<name>-<ordinal>", joined to pod "<name>-<n>".
		// Deployment ⇒ one shared PVC per template named "<vct>-<name>", joined to the first pod.
		if len(wl.VolumeClaims) == 0 {
			continue
		}
		isSTS := wlController(wl) == "statefulset"
		for _, vct := range wl.VolumeClaims {
			// StatefulSet ⇒ one PVC per ordinal (driven by Replicas, NOT len(PodNames) — PodNames may
			// not be populated yet when storage emits at resolve time); Deployment ⇒ one shared PVC.
			ordinals := 1
			if isSTS {
				ordinals = wl.Replicas
			}
			for ord := 0; ord < ordinals; ord++ {
				var pvc, pod string
				if isSTS {
					pvc = fmt.Sprintf("%s-%s-%d", vct, wl.Name, ord)
					if ord < len(wl.PodNames) {
						pod = wl.PodNames[ord]
					} else {
						pod = fixture.StatefulSetPodName(wl.Name, ord)
					}
				} else {
					pvc = fmt.Sprintf("%s-%s", vct, wl.Name)
					if len(wl.PodNames) > 0 {
						pod = wl.PodNames[0]
					} else {
						pod = synthPodName(wl.Name, 0)
					}
				}
				pv := "pvc-" + fixture.NodeUID(seed, "pv", wl.Namespace, pvc)
				vols = append(vols, volEntry{
					ns:            wl.Namespace,
					pvc:           pvc,
					pv:            pv,
					capacityBytes: 10 * 1024 * 1024 * 1024, // 10Gi requested
					storageClass:  storageClass,
					uid:           fixture.NodeUID(seed, "pvc", wl.Namespace, pvc),
					volumeID:      fixture.VolumeID(seed, "pv", wl.Namespace, pvc),
					pod:           pod,
				})
			}
		}
	}
	return vols
}

// kubeSystemDeployments returns the always-present EKS kube-system Deployments that are NOT
// supplied as real SubstrateWorkloads. coredns + metrics-server have MIGRATED to the addon
// catalog's BaselineWorkloads() (carried in cl.SubstrateWorkloads with resolver-minted PodNames +
// real Container), so they are no longer synthesized here — the pod-shape emitters pick them up
// from SubstrateWorkloads via podWorkloadByName, joining any addon construct's own metrics.
var kubeSystemDeployments = []string{}

// kubeSystemDaemonSets returns the always-present EKS kube-system DaemonSets:
// aws-node (VPC CNI, 1 pod/node) and kube-proxy (1 pod/node). These appear on every
// EKS cluster regardless of declared addons.
var kubeSystemDaemonSets = []string{"aws-node", "kube-proxy"}

// addonDeployments maps addon construct name → Deployments it adds in kube-system.
// Replicas are carried by emitKSMDeploymentMeta (uses addonDeployReplicas below).
//
// ALL addon Deployments have MIGRATED to the addon catalog (now real SubstrateWorkloads with
// resolver-minted cluster-scoped PodNames + real Container): cert_manager, external_dns,
// load_balancer_controller, cluster_autoscaler, ebs_csi (ebs-csi-controller). This map is now
// EMPTY but retained (documented-empty, like kubeSystemDeployments) because workloadDeployments
// still ranges over it. ebs-csi-NODE stays a name-only DaemonSet (substrateDaemonSets).
var addonDeployments = map[string][]string{}

// addonDeployReplicas returns the canonical replica count for a kube-system addon deployment.
// Most addons run 1 replica; load-balancer-controller and ebs-csi-controller run 2 for HA.
func addonDeployReplicas(deploy string) int {
	switch deploy {
	case "aws-load-balancer-controller", "ebs-csi-controller":
		return 2
	default:
		return 1
	}
}

// addonDaemonSets maps addon construct name → DaemonSets it adds in kube-system.
var addonDaemonSets = map[string][]string{
	"ebs_csi": {"ebs-csi-node"},
}

// workloadDeployments returns the map of namespace→[]deploymentName for the cluster,
// built from fixture.Cluster.Workloads plus the always-present substrate pods (alloy
// in "monitoring") and the EKS kube-system Deployment baseline + addon-derived
// Deployments. Addon-derived DaemonSets are returned by substrateDaemonSets.
func workloadDeployments(cl *fixture.Cluster) map[string][]string {
	m := make(map[string][]string)
	for _, wl := range cl.Workloads {
		m[wl.Namespace] = append(m[wl.Namespace], wl.Name)
	}
	// Substrate addon/baseline pods (cert-manager, coredns, karpenter, …) are real workloads with
	// resolver-minted PodNames + real Container, carried in a SEPARATE slice so they stay invisible
	// to node-count summers (which iterate cl.Workloads only). The pod-shape emitters treat them as
	// real workloads via podWorkloadByName, so an addon construct's own metrics — stamped with the
	// SAME PodNames — join the kube_pod_* series. Dedup on (namespace,name) defensively.
	seen := make(map[[2]string]bool)
	for ns, deploys := range m {
		for _, d := range deploys {
			seen[[2]string{ns, d}] = true
		}
	}
	for _, wl := range cl.SubstrateWorkloads {
		key := [2]string{wl.Namespace, wl.Name}
		if seen[key] {
			continue
		}
		seen[key] = true
		m[wl.Namespace] = append(m[wl.Namespace], wl.Name)
	}
	// alloy-metrics/alloy-logs in "monitoring" have MIGRATED to the addon catalog
	// (MonitoringBaselineWorkloads, carried in cl.SubstrateWorkloads with real Controller —
	// alloy-metrics StatefulSet, alloy-logs DaemonSet — and resolver-minted cluster-scoped
	// PodNames). They are picked up by the SubstrateWorkloads loop above, so no name-only add here.
	// EKS kube-system Deployment baseline (always present on any EKS cluster).
	m["kube-system"] = append(m["kube-system"], kubeSystemDeployments...)
	// Addon-derived Deployments in kube-system (only for declared addons).
	addonSet := make(map[string]bool, len(cl.Addons))
	for _, a := range cl.Addons {
		addonSet[a] = true
	}
	for addon, deploys := range addonDeployments {
		if addonSet[addon] {
			m["kube-system"] = append(m["kube-system"], deploys...)
		}
	}
	return m
}

// podWorkloadByName indexes BOTH cl.Workloads and cl.SubstrateWorkloads by deployment name, so the
// pod-shape emitters (ksm/cadvisor/opencost/kepler/kubelet/podlogs/events) resolve the real
// resolver-minted PodNames / NodeIdx / Container / owner-kind for substrate addons instead of
// synthesizing them name-only. cl.Workloads win on a name collision (declared app workloads take
// precedence over a same-named substrate entry). Returns pointers into the cluster's slices.
func podWorkloadByName(cl *fixture.Cluster) map[string]*fixture.Workload {
	m := make(map[string]*fixture.Workload, len(cl.Workloads)+len(cl.SubstrateWorkloads))
	for i := range cl.SubstrateWorkloads {
		m[cl.SubstrateWorkloads[i].Name] = &cl.SubstrateWorkloads[i]
	}
	// cl.Workloads override substrate on a name collision.
	for i := range cl.Workloads {
		m[cl.Workloads[i].Name] = &cl.Workloads[i]
	}
	return m
}

// substrateDaemonSets returns the full set of kube-system DaemonSets for the cluster:
// the EKS baseline (aws-node, kube-proxy) plus any addon-derived DaemonSets (ebs-csi-node
// for ebs_csi). The node-exporter DaemonSet in "monitoring" is emitted separately by
// emitKSMDaemonSets and is NOT included here (it has its own fixed labels).
func substrateDaemonSets(cl *fixture.Cluster) []string {
	ds := append([]string(nil), kubeSystemDaemonSets...)
	addonSet := make(map[string]bool, len(cl.Addons))
	for _, a := range cl.Addons {
		addonSet[a] = true
	}
	for addon, dsets := range addonDaemonSets {
		if addonSet[addon] {
			ds = append(ds, dsets...)
		}
	}
	return ds
}

// extraNamespaces are the platform namespaces beyond the app set.
var extraNamespaces = []string{"kube-system", "default"}
