// SPDX-License-Identifier: AGPL-3.0-only

package certmanager_test

// certmanager_test.go — construct invariant tests for the cert_manager construct.
//
// Test inventory:
//   (a) Exact series name inventory (all expected names, no extras).
//   (b) condition-enum completeness: ALL three condition values emitted for
//       certmanager_certificate_ready_status; exactly one =1 per cert.
//   (c) cluster + k8s_cluster_name on every series; NO blueprint label.
//   (d) Expiry timestamps are plausible (> now; < now+120d) and stable across ticks.
//   (e) Counters are monotone across two ticks.
//   (f) Build error on nil Cluster.
//   (g) Job-mode toggle + label corrections.
//   (h) Per-pod correlation: certmanager_* carries leader pod labels; controller_runtime_*
//       carries webhook/cainjector pod labels; fallback to cluster-scoped when no SubstrateWorkloads.
//   (i) ACME summary: quantile+_sum+_count emitted; no _bucket series.

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/certmanager"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// clusterWithAddons returns a coretest.Cluster with cert_manager SubstrateWorkloads
// populated (the "with-pods" path).
func clusterWithAddons(t *testing.T) *fixture.Cluster {
	t.Helper()
	cl := coretest.Cluster()
	seed := "test"
	wls := fixture.AddonWorkloads("cert_manager")
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

func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	c, err := certmanager.New(&certmanager.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("certmanager.New: %v", err)
	}
	return c
}

