// SPDX-License-Identifier: AGPL-3.0-only

package clusterautoscaler_test

// clusterautoscaler_test.go — construct invariant tests for the cluster_autoscaler construct.
//
// Test inventory:
//   (a) Exact series name inventory (all expected names, no extras).
//   (b) Three intentionally-omitted metrics are absent.
//   (c) nodes_count == fixture node count (for state=ready and state=schedulable).
//   (d) min/max nodes config reflected in emitted cpu/memory limit series.
//   (e) rest_client_* present.
//   (f) cluster + k8s_cluster_name on every series; NO blueprint label.
//   (g) Counters are monotone across two ticks.
//   (h) Build error on nil Cluster.
//   (i) Per-pod correlation: when SubstrateWorkloads contains "cluster-autoscaler",
//       all series carry pod/namespace=kube-system/container/instance ending :8085.
//   (j) Fallback: empty SubstrateWorkloads → cluster-scoped series (no pod label).

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/clusterautoscaler"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	c, err := clusterautoscaler.New(&clusterautoscaler.Config{MinNodes: 3, MaxNodes: 10}, &fixture.Set{
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("clusterautoscaler.New: %v", err)
	}
	return c
}

var testNow = time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC) // business hours

func tickOnce(t *testing.T, c core.Construct) *coretest.MetricCapture {
	t.Helper()
	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return cap
}

// ─── (a) Exact series name inventory ─────────────────────────────────────────

// expectedNames is derived from signals/k8s-addons.md [slug: k8s-cluster-autoscaler] + histogram expansion rules.
// Histograms (function_duration_seconds, aws_request_duration_seconds) expand to
// _bucket/_sum/_count; rest_client_requests_total is a plain counter.
var expectedNames = func() []string {
	names := []string{
		"cluster_autoscaler_cluster_safe_to_autoscale",
		"cluster_autoscaler_nodes_count",
		"cluster_autoscaler_node_groups_count",
		"cluster_autoscaler_node_group_target_count",
		"cluster_autoscaler_unschedulable_pods_count",
		"cluster_autoscaler_cluster_cpu_current_cores",
		"cluster_autoscaler_cluster_memory_current_bytes",
		"cluster_autoscaler_cpu_limits_cores",
		"cluster_autoscaler_memory_limits_bytes",
		"cluster_autoscaler_last_activity",
		// histogram expansion
		"cluster_autoscaler_function_duration_seconds_bucket",
		"cluster_autoscaler_function_duration_seconds_sum",
		"cluster_autoscaler_function_duration_seconds_count",
		"cluster_autoscaler_errors_total",
		"cluster_autoscaler_scaled_up_nodes_total",
		"cluster_autoscaler_failed_scale_ups_total",
		"cluster_autoscaler_scaled_down_nodes_total",
		"cluster_autoscaler_evicted_pods_total",
		"cluster_autoscaler_unneeded_nodes_count",
		"cluster_autoscaler_unremovable_nodes_count",
		"cluster_autoscaler_scale_down_in_cooldown",
		// histogram expansion
		"cluster_autoscaler_aws_request_duration_seconds_bucket",
		"cluster_autoscaler_aws_request_duration_seconds_sum",
		"cluster_autoscaler_aws_request_duration_seconds_count",
		// rest_client_* family
		"rest_client_requests_total",
	}
	sort.Strings(names)
	return names
}()

func TestSeriesInventory(t *testing.T) {
	c := buildDefault(t)
	cap := tickOnce(t, c)
	got := cap.Names()

	wantSet := map[string]bool{}
	for _, n := range expectedNames {
		wantSet[n] = true
	}
	gotSet := map[string]bool{}
	for _, n := range got {
		gotSet[n] = true
	}

	for _, n := range expectedNames {
		if !gotSet[n] {
			t.Errorf("MISSING series: %s", n)
		}
	}
	for _, n := range got {
		if !wantSet[n] {
			t.Errorf("UNEXPECTED series: %s", n)
		}
	}
}

// ─── (b) Three intentionally-omitted metrics absent ──────────────────────────

