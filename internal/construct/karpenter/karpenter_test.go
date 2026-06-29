// SPDX-License-Identifier: AGPL-3.0-only

package karpenter_test

// karpenter_test.go — construct invariant tests for the karpenter construct.
//
// Test inventory:
//   (a) Core series name inventory: all expected metric families present.
//   (b) SUMMARY check: nodes_termination_duration_seconds and pods_startup_duration_seconds
//       emit quantile series — NOT _bucket series.
//   (c) PascalCase reason exception: nodepools_allowed_disruptions carries "Empty"/"Underutilized"
//       (PascalCase), not "empty"/"underutilized".
//   (d) Domain metrics (karpenter_*) on LEADER pod only: pod=karpenter-*, namespace=kube-system,
//       container=controller, instance ending ":8080".
//   (e) go_* / process_* / controller_runtime_* on ALL pods (both replicas).
//   (f) Offering-price series count is bounded (< 100).
//   (g) cluster + k8s_cluster_name on every series; NO blueprint label.
//   (h) Counters are monotone across two ticks.
//   (i) Build error on nil Cluster.
//   (j) Fallback: cluster-scoped series when SubstrateWorkloads absent (no pod/namespace labels).
//   (k) Registration.
//   (l) Signals() includes core.Logs.
//   (m) Log streams: k8s_namespace_name=kube-system, k8s_pod_name matches karpenter pod,
//       detected_level present, body is zap-JSON with level+time fields.
//   (n) No high-card label (reconcileID) in stream labels.
//   (o) Log streams: k8s_deployment_name=karpenter, service_namespace=kube-system, log_iostream=stderr.
//   (p) Nil w.Logs guard: Tick succeeds when w.Logs is nil.

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/karpenter"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

var testNow = time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC) // business hours

// clusterWithKarpenter returns a coretest.Cluster with karpenter SubstrateWorkloads
// populated (the "with-pods" path).
func clusterWithKarpenter(t *testing.T) *fixture.Cluster {
	t.Helper()
	cl := coretest.Cluster()
	seed := "test"
	wls := fixture.AddonWorkloads("karpenter")
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

// buildWithPods builds a karpenter construct with SubstrateWorkloads populated.
func buildWithPods(t *testing.T) core.Construct {
	t.Helper()
	c, err := karpenter.New(&karpenter.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: clusterWithKarpenter(t),
	})
	if err != nil {
		t.Fatalf("karpenter.New (with pods): %v", err)
	}
	return c
}

// buildWithoutPods builds a karpenter construct with no SubstrateWorkloads (fallback path).
func buildWithoutPods(t *testing.T) core.Construct {
	t.Helper()
	c, err := karpenter.New(&karpenter.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("karpenter.New (no pods): %v", err)
	}
	return c
}

func tickOnce(t *testing.T, c core.Construct) *coretest.MetricCapture {
	t.Helper()
	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return cap
}

// filterLabel returns series where Labels[key]==val.
func filterLabel(ss []promrw.Series, key, val string) []promrw.Series {
	var out []promrw.Series
	for _, s := range ss {
		if s.Labels[key] == val {
			out = append(out, s)
		}
	}
	return out
}

// hasLabel returns true if any series in ss has Labels[key]==val.
func hasLabel(ss []promrw.Series, key, val string) bool {
	return len(filterLabel(ss, key, val)) > 0
}

// ─── (a) Core series name inventory ──────────────────────────────────────────

