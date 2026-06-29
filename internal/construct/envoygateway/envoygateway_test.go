// SPDX-License-Identifier: AGPL-3.0-only

package envoygateway_test

// envoygateway_test.go — construct invariant tests for the envoy_gateway construct.
//
// Test inventory:
//   (a) Control-plane series carry the envoy-gateway pod labels (xds_*, watchable_*,
//       controller_runtime_*, rest_client_*).
//   (b) NO envoy_gateway_* family exists in control-plane output (verified by recon:
//       that prefix query is empty in Mimir).
//   (c) Data-plane envoy_* series carry the proxy pods (container=envoy, 2 pods).
//   (d) Data-plane series carry node-topology labels (availability_zone, instance_type, nodepool).
//   (e) Data-plane _time histogram series have ms bucket boundaries (le ≥ 0.5, first
//       finite le < 1000 ms range confirms ms scale, not seconds).
//   (f) Fallback: nil SubstrateWorkloads → cluster-scoped (no pod labels).
//   (g) Build error on nil Cluster.
//   (h) base labels (cluster + k8s_cluster_name on every series; NO blueprint label).
//   (i) Counters are monotone across two ticks.
//   (j) Signals() includes core.Logs alongside core.Metrics.
//   (k) Data-plane log stream: correct namespace/container labels, JSON body with
//       response_code/method/path; route_name NOT a stream label.
//   (l) Control-plane log stream: k8s_container_name=envoy-gateway, detected_level set.
//   (m) Both log streams carry detected_level; fallback graceful when no SubstrateWorkloads.

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/envoygateway"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// clusterWithEnvoyGateway returns a coretest.Cluster with envoy_gateway SubstrateWorkloads.
func clusterWithEnvoyGateway(t *testing.T) *fixture.Cluster {
	t.Helper()
	cl := coretest.Cluster()
	seed := "test"
	wls := fixture.AddonWorkloads("envoy_gateway")
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

func buildWithPods(t *testing.T) core.Construct {
	t.Helper()
	c, err := envoygateway.New(&envoygateway.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: clusterWithEnvoyGateway(t),
	})
	if err != nil {
		t.Fatalf("envoygateway.New: %v", err)
	}
	return c
}

func buildFallback(t *testing.T) core.Construct {
	t.Helper()
	cl := coretest.Cluster()
	// SubstrateWorkloads is nil — no addon pods.
	c, err := envoygateway.New(&envoygateway.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: cl,
	})
	if err != nil {
		t.Fatalf("envoygateway.New: %v", err)
	}
	return c
}

var testNow = time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

func tickOnce(t *testing.T, c core.Construct) *coretest.MetricCapture {
	t.Helper()
	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return cap
}

func filterLabel(ss []promrw.Series, key, val string) []promrw.Series {
	var out []promrw.Series
	for _, s := range ss {
		if s.Labels[key] == val {
			out = append(out, s)
		}
	}
	return out
}

// hasPrefix returns every series whose name starts with prefix.
func hasPrefix(cap *coretest.MetricCapture, prefix string) []promrw.Series {
	var out []promrw.Series
	for _, s := range cap.All() {
		if strings.HasPrefix(s.Name, prefix) {
			out = append(out, s)
		}
	}
	return out
}

// ─── (a) Control-plane pod labels ────────────────────────────────────────────

// TestControlPlanePodLabels verifies that control-plane families carry the
// envoy-gateway pod labels (pod/namespace/container/instance).
func TestControlPlanePodLabels(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	// xds_snapshot_create_total is a representative control-plane family.
	series := cap.Find("xds_snapshot_create_total")
	if len(series) == 0 {
		t.Fatal("xds_snapshot_create_total: no series found — control plane not emitting")
	}

	for _, s := range series {
		if s.Labels["pod"] == "" {
			t.Errorf("xds_snapshot_create_total: missing pod label: %v", s.Labels)
		}
		if s.Labels["namespace"] != "envoy-gateway-system" {
			t.Errorf("namespace=%q want envoy-gateway-system", s.Labels["namespace"])
		}
		if s.Labels["container"] != "envoy-gateway" {
			t.Errorf("container=%q want envoy-gateway", s.Labels["container"])
		}
		if s.Labels["job"] != "gateway-helm" {
			t.Errorf("job=%q want gateway-helm", s.Labels["job"])
		}
		if !strings.HasSuffix(s.Labels["instance"], ":19001") {
			t.Errorf("instance=%q must end with :19001", s.Labels["instance"])
		}
	}

	// watchable_event_total — another EG-specific control-plane metric.
	watchable := cap.Find("watchable_event_total")
	if len(watchable) == 0 {
		t.Fatal("watchable_event_total: no series found")
	}
	for _, s := range watchable {
		if s.Labels["pod"] == "" {
			t.Errorf("watchable_event_total: missing pod label")
		}
	}

	// controller_runtime_reconcile_total on the control plane.
	cr := filterLabel(cap.Find("controller_runtime_reconcile_total"), "job", "gateway-helm")
	if len(cr) == 0 {
		t.Fatal("controller_runtime_reconcile_total with job=gateway-helm: no series found")
	}
}

