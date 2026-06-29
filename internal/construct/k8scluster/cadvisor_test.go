// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster_test

import (
	"testing"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// usageRatioCluster places two app workloads whose deterministic utilization targets differ
// sharply: "hotsvc" (util 0.75 of its request) and "coldsvc" (util 0.15). After one tick the
// cAdvisor CPU usage of the hot workload should be a HIGH fraction of its request (so an efficiency
// dashboard would NOT flag it as over-requested), while the cold one is a LOW fraction (would be
// flagged) — i.e. the usage-vs-request ratio is no longer uniform across the estate.
func usageRatioCluster(t *testing.T) *fixture.Cluster {
	t.Helper()
	cl := coretest.Cluster()
	cl.Workloads = nil // drop default test-api; pin exactly the two we reason about
	add := func(name string) {
		wl := fixture.Workload{Name: name, Namespace: name, Replicas: 1}
		wl.PodNames = []string{fixture.PodName("test", name, 0)}
		wl.NodeIdx = []int{0}
		cl.Workloads = append(cl.Workloads, wl)
	}
	add("hotsvc")  // util 0.75
	add("coldsvc") // util 0.15
	return cl
}

func cpuUsageByContainer(mc *coretest.MetricCapture) map[string]float64 {
	out := map[string]float64{}
	for _, s := range mc.Find("container_cpu_usage_seconds_total") {
		if s.Labels["cpu"] != "total" {
			continue
		}
		out[s.Labels["container"]] = s.Value
	}
	return out
}

func cpuRequestByContainer(mc *coretest.MetricCapture) map[string]float64 {
	out := map[string]float64{}
	for _, s := range mc.Find("kube_pod_container_resource_requests") {
		if s.Labels["resource"] != "cpu" {
			continue
		}
		out[s.Labels["container"]] = s.Value
	}
	return out
}

func TestCAdvisorUsageVsRequestVaries(t *testing.T) {
	cl := usageRatioCluster(t)
	mc := captureTick(t, cl)

	usage := cpuUsageByContainer(mc)
	req := cpuRequestByContainer(mc)

	for _, c := range []string{"hotsvc", "coldsvc"} {
		if usage[c] == 0 {
			t.Fatalf("no cAdvisor CPU usage for %q", c)
		}
		if req[c] == 0 {
			t.Fatalf("no CPU request for %q", c)
		}
	}

	hotRatio := usage["hotsvc"] / req["hotsvc"]
	coldRatio := usage["coldsvc"] / req["coldsvc"]

	// The whole point: the usage-vs-request ratio is NO LONGER uniform. The hot workload sits at a
	// substantially higher fraction of its request than the cold one.
	if !(hotRatio > coldRatio) {
		t.Errorf("usage-vs-request ratio is uniform: hot=%g cold=%g (want hot > cold)", hotRatio, coldRatio)
	}
	// Sanity: with util 0.75 vs 0.15 the hot ratio should be markedly higher (>2x) than cold.
	if hotRatio < 2*coldRatio {
		t.Errorf("hot ratio %g not markedly above cold ratio %g (expected ~5x from 0.75 vs 0.15 util)", hotRatio, coldRatio)
	}
}