func TestOmittedMetricsAbsent(t *testing.T) {
	c := buildDefault(t)
	cap := tickOnce(t, c)
	gotSet := map[string]bool{}
	for _, n := range cap.Names() {
		gotSet[n] = true
	}

	omitted := []string{
		"cluster_autoscaler_binpacking_heterogeneity",
		"cluster_autoscaler_max_node_skip_eval_duration_seconds",
		"cluster_autoscaler_inconsistent_instances_migs_count",
		// histogram variants should also be absent
		"cluster_autoscaler_binpacking_heterogeneity_bucket",
		"cluster_autoscaler_max_node_skip_eval_duration_seconds_bucket",
	}
	for _, name := range omitted {
		if gotSet[name] {
			t.Errorf("series %q must NOT be emitted (intentionally omitted)", name)
		}
	}
}

// ─── (c) nodes_count == fixture node count ────────────────────────────────────

func TestNodesCountMatchesFixture(t *testing.T) {
	clust := coretest.Cluster()
	c, err := clusterautoscaler.New(&clusterautoscaler.Config{MinNodes: 3, MaxNodes: 10}, &fixture.Set{
		Cluster: clust,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cap := tickOnce(t, c)

	nodeCount := float64(len(clust.Nodes))
	for _, s := range cap.Find("cluster_autoscaler_nodes_count") {
		st := s.Labels["state"]
		switch st {
		case "ready", "schedulable":
			if s.Value != nodeCount {
				t.Errorf("nodes_count[state=%q] = %.0f, want %.0f (len(Nodes))", st, s.Value, nodeCount)
			}
		default:
			if s.Value != 0 {
				t.Errorf("nodes_count[state=%q] = %.0f, want 0", st, s.Value)
			}
		}
	}

	// All 9 state enum values must be present
	stateVals := map[string]bool{}
	for _, s := range cap.Find("cluster_autoscaler_nodes_count") {
		stateVals[s.Labels["state"]] = true
	}
	expected := []string{"ready", "unready", "notStarted", "longNotStarted", "unregistered",
		"longUnregistered", "cloudProviderTarget", "schedulable", "unschedulable"}
	for _, st := range expected {
		if !stateVals[st] {
			t.Errorf("nodes_count missing state=%q", st)
		}
	}
}

// ─── (d) min/max config reflected in limit series ────────────────────────────

func TestMinMaxNodesInLimits(t *testing.T) {
	const minN, maxN = 2, 8
	c, err := clusterautoscaler.New(&clusterautoscaler.Config{MinNodes: minN, MaxNodes: maxN}, &fixture.Set{
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cap := tickOnce(t, c)

	const vcpusPerNode = 4
	const gibPerNode = 16
	const gib = 1 << 30

	for _, s := range cap.Find("cluster_autoscaler_cpu_limits_cores") {
		dir := s.Labels["direction"]
		var wantNodes int
		switch dir {
		case "up":
			wantNodes = maxN
		case "down":
			wantNodes = minN
		default:
			t.Errorf("unexpected direction label %q", dir)
			continue
		}
		want := float64(wantNodes * vcpusPerNode)
		if s.Value != want {
			t.Errorf("cpu_limits_cores[direction=%q] = %.0f, want %.0f (%d nodes × %d vCPUs)",
				dir, s.Value, want, wantNodes, vcpusPerNode)
		}
	}

	for _, s := range cap.Find("cluster_autoscaler_memory_limits_bytes") {
		dir := s.Labels["direction"]
		var wantNodes int
		switch dir {
		case "up":
			wantNodes = maxN
		case "down":
			wantNodes = minN
		default:
			t.Errorf("unexpected direction label %q", dir)
			continue
		}
		want := float64(wantNodes) * gibPerNode * gib
		if s.Value != want {
			t.Errorf("memory_limits_bytes[direction=%q] = %.0f, want %.0f",
				dir, s.Value, want)
		}
	}
}

// ─── (e) rest_client_* present ───────────────────────────────────────────────

func TestRestClientPresent(t *testing.T) {
	c := buildDefault(t)
	cap := tickOnce(t, c)

	series := cap.Find("rest_client_requests_total")
	if len(series) == 0 {
		t.Fatal("rest_client_requests_total: no series emitted")
	}
	// Verify the three (code,method) combinations are present
	type key struct{ code, method string }
	found := map[key]bool{}
	for _, s := range series {
		found[key{s.Labels["code"], s.Labels["method"]}] = true
	}
	for _, want := range []key{{"200", "GET"}, {"200", "PATCH"}, {"201", "POST"}} {
		if !found[want] {
			t.Errorf("rest_client_requests_total missing code=%q method=%q", want.code, want.method)
		}
	}
}

// ─── (f) cluster + k8s_cluster_name on every series; NO blueprint label ───────

func TestBaseLabels(t *testing.T) {
	c := buildDefault(t)
	cap := tickOnce(t, c)
	clust := coretest.Cluster()

	for _, s := range cap.All() {
		if s.Labels["cluster"] != clust.Name {
			t.Errorf("series %q: cluster=%q want %q", s.Name, s.Labels["cluster"], clust.Name)
		}
		if s.Labels["k8s_cluster_name"] != clust.Name {
			t.Errorf("series %q: k8s_cluster_name=%q want %q", s.Name, s.Labels["k8s_cluster_name"], clust.Name)
		}
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("series %q must NOT carry blueprint label (ScopeSubstrate)", s.Name)
		}
	}
}

// ─── (g) Counters are monotone across two ticks ───────────────────────────────

func TestCountersMonotone(t *testing.T) {
	c := buildDefault(t)

	cap1 := &coretest.MetricCapture{}
	if err := c.Tick(context.Background(), testNow, coretest.World(cap1, nil, nil)); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	cap2 := &coretest.MetricCapture{}
	t2 := testNow.Add(60 * time.Second)
	if err := c.Tick(context.Background(), t2, coretest.World(cap2, nil, nil)); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	vals1 := seriesVals(cap1)
	vals2 := seriesVals(cap2)

	counters := []string{
		"rest_client_requests_total",
		"cluster_autoscaler_errors_total",
		"cluster_autoscaler_scaled_up_nodes_total",
	}
	for _, name := range counters {
		for _, s := range cap2.Find(name) {
			sig := labelSig(s.Labels)
			v1 := vals1[name+"\x00"+sig]
			v2 := vals2[name+"\x00"+sig]
			if v2 < v1 {
				t.Errorf("counter %q decreased: tick1=%.2f tick2=%.2f (not monotone)", name, v1, v2)
			}
		}
	}
}

// ─── (h) Build error on nil Cluster ──────────────────────────────────────────

func TestBuildErrorOnNilCluster(t *testing.T) {
	_, err := clusterautoscaler.New(&clusterautoscaler.Config{}, &fixture.Set{})
	if err == nil {
		t.Fatal("expected error when Cluster is nil, got nil")
	}
}

// ─── metadata ─────────────────────────────────────────────────────────────────

func TestKindAndSignals(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "cluster_autoscaler" {
		t.Errorf("Kind() = %q, want %q", c.Kind(), "cluster_autoscaler")
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals() = %v, want [Metrics]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval() = %v, want 60s", c.Interval())
	}
}

// ─── (i) Per-pod correlation ──────────────────────────────────────────────────

// clusterWithAddon returns a coretest.Cluster with the "cluster_autoscaler" addon
// SubstrateWorkloads populated (1 pod, kube-system, container cluster-autoscaler).
func clusterWithAddon(t *testing.T) *fixture.Cluster {
	t.Helper()
	cl := coretest.Cluster()
	seed := "test"
	wls := fixture.AddonWorkloads("cluster_autoscaler")
	for i := range wls {
		wls[i].PodNames = fixture.WorkloadPodNames(seed, wls[i], cl.Nodes)
		wls[i].NodeIdx = make([]int, wls[i].Replicas)
		for p := range wls[i].Replicas {
			wls[i].NodeIdx[p] = p % len(cl.Nodes)
		}
	}
	cl.SubstrateWorkloads = wls
	return cl
}

// buildWithPods builds a construct whose cluster has "cluster-autoscaler" SubstrateWorkloads.
func buildWithPods(t *testing.T) core.Construct {
	t.Helper()
	c, err := clusterautoscaler.New(&clusterautoscaler.Config{MinNodes: 3, MaxNodes: 10}, &fixture.Set{
		Cluster: clusterWithAddon(t),
	})
	if err != nil {
		t.Fatalf("clusterautoscaler.New (with pods): %v", err)
	}
	return c
}

// TestPodCorrelationLabels verifies that when SubstrateWorkloads contains
// "cluster-autoscaler" (1 pod, kube-system), all emitted series carry:
//   - pod (non-empty)
//   - namespace = "kube-system"
//   - container = "cluster-autoscaler"
//   - instance ending with ":8085"
func TestPodCorrelationLabels(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	all := cap.All()
	if len(all) == 0 {
		t.Fatal("no series emitted")
	}

	for _, s := range all {
		// pod must be present and non-empty.
		if s.Labels["pod"] == "" {
			t.Errorf("series %q: missing pod label (labels=%v)", s.Name, s.Labels)
		}
		// namespace must be kube-system (cluster-autoscaler runs in kube-system).
		if s.Labels["namespace"] != "kube-system" {
			t.Errorf("series %q: namespace=%q want kube-system", s.Name, s.Labels["namespace"])
		}
		// container must be "cluster-autoscaler".
		if s.Labels["container"] != "cluster-autoscaler" {
			t.Errorf("series %q: container=%q want cluster-autoscaler", s.Name, s.Labels["container"])
		}
		// instance must end with ":8085".
		if !strings.HasSuffix(s.Labels["instance"], ":8085") {
			t.Errorf("series %q: instance=%q must end with :8085", s.Name, s.Labels["instance"])
		}
	}
}

// TestPodCorrelationPodNameMinted verifies that PodNames are minted (non-empty string)
// and that all series refer to the same single pod (1 replica).
func TestPodCorrelationPodNameMinted(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	pods := map[string]bool{}
	for _, s := range cap.All() {
		pods[s.Labels["pod"]] = true
	}
	// 1 replica → exactly 1 distinct pod name.
	if len(pods) != 1 {
		t.Errorf("expected exactly 1 distinct pod name (1 replica), got %d: %v", len(pods), pods)
	}
	// The pod name must not be empty.
	for p := range pods {
		if p == "" {
			t.Error("pod label is empty string")
		}
	}
}

// ─── (j) Fallback: no SubstrateWorkloads → cluster-scoped ────────────────────

// TestFallbackNoSubstrateWorkloads verifies back-compat: with empty SubstrateWorkloads,
// the construct still emits all cluster_autoscaler_* series but without pod labels.
func TestFallbackNoSubstrateWorkloads(t *testing.T) {
	// buildDefault uses coretest.Cluster() which has no SubstrateWorkloads.
	c := buildDefault(t)
	cap := tickOnce(t, c)

	if len(cap.All()) == 0 {
		t.Fatal("fallback: no series emitted")
	}

	// Core families must still be present in fallback.
	gotNames := map[string]bool{}
	for _, n := range cap.Names() {
		gotNames[n] = true
	}
	for _, n := range []string{
		"cluster_autoscaler_cluster_safe_to_autoscale",
		"cluster_autoscaler_nodes_count",
		"cluster_autoscaler_cluster_cpu_current_cores",
		"rest_client_requests_total",
	} {
		if !gotNames[n] {
			t.Errorf("fallback: MISSING series %s", n)
		}
	}

	// In fallback mode, no series must carry a pod label.
	for _, s := range cap.All() {
		if s.Labels["pod"] != "" {
			t.Errorf("fallback: series %q must NOT carry pod label, got pod=%q", s.Name, s.Labels["pod"])
		}
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func seriesVals(cap *coretest.MetricCapture) map[string]float64 {
	m := map[string]float64{}
	for _, s := range cap.All() {
		m[s.Name+"\x00"+labelSig(s.Labels)] = s.Value
	}
	return m
}

func labelSig(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb []byte
	for _, k := range keys {
		sb = append(sb, []byte(k+"="+labels[k]+";")...)
	}
	return string(sb)
}
