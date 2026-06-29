// SPDX-License-Identifier: AGPL-3.0-only

package alloyhealth_test

// alloyhealth_test.go — contract tests for the alloy_health construct.
//
// Covers:
//   (a) Exact metric inventory (all expected series present).
//   (b) synthkit_content_leak_test is ALWAYS exactly 0.0 in every tick.
//   (c) synthkit_content_dropped_total is monotonically non-decreasing (state.Add — I3).
//   (d) cluster label is present on every series (substrate identity disambiguator).
//   (e) No "blueprint" label ever (ScopeSubstrate — I21).
//   (f) alloy_build_info version is "v"-prefixed (I22/T10).
//   (g) alloy_build_info version reflects fx.Cluster.K8sMonitoring.AlloyVersion when set.
//   (h) fallback version "v1.16.3" when AlloyVersion is empty.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/alloyhealth"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// --- helpers ---------------------------------------------------------------

var testNow = time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)

func buildConstruct(t *testing.T, cluster *fixture.Cluster) (core.Construct, *coretest.MetricCapture) {
	t.Helper()
	cfg := &alloyhealth.Config{}
	fx := &fixture.Set{Cluster: cluster}
	c, err := alloyhealth.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return c, cap
}

func defaultCluster() *fixture.Cluster {
	return coretest.Cluster()
}

// --- tests -----------------------------------------------------------------

// TestInterfaceConformance verifies Kind/Signals/Interval match the spec.
func TestInterfaceConformance(t *testing.T) {
	cfg := &alloyhealth.Config{}
	fx := &fixture.Set{Cluster: defaultCluster()}
	c, err := alloyhealth.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Kind() != alloyhealth.Kind {
		t.Errorf("Kind()=%q want %q", c.Kind(), alloyhealth.Kind)
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals()=%v want [Metrics]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v want 60s", c.Interval())
	}
}

// TestNilClusterBuildError verifies Build returns an error when fx.Cluster is nil.
func TestNilClusterBuildError(t *testing.T) {
	_, err := alloyhealth.Build(&alloyhealth.Config{}, &fixture.Set{})
	if err == nil {
		t.Error("Build with nil Cluster must return error")
	}
}

// TestMetricInventory verifies the full set of expected metric name prefixes is present.
func TestMetricInventory(t *testing.T) {
	_, cap := buildConstruct(t, defaultCluster())
	names := cap.Names()
	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}

	// Required exact names or name-prefixes (histogram families appear as _bucket/_sum/_count).
	required := []string{
		"alloy_build_info",
		"up",
		"otelcol_receiver_accepted_spans_total",
		"otelcol_receiver_refused_spans_total",
		"otelcol_exporter_sent_spans_total",
		"otelcol_exporter_send_failed_spans_total",
		"otelcol_exporter_queue_size",
		"otelcol_processor_batch_batch_send_size_count",
		"otelcol_processor_batch_metadata_cardinality",
		"synthkit_content_dropped_total",
		"synthkit_content_leak_test",
	}
	for _, want := range required {
		if !nameSet[want] {
			t.Errorf("missing metric %q in inventory", want)
		}
	}
}

// TestLeakTestAlwaysZero verifies I23/T11: synthkit_content_leak_test is always 0.0.
func TestLeakTestAlwaysZero(t *testing.T) {
	cfg := &alloyhealth.Config{}
	fx := &fixture.Set{Cluster: defaultCluster()}
	c, err := alloyhealth.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Two ticks to confirm it stays 0.
	for tick, now := range []time.Time{testNow, testNow.Add(60 * time.Second)} {
		cap := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), now, coretest.World(cap, nil, nil)); err != nil {
			t.Fatalf("Tick %d: %v", tick, err)
		}
		series := cap.Find("synthkit_content_leak_test")
		if len(series) == 0 {
			t.Errorf("tick %d: synthkit_content_leak_test not emitted", tick)
			continue
		}
		for _, s := range series {
			if s.Value != 0.0 {
				t.Errorf("tick %d: synthkit_content_leak_test={%v} value=%v want 0.0 (I23/T11)",
					tick, s.Labels, s.Value)
			}
		}
	}
}

// TestDroppedTotalMonotone verifies I3: synthkit_content_dropped_total is non-decreasing.
func TestDroppedTotalMonotone(t *testing.T) {
	cfg := &alloyhealth.Config{}
	fx := &fixture.Set{Cluster: defaultCluster()}
	c, err := alloyhealth.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	cap1, cap2 := &coretest.MetricCapture{}, &coretest.MetricCapture{}
	t1 := testNow
	t2 := testNow.Add(60 * time.Second)
	if err := c.Tick(context.Background(), t1, coretest.World(cap1, nil, nil)); err != nil {
		t.Fatalf("Tick1: %v", err)
	}
	if err := c.Tick(context.Background(), t2, coretest.World(cap2, nil, nil)); err != nil {
		t.Fatalf("Tick2: %v", err)
	}

	// Build pipeline→value map for each tick.
	valMap := func(cap *coretest.MetricCapture) map[string]float64 {
		m := map[string]float64{}
		for _, s := range cap.Find("synthkit_content_dropped_total") {
			m[s.Labels["pipeline"]] = s.Value
		}
		return m
	}
	v1, v2 := valMap(cap1), valMap(cap2)

	if len(v1) == 0 {
		t.Fatal("synthkit_content_dropped_total not emitted in tick1")
	}
	for pipeline, val1 := range v1 {
		val2, ok := v2[pipeline]
		if !ok {
			t.Errorf("pipeline %q: missing in tick2", pipeline)
			continue
		}
		if val2 < val1 {
			t.Errorf("pipeline %q: tick2=%v < tick1=%v (must be monotone, I3)", pipeline, val2, val1)
		}
	}
}

