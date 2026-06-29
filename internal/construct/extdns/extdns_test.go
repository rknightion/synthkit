// SPDX-License-Identifier: AGPL-3.0-only

package extdns_test

// extdns_test.go — construct invariant tests for the external_dns construct.
//
// Test inventory:
//   (a) Exact series name inventory — all expected names present, no extras.
//   (b) build_info present with version + goversion labels; values match recon.
//   (c) cluster + k8s_cluster_name labels on every series.
//   (d) No blueprint label on any series (ScopeSubstrate invariant, ARCHITECTURE I21).
//   (e) Counters are monotone — two successive ticks show non-decreasing values (I3).
//   (f) record_type label lowercase per recon; correct types per family.
//   (g) source_type label ∈ {service, ingress} on deduplicated_endpoints series.
//   (h) Logs emitted with OTel/Alloy stream labels (k8s_namespace_name, k8s_container_name, etc.).
//   (i) No controller_runtime_* or rest_client_* (ExternalDNS does not use them).
//   (j) Build returns an error when fx.Cluster is nil.
//   (k) Kind/Signals/Interval metadata.
//   (l) Per-pod correlation: series carry pod/namespace=external-dns/container=external-dns/
//       instance ending ":7979" when SubstrateWorkloads contains "external-dns".
//   (m) Fallback: no pod labels when SubstrateWorkloads absent.
//   (n) HTTP summary: quantile series (0.5/0.9/0.99) + _count + _sum emitted; no _bucket.
//   (o) job label = "external-dns" (autodiscovery form, NOT integrations/).

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/extdns"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	reg := extdns.Registration()
	c, err := reg.Build(&extdns.Config{}, &fixture.Set{
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("extdns.Build: %v", err)
	}
	return c
}

// clusterWithExtDNSPods returns a cluster with SubstrateWorkloads populated for
// external_dns (1 pod, namespace=external-dns, container=external-dns).
func clusterWithExtDNSPods(t *testing.T) *fixture.Cluster {
	t.Helper()
	cl := coretest.Cluster()
	seed := "test"
	wls := fixture.AddonWorkloads("external_dns")
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
	reg := extdns.Registration()
	c, err := reg.Build(&extdns.Config{}, &fixture.Set{
		Cluster: clusterWithExtDNSPods(t),
	})
	if err != nil {
		t.Fatalf("extdns.Build(withPods): %v", err)
	}
	return c
}

var businessHours = time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

// tickOnce ticks the construct and returns (names, all series, log capture).
func tickOnce(t *testing.T, c core.Construct, now time.Time) ([]string, []promrw.Series, *coretest.LogCapture) {
	t.Helper()
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := coretest.World(mc, lc, nil)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc.Names(), mc.All(), lc
}

// ─── (a) Exact series name inventory ─────────────────────────────────────────