// ─── (b) NO envoy_gateway_* family ───────────────────────────────────────────

// TestNoEnvoyGatewayPrefix verifies that the construct emits NO metrics with the
// envoy_gateway_* prefix — confirmed by recon: that prefix query is empty in Mimir.
func TestNoEnvoyGatewayPrefix(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	badSeries := hasPrefix(cap, "envoy_gateway_")
	if len(badSeries) != 0 {
		names := map[string]bool{}
		for _, s := range badSeries {
			names[s.Name] = true
		}
		t.Errorf("envoy_gateway_* prefix must not be emitted (recon: empty in Mimir); found: %v", sortedKeys(names))
	}
}

// ─── (c) Data-plane proxy pod labels ─────────────────────────────────────────

// TestDataPlanePodLabels verifies that envoy_* series carry the proxy pod labels
// (container=envoy, 2 pods, namespace=envoy-gateway-system, job=envoy).
func TestDataPlanePodLabels(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	// envoy_cluster_upstream_rq_total is a representative data-plane family.
	series := cap.Find("envoy_cluster_upstream_rq_total")
	if len(series) == 0 {
		t.Fatal("envoy_cluster_upstream_rq_total: no series found — data plane not emitting")
	}

	pods := map[string]bool{}
	for _, s := range series {
		if s.Labels["container"] != "envoy" {
			t.Errorf("container=%q want envoy", s.Labels["container"])
		}
		if s.Labels["job"] != "envoy" {
			t.Errorf("job=%q want envoy", s.Labels["job"])
		}
		if s.Labels["namespace"] != "envoy-gateway-system" {
			t.Errorf("namespace=%q want envoy-gateway-system", s.Labels["namespace"])
		}
		if !strings.HasSuffix(s.Labels["instance"], ":19001") {
			t.Errorf("instance=%q must end with :19001", s.Labels["instance"])
		}
		pods[s.Labels["pod"]] = true
	}

	// 2 proxy pods (Replicas=2 in fixture).
	if len(pods) < 2 {
		t.Errorf("data plane: want ≥2 distinct pods, got %d: %v", len(pods), pods)
	}
}

// ─── (d) Node-topology labels on data plane ───────────────────────────────────

// TestDataPlaneNodeTopologyLabels verifies that data-plane envoy_* series carry the
// node-topology labels observed in the live recon: availability_zone, instance_type, nodepool.
func TestDataPlaneNodeTopologyLabels(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	// Pick a representative data-plane series.
	series := cap.Find("envoy_server_uptime")
	if len(series) == 0 {
		t.Fatal("envoy_server_uptime: no series found")
	}

	for _, s := range series {
		if s.Labels["availability_zone"] == "" {
			t.Errorf("envoy_server_uptime: missing availability_zone label: %v", s.Labels)
		}
		if s.Labels["instance_type"] == "" {
			t.Errorf("envoy_server_uptime: missing instance_type label: %v", s.Labels)
		}
		if s.Labels["nodepool"] == "" {
			t.Errorf("envoy_server_uptime: missing nodepool label: %v", s.Labels)
		}
	}
}

// ─── (e) Data-plane _time histogram buckets in milliseconds ──────────────────

