// SPDX-License-Identifier: AGPL-3.0-only

package lbc_test

// lbc_test.go — construct invariant tests for the load_balancer_controller construct.
//
// Test inventory:
//   (a) Exact series name inventory — all expected names present, no extras.
//   (b) rest_client_* present — LBC DOES emit rest_client_* (live recon svc-lbc.md §C5).
//       NOTE: the original test asserted absence (incorrect); corrected to assert presence.
//   (c) cluster + k8s_cluster_name labels on every series.
//   (d) No blueprint label on any series (ScopeSubstrate invariant, ARCHITECTURE I21).
//   (e) Counters are monotone — two successive ticks at distinct times show non-decreasing values.
//   (f) build_info is NOT in inventory (LBC does not expose it — absence is correct).
//   (g) Build returns an error when fx.Cluster is nil.
//   (h) Kind/Signals/Interval metadata.
//   (i) Per-pod stamp: awslbc_* and aws_api_* → leader pod only (one pod, not both).
//   (j) Per-pod stamp: controller_runtime_* / rest_client_* / workqueue_* → ALL pods (both).
//   (k) aws_api_* carries exported_service label (not service).
//   (l) Fallback: nil SubstrateWorkloads → cluster-scoped (no pod label, no crash).
//   (m) Logs: Signals() includes core.Logs.
//   (n) Logs: stream labels correct (k8s_namespace_name=kube-system, detected_level, etc.).
//   (o) Logs: k8s_pod_name matches an LBC pod name.
//   (p) Logs: no high-card stream labels (no reconcileID, error text, ARNs).
//   (q) Logs: zap-JSON body contains "ts" field.
//   (r) Logs: nil w.Logs → no crash (guard).

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/lbc"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// highCardLBCLabels is the set of high-cardinality keys that must NEVER appear
// as Loki stream labels (only in the body).
var highCardLBCLabels = []string{
	"reconcileID",
	"error",
	"arn",
	"stackID",
	"resourceID",
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// clusterWithAddonPods returns a coretest.Cluster with load_balancer_controller
// SubstrateWorkloads populated (the "with-pods" path for per-pod stamp tests).
func clusterWithAddonPods(t *testing.T) *fixture.Cluster {
	t.Helper()
	cl := coretest.Cluster()
	seed := "test"
	wls := fixture.AddonWorkloads("load_balancer_controller")
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

// buildWithPods builds a construct whose cluster has SubstrateWorkloads populated.
func buildWithPods(t *testing.T) core.Construct {
	t.Helper()
	reg := lbc.Registration()
	c, err := reg.Build(&lbc.Config{}, &fixture.Set{
		Cluster: clusterWithAddonPods(t),
	})
	if err != nil {
		t.Fatalf("lbc.Build (with pods): %v", err)
	}
	return c
}

func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	reg := lbc.Registration()
	c, err := reg.Build(&lbc.Config{}, &fixture.Set{
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("lbc.Build: %v", err)
	}
	return c
}

// tickOnce runs one Tick and returns (names, all series).
func tickOnce(t *testing.T, c core.Construct, now time.Time) ([]string, []promrw.Series) {
	t.Helper()
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc.Names(), mc.All()
}

// tickOnceWithLogs runs one Tick and returns (names, all series, log capture).
func tickOnceWithLogs(t *testing.T, c core.Construct, now time.Time) ([]string, []promrw.Series, *coretest.LogCapture) {
	t.Helper()
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := coretest.World(mc, lc, nil)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc.Names(), mc.All(), lc
}

var businessHours = time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

// ─── (a) Exact series name inventory ─────────────────────────────────────────

// expectedLBCNames is the canonical inventory derived from signals/k8s-addons.md [slug: k8s-lbc]
// and live recon svc-lbc.md (2026-06-16). rest_client_* IS emitted (recon-confirmed).
// Histograms expand to _bucket + _sum + _count; base names that expand are listed as base.
// The expected set includes the expanded forms.
//
// NOTE: This inventory is for the "with-pods" path; cluster-scoped (no SubstrateWorkloads)
// still emits core awslbc_*/aws_api_*/controller_runtime_*/workqueue_* but the per-pod stamp
// tests cover that distinction separately.
var expectedLBCNames = func() []string {
	// Counters (scalar, via Add → emitted as the name itself).
	scalars := []string{
		// awslbc_*
		"awslbc_controller_reconcile_errors_total",
		"awslbc_webhook_validation_failure_total",
		"awslbc_webhook_mutation_failure_total",
		"awslbc_quic_target_missing_server_id",
		// awslbc_* gauges
		"awslbc_controller_cache_object_total",
		"awslbc_controller_top_talkers",
		// aws_api_* counters
		"aws_api_calls_total",
		"aws_api_requests_total",
		"aws_api_call_permission_errors_total",
		"aws_api_call_service_limit_exceeded_errors_total",
		"aws_api_call_throttled_errors_total",
		"aws_api_call_validation_errors_total",
		// aws_api_* gauge
		"aws_target_group_info",
		// controller_runtime_* counters
		"controller_runtime_reconcile_total",
		"controller_runtime_reconcile_errors_total",
		"controller_runtime_webhook_requests_total",
		// controller_runtime_* gauges
		"controller_runtime_active_workers",
		"controller_runtime_max_concurrent_reconciles",
		// workqueue_* counter
		"workqueue_adds_total",
		"workqueue_retries_total",
		// workqueue_* gauge
		"workqueue_depth",
		// rest_client_* counters (live recon svc-lbc.md §A, §E — LBC uses client-go)
		"rest_client_requests_total",
	}

	// Histograms expand to _bucket, _sum, _count.
	histoBases := []string{
		"awslbc_readiness_gate_ready_seconds",
		"awslbc_controller_reconcile_stage_duration",
		"aws_api_call_duration_seconds",
		"aws_api_call_retries",
		"aws_api_request_duration_seconds",
		"controller_runtime_reconcile_time_seconds",
		"controller_runtime_webhook_latency_seconds",
		"workqueue_queue_duration_seconds",
		"workqueue_work_duration_seconds",
	}

	set := map[string]struct{}{}
	for _, n := range scalars {
		set[n] = struct{}{}
	}
	for _, base := range histoBases {
		set[base+"_bucket"] = struct{}{}
		set[base+"_sum"] = struct{}{}
		set[base+"_count"] = struct{}{}
	}

	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}()

func TestSeriesInventory(t *testing.T) {
	// Use buildWithPods so rest_client_* (per-pod stamped) is also included.
	c := buildWithPods(t)
	got, _ := tickOnce(t, c, businessHours)

	wantSet := map[string]bool{}
	for _, n := range expectedLBCNames {
		wantSet[n] = true
	}
	gotSet := map[string]bool{}
	for _, n := range got {
		gotSet[n] = true
	}

	for _, n := range expectedLBCNames {
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

// ─── (a2) Job label ──────────────────────────────────────────────────────────

// TestJobLabel asserts that every emitted series carries job="aws-load-balancer-controller"
// (bare name, SK-10 live-confirmed 2026-06-14 — NOT "integrations/aws-load-balancer-controller").
func TestJobLabel(t *testing.T) {
	c := buildDefault(t)
	_, all := tickOnce(t, c, businessHours)
	if len(all) == 0 {
		t.Fatal("no series emitted")
	}
	const wantJob = "aws-load-balancer-controller"
	for _, s := range all {
		if got := s.Labels["job"]; got != wantJob {
			t.Errorf("series %q: job=%q want %q", s.Name, got, wantJob)
		}
	}
}

// ─── (b) rest_client_* present (with pods) ───────────────────────────────────

// TestRestClientMetricsPresent asserts that LBC DOES emit rest_client_requests_total
// when SubstrateWorkloads is populated (live recon svc-lbc.md §A.6: LBC is a
// controller-runtime/sigs.k8s.io based controller that uses client-go for kube-apiserver).
//
// NOTE: The original test asserted ABSENCE ("cluster-autoscaler only") — that was
// incorrect. Live recon from a live reference cluster (2026-06-16) confirms rest_client_* is emitted.
// The absence test is replaced by this positive assertion. See svc-lbc.md §E gotcha 2.
func TestRestClientMetricsPresent(t *testing.T) {
	c := buildWithPods(t)
	names, _ := tickOnce(t, c, businessHours)
	found := false
	for _, n := range names {
		if strings.HasPrefix(n, "rest_client") {
			found = true
			break
		}
	}
	if !found {
		t.Error("rest_client_* MUST be emitted by lbc when SubstrateWorkloads is populated (live recon svc-lbc.md §A.6)")
	}
}

// ─── (c) cluster + k8s_cluster_name on every series ──────────────────────────

func TestClusterLabels(t *testing.T) {
	cl := coretest.Cluster()
	reg := lbc.Registration()
	c, err := reg.Build(&lbc.Config{}, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_, all := tickOnce(t, c, businessHours)
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
		if s.Labels["cluster"] != cl.Name {
			t.Errorf("series %q: cluster=%q want %q", s.Name, s.Labels["cluster"], cl.Name)
		}
		if s.Labels["k8s_cluster_name"] != cl.Name {
			t.Errorf("series %q: k8s_cluster_name=%q want %q", s.Name, s.Labels["k8s_cluster_name"], cl.Name)
		}
	}
}

// ─── (d) No blueprint label ───────────────────────────────────────────────────

// TestNoBlueprintLabel asserts ScopeSubstrate: no series ever carries a "blueprint" label.
func TestNoBlueprintLabel(t *testing.T) {
	c := buildDefault(t)
	_, all := tickOnce(t, c, businessHours)
	for _, s := range all {
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("ScopeSubstrate violation: series %q carries blueprint label", s.Name)
		}
	}
}

// ─── (e) Counters are monotone ────────────────────────────────────────────────

// TestCountersMonotone asserts I3: cumulative counters only increase across ticks.
// Uses the full label set as the series key so stochastic label values don't collapse
// distinct series onto the same slot.
func TestCountersMonotone(t *testing.T) {
	c := buildDefault(t)
	now := businessHours
	mc1 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, nil, nil)
	if err := c.Tick(context.Background(), now, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}

	// Second tick 60s later — all counters must be >= first tick.
	mc2 := &coretest.MetricCapture{}
	w2 := coretest.World(mc2, nil, nil)
	if err := c.Tick(context.Background(), now.Add(60*time.Second), w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	// Use a stable series signature that includes the full sorted label set so distinct
	// series (e.g. code=200 vs code=400) don't overwrite each other in the map.
	sig := func(s promrw.Series) string {
		keys := make([]string, 0, len(s.Labels))
		for k := range s.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString(s.Name)
		for _, k := range keys {
			b.WriteByte('|')
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(s.Labels[k])
		}
		return b.String()
	}

	// isCounter identifies series that use state.Add (cumulative counters/histograms).
	// Excludes awslbc_controller_cache_object_total which is a GAUGE named with _total
	// (signals/k8s-addons.md [slug: k8s-lbc]: "awslbc_controller_cache_object_total (G)").
	// Histogram _count and _sum are always cumulative.
	isCounter := func(name string) bool {
		if strings.HasSuffix(name, "_count") || strings.HasSuffix(name, "_sum") {
			return true
		}
		if name == "awslbc_controller_cache_object_total" {
			return false // gauge, despite the _total suffix
		}
		return strings.HasSuffix(name, "_total")
	}

	vals1 := map[string]float64{}
	for _, s := range mc1.All() {
		if isCounter(s.Name) {
			vals1[sig(s)] = s.Value
		}
	}
	vals2 := map[string]float64{}
	for _, s := range mc2.All() {
		if isCounter(s.Name) {
			vals2[sig(s)] = s.Value
		}
	}

	for k, v1 := range vals1 {
		v2, ok := vals2[k]
		if !ok {
			continue
		}
		if v2 < v1 {
			t.Errorf("counter regression: sig=%q tick1=%.4f tick2=%.4f", k, v1, v2)
		}
	}
}

// ─── (f) build_info absent ───────────────────────────────────────────────────

// TestNoBuildInfo verifies that lbc does not emit a build_info series
// (LBC does not expose one — its absence is correct per the signal contract).
func TestNoBuildInfo(t *testing.T) {
	c := buildDefault(t)
	names, _ := tickOnce(t, c, businessHours)
	for _, n := range names {
		if strings.HasSuffix(n, "_build_info") || n == "build_info" {
			t.Errorf("lbc must not emit build_info; found %q", n)
		}
	}
}

// ─── (g) Build error on nil Cluster ──────────────────────────────────────────

func TestBuildErrorOnNilCluster(t *testing.T) {
	reg := lbc.Registration()
	_, err := reg.Build(&lbc.Config{}, &fixture.Set{Cluster: nil})
	if err == nil {
		t.Fatal("expected error when fx.Cluster is nil, got nil")
	}
}

// ─── (h) Kind / Signals / Interval metadata ──────────────────────────────────

func TestMetadata(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "load_balancer_controller" {
		t.Errorf("Kind() = %q, want %q", c.Kind(), "load_balancer_controller")
	}
	sigs := c.Signals()
	if len(sigs) != 2 {
		t.Fatalf("Signals() len=%d, want 2 (Metrics + Logs)", len(sigs))
	}
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
		t.Errorf("Signals() missing core.Metrics")
	}
	if !hasLogs {
		t.Errorf("Signals() missing core.Logs")
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval() = %v, want 60s", c.Interval())
	}
}

// TestRegistrationScope verifies the construct is registered with ScopeSubstrate.
func TestRegistrationScope(t *testing.T) {
	reg := lbc.Registration()
	if reg.Scope != core.ScopeSubstrate {
		t.Errorf("Registration().Scope = %v, want ScopeSubstrate", reg.Scope)
	}
}

// ─── (i) Per-pod stamp: awslbc_* and aws_api_* → leader pod only ─────────────

// TestLeaderOnlyAWSLBC verifies that awslbc_controller_reconcile_errors_total,
// awslbc_controller_top_talkers, and awslbc_controller_reconcile_stage_duration_bucket
// are stamped with the leader pod label-set only (one pod, not both).
// Also asserts pod/namespace=kube-system/container/instance ending :8080.
func TestLeaderOnlyAWSLBC(t *testing.T) {
	c := buildWithPods(t)
	_, all := tickOnce(t, c, businessHours)

	for _, metricName := range []string{
		"awslbc_controller_reconcile_errors_total",
		"awslbc_controller_top_talkers",
		"awslbc_controller_reconcile_stage_duration_bucket",
	} {
		var series []promrw.Series
		for _, s := range all {
			if s.Name == metricName {
				series = append(series, s)
			}
		}
		if len(series) == 0 {
			t.Errorf("MISSING series: %s", metricName)
			continue
		}

		// Collect distinct pods
		pods := map[string]bool{}
		for _, s := range series {
			pod := s.Labels["pod"]
			if pod == "" {
				t.Errorf("%s: series missing pod label: %v", metricName, s.Labels)
				continue
			}
			pods[pod] = true

			// namespace must be kube-system
			if s.Labels["namespace"] != "kube-system" {
				t.Errorf("%s: namespace=%q want kube-system", metricName, s.Labels["namespace"])
			}
			// container must be aws-load-balancer-controller
			if s.Labels["container"] != "aws-load-balancer-controller" {
				t.Errorf("%s: container=%q want aws-load-balancer-controller", metricName, s.Labels["container"])
			}
			// instance must end with :8080
			if !strings.HasSuffix(s.Labels["instance"], ":8080") {
				t.Errorf("%s: instance=%q must end with :8080", metricName, s.Labels["instance"])
			}
		}

		// Leader-only: exactly ONE distinct pod name.
		if len(pods) != 1 {
			t.Errorf("%s: leader-only: expected 1 distinct pod, got %d: %v", metricName, len(pods), pods)
		}
	}
}

// TestLeaderOnlyAWSAPI verifies that aws_api_calls_total is stamped on the leader
// pod only and carries exported_service (not service).
func TestLeaderOnlyAWSAPI(t *testing.T) {
	c := buildWithPods(t)
	_, all := tickOnce(t, c, businessHours)

	var series []promrw.Series
	for _, s := range all {
		if s.Name == "aws_api_calls_total" {
			series = append(series, s)
		}
	}
	if len(series) == 0 {
		t.Fatal("no aws_api_calls_total series found")
	}

	pods := map[string]bool{}
	for _, s := range series {
		pod := s.Labels["pod"]
		if pod == "" {
			t.Errorf("aws_api_calls_total: missing pod label: %v", s.Labels)
		}
		pods[pod] = true

		// Must carry exported_service, not service.
		if s.Labels["exported_service"] == "" {
			t.Errorf("aws_api_calls_total: missing exported_service label: %v", s.Labels)
		}
		if _, hasService := s.Labels["service"]; hasService {
			t.Errorf("aws_api_calls_total: must use exported_service not service: %v", s.Labels)
		}

		// instance must end with :8080
		if !strings.HasSuffix(s.Labels["instance"], ":8080") {
			t.Errorf("aws_api_calls_total: instance=%q must end with :8080", s.Labels["instance"])
		}
	}

	// Leader-only: exactly ONE distinct pod.
	if len(pods) != 1 {
		t.Errorf("aws_api_calls_total: leader-only: expected 1 distinct pod, got %d: %v", len(pods), pods)
	}
}

// ─── (j) Per-pod stamp: controller_runtime_* → ALL pods (both) ───────────────

// TestAllPodsControllerRuntime verifies that controller_runtime_reconcile_total
// is stamped on BOTH pods (two distinct pod values in the label sets).
// Also asserts namespace=kube-system, container=aws-load-balancer-controller, instance :8080.
func TestAllPodsControllerRuntime(t *testing.T) {
	c := buildWithPods(t)
	_, all := tickOnce(t, c, businessHours)

	var series []promrw.Series
	for _, s := range all {
		if s.Name == "controller_runtime_reconcile_total" {
			series = append(series, s)
		}
	}
	if len(series) == 0 {
		t.Fatal("no controller_runtime_reconcile_total series found")
	}

	pods := map[string]bool{}
	for _, s := range series {
		pod := s.Labels["pod"]
		if pod == "" {
			t.Errorf("controller_runtime_reconcile_total: missing pod label: %v", s.Labels)
			continue
		}
		pods[pod] = true

		if s.Labels["namespace"] != "kube-system" {
			t.Errorf("controller_runtime_reconcile_total: namespace=%q want kube-system", s.Labels["namespace"])
		}
		if s.Labels["container"] != "aws-load-balancer-controller" {
			t.Errorf("controller_runtime_reconcile_total: container=%q want aws-load-balancer-controller", s.Labels["container"])
		}
		if !strings.HasSuffix(s.Labels["instance"], ":8080") {
			t.Errorf("controller_runtime_reconcile_total: instance=%q must end with :8080", s.Labels["instance"])
		}
	}

	// All-pods: LBC has 2 replicas — both must be present.
	// AddonWorkloads("load_balancer_controller") returns Replicas: 2.
	if len(pods) < 2 {
		t.Errorf("controller_runtime_reconcile_total: expected ≥2 distinct pods (all replicas), got %d: %v", len(pods), pods)
	}
}

// TestAllPodsRestClient verifies that rest_client_requests_total is stamped on
// BOTH pods (per-pod, not leader-only).
func TestAllPodsRestClient(t *testing.T) {
	c := buildWithPods(t)
	_, all := tickOnce(t, c, businessHours)

	var series []promrw.Series
	for _, s := range all {
		if s.Name == "rest_client_requests_total" {
			series = append(series, s)
		}
	}
	if len(series) == 0 {
		t.Fatal("no rest_client_requests_total series found (must be emitted with SubstrateWorkloads)")
	}

	pods := map[string]bool{}
	for _, s := range series {
		pod := s.Labels["pod"]
		if pod == "" {
			t.Errorf("rest_client_requests_total: missing pod label: %v", s.Labels)
			continue
		}
		pods[pod] = true

		if !strings.HasSuffix(s.Labels["instance"], ":8080") {
			t.Errorf("rest_client_requests_total: instance=%q must end with :8080", s.Labels["instance"])
		}
	}

	// Per-pod: both replicas must appear.
	if len(pods) < 2 {
		t.Errorf("rest_client_requests_total: expected ≥2 distinct pods (per-pod), got %d: %v", len(pods), pods)
	}
}

// TestAllPodsWorkqueue verifies that workqueue_adds_total is stamped on BOTH pods.
func TestAllPodsWorkqueue(t *testing.T) {
	c := buildWithPods(t)
	_, all := tickOnce(t, c, businessHours)

	var series []promrw.Series
	for _, s := range all {
		if s.Name == "workqueue_adds_total" {
			series = append(series, s)
		}
	}
	if len(series) == 0 {
		t.Fatal("no workqueue_adds_total series found")
	}

	pods := map[string]bool{}
	for _, s := range series {
		pod := s.Labels["pod"]
		if pod == "" {
			t.Errorf("workqueue_adds_total: missing pod label: %v", s.Labels)
			continue
		}
		pods[pod] = true
	}

	if len(pods) < 2 {
		t.Errorf("workqueue_adds_total: expected ≥2 distinct pods (per-pod), got %d: %v", len(pods), pods)
	}
}

// ─── (k) exported_service label on aws_api_* ─────────────────────────────────

// TestExportedServiceLabel asserts that ALL aws_api_* series use exported_service
// as the AWS-service dimension (not service), which Prometheus relabelling applies
// because Alloy's identity label already stamps "service" (recon svc-lbc.md §E.3).
func TestExportedServiceLabel(t *testing.T) {
	c := buildWithPods(t)
	_, all := tickOnce(t, c, businessHours)

	for _, s := range all {
		if !strings.HasPrefix(s.Name, "aws_api_") {
			continue
		}
		// aws_target_group_info is NOT an aws_api_* metric — skip.
		if s.Name == "aws_target_group_info" {
			continue
		}
		if _, hasService := s.Labels["service"]; hasService {
			t.Errorf("%s: must use exported_service not service label: %v", s.Name, s.Labels)
		}
		if s.Labels["exported_service"] == "" {
			t.Errorf("%s: missing exported_service label: %v", s.Name, s.Labels)
		}
	}
}

// ─── (l) Fallback: nil SubstrateWorkloads → cluster-scoped ───────────────────

// TestFallbackNilSubstrateWorkloads verifies back-compat: with no SubstrateWorkloads,
// the construct still emits cluster-scoped awslbc_*/aws_api_* series (no pod label, no crash).
func TestFallbackNilSubstrateWorkloads(t *testing.T) {
	c := buildDefault(t) // no SubstrateWorkloads
	_, all := tickOnce(t, c, businessHours)

	if len(all) == 0 {
		t.Fatal("fallback: no series emitted")
	}

	// Core awslbc_* must be present.
	found := false
	for _, s := range all {
		if s.Name == "awslbc_controller_cache_object_total" {
			found = true
			// Fallback: no pod label.
			if s.Labels["pod"] != "" {
				t.Errorf("fallback: awslbc_controller_cache_object_total should not have pod label, got pod=%q", s.Labels["pod"])
			}
			break
		}
	}
	if !found {
		t.Error("fallback: MISSING awslbc_controller_cache_object_total")
	}

	// aws_api_calls_total must be present.
	foundAPI := false
	for _, s := range all {
		if s.Name == "aws_api_calls_total" {
			foundAPI = true
			if s.Labels["pod"] != "" {
				t.Errorf("fallback: aws_api_calls_total should not have pod label, got pod=%q", s.Labels["pod"])
			}
			break
		}
	}
	if !foundAPI {
		t.Error("fallback: MISSING aws_api_calls_total")
	}
}

// ─── (m) Signals() includes core.Logs ────────────────────────────────────────

// TestSignalsIncludeLogs is a focused assertion that Signals() returns both
// core.Metrics and core.Logs (the full metadata test covers both; this one is explicit).
func TestSignalsIncludeLogs(t *testing.T) {
	c := buildDefault(t)
	sigs := c.Signals()
	found := false
	for _, s := range sigs {
		if s == core.Logs {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Signals() does not include core.Logs; got %v", sigs)
	}
}

// ─── (n–q) Log stream label + body assertions ─────────────────────────────────

// TestLBCLogsStreamLabels runs 20 ticks to guarantee at least one log emission
// (LBC emits every tick, but guard in case cadence changes) and validates:
//
//	(n) k8s_namespace_name=kube-system, k8s_container_name=aws-load-balancer-controller,
//	    k8s_deployment_name=aws-load-balancer-controller, service_namespace=kube-system,
//	    log_iostream=stderr, detected_level ∈ {info, error}.
//	(o) k8s_pod_name is set and matches a real LBC pod from SubstrateWorkloads.
//	(p) No high-card stream label (reconcileID, error text, ARNs).
//	(q) Body is zap-JSON containing "ts" field.
func TestLBCLogsStreamLabels(t *testing.T) {
	// Use the with-pods build so k8s_pod_name can be checked against real pod names.
	cl := clusterWithAddonPods(t)
	reg := lbc.Registration()
	c, err := reg.Build(&lbc.Config{}, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Collect known pod names for the LBC workload.
	lbcPodNames := map[string]bool{}
	for _, wl := range cl.SubstrateWorkloads {
		if wl.Name == "aws-load-balancer-controller" {
			for _, pn := range wl.PodNames {
				lbcPodNames[pn] = true
			}
		}
	}

	var gotStreams int
	for i := range 20 {
		mc := &coretest.MetricCapture{}
		lc := &coretest.LogCapture{}
		w := coretest.World(mc, lc, nil)
		now := businessHours.Add(time.Duration(i) * 60 * time.Second)
		if err := c.Tick(context.Background(), now, w); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		for _, stream := range lc.Streams {
			gotStreams++

			// (n) Required stream labels with fixed values.
			if stream.Labels["k8s_namespace_name"] != "kube-system" {
				t.Errorf("k8s_namespace_name=%q want kube-system", stream.Labels["k8s_namespace_name"])
			}
			if stream.Labels["k8s_container_name"] != "aws-load-balancer-controller" {
				t.Errorf("k8s_container_name=%q want aws-load-balancer-controller", stream.Labels["k8s_container_name"])
			}
			if stream.Labels["k8s_deployment_name"] != "aws-load-balancer-controller" {
				t.Errorf("k8s_deployment_name=%q want aws-load-balancer-controller", stream.Labels["k8s_deployment_name"])
			}
			if stream.Labels["service_namespace"] != "kube-system" {
				t.Errorf("service_namespace=%q want kube-system", stream.Labels["service_namespace"])
			}
			if stream.Labels["log_iostream"] != "stderr" {
				t.Errorf("log_iostream=%q want stderr", stream.Labels["log_iostream"])
			}
			dl := stream.Labels["detected_level"]
			if dl != "info" && dl != "error" {
				t.Errorf("detected_level=%q want info or error", dl)
			}
			if stream.Labels["cluster"] == "" {
				t.Error("stream missing cluster label")
			}
			if stream.Labels["k8s_cluster_name"] == "" {
				t.Error("stream missing k8s_cluster_name label")
			}
			if stream.Labels["service_name"] == "" {
				t.Error("stream missing service_name label")
			}

			// (o) k8s_pod_name set and matches a real pod.
			podName := stream.Labels["k8s_pod_name"]
			if podName == "" {
				t.Error("stream missing k8s_pod_name label")
			} else if len(lbcPodNames) > 0 && !lbcPodNames[podName] {
				t.Errorf("k8s_pod_name=%q not in known LBC pod names %v", podName, lbcPodNames)
			}

			// (p) No high-card stream labels.
			for _, badKey := range highCardLBCLabels {
				if _, ok := stream.Labels[badKey]; ok {
					t.Errorf("stream carries high-cardinality label %q (must be body-only)", badKey)
				}
			}

			// ScopeSubstrate: no blueprint label.
			if _, ok := stream.Labels["blueprint"]; ok {
				t.Errorf("log stream carries blueprint label (ScopeSubstrate violation)")
			}

			if len(stream.Lines) == 0 {
				t.Error("stream has no lines")
			}

			// (q) Body is zap-JSON and contains "ts" field.
			for _, line := range stream.Lines {
				if len(line.Body) == 0 || line.Body[0] != '{' {
					t.Errorf("log body is not JSON: %q", line.Body)
					continue
				}
				if !strings.Contains(line.Body, `"ts"`) {
					t.Errorf("log body missing zap ts field: %q", line.Body)
				}
			}
		}
	}
	if gotStreams == 0 {
		t.Error("no log streams emitted across 20 ticks")
	}
}

// ─── (r) nil w.Logs → no crash ────────────────────────────────────────────────

// TestLogsNilWorldLogsNocrash verifies the guard: when w.Logs==nil Tick must
// complete without panic or error (metrics still emit normally).
func TestLogsNilWorldLogsNocrash(t *testing.T) {
	c := buildDefault(t)
	mc := &coretest.MetricCapture{}
	// Deliberately pass nil for logs.
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), businessHours, w); err != nil {
		t.Fatalf("Tick with nil Logs: %v", err)
	}
	if len(mc.All()) == 0 {
		t.Error("no metrics emitted when Logs is nil")
	}
}
