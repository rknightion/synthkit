// SPDX-License-Identifier: AGPL-3.0-only

package fleetmgmt_test

// fleetmgmt_test.go — contract tests for the fleet_management construct.
//
// Covers:
//   (a) Collector count == Σ config.CollectorsPerOS values.
//   (b) Collector IDs are deterministic across two independent Build calls (same seed).
//   (c) Counter series are monotonically non-decreasing across ticks (I3).
//   (d) Label sets match the extract §2.4: job, namespace, cluster, k8s_cluster_name,
//       instance, collector_id, os, plus version on alloy_build_info.
//   (e) No "blueprint" label ever (ScopeSubstrate — I21).
//   (f) alloy_build_info carries a "v"-prefixed version (I22/T10).
//   (g) Histogram alloy_component_evaluation_seconds is present.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// --- helpers ---------------------------------------------------------------

func buildConstruct(t *testing.T, perOS map[string]int, seed string) (core.Construct, *coretest.MetricCapture) {
	t.Helper()
	cfg := &fleetmgmt.Config{CollectorsPerOS: perOS}
	fx := &fixture.Set{
		Seed:    seed,
		Cluster: coretest.Cluster(),
	}
	c, err := fleetmgmt.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	now := time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return c, cap
}

// collectorIDs returns the set of distinct collector_id label values seen across all
// alloy_build_info series (one per collector).
func collectorIDs(cap *coretest.MetricCapture) map[string]bool {
	ids := map[string]bool{}
	for _, s := range cap.Find("alloy_build_info") {
		if id, ok := s.Labels["collector_id"]; ok {
			ids[id] = true
		}
	}
	return ids
}

// --- tests -----------------------------------------------------------------

// TestCollectorCount verifies that the number of alloy_build_info series == Σ perOS.
func TestCollectorCount(t *testing.T) {
	perOS := map[string]int{"linux": 6, "darwin": 2}
	want := 6 + 2
	_, cap := buildConstruct(t, perOS, "test")
	got := len(collectorIDs(cap))
	if got != want {
		t.Errorf("alloy_build_info count=%d want %d", got, want)
	}
}

// TestCollectorCountMultiOS verifies count with windows.
func TestCollectorCountMultiOS(t *testing.T) {
	perOS := map[string]int{"linux": 3, "windows": 2, "darwin": 1}
	want := 6
	_, cap := buildConstruct(t, perOS, "test")
	got := len(collectorIDs(cap))
	if got != want {
		t.Errorf("alloy_build_info count=%d want %d", got, want)
	}
}

// TestCollectorIDsDeterministic verifies that two independent Build+Tick calls with the
// same seed produce exactly the same collector_id values.
func TestCollectorIDsDeterministic(t *testing.T) {
	perOS := map[string]int{"linux": 4, "darwin": 2}

	_, cap1 := buildConstruct(t, perOS, "seed-abc")
	_, cap2 := buildConstruct(t, perOS, "seed-abc")

	ids1 := collectorIDs(cap1)
	ids2 := collectorIDs(cap2)

	if len(ids1) != len(ids2) {
		t.Fatalf("ID count mismatch: %d vs %d", len(ids1), len(ids2))
	}
	for id := range ids1 {
		if !ids2[id] {
			t.Errorf("ID %q in first build but not second", id)
		}
	}
}

// TestCollectorIDsDifferentSeeds verifies that different seeds produce different IDs.
func TestCollectorIDsDifferentSeeds(t *testing.T) {
	perOS := map[string]int{"linux": 2}

	_, cap1 := buildConstruct(t, perOS, "seed-AAA")
	_, cap2 := buildConstruct(t, perOS, "seed-ZZZ")

	ids1 := collectorIDs(cap1)
	ids2 := collectorIDs(cap2)

	overlap := 0
	for id := range ids1 {
		if ids2[id] {
			overlap++
		}
	}
	if overlap == len(ids1) {
		t.Error("different seeds produced identical collector IDs — determinism seed is not wired")
	}
}

// TestCountersMonotone verifies I3: cumulative counter series are non-decreasing across
// two ticks.
func TestCountersMonotone(t *testing.T) {
	perOS := map[string]int{"linux": 2}
	cfg := &fleetmgmt.Config{CollectorsPerOS: perOS}
	fx := &fixture.Set{Seed: "test", Cluster: coretest.Cluster()}
	c, err := fleetmgmt.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	cap1, cap2 := &coretest.MetricCapture{}, &coretest.MetricCapture{}
	t1 := time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)
	t2 := t1.Add(60 * time.Second)

	if err := c.Tick(context.Background(), t1, coretest.World(cap1, nil, nil)); err != nil {
		t.Fatalf("Tick1: %v", err)
	}
	if err := c.Tick(context.Background(), t2, coretest.World(cap2, nil, nil)); err != nil {
		t.Fatalf("Tick2: %v", err)
	}

	counters := []string{
		"alloy_resources_process_cpu_seconds_total",
		"alloy_resources_machine_rx_bytes_total",
		"alloy_resources_machine_tx_bytes_total",
	}

	// Build value maps keyed by series identity (name+collector_id) for each tick.
	valMap := func(cap *coretest.MetricCapture, name string) map[string]float64 {
		m := map[string]float64{}
		for _, s := range cap.Find(name) {
			m[s.Labels["collector_id"]] = s.Value
		}
		return m
	}

	for _, name := range counters {
		v1 := valMap(cap1, name)
		v2 := valMap(cap2, name)
		if len(v1) == 0 {
			t.Errorf("counter %q: no series in tick1", name)
			continue
		}
		for id, val1 := range v1 {
			val2, ok := v2[id]
			if !ok {
				t.Errorf("counter %q: id %q missing in tick2", name, id)
				continue
			}
			if val2 < val1 {
				t.Errorf("counter %q id=%q: tick2 value %v < tick1 value %v (must be monotone, I3)",
					name, id, val2, val1)
			}
		}
	}
}

