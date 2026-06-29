// SPDX-License-Identifier: AGPL-3.0-only

package vpccni_test

// vpccni_test.go — construct invariant tests for the vpc_cni construct.
//
// Test inventory:
//   (a) Exact series name inventory (all expected names, no extras).
//   (b) fn/api/reason label value enums.
//   (c) cluster+k8s_cluster_name dual label on every series.
//   (d) NO blueprint label on any series (ScopeSubstrate invariant — ARCHITECTURE I21).
//   (e) ENI/IP gauge consistency with node count.
//   (f) Counter monotone: two ticks accumulate (state.Add).
//   (g) Build returns an error when Cluster fixture is nil.
//   (h) Kind/Signals/Interval metadata.

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/vpccni"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	c, err := vpccni.New(&vpccni.Config{}, &fixture.Set{
		Env:     coretest.Env(),
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("vpccni.New: %v", err)
	}
	return c
}

func tickOnce(t *testing.T, c core.Construct) *coretest.MetricCapture {
	t.Helper()
	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC) // business hours
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return cap
}

// ─── (a) Exact series inventory ──────────────────────────────────────────────

// expectedNames is the canonical inventory derived from §6.4 of the extract.
// No histograms — awscni_aws_api_latency_ms is a summary approximation (count+sum only).
var expectedNames = func() []string {
	names := []string{
		// gauges
		"awscni_eni_allocated",
		"awscni_total_ip_addresses",
		"awscni_assigned_ip_addresses",
		"awscni_eni_max",
		"awscni_ip_max",
		"awscni_ipamd_action_inprogress",
		"awscni_total_ipv4_prefixes",
		"awscni_build_info",
		// counters
		"awscni_ipamd_error_count",
		"awscni_add_ip_req_count",
		"awscni_del_ip_req_count",
		"awscni_aws_api_latency_ms_count",
		"awscni_aws_api_latency_ms_sum",
		"awscni_aws_api_error_count",
		"awscni_ec2api_req_count",
		"awscni_ec2api_error_count",
		"awscni_reconcile_count",
		"awscni_no_available_ip_addresses",
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

// ─── (b) Label value enums ───────────────────────────────────────────────────

func TestIPAMDFnEnum(t *testing.T) {
	cap := tickOnce(t, buildDefault(t))
	fns := map[string]bool{}
	for _, s := range cap.All() {
		if s.Name == "awscni_ipamd_action_inprogress" {
			fns[s.Labels["fn"]] = true
		}
	}
	for _, want := range []string{"nodeIPPoolReconcile", "eniIPPoolReconcile", "decreaseIPPool", "increaseIPPool"} {
		if !fns[want] {
			t.Errorf("IPAMD fn %q absent from awscni_ipamd_action_inprogress", want)
		}
	}
}

func TestEC2FnEnum(t *testing.T) {
	cap := tickOnce(t, buildDefault(t))
	fns := map[string]bool{}
	for _, s := range cap.All() {
		if s.Name == "awscni_ec2api_req_count" {
			fns[s.Labels["fn"]] = true
		}
	}
	for _, want := range []string{"AssignPrivateIpAddresses", "DescribeNetworkInterfaces", "AttachNetworkInterface"} {
		if !fns[want] {
			t.Errorf("EC2 fn %q absent from awscni_ec2api_req_count", want)
		}
	}
}

func TestDelIPReasonEnum(t *testing.T) {
	cap := tickOnce(t, buildDefault(t))
	reasons := map[string]bool{}
	for _, s := range cap.All() {
		if s.Name == "awscni_del_ip_req_count" {
			reasons[s.Labels["reason"]] = true
		}
	}
	for _, want := range []string{"pod_deleted", "failed_node"} {
		if !reasons[want] {
			t.Errorf("reason %q absent from awscni_del_ip_req_count", want)
		}
	}
}

func TestAPINameEnum(t *testing.T) {
	cap := tickOnce(t, buildDefault(t))
	apis := map[string]bool{}
	for _, s := range cap.All() {
		if s.Name == "awscni_aws_api_latency_ms_count" {
			apis[s.Labels["api"]] = true
		}
	}
	for _, want := range []string{"EC2:DescribeNetworkInterfaces", "EC2:AssignPrivateIpAddresses"} {
		if !apis[want] {
			t.Errorf("api %q absent from awscni_aws_api_latency_ms_count", want)
		}
	}
}

// ─── (c) cluster + k8s_cluster_name dual label on every series ───────────────

func TestClusterLabels(t *testing.T) {
	clust := coretest.Cluster()
	c, err := vpccni.New(&vpccni.Config{}, &fixture.Set{
		Env:     coretest.Env(),
		Cluster: clust,
	})
	if err != nil {
		t.Fatalf("vpccni.New: %v", err)
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

// ─── (e) ENI/IP gauge consistency with node count ────────────────────────────

// TestENIIPGaugeConsistency checks that awscni_eni_max and awscni_ip_max scale with
// the number of nodes, and that assigned_ip_addresses <= total_ip_addresses.
func TestENIIPGaugeConsistency(t *testing.T) {
	clust := coretest.Cluster() // 3 nodes
	c, err := vpccni.New(&vpccni.Config{}, &fixture.Set{
		Env:     coretest.Env(),
		Cluster: clust,
	})
	if err != nil {
		t.Fatalf("vpccni.New: %v", err)
	}
	cap := tickOnce(t, c)

	nodeCount := len(clust.Nodes) // 3

	getGauge := func(name string) float64 {
		for _, s := range cap.All() {
			if s.Name == name {
				return s.Value
			}
		}
		return -1
	}

	eniMax := getGauge("awscni_eni_max")
	ipMax := getGauge("awscni_ip_max")
	assignedIPs := getGauge("awscni_assigned_ip_addresses")
	totalIPs := getGauge("awscni_total_ip_addresses")

	if eniMax < 0 {
		t.Fatal("awscni_eni_max absent")
	}
	if ipMax < 0 {
		t.Fatal("awscni_ip_max absent")
	}

	// eni_max should be nodeCount * 3 (typical ~3 ENIs per m6i instance).
	wantENIMax := float64(nodeCount * 3)
	if eniMax != wantENIMax {
		t.Errorf("awscni_eni_max=%.0f want %.0f (nodeCount=%d * 3)", eniMax, wantENIMax, nodeCount)
	}

	// ip_max should be nodeCount * 14 (typical ~14 IPs per m6i instance).
	wantIPMax := float64(nodeCount * 14)
	if ipMax != wantIPMax {
		t.Errorf("awscni_ip_max=%.0f want %.0f (nodeCount=%d * 14)", ipMax, wantIPMax, nodeCount)
	}

	// total_ip_addresses should be slightly above assigned (warm buffer).
	if totalIPs < 0 {
		t.Fatal("awscni_total_ip_addresses absent")
	}
	if assignedIPs < 0 {
		t.Fatal("awscni_assigned_ip_addresses absent")
	}
	if totalIPs < assignedIPs {
		t.Errorf("awscni_total_ip_addresses=%.1f < awscni_assigned_ip_addresses=%.1f — total should be >= assigned",
			totalIPs, assignedIPs)
	}

	// All IP counts should be positive.
	if assignedIPs <= 0 {
		t.Errorf("awscni_assigned_ip_addresses=%.1f should be > 0 during business hours", assignedIPs)
	}
}

// ─── (f) Counter monotone across ticks ───────────────────────────────────────

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

	// awscni_add_ip_req_count is a counter with a non-zero delta at factor>0.
	val := func(cap *coretest.MetricCapture, name string) float64 {
		for _, s := range cap.All() {
			if s.Name == name {
				return s.Value
			}
		}
		return -1
	}

	v1 := val(cap1, "awscni_add_ip_req_count")
	v2 := val(cap2, "awscni_add_ip_req_count")
	if v1 < 0 {
		t.Fatal("awscni_add_ip_req_count absent in tick1")
	}
	if v2 < 0 {
		t.Fatal("awscni_add_ip_req_count absent in tick2")
	}
	if v2 <= v1 {
		t.Errorf("counter should accumulate: tick1=%.2f tick2=%.2f — not monotone", v1, v2)
	}
}

// TestGaugeNotAccumulated verifies that gauges (state.Set) do NOT accumulate.
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

	gauge := func(cap *coretest.MetricCapture, name string) float64 {
		for _, s := range cap.All() {
			if s.Name == name {
				return s.Value
			}
		}
		return -1
	}

	for _, name := range []string{"awscni_eni_max", "awscni_ip_max", "awscni_total_ip_addresses"} {
		v1 := gauge(cap1, name)
		v2 := gauge(cap2, name)
		if v1 < 0 {
			t.Errorf("%s absent in tick1", name)
			continue
		}
		if v2 >= v1*1.5 {
			t.Errorf("gauge %s appears to accumulate: tick1=%.0f tick2=%.0f", name, v1, v2)
		}
	}
}

// ─── (g) Build error on nil Cluster ──────────────────────────────────────────

func TestBuildErrorOnNilCluster(t *testing.T) {
	_, err := vpccni.New(&vpccni.Config{}, &fixture.Set{
		Env:     coretest.Env(),
		Cluster: nil,
	})
	if err == nil {
		t.Fatal("expected error when Cluster is nil, got nil")
	}
}

// ─── (h) Metadata ─────────────────────────────────────────────────────────────

func TestKindAndSignals(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "vpc_cni" {
		t.Errorf("Kind() = %q, want %q", c.Kind(), "vpc_cni")
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals() = %v, want [Metrics]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval() = %v, want 60s", c.Interval())
	}
}
