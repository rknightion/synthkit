// SPDX-License-Identifier: AGPL-3.0-only

package k8saddon_test

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/k8saddon"
)

// buildCluster returns a Cluster with one substrate workload ("cert-manager", 2 pods)
// and one real node so PodIdentities yields a non-empty Node and PodIP.
func buildCluster() *fixture.Cluster {
	nodes := []fixture.Node{
		{Hostname: "ip-10-0-1-1.us-east-1.compute.internal"},
		{Hostname: "ip-10-0-1-2.us-east-1.compute.internal"},
	}
	wl := fixture.Workload{
		Name:      "cert-manager",
		Namespace: "cert-manager",
		Replicas:  2,
		Container: "cert-manager",
		PodNames:  []string{"cert-manager-abc-aa111", "cert-manager-abc-bb222"},
		NodeIdx:   []int{0, 1},
	}
	return &fixture.Cluster{
		Name:               "test-cluster",
		Nodes:              nodes,
		SubstrateWorkloads: []fixture.Workload{wl},
	}
}

// TestLookupSubstrateWorkload_found verifies that a known workload is found.
func TestLookupSubstrateWorkload_found(t *testing.T) {
	cl := buildCluster()
	wl, ok := k8saddon.LookupSubstrateWorkload(cl, "cert-manager")
	if !ok {
		t.Fatal("expected to find cert-manager, got false")
	}
	if wl.Name != "cert-manager" {
		t.Errorf("unexpected Name: %q", wl.Name)
	}
}

// TestLookupSubstrateWorkload_absent verifies that a missing workload returns false.
func TestLookupSubstrateWorkload_absent(t *testing.T) {
	cl := buildCluster()
	_, ok := k8saddon.LookupSubstrateWorkload(cl, "no-such-workload")
	if ok {
		t.Fatal("expected false for absent workload, got true")
	}
}

// TestStampPods_oneMapPerPod verifies correct count and pod/namespace/container/instance labels.
func TestStampPods_oneMapPerPod(t *testing.T) {
	cl := buildCluster()
	base := map[string]string{"cluster": "test-cluster", "job": "cert-manager"}
	result := k8saddon.StampPods(cl, "cert-manager", base, 9402)

	if len(result) != 2 {
		t.Fatalf("want 2 maps (one per pod), got %d", len(result))
	}
	for i, m := range result {
		if m["namespace"] != "cert-manager" {
			t.Errorf("[%d] want namespace=cert-manager, got %q", i, m["namespace"])
		}
		if m["container"] != "cert-manager" {
			t.Errorf("[%d] want container=cert-manager, got %q", i, m["container"])
		}
		if m["pod"] == "" {
			t.Errorf("[%d] pod label must not be empty", i)
		}
		if !strings.HasSuffix(m["instance"], ":9402") {
			t.Errorf("[%d] instance must end with :9402, got %q", i, m["instance"])
		}
		// instance must have a non-empty IP part
		if strings.HasPrefix(m["instance"], ":") {
			t.Errorf("[%d] instance IP part must not be empty, got %q", i, m["instance"])
		}
	}
}

// TestStampPods_nodeLabel verifies node label is set when Node is non-empty.
func TestStampPods_nodeLabel(t *testing.T) {
	cl := buildCluster()
	base := map[string]string{}
	result := k8saddon.StampPods(cl, "cert-manager", base, 9402)
	if len(result) < 1 {
		t.Fatal("expected at least one result")
	}
	// All pods have a node assignment (NodeIdx=[0,1], nodes are non-empty)
	for i, m := range result {
		if m["node"] == "" {
			t.Errorf("[%d] node label must not be empty when node is assigned, got %q", i, m["node"])
		}
	}
}

// TestStampPods_nodeLabelOmittedWhenEmpty verifies that node is omitted when Node=="".
func TestStampPods_nodeLabelOmittedWhenEmpty(t *testing.T) {
	// Build a cluster with no nodes so PodIdentities returns Node=="".
	wl := fixture.Workload{
		Name:      "coredns",
		Namespace: "kube-system",
		Replicas:  1,
		Container: "coredns",
		PodNames:  []string{"coredns-abc-001"},
		NodeIdx:   []int{}, // no node assignment
	}
	cl := &fixture.Cluster{
		Name:               "no-nodes",
		Nodes:              nil,
		SubstrateWorkloads: []fixture.Workload{wl},
	}
	base := map[string]string{"job": "coredns"}
	result := k8saddon.StampPods(cl, "coredns", base, 9153)
	if len(result) != 1 {
		t.Fatalf("want 1 map, got %d", len(result))
	}
	if _, hasNode := result[0]["node"]; hasNode {
		t.Errorf("node label must be omitted when Node is empty, got %q", result[0]["node"])
	}
}