// TestClusterLabelEverywhere verifies the cluster label is present on every emitted series
// (the substrate disambiguator — ARCHITECTURE §5).
func TestClusterLabelEverywhere(t *testing.T) {
	cl := defaultCluster()
	_, cap := buildConstruct(t, cl)
	for _, s := range cap.All() {
		got, ok := s.Labels["cluster"]
		if !ok {
			t.Errorf("series %q: missing 'cluster' label", s.Name)
			continue
		}
		if got != cl.Name {
			t.Errorf("series %q: cluster=%q want %q", s.Name, got, cl.Name)
		}
	}
}

// TestNoBlueprintLabel verifies ScopeSubstrate: no "blueprint" label on any series (I21).
func TestNoBlueprintLabel(t *testing.T) {
	_, cap := buildConstruct(t, defaultCluster())
	for _, s := range cap.All() {
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("series %q carries 'blueprint' label — ScopeSubstrate forbids it (I21)", s.Name)
		}
	}
}

// TestAlloyBuildInfoVersionPrefix verifies "v"-prefixed version on alloy_build_info (I22/T10).
func TestAlloyBuildInfoVersionPrefix(t *testing.T) {
	_, cap := buildConstruct(t, defaultCluster())
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

// TestAlloyVersionFromClusterFixture verifies that AlloyVersion from the cluster fixture
// is used verbatim when set.
func TestAlloyVersionFromClusterFixture(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.AlloyVersion = "v2.1.0"

	cfg := &alloyhealth.Config{}
	fx := &fixture.Set{Cluster: cl}
	c, err := alloyhealth.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cap := &coretest.MetricCapture{}
	if err := c.Tick(context.Background(), testNow, coretest.World(cap, nil, nil)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	for _, s := range cap.Find("alloy_build_info") {
		if s.Labels["version"] != "v2.1.0" {
			t.Errorf("alloy_build_info: version=%q want 'v2.1.0'", s.Labels["version"])
		}
	}
}

// TestAlloyVersionFallback verifies the "v1.16.3" default when AlloyVersion is empty.
func TestAlloyVersionFallback(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.AlloyVersion = "" // clear to test fallback

	cfg := &alloyhealth.Config{}
	fx := &fixture.Set{Cluster: cl}
	c, err := alloyhealth.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cap := &coretest.MetricCapture{}
	if err := c.Tick(context.Background(), testNow, coretest.World(cap, nil, nil)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	for _, s := range cap.Find("alloy_build_info") {
		if s.Labels["version"] != "v1.16.3" {
			t.Errorf("alloy_build_info: version=%q want 'v1.16.3' (fallback)", s.Labels["version"])
		}
	}
}

// TestReceiverLabelPresent verifies the `receiver` label on otelcol_receiver_* series.
func TestReceiverLabelPresent(t *testing.T) {
	_, cap := buildConstruct(t, defaultCluster())
	for _, name := range []string{
		"otelcol_receiver_accepted_spans_total",
		"otelcol_receiver_refused_spans_total",
	} {
		series := cap.Find(name)
		if len(series) == 0 {
			t.Errorf("metric %q not emitted", name)
			continue
		}
		for _, s := range series {
			if _, ok := s.Labels["receiver"]; !ok {
				t.Errorf("%q: missing 'receiver' label", name)
			}
		}
	}
}

// TestExporterLabelPresent verifies the `exporter` label on otelcol_exporter_* series.
func TestExporterLabelPresent(t *testing.T) {
	_, cap := buildConstruct(t, defaultCluster())
	for _, name := range []string{
		"otelcol_exporter_sent_spans_total",
		"otelcol_exporter_send_failed_spans_total",
		"otelcol_exporter_queue_size",
	} {
		series := cap.Find(name)
		if len(series) == 0 {
			t.Errorf("metric %q not emitted", name)
			continue
		}
		for _, s := range series {
			if _, ok := s.Labels["exporter"]; !ok {
				t.Errorf("%q: missing 'exporter' label", name)
			}
		}
	}
}

// TestPipelineLabelOnSentinel verifies `pipeline` label is present on both sentinel series.
func TestPipelineLabelOnSentinel(t *testing.T) {
	_, cap := buildConstruct(t, defaultCluster())
	for _, name := range []string{"synthkit_content_dropped_total", "synthkit_content_leak_test"} {
		series := cap.Find(name)
		if len(series) == 0 {
			t.Errorf("metric %q not emitted", name)
			continue
		}
		for _, s := range series {
			if _, ok := s.Labels["pipeline"]; !ok {
				t.Errorf("%q: missing 'pipeline' label", name)
			}
		}
	}
}
