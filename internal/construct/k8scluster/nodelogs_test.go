// SPDX-License-Identifier: AGPL-3.0-only

// nodelogs_test.go — TDD for emitNodeLogs / buildNodeLogStreams.
package k8scluster_test

import (
	"testing"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/sink/loki"
)

// nodeLogStreams ticks a cluster with node_logs feature set to featureOn and returns streams.
func nodeLogStreams(t *testing.T, featureOn bool, osid string) []loki.Stream {
	t.Helper()
	cl := coretest.Cluster()
	cl.K8sMonitoring.Features = map[string]bool{
		"node_logs": featureOn,
	}
	cl.Platform.OSID = osid
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)
	return lc.Streams
}

// journalStreams filters lc.Streams to only those with job=="integrations/kubernetes/journal".
func journalStreams(streams []loki.Stream) []loki.Stream {
	var out []loki.Stream
	for _, s := range streams {
		if s.Labels["job"] == "integrations/kubernetes/journal" {
			out = append(out, s)
		}
	}
	return out
}

// ── Lane F: node logs ─────────────────────────────────────────────────────────────────

// TestNodeLogsBottlerocketStreams verifies bottlerocket platform produces journal streams
// with the required label shape.
func TestNodeLogsBottlerocketStreams(t *testing.T) {
	streams := journalStreams(nodeLogStreams(t, true, "bottlerocket"))

	if len(streams) == 0 {
		t.Fatal("node_logs bottlerocket: no journal streams emitted")
	}

	for _, s := range streams {
		// Required labels.
		if s.Labels["job"] != "integrations/kubernetes/journal" {
			t.Errorf("node log stream job=%q, want integrations/kubernetes/journal", s.Labels["job"])
		}
		if s.Labels["source"] != "journal" {
			t.Errorf("node log stream source=%q, want journal", s.Labels["source"])
		}
		// instance (node hostname) must be non-empty.
		if s.Labels["instance"] == "" {
			t.Errorf("node log stream instance is empty (labels: %v)", s.Labels)
		}
		// service_name must equal unit.
		if s.Labels["service_name"] != s.Labels["unit"] {
			t.Errorf("node log stream service_name=%q, unit=%q (must match)", s.Labels["service_name"], s.Labels["unit"])
		}
		// unit must be non-empty.
		if s.Labels["unit"] == "" {
			t.Errorf("node log stream unit is empty (labels: %v)", s.Labels)
		}
		// level must be uppercase.
		lvl := s.Labels["level"]
		if lvl != "INFO" && lvl != "UNKNOWN" && lvl != "WARN" && lvl != "ERROR" && lvl != "DEBUG" {
			t.Errorf("node log stream level=%q, want uppercase (INFO/UNKNOWN/...)", lvl)
		}
		// Must have lines.
		if len(s.Lines) == 0 {
			t.Errorf("node log stream has no lines (labels: %v)", s.Labels)
		}
	}
}

// TestNodeLogsBottlerocketUnits verifies bottlerocket produces the expected journal units.
func TestNodeLogsBottlerocketUnits(t *testing.T) {
	streams := journalStreams(nodeLogStreams(t, true, "bottlerocket"))

	units := map[string]bool{}
	for _, s := range streams {
		units[s.Labels["unit"]] = true
	}

	// Bottlerocket must have host-containers@control.service and init.scope.
	for _, want := range []string{"host-containers@control.service", "init.scope"} {
		if !units[want] {
			t.Errorf("bottlerocket node logs: unit=%q not found (got %v)", want, units)
		}
	}
}

// TestNodeLogsDefaultOSUnits verifies that a non-bottlerocket OS (default) emits kubelet and containerd.
func TestNodeLogsDefaultOSUnits(t *testing.T) {
	streams := journalStreams(nodeLogStreams(t, true, "amzn"))

	units := map[string]bool{}
	for _, s := range streams {
		units[s.Labels["unit"]] = true
	}

	for _, want := range []string{"kubelet.service", "containerd.service"} {
		if !units[want] {
			t.Errorf("default OS node logs: unit=%q not found (got %v)", want, units)
		}
	}
}

// TestNodeLogsFeatureOff verifies no journal streams when node_logs is off.
func TestNodeLogsFeatureOff(t *testing.T) {
	streams := journalStreams(nodeLogStreams(t, false, "bottlerocket"))

	if len(streams) != 0 {
		t.Errorf("node_logs feature off: expected zero journal streams, got %d", len(streams))
	}
}

// TestNodeLogsPerNodePerUnit verifies each node emits one stream per journal unit.
func TestNodeLogsPerNodePerUnit(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.Features = map[string]bool{"node_logs": true}
	cl.Platform.OSID = "bottlerocket"
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	streams := journalStreams(lc.Streams)

	// Expect at least len(nodes) × units streams.
	// Bottlerocket has 2 units, coretest.Cluster() has 3 nodes → at least 6 streams.
	if len(streams) < len(cl.Nodes)*2 {
		t.Errorf("node logs: want at least %d streams (%d nodes × 2 units), got %d",
			len(cl.Nodes)*2, len(cl.Nodes), len(streams))
	}

	// Each distinct instance must appear.
	instances := map[string]bool{}
	for _, s := range streams {
		instances[s.Labels["instance"]] = true
	}
	if len(instances) < len(cl.Nodes) {
		t.Errorf("node logs: want %d distinct instances, got %d: %v", len(cl.Nodes), len(instances), instances)
	}
}

// TestNodeLogsClusterLabels verifies cluster and k8s_cluster_name on journal streams.
func TestNodeLogsClusterLabels(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.Features = map[string]bool{"node_logs": true}
	cl.Platform.OSID = "bottlerocket"
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	for _, s := range journalStreams(lc.Streams) {
		if got := s.Labels["cluster"]; got != cl.Name {
			t.Errorf("node log cluster=%q, want %q", got, cl.Name)
		}
		if got := s.Labels["k8s_cluster_name"]; got != cl.Name {
			t.Errorf("node log k8s_cluster_name=%q, want %q", got, cl.Name)
		}
	}
}

// TestNodeLogsNoBlueprintLabel verifies no blueprint label on journal streams (ScopeSubstrate).
func TestNodeLogsNoBlueprintLabel(t *testing.T) {
	streams := journalStreams(nodeLogStreams(t, true, "bottlerocket"))
	for _, s := range streams {
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("node log stream must NOT carry blueprint label: %v", s.Labels)
		}
	}
}
