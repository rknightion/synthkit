// SPDX-License-Identifier: AGPL-3.0-only

// hostinfo_test.go — TDD tests for emitHostInfo (traces_host_info).
package k8scluster_test

import (
	"testing"

	"github.com/rknightion/synthkit/internal/core/coretest"
)

// hostInfoState runs emitHostInfo indirectly by building a construct with the feature
// gate toggled and collecting via a full Tick.
// We need to call emitHostInfo directly since it works on *state.State (unexported path);
// instead we test via the full tick and examine traces_host_info.

func TestHostInfoFeatureOn(t *testing.T) {
	cl := coretest.Cluster()
	// Enable application_observability feature.
	if cl.K8sMonitoring.Features == nil {
		cl.K8sMonitoring.Features = map[string]bool{}
	}
	cl.K8sMonitoring.Features["application_observability"] = true

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	series := findSeries(mc, "traces_host_info")
	if len(series) != len(cl.Nodes) {
		t.Errorf("traces_host_info: want %d series (one per node), got %d", len(cl.Nodes), len(series))
	}
	for _, s := range series {
		if s.Value != 1 {
			t.Errorf("traces_host_info: value=%v, want 1", s.Value)
		}
		if s.Labels["grafana_host_id"] == "" {
			t.Errorf("traces_host_info: grafana_host_id is empty (labels: %v)", s.Labels)
		}
		// Live reference (2026-06-15): traces_host_info carries ONLY grafana_host_id — no cluster label.
		if _, ok := s.Labels["k8s_cluster_name"]; ok {
			t.Errorf("traces_host_info: must NOT carry k8s_cluster_name (labels: %v)", s.Labels)
		}
	}

	// All grafana_host_id values must correspond to a fixture node InstanceID.
	nodeIDs := map[string]bool{}
	for _, n := range cl.Nodes {
		nodeIDs[n.InstanceID] = true
	}
	for _, s := range series {
		if !nodeIDs[s.Labels["grafana_host_id"]] {
			t.Errorf("traces_host_info: grafana_host_id=%q not in fixture node InstanceIDs %v", s.Labels["grafana_host_id"], nodeIDs)
		}
	}
}

func TestHostInfoFeatureOff(t *testing.T) {
	cl := coretest.Cluster()
	// Ensure feature is off (default nil map → missing → false).
	// Explicitly set to false to be sure.
	if cl.K8sMonitoring.Features == nil {
		cl.K8sMonitoring.Features = map[string]bool{}
	}
	cl.K8sMonitoring.Features["application_observability"] = false

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	series := findSeries(mc, "traces_host_info")
	if len(series) != 0 {
		t.Errorf("traces_host_info: want 0 series when feature is off, got %d", len(series))
	}
}

func TestHostInfoFeatureDefault(t *testing.T) {
	// coretest.Cluster() does not set Features map — default nil → feature off.
	cl := coretest.Cluster()
	// Defensive: coretest fixture may or may not set application_observability.
	// If it's not set (nil map), feature is off.
	if cl.K8sMonitoring.Features["application_observability"] {
		t.Skip("coretest.Cluster() has application_observability=true — skip default-off test")
	}

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	series := findSeries(mc, "traces_host_info")
	if len(series) != 0 {
		t.Errorf("traces_host_info: want 0 series when feature not set, got %d", len(series))
	}
}

// TestHostInfoDirectState tests emitHostInfo via state.State.Collect directly,
// using the internal package since this is package k8scluster_test (external).
// We exercise this via the build-construct + tick approach above; this test
// additionally verifies that the state accumulation (Set, not Add) is correct
// by running two ticks and asserting no doubling.
func TestHostInfoGaugeNoAccumulate(t *testing.T) {
	cl := coretest.Cluster()
	if cl.K8sMonitoring.Features == nil {
		cl.K8sMonitoring.Features = map[string]bool{}
	}
	cl.K8sMonitoring.Features["application_observability"] = true

	c := buildConstruct(t, cl)
	lc := &coretest.LogCapture{}

	mc1 := &coretest.MetricCapture{}
	tick(t, c, mc1, lc)
	v1 := map[string]float64{}
	for _, s := range findSeries(mc1, "traces_host_info") {
		v1[s.Labels["grafana_host_id"]] = s.Value
	}
	if len(v1) == 0 {
		t.Fatal("traces_host_info: no series in tick1")
	}

	mc2 := &coretest.MetricCapture{}
	tick(t, c, mc2, lc)
	for _, s := range findSeries(mc2, "traces_host_info") {
		prev, ok := v1[s.Labels["grafana_host_id"]]
		if !ok {
			continue
		}
		if s.Value > prev*1.5 {
			t.Errorf("traces_host_info: gauge appears to accumulate (tick1=%.2f tick2=%.2f) — must use Set not Add", prev, s.Value)
		}
	}
}

// ── Direct state.State test (package-internal emitHostInfo, tested via types) ────────────
// Since emitHostInfo is unexported, we cannot call it from k8scluster_test.
// We use the fixture.Cluster + state.State approach via an exported helper if available,
// otherwise rely on the tick-based tests above. The following demonstrates that even
// with a nil Features map (not just absent key) the function stays no-op.
func TestHostInfoNilFeaturesMap(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.Features = nil // explicitly nil map

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	if len(findSeries(mc, "traces_host_info")) != 0 {
		t.Error("traces_host_info: must be absent when Features map is nil")
	}
}
