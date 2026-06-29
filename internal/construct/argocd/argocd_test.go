// SPDX-License-Identifier: AGPL-3.0-only

package argocd_test

// argocd_test.go — construct invariant tests for the argocd construct.
//
// Test inventory:
//   (a) Series name inventory: all 49 metric families present; NO controller_runtime_* or
//       argocd_notifications_* series.
//   (b) Base labels: cluster + k8s_cluster_name on every series; NO blueprint label.
//   (c) App-controller series carry the StatefulSet ordinal pod name (suffix "-0") and
//       namespace=argocd.
//   (d) Redis series carry container=redis_exporter.
//   (e) controller_runtime_* / argocd_notifications_* are ABSENT.
//   (f) Two redis histogram families: argocd_redis_request_duration (with hostname) and
//       argocd_redis_request_duration_seconds (no hostname, from repo-server).
//   (g) Every stamped series carries pod, namespace, instance labels.
//   (h) Fallback (nil SubstrateWorkloads): cluster-scoped series still emitted, no pod label.
//   (i) Build error on nil Cluster.
//   (j) Counters monotone across two ticks.
//   (k) Kind/Signals/Interval metadata.

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/argocd"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/loki"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

var testNow = time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

// clusterWithArgoCD returns a fixture.Cluster with argocd SubstrateWorkloads populated.
func clusterWithArgoCD(t *testing.T) *fixture.Cluster {
	t.Helper()
	cl := coretest.Cluster()
	seed := "test"
	wls := fixture.AddonWorkloads("argocd")
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

// buildWithPods builds the construct with SubstrateWorkloads populated.
func buildWithPods(t *testing.T) core.Construct {
	t.Helper()
	c, err := argocd.New(&argocd.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: clusterWithArgoCD(t),
	})
	if err != nil {
		t.Fatalf("argocd.New (with pods): %v", err)
	}
	return c
}

// buildDefault builds the construct WITHOUT SubstrateWorkloads (fallback path).
func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	c, err := argocd.New(&argocd.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("argocd.New (default): %v", err)
	}
	return c
}