// TestStampPods_baseNotMutated verifies that the caller's base map is never mutated.
func TestStampPods_baseNotMutated(t *testing.T) {
	cl := buildCluster()
	base := map[string]string{"cluster": "test-cluster"}
	snapshot := make(map[string]string, len(base))
	for k, v := range base {
		snapshot[k] = v
	}
	_ = k8saddon.StampPods(cl, "cert-manager", base, 9402)
	for k, v := range snapshot {
		if base[k] != v {
			t.Errorf("base[%q] was mutated: want %q, got %q", k, v, base[k])
		}
	}
	if len(base) != len(snapshot) {
		t.Errorf("base grew: was %d keys, now %d", len(snapshot), len(base))
	}
}

// TestStampPods_absentWorkload verifies that nil is returned for an absent workload.
func TestStampPods_absentWorkload(t *testing.T) {
	cl := buildCluster()
	base := map[string]string{"job": "no-such"}
	result := k8saddon.StampPods(cl, "no-such", base, 9402)
	if result != nil {
		t.Errorf("expected nil for absent workload, got %v", result)
	}
}

// TestStampLeader_firstPodOnly verifies StampLeader returns exactly one map (the first pod).
func TestStampLeader_firstPodOnly(t *testing.T) {
	cl := buildCluster()
	base := map[string]string{"cluster": "test-cluster"}
	result := k8saddon.StampLeader(cl, "cert-manager", base, 9402)
	if len(result) != 1 {
		t.Fatalf("StampLeader must return exactly 1 map, got %d", len(result))
	}
	// Must match the first pod name from the workload.
	if result[0]["pod"] != "cert-manager-abc-aa111" {
		t.Errorf("StampLeader must return first pod, got pod=%q", result[0]["pod"])
	}
	if !strings.HasSuffix(result[0]["instance"], ":9402") {
		t.Errorf("instance must end with :9402, got %q", result[0]["instance"])
	}
}

// TestStampLeader_absentWorkload verifies nil is returned for an absent workload.
func TestStampLeader_absentWorkload(t *testing.T) {
	cl := buildCluster()
	result := k8saddon.StampLeader(cl, "absent", map[string]string{}, 9402)
	if result != nil {
		t.Errorf("expected nil for absent workload, got %v", result)
	}
}

// TestStampLeader_noPods verifies nil is returned when the workload has no pods.
func TestStampLeader_noPods(t *testing.T) {
	wl := fixture.Workload{
		Name:      "empty-wl",
		Namespace: "default",
		Replicas:  0,
		PodNames:  nil,
	}
	cl := &fixture.Cluster{
		Name:               "c",
		SubstrateWorkloads: []fixture.Workload{wl},
	}
	result := k8saddon.StampLeader(cl, "empty-wl", map[string]string{}, 9402)
	if result != nil {
		t.Errorf("expected nil for empty pod list, got %v", result)
	}
}

// TestStampPodsContainer_overridesContainer verifies that StampPodsContainer replaces
// the container label with the explicitly provided name.
func TestStampPodsContainer_overridesContainer(t *testing.T) {
	cl := buildCluster() // cert-manager workload has Container="cert-manager"
	base := map[string]string{"job": "cert-manager"}
	result := k8saddon.StampPodsContainer(cl, "cert-manager", "redis_exporter", base, 9402)
	if len(result) != 2 {
		t.Fatalf("want 2 maps, got %d", len(result))
	}
	for i, m := range result {
		if m["container"] != "redis_exporter" {
			t.Errorf("[%d] want container=redis_exporter, got %q", i, m["container"])
		}
		// Other labels must still be present.
		if !strings.HasSuffix(m["instance"], ":9402") {
			t.Errorf("[%d] instance must end with :9402, got %q", i, m["instance"])
		}
		if m["pod"] == "" {
			t.Errorf("[%d] pod must not be empty", i)
		}
	}
}

// TestStampPodsContainer_absentWorkload verifies nil is returned for an absent workload.
func TestStampPodsContainer_absentWorkload(t *testing.T) {
	cl := buildCluster()
	result := k8saddon.StampPodsContainer(cl, "absent", "sidecar", map[string]string{}, 9402)
	if result != nil {
		t.Errorf("expected nil for absent workload, got %v", result)
	}
}

// TestStampPods_distinctInstances verifies that each pod gets a distinct instance label.
func TestStampPods_distinctInstances(t *testing.T) {
	cl := buildCluster()
	result := k8saddon.StampPods(cl, "cert-manager", map[string]string{}, 9402)
	if len(result) < 2 {
		t.Skip("need ≥2 pods for this test")
	}
	if result[0]["instance"] == result[1]["instance"] {
		t.Errorf("each pod must have a distinct instance label, got duplicate: %q", result[0]["instance"])
	}
}