// TestLabelSet verifies the required base label set on every emitted series.
func TestLabelSet(t *testing.T) {
	perOS := map[string]int{"linux": 2}
	_, cap := buildConstruct(t, perOS, "test")

	requiredBase := []string{
		"job", "namespace", "cluster", "k8s_cluster_name",
		"instance", "collector_id", "os",
	}

	for _, s := range cap.All() {
		// histogram bucket series carry an extra le label — skip checking le in required
		for _, k := range requiredBase {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("series %q: missing required label %q", s.Name, k)
			}
		}
	}
}

// TestAlloyBuildInfoVersionPrefix verifies that alloy_build_info.version starts with "v"
// (I22 / T10 in the extract — k8s-monitoring filters alloy_build_info{version=~"v.+"}).
func TestAlloyBuildInfoVersionPrefix(t *testing.T) {
	_, cap := buildConstruct(t, map[string]int{"linux": 1}, "test")
	series := cap.Find("alloy_build_info")
	if len(series) == 0 {
		t.Fatal("alloy_build_info not emitted")
	}
	for _, s := range series {
		ver, ok := s.Labels["version"]
		if !ok {
			t.Errorf("alloy_build_info: missing version label")
			continue
		}
		if !strings.HasPrefix(ver, "v") {
			t.Errorf("alloy_build_info: version=%q must start with 'v'", ver)
		}
	}
}

// TestNoBlueprintLabel verifies ScopeSubstrate: no "blueprint" label on any emitted
// series (I21 — substrate families never carry the blueprint label).
func TestNoBlueprintLabel(t *testing.T) {
	_, cap := buildConstruct(t, map[string]int{"linux": 3, "darwin": 1}, "test")
	for _, s := range cap.All() {
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("series %q carries 'blueprint' label — ScopeSubstrate forbids it (I21)", s.Name)
		}
	}
}

// TestEvaluationHistogramPresent verifies alloy_component_evaluation_seconds buckets
// are emitted (the histogram drives rate + quantile panels in the Collector dashboard).
func TestEvaluationHistogramPresent(t *testing.T) {
	_, cap := buildConstruct(t, map[string]int{"linux": 1}, "test")
	found := false
	for _, s := range cap.All() {
		if strings.HasPrefix(s.Name, "alloy_component_evaluation_seconds") {
			found = true
			break
		}
	}
	if !found {
		t.Error("alloy_component_evaluation_seconds histogram not emitted")
	}
}

// TestJobAndNamespaceLabels verifies the Alloy self-metric label contract.
func TestJobAndNamespaceLabels(t *testing.T) {
	_, cap := buildConstruct(t, map[string]int{"linux": 2}, "test")
	for _, s := range cap.All() {
		// Histogram bucket series have job/namespace from base labels too.
		if s.Labels["job"] != "" && s.Labels["job"] != "integrations/alloy" {
			t.Errorf("series %q: job=%q want 'integrations/alloy'", s.Name, s.Labels["job"])
		}
		if s.Labels["namespace"] != "" && s.Labels["namespace"] != "infra" {
			t.Errorf("series %q: namespace=%q want 'infra'", s.Name, s.Labels["namespace"])
		}
	}
}

// TestInterfaceConformance verifies Kind/Signals/Interval match the spec.
func TestInterfaceConformance(t *testing.T) {
	cfg := &fleetmgmt.Config{CollectorsPerOS: map[string]int{"linux": 1}}
	fx := &fixture.Set{Seed: "test", Cluster: coretest.Cluster()}
	c, err := fleetmgmt.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Kind() != fleetmgmt.Kind {
		t.Errorf("Kind()=%q want %q", c.Kind(), fleetmgmt.Kind)
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals()=%v want [Metrics]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v want 60s", c.Interval())
	}
}