func tickOnce(t *testing.T, c core.Construct) *coretest.MetricCapture {
	t.Helper()
	cap := &coretest.MetricCapture{}
	if err := c.Tick(context.Background(), testNow, coretest.World(cap, nil, nil)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return cap
}

// ─── (a) Series name inventory ────────────────────────────────────────────────

// expectedFamilies lists the metric base-names (before histogram _bucket/_sum/_count
// expansion) that must be present.
// Per svc-group-b.md §2.A: 49 metric names. Histograms appear as 3 leaf names each.
// redis_connected_clients + redis_uptime_in_seconds are emitted via redis_exporter sidecar.
var expectedFamilies = []string{
	// argocd_* families
	"argocd_app_info",
	"argocd_app_k8s_request_total",
	"argocd_app_orphaned_resources_count",
	"argocd_app_reconcile_bucket",
	"argocd_app_reconcile_count",
	"argocd_app_reconcile_sum",
	"argocd_app_sync_duration_seconds_total",
	"argocd_app_sync_total",
	"argocd_cluster_api_resource_objects",
	"argocd_cluster_api_resources",
	"argocd_cluster_cache_age_seconds",
	"argocd_cluster_connection_status",
	"argocd_cluster_events_total",
	"argocd_cluster_info",
	"argocd_git_request_duration_seconds_bucket",
	"argocd_git_request_duration_seconds_count",
	"argocd_git_request_duration_seconds_sum",
	"argocd_git_request_total",
	"argocd_info",
	"argocd_kubectl_exec_pending",
	"argocd_kubectl_exec_total",
	"argocd_kubectl_rate_limiter_duration_seconds_bucket",
	"argocd_kubectl_rate_limiter_duration_seconds_count",
	"argocd_kubectl_rate_limiter_duration_seconds_sum",
	"argocd_kubectl_request_duration_seconds_bucket",
	"argocd_kubectl_request_duration_seconds_count",
	"argocd_kubectl_request_duration_seconds_sum",
	"argocd_kubectl_request_retries_total",
	"argocd_kubectl_request_size_bytes_bucket",
	"argocd_kubectl_request_size_bytes_count",
	"argocd_kubectl_request_size_bytes_sum",
	"argocd_kubectl_requests_total",
	"argocd_kubectl_response_size_bytes_bucket",
	"argocd_kubectl_response_size_bytes_count",
	"argocd_kubectl_response_size_bytes_sum",
	"argocd_kubectl_transport_cache_entries",
	"argocd_kubectl_transport_create_calls_total",
	// Two redis histogram families (version artifact)
	"argocd_redis_request_duration_bucket",
	"argocd_redis_request_duration_count",
	"argocd_redis_request_duration_sum",
	"argocd_redis_request_duration_seconds_bucket",
	"argocd_redis_request_duration_seconds_count",
	"argocd_redis_request_duration_seconds_sum",
	"argocd_redis_request_total",
	"argocd_repo_pending_request_total",
	"argocd_resource_events_processed_in_batch",
	"argocd_resource_events_processing_bucket",
	"argocd_resource_events_processing_count",
	"argocd_resource_events_processing_sum",
	// workqueue_* families
	"workqueue_adds_total",
	"workqueue_depth",
	"workqueue_longest_running_processor_seconds",
	"workqueue_queue_duration_seconds_bucket",
	"workqueue_queue_duration_seconds_count",
	"workqueue_queue_duration_seconds_sum",
	"workqueue_retries_total",
	"workqueue_unfinished_work_seconds",
	"workqueue_work_duration_seconds_bucket",
	"workqueue_work_duration_seconds_count",
	"workqueue_work_duration_seconds_sum",
	// redis_exporter sidecar metrics
	"redis_connected_clients",
	"redis_uptime_in_seconds",
}

func TestSeriesInventory(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)
	got := cap.Names()

	wantSet := map[string]bool{}
	for _, n := range expectedFamilies {
		wantSet[n] = true
	}
	gotSet := map[string]bool{}
	for _, n := range got {
		gotSet[n] = true
	}

	for _, n := range expectedFamilies {
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

// ─── (b) Base labels on every series ─────────────────────────────────────────

func TestBaseLabels(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)
	cl := clusterWithArgoCD(t)

	if len(cap.All()) == 0 {
		t.Fatal("no series emitted")
	}
	for _, s := range cap.All() {
		if s.Labels["cluster"] != cl.Name {
			t.Errorf("series %q: cluster=%q want %q", s.Name, s.Labels["cluster"], cl.Name)
		}
		if s.Labels["k8s_cluster_name"] != cl.Name {
			t.Errorf("series %q: k8s_cluster_name=%q want %q", s.Name, s.Labels["k8s_cluster_name"], cl.Name)
		}
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("series %q must NOT carry blueprint label (ScopeSubstrate)", s.Name)
		}
	}
}

// ─── (c) App-controller series: StatefulSet ordinal pod name + namespace=argocd ───

func TestAppControllerPodLabels(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	// argocd_app_info is emitted by app-controller.
	series := cap.Find("argocd_app_info")
	if len(series) == 0 {
		t.Fatal("no argocd_app_info series found")
	}

	for _, s := range series {
		pod := s.Labels["pod"]
		if pod == "" {
			t.Errorf("argocd_app_info: missing pod label, labels=%v", s.Labels)
			continue
		}
		// StatefulSet ordinal: pod name must end with "-0".
		if !strings.HasSuffix(pod, "-0") {
			t.Errorf("argocd_app_info: pod=%q should end with -0 (StatefulSet ordinal)", pod)
		}
		// Namespace must be argocd.
		if s.Labels["namespace"] != "argocd" {
			t.Errorf("argocd_app_info: namespace=%q want argocd", s.Labels["namespace"])
		}
		// instance must end with ":8082"
		if !strings.HasSuffix(s.Labels["instance"], ":8082") {
			t.Errorf("argocd_app_info: instance=%q must end with :8082", s.Labels["instance"])
		}
	}
}

// ─── (d) Redis series carry container=metrics (the real redis_exporter sidecar
// container name in kube_pod_container_info, svc-group-b.md §2.B) ─────────────

func TestRedisExporterContainer(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	series := cap.Find("redis_connected_clients")
	if len(series) == 0 {
		t.Fatal("no redis_connected_clients series found")
	}
	for _, s := range series {
		if s.Labels["container"] != "metrics" {
			t.Errorf("redis_connected_clients: container=%q want metrics", s.Labels["container"])
		}
		if s.Labels["namespace"] != "argocd" {
			t.Errorf("redis_connected_clients: namespace=%q want argocd", s.Labels["namespace"])
		}
		if !strings.HasSuffix(s.Labels["instance"], ":9121") {
			t.Errorf("redis_connected_clients: instance=%q must end with :9121", s.Labels["instance"])
		}
	}
}

