// SPDX-License-Identifier: AGPL-3.0-only

package dashgen

import (
	"testing"

	"github.com/rknightion/synthkit/dashboard"
)

// TestDeriveK8sFullStack proves Derive extracts the estate graph (cluster + workload topology)
// and harvests both blueprint-scoped and substrate metric scopes plus the trace surface, using the
// stable single-env k8s-full-stack blueprint (cluster k8sfull-prod-usw2 + web_service k8sfull-api).
func TestDeriveK8sFullStack(t *testing.T) {
	m, err := Derive("../../blueprints/k8s-full-stack.yaml")
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if m.Blueprint != "k8s-full-stack" || m.Label != "k8sfull" {
		t.Fatalf("blueprint/label = %q/%q", m.Blueprint, m.Label)
	}
	// topology: the declared cluster is present.
	clusters := map[string]bool{}
	for _, c := range m.Clusters {
		clusters[c.Name] = true
	}
	if !clusters["k8sfull-prod-usw2"] {
		t.Errorf("clusters = %+v, want k8sfull-prod-usw2", m.Clusters)
	}
	// workload wiring: the k8sfull-api web_service is derived and bound to the cluster.
	found := false
	for _, w := range m.Workloads {
		if w.Name != "k8sfull-api" {
			continue
		}
		found = true
		if w.Cluster != "k8sfull-prod-usw2" {
			t.Errorf("k8sfull-api cluster = %q, want k8sfull-prod-usw2", w.Cluster)
		}
	}
	if !found {
		t.Errorf("workload k8sfull-api not in manifest")
	}
	// signals: both a blueprint-scoped metric and a substrate metric are present
	var sawBlueprintScoped, sawSubstrate bool
	for _, s := range m.Metrics {
		if s.Scope == dashboard.ScopeBlueprint {
			sawBlueprintScoped = true
		} else {
			sawSubstrate = true
		}
	}
	if !sawBlueprintScoped || !sawSubstrate {
		t.Errorf("expected both blueprint-scoped and substrate metrics; bp=%v sub=%v (n=%d)", sawBlueprintScoped, sawSubstrate, len(m.Metrics))
	}
	// the warmup cycles MUST populate the trace surface (spans need minted traffic) —
	// this assertion is what proves warmupCycles/peakInstant actually land in-peak.
	if len(m.Spans) == 0 {
		t.Errorf("no span sources derived — warmupCycles may be landing off-peak (see peakInstant)")
	}
	// Backend spanmetrics now default OFF per-blueprint; Derive must opt the blueprint in for the
	// dry harvest so the manifest still carries the span-metrics families (dashboards reference them;
	// runtime data comes from synthkit-when-on or metrics-generator-when-off).
	metricNames := map[string]bool{}
	for _, s := range m.Metrics {
		metricNames[s.Name] = true
	}
	for _, want := range []string{
		"traces_spanmetrics_latency",
		"traces_spanmetrics_calls_total",
	} {
		if !metricNames[want] {
			t.Errorf("manifest missing spanmetrics family %q — Derive must opt the blueprint into span-metrics for the dry harvest", want)
		}
	}
}