// TestDataPlaneTimeBucketsAreMs verifies that _time histogram bucket boundaries
// are in milliseconds (first finite le is 0.5, not 0.005 — see recon le list).
func TestDataPlaneTimeBucketsAreMs(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	// envoy_http_downstream_rq_time_bucket — first finite le should be 0.5 (ms scale).
	buckets := cap.Find("envoy_http_downstream_rq_time_bucket")
	if len(buckets) == 0 {
		t.Fatal("envoy_http_downstream_rq_time_bucket: no series found")
	}

	// Collect le values across all series.
	leVals := map[string]bool{}
	for _, s := range buckets {
		if le := s.Labels["le"]; le != "" {
			leVals[le] = true
		}
	}

	// Must have le=0.5 (first finite bucket in ms scale).
	if !leVals["0.5"] {
		t.Errorf("envoy_http_downstream_rq_time_bucket: expected le=0.5 (ms scale), le values: %v", sortedKeys(leVals))
	}
	// Must NOT have le=0.005 (that would be seconds scale).
	if leVals["0.005"] {
		t.Errorf("envoy_http_downstream_rq_time_bucket: found le=0.005 (seconds scale), but _time histos should be ms")
	}
}

// ─── (f) Fallback: nil SubstrateWorkloads → cluster-scoped ───────────────────

// TestFallbackNoSubstrateWorkloads verifies that with empty SubstrateWorkloads,
// the construct still emits series (no pod label on them) without crashing.
func TestFallbackNoSubstrateWorkloads(t *testing.T) {
	c := buildFallback(t)
	cap := tickOnce(t, c)

	all := cap.All()
	if len(all) == 0 {
		t.Fatal("fallback: no series emitted at all")
	}

	// In fallback mode, no pod label should be present.
	for _, s := range all {
		if s.Labels["pod"] != "" {
			t.Errorf("fallback: series %q should not have pod label, got pod=%q", s.Name, s.Labels["pod"])
		}
	}

	// xds_snapshot_create_total must still exist in fallback.
	if len(cap.Find("xds_snapshot_create_total")) == 0 {
		t.Error("fallback: missing xds_snapshot_create_total")
	}
	// envoy_server_uptime must still exist in fallback.
	if len(cap.Find("envoy_server_uptime")) == 0 {
		t.Error("fallback: missing envoy_server_uptime")
	}
}

// ─── (g) Build error on nil Cluster ──────────────────────────────────────────

func TestBuildErrorOnNilCluster(t *testing.T) {
	_, err := envoygateway.New(&envoygateway.Config{}, &fixture.Set{Seed: "test"})
	if err == nil {
		t.Fatal("expected error when Cluster is nil, got nil")
	}
}

// ─── (h) Base labels ──────────────────────────────────────────────────────────

