// SPDX-License-Identifier: AGPL-3.0-only

package fleetmgmt

// Collector is the exported roster entry. The construct controller and the metric
// emitter must use byte-identical collector identities; this type is the shared seam.
//
// Fields match the collectorSpec internal type 1-to-1 (ID → id, Instance → instance,
// OS → os, Cluster → cluster). Healthy is deliberately omitted: the API controller
// registers whatever the emitter declares healthy; health-state is an emitter concern.
//
// The k8s-monitoring fidelity fields are zero-valued for standalone collectors_per_os
// entries — behaviour unchanged. For k8s-mirror collectors they reproduce, verbatim, what a
// real grafana/k8s-monitoring deploy reports (FM register attributes from the chart's
// _collector_remoteConfig.tpl, metric labels from a live reference capture 2026-06-15).
type Collector struct {
	ID       string // "fleet-<os>-<i:02d>-<hexsuffix>" (standalone) or chart-derived id (k8s)
	Instance string // pod IP:port (k8s) or "alloy-<os>-<i:02d>-<hexsuffix>" (standalone)
	OS       string // "linux" | "windows" | "darwin"
	Cluster  string // empty when no cluster fixture is bound (standalone fleet)

	// k8s-monitoring fidelity fields (zero-valued for standalone collectors_per_os entries).
	Version      string // per-collector Alloy version; "" ⇒ package collectorVersion
	Namespace    string // k8s namespace ("" ⇒ standalone)
	App          string // short role, e.g. "alloy-logs"      (metric label `app`; FM attr `workloadName`)
	Workload     string // full, e.g. "<release>-alloy-logs"  (metric label `workload`)
	Controller   string // "daemonset"|"statefulset"|"deployment" (FM attr `workloadType`; metric `workload_type` derives)
	Pod          string // pod name (metric label `pod`)
	Release      string // helm release, e.g. "grafana-k8s-monitoring" (FM attr `release`)
	ChartVersion string // k8s-monitoring chart version (FM attr `sourceVersion`)
}

// isK8s reports whether this is a k8s-monitoring mirror collector (vs a standalone machine).
func (c Collector) isK8s() bool { return c.App != "" }

// metricWorkloadType maps the controller type to the value the kube service-discovery relabeling
// stamps on metrics: a Deployment's pods are owned by a ReplicaSet, so the metric label reads
// "replicaset" (live-captured), while DaemonSet/StatefulSet read through unchanged.
func metricWorkloadType(controller string) string {
	if controller == "deployment" {
		return "replicaset"
	}
	return controller
}

// LocalAttributes returns the per-heartbeat attributes for the FM API (signals/fm.md, I13).
//
// The two "collector."-namespaced keys are the EXACT reserved system attributes a real Alloy
// reports via remotecfg getSystemAttributes() — the keys the FM UI reads for its "Operating
// System" and "Alloy version" columns. Sending a bare "os" key or omitting the version makes
// both columns show "unknown". The collector ID is NOT an attribute — it travels as the
// top-level `id` field on the request.
//
// For k8s-mirror collectors the remaining keys reproduce the chart's remotecfg `attributes`
// block verbatim (k8s-monitoring-helm _collector_remoteConfig.tpl): cluster, platform, source,
// sourceVersion, release, namespace, workloadName (=role), workloadType (=controller.type).
// Each is omitted when empty (I13 — absent ⇒ omitted, never "").
func (c Collector) LocalAttributes() map[string]string {
	version := c.Version
	if version == "" {
		version = collectorVersion
	}
	attrs := map[string]string{
		"collector.os":      c.OS,
		"collector.version": version,
	}
	addIf := func(k, v string) {
		if v != "" {
			attrs[k] = v
		}
	}
	addIf("cluster", c.Cluster)
	addIf("namespace", c.Namespace)
	if c.isK8s() {
		// chart-source vocabulary (camelCase); workloadType is the controller type (e.g.
		// "deployment"), NOT the metric "replicaset" projection.
		attrs["platform"] = "kubernetes"
		attrs["source"] = "k8s-monitoring"
		addIf("sourceVersion", c.ChartVersion)
		addIf("release", c.Release)
		addIf("workloadName", c.App)
		addIf("workloadType", c.Controller)
	}
	return attrs
}

// Roster derives the deterministic collector roster from seed, cluster, and perOS.
// The returned slice is byte-identical to the IDs the fleetmgmt construct emits as
// collector_id / instance labels — the fleet controller MUST use these exact identities
// so the API registration and the emitted metrics name the same collectors.
//
// seed and cluster are the same values passed to Build (fx.Seed and fx.Cluster.Name
// respectively; pass "" for cluster when fx.Cluster is nil).
// perOS maps OS name → count; unrecognised OS names and counts ≤ 0 are silently skipped.
func Roster(seed string, cluster string, perOS map[string]int) []Collector {
	specs := buildRoster(seed, cluster, perOS)
	out := make([]Collector, len(specs))
	for i, s := range specs {
		out[i] = Collector{
			ID:       s.id,
			Instance: s.instance,
			OS:       s.os,
			Cluster:  s.cluster,
		}
	}
	return out
}