// expectedExtDNSNames is derived from svc-external-dns.md §A.1 (22 metric families).
// HTTP summary is emitted as quantile series (base name) + _count + _sum — no _bucket.
var expectedExtDNSNames = func() []string {
	names := []string{
		// Controller status gauges
		"external_dns_controller_last_sync_timestamp_seconds",
		"external_dns_controller_last_reconcile_timestamp_seconds",
		"external_dns_controller_consecutive_soft_errors",
		// Counter
		"external_dns_controller_no_op_runs_total",
		// Per-record-type gauges
		"external_dns_controller_verified_records",
		"external_dns_registry_records",
		"external_dns_source_records",
		"external_dns_registry_endpoints_total",
		"external_dns_source_endpoints_total",
		"external_dns_source_deduplicated_endpoints",
		// Error counters
		"external_dns_registry_errors_total",
		"external_dns_source_errors_total",
		// Provider cache counter
		"external_dns_provider_cache_apply_changes_calls",
		// Webhook provider counters (all zero — cloudflare native)
		"external_dns_webhook_provider_records_requests_total",
		"external_dns_webhook_provider_records_errors_total",
		"external_dns_webhook_provider_adjustendpoints_requests_total",
		"external_dns_webhook_provider_adjustendpoints_errors_total",
		"external_dns_webhook_provider_applychanges_requests_total",
		"external_dns_webhook_provider_applychanges_errors_total",
		// Build info
		"external_dns_build_info",
		// HTTP summary
		"external_dns_http_request_duration_seconds",       // quantile series
		"external_dns_http_request_duration_seconds_count", // companion count
		"external_dns_http_request_duration_seconds_sum",   // companion sum
	}
	set := map[string]struct{}{}
	for _, n := range names {
		set[n] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}()

func TestSeriesInventory(t *testing.T) {
	c := buildDefault(t)
	got, _, _ := tickOnce(t, c, businessHours)

	wantSet := map[string]bool{}
	for _, n := range expectedExtDNSNames {
		wantSet[n] = true
	}
	gotSet := map[string]bool{}
	for _, n := range got {
		gotSet[n] = true
	}

	for _, n := range expectedExtDNSNames {
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

// ─── (b) build_info present with correct version + goversion labels ──────────

func TestBuildInfoPresent(t *testing.T) {
	c := buildDefault(t)
	_, all, _ := tickOnce(t, c, businessHours)

	var found []promrw.Series
	for _, s := range all {
		if s.Name == "external_dns_build_info" {
			found = append(found, s)
		}
	}
	if len(found) == 0 {
		t.Fatal("external_dns_build_info not emitted")
	}
	for _, s := range found {
		if s.Labels["version"] == "" {
			t.Errorf("external_dns_build_info missing version label")
		}
		if s.Labels["goversion"] == "" {
			t.Errorf("external_dns_build_info missing goversion label")
		}
		// Recon: version = "v20260406-v0.21.0" (svc-external-dns.md §A.2)
		if s.Labels["version"] != "v20260406-v0.21.0" {
			t.Errorf("build_info version=%q want %q", s.Labels["version"], "v20260406-v0.21.0")
		}
		// Recon: go_version = "go1.26.1"
		if s.Labels["goversion"] != "go1.26.1" {
			t.Errorf("build_info goversion=%q want %q", s.Labels["goversion"], "go1.26.1")
		}
		if s.Labels["arch"] != "arm64" {
			t.Errorf("build_info arch=%q want %q", s.Labels["arch"], "arm64")
		}
		if s.Value != 1 {
			t.Errorf("external_dns_build_info value = %.1f, want 1", s.Value)
		}
	}
}

// ─── (c) cluster + k8s_cluster_name labels on every series ───────────────────

func TestClusterLabels(t *testing.T) {
	cl := coretest.Cluster()
	reg := extdns.Registration()
	c, err := reg.Build(&extdns.Config{}, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_, all, _ := tickOnce(t, c, businessHours)
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

func TestNoBlueprintLabel(t *testing.T) {
	c := buildDefault(t)
	_, all, _ := tickOnce(t, c, businessHours)
	for _, s := range all {
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("ScopeSubstrate violation: series %q carries blueprint label", s.Name)
		}
	}
}

// ─── (e) Counters are monotone ────────────────────────────────────────────────

func TestCountersMonotone(t *testing.T) {
	c := buildDefault(t)
	now := businessHours

	mc1 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, &coretest.LogCapture{}, nil)
	if err := c.Tick(context.Background(), now, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}

	mc2 := &coretest.MetricCapture{}
	w2 := coretest.World(mc2, &coretest.LogCapture{}, nil)
	if err := c.Tick(context.Background(), now.Add(60*time.Second), w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	sig := func(s promrw.Series) string {
		return s.Name + "|" + s.Labels["record_type"] + "|" + s.Labels["path"]
	}
	vals1 := map[string]float64{}
	for _, s := range mc1.All() {
		if strings.HasSuffix(s.Name, "_total") || strings.HasSuffix(s.Name, "_count") || strings.HasSuffix(s.Name, "_sum") || strings.HasSuffix(s.Name, "_calls") {
			vals1[sig(s)] = s.Value
		}
	}
	vals2 := map[string]float64{}
	for _, s := range mc2.All() {
		if strings.HasSuffix(s.Name, "_total") || strings.HasSuffix(s.Name, "_count") || strings.HasSuffix(s.Name, "_sum") || strings.HasSuffix(s.Name, "_calls") {
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

// ─── (f) record_type labels are lowercase per recon ──────────────────────────

func TestRecordTypeLabels(t *testing.T) {
	c := buildDefault(t)
	_, all, _ := tickOnce(t, c, businessHours)

	// Recon: record_type is lowercase (a, aaaa, cname, mx, txt) — svc-external-dns.md §A.2.
	// Each family has distinct observed types; check per-family.
	type typeCheck struct {
		name     string
		expected []string
	}
	checks := []typeCheck{
		// verified_records: only "a" observed in recon
		{"external_dns_controller_verified_records", []string{"a"}},
		// registry_records: 5 types
		{"external_dns_registry_records", []string{"a", "aaaa", "cname", "mx", "txt"}},
		// source_records: 2 types
		{"external_dns_source_records", []string{"a", "cname"}},
	}

	for _, tc := range checks {
		seen := map[string]bool{}
		for _, s := range all {
			if s.Name == tc.name {
				rt := s.Labels["record_type"]
				if rt == "" {
					t.Errorf("series %q missing record_type label", s.Name)
					continue
				}
				// Must be lowercase per recon.
				if rt != strings.ToLower(rt) {
					t.Errorf("series %q: record_type=%q must be lowercase", s.Name, rt)
				}
				seen[rt] = true
			}
		}
		for _, rt := range tc.expected {
			if !seen[rt] {
				t.Errorf("series %q missing record_type=%q", tc.name, rt)
			}
		}
	}
}

// ─── (g) source_type label on deduplicated_endpoints ─────────────────────────

func TestSourceTypeLabels(t *testing.T) {
	c := buildDefault(t)
	_, all, _ := tickOnce(t, c, businessHours)

	validSourceTypes := map[string]bool{"service": true, "ingress": true}
	seenTypes := map[string]bool{}

	for _, s := range all {
		if s.Name != "external_dns_source_deduplicated_endpoints" {
			continue
		}
		st := s.Labels["source_type"]
		if st == "" {
			t.Errorf("external_dns_source_deduplicated_endpoints missing source_type label")
			continue
		}
		if !validSourceTypes[st] {
			t.Errorf("source_type=%q not in {service, ingress}", st)
		}
		seenTypes[st] = true
	}
	for _, st := range []string{"service", "ingress"} {
		if !seenTypes[st] {
			t.Errorf("source_type=%q not emitted for external_dns_source_deduplicated_endpoints", st)
		}
	}
}

// ─── (h) Logs emitted with OTel/Alloy stream labels ──────────────────────────

// TestLogsStreamLabels runs 20 ticks to ensure at least one log batch is emitted.
// Verifies OTel/Alloy stream label convention (k8s_namespace_name, NOT namespace).
func TestLogsStreamLabels(t *testing.T) {
	c := buildDefault(t)
	cl := coretest.Cluster()

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
			// OTel/Alloy namespace label is k8s_namespace_name (NOT "namespace").
			if stream.Labels["k8s_namespace_name"] != "external-dns" {
				t.Errorf("stream k8s_namespace_name=%q want %q", stream.Labels["k8s_namespace_name"], "external-dns")
			}
			if stream.Labels["k8s_container_name"] != "external-dns" {
				t.Errorf("stream k8s_container_name=%q want %q", stream.Labels["k8s_container_name"], "external-dns")
			}
			if stream.Labels["cluster"] != cl.Name {
				t.Errorf("stream cluster=%q want %q", stream.Labels["cluster"], cl.Name)
			}
			if stream.Labels["k8s_cluster_name"] != cl.Name {
				t.Errorf("stream k8s_cluster_name=%q want %q", stream.Labels["k8s_cluster_name"], cl.Name)
			}
			if stream.Labels["service_name"] != "external-dns" {
				t.Errorf("stream service_name=%q want %q", stream.Labels["service_name"], "external-dns")
			}
			// detected_level must be present (info or warn).
			dl := stream.Labels["detected_level"]
			if dl != "info" && dl != "warn" {
				t.Errorf("stream detected_level=%q want info or warn", dl)
			}
			// log_iostream must be stderr (external-dns writes exclusively to stderr).
			if stream.Labels["log_iostream"] != "stderr" {
				t.Errorf("stream log_iostream=%q want stderr", stream.Labels["log_iostream"])
			}
			// Must not carry a blueprint label.
			if _, ok := stream.Labels["blueprint"]; ok {
				t.Errorf("log stream carries blueprint label (ScopeSubstrate violation)")
			}
			if len(stream.Lines) == 0 {
				t.Error("stream has no lines")
			}
			// Log body must be JSON (start with '{').
			for _, line := range stream.Lines {
				if len(line.Body) == 0 || line.Body[0] != '{' {
					t.Errorf("log line body is not JSON: %q", line.Body)
				}
			}
		}
	}
	if gotStreams == 0 {
		t.Error("no log streams emitted across 20 ticks")
	}
}

// ─── (i) No controller_runtime_* or rest_client_* ────────────────────────────

func TestNoControllerRuntimeOrRestClient(t *testing.T) {
	c := buildDefault(t)
	names, _, _ := tickOnce(t, c, businessHours)
	for _, n := range names {
		if strings.HasPrefix(n, "controller_runtime") {
			t.Errorf("ExternalDNS must not emit controller_runtime_*; found %q", n)
		}
		if strings.HasPrefix(n, "rest_client") {
			t.Errorf("ExternalDNS must not emit rest_client_*; found %q", n)
		}
	}
}

// ─── (j) Build error on nil Cluster ──────────────────────────────────────────

func TestBuildErrorOnNilCluster(t *testing.T) {
	reg := extdns.Registration()
	_, err := reg.Build(&extdns.Config{}, &fixture.Set{Cluster: nil})
	if err == nil {
		t.Fatal("expected error when fx.Cluster is nil, got nil")
	}
}

// ─── (k) Kind / Signals / Interval metadata ──────────────────────────────────

func TestMetadata(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "external_dns" {
		t.Errorf("Kind() = %q, want %q", c.Kind(), "external_dns")
	}
	sigs := c.Signals()
	if len(sigs) != 2 {
		t.Fatalf("Signals() len=%d, want 2", len(sigs))
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
	reg := extdns.Registration()
	if reg.Scope != core.ScopeSubstrate {
		t.Errorf("Registration().Scope = %v, want ScopeSubstrate", reg.Scope)
	}
}

// ─── (l) Per-pod correlation ──────────────────────────────────────────────────

// TestPerPodLabels verifies that when SubstrateWorkloads contains "external-dns",
// all metric series carry pod/namespace/container/instance with the correct values:
//   - namespace = "external-dns"
//   - container = "external-dns"
//   - instance ends with ":7979"
func TestPerPodLabels(t *testing.T) {
	c := buildWithPods(t)
	_, all, _ := tickOnce(t, c, businessHours)
	if len(all) == 0 {
		t.Fatal("no series emitted")
	}

	for _, s := range all {
		if s.Labels["pod"] == "" {
			t.Errorf("series %q missing pod label (SubstrateWorkloads was populated)", s.Name)
		}
		if s.Labels["namespace"] != "external-dns" {
			t.Errorf("series %q: namespace=%q want %q", s.Name, s.Labels["namespace"], "external-dns")
		}
		if s.Labels["container"] != "external-dns" {
			t.Errorf("series %q: container=%q want %q", s.Name, s.Labels["container"], "external-dns")
		}
		inst := s.Labels["instance"]
		if inst == "" {
			t.Errorf("series %q missing instance label", s.Name)
		} else if !strings.HasSuffix(inst, ":7979") {
			t.Errorf("series %q: instance=%q must end with :7979", s.Name, inst)
		}
	}
}

// ─── (m) Fallback: no pod labels when SubstrateWorkloads absent ───────────────

func TestFallbackNoSubstrateWorkloads(t *testing.T) {
	// Build with no SubstrateWorkloads (nil slice).
	reg := extdns.Registration()
	cl := coretest.Cluster()
	cl.SubstrateWorkloads = nil
	c, err := reg.Build(&extdns.Config{}, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_, all, _ := tickOnce(t, c, businessHours)
	if len(all) == 0 {
		t.Fatal("fallback: no series emitted")
	}

	// Fallback: series must NOT carry pod/namespace/container/instance labels.
	for _, s := range all {
		if s.Labels["pod"] != "" {
			t.Errorf("fallback: series %q should not have pod label, got %q", s.Name, s.Labels["pod"])
		}
		if s.Labels["namespace"] != "" {
			t.Errorf("fallback: series %q should not have namespace label, got %q", s.Name, s.Labels["namespace"])
		}
		if s.Labels["instance"] != "" {
			t.Errorf("fallback: series %q should not have instance label, got %q", s.Name, s.Labels["instance"])
		}
	}

	// Fallback must still emit the correct metric families.
	names := map[string]bool{}
	for _, s := range all {
		names[s.Name] = true
	}
	for _, required := range []string{
		"external_dns_controller_last_sync_timestamp_seconds",
		"external_dns_build_info",
		"external_dns_registry_records",
		"external_dns_http_request_duration_seconds",
	} {
		if !names[required] {
			t.Errorf("fallback: missing required series %q", required)
		}
	}
}

// ─── (n) HTTP summary: quantile + _count + _sum; no _bucket ──────────────────

func TestHTTPSummaryShape(t *testing.T) {
	c := buildDefault(t)
	_, all, _ := tickOnce(t, c, businessHours)

	seenQuantiles := map[string]bool{}
	var countSeen, sumSeen bool

	for _, s := range all {
		switch s.Name {
		case "external_dns_http_request_duration_seconds":
			q := s.Labels["quantile"]
			if q == "" {
				t.Errorf("summary base series missing quantile label")
				continue
			}
			seenQuantiles[q] = true
			// Must have path label — one of the 7 K8s API paths from recon.
			if s.Labels["path"] == "" {
				t.Errorf("summary quantile series missing path label")
			}
			// handler must be "instrumented_http" per recon.
			if s.Labels["handler"] != "instrumented_http" {
				t.Errorf("summary: handler=%q want %q", s.Labels["handler"], "instrumented_http")
			}
			// host must be kube-apiserver address per recon.
			if s.Labels["host"] != "172.20.0.1:443" {
				t.Errorf("summary: host=%q want %q", s.Labels["host"], "172.20.0.1:443")
			}
		case "external_dns_http_request_duration_seconds_count":
			countSeen = true
		case "external_dns_http_request_duration_seconds_sum":
			sumSeen = true
		case "external_dns_http_request_duration_seconds_bucket":
			t.Errorf("histogram _bucket series emitted — this is a SUMMARY, not a histogram")
		}
	}

	for _, q := range []string{"0.5", "0.9", "0.99"} {
		if !seenQuantiles[q] {
			t.Errorf("summary quantile %q not emitted", q)
		}
	}
	if !countSeen {
		t.Error("external_dns_http_request_duration_seconds_count not emitted")
	}
	if !sumSeen {
		t.Error("external_dns_http_request_duration_seconds_sum not emitted")
	}
}

// ─── (o) job label = "external-dns" (autodiscovery form) ─────────────────────

func TestJobLabel(t *testing.T) {
	c := buildDefault(t)
	_, all, _ := tickOnce(t, c, businessHours)
	for _, s := range all {
		if s.Labels["job"] != "external-dns" {
			t.Errorf("series %q: job=%q want %q", s.Name, s.Labels["job"], "external-dns")
		}
	}
}
