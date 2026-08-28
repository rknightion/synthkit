// SPDX-License-Identifier: AGPL-3.0-only

// Package k8saddon provides helpers for addon constructs that need to stamp their own
// metrics with per-pod join labels derived from the cluster's substrate workloads.
//
// Addon constructs call LookupSubstrateWorkload / StampPods / StampLeader /
// StampPodsContainer — they never iterate fixture.Cluster.SubstrateWorkloads directly.
package k8saddon

import (
	"fmt"

	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/loki"
)

// LokiPodLogConfig describes the low-cardinality stream identity and the pod identity
// carried as structured metadata by the Loki-native pod-log transport. AppName and
// ServiceName are optional because the collector only includes those dimensions when the
// pod has the corresponding application/service identity.
type LokiPodLogConfig struct {
	Cluster          string
	Namespace        string
	Container        string
	AppName          string
	ServiceName      string
	ServiceNamespace string
	Stream           string
	Flags            string
	Pod              string
}

// NewLokiPodLogStream returns a stream in the captured podLogsViaLoki wire shape.
//
// The stream labels intentionally use the classic Loki-native spellings (namespace,
// container, stream) rather than the destination-promoted OTLP spellings. Pod identity
// is structured metadata, not a stream label. Lines are cloned so this mechanic never
// mutates a caller-owned line or metadata map; bodies and timestamps are unchanged.
func NewLokiPodLogStream(cfg LokiPodLogConfig, lines []loki.Line) loki.Stream {
	labels := make(map[string]string, 10)
	put := func(key, value string) {
		if value != "" {
			labels[key] = value
		}
	}

	put("cluster", cfg.Cluster)
	put("k8s_cluster_name", cfg.Cluster)
	put("namespace", cfg.Namespace)
	put("container", cfg.Container)
	if cfg.Namespace != "" && cfg.Container != "" {
		labels["job"] = cfg.Namespace + "/" + cfg.Container
	}
	put("app_kubernetes_io_name", cfg.AppName)
	put("service_name", cfg.ServiceName)
	put("service_namespace", cfg.ServiceNamespace)
	put("stream", cfg.Stream)
	put("flags", cfg.Flags)

	clonedLines := make([]loki.Line, len(lines))
	for i, line := range lines {
		meta := cloneMap(line.Meta)
		if cfg.Pod != "" {
			meta["pod"] = cfg.Pod
			if cfg.Namespace != "" && cfg.Container != "" {
				meta["service_instance_id"] = cfg.Namespace + "." + cfg.Pod + "." + cfg.Container
			}
		}
		line.Meta = meta
		clonedLines[i] = line
	}

	return loki.Stream{Labels: labels, Lines: clonedLines}
}

// LookupSubstrateWorkload finds an addon/baseline workload by name in
// cl.SubstrateWorkloads. Returns the workload and true if found, or zero value and
// false if absent.
func LookupSubstrateWorkload(cl *fixture.Cluster, name string) (fixture.Workload, bool) {
	for _, wl := range cl.SubstrateWorkloads {
		if wl.Name == name {
			return wl, true
		}
	}
	return fixture.Workload{}, false
}

// StampPods returns one label-set per pod of the named substrate workload: base cloned
// plus pod/namespace/container/node/instance(=PodIP:port). Returns nil if the workload
// is absent (caller then falls back to a single cluster-scoped series). Never mutates
// base.
func StampPods(cl *fixture.Cluster, name string, base map[string]string, port int) []map[string]string {
	wl, ok := LookupSubstrateWorkload(cl, name)
	if !ok {
		return nil
	}
	return stampIdentities(wl.PodIdentities(cl.Nodes), base, port)
}

// StampLeader is StampPods limited to the leader (first) pod — for leader-elected
// metrics that only the leader emits. Returns nil if the workload is absent or has no
// pods.
func StampLeader(cl *fixture.Cluster, name string, base map[string]string, port int) []map[string]string {
	all := StampPods(cl, name, base, port)
	if len(all) == 0 {
		return nil
	}
	return all[:1]
}

// StampPodsContainer is StampPods but overrides the container label (for sidecars, e.g.
// argocd redis_exporter). Returns nil if the workload is absent.
func StampPodsContainer(cl *fixture.Cluster, name, container string, base map[string]string, port int) []map[string]string {
	wl, ok := LookupSubstrateWorkload(cl, name)
	if !ok {
		return nil
	}
	ids := wl.PodIdentities(cl.Nodes)
	// Override the container in each identity before stamping.
	overridden := make([]fixture.PodIdentity, len(ids))
	for i, id := range ids {
		id.Container = container
		overridden[i] = id
	}
	return stampIdentities(overridden, base, port)
}

// stampIdentities converts a slice of PodIdentity values into cloned label maps.
// Each map is a fresh clone of base with pod/namespace/container/node/instance added.
// node is omitted when empty (ARCHITECTURE I13: absent dimension → omit, never "").
func stampIdentities(ids []fixture.PodIdentity, base map[string]string, port int) []map[string]string {
	out := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		m := cloneMap(base)
		m["pod"] = id.Pod
		m["namespace"] = id.Namespace
		m["container"] = id.Container
		if id.Node != "" {
			m["node"] = id.Node
		}
		m["instance"] = fmt.Sprintf("%s:%d", id.PodIP, port)
		out = append(out, m)
	}
	return out
}

// cloneMap returns a shallow copy of m.
func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
