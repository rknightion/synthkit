// SPDX-License-Identifier: AGPL-3.0-only

package ec2_test

// ec2_test.go — construct invariant tests for the ec2 construct.
//
// Test inventory:
//   (a) Exact series name inventory (all expected names, no extras).
//   (b) One series-set per fixture node; dimension_InstanceId values == fixture node
//       InstanceIDs byte-exact. This is THE correlation test (ARCHITECTURE I12),
//       mirroring the predecessor's TestEC2PerInstanceJoinAndCorrelation invariant.
//   (c) Label keys present on every series (universal CW labels).
//   (d) No cumulative Add behaviour: two ticks produce the same values (gauges, not
//       counters — ARCHITECTURE I5).
//   (e) Build returns an error when Cloud or Cluster fixture is nil.

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/ec2"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/scale"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// buildDefault builds an EC2 construct against the standard coretest fixtures,
// with NodeGroups/Seed/Region set so that live node derivation (DeriveNodes) works.
func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	c, err := ec2.New(&ec2.Config{}, &fixture.Set{
		Env:     coretest.Env(),
		Cloud:   coretest.Cloud(),
		Cluster: clusterWithNodeGroups(),
	})
	if err != nil {
		t.Fatalf("ec2.New: %v", err)
	}
	return c
}

// tickOnce ticks the construct and returns all captured series.
func tickOnce(t *testing.T, c core.Construct) []string {
	t.Helper()
	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC) // business hours
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return cap.Names()
}

// ─── (a) Exact series inventory ───────────────────────────────────────────────

// expectedNames is the canonical inventory of all series names the ec2 construct must
// emit. Derived from signals/cw.md [slug: cw-ec2] table + the five CW stat suffixes per base
// name + the info series (no stat suffix).
var expectedNames = func() []string {
	// Base metric names (no stat suffix) — all have all five suffixes.
	bases := []string{
		"aws_ec2_cpuutilization",
		"aws_ec2_network_in",
		"aws_ec2_network_out",
		"aws_ec2_status_check_failed",
		"aws_ec2_status_check_failed_instance",
		"aws_ec2_status_check_failed_system",
		"aws_ec2_status_check_failed_attached_ebs",
		"aws_ec2_ebsread_bytes",
		"aws_ec2_ebswrite_bytes",
		"aws_ec2_ebsread_ops",
		"aws_ec2_ebswrite_ops",
		"aws_ec2_cpucredit_balance",
	}
	stats := []string{"_average", "_maximum", "_minimum", "_sum", "_sample_count"}
	set := map[string]struct{}{}
	for _, b := range bases {
		for _, s := range stats {
			set[b+s] = struct{}{}
		}
	}
	// Info series (no stat suffix)
	set["aws_ec2_info"] = struct{}{}

	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}()