// TestNilClusterOmitsClusterLabels verifies the standalone-fleet contract: with no
// cluster fixture the construct builds fine and the cluster/k8s_cluster_name labels
// are OMITTED entirely (I13 — never a sentinel).
func TestNilClusterOmitsClusterLabels(t *testing.T) {
	cfg := &fleetmgmt.Config{CollectorsPerOS: map[string]int{"linux": 1}}
	c, err := fleetmgmt.Build(cfg, &fixture.Set{Seed: "test"})
	if err != nil {
		t.Fatalf("Build with nil Cluster must succeed (standalone fleet): %v", err)
	}
	mc := &coretest.MetricCapture{}
	if err := c.Tick(context.Background(), time.Now(), coretest.World(mc, nil, nil)); err != nil {
		t.Fatal(err)
	}
	if len(mc.All()) == 0 {
		t.Fatal("no series emitted")
	}
	for _, s := range mc.All() {
		if _, has := s.Labels["cluster"]; has {
			t.Fatalf("cluster label present on standalone-fleet series: %v", s.Labels)
		}
		if _, has := s.Labels["k8s_cluster_name"]; has {
			t.Fatalf("k8s_cluster_name present on standalone-fleet series: %v", s.Labels)
		}
	}
}

// TestEmptyCollectorsBuildError verifies Build rejects an empty CollectorsPerOS map.
func TestEmptyCollectorsBuildError(t *testing.T) {
	cfg := &fleetmgmt.Config{CollectorsPerOS: map[string]int{}}
	fx := &fixture.Set{Seed: "test", Cluster: coretest.Cluster()}
	_, err := fleetmgmt.Build(cfg, fx)
	if err == nil {
		t.Error("Build with empty CollectorsPerOS must return error")
	}
}

// idsFromBatch collects distinct collector_id label values from a raw series batch. (The existing
// collectorIDs helper takes a *coretest.MetricCapture and cannot be used on a []promrw.Series.)
func idsFromBatch(batch []promrw.Series) map[string]bool {
	ids := map[string]bool{}
	for _, s := range batch {
		if id, ok := s.Labels["collector_id"]; ok {
			ids[id] = true
		}
	}
	return ids
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestConstructMirrorsK8sCollectors(t *testing.T) {
	// A cluster-path mirror instance: EMPTY config + a cluster fixture whose k8s_monitoring has
	// FleetManagement:true. Mirror mode is DERIVED from the fixture (DD6b) — there is no config flag.
	fx := &fixture.Set{
		Seed: "seedM",
		Cluster: &fixture.Cluster{
			Name:   "demo-prod-usw2",
			Region: "us-west-2",
			Seed:   "seedM:demo-prod-usw2",
			Nodes: []fixture.Node{
				{Hostname: "ip-10-0-1-23.us-west-2.compute.internal"},
				{Hostname: "ip-10-0-1-24.us-west-2.compute.internal"},
			},
			K8sMonitoring: fixture.K8sMonitoring{
				Enabled: true, Alloy: true, AlloyVersion: "v1.10.0", FleetManagement: true,
				Features: map[string]bool{"pod_logs": true, "cluster_metrics": true},
			},
		},
	}
	c, err := fleetmgmt.Build(&fleetmgmt.Config{}, fx) // empty config; cluster fixture drives mirror mode
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	batch := c.(*fleetmgmt.Construct).BuildWith(time.Unix(0, 0), nil)
	ids := idsFromBatch(batch)

	// k8s daemonset collectors present (2 nodes → 2 alloy-logs).
	wantLogs := "grafana-k8s-monitoring-demo-prod-usw2-monitoring-alloy-logs-ip-10-0-1-23.us-west-2.compute.internal"
	if !ids[wantLogs] {
		t.Errorf("k8s alloy-logs collector missing: %v", keysOf(ids))
	}
	// statefulset metrics collector present.
	if !ids["grafana-k8s-monitoring-demo-prod-usw2-monitoring-grafana-k8s-monitoring-alloy-metrics-0"] {
		t.Errorf("k8s alloy-metrics collector missing: %v", keysOf(ids))
	}
	// alloy_build_info for a k8s collector carries the cluster's version verbatim.
	for _, s := range batch {
		if s.Name == "alloy_build_info" && s.Labels["collector_id"] == wantLogs {
			if s.Labels["version"] != "v1.10.0" {
				t.Errorf("k8s alloy_build_info version = %q, want v1.10.0", s.Labels["version"])
			}
			if s.Labels["workload_type"] != "daemonset" {
				t.Errorf("k8s alloy_build_info workload_type = %q, want daemonset", s.Labels["workload_type"])
			}
		}
	}
}

func TestConstructStandaloneStillWorks(t *testing.T) {
	// A feature-path standalone instance: collectors_per_os + a CLUSTER-LESS fixture ⇒ no mirror.
	c, err := fleetmgmt.Build(&fleetmgmt.Config{CollectorsPerOS: map[string]int{"linux": 1}}, &fixture.Set{Seed: "s"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ids := idsFromBatch(c.(*fleetmgmt.Construct).BuildWith(time.Unix(0, 0), nil))
	found := false
	for id := range ids {
		if strings.HasPrefix(id, "fleet-linux-00-") {
			found = true
		}
	}
	if !found {
		t.Errorf("standalone collector missing: %v", keysOf(ids))
	}
}