// expectedFamilies is the set of metric NAME PREFIXES (families) that must be present.
// Histograms are checked by their _bucket suffix; summaries by their plain + _count + _sum.
var expectedFamilies = []string{
	// karpenter domain families (leader)
	"karpenter_build_info",
	"karpenter_cluster_state_node_count",
	"karpenter_cluster_state_synced",
	"karpenter_cluster_state_unsynced_time_seconds",
	"karpenter_cluster_utilization_percent",
	"karpenter_cloudprovider_duration_seconds_bucket",
	"karpenter_cloudprovider_batcher_batch_size_bucket",
	"karpenter_cloudprovider_batcher_batch_time_seconds_bucket",
	"karpenter_cloudprovider_errors_total",
	"karpenter_cloudprovider_instance_type_cpu_cores",
	"karpenter_cloudprovider_instance_type_memory_bytes",
	"karpenter_cloudprovider_instance_type_offering_available",
	"karpenter_cloudprovider_instance_type_offering_price_estimate",
	"karpenter_nodeclaims_created_total",
	"karpenter_nodeclaims_disrupted_total",
	"karpenter_nodeclaims_instance_termination_duration_seconds_bucket",
	"karpenter_nodeclaims_terminated_total",
	"karpenter_nodeclaims_termination_duration_seconds_bucket",
	"karpenter_nodepools_allowed_disruptions",
	"karpenter_nodepools_cost_total",
	"karpenter_nodepools_limit",
	"karpenter_nodepools_nodes_consuming_budgets",
	"karpenter_nodepools_usage",
	"karpenter_nodes_allocatable",
	"karpenter_nodes_created_total",
	"karpenter_nodes_current_lifetime_seconds",
	"karpenter_nodes_drained_total",
	"karpenter_nodes_lifetime_duration_seconds_bucket",
	"karpenter_nodes_system_overhead",
	"karpenter_nodes_terminated_total",
	// summaries (plain series = quantile gauge)
	"karpenter_nodes_termination_duration_seconds",
	"karpenter_nodes_termination_duration_seconds_count",
	"karpenter_nodes_termination_duration_seconds_sum",
	"karpenter_nodes_total_daemon_limits",
	"karpenter_nodes_total_daemon_requests",
	"karpenter_nodes_total_pod_limits",
	"karpenter_nodes_total_pod_requests",
	"karpenter_interruption_deleted_messages_total",
	"karpenter_interruption_message_queue_duration_seconds_bucket",
	"karpenter_interruption_received_messages_total",
	"karpenter_pods_bound_duration_seconds_bucket",
	"karpenter_pods_drained_total",
	"karpenter_pods_eviction_requests_total",
	"karpenter_pods_provisioning_bound_duration_seconds_bucket",
	"karpenter_pods_provisioning_startup_duration_seconds_bucket",
	"karpenter_pods_scheduling_decision_duration_seconds_bucket",
	// summary
	"karpenter_pods_startup_duration_seconds",
	"karpenter_pods_startup_duration_seconds_count",
	"karpenter_pods_startup_duration_seconds_sum",
	"karpenter_pods_state",
	"karpenter_scheduler_ignored_pods_count",
	"karpenter_scheduler_queue_depth",
	"karpenter_scheduler_scheduling_duration_seconds_bucket",
	"karpenter_scheduler_unschedulable_pods_count",
	"karpenter_voluntary_disruption_consolidation_timeouts_total",
	"karpenter_voluntary_disruption_decision_evaluation_duration_seconds_bucket",
	"karpenter_voluntary_disruption_decisions_by_nodepool_total",
	"karpenter_voluntary_disruption_decisions_total",
	"karpenter_voluntary_disruption_eligible_nodes",
	// go_* + process_* + controller_runtime_* (all pods)
	"go_goroutines",
	"go_threads",
	"go_gc_duration_seconds",
	"go_memstats_alloc_bytes",
	"go_memstats_heap_alloc_bytes",
	"go_memstats_heap_inuse_bytes",
	"process_cpu_seconds_total",
	"process_resident_memory_bytes",
	"process_open_fds",
	"process_max_fds",
	"controller_runtime_reconcile_total",
	"controller_runtime_active_workers",
	"controller_runtime_max_concurrent_reconciles",
}

