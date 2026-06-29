// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"fmt"
	"math"
	"strconv"
)

// EnvNodeFloor returns the minimum node count for the first (auto-sized) node group based
// on the environment weight. Production clusters (weight ≥ 1.0) get a 6-node floor;
// staging/canary (weight ≥ 0.5) get 4; dev/test keep the original 3-node floor.
// A zero weight (env absent / test fixtures) defaults to 3.
func EnvNodeFloor(envWeight float64) int {
	switch {
	case envWeight >= 1.0:
		return 6
	case envWeight >= 0.5:
		return 4
	default:
		return 3
	}
}

// DeriveNodes returns the deterministic node set for a cluster given a live pod total. It is the
// SINGLE source of node identities — both the k8s and EC2 constructs call it each tick with the
// current pod total, so they always agree byte-for-byte (I12: identities are pure seed+ordinal
// functions; only the COUNT is live). A node group with Desired>0 is pinned (ignores totalPods);
// otherwise the FIRST group's count derives as max(3, ceil(totalPods/8)) — matching the resolver.
// The floor is always 3; use LiveNodes (which reads c.Env.Weight) for env-weighted sizing.
func DeriveNodes(seed, clusterName string, groups []NodeGroupSpec, region string, totalPods int) []Node {
	return deriveNodesWithFloor(seed, clusterName, groups, region, totalPods, 3)
}

// DeriveNodesFloor is like DeriveNodes but applies an explicit floor instead of the hardcoded 3.
// Use this when you need env-weighted sizing — call EnvNodeFloor to get the right floor.
// DeriveNodes remains the stable public API for callers that don't need env-aware sizing.
func DeriveNodesFloor(seed, clusterName string, groups []NodeGroupSpec, region string, totalPods, floor int) []Node {
	return deriveNodesWithFloor(seed, clusterName, groups, region, totalPods, floor)
}

// deriveNodesWithFloor is the shared implementation: like DeriveNodes but applies the given
// floor instead of the hardcoded 3, so env-weighted sizing can share the same derivation.
func deriveNodesWithFloor(seed, clusterName string, groups []NodeGroupSpec, region string, totalPods, floor int) []Node {
	derived := int(math.Max(float64(floor), math.Ceil(float64(totalPods)/8)))
	anyDesired := false
	for _, g := range groups {
		if g.Desired > 0 {
			anyDesired = true
		}
	}
	// Guarantee every node in the cluster has a UNIQUE PrivateIP (hence a unique Hostname).
	// PrivateIP spans ~64k addresses (10.0.0.0/16, second octet pinned), but multiple groups/ordinals
	// still birthday-collide at a low rate (~1 per 200 nodes), and two nodes sharing an IP share a
	// hostname → two kube_node_info series with an identical (cluster, node) key, which the Grafana
	// k8s-monitoring app's provider_id-rewriting backbone query collapses to the same labelset →
	// Prometheus 422 → blank cluster row. On a collision we deterministically re-derive that node's IP
	// with an incrementing "dedup" salt (so the dedup loop is load-bearing, not belt-and-suspenders),
	// leaving the first-assigned node byte-identical (low -dump churn) and staying a pure function of
	// inputs (I12). firstFreeIP is a deterministic fallback that bounds the loop at space saturation.
	assigned := map[string]bool{}
	var nodes []Node
	for gi, g := range groups {
		count := g.Desired
		if !anyDesired && gi == 0 {
			count = derived
		}
		prov := g.Provisioner
		if prov == "" {
			prov = "managed"
		}
		nodeOS := g.OS
		if nodeOS == "" {
			nodeOS = "linux"
		}
		for n := range count {
			ord := strconv.Itoa(n)
			ip := PrivateIP(seed, clusterName, g.Name, ord)
			for k := 1; assigned[ip]; k++ {
				// Salt re-draws find a free slot almost immediately at realistic node counts. Cap the
				// re-draw attempts so a near-saturated space can never spin forever; fall back to a
				// deterministic linear probe that is guaranteed to terminate while any slot is free.
				if k > 4096 {
					ip = firstFreeIP(assigned)
					break
				}
				ip = PrivateIP(seed, clusterName, g.Name, ord, "dedup", strconv.Itoa(k))
			}
			assigned[ip] = true
			nodes = append(nodes, Node{
				InstanceID:   EC2InstanceID(seed, clusterName, g.Name, ord),
				Hostname:     NodeHostname(ip, region),
				PrivateIP:    ip,
				InstanceType: g.InstanceType,
				NodeGroup:    g.Name,
				UID:          NodeUID(seed, clusterName, g.Name, ord),
				Provisioner:  prov,
				OS:           nodeOS,
			})
		}
	}
	return nodes
}

// firstFreeIP returns the lowest 10.0.x.y address (x∈[0,256), y∈[4,254)) not already assigned —
// the exact shape PrivateIP mints. It is a deterministic fallback used only when salt re-derivation
// can't find a free slot (address space near-saturation, far beyond any real cluster), guaranteeing
// the dedup loop terminates with a unique IP while any slot remains free.
func firstFreeIP(assigned map[string]bool) string {
	for x := 0; x < 256; x++ {
		for y := 4; y < 254; y++ {
			ip := fmt.Sprintf("10.0.%d.%d", x, y)
			if !assigned[ip] {
				return ip
			}
		}
	}
	return "10.0.0.0" // space fully exhausted (>64k nodes — unreachable); degenerate fallback
}

// LiveNodes returns the cluster's node set for the current live pod total. count(name, declared)
// resolves each workload's live replica count (callers pass scale.Source.Count or core.World's
// equivalent); the summed total drives DeriveNodes. Falls back to the resolved c.Nodes when the
// cluster declares no node groups. Pure + deterministic: identical to DeriveNodes for the same
// total, so the k8s construct (metrics) and the fleet controller (registration) agree byte-for-byte.
// The env weight (from c.Env) sets the minimum node floor: production ≥ 6, staging ≥ 4, dev ≥ 3.
func LiveNodes(c *Cluster, count func(target string, declared int) int) []Node {
	total := 0
	for _, wl := range c.Workloads {
		n := count(wl.Name, wl.Replicas)
		if n > 0 {
			total += n
		}
	}
	floor := 3
	if c.Env != nil {
		floor = EnvNodeFloor(c.Env.Weight)
	}
	nodes := deriveNodesWithFloor(c.Seed, c.Name, c.NodeGroups, c.Region, total, floor)
	if len(nodes) == 0 {
		return c.Nodes
	}
	return nodes
}
