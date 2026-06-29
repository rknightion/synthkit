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
)

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
