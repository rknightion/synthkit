// SPDX-License-Identifier: AGPL-3.0-only

package coredns_test

// coredns_test.go — construct invariant tests for the core_dns construct.
//
// Test inventory:
//   (a) Exact series name inventory (all expected names, no extras).
//   (b) rcode/proto/type/plugin label value enums (recon-corrected).
//   (c) cluster+k8s_cluster_name dual label on every series.
//   (d) NO blueprint label on any series (ScopeSubstrate invariant — ARCHITECTURE I21).
//   (e) Counter monotone: two ticks accumulate (state.Add), gauges do not.
//   (f) Build returns an error when Cluster fixture is nil.
//   (g) Kind/Signals/Interval metadata.
//   (h) Per-pod stamping: coredns_dns_* series carry pod/namespace/container/instance
//       for BOTH pods when SubstrateWorkloads contains "coredns".
//   (i) Fallback path: empty SubstrateWorkloads → cluster-scoped (no pod label).
//   (j) server label always "dns://:53" on series that carry it.
//   (k) proxy upstream label = VPC DNS "10.1.0.2:53".

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/coredns"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// clusterWithAddons returns a coretest.Cluster with core_dns SubstrateWorkloads
// populated (the "with-pods" path used by per-pod correlation tests).
func clusterWithAddons(t *testing.T) *fixture.Cluster {
	t.Helper()
	cl := coretest.Cluster()
	seed := "test"
	wls := fixture.AddonWorkloads("core_dns")
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
	c, err := coredns.New(&coredns.Config{}, &fixture.Set{
		Env:     coretest.Env(),
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("coredns.New: %v", err)
	}
	return c
}

func buildWithPods(t *testing.T) core.Construct {
	t.Helper()
	c, err := coredns.New(&coredns.Config{}, &fixture.Set{
		Env:     coretest.Env(),
		Cluster: clusterWithAddons(t),
	})
	if err != nil {
		t.Fatalf("coredns.New (with pods): %v", err)
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

// labelsFor returns the union of label maps seen on a named series across all samples.
func labelsFor(series []promrw.Series, name string) []map[string]string {
	var out []map[string]string
	for _, s := range series {
		if s.Name == name {
			out = append(out, s.Labels)
		}
	}
	return out
}

// ─── (a) Exact series inventory ──────────────────────────────────────────────

// expectedNames is the canonical inventory derived from live recon svc-coredns.md §A.
// Histograms expand to _bucket + _sum + _count.
// Counters and gauges use their base name.
// coredns_cache_evictions_total is NOT in recon metric inventory — removed.
// Families added from live capture: build_info, cache_requests_total,
// forward_max_concurrent_rejects_total, health_request_failures_total,
// hosts_reload_timestamp_seconds, kubernetes_dns_programming_duration_seconds,
// kubernetes_rest_client_requests_total, local_localhost_requests_total,
// proxy_conn_cache_hits_total, proxy_conn_cache_misses_total, reload_failed_total.
var expectedNames = func() []string {
	names := []string{
		// gauges
		"coredns_build_info",
		"coredns_cache_entries",
		"coredns_hosts_reload_timestamp_seconds",
		"coredns_plugin_enabled",
		// counters
		"coredns_dns_requests_total",
		"coredns_dns_responses_total",
		"coredns_cache_hits_total",
		"coredns_cache_misses_total",
		"coredns_cache_requests_total",
		"coredns_forward_healthcheck_broken_total",
		"coredns_forward_max_concurrent_rejects_total",
		"coredns_health_request_failures_total",
		"coredns_kubernetes_rest_client_requests_total",
		"coredns_local_localhost_requests_total",
		"coredns_panics_total",
		"coredns_proxy_conn_cache_hits_total",
		"coredns_proxy_conn_cache_misses_total",
		"coredns_proxy_healthcheck_failures_total",
		"coredns_reload_failed_total",
		// histograms expand into _bucket, _sum, _count
		"coredns_dns_request_duration_seconds_bucket",
		"coredns_dns_request_duration_seconds_sum",
		"coredns_dns_request_duration_seconds_count",
		"coredns_dns_request_size_bytes_bucket",
		"coredns_dns_request_size_bytes_sum",
		"coredns_dns_request_size_bytes_count",
		"coredns_dns_response_size_bytes_bucket",
		"coredns_dns_response_size_bytes_sum",
		"coredns_dns_response_size_bytes_count",
		"coredns_health_request_duration_seconds_bucket",
		"coredns_health_request_duration_seconds_sum",
		"coredns_health_request_duration_seconds_count",
		"coredns_kubernetes_dns_programming_duration_seconds_bucket",
		"coredns_kubernetes_dns_programming_duration_seconds_sum",
		"coredns_kubernetes_dns_programming_duration_seconds_count",
		"coredns_proxy_request_duration_seconds_bucket",
		"coredns_proxy_request_duration_seconds_sum",
		"coredns_proxy_request_duration_seconds_count",
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

// ─── (b) rcode / proto / type / plugin label value enums ─────────────────────

// TestRcodeEnum verifies rcode ∈ {NOERROR, NXDOMAIN} on dns_responses_total.
// SERVFAIL is NOT in the live recon capture (removed — recon §A2 only NOERROR/NXDOMAIN).
func TestRcodeEnum(t *testing.T) {
	cap := tickOnce(t, buildDefault(t))
	rcodes := map[string]bool{}
	for _, s := range cap.All() {
		if s.Name == "coredns_dns_responses_total" {
			rcodes[s.Labels["rcode"]] = true
		}
	}
	for _, want := range []string{"NOERROR", "NXDOMAIN"} {
		if !rcodes[want] {
			t.Errorf("rcode %q absent from coredns_dns_responses_total", want)
		}
	}
	// SERVFAIL must NOT appear (not in live recon).
	if rcodes["SERVFAIL"] {
		t.Error("unexpected rcode SERVFAIL on coredns_dns_responses_total (not in live recon)")
	}
	// No other unexpected rcodes.
	allowed := map[string]bool{"NOERROR": true, "NXDOMAIN": true}
	for rc := range rcodes {
		if !allowed[rc] {
			t.Errorf("unexpected rcode %q on coredns_dns_responses_total", rc)
		}
	}
}

func TestProtoEnum(t *testing.T) {
	cap := tickOnce(t, buildDefault(t))
	protos := map[string]bool{}
	for _, s := range cap.All() {
		if s.Name == "coredns_dns_requests_total" {
			protos[s.Labels["proto"]] = true
		}
	}
	for _, want := range []string{"udp", "tcp"} {
		if !protos[want] {
			t.Errorf("proto %q absent from coredns_dns_requests_total", want)
		}
	}
}

// TestQueryTypeEnum verifies type ∈ {A, AAAA, PTR, SRV, TXT, other} (recon §A2).
// HTTPS is NOT in the live recon capture — replaced by TXT and other.
func TestQueryTypeEnum(t *testing.T) {
	cap := tickOnce(t, buildDefault(t))
	types := map[string]bool{}
	for _, s := range cap.All() {
		if s.Name == "coredns_dns_requests_total" {
			types[s.Labels["type"]] = true
		}
	}
	for _, want := range []string{"A", "AAAA", "PTR", "SRV", "TXT", "other"} {
		if !types[want] {
			t.Errorf("query type %q absent from coredns_dns_requests_total", want)
		}
	}
	// HTTPS must NOT appear (not in live recon).
	if types["HTTPS"] {
		t.Error("unexpected query type HTTPS (not in live recon capture)")
	}
}

// TestPluginEnum verifies name ∈ {cache,errors,forward,kubernetes,loadbalance,loop,prometheus}
// on coredns_plugin_enabled (recon §A2: 7 plugins; health/ready/reload do NOT register).
func TestPluginEnum(t *testing.T) {
	cap := tickOnce(t, buildDefault(t))
	plugins := map[string]bool{}
	for _, s := range cap.All() {
		if s.Name == "coredns_plugin_enabled" {
			plugins[s.Labels["name"]] = true
		}
	}
	for _, want := range []string{"cache", "errors", "forward", "kubernetes", "loadbalance", "loop", "prometheus"} {
		if !plugins[want] {
			t.Errorf("plugin %q absent from coredns_plugin_enabled", want)
		}
	}
	// health/ready/metrics must NOT appear (they do not register with this metric per recon §A2 note).
	for _, bad := range []string{"health", "ready", "metrics"} {
		if plugins[bad] {
			t.Errorf("unexpected plugin %q in coredns_plugin_enabled (not registered per recon)", bad)
		}
	}
}

// ─── (c) cluster + k8s_cluster_name dual label on every series ───────────────

func TestClusterLabels(t *testing.T) {
	clust := coretest.Cluster()
	c, err := coredns.New(&coredns.Config{}, &fixture.Set{
		Env:     coretest.Env(),
		Cluster: clust,
	})
	if err != nil {
		t.Fatalf("coredns.New: %v", err)
	}
	cap := tickOnce(t, c)

	if len(cap.All()) == 0 {
		t.Fatal("no series emitted")
	}
	for _, s := range cap.All() {
		if s.Labels["cluster"] != clust.Name {
			t.Errorf("series %q: cluster=%q want %q", s.Name, s.Labels["cluster"], clust.Name)
		}
		if s.Labels["k8s_cluster_name"] != clust.Name {
			t.Errorf("series %q: k8s_cluster_name=%q want %q", s.Name, s.Labels["k8s_cluster_name"], clust.Name)
		}
	}
}

// ─── (d) NO blueprint label (ScopeSubstrate invariant) ───────────────────────

func TestNoBlueprintLabel(t *testing.T) {
	cap := tickOnce(t, buildDefault(t))
	for _, s := range cap.All() {
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("series %q must NOT carry blueprint label (ScopeSubstrate)", s.Name)
		}
	}
}

// ─── (e) Counter monotone across ticks ───────────────────────────────────────

// TestCounterMonotone verifies that counters (state.Add) accumulate across two ticks
// rather than resetting — ARCHITECTURE I3.
func TestCounterMonotone(t *testing.T) {
	c := buildDefault(t)
	cap1 := &coretest.MetricCapture{}
	cap2 := &coretest.MetricCapture{}
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

	if err := c.Tick(context.Background(), now, coretest.World(cap1, nil, nil)); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if err := c.Tick(context.Background(), now, coretest.World(cap2, nil, nil)); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	// Pick a counter that has a non-zero delta — dns_requests_total with proto=udp, type=A.
	val := func(cap *coretest.MetricCapture, proto, typ string) float64 {
		for _, s := range cap.All() {
			if s.Name == "coredns_dns_requests_total" &&
				s.Labels["proto"] == proto && s.Labels["type"] == typ {
				return s.Value
			}
		}
		return -1
	}

	v1 := val(cap1, "udp", "A")
	v2 := val(cap2, "udp", "A")
	if v1 < 0 {
		t.Fatal("coredns_dns_requests_total{proto=udp,type=A} absent in tick1")
	}
	if v2 < 0 {
		t.Fatal("coredns_dns_requests_total{proto=udp,type=A} absent in tick2")
	}
	if v2 <= v1 {
		t.Errorf("counter should accumulate: tick1=%.2f tick2=%.2f — not monotone", v1, v2)
	}
	if v2 < v1*1.5 {
		t.Errorf("counter should roughly double after equal ticks: tick1=%.2f tick2=%.2f", v1, v2)
	}
}

// TestGaugeNotAccumulated verifies that gauges (state.Set) do NOT accumulate — the
// second tick value equals the first (at the same time / factor), not double.
func TestGaugeNotAccumulated(t *testing.T) {
	c := buildDefault(t)
	cap1 := &coretest.MetricCapture{}
	cap2 := &coretest.MetricCapture{}
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

	if err := c.Tick(context.Background(), now, coretest.World(cap1, nil, nil)); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if err := c.Tick(context.Background(), now, coretest.World(cap2, nil, nil)); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	// coredns_cache_entries is a gauge — must not double.
	cacheVal := func(cap *coretest.MetricCapture, cacheType string) float64 {
		for _, s := range cap.All() {
			if s.Name == "coredns_cache_entries" && s.Labels["type"] == cacheType {
				return s.Value
			}
		}
		return -1
	}

	for _, cacheType := range []string{"success", "denial"} {
		v1 := cacheVal(cap1, cacheType)
		v2 := cacheVal(cap2, cacheType)
		if v1 < 0 {
			t.Errorf("coredns_cache_entries{type=%s} absent in tick1", cacheType)
			continue
		}
		if v2 < 0 {
			t.Errorf("coredns_cache_entries{type=%s} absent in tick2", cacheType)
			continue
		}
		if v2 >= v1*1.5 {
			t.Errorf("gauge coredns_cache_entries{type=%s} appears to accumulate: tick1=%.0f tick2=%.0f",
				cacheType, v1, v2)
		}
	}
}

// ─── (f) Build error on nil Cluster ──────────────────────────────────────────

func TestBuildErrorOnNilCluster(t *testing.T) {
	_, err := coredns.New(&coredns.Config{}, &fixture.Set{
		Env:     coretest.Env(),
		Cluster: nil,
	})
	if err == nil {
		t.Fatal("expected error when Cluster is nil, got nil")
	}
}

// ─── (g) Metadata ─────────────────────────────────────────────────────────────

func TestKindAndSignals(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "core_dns" {
		t.Errorf("Kind() = %q, want %q", c.Kind(), "core_dns")
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals() = %v, want [Metrics]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval() = %v, want 60s", c.Interval())
	}
}

// ─── (h) Per-pod correlation ──────────────────────────────────────────────────

// TestPerPodStamping verifies that when SubstrateWorkloads contains the "coredns"
// workload, coredns_dns_* series carry per-pod join labels for BOTH pods:
//   - pod label matches a "coredns-*" PodName
//   - namespace = "kube-system"
//   - container = "coredns"
//   - instance ends with ":9153"
//
// Both pods must appear (all replicas serve DNS — not leader-only).
func TestPerPodStamping(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	// Gather distinct pod values from dns_requests_total (a high-cardinality counter
	// that is emitted per-pod on every tick).
	podsOnDNSRequests := map[string]bool{}
	for _, s := range cap.All() {
		if s.Name != "coredns_dns_requests_total" {
			continue
		}

		pod, hasPod := s.Labels["pod"]
		ns := s.Labels["namespace"]
		container := s.Labels["container"]
		instance := s.Labels["instance"]

		if !hasPod || pod == "" {
			t.Errorf("coredns_dns_requests_total: missing pod label")
			continue
		}
		if !strings.HasPrefix(pod, "coredns-") {
			t.Errorf("coredns_dns_requests_total: pod=%q does not start with coredns-", pod)
		}
		if ns != "kube-system" {
			t.Errorf("coredns_dns_requests_total: namespace=%q want kube-system", ns)
		}
		if container != "coredns" {
			t.Errorf("coredns_dns_requests_total: container=%q want coredns", container)
		}
		if !strings.HasSuffix(instance, ":9153") {
			t.Errorf("coredns_dns_requests_total: instance=%q does not end with :9153", instance)
		}
		podsOnDNSRequests[pod] = true
	}

	// Both replicas must emit (all pods serve DNS).
	if len(podsOnDNSRequests) < 2 {
		t.Errorf("expected series from 2 coredns pods, got %d: %v", len(podsOnDNSRequests), podsOnDNSRequests)
	}
}

// TestPerPodJobLabel verifies the job label is the per-pod scrape job value.
func TestPerPodJobLabel(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)
	for _, s := range cap.All() {
		if job := s.Labels["job"]; job != "integrations/kubernetes/kube-dns" {
			t.Errorf("series %q: job=%q want integrations/kubernetes/kube-dns", s.Name, job)
			break
		}
	}
}

// TestPerPodBothPodsOnMultipleFamilies verifies that BOTH pods appear on multiple
// coredns families (build_info, cache_entries, health_request_duration) —
// confirming the "all pods emit" invariant is not limited to dns_requests_total.
func TestPerPodBothPodsOnMultipleFamilies(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	checkFamily := func(name string) {
		t.Helper()
		pods := map[string]bool{}
		for _, s := range cap.All() {
			if s.Name == name {
				if pod, ok := s.Labels["pod"]; ok && pod != "" {
					pods[pod] = true
				}
			}
		}
		if len(pods) < 2 {
			t.Errorf("family %s: expected 2 distinct pod labels, got %d: %v", name, len(pods), pods)
		}
	}

	checkFamily("coredns_build_info")
	checkFamily("coredns_cache_entries")
	checkFamily("coredns_health_request_duration_seconds_count")
}

// ─── (i) Fallback: empty SubstrateWorkloads → cluster-scoped ─────────────────

// TestFallbackNoSubstrateWorkloads verifies back-compat: with SubstrateWorkloads absent
// (nil), coredns_dns_requests_total is still emitted without pod/namespace/container/instance
// labels (cluster-scoped series).
func TestFallbackNoSubstrateWorkloads(t *testing.T) {
	c, err := coredns.New(&coredns.Config{}, &fixture.Set{
		Env:     coretest.Env(),
		Cluster: coretest.Cluster(), // no SubstrateWorkloads
	})
	if err != nil {
		t.Fatalf("coredns.New: %v", err)
	}
	cap := tickOnce(t, c)

	// Verify dns_requests_total is still emitted.
	found := false
	for _, s := range cap.All() {
		if s.Name == "coredns_dns_requests_total" {
			found = true
			// No pod/namespace/container/instance labels on fallback path.
			if _, hasPod := s.Labels["pod"]; hasPod {
				t.Errorf("fallback path: pod label present but SubstrateWorkloads is nil")
			}
			if _, hasNS := s.Labels["namespace"]; hasNS {
				t.Errorf("fallback path: namespace label present but SubstrateWorkloads is nil")
			}
			if _, hasCont := s.Labels["container"]; hasCont {
				t.Errorf("fallback path: container label present but SubstrateWorkloads is nil")
			}
			break
		}
	}
	if !found {
		t.Error("fallback: coredns_dns_requests_total not emitted with nil SubstrateWorkloads")
	}
}

// ─── (j) server label correctness ────────────────────────────────────────────

func TestServerLabel(t *testing.T) {
	cap := tickOnce(t, buildDefault(t))
	// Every series that carries a "server" label must have exactly "dns://:53".
	for _, s := range cap.All() {
		if sv, ok := s.Labels["server"]; ok && sv != "dns://:53" {
			t.Errorf("series %q: server=%q want %q", s.Name, sv, "dns://:53")
		}
	}
}

// ─── (k) proxy upstream labels ───────────────────────────────────────────────

// TestProxyUpstreamLabel verifies the proxy upstream is the VPC DNS resolver
// "10.1.0.2:53" (recon §A3/§B3). Google DNS addresses (8.8.8.8:53, 8.8.4.4:53)
// must NOT appear — those were pre-recon assumptions.
func TestProxyUpstreamLabel(t *testing.T) {
	cap := tickOnce(t, buildDefault(t))
	upstreams := map[string]bool{}
	for _, s := range labelsFor(cap.All(), "coredns_proxy_request_duration_seconds_bucket") {
		if to, ok := s["to"]; ok {
			upstreams[to] = true
		}
	}
	if !upstreams["10.1.0.2:53"] {
		t.Errorf("expected upstream 10.1.0.2:53 absent from coredns_proxy_request_duration_seconds")
	}
	for _, bad := range []string{"8.8.8.8:53", "8.8.4.4:53"} {
		if upstreams[bad] {
			t.Errorf("stale upstream %q present (pre-recon assumption; live recon shows VPC DNS)", bad)
		}
	}
}

// ─── view label must NOT appear (ARCHITECTURE I13) ────────────────────────────

// TestNoViewLabel verifies the "view" label is absent from all series.
// The view plugin is NOT enabled in the EKS default Corefile (recon §A2 note).
// An absent dimension must be OMITTED per ARCHITECTURE I13 — never emitted as "".
func TestNoViewLabel(t *testing.T) {
	cap := tickOnce(t, buildDefault(t))
	for _, s := range cap.All() {
		if v, ok := s.Labels["view"]; ok {
			t.Errorf("series %q: view label must be absent (I13), got %q", s.Name, v)
		}
	}
}

// ─── zones (plural) on cache metrics ─────────────────────────────────────────

// TestCacheZonesLabel verifies cache_* metrics carry "zones" (plural) not "zone"
// (recon §A2/§D: "zones" label, not "zone").
func TestCacheZonesLabel(t *testing.T) {
	cap := tickOnce(t, buildDefault(t))
	for _, name := range []string{
		"coredns_cache_entries",
		"coredns_cache_hits_total",
		"coredns_cache_misses_total",
		"coredns_cache_requests_total",
	} {
		found := false
		for _, s := range cap.All() {
			if s.Name != name {
				continue
			}
			found = true
			if _, hasZones := s.Labels["zones"]; !hasZones {
				t.Errorf("%s: missing 'zones' (plural) label", name)
			}
			if _, hasZone := s.Labels["zone"]; hasZone {
				t.Errorf("%s: has 'zone' (singular) label — should be 'zones' (plural) per recon", name)
			}
			break
		}
		if !found {
			t.Errorf("series %s not emitted", name)
		}
	}
}