func TestSeriesInventory(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)
	names := cap.Names()
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	var missing []string
	for _, want := range expectedFamilies {
		if !nameSet[want] {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("missing %d expected metric names:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}

// ─── (b) SUMMARY check ────────────────────────────────────────────────────────

// TestSummaryNotHistogram verifies that the two SUMMARY metrics do NOT emit _bucket series
// and DO emit quantile-labelled series + _count + _sum.
func TestSummaryNotHistogram(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)
	names := cap.Names()
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	for _, base := range []string{
		"karpenter_nodes_termination_duration_seconds",
		"karpenter_pods_startup_duration_seconds",
	} {
		// Must NOT have _bucket.
		bucketName := base + "_bucket"
		if nameSet[bucketName] {
			t.Errorf("SUMMARY %q must not emit _bucket series (got %q)", base, bucketName)
		}

		// Must have _count and _sum.
		for _, suffix := range []string{"_count", "_sum"} {
			n := base + suffix
			if !nameSet[n] {
				t.Errorf("SUMMARY %q: missing %q", base, n)
			}
		}

		// Must have quantile-labelled series on the base name.
		baseSeries := cap.Find(base)
		if len(baseSeries) == 0 {
			t.Errorf("SUMMARY %q: no plain series found (expected quantile-labelled series)", base)
			continue
		}
		hasQuantile := false
		for _, s := range baseSeries {
			if _, ok := s.Labels["quantile"]; ok {
				hasQuantile = true
				break
			}
		}
		if !hasQuantile {
			t.Errorf("SUMMARY %q: plain series found but none have quantile label", base)
		}
	}
}

// ─── (c) PascalCase reason exception ─────────────────────────────────────────

// TestAllowedDisruptionsPascalCase verifies that karpenter_nodepools_allowed_disruptions
// uses PascalCase reason values (Empty, Underutilized) — the documented exception.
func TestAllowedDisruptionsPascalCase(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)
	series := cap.Find("karpenter_nodepools_allowed_disruptions")
	if len(series) == 0 {
		t.Fatal("karpenter_nodepools_allowed_disruptions: no series emitted")
	}

	reasons := make(map[string]bool)
	for _, s := range series {
		reasons[s.Labels["reason"]] = true
	}

	// Must have PascalCase.
	for _, want := range []string{"Empty", "Underutilized"} {
		if !reasons[want] {
			t.Errorf("nodepools_allowed_disruptions: missing PascalCase reason %q (got reasons: %v)", want, reasons)
		}
	}
	// Must NOT have lowercase variants.
	for _, bad := range []string{"empty", "underutilized"} {
		if reasons[bad] {
			t.Errorf("nodepools_allowed_disruptions: unexpected lowercase reason %q", bad)
		}
	}
}

// ─── (d) Domain metrics on LEADER pod only ────────────────────────────────────

// TestDomainMetricsOnLeaderPod verifies that karpenter_* domain metrics are stamped with
// leader pod labels (pod=karpenter-*, namespace=kube-system, container=controller,
// instance ending :8080). There must be exactly ONE pod for a domain metric.
func TestDomainMetricsOnLeaderPod(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	// Use karpenter_cluster_state_node_count as a representative domain metric.
	series := cap.Find("karpenter_cluster_state_node_count")
	if len(series) == 0 {
		t.Fatal("karpenter_cluster_state_node_count: no series emitted")
	}

	// Domain metric should be exactly 1 series (leader only).
	if len(series) != 1 {
		t.Errorf("karpenter_cluster_state_node_count: expected 1 series (leader), got %d", len(series))
	}

	s := series[0]

	// pod label must start with "karpenter-".
	if !strings.HasPrefix(s.Labels["pod"], "karpenter-") {
		t.Errorf("pod label %q: want prefix karpenter-", s.Labels["pod"])
	}

	// namespace must be kube-system.
	if s.Labels["namespace"] != "kube-system" {
		t.Errorf("namespace=%q, want kube-system", s.Labels["namespace"])
	}

	// container must be controller.
	if s.Labels["container"] != "controller" {
		t.Errorf("container=%q, want controller", s.Labels["container"])
	}

	// instance must end with ":8080".
	if !strings.HasSuffix(s.Labels["instance"], ":8080") {
		t.Errorf("instance=%q: want suffix :8080", s.Labels["instance"])
	}
}

// ─── (e) go_* / process_* on ALL pods (both replicas) ────────────────────────

// TestGoMetricsOnAllPods verifies that go_goroutines (and process_cpu_seconds_total) are
// emitted for both pod replicas.
func TestGoMetricsOnAllPods(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	for _, name := range []string{"go_goroutines", "process_cpu_seconds_total", "controller_runtime_reconcile_total"} {
		series := cap.Find(name)
		if len(series) == 0 {
			t.Errorf("%s: no series emitted", name)
			continue
		}
		// Collect distinct pod labels.
		pods := make(map[string]bool)
		for _, s := range series {
			if p, ok := s.Labels["pod"]; ok && p != "" {
				pods[p] = true
			}
		}
		// karpenter has 2 replicas → should see 2 distinct pods.
		if len(pods) < 2 {
			t.Errorf("%s: expected series from 2 pods, got %d distinct pods (%v)", name, len(pods), pods)
		}
	}
}

// ─── (f) Offering-price series count is bounded ───────────────────────────────

// TestOfferingPriceCardinality verifies the bounded offering-price series count.
// Live production emits ~9261; synthkit must emit < 100.
func TestOfferingPriceCardinality(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)
	series := cap.Find("karpenter_cloudprovider_instance_type_offering_price_estimate")
	if len(series) == 0 {
		t.Fatal("offering_price_estimate: no series emitted")
	}
	const capMax = 100 // intentional cap is 10 types × 3 AZs × 3 cap types = 90; allow small margin
	if len(series) >= capMax {
		t.Errorf("offering_price_estimate: %d series ≥ cap %d (deliberate cardinality cap violated)", len(series), capMax)
	}
	t.Logf("offering_price_estimate series count: %d (cap <%d)", len(series), capMax)
}

