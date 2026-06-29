// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster_test

import (
	"testing"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/scale"
)

// TestDaemonsetLivePerNode verifies that, on the LIVE path (w.Scaling != nil), a daemonset workload
// yields exactly one pod per current node with stable DaemonSetPodName names — NOT reverted to the
// ReplicaSet form by liveCluster's pod-name regeneration (the live-revert bug, spec §3.4 / §7 B3).
// Names key on the node hostname so they survive node-count changes, and each daemonset pod carries
// a `node` label so retireScaledAway drops it when its node is retired.
func TestDaemonsetLivePerNode(t *testing.T) {
	cl := scalableCluster()
	// Add a daemonset workload (Replicas 0 / per-node, as the resolver leaves it). Its pod names +
	// NodeIdx are minted per current node.
	ds := fixture.Workload{Name: "ds-agent", Namespace: "obs", Controller: "daemonset"}
	ds.PodNames = fixture.WorkloadPodNames(cl.Seed+":"+cl.Name, ds, cl.Nodes)
	for ni := range cl.Nodes {
		ds.NodeIdx = append(ds.NodeIdx, ni)
	}
	cl.Workloads = append(cl.Workloads, ds)

	wl := cl.Workloads[0].Name

	c := buildConstruct(t, cl)
	sc := scale.New()
	sc.Set(map[string]int{wl: 49}) // scale the deployment → 7 nodes (crosses prod floor 6)
	mc := &coretest.MetricCapture{}
	tickWorld(t, c, liveWorld(mc, &coretest.LogCapture{}, nil, sc))

	// Node set at this tick.
	wantNodes := fixture.DeriveNodesFloor(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, 49, fixture.EnvNodeFloor(cl.Env.Weight))
	if len(wantNodes) != 7 {
		t.Fatalf("test premise: expected 7 derived nodes, got %d", len(wantNodes))
	}

	// Expected daemonset pod = one per node, named by node hostname.
	wantDS := map[string]string{} // pod name → node hostname
	for _, n := range wantNodes {
		wantDS[fixture.DaemonSetPodName(cl.Seed+":"+cl.Name, "ds-agent", n.Hostname)] = n.Hostname
	}

	got := map[string]string{}
	for _, s := range mc.Find("kube_pod_info") {
		if s.Labels["namespace"] == "obs" {
			got[s.Labels["pod"]] = s.Labels["node"]
		}
	}
	if len(got) != 7 {
		t.Fatalf("daemonset pods: got %d, want 7 (one per node): %v", len(got), got)
	}
	for pod, node := range wantDS {
		gn, ok := got[pod]
		if !ok {
			t.Errorf("daemonset pod %q missing (live-revert bug — names must use DaemonSetPodName)", pod)
			continue
		}
		if gn != node {
			t.Errorf("daemonset pod %q on node %q, want %q", pod, gn, node)
		}
	}
	// owner_kind must be DaemonSet.
	for _, s := range mc.Find("kube_pod_owner") {
		if s.Labels["namespace"] == "obs" && s.Labels["owner_kind"] != "DaemonSet" {
			t.Errorf("daemonset pod %q owner_kind=%q want DaemonSet", s.Labels["pod"], s.Labels["owner_kind"])
		}
	}
	// kube_daemonset_status_desired_number_scheduled = node count (7).
	for _, s := range mc.Find("kube_daemonset_status_desired_number_scheduled") {
		if s.Labels["daemonset"] == "ds-agent" && s.Value != 7 {
			t.Errorf("ds-agent desired_number_scheduled=%v want 7", s.Value)
		}
	}
}