func TestSeriesInventory(t *testing.T) {
	c := buildDefault(t)
	got := tickOnce(t, c)

	want := expectedNames

	wantSet := map[string]bool{}
	for _, n := range want {
		wantSet[n] = true
	}
	gotSet := map[string]bool{}
	for _, n := range got {
		gotSet[n] = true
	}

	for _, n := range want {
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

// ─── (b) One series-set per node; dimension_InstanceId byte-exact ─────────────

// TestPerNodeCorrelation asserts the EC2↔EKS correlation invariant (ARCHITECTURE I12):
//   - Every fixture node has a per-instance aws_ec2_cpuutilization_average series.
//   - The dimension_InstanceId label on that series matches the fixture Node.InstanceID
//     byte-exact.
//   - The CPU value is in (0, 100].
//   - Every fixture node also has a per-instance aws_ec2_network_in_sum series.
//
// This mirrors the invariant the predecessor's TestEC2PerInstanceJoinAndCorrelation asserted,
// adapted for synthkit's fixture model.
func TestPerNodeCorrelation(t *testing.T) {
	clust := clusterWithNodeGroups()
	c, err := ec2.New(&ec2.Config{}, &fixture.Set{
		Env:     coretest.Env(),
		Cloud:   coretest.Cloud(),
		Cluster: clust,
	})
	if err != nil {
		t.Fatalf("ec2.New: %v", err)
	}

	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Build lookup: dimension_InstanceId → cpu_average value
	cpuByInstance := map[string]float64{}
	netByInstance := map[string]bool{}
	for _, s := range cap.All() {
		id := s.Labels["dimension_InstanceId"]
		if id == "" {
			continue
		}
		if s.Name == "aws_ec2_cpuutilization_average" {
			cpuByInstance[id] = s.Value
		}
		if s.Name == "aws_ec2_network_in_sum" {
			netByInstance[id] = true
		}
	}

	// Every fixture node must have a per-instance EC2 series with the exact InstanceID.
	for i, n := range clust.Nodes {
		cpu, ok := cpuByInstance[n.InstanceID]
		if !ok {
			t.Errorf("node[%d] InstanceID=%q: no per-instance aws_ec2_cpuutilization_average", i, n.InstanceID)
			continue
		}
		if cpu <= 0 || cpu > 100 {
			t.Errorf("node[%d] InstanceID=%q: cpuutilization_average %.1f outside (0,100]", i, n.InstanceID, cpu)
		}
		if !netByInstance[n.InstanceID] {
			t.Errorf("node[%d] InstanceID=%q: no per-instance aws_ec2_network_in_sum", i, n.InstanceID)
		}
	}

	// No spurious instance IDs that are not in the fixture.
	fixtureIDs := map[string]bool{}
	for _, n := range clust.Nodes {
		fixtureIDs[n.InstanceID] = true
	}
	for id := range cpuByInstance {
		if !fixtureIDs[id] {
			t.Errorf("found dimension_InstanceId=%q not in fixture nodes", id)
		}
	}

	// Total per-instance CPU series count must equal node count.
	if len(cpuByInstance) != len(clust.Nodes) {
		t.Errorf("per-instance cpu series count %d != node count %d", len(cpuByInstance), len(clust.Nodes))
	}
}

// ─── (c) Label keys present on every series ──────────────────────────────────

// universalCWKeys are the label keys that must appear on every aws_ec2_* series
// (except aws_ec2_info which additionally has tag_* keys).
var universalCWKeys = []string{"account_id", "job", "name", "namespace", "region"}

func TestUniversalLabelKeys(t *testing.T) {
	c := buildDefault(t)
	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	for _, name := range expectedNames {
		keys := cap.LabelKeys(name)
		keySet := map[string]bool{}
		for _, k := range keys {
			keySet[k] = true
		}
		for _, req := range universalCWKeys {
			if !keySet[req] {
				t.Errorf("series %q missing required label key %q (got %v)", name, req, keys)
			}
		}
	}

	// aws_ec2_info must also carry tag_VpcId and dimension_InstanceId.
	infoKeys := cap.LabelKeys("aws_ec2_info")
	infoSet := map[string]bool{}
	for _, k := range infoKeys {
		infoSet[k] = true
	}
	for _, req := range []string{"tag_VpcId", "dimension_InstanceId"} {
		if !infoSet[req] {
			t.Errorf("aws_ec2_info missing label key %q (got %v)", req, infoKeys)
		}
	}
}

// ─── (d) No cumulative Add behaviour ─────────────────────────────────────────

// TestNoCumulativeAdd verifies that two successive ticks at the same time produce the
// same values (i.e. Set semantics, not Add/accumulation). ARCHITECTURE I5: all CW _sum
// series are per-period gauges, never cumulative counters.
func TestNoCumulativeAdd(t *testing.T) {
	c := buildDefault(t)
	cap1 := &coretest.MetricCapture{}
	cap2 := &coretest.MetricCapture{}
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

	w1 := coretest.World(cap1, nil, nil)
	if err := c.Tick(context.Background(), now, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	w2 := coretest.World(cap2, nil, nil)
	if err := c.Tick(context.Background(), now, w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	// Discriminator: _sample_count. It is a DETERMINISTIC constant (60) per CW period, so
	// it is immune to the NormFloat64 noise that makes _average/_sum vary legitimately
	// between ticks (which would make a value-ratio test flaky). Under Set semantics it
	// stays 60 across re-ticks; under Add accumulation it would double to 120. This is the
	// cleanest, noise-free Add-tell.
	sampleCount := func(cap *coretest.MetricCapture, name string) map[string]float64 {
		out := map[string]float64{}
		for _, s := range cap.All() {
			if s.Name == name {
				out[s.Labels["dimension_InstanceId"]] = s.Value
			}
		}
		return out
	}
	c1 := sampleCount(cap1, "aws_ec2_cpuutilization_sample_count")
	c2 := sampleCount(cap2, "aws_ec2_cpuutilization_sample_count")
	if len(c1) == 0 {
		t.Fatal("no aws_ec2_cpuutilization_sample_count series in first tick")
	}
	for id, v1 := range c1 {
		v2, ok := c2[id]
		if !ok {
			t.Errorf("instance %q present in tick1 but absent in tick2", id)
			continue
		}
		if v2 != v1 {
			t.Errorf("instance %q: _sample_count tick1=%.0f tick2=%.0f — must be identical (Set, per-period gauge); doubling = Add accumulation",
				id, v1, v2)
		}
	}

	// Same deterministic check at the ASG level for network_in (no InstanceId dimension).
	asgCount := func(cap *coretest.MetricCapture) float64 {
		for _, s := range cap.All() {
			if s.Name == "aws_ec2_network_in_sample_count" && s.Labels["dimension_AutoScalingGroupName"] != "" && s.Labels["dimension_InstanceId"] == "" {
				return s.Value
			}
		}
		return 0
	}
	if a1, a2 := asgCount(cap1), asgCount(cap2); a1 > 0 && a2 != a1 {
		t.Errorf("aws_ec2_network_in_sample_count: tick1=%.0f tick2=%.0f — must be identical (per-period gauge, not Add)", a1, a2)
	}
}

// ─── (e) Build errors on nil Cloud / nil Cluster ──────────────────────────────

func TestBuildErrorOnNilCloud(t *testing.T) {
	_, err := ec2.New(&ec2.Config{}, &fixture.Set{
		Env:     coretest.Env(),
		Cloud:   nil,
		Cluster: coretest.Cluster(),
	})
	if err == nil {
		t.Fatal("expected error when Cloud is nil, got nil")
	}
}

func TestBuildErrorOnNilCluster(t *testing.T) {
	_, err := ec2.New(&ec2.Config{}, &fixture.Set{
		Env:     coretest.Env(),
		Cloud:   coretest.Cloud(),
		Cluster: nil,
	})
	if err == nil {
		t.Fatal("expected error when Cluster is nil, got nil")
	}
}

// ─── live-scaling tests ───────────────────────────────────────────────────────

// clusterWithNodeGroups returns a cluster whose Seed/NodeGroups/Region are set so that
// fixture.DeriveNodes produces byte-identical nodes to what the resolver would build.
// The seed "test" + cluster "test-prod-use1" + group "general" matches coretest.Cluster().
func clusterWithNodeGroups() *fixture.Cluster {
	cl := coretest.Cluster()
	cl.Seed = "test"
	cl.Region = "us-east-1"
	cl.NodeGroups = []fixture.NodeGroupSpec{{Name: "general", InstanceType: "m6i.xlarge"}}
	return cl
}

// worldWithScaling returns a World whose Scaling source has the given target→count overrides.
func worldWithScaling(cap *coretest.MetricCapture, src *scale.Source) *core.World {
	w := coretest.World(cap, nil, nil)
	w.Scaling = src
	return w
}

// tickAt ticks the construct with an optional scaling source and returns the captured series.
func tickAt(t *testing.T, c core.Construct, src *scale.Source) *coretest.MetricCapture {
	t.Helper()
	cap := &coretest.MetricCapture{}
	var w *core.World
	if src != nil {
		w = worldWithScaling(cap, src)
	} else {
		w = coretest.World(cap, nil, nil)
	}
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return cap
}

// countCPUInstances returns the count of distinct dimension_InstanceId values in
// aws_ec2_cpuutilization_average across a capture, and the set of those IDs.
func countCPUInstances(cap *coretest.MetricCapture) (int, map[string]bool) {
	ids := map[string]bool{}
	for _, s := range cap.All() {
		if s.Name == "aws_ec2_cpuutilization_average" {
			id := s.Labels["dimension_InstanceId"]
			if id != "" {
				ids[id] = true
			}
		}
	}
	return len(ids), ids
}

// TestScaleUpDerivesMoreNodes asserts that when the live workload total crosses a node
// boundary (4 pods → 40 pods ≡ 5 nodes vs baseline 3), the per-instance CPU series
// count equals 5 and the InstanceIDs match fixture.DeriveNodes byte-exactly.
func TestScaleUpDerivesMoreNodes(t *testing.T) {
	// Baseline cluster: 1 workload "test-api" with 4 replicas → total=4 → max(3,1)=3 nodes.
	cl := clusterWithNodeGroups()
	cl.Workloads[0].Replicas = 4

	c, err := ec2.New(&ec2.Config{}, &fixture.Set{
		Env: coretest.Env(), Cloud: coretest.Cloud(), Cluster: cl,
	})
	if err != nil {
		t.Fatalf("ec2.New: %v", err)
	}

	// Scale the workload to 40 replicas → total=40 → max(3,5)=5 nodes.
	src := scale.New()
	src.Set(map[string]int{"test-api": 40})

	cap := tickAt(t, c, src)

	n, gotIDs := countCPUInstances(cap)
	if n != 5 {
		t.Errorf("scale-up: got %d per-instance cpuutilization_average series, want 5", n)
	}

	// InstanceIDs must match fixture.DeriveNodes with total=40.
	wantNodes := fixture.DeriveNodes(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, 40)
	if len(wantNodes) != 5 {
		t.Fatalf("fixture.DeriveNodes returned %d nodes, want 5 (test setup bug)", len(wantNodes))
	}
	for _, nd := range wantNodes {
		if !gotIDs[nd.InstanceID] {
			t.Errorf("scale-up: expected InstanceID %q from DeriveNodes absent in capture", nd.InstanceID)
		}
	}
}

// TestScaleDownRetiresInstances asserts that after ticking with a high node count and
// then lowering the scaling, the retired instances' series are absent from the next tick's
// capture (not frozen at their last value).
func TestScaleDownRetiresInstances(t *testing.T) {
	// Cluster: 1 workload "test-api" with 1 replica (baseline total=1 → 3 nodes).
	cl := clusterWithNodeGroups()
	cl.Workloads[0].Replicas = 1

	c, err := ec2.New(&ec2.Config{}, &fixture.Set{
		Env: coretest.Env(), Cloud: coretest.Cloud(), Cluster: cl,
	})
	if err != nil {
		t.Fatalf("ec2.New: %v", err)
	}

	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

	// First tick: scale up to 40 → 5 nodes; collect their IDs.
	src := scale.New()
	src.Set(map[string]int{"test-api": 40})
	cap1 := &coretest.MetricCapture{}
	w1 := worldWithScaling(cap1, src)
	if err := c.Tick(context.Background(), now, w1); err != nil {
		t.Fatalf("Tick 1 (high): %v", err)
	}
	n1, highIDs := countCPUInstances(cap1)
	if n1 != 5 {
		t.Fatalf("tick1: expected 5 nodes, got %d", n1)
	}

	// Second tick: scale back to 1 replica → 3 nodes; retired nodes must be absent.
	src.Set(map[string]int{"test-api": 1})
	cap2 := &coretest.MetricCapture{}
	w2 := worldWithScaling(cap2, src)
	if err := c.Tick(context.Background(), now, w2); err != nil {
		t.Fatalf("Tick 2 (low): %v", err)
	}
	n2, lowIDs := countCPUInstances(cap2)
	if n2 != 3 {
		t.Errorf("scale-down: got %d per-instance series, want 3", n2)
	}

	// Compute retired IDs: in high set but not in low set.
	retiredCount := 0
	for id := range highIDs {
		if !lowIDs[id] {
			retiredCount++
		}
	}
	if retiredCount == 0 {
		t.Error("scale-down: no instances were retired between tick1 and tick2; expected some")
	}

	// Confirm the retired IDs are truly absent (not just not in the low active set by chance).
	// Every ID in cap2 must be one of the low-scale DeriveNodes IDs.
	wantLow := fixture.DeriveNodes(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, 1)
	wantLowIDs := map[string]bool{}
	for _, nd := range wantLow {
		wantLowIDs[nd.InstanceID] = true
	}
	for id := range lowIDs {
		if !wantLowIDs[id] {
			t.Errorf("scale-down: unexpected InstanceID %q in cap2; should have been retired", id)
		}
	}
}

// TestDefaultScalingParity asserts that with no Scaling source (nil), the construct
// produces one per-instance CPU series for each node in the baseline cluster, and the
// InstanceIDs match DeriveNodes with the declared replica total.
func TestDefaultScalingParity(t *testing.T) {
	cl := clusterWithNodeGroups()
	// baseline: 2 replicas → total=2 → max(3,1)=3 nodes, matching coretest.Cluster().
	// (cl.Workloads[0].Replicas is already 2 from coretest.Cluster())
	c, err := ec2.New(&ec2.Config{}, &fixture.Set{
		Env: coretest.Env(), Cloud: coretest.Cloud(), Cluster: cl,
	})
	if err != nil {
		t.Fatalf("ec2.New: %v", err)
	}

	cap := tickAt(t, c, nil) // no Scaling → use declared defaults

	n, gotIDs := countCPUInstances(cap)
	wantNodes := fixture.DeriveNodes(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, cl.Workloads[0].Replicas)
	if n != len(wantNodes) {
		t.Errorf("parity: got %d per-instance series, want %d (= DeriveNodes count)", n, len(wantNodes))
	}
	for _, nd := range wantNodes {
		if !gotIDs[nd.InstanceID] {
			t.Errorf("parity: InstanceID %q from DeriveNodes absent in capture", nd.InstanceID)
		}
	}
	// Also confirm DeriveNodes byte-matches the manually-built coretest nodes (regression guard).
	coretestNodes := coretest.Cluster().Nodes
	for i, nd := range wantNodes {
		if i >= len(coretestNodes) {
			break
		}
		if nd.InstanceID != coretestNodes[i].InstanceID {
			t.Errorf("parity: DeriveNodes[%d].InstanceID=%q != coretest.Cluster().Nodes[%d].InstanceID=%q",
				i, nd.InstanceID, i, coretestNodes[i].InstanceID)
		}
	}
}

// ─── metadata ─────────────────────────────────────────────────────────────────

func TestKindAndSignals(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "ec2" {
		t.Errorf("Kind() = %q, want %q", c.Kind(), "ec2")
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals() = %v, want [Metrics]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval() = %v, want 60s", c.Interval())
	}
}