func TestBaseLabels(t *testing.T) {
	c := buildWithPods(t)
	cap := tickOnce(t, c)

	clust := clusterWithEnvoyGateway(t)

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

// ─── (i) Counters are monotone across two ticks ───────────────────────────────

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
		"xds_snapshot_create_total",
		"watchable_event_total",
		"envoy_cluster_upstream_rq_total",
		"controller_runtime_reconcile_total",
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

// ─── metadata ─────────────────────────────────────────────────────────────────

// TestKindAndSignals verifies Kind/Interval and that Signals() includes both
// core.Metrics and core.Logs (j).
func TestKindAndSignals(t *testing.T) {
	c := buildWithPods(t)
	if c.Kind() != "envoy_gateway" {
		t.Errorf("Kind() = %q, want envoy_gateway", c.Kind())
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
		t.Errorf("Signals() missing core.Metrics; got %v", sigs)
	}
	// (j) core.Logs must be present
	if !hasLogs {
		t.Errorf("Signals() missing core.Logs; got %v", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval() = %v, want 60s", c.Interval())
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

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ─── log helpers ──────────────────────────────────────────────────────────────

// tickOnceWithLogs ticks the construct and returns (metric capture, log capture).
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

// streamsByContainer groups Loki streams by their k8s_container_name label.
func streamsByContainer(streams []loki.Stream) map[string][]loki.Stream {
	out := make(map[string][]loki.Stream)
	for _, s := range streams {
		key := s.Labels["k8s_container_name"]
		out[key] = append(out[key], s)
	}
	return out
}

// ─── (j) Signals() includes core.Logs ────────────────────────────────────────
// (Tested inline in TestKindAndSignals above.)

// ─── (k) Data-plane log stream labels + JSON body ─────────────────────────────

// TestDataPlaneLogStream verifies:
//   - stream label k8s_namespace_name = "envoy-gateway-system"
//   - stream label k8s_container_name = "envoy"
//   - stream label k8s_pod_name matches a proxy pod name pattern
//   - route_name is NOT a stream label (high-card, body only)
//   - log body is JSON containing response_code, method, path
//   - detected_level is set on the stream
func TestDataPlaneLogStream(t *testing.T) {
	c := buildWithPods(t)
	_, lc := tickOnceWithLogs(t, c)

	byContainer := streamsByContainer(lc.Streams)
	envoyStreams := byContainer["envoy"]
	if len(envoyStreams) == 0 {
		t.Fatal("no data-plane log streams (k8s_container_name=envoy) emitted")
	}

	cl := clusterWithEnvoyGateway(t)

	for _, stream := range envoyStreams {
		// k8s_namespace_name must be envoy-gateway-system
		if stream.Labels["k8s_namespace_name"] != "envoy-gateway-system" {
			t.Errorf("data-plane: k8s_namespace_name=%q want envoy-gateway-system", stream.Labels["k8s_namespace_name"])
		}
		// k8s_container_name already selected by this loop.
		if stream.Labels["k8s_container_name"] != "envoy" {
			t.Errorf("data-plane: k8s_container_name=%q want envoy", stream.Labels["k8s_container_name"])
		}
		// cluster labels
		if stream.Labels["cluster"] != cl.Name {
			t.Errorf("data-plane: cluster=%q want %q", stream.Labels["cluster"], cl.Name)
		}
		if stream.Labels["k8s_cluster_name"] != cl.Name {
			t.Errorf("data-plane: k8s_cluster_name=%q want %q", stream.Labels["k8s_cluster_name"], cl.Name)
		}
		// k8s_pod_name must be present (proxy pod name)
		if stream.Labels["k8s_pod_name"] == "" {
			t.Errorf("data-plane: k8s_pod_name must be set (got empty)")
		}
		// detected_level must be present
		if stream.Labels["detected_level"] == "" {
			t.Errorf("data-plane: detected_level must be set")
		}
		// route_name must NOT be a stream label (high-cardinality — body only)
		if _, ok := stream.Labels["route_name"]; ok {
			t.Errorf("data-plane: route_name must NOT be a stream label (high-card); found in Labels")
		}
		// upstream_cluster must NOT be a stream label either
		if _, ok := stream.Labels["upstream_cluster"]; ok {
			t.Errorf("data-plane: upstream_cluster must NOT be a stream label (high-card); found in Labels")
		}
		// Must not carry blueprint label
		if _, ok := stream.Labels["blueprint"]; ok {
			t.Errorf("data-plane log stream carries blueprint label (ScopeSubstrate violation)")
		}
		// Must have at least one log line
		if len(stream.Lines) == 0 {
			t.Errorf("data-plane: stream has no lines")
			continue
		}
		// Each line body must be valid JSON containing response_code, method, path
		for _, line := range stream.Lines {
			if len(line.Body) == 0 {
				t.Errorf("data-plane: empty log line body")
				continue
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(line.Body), &obj); err != nil {
				t.Errorf("data-plane: log line body is not valid JSON: %q, err=%v", line.Body, err)
				continue
			}
			if _, ok := obj["response_code"]; !ok {
				t.Errorf("data-plane: JSON body missing response_code field: %q", line.Body)
			}
			if _, ok := obj["method"]; !ok {
				t.Errorf("data-plane: JSON body missing method field: %q", line.Body)
			}
			if _, ok := obj["path"]; !ok {
				// Accept either "path" or "x-envoy-origin-path" — recon shows x-envoy-origin-path.
				// Actually the requirement says "path" field, so assert "path".
				t.Errorf("data-plane: JSON body missing path field: %q", line.Body)
			}
			// route_name must be in the body (high-card field lives in body, not labels)
			if _, ok := obj["route_name"]; !ok {
				t.Errorf("data-plane: JSON body missing route_name field (must be in body, not labels): %q", line.Body)
			}
		}
	}
}

// ─── (l) Control-plane log stream ─────────────────────────────────────────────

// TestControlPlaneLogStream verifies:
//   - stream label k8s_container_name = "envoy-gateway"
//   - detected_level is set (info or warn)
//   - has at least one log line per tick
func TestControlPlaneLogStream(t *testing.T) {
	c := buildWithPods(t)
	_, lc := tickOnceWithLogs(t, c)

	byContainer := streamsByContainer(lc.Streams)
	cpStreams := byContainer["envoy-gateway"]
	if len(cpStreams) == 0 {
		t.Fatal("no control-plane log streams (k8s_container_name=envoy-gateway) emitted")
	}

	cl := clusterWithEnvoyGateway(t)

	for _, stream := range cpStreams {
		if stream.Labels["k8s_container_name"] != "envoy-gateway" {
			t.Errorf("control-plane: k8s_container_name=%q want envoy-gateway", stream.Labels["k8s_container_name"])
		}
		if stream.Labels["k8s_namespace_name"] != "envoy-gateway-system" {
			t.Errorf("control-plane: k8s_namespace_name=%q want envoy-gateway-system", stream.Labels["k8s_namespace_name"])
		}
		if stream.Labels["cluster"] != cl.Name {
			t.Errorf("control-plane: cluster=%q want %q", stream.Labels["cluster"], cl.Name)
		}
		// detected_level must be set (info or warn)
		dl := stream.Labels["detected_level"]
		if dl != "info" && dl != "warn" {
			t.Errorf("control-plane: detected_level=%q want info or warn", dl)
		}
		// Must not carry blueprint label
		if _, ok := stream.Labels["blueprint"]; ok {
			t.Errorf("control-plane log stream carries blueprint label (ScopeSubstrate violation)")
		}
		if len(stream.Lines) == 0 {
			t.Errorf("control-plane: stream has no lines")
		}
	}
}

// ─── (m) Both streams present; fallback graceful ──────────────────────────────

// TestBothLogSurfaces verifies that a single tick produces both a data-plane stream
// (container=envoy) and a control-plane stream (container=envoy-gateway).
func TestBothLogSurfaces(t *testing.T) {
	c := buildWithPods(t)
	_, lc := tickOnceWithLogs(t, c)

	byContainer := streamsByContainer(lc.Streams)

	if len(byContainer["envoy"]) == 0 {
		t.Error("missing data-plane log stream (k8s_container_name=envoy)")
	}
	if len(byContainer["envoy-gateway"]) == 0 {
		t.Error("missing control-plane log stream (k8s_container_name=envoy-gateway)")
	}
}

// TestLogsNilGuard verifies that passing a nil Logs writer (w.Logs==nil) does not panic.
// Construct must check w.Logs != nil before writing logs.
func TestLogsNilGuard(t *testing.T) {
	c := buildWithPods(t)
	mc := &coretest.MetricCapture{}
	// World with nil LogCapture → w.Logs is nil
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick with nil Logs should not error: %v", err)
	}
}

// TestLogsFallbackGraceful verifies that when SubstrateWorkloads is absent, logs are
// still emitted (fallback to synthetic pod names) and both surfaces appear.
func TestLogsFallbackGraceful(t *testing.T) {
	c := buildFallback(t)
	_, lc := tickOnceWithLogs(t, c)

	byContainer := streamsByContainer(lc.Streams)

	if len(byContainer["envoy"]) == 0 {
		t.Error("fallback: missing data-plane log stream (k8s_container_name=envoy)")
	}
	if len(byContainer["envoy-gateway"]) == 0 {
		t.Error("fallback: missing control-plane log stream (k8s_container_name=envoy-gateway)")
	}

	// In fallback mode, k8s_pod_name should still be set (synthetic name).
	for _, stream := range byContainer["envoy"] {
		if stream.Labels["k8s_pod_name"] == "" {
			t.Errorf("fallback: data-plane k8s_pod_name must be set (synthetic fallback)")
		}
	}
}

// TestDataPlaneLogVolume verifies 3–6 lines are emitted per tick (data-plane is high volume).
func TestDataPlaneLogVolume(t *testing.T) {
	c := buildWithPods(t)
	_, lc := tickOnceWithLogs(t, c)

	var totalLines int
	for _, stream := range lc.Streams {
		if stream.Labels["k8s_container_name"] == "envoy" {
			totalLines += len(stream.Lines)
		}
	}
	if totalLines < 3 {
		t.Errorf("data-plane: want ≥3 log lines per tick, got %d", totalLines)
	}
	if totalLines > 6 {
		t.Errorf("data-plane: want ≤6 log lines per tick, got %d", totalLines)
	}
}