// ─── (g) cluster + k8s_cluster_name on every series; NO blueprint label ──────

// TestUniversalLabels verifies every emitted series has cluster + k8s_cluster_name set
// and has no blueprint label.
func TestUniversalLabels(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)
	all := cap.All()
	if len(all) == 0 {
		t.Fatal("no series emitted")
	}

	for _, s := range all {
		if s.Labels["cluster"] == "" {
			t.Errorf("series %q missing cluster label", s.Name)
		}
		if s.Labels["k8s_cluster_name"] == "" {
			t.Errorf("series %q missing k8s_cluster_name label", s.Name)
		}
		if s.Labels["blueprint"] != "" {
			t.Errorf("series %q has blueprint label (ScopeSubstrate must not stamp blueprint)", s.Name)
		}
	}
}

// ─── (h) Counters are monotone across two ticks ───────────────────────────────

// TestCounterMonotonicity verifies that counter series increase (or stay equal) across two ticks.
func TestCounterMonotonicity(t *testing.T) {
	c := buildWithPods(t)
	cap1 := tickOnce(t, c)
	t2 := testNow.Add(60 * time.Second)

	cap2 := &coretest.MetricCapture{}
	w2 := coretest.World(cap2, nil, nil)
	if err := c.Tick(context.Background(), t2, w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	// Spot-check a counter family.
	counterNames := []string{
		"karpenter_nodeclaims_created_total",
		"karpenter_nodes_terminated_total",
		"karpenter_interruption_received_messages_total",
		"controller_runtime_reconcile_total",
	}

	for _, name := range counterNames {
		s1 := cap1.Find(name)
		s2 := cap2.Find(name)
		if len(s1) == 0 || len(s2) == 0 {
			t.Errorf("%s: no series in tick 1 or tick 2", name)
			continue
		}
		// Build value map by label sig.
		vals1 := make(map[string]float64, len(s1))
		for _, s := range s1 {
			vals1[labelSig(s.Labels)] = s.Value
		}
		for _, s := range s2 {
			sig := labelSig(s.Labels)
			v1 := vals1[sig]
			if s.Value < v1 {
				t.Errorf("%s: counter decreased tick1=%.4f → tick2=%.4f (labels=%v)", name, v1, s.Value, s.Labels)
			}
		}
	}
}

// ─── (i) Build error on nil Cluster ──────────────────────────────────────────

func TestBuildNilCluster(t *testing.T) {
	_, err := karpenter.New(&karpenter.Config{}, &fixture.Set{Seed: "test", Cluster: nil})
	if err == nil {
		t.Fatal("expected error building karpenter with nil Cluster, got nil")
	}
}

// ─── (j) Fallback: cluster-scoped series without SubstrateWorkloads ───────────

// TestFallbackNoPodLabels verifies that when SubstrateWorkloads is absent, series are
// emitted without pod/namespace/container/instance labels (cluster-scoped fallback).
func TestFallbackNoPodLabels(t *testing.T) {
	c := buildWithoutPods(t)
	cap := tickOnce(t, c)
	all := cap.All()
	if len(all) == 0 {
		t.Fatal("no series emitted in fallback mode")
	}

	for _, s := range all {
		if s.Labels["pod"] != "" {
			t.Errorf("fallback: series %q should have no pod label, got %q", s.Name, s.Labels["pod"])
		}
		if s.Labels["namespace"] != "" {
			t.Errorf("fallback: series %q should have no namespace label, got %q", s.Name, s.Labels["namespace"])
		}
		if s.Labels["container"] != "" {
			t.Errorf("fallback: series %q should have no container label, got %q", s.Name, s.Labels["container"])
		}
	}
	// Cluster label must still be present.
	if !hasLabel(all, "cluster", coretest.Cluster().Name) {
		t.Error("fallback: cluster label absent")
	}
}

// ─── (k) Registration ────────────────────────────────────────────────────────

func TestRegistration(t *testing.T) {
	reg := karpenter.Registration()
	if reg.Kind != "karpenter" {
		t.Errorf("Registration().Kind = %q, want karpenter", reg.Kind)
	}
	if reg.Scope != core.ScopeSubstrate {
		t.Errorf("Registration().Scope = %v, want ScopeSubstrate", reg.Scope)
	}
	if reg.NewConfig == nil {
		t.Error("Registration().NewConfig is nil")
	}
	if reg.Build == nil {
		t.Error("Registration().Build is nil")
	}
}

// ─── (l) Signals() includes core.Logs ────────────────────────────────────────

// TestSignalsIncludeLogs verifies that Signals() returns a slice containing core.Logs.
func TestSignalsIncludeLogs(t *testing.T) {
	c := buildWithPods(t)
	sigs := c.Signals()
	hasMetrics, hasLogs := false, false
	for _, s := range sigs {
		switch s {
		case core.Metrics:
			hasMetrics = true
		case core.Logs:
			hasLogs = true
		}
	}
	if !hasMetrics {
		t.Error("Signals() missing core.Metrics")
	}
	if !hasLogs {
		t.Error("Signals() missing core.Logs")
	}
}

// ─── (m) Log streams carry correct stream labels + zap-JSON body ─────────────

// tickOnceLogs runs multiple ticks until at least one log stream is captured (or max ticks).
// Returns the captured log streams.
func tickOnceLogs(t *testing.T, c core.Construct, maxTicks int) []*coretest.LogCapture {
	t.Helper()
	var caps []*coretest.LogCapture
	for i := range maxTicks {
		mc := &coretest.MetricCapture{}
		lc := &coretest.LogCapture{}
		w := coretest.World(mc, lc, nil)
		now := testNow.Add(time.Duration(i) * 60 * time.Second)
		if err := c.Tick(context.Background(), now, w); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		if len(lc.Streams) > 0 {
			caps = append(caps, lc)
		}
	}
	return caps
}

// karpenterPodNames collects pod names from SubstrateWorkloads for key "karpenter".
func karpenterPodNames(cl *fixture.Cluster) map[string]bool {
	names := map[string]bool{}
	for _, wl := range cl.SubstrateWorkloads {
		if wl.Name == "karpenter" {
			for _, pn := range wl.PodNames {
				names[pn] = true
			}
		}
	}
	return names
}

// TestLogStreamsLabels verifies that emitted log streams carry the required stream labels.
func TestLogStreamsLabels(t *testing.T) {
	cl := clusterWithKarpenter(t)
	c, err := karpenter.New(&karpenter.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: cl,
	})
	if err != nil {
		t.Fatalf("karpenter.New: %v", err)
	}

	caps := tickOnceLogs(t, c, 20)
	if len(caps) == 0 {
		t.Fatal("no log streams emitted across 20 ticks")
	}

	podNames := karpenterPodNames(cl)

	for _, lc := range caps {
		for _, stream := range lc.Streams {
			lbls := stream.Labels

			// k8s_namespace_name must be kube-system.
			if lbls["k8s_namespace_name"] != "kube-system" {
				t.Errorf("k8s_namespace_name=%q want kube-system", lbls["k8s_namespace_name"])
			}

			// k8s_pod_name must match a real karpenter pod from SubstrateWorkloads.
			podName := lbls["k8s_pod_name"]
			if podName == "" {
				t.Error("k8s_pod_name label absent or empty")
			} else if len(podNames) > 0 && !podNames[podName] {
				t.Errorf("k8s_pod_name=%q not in karpenter SubstrateWorkloads pods %v", podName, podNames)
			}

			// detected_level must be present and one of info/error.
			dl := lbls["detected_level"]
			if dl != "info" && dl != "error" {
				t.Errorf("detected_level=%q want info or error", dl)
			}

			// k8s_deployment_name must be "karpenter".
			if lbls["k8s_deployment_name"] != "karpenter" {
				t.Errorf("k8s_deployment_name=%q want karpenter", lbls["k8s_deployment_name"])
			}

			// service_namespace must be kube-system.
			if lbls["service_namespace"] != "kube-system" {
				t.Errorf("service_namespace=%q want kube-system", lbls["service_namespace"])
			}

			// log_iostream must be stderr.
			if lbls["log_iostream"] != "stderr" {
				t.Errorf("log_iostream=%q want stderr", lbls["log_iostream"])
			}

			// k8s_container_name must be controller.
			if lbls["k8s_container_name"] != "controller" {
				t.Errorf("k8s_container_name=%q want controller", lbls["k8s_container_name"])
			}

			// cluster and k8s_cluster_name must be present and match.
			if lbls["cluster"] == "" {
				t.Error("cluster label absent")
			}
			if lbls["k8s_cluster_name"] == "" {
				t.Error("k8s_cluster_name label absent")
			}
			if lbls["cluster"] != lbls["k8s_cluster_name"] {
				t.Errorf("cluster=%q != k8s_cluster_name=%q", lbls["cluster"], lbls["k8s_cluster_name"])
			}

			// Lines must be non-empty and body must be zap-JSON.
			if len(stream.Lines) == 0 {
				t.Error("stream has no lines")
				continue
			}
			for _, line := range stream.Lines {
				if len(line.Body) == 0 || line.Body[0] != '{' {
					t.Errorf("log line body is not JSON: %q", line.Body)
					continue
				}
				var obj map[string]any
				if err := json.Unmarshal([]byte(line.Body), &obj); err != nil {
					t.Errorf("log line body is invalid JSON: %v — body: %q", err, line.Body)
					continue
				}
				// Zap-JSON must have "level" field.
				if _, ok := obj["level"]; !ok {
					t.Errorf("zap-JSON body missing 'level' field: %q", line.Body)
				}
				// Karpenter uses "time" (RFC3339), NOT "ts" (lbc uses ts).
				if _, ok := obj["time"]; !ok {
					t.Errorf("zap-JSON body missing 'time' field (karpenter uses time, not ts): %q", line.Body)
				}
			}
		}
	}
}