// ─── (e) Absent series: controller_runtime_* and argocd_notifications_* ──────

func TestAbsentSeriesNotEmitted(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	for _, s := range cap.All() {
		if strings.HasPrefix(s.Name, "controller_runtime_") {
			t.Errorf("controller_runtime_* must NOT be emitted (not scraped on the reference cluster): %s", s.Name)
		}
		if strings.HasPrefix(s.Name, "argocd_notifications_") {
			t.Errorf("argocd_notifications_* must NOT be emitted (not scraped on the reference cluster): %s", s.Name)
		}
	}
}

// ─── (f) Two redis histogram families ────────────────────────────────────────

func TestRedisHistogramFamilies(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	// argocd_redis_request_duration (app-controller, has hostname label)
	durBucket := cap.Find("argocd_redis_request_duration_bucket")
	if len(durBucket) == 0 {
		t.Error("argocd_redis_request_duration_bucket: absent (app-controller family)")
	}
	for _, s := range durBucket {
		if s.Labels["hostname"] == "" {
			t.Errorf("argocd_redis_request_duration_bucket: missing hostname label (app-controller emitter)")
		}
	}

	// argocd_redis_request_duration_seconds (repo-server, NO hostname label)
	durSecBucket := cap.Find("argocd_redis_request_duration_seconds_bucket")
	if len(durSecBucket) == 0 {
		t.Error("argocd_redis_request_duration_seconds_bucket: absent (repo-server family)")
	}
	for _, s := range durSecBucket {
		if s.Labels["hostname"] != "" {
			t.Errorf("argocd_redis_request_duration_seconds_bucket: should NOT have hostname label (repo-server emitter), got %q", s.Labels["hostname"])
		}
	}
}

// ─── (g) All stamped series carry pod/namespace/instance ─────────────────────

func TestStampedSeriesHaveCorrelationLabels(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	// Pick representative argocd_ series that must be stamped.
	for _, name := range []string{
		"argocd_app_info",
		"argocd_cluster_info",
		"argocd_info",
	} {
		for _, s := range cap.Find(name) {
			if s.Labels["pod"] == "" {
				t.Errorf("%s: missing pod label", name)
			}
			if s.Labels["namespace"] == "" {
				t.Errorf("%s: missing namespace label", name)
			}
			if s.Labels["instance"] == "" {
				t.Errorf("%s: missing instance label", name)
			}
		}
	}
}

// ─── (h) Fallback: nil SubstrateWorkloads → cluster-scoped, no pod label ─────

func TestFallbackNoSubstrateWorkloads(t *testing.T) {
	c := buildDefault(t)
	cap := tickOnce(t, c)

	// Core argocd families must still be present.
	gotNames := map[string]bool{}
	for _, n := range cap.Names() {
		gotNames[n] = true
	}
	for _, n := range []string{
		"argocd_app_info",
		"argocd_cluster_info",
		"argocd_info",
		"argocd_redis_request_total",
	} {
		if !gotNames[n] {
			t.Errorf("fallback: MISSING %s", n)
		}
	}

	// In fallback mode, argocd_app_info must NOT carry a pod label.
	for _, s := range cap.Find("argocd_app_info") {
		if s.Labels["pod"] != "" {
			t.Errorf("fallback: argocd_app_info should not have pod label, got pod=%q", s.Labels["pod"])
		}
	}

	// controller_runtime_* still must not appear.
	for _, s := range cap.All() {
		if strings.HasPrefix(s.Name, "controller_runtime_") {
			t.Errorf("fallback: controller_runtime_* must NOT be emitted: %s", s.Name)
		}
	}
}

// ─── (i) Build error on nil Cluster ──────────────────────────────────────────

func TestBuildErrorOnNilCluster(t *testing.T) {
	_, err := argocd.New(&argocd.Config{}, &fixture.Set{Seed: "test"})
	if err == nil {
		t.Fatal("expected error when Cluster is nil, got nil")
	}
}

// ─── (j) Counters monotone across two ticks ──────────────────────────────────

