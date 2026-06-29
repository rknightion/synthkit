// SPDX-License-Identifier: AGPL-3.0-only

package fleetmgmt

import (
	"fmt"
	"sort"

	"github.com/rknightion/synthkit/internal/fixture"
)

// roleSpec is one Alloy collector role a k8s-monitoring deploy would create. Grounded in
// grafana/k8s-monitoring-helm 3.8.9 (templates/collectors/_collector_helpers.tpl): a role's
// workload exists iff its enabling feature is on; only DaemonSet roles run one-per-node.
type roleSpec struct {
	name     string // "alloy-metrics" | "alloy-singleton" | "alloy-logs" | "alloy-profiles" | "alloy-receiver"
	workload string // "statefulset" | "deployment" | "daemonset"
	perNode  bool   // true ⇒ one collector per live node (DaemonSet)
}

// enabledRoles maps the blueprint's k8s-monitoring feature toggles to the set of Alloy
// collector roles a real deploy would register, in a stable (alphabetical-by-name) order.
//
// Routing (k8s-monitoring 3.8.9 values.yaml `collector:` defaults):
//
//	cluster_metrics            → alloy-metrics    (statefulset, fixed replicas; never node-scaled)
//	cluster_events             → alloy-singleton  (deployment, replicas:1)
//	pod_logs | node_logs       → alloy-logs       (daemonset, per node)
//	profiling                  → alloy-profiles   (daemonset, per node)
//	application_observability  → alloy-receiver   (deployment by default; daemonset if recvDS)
func enabledRoles(feat map[string]bool, recvDS bool) []roleSpec {
	var out []roleSpec
	if feat["cluster_metrics"] {
		out = append(out, roleSpec{"alloy-metrics", "statefulset", false})
	}
	if feat["cluster_events"] {
		out = append(out, roleSpec{"alloy-singleton", "deployment", false})
	}
	if feat["pod_logs"] || feat["node_logs"] {
		out = append(out, roleSpec{"alloy-logs", "daemonset", true})
	}
	if feat["profiling"] {
		out = append(out, roleSpec{"alloy-profiles", "daemonset", true})
	}
	if feat["application_observability"] {
		if recvDS {
			out = append(out, roleSpec{"alloy-receiver", "daemonset", true})
		} else {
			out = append(out, roleSpec{"alloy-receiver", "deployment", false})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// DefaultRelease / DefaultNamespace are the k8s-monitoring chart defaults used when a
// blueprint does not override them.
const (
	DefaultRelease   = "grafana-k8s-monitoring"
	DefaultNamespace = "monitoring"
)

// K8sRoster builds the deterministic FM collector roster a k8s-monitoring deploy would
// register for the given live node set + enabled features. Identities match the chart's
// _collector_remoteConfig.tpl GCLOUD_FM_COLLECTOR_ID format (DD3); the `resource-` prefix
// FM adds server-side is NOT included (real Alloy posts the bare id).
//
// release/namespace fall back to the chart defaults when empty. version is the cluster's
// canonical "v"-prefixed Alloy version (km.AlloyVersion); empty ⇒ package collectorVersion.
func K8sRoster(seed, release, cluster, namespace string, nodes []fixture.Node, km fixture.K8sMonitoring) []Collector {
	if release == "" {
		release = DefaultRelease
	}
	if namespace == "" {
		namespace = DefaultNamespace
	}
	version := km.AlloyVersion
	if version == "" {
		version = collectorVersion
	}
	prefix := release + "-" + cluster + "-" + namespace // <R>-<C>-<N>

	// mk builds one Collector. Fields reproduce a real k8s-monitoring collector verbatim:
	//   - instance: pod IP:port (live-captured form); synthesized deterministically per collector.
	//   - App/Workload/Controller/Pod: the metric-label + FM-attr identity (see Collector docs).
	// id is the FM collector id (chart _collector_remoteConfig.tpl format); pod is the k8s pod name
	// (DIFFERENT from id for DaemonSets, where id ends in the node name but the pod is release-role-hash).
	mk := func(id, role, controller, pod, os string) Collector {
		instIP := fixture.PrivateIP(seed, cluster, role, pod)
		return Collector{
			ID:           id,
			Instance:     instIP + ":12345",
			OS:           os,
			Cluster:      cluster,
			Version:      version,
			Namespace:    namespace,
			App:          role,
			Workload:     release + "-" + role,
			Controller:   controller,
			Pod:          pod,
			Release:      release,
			ChartVersion: km.ChartVersion,
		}
	}

	var out []Collector
	for _, r := range enabledRoles(km.Features, km.ReceiverAsDaemonset) {
		switch r.workload {
		case "daemonset":
			for _, n := range nodes {
				// id ends in the node name; the pod name is release-role-<5hex> (per-node DaemonSet pod).
				id := fmt.Sprintf("%s-%s-%s", prefix, r.name, n.Hostname)
				pod := fmt.Sprintf("%s-%s-%s", release, r.name, fixture.HexID(seed, 5, "fm-k8s-pod", cluster, r.name, n.Hostname))
				out = append(out, mk(id, r.name, r.workload, pod, nodeOS(n)))
			}
		case "statefulset":
			reps := km.MetricsReplicas
			if reps <= 0 {
				reps = 1
			}
			for i := 0; i < reps; i++ {
				pod := fmt.Sprintf("%s-%s-%d", release, r.name, i)
				out = append(out, mk(prefix+"-"+pod, r.name, r.workload, pod, "linux"))
			}
		case "deployment":
			// One replica (replicas:1 for singleton; synth models receiver as 1 too). Pod name
			// carries a deterministic replicaset-hash + 5-char suffix so the id is stable per seed.
			rsHash := fixture.HexID(seed, 10, "fm-k8s", cluster, r.name, "rs")
			podSuffix := fixture.HexID(seed, 5, "fm-k8s", cluster, r.name, "pod")
			pod := fmt.Sprintf("%s-%s-%s-%s", release, r.name, rsHash, podSuffix)
			out = append(out, mk(prefix+"-"+pod, r.name, r.workload, pod, "linux"))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// nodeOS returns the node's OS, defaulting to "linux" when unset.
func nodeOS(n fixture.Node) string {
	if n.OS == "" {
		return "linux"
	}
	return n.OS
}