// ─── (n) No high-card label (reconcileID) in stream labels ───────────────────

// TestNoHighCardStreamLabels verifies that high-cardinality fields (reconcileID, nodeclaim
// name, node name) do NOT appear as stream labels.
func TestNoHighCardStreamLabels(t *testing.T) {
	c := buildWithPods(t)
	caps := tickOnceLogs(t, c, 20)
	if len(caps) == 0 {
		t.Skip("no log streams emitted — cannot verify high-card label absence")
	}

	highCardKeys := []string{"reconcileID", "reconcile_id", "nodeclaim", "node_name"}
	for _, lc := range caps {
		for _, stream := range lc.Streams {
			for _, hk := range highCardKeys {
				if v, ok := stream.Labels[hk]; ok {
					t.Errorf("high-card label %q=%q must be in body, not stream labels", hk, v)
				}
			}
		}
	}
}

// ─── (p) Nil w.Logs guard ─────────────────────────────────────────────────────

// TestNilLogsGuard verifies that Tick does not panic or error when w.Logs is nil.
// (The runner only wires Logs when the sink is available.)
func TestNilLogsGuard(t *testing.T) {
	c := buildWithPods(t)
	mc := &coretest.MetricCapture{}
	// Pass nil for LogCapture — World will leave w.Logs nil.
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick with nil w.Logs returned error: %v", err)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// labelSig returns a stable string key for a label map.
func labelSig(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m[k])
		b.WriteByte(',')
	}
	return b.String()
}
