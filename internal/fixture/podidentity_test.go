// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import "testing"

// Regression for the substrate-NodeIdx join bug: addon workloads mint PodNames at resolve
// time with nil NodeIdx. PodIdentities must then derive placement via the SAME nodeAssignment
// formula k8scluster uses (ns, workloadName, ri, numNodes) — NOT fall back to node 0 — so the
// addon `node` label and `instance` podIP are byte-identical to kube_pod_info.
func TestPodIdentitiesEmptyNodeIdxMatchesNodeAssignment(t *testing.T) {
	nodes := []Node{{Hostname: "n0"}, {Hostname: "n1"}, {Hostname: "n2"}}
	wl := Workload{
		Name: "karpenter", Namespace: "kube-system", Replicas: 2, Container: "controller",
		PodNames: []string{"karpenter-aaaaaaaaaa-11111", "karpenter-aaaaaaaaaa-22222"},
		// NodeIdx intentionally nil (resolve-time substrate minting).
	}
	ids := wl.PodIdentities(nodes)
	if len(ids) != 2 {
		t.Fatalf("want 2 identities, got %d", len(ids))
	}
	for i, id := range ids {
		wantIdx := nodeAssignment("kube-system", "karpenter", i, len(nodes))
		if id.Node != nodes[wantIdx].Hostname {
			t.Errorf("id[%d].Node=%q want %q (nodeAssignment idx %d)", i, id.Node, nodes[wantIdx].Hostname, wantIdx)
		}
		if id.Node == "" {
			t.Errorf("id[%d].Node must not be empty (the bug: fell back to 0 + empty)", i)
		}
		if want := PodIP(wantIdx, "kube-system", id.Pod, i); id.PodIP != want {
			t.Errorf("id[%d].PodIP=%q want %q (must match kube_pod_info.pod_ip formula)", i, id.PodIP, want)
		}
	}
}

func TestPodIdentitiesDeployment(t *testing.T) {
	nodes := []Node{{Hostname: "ip-10-1-1-1"}, {Hostname: "ip-10-1-2-2"}}
	wl := Workload{
		Name: "cert-manager", Namespace: "cert-manager", Replicas: 2,
		Controller: "", Container: "cert-manager",
		PodNames: []string{"cert-manager-abc123def0-aa111", "cert-manager-abc123def0-bb222"},
		NodeIdx:  []int{0, 1},
	}
	ids := wl.PodIdentities(nodes)
	if len(ids) != 2 {
		t.Fatalf("want 2 identities, got %d", len(ids))
	}
	if ids[0].Pod != "cert-manager-abc123def0-aa111" || ids[0].Namespace != "cert-manager" {
		t.Errorf("bad id[0]: %+v", ids[0])
	}
	if ids[0].Container != "cert-manager" {
		t.Errorf("want container cert-manager, got %q", ids[0].Container)
	}
	if ids[0].Node != "ip-10-1-1-1" || ids[1].Node != "ip-10-1-2-2" {
		t.Errorf("node placement wrong: %+v %+v", ids[0], ids[1])
	}
	if ids[0].PodIP == "" || ids[0].PodIP == ids[1].PodIP {
		t.Errorf("pod IPs must be present and distinct: %q %q", ids[0].PodIP, ids[1].PodIP)
	}
}

func TestPodIdentitiesContainerFallback(t *testing.T) {
	// When Container is "", it falls back to the workload Name.
	nodes := []Node{{Hostname: "ip-10-1-0-1"}}
	wl := Workload{
		Name: "karpenter", Namespace: "kube-system", Replicas: 1,
		Container: "",
		PodNames:  []string{"karpenter-abc-001"},
		NodeIdx:   []int{0},
	}
	ids := wl.PodIdentities(nodes)
	if len(ids) != 1 {
		t.Fatalf("want 1 identity, got %d", len(ids))
	}
	if ids[0].Container != "karpenter" {
		t.Errorf("want container karpenter (fallback to Name), got %q", ids[0].Container)
	}
}

func TestPodIP(t *testing.T) {
	// same node, different pods/replica-index → distinct (nodeAssignment varies by pod+ri)
	a := PodIP(0, "cert-manager", "cert-manager-abc-aa111", 0)
	b := PodIP(0, "cert-manager", "cert-manager-abc-bb222", 1)
	if a == b {
		t.Fatalf("distinct pods on same node must get distinct IPs: %q %q", a, b)
	}
	if PodIP(0, "cert-manager", "cert-manager-abc-aa111", 0) != a {
		t.Fatalf("PodIP must be deterministic")
	}
	if got := a; len(got) < 8 || got[:5] != "10.1." {
		t.Errorf("want 10.1.x.y form (VPC-CNI realism), got %q", got)
	}
}