// buildWithPods builds a construct whose cluster has SubstrateWorkloads populated.
func buildWithPods(t *testing.T) core.Construct {
	t.Helper()
	c, err := certmanager.New(&certmanager.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: clusterWithAddons(t),
	})
	if err != nil {
		t.Fatalf("certmanager.New: %v", err)
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

// filterLabel returns those series from ss where Labels[key]==val.
func filterLabel(ss []promrw.Series, key, val string) []promrw.Series {
	var out []promrw.Series
	for _, s := range ss {
		if s.Labels[key] == val {
			out = append(out, s)
		}
	}
	return out
}

// ─── (a) Exact series name inventory ─────────────────────────────────────────

// expectedNames is the canonical series-name inventory derived from signals/k8s-addons.md [slug: k8s-cert-manager].
// certmanager_http_acme_client_request_duration_seconds is a Prometheus SUMMARY — emits
// the base name with a quantile label (NEW) plus _count and _sum companions.
// certmanager_certificate_challenge_status is NOT emitted (absent in live idle data).
// certmanager_clock_time_seconds is emitted alongside _gauge (live reference fact).
// controller_runtime_* / workqueue_* / rest_client_* are emitted when SubstrateWorkloads
// is populated — they are NOT in this inventory (tested separately via with-pods helpers).
var expectedNames = func() []string {
	names := []string{
		"certmanager_certificate_ready_status",
		"certmanager_certificate_expiration_timestamp_seconds",
		"certmanager_certificate_renewal_timestamp_seconds",
		"certmanager_certificate_not_after_timestamp_seconds",
		"certmanager_certificate_not_before_timestamp_seconds",
		"certmanager_issuer_ready_status",
		"certmanager_clusterissuer_ready_status",
		"certmanager_http_acme_client_request_count",
		"certmanager_http_acme_client_request_duration_seconds",       // summary quantile series (NEW)
		"certmanager_http_acme_client_request_duration_seconds_count", // summary _count
		"certmanager_http_acme_client_request_duration_seconds_sum",   // summary _sum
		"certmanager_controller_sync_call_count",
		"certmanager_controller_sync_error_count",
		"certmanager_clock_time_seconds",
		"certmanager_clock_time_seconds_gauge",
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

// ─── (b) condition-enum completeness ──────────────────────────────────────────

// TestConditionEnumCompleteness verifies that certmanager_certificate_ready_status
// carries ALL three condition values (True, False, Unknown) per cert and exactly
// one is =1 (True).
func TestConditionEnumCompleteness(t *testing.T) {
	c := buildDefault(t)
	cap := tickOnce(t, c)

	// Group by cert name → condition → value
	type key struct{ name, cond string }
	vals := map[key]float64{}
	for _, s := range cap.Find("certmanager_certificate_ready_status") {
		k := key{s.Labels["name"], s.Labels["condition"]}
		vals[k] = s.Value
	}

	certs := []string{"demo-tls", "api-tls", "internal-tls"}
	conds := []string{"True", "False", "Unknown"}

	for _, cert := range certs {
		trueCount := 0
		for _, cond := range conds {
			v, ok := vals[key{cert, cond}]
			if !ok {
				t.Errorf("cert %q condition %q: series absent", cert, cond)
				continue
			}
			if cond == "True" {
				if v != 1.0 {
					t.Errorf("cert %q condition True: want 1.0 got %.1f", cert, v)
				}
				trueCount++
			} else {
				if v != 0.0 {
					t.Errorf("cert %q condition %q: want 0.0 got %.1f", cert, cond, v)
				}
			}
		}
		if trueCount != 1 {
			t.Errorf("cert %q: expected exactly 1 True series, got %d", cert, trueCount)
		}
	}

	// Same invariant for certmanager_issuer_ready_status
	type issuerKey struct{ name, cond string }
	issuerVals := map[issuerKey]float64{}
	for _, s := range cap.Find("certmanager_issuer_ready_status") {
		k := issuerKey{s.Labels["name"], s.Labels["condition"]}
		issuerVals[k] = s.Value
	}
	for _, cond := range conds {
		v, ok := issuerVals[issuerKey{"internal-ca", cond}]
		if !ok {
			t.Errorf("issuer internal-ca condition %q: series absent", cond)
			continue
		}
		if cond == "True" && v != 1.0 {
			t.Errorf("issuer condition True: want 1.0 got %.1f", v)
		}
	}

	// And certmanager_clusterissuer_ready_status
	type ciKey struct{ name, cond string }
	ciVals := map[ciKey]float64{}
	for _, s := range cap.Find("certmanager_clusterissuer_ready_status") {
		k := ciKey{s.Labels["name"], s.Labels["condition"]}
		ciVals[k] = s.Value
	}
	for _, cond := range conds {
		v, ok := ciVals[ciKey{"letsencrypt-prod", cond}]
		if !ok {
			t.Errorf("clusterissuer letsencrypt-prod condition %q: series absent", cond)
			continue
		}
		if cond == "True" && v != 1.0 {
			t.Errorf("clusterissuer condition True: want 1.0 got %.1f", v)
		}
	}
}

// ─── (c) cluster + k8s_cluster_name on every series; NO blueprint label ───────

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

// ─── (d) Expiry timestamps are plausible and stable ──────────────────────────

func TestExpiryTimestamps(t *testing.T) {
	c := buildDefault(t)
	cap := tickOnce(t, c)

	nowUnix := float64(testNow.Unix())
	day := float64(86400)

	for _, name := range []string{
		"certmanager_certificate_expiration_timestamp_seconds",
		"certmanager_certificate_not_after_timestamp_seconds",
	} {
		for _, s := range cap.Find(name) {
			if s.Value <= nowUnix+59*day {
				t.Errorf("%s cert=%q: timestamp %.0f not at least 60d in future", name, s.Labels["name"], s.Value)
			}
			if s.Value >= nowUnix+120*day {
				t.Errorf("%s cert=%q: timestamp %.0f more than 120d in future", name, s.Labels["name"], s.Value)
			}
		}
	}

	// Stability: a second tick with the same construct produces the same expiry values.
	cap2 := &coretest.MetricCapture{}
	w2 := coretest.World(cap2, nil, nil)
	if err := c.Tick(context.Background(), testNow, w2); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	v1 := map[string]float64{}
	for _, s := range cap.Find("certmanager_certificate_expiration_timestamp_seconds") {
		v1[s.Labels["name"]] = s.Value
	}
	for _, s := range cap2.Find("certmanager_certificate_expiration_timestamp_seconds") {
		if prev, ok := v1[s.Labels["name"]]; ok {
			if prev != s.Value {
				t.Errorf("cert %q expiry changed between ticks: %.0f → %.0f", s.Labels["name"], prev, s.Value)
			}
		}
	}
}

// ─── (e) Counters are monotone across two ticks ───────────────────────────────

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

	// Build value maps keyed by (name, labels-sig)
	vals1 := seriesVals(cap1)
	vals2 := seriesVals(cap2)

	counters := []string{
		"certmanager_http_acme_client_request_count",
		"certmanager_http_acme_client_request_duration_seconds_count",
		"certmanager_controller_sync_call_count",
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

// ─── (g) Job-mode toggle + label corrections ──────────────────────────────────

// mustNew is a convenience helper for building a certmanager construct in tests.
func mustNew(t *testing.T, cfg *certmanager.Config) core.Construct {
	t.Helper()
	c, err := certmanager.New(cfg, &fixture.Set{
		Seed:    "test",
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("certmanager.New: %v", err)
	}
	return c
}

func TestCertManagerJobModeAndLabels(t *testing.T) {
	// default (autodiscovery) → bare job
	cDefault := mustNew(t, &certmanager.Config{})
	def := tickOnce(t, cDefault)

	for _, s := range def.Find("certmanager_clock_time_seconds_gauge") {
		if s.Labels["job"] != "cert-manager" {
			t.Fatalf("default job=%q want cert-manager", s.Labels["job"])
		}
	}
	if len(def.Find("certmanager_clock_time_seconds")) == 0 {
		t.Fatal("missing certmanager_clock_time_seconds")
	}
	if len(def.Find("certmanager_certificate_challenge_status")) != 0 {
		t.Fatal("challenge_status must not be emitted")
	}
	for _, s := range def.Find("certmanager_certificate_ready_status") {
		if s.Labels["namespace"] != "cert-manager" {
			t.Fatalf("namespace=%q want cert-manager", s.Labels["namespace"])
		}
		if s.Labels["exported_namespace"] == "" {
			t.Fatal("missing exported_namespace")
		}
	}

	// integration mode → prefixed job
	cInt := mustNew(t, &certmanager.Config{JobMode: "integration"})
	for _, s := range tickOnce(t, cInt).Find("certmanager_clock_time_seconds_gauge") {
		if s.Labels["job"] != "integrations/cert-manager" {
			t.Fatalf("integration job=%q want integrations/cert-manager", s.Labels["job"])
		}
	}
}

// ─── (f) Build error on nil Cluster ──────────────────────────────────────────

func TestBuildErrorOnNilCluster(t *testing.T) {
	_, err := certmanager.New(&certmanager.Config{}, &fixture.Set{Seed: "test"})
	if err == nil {
		t.Fatal("expected error when Cluster is nil, got nil")
	}
}

// ─── (h) Per-pod correlation ──────────────────────────────────────────────────

// TestLeaderPodLabels verifies that when SubstrateWorkloads is populated,
// certmanager_certificate_ready_status carries per-pod join labels from the leader
// (first) pod of the cert-manager workload.
func TestLeaderPodLabels(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	series := cap.Find("certmanager_certificate_ready_status")
	if len(series) == 0 {
		t.Fatal("no certmanager_certificate_ready_status series found")
	}

	// Collect distinct pod values — must all be the same (leader only, not 2 replicas per cert).
	podsSeen := map[string]bool{}
	for _, s := range series {
		pod := s.Labels["pod"]
		if pod == "" {
			t.Errorf("certmanager_certificate_ready_status series missing pod label: %v", s.Labels)
		}
		podsSeen[pod] = true

		// namespace must be "cert-manager" (scrape namespace)
		if s.Labels["namespace"] != "cert-manager" {
			t.Errorf("namespace=%q want cert-manager", s.Labels["namespace"])
		}

		// container must be "cert-manager-controller" (real container name from recon)
		if s.Labels["container"] != "cert-manager-controller" {
			t.Errorf("container=%q want cert-manager-controller", s.Labels["container"])
		}

		// job must be "cert-manager"
		if s.Labels["job"] != "cert-manager" {
			t.Errorf("job=%q want cert-manager", s.Labels["job"])
		}

		// instance must end with ":9402"
		if !strings.HasSuffix(s.Labels["instance"], ":9402") {
			t.Errorf("instance=%q must end with :9402", s.Labels["instance"])
		}
	}

	// Leader-only: exactly ONE pod name across all cert×condition series.
	if len(podsSeen) != 1 {
		t.Errorf("leader-only: expected exactly 1 distinct pod, got %d: %v", len(podsSeen), podsSeen)
	}

	// Per (cert, condition) — only ONE series (not duplicated across 2 replicas).
	type key struct{ name, cond string }
	counts := map[key]int{}
	for _, s := range series {
		k := key{s.Labels["name"], s.Labels["condition"]}
		counts[k]++
	}
	for k, n := range counts {
		if n != 1 {
			t.Errorf("cert=%q condition=%q: got %d series, want exactly 1 (leader-only)", k.name, k.cond, n)
		}
	}
}

// TestClockTimeAllReplicas verifies that certmanager_clock_time_seconds emits one
// series per controller replica (both pods, not just the leader).
func TestClockTimeAllReplicas(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	// cert-manager has 2 replicas → clock_time should have 2 series.
	series := cap.Find("certmanager_clock_time_seconds")
	if len(series) < 2 {
		t.Errorf("certmanager_clock_time_seconds: want ≥2 series (one per controller replica), got %d", len(series))
	}
	pods := map[string]bool{}
	for _, s := range series {
		pods[s.Labels["pod"]] = true
	}
	if len(pods) < 2 {
		t.Errorf("clock_time_seconds: want 2 distinct pods, got %d", len(pods))
	}
}

// TestWebhookPodLabels verifies that controller_runtime_reconcile_total (webhook)
// carries the webhook pod's labels with job="webhook".
func TestWebhookPodLabels(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	series := cap.Find("controller_runtime_reconcile_total")
	if len(series) == 0 {
		t.Fatal("no controller_runtime_reconcile_total series found")
	}

	// Must have webhook job series.
	webhookSeries := filterLabel(series, "job", "webhook")
	if len(webhookSeries) == 0 {
		t.Error("no controller_runtime_reconcile_total series with job=webhook")
	}
	for _, s := range webhookSeries {
		if s.Labels["container"] != "cert-manager-webhook" {
			t.Errorf("webhook series container=%q want cert-manager-webhook", s.Labels["container"])
		}
		if !strings.HasSuffix(s.Labels["instance"], ":9402") {
			t.Errorf("webhook instance=%q must end with :9402", s.Labels["instance"])
		}
		if s.Labels["namespace"] != "cert-manager" {
			t.Errorf("webhook namespace=%q want cert-manager", s.Labels["namespace"])
		}
	}
}

// TestCainjectorPodLabels verifies that controller_runtime_reconcile_total (cainjector)
// carries the cainjector pod's labels with job="cainjector", and that workqueue_*
// series carry cainjector labels.
func TestCainjectorPodLabels(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	series := cap.Find("controller_runtime_reconcile_total")
	cainjectorSeries := filterLabel(series, "job", "cainjector")
	if len(cainjectorSeries) == 0 {
		t.Error("no controller_runtime_reconcile_total series with job=cainjector")
	}
	for _, s := range cainjectorSeries {
		if s.Labels["container"] != "cert-manager-cainjector" {
			t.Errorf("cainjector series container=%q want cert-manager-cainjector", s.Labels["container"])
		}
		if !strings.HasSuffix(s.Labels["instance"], ":9402") {
			t.Errorf("cainjector instance=%q must end with :9402", s.Labels["instance"])
		}
	}

	// workqueue_adds_total must carry cainjector pod labels.
	wqSeries := cap.Find("workqueue_adds_total")
	if len(wqSeries) == 0 {
		t.Fatal("no workqueue_adds_total series found")
	}
	for _, s := range wqSeries {
		if s.Labels["job"] != "cainjector" {
			t.Errorf("workqueue_adds_total job=%q want cainjector", s.Labels["job"])
		}
		if s.Labels["container"] != "cert-manager-cainjector" {
			t.Errorf("workqueue_adds_total container=%q want cert-manager-cainjector", s.Labels["container"])
		}
	}
}

// TestFallbackNoSubstrateWorkloads verifies back-compat: with empty SubstrateWorkloads,
// the construct still emits cluster-scoped certmanager_* series (no pod label).
func TestFallbackNoSubstrateWorkloads(t *testing.T) {
	cl := coretest.Cluster()
	// SubstrateWorkloads is nil — no addon pods populated.
	c, err := certmanager.New(&certmanager.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: cl,
	})
	if err != nil {
		t.Fatalf("certmanager.New: %v", err)
	}

	cap := tickOnce(t, c)

	// Core cert families must be present.
	gotNames := map[string]bool{}
	for _, n := range cap.Names() {
		gotNames[n] = true
	}
	for _, n := range []string{
		"certmanager_certificate_ready_status",
		"certmanager_clock_time_seconds",
		"certmanager_controller_sync_call_count",
		"certmanager_http_acme_client_request_count",
		"certmanager_http_acme_client_request_duration_seconds",
	} {
		if !gotNames[n] {
			t.Errorf("fallback: MISSING %s", n)
		}
	}

	// In fallback mode, certmanager_certificate_ready_status must NOT carry a pod label.
	for _, s := range cap.Find("certmanager_certificate_ready_status") {
		if s.Labels["pod"] != "" {
			t.Errorf("fallback: certmanager_certificate_ready_status should not have pod label, got pod=%q", s.Labels["pod"])
		}
	}

	// In fallback mode, controller_runtime_* must NOT be emitted (no pods to stamp).
	if len(cap.Find("controller_runtime_reconcile_total")) != 0 {
		t.Error("fallback: controller_runtime_reconcile_total should not be emitted without SubstrateWorkloads")
	}
}

// ─── (i) ACME summary shape ───────────────────────────────────────────────────

// TestACMESummaryShape verifies:
//   - certmanager_http_acme_client_request_duration_seconds emits with quantile label (0.5, 0.9, 0.99)
//   - _count and _sum companions exist
//   - no _bucket series
func TestACMESummaryShape(t *testing.T) {
	c := buildDefault(t)
	cap := tickOnce(t, c)

	// Must have quantile series (base metric name with quantile label).
	quantileSeries := cap.Find("certmanager_http_acme_client_request_duration_seconds")
	if len(quantileSeries) == 0 {
		t.Fatal("certmanager_http_acme_client_request_duration_seconds: no quantile series found")
	}
	quantilesFound := map[string]bool{}
	for _, s := range quantileSeries {
		q := s.Labels["quantile"]
		if q == "" {
			t.Errorf("quantile series missing quantile label: %v", s.Labels)
		}
		quantilesFound[q] = true
	}
	for _, want := range []string{"0.5", "0.9", "0.99"} {
		if !quantilesFound[want] {
			t.Errorf("ACME summary: missing quantile=%s", want)
		}
	}

	// _count companion must exist.
	if len(cap.Find("certmanager_http_acme_client_request_duration_seconds_count")) == 0 {
		t.Error("ACME summary: missing _count series")
	}

	// _sum companion must exist.
	if len(cap.Find("certmanager_http_acme_client_request_duration_seconds_sum")) == 0 {
		t.Error("ACME summary: missing _sum series")
	}

	// No _bucket series (SUMMARY not histogram).
	if len(cap.Find("certmanager_http_acme_client_request_duration_seconds_bucket")) != 0 {
		t.Error("ACME must be SUMMARY (not histogram): _bucket series must NOT be emitted")
	}
}

// ─── metadata ─────────────────────────────────────────────────────────────────

func TestKindAndSignals(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "cert_manager" {
		t.Errorf("Kind() = %q, want %q", c.Kind(), "cert_manager")
	}
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
		t.Errorf("Signals() missing core.Metrics: got %v", sigs)
	}
	if !hasLogs {
		t.Errorf("Signals() missing core.Logs: got %v", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval() = %v, want 60s", c.Interval())
	}
}

// ─── Log lane tests ───────────────────────────────────────────────────────────

// tickOnceWithLogs ticks the construct with both a MetricCapture and LogCapture.
func tickOnceWithLogs(t *testing.T, c core.Construct) (*coretest.MetricCapture, *coretest.LogCapture) {
	t.Helper()
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := coretest.World(mc, lc, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc, lc
}

// TestCertManagerSignalsIncludeLogs asserts that Signals() contains core.Logs
// after the log lane is added.
func TestCertManagerSignalsIncludeLogs(t *testing.T) {
	c := buildDefault(t)
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

// TestCertManagerEmitsLogs verifies the log lane with cert-manager workloads
// in SubstrateWorkloads. Checks:
//   - at least one stream has k8s_namespace_name="cert-manager"
//   - at least one stream has k8s_pod_name matching a cert-manager pod from SubstrateWorkloads
//   - detected_level is set on every stream
//   - log body matches klog format (starts with I, W, or E followed by date digits)
//   - no high-card label keys (certificate, order, reconcileID, etc.) in stream labels
func TestCertManagerEmitsLogs(t *testing.T) {
	c := buildWithPods(t)
	_, lc := tickOnceWithLogs(t, c)

	if len(lc.Streams) == 0 {
		t.Fatal("no log streams emitted (expected at least one with SubstrateWorkloads populated)")
	}

	// Collect expected cert-manager pod names from SubstrateWorkloads.
	cl := clusterWithAddons(t)
	certManagerPodNames := map[string]bool{}
	for _, wl := range cl.SubstrateWorkloads {
		for _, pn := range wl.PodNames {
			certManagerPodNames[pn] = true
		}
	}

	// High-card label keys that must never appear in stream labels.
	forbiddenLabelKeys := []string{
		"certificate", "order", "reconcileID", "name", "issuer_name",
		"exported_namespace", "secret", "err",
	}

	var gotNamespace, gotPodName bool
	for _, stream := range lc.Streams {
		// k8s_namespace_name must be "cert-manager".
		if stream.Labels["k8s_namespace_name"] == "cert-manager" {
			gotNamespace = true
		}

		// At least one stream must have a pod name from SubstrateWorkloads.
		if podName := stream.Labels["k8s_pod_name"]; certManagerPodNames[podName] {
			gotPodName = true
		}

		// detected_level must be set.
		dl := stream.Labels["detected_level"]
		if dl == "" {
			t.Errorf("stream missing detected_level: labels=%v", stream.Labels)
		}

		// log_iostream must be stderr (cert-manager uses klog → stderr).
		if stream.Labels["log_iostream"] != "stderr" {
			t.Errorf("stream log_iostream=%q want stderr", stream.Labels["log_iostream"])
		}

		// cluster labels must be set.
		if stream.Labels["cluster"] == "" {
			t.Errorf("stream missing cluster label")
		}
		if stream.Labels["k8s_cluster_name"] == "" {
			t.Errorf("stream missing k8s_cluster_name label")
		}

		// No high-card label keys.
		for _, key := range forbiddenLabelKeys {
			if _, ok := stream.Labels[key]; ok {
				t.Errorf("stream has forbidden high-card label key %q: labels=%v", key, stream.Labels)
			}
		}

		// Each stream must have at least one line.
		if len(stream.Lines) == 0 {
			t.Errorf("stream has no lines: labels=%v", stream.Labels)
		}

		// Log body must match klog format: starts with I/W/E followed by 4 date digits.
		for _, line := range stream.Lines {
			if len(line.Body) < 5 {
				t.Errorf("log line body too short: %q", line.Body)
				continue
			}
			firstChar := line.Body[0]
			if firstChar != 'I' && firstChar != 'W' && firstChar != 'E' {
				t.Errorf("log line body does not start with I/W/E: %q", line.Body)
			}
			// Next 4 chars should be digits (MMDD).
			for i := 1; i <= 4; i++ {
				if line.Body[i] < '0' || line.Body[i] > '9' {
					t.Errorf("log line body[%d]=%q is not a digit (expected klog MMDD): %q", i, line.Body[i], line.Body)
				}
			}
		}
	}

	if !gotNamespace {
		t.Error("no stream with k8s_namespace_name=\"cert-manager\"")
	}
	if !gotPodName {
		t.Error("no stream with k8s_pod_name matching a cert-manager pod from SubstrateWorkloads")
	}
}

// TestCertManagerEmitsLogsNoWorkloads verifies that Tick completes without panic
// when SubstrateWorkloads does not contain cert-manager pods.
func TestCertManagerEmitsLogsNoWorkloads(t *testing.T) {
	// Build with no SubstrateWorkloads.
	c, err := certmanager.New(&certmanager.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: coretest.Cluster(), // SubstrateWorkloads = nil
	})
	if err != nil {
		t.Fatalf("certmanager.New: %v", err)
	}

	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := coretest.World(mc, lc, nil)

	// Must not panic.
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick with no workloads: %v", err)
	}

	// Metrics must still be emitted (back-compat).
	if len(mc.All()) == 0 {
		t.Error("no metrics emitted in no-workloads fallback")
	}
	// Logs may be nil or empty — must not panic.
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
