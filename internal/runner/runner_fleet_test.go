// SPDX-License-Identifier: AGPL-3.0-only

package runner

// runner_fleet_test.go — FM k8s-monitoring collector-mirroring wiring tests.
//
// Separate file from runner_test.go (which a concurrent migration owns). Covers:
//   (a) the union roster: a blueprint with a standalone collectors_per_os instance AND a
//       cluster mirror instance registers both via the composed fleetProvider();
//   (b) dynamic growth: the mirror provider grows as the bp's *scale.Source scales nodes;
//   (c) coherence: the collector_id set the construct emits == the set the provider returns,
//       at baseline and after a scale change (the roster≡metrics invariant under churn).

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/fleet"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// mirrorCluster builds a cluster fixture with k8s_monitoring.fleet_management on, a single
// node group (Desired 0 ⇒ node count derives from live pod total), and a scalable workload.
func mirrorCluster(name string) *fixture.Cluster {
	cl := &fixture.Cluster{
		Name: name, Region: "us-west-2", Seed: "seed:" + name,
		NodeGroups: []fixture.NodeGroupSpec{{Name: "general", InstanceType: "m6i.xlarge"}},
		Workloads:  []fixture.Workload{{Name: "api", Replicas: 8}},
		K8sMonitoring: fixture.K8sMonitoring{
			Enabled: true, Alloy: true, AlloyVersion: "v1.10.0", FleetManagement: true,
			Features: map[string]bool{"pod_logs": true, "cluster_metrics": true, "cluster_events": true},
		},
	}
	cl.Nodes = fixture.DeriveNodes(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, 8)
	return cl
}

// TestFleetProviderUnionAndChurn proves the composed provider includes BOTH the standalone roster
// and the cluster mirror, and that the mirror grows as the blueprint's scale source scales nodes.
func TestFleetProviderUnionAndChurn(t *testing.T) {
	mc := &coretest.MetricCapture{}
	r := New(Sinks{Metrics: mc}, fleetRegistry(t), Options{})

	cl := mirrorCluster("fleetk8s-cluster")
	res := &blueprint.Resolved{
		Name: "fleetk8s", Label: "fleetk8s",
		Constructs: []blueprint.ConstructInstance{
			// Standalone (cluster-less) instance.
			{Kind: fleetmgmt.Kind, Name: "fleet-standalone", Config: &fleetmgmt.Config{CollectorsPerOS: map[string]int{"linux": 2}},
				Fixtures: &fixture.Set{Seed: "fleetk8s"}},
			// Cluster-path mirror instance (cluster fixture attached, as resolve.go emits).
			{Kind: fleetmgmt.Kind, Name: "fleetk8s-cluster/fleet", Config: &fleetmgmt.Config{},
				Fixtures: &fixture.Set{Seed: "fleetk8s:" + cl.Name, Cluster: cl}},
		},
	}
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	bp := r.bps[0]
	provider := bp.fleetProvider()
	if provider == nil {
		t.Fatal("expected a composed fleet provider")
	}

	base := provider()
	var hasStandalone, hasK8sLogs bool
	for _, c := range base {
		if strings.HasPrefix(c.ID, "fleet-linux-") {
			hasStandalone = true
		}
		if strings.Contains(c.ID, "-alloy-logs-ip-") {
			hasK8sLogs = true
		}
	}
	if !hasStandalone || !hasK8sLogs {
		t.Fatalf("union missing members: standalone=%v k8sLogs=%v ids=%v", hasStandalone, hasK8sLogs, idsOf(base))
	}

	// Scale the workload up → more nodes → more DaemonSet (alloy-logs) collectors.
	bp.scale.Set(map[string]int{"api": 200})
	scaled := provider()
	if len(scaled) <= len(base) {
		t.Fatalf("provider did not grow with scaling: base=%d scaled=%d", len(base), len(scaled))
	}
}

// TestFleetMirrorMetricsControllerCoherence proves the collector_id set the construct EMITS equals
// the set the runner's REAL mirror provider REGISTERS, at baseline and after scaling — the
// roster≡metrics invariant under churn. Critically it uses the runner's own bp.fleetProvider() and
// bp.scale (the same *scale.Source fed into World.Scaling), so a regression that wired a DIFFERENT
// scale source into the mirror closure would break this test.
func TestFleetMirrorMetricsControllerCoherence(t *testing.T) {
	mc := &coretest.MetricCapture{}
	r := New(Sinks{Metrics: mc}, fleetRegistry(t), Options{})

	cl := mirrorCluster("coherence")
	// Mirror-ONLY blueprint (no standalone instance) so the registered set == the emitted set exactly.
	res := &blueprint.Resolved{
		Name: "coherence", Label: "coherence",
		Constructs: []blueprint.ConstructInstance{
			{Kind: fleetmgmt.Kind, Name: cl.Name + "/fleet", Config: &fleetmgmt.Config{},
				Fixtures: &fixture.Set{Seed: "coherence:" + cl.Name, Cluster: cl}},
		},
	}
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	bp := r.bps[0]
	provider := bp.fleetProvider()
	if provider == nil {
		t.Fatal("expected a mirror provider")
	}

	// The construct reads the SAME bp.scale the provider closes over (via World.Scaling).
	cons, err := fleetmgmt.Build(&fleetmgmt.Config{}, &fixture.Set{Seed: "coherence:" + cl.Name, Cluster: cl})
	if err != nil {
		t.Fatal(err)
	}

	check := func(label string, tick int64) {
		w := &core.World{Scaling: bp.scale} // bp.scale — the runner's source, not a fresh one
		metricIDs := collectorIDsFromBatch(cons.(*fleetmgmt.Construct).BuildWith(time.Unix(tick, 0), w))
		provIDs := map[string]bool{}
		for _, c := range provider() {
			provIDs[c.ID] = true
		}
		if len(metricIDs) == 0 {
			t.Fatalf("%s: no metric ids emitted", label)
		}
		if !equalStringSet(metricIDs, provIDs) {
			t.Errorf("%s: metric ids != provider ids\n only-in-metrics=%v\n only-in-provider=%v",
				label, diffSet(metricIDs, provIDs), diffSet(provIDs, metricIDs))
		}
	}
	check("baseline", 0)
	bp.scale.Set(map[string]int{"api": 56}) // 7 nodes
	check("scaled-up", 60)
	bp.scale.Set(map[string]int{"api": 8}) // back to 3 nodes
	check("scaled-down", 120)
}

// --- local helpers (named to avoid clashing with runner_test.go) ---

func idsOf(cs []fleet.Collector) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func collectorIDsFromBatch(batch []promrw.Series) map[string]bool {
	ids := map[string]bool{}
	for _, s := range batch {
		if id, ok := s.Labels["collector_id"]; ok {
			ids[id] = true
		}
	}
	return ids
}

func equalStringSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func diffSet(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	return out
}
