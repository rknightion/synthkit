// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster

import "github.com/rknightion/synthkit/internal/failuremode"

// FailureModes are the cluster-axis modes k8s_cluster responds to. All three are scoped to the
// cluster name (cl.Name — the ScopeSubstrate disambiguator) and bend EXISTING KSM series values
// only (no new series names or label keys; see ksm.go):
//   - oom_kill:       kube_pod_container_status_restarts_total climbs; kube_pod_status_reason
//     gains an OOMKilled=1 sample (new label VALUE on the existing reason key).
//   - pod_crashloop:  kube_pod_container_status_restarts_total climbs; kube_pod_status_phase flips
//     Running→0 / Pending→1 for affected pods.
//   - node_not_ready: kube_node_status_condition Ready{status=true}→0 / Ready{status=false}→1 for
//     one node; that node's pods go kube_pod_status_phase Running→0 / Pending→1.
var FailureModes = []failuremode.Mode{
	{Name: "oom_kill", Axis: failuremode.AxisCluster, Help: "containers OOM-killed; intensity selects fraction of pods affected (low intensity ⇒ a few pods); restart count climbs, status reason OOMKilled"},
	{Name: "pod_crashloop", Axis: failuremode.AxisCluster, Help: "pods crash-looping; intensity selects fraction of pods affected (low intensity ⇒ a few pods); restarts climb, phase Pending not Running"},
	{Name: "node_not_ready", Axis: failuremode.AxisCluster, Help: "a node flips NotReady; its pods go Pending"},
}
