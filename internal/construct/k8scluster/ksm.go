// SPDX-License-Identifier: AGPL-3.0-only

// ksm.go — kube-state-metrics (KSM) emission for k8scluster.
// All series carry ksmLabels (job=integrations/kubernetes/kube-state-metrics,
// instance=<ksmInstance>, cluster, k8s_cluster_name, source="kubernetes").
//
// Fidelity note: label KEYS and value enums on every series below are sourced from the live
// a live reference cluster KSM capture (signals/k8s.md [slug: k8s-ksm]). Values are synthetic but
// plausible for synth's declared entities; no metric/label name is invented. Where the audit had
// no live sample (Jobs/CronJobs — the reference cluster ran none) the label set is sourced from the KSM docs /
// k8s-monitoring chart allowlist and flagged as such in the lane report.
package k8scluster

import (
	"fmt"
	"sort"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// ── Platform defaults ───────────────────────────────────────────────────────────────
//
// platformOr returns the cluster Platform with Amazon-Linux-2/containerd defaults filled in for any
// field the blueprint/resolver left blank, so node_info/conditions never emit "" identity fields.
func platformOr(p fixture.Platform) fixture.Platform {
	// Defaults MUST match blueprint.resolvePlatform's AL2023 defaults so test fixtures
	// (which leave Platform zero) and resolver-built clusters agree.
	if p.OSImage == "" {
		p.OSImage = "Amazon Linux 2023"
	}
	if p.OSID == "" {
		p.OSID = "amzn"
	}
	if p.ContainerRuntime == "" {
		p.ContainerRuntime = "containerd://1.7.27"
	}
	if p.KubeletVersion == "" {
		p.KubeletVersion = "v1.31.2-eks-f69f56f"
	}
	if p.KubernetesVersion == "" {
		p.KubernetesVersion = "1.31"
	}
	if p.KernelVersion == "" {
		p.KernelVersion = "6.1.141"
	}
	return p
}

// clusterRegion derives the cluster region from the first node hostname
// ("ip-10-0-1-2.us-east-1.compute.internal" → "us-east-1"); falls back to "".
func clusterRegion(nodes []fixture.Node) string {
	for _, n := range nodes {
		parts := splitHostname(n.Hostname)
		if len(parts) >= 2 {
			return parts[1]
		}
		break
	}
	return ""
}

// hex16 returns a deterministic 16-hex digest of the inputs (for the synthetic
// label_k8s_io_cloud_provider_aws node value, which real KSM carries as a 16-hex string).
func hex16(parts ...string) string {
	h := uint64(1469598103934665603) // FNV-1a 64 offset
	for _, p := range parts {
		for i := 0; i < len(p); i++ {
			h ^= uint64(p[i])
			h *= 1099511628211
		}
	}
	return fmt.Sprintf("%016x", h)
}

// ── Node objects ──────────────────────────────────────────────────────────────────

// emitKSMNodeObjects emits kube_node_info (discovery gate) plus created, labels,
// capacity, allocatable for every node. Platform identity (os_image, runtime, kubelet/kernel
// versions) flows from cl.Platform; capacity pods is the instance-type MaxPods.
func emitKSMNodeObjects(st *state.State, cluster string, cl *fixture.Cluster, nodes []fixture.Node, factor float64) {
	region := clusterRegion(nodes)
	plat := platformOr(cl.Platform)
	_ = factor
	for ni, n := range nodes {
		node := n.Hostname
		internalIP := n.PrivateIP
		if internalIP == "" {
			internalIP = nodeInternalIP(node)
		}

		hostHash := 0
		for _, c := range node {
			hostHash = (hostHash*31 + int(c)) & 0x7fffffff
		}

		providerID := nodeProviderID(n, region, ni)
		az := region + string(rune('a'+ni%3))

		info := merge(ksmLabels(cluster), map[string]string{
			"node":                      node,
			"internal_ip":               internalIP,
			"kernel_version":            plat.KernelVersion,
			"kubelet_version":           plat.KubeletVersion,
			"kubeproxy_version":         plat.KubeletVersion,
			"os_image":                  plat.OSImage,
			"container_runtime_version": plat.ContainerRuntime,
			"provider_id":               providerID,
			"system_uuid":               fmt.Sprintf("ec2%013x", hostHash),
		})
		st.Set("kube_node_info", info, 1)
		st.Set("kube_node_created", merge(ksmLabels(cluster), map[string]string{"node": node}), float64(clusterCreatedUnix))

		// kube_node_labels — match the real label_* key set (the live-reference audit §kube_node_labels).
		// The provisioner key differs by node provenance: Karpenter nodes carry
		// label_karpenter_sh_nodepool; managed-nodegroup nodes carry label_eks_amazonaws_com_nodegroup.
		osLabel := n.OS
		if osLabel == "" {
			osLabel = "linux"
		}
		labels := map[string]string{
			"node":                                   node,
			"label_node_kubernetes_io_instance_type": n.InstanceType,
			"label_beta_kubernetes_io_instance_type": n.InstanceType,
			"label_kubernetes_io_arch":               fixture.LookupInstanceSpec(n.InstanceType).KubeArch(),
			"label_kubernetes_io_os":                 osLabel,
			"label_kubernetes_io_hostname":           node,
			"label_topology_kubernetes_io_zone":      az,
			"label_topology_kubernetes_io_region":    region,
			"label_k8s_io_cloud_provider_aws":        hex16(cluster, node),
		}
		if n.Provisioner == "karpenter" {
			labels["label_karpenter_sh_nodepool"] = n.NodeGroup
		} else {
			labels["label_eks_amazonaws_com_nodegroup"] = n.NodeGroup
		}
		st.Set("kube_node_labels", merge(ksmLabels(cluster), labels), 1)
		st.Set("kube_node_spec_unschedulable", merge(ksmLabels(cluster), map[string]string{"node": node}), 0)

		// kube_node_status_addresses — 3 series per node (the live-reference audit §Node):
		// type=Hostname (address=hostname), type=InternalDNS (address=hostname), type=InternalIP (address=privateIP).
		for _, addrEntry := range []struct{ addrType, address string }{
			{"Hostname", node},
			{"InternalDNS", node},
			{"InternalIP", internalIP},
		} {
			st.Set("kube_node_status_addresses", merge(ksmLabels(cluster), map[string]string{
				"node":    node,
				"type":    addrEntry.addrType,
				"address": addrEntry.address,
			}), 1)
		}

		numCPUs := vcpusForNode(n)
		mem := memBytesForNode(n)
		maxPods := fixture.LookupInstanceSpec(n.InstanceType).MaxPods()
		// resource → [value, unit]. hugepages variants are present on real EKS/Bottlerocket nodes as
		// capacity/allocatable with value 0 and unit byte (none configured).
		capacity := []struct {
			res, unit string
			val       float64
		}{
			{"cpu", "core", float64(numCPUs)},
			{"memory", "byte", mem},
			{"pods", "integer", float64(maxPods)},
			{"ephemeral_storage", "byte", float64(100 * 1024 * 1024 * 1024)},
			{"hugepages_1Gi", "byte", 0},
			{"hugepages_2Mi", "byte", 0},
			{"hugepages_32Mi", "byte", 0},
			{"hugepages_64Ki", "byte", 0},
		}
		for _, c := range capacity {
			st.Set("kube_node_status_capacity", merge(ksmLabels(cluster), map[string]string{
				"node": node, "resource": c.res, "unit": c.unit,
			}), c.val)
			alloc := c.val
			// pods/hugepages allocatable == capacity; cpu/memory/storage carry the reserved haircut.
			if c.res == "cpu" || c.res == "memory" || c.res == "ephemeral_storage" {
				alloc = c.val * 0.95
			}
			st.Set("kube_node_status_allocatable", merge(ksmLabels(cluster), map[string]string{
				"node": node, "resource": c.res, "unit": c.unit,
			}), alloc)
		}
	}
}

// emitKSMNodeConditions emits kube_node_status_condition for every node. When notReadyIdx >= 0 that
// node's Ready condition is flipped (true→0, false→1) to model the node_not_ready failure mode —
// bending VALUES on the existing series, never the schema. PIDPressure is always emitted; on
// Bottlerocket nodes (cl.Platform.OSID=="bottlerocket") the extra ContainerRuntimeReady/KernelReady/
// NetworkingReady/StorageReady conditions are emitted too (the live-reference audit §kube_node_status_condition).
func emitKSMNodeConditions(st *state.State, cluster string, cl *fixture.Cluster, nodes []fixture.Node, notReadyIdx int) {
	type nc struct{ condition, status string }
	// Pressure-style conditions are healthy at status=false (value 1 on the false series).
	pressure := []string{"MemoryPressure", "DiskPressure", "PIDPressure"}
	// Ready-style conditions are healthy at status=true (value 1 on the true series).
	ready := []string{"Ready"}
	if platformOr(cl.Platform).OSID == "bottlerocket" {
		ready = append(ready, "ContainerRuntimeReady", "KernelReady", "NetworkingReady", "StorageReady")
	}

	// Real KSM fans every condition over status ∈ {true,false,unknown} (live-reference audit). The
	// unknown series sits at 0 at baseline (only a partitioned node would carry unknown=1, which the
	// node_not_ready model represents as Ready false=1 instead).
	var conditions []nc
	for _, c := range ready {
		conditions = append(conditions, nc{c, "true"}, nc{c, "false"}, nc{c, "unknown"})
	}
	for _, c := range pressure {
		conditions = append(conditions, nc{c, "true"}, nc{c, "false"}, nc{c, "unknown"})
	}

	for ni, n := range nodes {
		down := ni == notReadyIdx
		for _, c := range conditions {
			val := 0.0
			isReadyKind := false
			for _, r := range ready {
				if c.condition == r {
					isReadyKind = true
				}
			}
			switch {
			case isReadyKind && c.status == "true":
				val = 1
				if down && c.condition == "Ready" {
					val = 0
				}
			case c.condition == "Ready" && c.status == "false":
				if down {
					val = 1
				}
			case !isReadyKind && c.status == "false":
				// pressure conditions: healthy = false
				val = 1
			}
			st.Set("kube_node_status_condition", merge(ksmLabels(cluster), map[string]string{
				"node":      n.Hostname,
				"condition": c.condition,
				"status":    c.status,
			}), val)
		}
	}
}

// ── Namespaces ────────────────────────────────────────────────────────────────────

func emitKSMNamespacePhase(st *state.State, cluster string, cl *fixture.Cluster) {
	nsSet := map[string]bool{}
	for ns := range workloadDeployments(cl) {
		nsSet[ns] = true
	}
	for _, ns := range extraNamespaces {
		nsSet[ns] = true
	}
	nsList := make([]string, 0, len(nsSet))
	for ns := range nsSet {
		nsList = append(nsList, ns)
	}
	sort.Strings(nsList)

	for _, ns := range nsList {
		for _, phase := range []string{"Active", "Terminating"} {
			val := 0.0
			if phase == "Active" {
				val = 1
			}
			st.Set("kube_namespace_status_phase", merge(ksmLabels(cluster), map[string]string{
				"namespace": ns, "phase": phase,
			}), val)
		}
	}
}

// ── Deployments ───────────────────────────────────────────────────────────────────

// deployReplicas returns the replica count for a deployment. A substrate-backed workload
// (fwl != nil — cert-manager, coredns, karpenter, lbc, …) uses its catalog Replicas. For a
// name-only synthesized kube-system Deployment (fwl == nil, e.g. ebs-csi-controller) the count is
// fixed by addonDeployReplicas; for app workloads the live replicas value is used.
func deployReplicas(fwl *fixture.Workload, ns, deploy string, appReplicas int) int {
	if fwl != nil {
		if fwl.Replicas > 0 {
			return fwl.Replicas
		}
		return appReplicas
	}
	if ns == "kube-system" {
		return addonDeployReplicas(deploy)
	}
	return appReplicas
}

func emitKSMDeploymentMeta(st *state.State, cluster string, cl *fixture.Cluster, replicas int) {
	skip := nonDeploymentWorkloadNames(cl)
	wlByName := podWorkloadByName(cl)
	for ns, deploys := range workloadDeployments(cl) {
		for _, deploy := range deploys {
			if skip[deploy] {
				continue // StatefulSet/DaemonSet workload — emitted by its own controller family
			}
			r := deployReplicas(wlByName[deploy], ns, deploy, replicas)
			base := merge(ksmLabels(cluster), map[string]string{"namespace": ns, "deployment": deploy})
			st.Set("kube_deployment_spec_replicas", base, float64(r))
			st.Set("kube_deployment_status_replicas", base, float64(r))
			st.Set("kube_deployment_status_replicas_available", base, float64(r))
			st.Set("kube_deployment_status_replicas_updated", base, float64(r))
			st.Set("kube_deployment_metadata_generation", base, 3)
			st.Set("kube_deployment_status_observed_generation", base, 3)
			// reason per condition (the live-reference audit §kube_deployment_status_condition:
			// Available→MinimumReplicasAvailable, Progressing→NewReplicaSetAvailable).
			condReason := map[string]string{
				"Available":   "MinimumReplicasAvailable",
				"Progressing": "NewReplicaSetAvailable",
			}
			for _, cond := range []string{"Available", "Progressing"} {
				reason := condReason[cond]
				st.Set("kube_deployment_status_condition", merge(base, map[string]string{
					"condition": cond, "status": "true", "reason": reason,
				}), 1)
				st.Set("kube_deployment_status_condition", merge(base, map[string]string{
					"condition": cond, "status": "false", "reason": reason,
				}), 0)
			}
		}
	}
}

// ── ReplicaSets ───────────────────────────────────────────────────────────────────

func emitKSMReplicaSets(st *state.State, cluster string, cl *fixture.Cluster, replicas int) {
	skip := nonDeploymentWorkloadNames(cl)
	wlByName := podWorkloadByName(cl)
	for ns, deploys := range workloadDeployments(cl) {
		for _, deploy := range deploys {
			if skip[deploy] {
				continue // non-Deployment workload — no ReplicaSet
			}
			r := float64(deployReplicas(wlByName[deploy], ns, deploy, replicas))
			rs := replicaSetName(deploy)
			base := merge(ksmLabels(cluster), map[string]string{"namespace": ns, "replicaset": rs})
			st.Set("kube_replicaset_owner", merge(base, map[string]string{
				"owner_kind": "Deployment", "owner_name": deploy, "owner_is_controller": "true",
			}), 1)
			st.Set("kube_replicaset_spec_replicas", base, r)
			st.Set("kube_replicaset_status_replicas", base, r)
			st.Set("kube_replicaset_status_ready_replicas", base, r)
			st.Set("kube_replicaset_created", base, float64(clusterCreatedUnix))
			// Additional replicaset gauges (the live-reference audit §ReplicaSet).
			st.Set("kube_replicaset_metadata_generation", base, 1)
			st.Set("kube_replicaset_status_observed_generation", base, 1)
			st.Set("kube_replicaset_status_fully_labeled_replicas", base, r)
			st.Set("kube_replicaset_status_terminating_replicas", base, 0)
		}
	}
}

// ── StatefulSets ──────────────────────────────────────────────────────────────────

// statefulSetsForCluster returns the representative StatefulSet set for a cluster: one per workload
// whose name implies stateful character (db/cache/queue/broker/zk), else a single synthetic
// "<cluster>-state" StatefulSet in the "databases" namespace so the family is never empty. Names +
// namespaces are deterministic from the declared entities (no random minting).
type ssEntry struct {
	ns, name string
	replicas int
}

// wlController classifies a workload's k8s controller kind: an explicit Controller wins; otherwise
// the legacy name heuristic (back-compat for unset workloads) maps stateful-sounding names to
// statefulset, everything else to deployment.
func wlController(wl fixture.Workload) string {
	switch wl.Controller {
	case "statefulset", "daemonset", "deployment":
		return wl.Controller
	default:
		if isStatefulName(wl.Name) {
			return "statefulset"
		}
		return "deployment"
	}
}

// nonDeploymentWorkloadNames is the set of workloads (cl.Workloads AND cl.SubstrateWorkloads) that
// are NOT Deployments, so the Deployment meta/ReplicaSet emitters skip them — each workload lands in
// exactly one controller family. Substrate StatefulSets (e.g. argocd-application-controller) carry an
// explicit Controller, so wlController routes them out of the Deployment family.
func nonDeploymentWorkloadNames(cl *fixture.Cluster) map[string]bool {
	m := map[string]bool{}
	for _, wl := range cl.Workloads {
		if wlController(wl) != "deployment" {
			m[wl.Name] = true
		}
	}
	for _, wl := range cl.SubstrateWorkloads {
		if wlController(wl) != "deployment" {
			m[wl.Name] = true
		}
	}
	return m
}

func statefulSetsForCluster(cl *fixture.Cluster, replicas int) []ssEntry {
	var out []ssEntry
	declaredStateful := false
	for _, wl := range cl.Workloads {
		if wlController(wl) == "statefulset" {
			declaredStateful = true
			reps := wl.Replicas
			if reps <= 0 {
				reps = replicas
			}
			out = append(out, ssEntry{ns: wl.Namespace, name: wl.Name, replicas: reps})
		}
	}
	// Substrate StatefulSets (e.g. argocd-application-controller) get their own kube_statefulset_*
	// family so their pod owner routing (StatefulSet) is internally consistent.
	for _, wl := range cl.SubstrateWorkloads {
		if wlController(wl) == "statefulset" {
			reps := wl.Replicas
			if reps <= 0 {
				reps = replicas
			}
			out = append(out, ssEntry{ns: wl.Namespace, name: wl.Name, replicas: reps})
		}
	}
	// The synthetic <cluster>-state fallback's trigger stays keyed on cl.Workloads ONLY (a substrate
	// StatefulSet must NOT suppress it — it keeps the representative app stateful workload present).
	if !declaredStateful {
		out = append(out, ssEntry{ns: "databases", name: cl.Name + "-state", replicas: 1})
	}
	return out
}

func isStatefulName(name string) bool {
	for _, kw := range []string{"db", "database", "postgres", "mysql", "redis", "cache", "kafka", "broker", "queue", "zookeeper", "etcd", "elastic", "mongo", "cassandra", "state"} {
		if containsStr(name, kw) {
			return true
		}
	}
	return false
}

// emitKSMStatefulSets emits the full kube_statefulset_* family for the cluster's representative
// StatefulSets (the live-reference audit §StatefulSet). The revision metrics carry a `revision` label; the
// retention-policy metric carries when_deleted/when_scaled (both "Retain", per live reference cluster).
func emitKSMStatefulSets(st *state.State, cluster string, cl *fixture.Cluster, replicas int) {
	for _, ss := range statefulSetsForCluster(cl, replicas) {
		base := merge(ksmLabels(cluster), map[string]string{"namespace": ss.ns, "statefulset": ss.name})
		reps := float64(ss.replicas)
		rev := fmt.Sprintf("%s-%s", ss.name, hex16(cluster, ss.name)[:10])

		st.Set("kube_statefulset_created", base, float64(clusterCreatedUnix))
		st.Set("kube_statefulset_metadata_generation", base, 1)
		st.Set("kube_statefulset_replicas", base, reps)
		st.Set("kube_statefulset_status_replicas", base, reps)
		st.Set("kube_statefulset_status_replicas_available", base, reps)
		st.Set("kube_statefulset_status_replicas_current", base, reps)
		st.Set("kube_statefulset_status_replicas_ready", base, reps)
		st.Set("kube_statefulset_status_replicas_updated", base, reps)
		st.Set("kube_statefulset_status_observed_generation", base, 1)

		st.Set("kube_statefulset_status_current_revision", merge(base, map[string]string{"revision": rev}), 1)
		st.Set("kube_statefulset_status_update_revision", merge(base, map[string]string{"revision": rev}), 1)

		st.Set("kube_statefulset_persistentvolumeclaim_retention_policy", merge(base, map[string]string{
			"when_deleted": "Retain", "when_scaled": "Retain",
		}), 1)
	}
}

// ── DaemonSets ────────────────────────────────────────────────────────────────────

// emitOneDaemonSet emits the full kube_daemonset_* family for a single DaemonSet.
// n is the desired/current scheduled count (typically nodeCount for a per-node DS).
func emitOneDaemonSet(st *state.State, cluster, ns, name string, n float64) {
	base := merge(ksmLabels(cluster), map[string]string{"namespace": ns, "daemonset": name})
	st.Set("kube_daemonset_status_desired_number_scheduled", base, n)
	st.Set("kube_daemonset_status_current_number_scheduled", base, n)
	st.Set("kube_daemonset_status_number_ready", base, n)
	st.Set("kube_daemonset_status_number_available", base, n)
	st.Set("kube_daemonset_status_number_misscheduled", base, 0)
	st.Set("kube_daemonset_metadata_generation", base, 1)
	st.Set("kube_daemonset_created", base, float64(clusterCreatedUnix))
	st.Set("kube_daemonset_status_number_unavailable", base, 0)
	st.Set("kube_daemonset_status_observed_generation", base, 1)
	st.Set("kube_daemonset_status_updated_number_scheduled", base, n)
}

func emitKSMDaemonSets(st *state.State, cluster string, cl *fixture.Cluster, nodeCount int) {
	n := float64(nodeCount)
	// node-exporter in "monitoring" (always present).
	emitOneDaemonSet(st, cluster, "monitoring", nodeExporterDS, n)
	// EKS kube-system baseline + addon-derived DaemonSets (each runs 1 pod/node).
	for _, ds := range substrateDaemonSets(cl) {
		emitOneDaemonSet(st, cluster, "kube-system", ds, n)
	}
	// Substrate-workload DaemonSets (e.g. alloy-logs in "monitoring", carried in SubstrateWorkloads
	// with a real Controller + resolver-minted per-node PodNames). One pod/node like the rest.
	for _, wl := range cl.SubstrateWorkloads {
		if wlController(wl) == "daemonset" {
			emitOneDaemonSet(st, cluster, wl.Namespace, wl.Name, n)
		}
	}
	// Blueprint-declared DaemonSet workloads (one pod/node, like the substrate set).
	for _, wl := range cl.Workloads {
		if wlController(wl) == "daemonset" {
			emitOneDaemonSet(st, cluster, wl.Namespace, wl.Name, n)
		}
	}
}

// ── Jobs / CronJobs ───────────────────────────────────────────────────────────────

// emitKSMJobsCron emits one representative CronJob (a nightly backup) plus its two most-recent Jobs
// per cluster. the reference cluster ran NO jobs live, so the label set + value enums here are sourced from the
// kube-state-metrics docs / k8s-monitoring chart allowlist (kube_job.*/kube_cronjob.*), NOT from the
// live capture — flagged in the lane report. Standard KSM labels: cronjob+namespace+schedule on the
// cronjob family; job_name+namespace on the job family; owner identity via
// kube_job_owner{owner_kind=CronJob}.
func emitKSMJobsCron(st *state.State, cluster string, cl *fixture.Cluster) {
	ns := "default"
	cron := cl.Name + "-backup"
	schedule := "0 2 * * *" // nightly
	cbase := merge(ksmLabels(cluster), map[string]string{"namespace": ns, "cronjob": cron})

	// CronJob family. Label keys + the family verified against a live capture (Jobs/CronJobs
	// created on the a live reference cluster lab cluster, 2026-06-14): kube_cronjob_info carries `timezone`;
	// spec history-limit + kube_job_status_ready are part of the real set.
	st.Set("kube_cronjob_info", merge(cbase, map[string]string{
		"schedule": schedule, "concurrency_policy": "Forbid", "timezone": "Etc/UTC",
	}), 1)
	st.Set("kube_cronjob_created", cbase, float64(clusterCreatedUnix))
	st.Set("kube_cronjob_status_active", cbase, 0)
	st.Set("kube_cronjob_spec_suspend", cbase, 0)
	st.Set("kube_cronjob_spec_successful_job_history_limit", cbase, 3)
	st.Set("kube_cronjob_spec_failed_job_history_limit", cbase, 1)
	st.Set("kube_cronjob_next_schedule_time", cbase, float64(clusterCreatedUnix+86400))
	st.Set("kube_cronjob_status_last_schedule_time", cbase, float64(clusterCreatedUnix))
	st.Set("kube_cronjob_metadata_resource_version", cbase, float64(clusterCreatedUnix))

	// Two recent Jobs spawned by the CronJob. Real CronJob Jobs are named "<cronjob>-<scheduleIndex>"
	// where scheduleIndex is the unix-MINUTES of the scheduled run (verified via live reference, 2026-06-16:
	// "gt-cron-29693797"). That index is a LIVE wall-clock value IRL — but synth must NOT key a series
	// NAME on wall-clock (it would churn the series every minute → unbounded cardinality), so we derive
	// it from the STABLE clusterCreatedUnix cadence: schedIdx = (clusterCreatedUnix/60) - i*1440 (one
	// nightly run per 1440 minutes), giving stable per-cluster names that look real.
	for i := 0; i < 2; i++ {
		schedIdx := clusterCreatedUnix/60 - int64(i)*1440
		jobName := fmt.Sprintf("%s-%d", cron, schedIdx)
		jbase := merge(ksmLabels(cluster), map[string]string{"namespace": ns, "job_name": jobName})
		completeTS := float64(clusterCreatedUnix - int64(i)*86400)
		st.Set("kube_job_info", jbase, 1)
		st.Set("kube_job_created", jbase, completeTS-60)
		st.Set("kube_job_owner", merge(jbase, map[string]string{
			"owner_kind": "CronJob", "owner_name": cron, "owner_is_controller": "true",
		}), 1)
		st.Set("kube_job_spec_completions", jbase, 1)
		st.Set("kube_job_spec_parallelism", jbase, 1)
		st.Set("kube_job_status_active", jbase, 0)
		st.Set("kube_job_status_ready", jbase, 0)
		st.Set("kube_job_status_succeeded", jbase, 1)
		st.Set("kube_job_status_failed", jbase, 0)
		st.Set("kube_job_status_start_time", jbase, completeTS-60)
		st.Set("kube_job_status_completion_time", jbase, completeTS)
		st.Set("kube_job_complete", merge(jbase, map[string]string{
			"condition": "true",
		}), 1)

		// The Job's completed pod: "<job>-<5char>", owned by the Job (owner_kind=Job, verified via
		// live reference 2026-06-16). The batch.kubernetes.io/{job-name,controller-uid} POD LABELS surface
		// on kube_pod_labels in real KSM — which this construct does not emit at all — so they are
		// intentionally NOT placed on kube_pod_info (that family does not carry pod metadata labels).
		// The Job identity here rides created_by_kind=Job + kube_pod_owner{owner_kind=Job}.
		jobPod := fmt.Sprintf("%s-%s", jobName, hex16(cluster, jobName)[:5])
		puid := podUID(cluster, ns, jobPod)
		st.Set("kube_pod_info", merge(ksmLabels(cluster), map[string]string{
			"namespace": ns, "pod": jobPod, "uid": puid,
			"created_by_kind": "Job", "created_by_name": jobName,
			"host_ip": "10.0.0.1", "pod_ip": "10.1.40.50",
			"priority_class": "normal", "host_network": "false",
		}), 1)
		st.Set("kube_pod_owner", merge(ksmLabels(cluster), map[string]string{
			"namespace": ns, "pod": jobPod, "uid": puid,
			"owner_kind": "Job", "owner_name": jobName, "owner_is_controller": "true",
		}), 1)
		st.Set("kube_pod_start_time", merge(ksmLabels(cluster), map[string]string{
			"namespace": ns, "pod": jobPod, "uid": puid,
		}), completeTS-60)
	}
}

// ── HPAs ──────────────────────────────────────────────────────────────────────────

func emitKSMHPAs(st *state.State, cluster string, cl *fixture.Cluster, replicas int, factor float64) {
	for _, wl := range cl.Workloads {
		// HPAs are opt-in (real clusters scope them to a subset of Deployments/StatefulSets — never
		// DaemonSets). Only workloads that declare HasHPA get the family.
		if !wl.HasHPA || wlController(wl) == "daemonset" {
			continue
		}
		base := merge(ksmLabels(cluster), map[string]string{
			"namespace": wl.Namespace, "horizontalpodautoscaler": wl.Name,
		})
		desired := float64(replicas)
		if factor > 0.7 {
			desired++
		}
		st.Set("kube_horizontalpodautoscaler_spec_min_replicas", base, 2)
		st.Set("kube_horizontalpodautoscaler_spec_max_replicas", base, 6)
		st.Set("kube_horizontalpodautoscaler_status_current_replicas", base, float64(replicas))
		st.Set("kube_horizontalpodautoscaler_status_desired_replicas", base, desired)
	}
}

// ── Storage ───────────────────────────────────────────────────────────────────────

// emitKSMStorage emits the PV/PVC KSM family (the live-reference audit §PersistentVolume /
// §PersistentVolumeClaim / kube_pod_spec_volumes_persistentvolumeclaims_info). Every series reads
// the SHARED volumesForCluster identity source, so persistentvolumeclaim/persistentvolume names
// join across kubelet_volume_stats (kubelet.go) and OpenCost pv/pvc cost (conformance.go).
func emitKSMStorage(st *state.State, cluster string, cl *fixture.Cluster) {
	// PVC status phases (real KSM enum); PV status phases (separate enum). value 1 on Bound.
	pvcPhases := []string{"Bound", "Pending", "Lost"}
	pvPhases := []string{"Available", "Bound", "Released", "Failed", "Pending"}

	for _, vol := range volumesForCluster(cl) {
		// The POD uid uses the same derivation KSM uses for kube_pod_* (podUID(ns, pod)) so
		// kube_pod_spec_volumes_persistentvolumeclaims_info.uid joins the pod's other KSM series.
		podUid := podUID(cluster, vol.ns, vol.pod)

		// kube_persistentvolumeclaim_info — carries storageclass/volumemode/volumename (PV join).
		st.Set("kube_persistentvolumeclaim_info", merge(ksmLabels(cluster), map[string]string{
			"namespace":             vol.ns,
			"persistentvolumeclaim": vol.pvc,
			"storageclass":          vol.storageClass,
			"volumemode":            "Filesystem",
			"volumename":            vol.pv,
		}), 1)

		// kube_persistentvolumeclaim_access_mode
		st.Set("kube_persistentvolumeclaim_access_mode", merge(ksmLabels(cluster), map[string]string{
			"namespace":             vol.ns,
			"persistentvolumeclaim": vol.pvc,
			"access_mode":           "ReadWriteOnce",
		}), 1)

		// kube_persistentvolumeclaim_resource_requests_storage_bytes
		st.Set("kube_persistentvolumeclaim_resource_requests_storage_bytes", merge(ksmLabels(cluster), map[string]string{
			"namespace":             vol.ns,
			"persistentvolumeclaim": vol.pvc,
		}), vol.capacityBytes)

		// kube_persistentvolumeclaim_labels — identity + a representative app label.
		st.Set("kube_persistentvolumeclaim_labels", merge(ksmLabels(cluster), map[string]string{
			"namespace":                         vol.ns,
			"persistentvolumeclaim":             vol.pvc,
			"label_app_kubernetes_io_name":      vol.ns,
			"label_app_kubernetes_io_component": "storage",
		}), 1)

		// kube_persistentvolumeclaim_status_phase — fan out all phases, 1 on Bound.
		for _, phase := range pvcPhases {
			val := 0.0
			if phase == "Bound" {
				val = 1
			}
			st.Set("kube_persistentvolumeclaim_status_phase", merge(ksmLabels(cluster), map[string]string{
				"namespace":             vol.ns,
				"persistentvolumeclaim": vol.pvc,
				"phase":                 phase,
			}), val)
		}

		// kube_persistentvolume_status_phase — one PV per PVC, fan out all phases, 1 on Bound.
		for _, phase := range pvPhases {
			val := 0.0
			if phase == "Bound" {
				val = 1
			}
			st.Set("kube_persistentvolume_status_phase", merge(ksmLabels(cluster), map[string]string{
				"persistentvolume": vol.pv,
				"phase":            phase,
			}), val)
		}

		// kube_pod_spec_volumes_persistentvolumeclaims_info — pod→PVC mount. uid = POD uid.
		st.Set("kube_pod_spec_volumes_persistentvolumeclaims_info", merge(ksmLabels(cluster), map[string]string{
			"namespace":             vol.ns,
			"pod":                   vol.pod,
			"persistentvolumeclaim": vol.pvc,
			"uid":                   podUid,
			"volume":                vol.pvc + "-vol",
		}), 1)
	}
}

// ── Quotas / ConfigMaps / Secrets ────────────────────────────────────────────────

func emitKSMQuotaConfigSecret(st *state.State, cluster string, cl *fixture.Cluster) {
	nsSet := map[string]bool{}
	for ns := range workloadDeployments(cl) {
		nsSet[ns] = true
	}
	for ns := range nsSet {
		for _, res := range []string{"requests.cpu", "requests.memory", "limits.cpu", "limits.memory"} {
			hard, used := 16.0, 6.0
			if containsStr(res, "memory") {
				hard = 64 * 1024 * 1024 * 1024
				used = 18 * 1024 * 1024 * 1024
			}
			for _, t := range []struct {
				typ string
				val float64
			}{{"hard", hard}, {"used", used}} {
				st.Set("kube_resourcequota", merge(ksmLabels(cluster), map[string]string{
					"namespace": ns, "resourcequota": ns + "-quota", "resource": res, "type": t.typ,
				}), t.val)
			}
		}
		cm := ns + "-config"
		st.Set("kube_configmap_info", merge(ksmLabels(cluster), map[string]string{
			"namespace": ns, "configmap": cm,
		}), 1)
		st.Set("kube_configmap_metadata_resource_version", merge(ksmLabels(cluster), map[string]string{
			"namespace": ns, "configmap": cm,
		}), float64(clusterCreatedUnix))
		st.Set("kube_secret_metadata_resource_version", merge(ksmLabels(cluster), map[string]string{
			"namespace": ns, "secret": ns + "-secret",
		}), float64(clusterCreatedUnix))
	}
}

// ── Pod failure-mode intensity helpers ──────────────────────────────────────────

// podAffected reports whether a pod (identified by uid) is selected by the intensity-
// fraction for a given failure mode. It uses a deterministic polynomial hash of
// uid+"/"+mode mapped to a 0–999 bucket; the pod is affected when that bucket is below
// int(inten*1000). This is purely deterministic — no math/rand, no time.Now.
//
// FLOOR contract: callers must ensure AT LEAST ONE pod is affected when inten > 0. That is
// handled by buildAffectedSets (pre-scan), not here, so this function stays pure.
func podAffected(uid, mode string, inten float64) bool {
	h := 0
	for _, c := range uid + "/" + mode {
		h = (h*31 + int(c)) & 0x7fffffff
	}
	threshold := int(inten * 1000)
	return (h % 1000) < threshold
}

// buildAffectedSets iterates all pod UIDs for the cluster once and returns two sets:
// oomAffected and crashAffected (uid → true). When a mode is inactive the returned map
// is empty. When the mode is active but intensity rounds to 0 affected pods, the pod
// with the minimum hash value is force-selected (AT-LEAST-1 floor).
func buildAffectedSets(
	cl *fixture.Cluster,
	nodes []fixture.Node,
	replicas int,
	cluster string,
	oomActive bool, oomInten float64,
	crashActive bool, crashInten float64,
) (oomAffected, crashAffected map[string]bool) {
	oomAffected = map[string]bool{}
	crashAffected = map[string]bool{}
	if !oomActive && !crashActive {
		return
	}

	// Collect all pod UIDs in the same iteration order as emitKSMPods.
	var allUIDs []string
	for ns, deploys := range workloadDeployments(cl) {
		wlByName := podWorkloadByName(cl)
		for _, deploy := range deploys {
			fwl := wlByName[deploy]
			var reps int
			if fwl == nil && ns == "kube-system" {
				reps = addonDeployReplicas(deploy)
			} else {
				reps = substrateReps(fwl, replicas, len(nodes))
			}
			for ri := 0; ri < reps; ri++ {
				var pod string
				if fwl != nil && ri < len(fwl.PodNames) {
					pod = fwl.PodNames[ri]
				} else {
					pod = synthPodName(deploy, ri)
				}
				uid := podUID(cluster, ns, pod)
				allUIDs = append(allUIDs, uid)
			}
		}
	}

	if oomActive {
		oomAffected = selectAffected(allUIDs, "oom_kill", oomInten)
	}
	if crashActive {
		crashAffected = selectAffected(allUIDs, "pod_crashloop", crashInten)
	}
	return
}

// selectAffected applies podAffected to every uid in allUIDs and returns the resulting
// set. If no uid passes the threshold (intensity too low after rounding), the uid with
// the minimum polynomial hash is force-selected to satisfy the AT-LEAST-1 floor.
func selectAffected(allUIDs []string, mode string, inten float64) map[string]bool {
	out := map[string]bool{}
	minHash := -1
	minUID := ""
	for _, uid := range allUIDs {
		h := 0
		for _, c := range uid + "/" + mode {
			h = (h*31 + int(c)) & 0x7fffffff
		}
		bucket := h % 1000
		if podAffected(uid, mode, inten) {
			out[uid] = true
		}
		if minHash < 0 || bucket < minHash {
			minHash = bucket
			minUID = uid
		}
	}
	// AT-LEAST-1 floor: when inten > 0 but no pod was selected (e.g. intensity=0.001
	// and every hash bucket falls above the threshold), force the minimum-hash pod.
	if len(out) == 0 && inten > 0 && minUID != "" {
		out[minUID] = true
	}
	return out
}

// ── Pod emission ──────────────────────────────────────────────────────────────────

// emitKSMPods emits all pod-level KSM series for workloads placed on the cluster.
// For workloads with explicit PodNames (from fixture.Workload), those names are used.
// For substrate deployments (alloy etc.) without fixture placements, names are synthesized.
func emitKSMPods(
	st *state.State,
	cluster string,
	cl *fixture.Cluster,
	nodes []fixture.Node,
	replicas int,
	scale float64,
	now time.Time,
	w *core.World,
	notReadyIdx int,
) {
	// All 5 real phase enum values (audit: kube_pod_status_phase). Only Running/Pending ever
	// non-zero; Failed/Succeeded/Unknown stay 0 at baseline.
	phases := []string{"Running", "Pending", "Failed", "Succeeded", "Unknown"}

	// Cluster-axis failure factors (scope = cluster name). These bend VALUES on existing pod series
	// (restarts climb, phase shifts Running→Pending) and, for oom_kill, introduce the documented
	// container-termination INCIDENT series for the duration of the incident.
	oomFactor := w.Shape.FailFactor(now, "oom_kill", cluster, 8)
	crashFactor := w.Shape.FailFactor(now, "pod_crashloop", cluster, 5)
	oomActive, oomInten := w.Shape.Eval(now, "oom_kill", cluster)
	crashActive, crashInten := w.Shape.Eval(now, "pod_crashloop", cluster)

	// Pre-compute the set of pod UIDs affected by each mode (intensity-fraction selection).
	// Pre-scanning avoids the AT-LEAST-1 floor complexity inside the hot per-pod loop: we
	// collect all UIDs, run podAffected, and if none were selected we force the minimum-hash
	// pod to be affected. This is O(pods) extra work but keeps the loop body clean.
	oomAffectedUIDs, crashAffectedUIDs := buildAffectedSets(cl, nodes, replicas, cluster, oomActive, oomInten, crashActive, crashInten)

	notReadyNode := ""
	if notReadyIdx >= 0 && notReadyIdx < len(nodes) {
		notReadyNode = nodes[notReadyIdx].Hostname
	}

	wlByName := podWorkloadByName(cl)

	// Substrate workloads (addon/baseline pods) are system pods: they carry priority_class
	// system-node-critical like the name-only synthesized substrate did before the migration.
	substrateNames := make(map[string]bool, len(cl.SubstrateWorkloads))
	for _, wl := range cl.SubstrateWorkloads {
		substrateNames[wl.Name] = true
	}

	for ns, deploys := range workloadDeployments(cl) {
		for _, deploy := range deploys {
			fwl := wlByName[deploy]
			// Name-only synthesized kube-system Deployments (fwl == nil, e.g. ebs-csi-controller) use
			// fixed per-deployment replica counts via addonDeployReplicas rather than nodeCount.
			// Substrate-backed workloads (fwl != nil — coredns, cert-manager, karpenter, …) and app
			// workloads emit one pod per declared replica via substrateReps; other name-only substrate
			// (monitoring/alloy) returns nodeCount.
			var reps int
			if fwl == nil && ns == "kube-system" {
				reps = addonDeployReplicas(deploy)
			} else {
				reps = substrateReps(fwl, replicas, len(nodes))
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
				uid := podUID(cluster, ns, pod)
				rs := replicaSetName(deploy)
				// Controller-aware owner identity. Substrate (fwl==nil) is always a Deployment
				// (ReplicaSet-owned). Declared workloads route by controller kind.
				ownerKind, ownerName, wlType := "ReplicaSet", rs, "deployment"
				if fwl != nil {
					switch wlController(*fwl) {
					case "statefulset":
						ownerKind, ownerName, wlType = "StatefulSet", deploy, "statefulset"
					case "daemonset":
						ownerKind, ownerName, wlType = "DaemonSet", deploy, "daemonset"
					}
				}
				container := deploy
				if fwl != nil && fwl.Container != "" {
					container = fwl.Container
				}
				image := imageRepo(deploy) + ":latest"
				hostIP := nodeInternalIP(node)
				podIP := fixture.PodIP(nodeIdx, ns, pod, ri)

				// kube_pod_info — priority_class is never "" (I13): substrate/kube-system pods are
				// system-node-critical, app pods are normal (the live-reference audit §kube_pod_info
				// priority_class enum: normal, system-node-critical). host_network present on real.
				priorityClass := "normal"
				if ns == "kube-system" || fwl == nil || substrateNames[deploy] {
					priorityClass = "system-node-critical"
				}
				// kube_pod_info
				st.Set("kube_pod_info", merge(ksmLabels(cluster), map[string]string{
					"namespace": ns, "pod": pod, "node": node, "uid": uid,
					"created_by_kind": ownerKind, "created_by_name": ownerName,
					"host_ip": hostIP, "pod_ip": podIP,
					"priority_class": priorityClass, "host_network": "false",
				}), 1)
				// kube_pod_owner
				st.Set("kube_pod_owner", merge(ksmLabels(cluster), map[string]string{
					"namespace": ns, "pod": pod, "uid": uid,
					"owner_kind": ownerKind, "owner_name": ownerName, "owner_is_controller": "true",
				}), 1)
				// namespace_workload_pod
				nwp := merge(ksmLabels(cluster), map[string]string{
					"namespace": ns, "pod": pod, "workload": deploy, "workload_type": wlType,
				})
				// namespace_workload_pod is the RAW input KSM-style series. The
				// `namespace_workload_pod:kube_pod_owner:relabel` series is a Grafana
				// k8s-monitoring RECORDING-RULE output (it strips scrape labels to
				// {cluster,namespace,pod,workload,workload_type}); the deployed rule is the
				// sole producer. Synthkit must NOT also push it — doing so created two series
				// per {cluster,namespace,pod} with differing label cardinality (our scrape
				// labels vs the rule's stripped set), breaking 1:1 vector matching in panels.
				st.Set("namespace_workload_pod", nwp, 1)
				// kube_pod_start_time
				st.Set("kube_pod_start_time", merge(ksmLabels(cluster), map[string]string{
					"namespace": ns, "pod": pod, "uid": uid,
				}), float64(clusterCreatedUnix+nodeIdx*3600))
				// kube_pod_restart_policy
				st.Set("kube_pod_restart_policy", merge(ksmLabels(cluster), map[string]string{
					"namespace": ns, "pod": pod, "type": "Always",
				}), 1)
				// kube_pod_status_reason — all 5 real reason values (audit: Evicted, NodeAffinity,
				// NodeLost, Shutdown, UnexpectedAdmissionError) at baseline 0. Real carries `uid` here.
				// OOMKilled is a CONTAINER termination reason and is emitted below on
				// kube_pod_container_status_last_terminated_reason, never here.
				for _, reason := range []string{"Evicted", "NodeAffinity", "NodeLost", "Shutdown", "UnexpectedAdmissionError"} {
					st.Set("kube_pod_status_reason", merge(ksmLabels(cluster), map[string]string{
						"namespace": ns, "pod": pod, "reason": reason, "uid": uid,
					}), 0)
				}
				// kube_pod_container_status_last_terminated_reason{reason="OOMKilled"} — INCIDENT series.
				// Only emitted for pods selected by the intensity-fraction (oomAffectedUIDs).
				if oomAffectedUIDs[uid] {
					st.Set("kube_pod_container_status_last_terminated_reason", merge(ksmLabels(cluster), map[string]string{
						"namespace": ns, "pod": pod, "container": container, "uid": uid, "reason": "OOMKilled",
					}), 1)
				}
				// kube_pod_container_info
				st.Set("kube_pod_container_info", merge(ksmLabels(cluster), map[string]string{
					"namespace": ns, "pod": pod, "container": container, "uid": uid,
					"image": image, "image_spec": image,
					"image_id":     imageRepo(deploy) + "@sha256:" + fmt.Sprintf("%064x", nodeAssignment(ns, deploy, ri, 0x7fffffff)),
					"container_id": "containerd://" + fmt.Sprintf("%064x", nodeAssignment(ns, pod, ri, 0x7fffffff)),
				}), 1)
				// kube_pod_container_resource_requests / _limits — per-workload resolved values
				// (deterministic size-class default, or a blueprint override). The init container
				// (emitKSMInitContainer below) keeps its memory basis = the resolved memory limit.
				memLim := resolveMemLimit(fwl, deploy)
				for _, rl := range []struct {
					metric         string
					cpuVal, memVal float64
				}{
					{"kube_pod_container_resource_requests", resolveCPURequest(fwl, deploy), resolveMemRequest(fwl, deploy)},
					{"kube_pod_container_resource_limits", resolveCPULimit(fwl, deploy), memLim},
				} {
					st.Set(rl.metric, merge(ksmLabels(cluster), map[string]string{
						"namespace": ns, "pod": pod, "container": container, "node": node,
						"resource": "cpu", "unit": "core", "uid": uid,
					}), rl.cpuVal)
					st.Set(rl.metric, merge(ksmLabels(cluster), map[string]string{
						"namespace": ns, "pod": pod, "container": container, "node": node,
						"resource": "memory", "unit": "byte", "uid": uid,
					}), rl.memVal)
				}
				// Startup churn: deterministic per-pod, per-bucket (see churn.go).
				// Does not affect crashActive / notReadyNode logic — incidents override.
				wReason, startup := startingUp(ns, pod, now)

				// kube_pod_status_phase — all 5 phases emitted per pod (0/1). Running steady-state;
				// pod_crashloop / node_not_ready move Running→Pending; startup transient also → Pending.
				// crashAffectedUIDs selects only the intensity-fraction of pods for pod_crashloop.
				running := !crashAffectedUIDs[uid] && node != notReadyNode && !startup
				for _, phase := range phases {
					val := 0.0
					switch phase {
					case "Running":
						if running {
							val = 1
						}
					case "Pending":
						if !running {
							val = 1
						}
					}
					st.Set("kube_pod_status_phase", merge(ksmLabels(cluster), map[string]string{
						"namespace": ns, "pod": pod, "phase": phase, "uid": uid,
					}), val)
				}

				// kube_pod_status_ready — 3-condition fan-out per pod, matching real KSM
				// (mirrors kube_node_status_condition's 3-way fan-out). Ready = running &&
				// no startup transient; starting-up or incident-down → condition=false=1.
				// Source: kube-state-metrics standard; confirmed real on a live reference cluster 2026-06-16.
				for _, cond := range []string{"true", "false", "unknown"} {
					var readyVal float64
					switch cond {
					case "true":
						if running {
							readyVal = 1
						}
					case "false":
						if !running {
							readyVal = 1
						}
						// unknown stays 0 at baseline.
					}
					st.Set("kube_pod_status_ready", merge(ksmLabels(cluster), map[string]string{
						"namespace": ns, "pod": pod, "uid": uid, "condition": cond,
					}), readyVal)
				}

				// kube_pod_container_status_waiting (0/1) and
				// kube_pod_container_status_waiting_reason{reason} — real KSM emits one series
				// per container; reason ∈ {ContainerCreating, PodInitializing} for normal
				// transients (error reasons are incident-only). Confirmed real on a live reference cluster 2026-06-16.
				waitingVal := 0.0
				if startup {
					waitingVal = 1
				}
				waitingLbls := merge(ksmLabels(cluster), map[string]string{
					"namespace": ns, "pod": pod, "container": container, "uid": uid,
				})
				st.Set("kube_pod_container_status_waiting", waitingLbls, waitingVal)
				// Emit reason series for both normal-transient reasons (bounded cardinality).
				// Value 1 only for the active reason; 0 for the other (real KSM emits all
				// known reason series at 0 when not waiting, preserving the label set).
				for _, r := range []string{"ContainerCreating", "PodInitializing"} {
					rVal := 0.0
					if startup && r == wReason {
						rVal = 1
					}
					st.Set("kube_pod_container_status_waiting_reason", merge(ksmLabels(cluster), map[string]string{
						"namespace": ns, "pod": pod, "container": container, "uid": uid, "reason": r,
					}), rVal)
				}

				// kube_pod_container_status_restarts_total (counter).
				// Restart delta is only applied to pods selected by the intensity-fraction.
				rstLbls := merge(ksmLabels(cluster), map[string]string{
					"namespace": ns, "pod": pod, "container": container, "uid": uid,
				})
				restartDelta := 0.0
				if oomAffectedUIDs[uid] && oomFactor > 1 {
					restartDelta += oomFactor - 1
				}
				if crashAffectedUIDs[uid] && crashFactor > 1 {
					restartDelta += crashFactor - 1
				}
				st.Add("kube_pod_container_status_restarts_total", rstLbls, restartDelta)

				// ── Init container ("init") — one per pod (audit §Pod — Init Containers) ──
				emitKSMInitContainer(st, cluster, ns, pod, uid, node, memLim)
			}
		}
	}

	// Retire the OOM incident series for pods NOT selected this tick (intensity-fraction may
	// change which pods are affected; when oom_kill is entirely inactive, retire all of them).
	if !oomActive {
		st.DropWhere(func(name string, lbls map[string]string) bool {
			return name == "kube_pod_container_status_last_terminated_reason" && lbls["reason"] == "OOMKilled"
		})
	} else {
		// oom_kill is active but only a fraction of pods are affected: retire series for any
		// pod whose uid is no longer in the affected set (covers intensity changes between ticks).
		st.DropWhere(func(name string, lbls map[string]string) bool {
			return name == "kube_pod_container_status_last_terminated_reason" &&
				lbls["reason"] == "OOMKilled" &&
				!oomAffectedUIDs[lbls["uid"]]
		})
	}
}

// emitKSMInitContainer emits the kube_pod_init_container_* family for one init container ("init") on
// a pod (the live-reference audit §Pod — Init Containers). At steady state the init container has run to
// completion: status_running=0, status_waiting=0, status_terminated=1, terminated_reason=Completed,
// status_ready=1, restarts=0. _info carries the container image identity; resource_* carry the
// requests/limits with the node label, like the main-container series.
func emitKSMInitContainer(st *state.State, cluster, ns, pod, uid, node string, mem float64) {
	const initC = "init"
	idLbls := merge(ksmLabels(cluster), map[string]string{
		"namespace": ns, "pod": pod, "container": initC, "uid": uid,
	})
	initImage := "ghcr.io/synthkit/init:latest"
	st.Set("kube_pod_init_container_info", merge(idLbls, map[string]string{
		"image": initImage, "image_spec": initImage,
		"image_id":     "ghcr.io/synthkit/init@sha256:" + fmt.Sprintf("%064x", nodeAssignment(ns, pod+"init", 0, 0x7fffffff)),
		"container_id": "containerd://" + fmt.Sprintf("%064x", nodeAssignment(ns, pod+"initid", 0, 0x7fffffff)),
	}), 1)
	st.Set("kube_pod_init_container_status_ready", idLbls, 1)
	st.Set("kube_pod_init_container_status_running", idLbls, 0)
	st.Set("kube_pod_init_container_status_terminated", idLbls, 1)
	st.Set("kube_pod_init_container_status_waiting", idLbls, 0)
	st.Set("kube_pod_init_container_status_terminated_reason", merge(idLbls, map[string]string{
		"reason": "Completed",
	}), 1)
	st.Add("kube_pod_init_container_status_restarts_total", idLbls, 0)

	for _, rl := range []struct {
		metric         string
		cpuVal, memVal float64
	}{
		{"kube_pod_init_container_resource_requests", 0.05, mem * 0.1},
		{"kube_pod_init_container_resource_limits", 0.1, mem * 0.25},
	} {
		st.Set(rl.metric, merge(ksmLabels(cluster), map[string]string{
			"namespace": ns, "pod": pod, "container": initC, "node": node,
			"resource": "cpu", "unit": "core", "uid": uid,
		}), rl.cpuVal)
		st.Set(rl.metric, merge(ksmLabels(cluster), map[string]string{
			"namespace": ns, "pod": pod, "container": initC, "node": node,
			"resource": "memory", "unit": "byte", "uid": uid,
		}), rl.memVal)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────────

// substrateReps returns the per-deployment pod count. App workloads (fwl != nil) emit one pod per
// declared/live replica. Substrate deployments (fwl == nil) emit ONE pod per NODE.
func substrateReps(fwl *fixture.Workload, replicas, nodeCount int) int {
	if fwl == nil {
		return nodeCount
	}
	// A DaemonSet runs exactly one pod per schedulable node (its Replicas is zeroed at resolve time
	// so it never inflates node demand) — use the live node count.
	if wlController(*fwl) == "daemonset" {
		return nodeCount
	}
	if fwl.Replicas > 0 {
		return fwl.Replicas
	}
	return replicas
}

// synthPodName returns a deterministic pod name for a deployment replica when no fixture
// PodName is available.
func synthPodName(deploy string, ri int) string {
	rsHash := 0
	for _, c := range deploy {
		rsHash = (rsHash*31 + int(c)) & 0xfffffff
	}
	podHash := (rsHash + ri*0x1234) & 0xfffff
	return fmt.Sprintf("%s-%08x-%05x", deploy, rsHash, podHash)
}

// splitHostname splits "ip-10-0-1-2.region.compute.internal" into ["ip-10-0-1-2", "region", ...].
func splitHostname(host string) []string {
	var parts []string
	start := 0
	for i, c := range host {
		if c == '.' {
			parts = append(parts, host[start:i])
			start = i + 1
		}
	}
	if start < len(host) {
		parts = append(parts, host[start:])
	}
	return parts
}

// containsStr returns true if sub is a substring of s.
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
