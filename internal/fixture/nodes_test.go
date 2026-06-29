// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import "testing"

func TestDeriveNodesCascadesAndIsOrdinalStable(t *testing.T) {
	groups := []NodeGroupSpec{{Name: "general", InstanceType: "m6i.large"}} // desired 0 ⇒ derive
	n8 := DeriveNodes("seed", "prod", groups, "us-east-1", 8)               // ceil(8/8)=1 ⇒ max(3,1)=3
	n40 := DeriveNodes("seed", "prod", groups, "us-east-1", 40)             // ceil(40/8)=5 ⇒ 5
	if len(n8) != 3 {
		t.Errorf("8 pods → %d nodes, want 3", len(n8))
	}
	if len(n40) != 5 {
		t.Errorf("40 pods → %d nodes, want 5", len(n40))
	}
	for i := 0; i < 3; i++ { // ordinal stability: first 3 identities unchanged under scaling
		if n8[i].InstanceID != n40[i].InstanceID {
			t.Errorf("node %d identity changed under scaling: %q != %q", i, n8[i].InstanceID, n40[i].InstanceID)
		}
	}
}

func TestDeriveNodesPinnedGroupIgnoresPods(t *testing.T) {
	groups := []NodeGroupSpec{{Name: "general", InstanceType: "m6i.large", Desired: 4}}
	if got := len(DeriveNodes("seed", "prod", groups, "us-east-1", 400)); got != 4 {
		t.Errorf("pinned desired=4 must ignore pod total: got %d", got)
	}
}

// TestDeriveNodesUniqueIPAndHostname guards the invariant the Grafana k8s-monitoring app
// relies on: every node in a cluster has a UNIQUE PrivateIP (hence a unique Hostname).
// The legacy PrivateIP space is only ~4000 addresses, so a large node group birthday-collides;
// a single 200-node group reliably produces duplicate (IP, Hostname) pairs (verified pre-fix).
// Two nodes sharing a hostname produce two kube_node_info series whose (cluster, node) key is
// identical → the app's label_replace(provider_id→"aws") backbone query collapses them to the
// same labelset → Prometheus 422 → the whole cluster row (incl. MEMORY column) renders blank.
func TestDeriveNodesUniqueIPAndHostname(t *testing.T) {
	// A single large group forces birthday collisions in the legacy ~4000-address space.
	bigGroup := []NodeGroupSpec{{Name: "general", InstanceType: "m6i.large", Desired: 200}}
	// A multi-AZ multi-group cluster (the live-confirmed shape: two AZ node groups).
	multiGroup := []NodeGroupSpec{
		{Name: "ng-use1a", InstanceType: "m6i.large", Desired: 60},
		{Name: "ng-use1c", InstanceType: "m6i.large", Desired: 60},
	}
	cases := []struct {
		name   string
		seed   string
		clust  string
		region string
		groups []NodeGroupSpec
	}{
		{"single-200", "seed", "prod-eks-use1", "eu-central-1", bigGroup},
		{"multi-az", "seed", "prod-eks-use1", "eu-central-1", multiGroup},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodes := DeriveNodes(tc.seed, tc.clust, tc.groups, tc.region, 0)
			ips := map[string]int{}
			hosts := map[string]int{}
			for i, n := range nodes {
				if j, dup := ips[n.PrivateIP]; dup {
					t.Errorf("duplicate PrivateIP %q: node %d (%s) and node %d (%s)",
						n.PrivateIP, j, nodes[j].Hostname, i, n.Hostname)
				}
				ips[n.PrivateIP] = i
				if j, dup := hosts[n.Hostname]; dup {
					t.Errorf("duplicate Hostname %q: node %d and node %d", n.Hostname, j, i)
				}
				hosts[n.Hostname] = i
			}
		})
	}
}

func TestLiveNodesScalesWithPodCount(t *testing.T) {
	c := &Cluster{
		Name: "c1", Region: "us-west-2", Seed: "s1",
		NodeGroups: []NodeGroupSpec{{Name: "general", InstanceType: "m6i.xlarge"}}, // Desired 0 ⇒ derive
		Workloads:  []Workload{{Name: "api", Replicas: 8}},
	}
	// declared (count returns declared): 8 pods → ceil(8/8)=1 → max(3,1)=3 nodes.
	base := LiveNodes(c, func(_ string, d int) int { return d })
	if len(base) != 3 {
		t.Fatalf("declared nodes = %d, want 3", len(base))
	}
	// scaled to 40 pods → ceil(40/8)=5 nodes; first 3 hostnames stable (only count grows).
	scaled := LiveNodes(c, func(_ string, _ int) int { return 40 })
	if len(scaled) != 5 {
		t.Fatalf("scaled nodes = %d, want 5", len(scaled))
	}
	for i := 0; i < 3; i++ {
		if scaled[i].Hostname != base[i].Hostname {
			t.Errorf("node %d identity changed under scale: %q vs %q", i, scaled[i].Hostname, base[i].Hostname)
		}
	}
}