func TestCountersMonotone(t *testing.T) {
	c := buildWithPods(t)

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
		"argocd_app_sync_total",
		"argocd_kubectl_requests_total",
		"workqueue_adds_total",
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

// ─── (k) Kind/Signals/Interval metadata ──────────────────────────────────────

func TestKindAndSignals(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "argocd" {
		t.Errorf("Kind() = %q, want %q", c.Kind(), "argocd")
	}
	sigs := c.Signals()
	// After logs lane: must include both Metrics and Logs.
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
		t.Errorf("Signals() missing core.Metrics, got %v", sigs)
	}
	if !hasLogs {
		t.Errorf("Signals() missing core.Logs, got %v", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval() = %v, want 60s", c.Interval())
	}
}

// ─── (m) Log lane: Signals() includes core.Logs ──────────────────────────────

func TestSignalsIncludesLogs(t *testing.T) {
	c := buildDefault(t)
	sigs := c.Signals()
	for _, s := range sigs {
		if s == core.Logs {
			return
		}
	}
	t.Errorf("Signals() = %v: missing core.Logs", sigs)
}

// tickWithLogs ticks the construct and returns (metric capture, log capture).
func tickWithLogs(t *testing.T, c core.Construct) (*coretest.MetricCapture, *coretest.LogCapture) {
	t.Helper()
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	if err := c.Tick(context.Background(), testNow, coretest.World(mc, lc, nil)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc, lc
}

// ─── (n) Log streams: OTel/Alloy stream labels on all components ─────────────

// TestLogStreamLabels verifies that each emitted log stream carries the required
// OTel/Alloy stream labels per svc-group-b.md §2.C. Runs multiple ticks to ensure
// streams are always emitted (argocd logs should emit every tick).
func TestLogStreamLabels(t *testing.T) {
	c := buildWithPods(t)
	cl := clusterWithArgoCD(t)

	var allStreams []loki.Stream
	for i := range 5 {
		mc := &coretest.MetricCapture{}
		lc := &coretest.LogCapture{}
		now := testNow.Add(time.Duration(i) * 60 * time.Second)
		if err := c.Tick(context.Background(), now, coretest.World(mc, lc, nil)); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		allStreams = append(allStreams, lc.Streams...)
	}

	if len(allStreams) == 0 {
		t.Fatal("no log streams emitted across 5 ticks")
	}

	for _, stream := range allStreams {
		// cluster + k8s_cluster_name must match the fixture cluster.
		if stream.Labels["cluster"] != cl.Name {
			t.Errorf("stream cluster=%q want %q", stream.Labels["cluster"], cl.Name)
		}
		if stream.Labels["k8s_cluster_name"] != cl.Name {
			t.Errorf("stream k8s_cluster_name=%q want %q", stream.Labels["k8s_cluster_name"], cl.Name)
		}
		// All argocd streams must be in namespace "argocd".
		if stream.Labels["k8s_namespace_name"] != "argocd" {
			t.Errorf("stream k8s_namespace_name=%q want argocd", stream.Labels["k8s_namespace_name"])
		}
		// All argocd components write to stderr.
		if stream.Labels["log_iostream"] != "stderr" {
			t.Errorf("stream log_iostream=%q want stderr", stream.Labels["log_iostream"])
		}
		// detected_level must be present.
		dl := stream.Labels["detected_level"]
		if dl == "" {
			t.Errorf("stream missing detected_level label; labels=%v", stream.Labels)
		}
		// service_namespace must be argocd.
		if stream.Labels["service_namespace"] != "argocd" {
			t.Errorf("stream service_namespace=%q want argocd", stream.Labels["service_namespace"])
		}
		// No blueprint label (ScopeSubstrate).
		if _, ok := stream.Labels["blueprint"]; ok {
			t.Errorf("log stream carries blueprint label (ScopeSubstrate violation)")
		}
		// Must have lines.
		if len(stream.Lines) == 0 {
			t.Errorf("stream has no lines; labels=%v", stream.Labels)
		}
	}
}

// ─── (o) Log streams: app-controller carries StatefulSet pod name + ordinal ───

func TestLogAppControllerPodName(t *testing.T) {
	c := buildWithPods(t)
	_, lc := tickWithLogs(t, c)

	// Find a stream for the application-controller container.
	var found []loki.Stream
	for _, s := range lc.Streams {
		if s.Labels["k8s_container_name"] == "application-controller" {
			found = append(found, s)
		}
	}
	if len(found) == 0 {
		t.Fatal("no log stream for k8s_container_name=application-controller")
	}
	for _, s := range found {
		pod := s.Labels["k8s_pod_name"]
		if pod == "" {
			t.Errorf("application-controller stream missing k8s_pod_name; labels=%v", s.Labels)
			continue
		}
		// StatefulSet ordinal: pod name must end with "-0".
		if !strings.HasSuffix(pod, "-0") {
			t.Errorf("application-controller k8s_pod_name=%q should end with -0 (StatefulSet ordinal)", pod)
		}
		// Must have k8s_statefulset_name or k8s_deployment_name.
		if s.Labels["k8s_statefulset_name"] == "" {
			t.Errorf("application-controller stream missing k8s_statefulset_name; labels=%v", s.Labels)
		}
	}
}

// ─── (p) Log streams: notifications-controller body is JSON; others are logfmt ─

func TestLogBodyFormats(t *testing.T) {
	c := buildWithPods(t)
	_, lc := tickWithLogs(t, c)

	if len(lc.Streams) == 0 {
		t.Fatal("no log streams emitted")
	}

	for _, s := range lc.Streams {
		container := s.Labels["k8s_container_name"]
		for _, line := range s.Lines {
			if line.Body == "" {
				t.Errorf("container=%q: empty log body", container)
				continue
			}
			switch container {
			case "notifications-controller":
				// Must be JSON.
				if line.Body[0] != '{' {
					t.Errorf("notifications-controller log body is not JSON: %q", line.Body)
				}
			default:
				// Must be logfmt: starts with `time="`.
				if !strings.HasPrefix(line.Body, `time="`) {
					t.Errorf("container=%q log body is not logfmt (expected time=...): %q", container, line.Body)
				}
			}
		}
	}
}

// ─── (q) Log streams: all 5 argocd components emit at least one stream ────────

func TestLogAllComponentsEmit(t *testing.T) {
	c := buildWithPods(t)
	_, lc := tickWithLogs(t, c)

	expectedContainers := []string{
		"application-controller",
		"server",
		"repo-server",
		"applicationset-controller",
		"notifications-controller",
	}

	seen := map[string]bool{}
	for _, s := range lc.Streams {
		seen[s.Labels["k8s_container_name"]] = true
	}
	for _, container := range expectedContainers {
		if !seen[container] {
			t.Errorf("no log stream emitted for k8s_container_name=%q", container)
		}
	}
}

// ─── (r) Log streams: no high-card stream labels (app names / sync IDs in body only) ─

func TestLogNoHighCardStreamLabels(t *testing.T) {
	c := buildWithPods(t)
	_, lc := tickWithLogs(t, c)

	// These are specifically called out as body-only in the spec.
	forbiddenStreamKeys := []string{
		"application",
		"app",
		"sync_id",
		"reconcileID",
		"name",
		"project",
	}

	for _, s := range lc.Streams {
		for _, k := range forbiddenStreamKeys {
			if _, ok := s.Labels[k]; ok {
				t.Errorf("stream has high-card key %q as stream label (must be in body only); container=%q labels=%v",
					k, s.Labels["k8s_container_name"], s.Labels)
			}
		}
	}
}

// ─── (s) Guard: nil Logs writer — Tick does not panic ─────────────────────────

func TestNilLogsDoesNotPanic(t *testing.T) {
	c := buildWithPods(t)
	mc := &coretest.MetricCapture{}
	// Pass nil for the LogCapture — the World will have w.Logs == nil.
	if err := c.Tick(context.Background(), testNow, coretest.World(mc, nil, nil)); err != nil {
		t.Fatalf("Tick with nil Logs: unexpected error: %v", err)
	}
}

// ─── (l) Registration metadata ────────────────────────────────────────────────

func TestRegistration(t *testing.T) {
	reg := argocd.Registration()
	if reg.Kind != "argocd" {
		t.Errorf("Registration.Kind = %q, want argocd", reg.Kind)
	}
	if reg.Scope != core.ScopeSubstrate {
		t.Errorf("Registration.Scope = %v, want ScopeSubstrate", reg.Scope)
	}
	if reg.NewConfig == nil {
		t.Error("Registration.NewConfig is nil")
	}
	if reg.Build == nil {
		t.Error("Registration.Build is nil")
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
