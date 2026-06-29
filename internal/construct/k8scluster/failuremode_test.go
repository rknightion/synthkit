// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster_test

import (
	"testing"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// bigCluster builds a cluster with N >= 10 app pods so the intensity-fraction
// test has statistical room: at 0.2 we expect ~2 pods affected, not 0 and not all.
func bigCluster(t *testing.T) *fixture.Cluster {
	t.Helper()
	cl := coretest.Cluster()
	// Replace workloads with a set that gives 15 pods total across 3 workloads.
	// This ensures the intensity-fraction test has sufficient population.
	wlNames := []struct{ name, ns string }{
		{"svc-alpha", "apps"},
		{"svc-beta", "apps"},
		{"svc-gamma", "apps"},
	}
	replicas := 5
	cl.Workloads = nil
	for _, w := range wlNames {
		wl := fixture.Workload{
			Name:      w.name,
			Namespace: w.ns,
			Replicas:  replicas,
		}
		for p := 0; p < replicas; p++ {
			wl.PodNames = append(wl.PodNames, fixture.PodName(cl.Name, w.name, p))
			wl.NodeIdx = append(wl.NodeIdx, p%len(cl.Nodes))
		}
		cl.Workloads = append(cl.Workloads, wl)
	}
	// Ensure we have at least 10 pods.
	totalPods := 0
	for _, wl := range cl.Workloads {
		totalPods += wl.Replicas
	}
	if totalPods < 10 {
		t.Fatalf("bigCluster: only %d app-workload pods; need >= 10 for intensity test", totalPods)
	}
	return cl
}

// pendingPodCount counts pods whose kube_pod_status_phase{phase="Pending"} == 1.
func pendingPodCount(mc *coretest.MetricCapture) int {
	seen := map[string]bool{}
	for _, s := range mc.Find("kube_pod_status_phase") {
		if s.Labels["phase"] == "Pending" && s.Value == 1 {
			pod := s.Labels["pod"]
			if pod != "" {
				seen[pod] = true
			}
		}
	}
	return len(seen)
}

// runningPodCount counts pods whose kube_pod_status_phase{phase="Running"} == 1.
func runningPodCount(mc *coretest.MetricCapture) int {
	seen := map[string]bool{}
	for _, s := range mc.Find("kube_pod_status_phase") {
		if s.Labels["phase"] == "Running" && s.Value == 1 {
			pod := s.Labels["pod"]
			if pod != "" {
				seen[pod] = true
			}
		}
	}
	return len(seen)
}

// totalPodCount counts distinct pods that appear in kube_pod_status_phase at all.
func totalPodCount(mc *coretest.MetricCapture) int {
	seen := map[string]bool{}
	for _, s := range mc.Find("kube_pod_status_phase") {
		if pod := s.Labels["pod"]; pod != "" {
			seen[pod] = true
		}
	}
	return len(seen)
}

// TestFailureModeIntensity asserts that pod_crashloop at LOW intensity (0.2) affects
// only a FRACTION of pods — not all pods and not zero pods — while at intensity 1.0
// it affects all (or nearly all) pods. This is the key behavioural property that the
// intensity-fraction implementation must satisfy.
func TestFailureModeIntensity(t *testing.T) {
	cl := bigCluster(t)
	c := buildConstruct(t, cl)

	// ── Baseline (no failure) ─────────────────────────────────────────────────────
	mcBase := &coretest.MetricCapture{}
	tickWorld(t, c, liveWorld(mcBase, &coretest.LogCapture{}, nil, nil))
	totalPods := totalPodCount(mcBase)
	basePending := pendingPodCount(mcBase) // churn baseline (typically 0–1 for small cluster)
	t.Logf("baseline: totalPods=%d basePending=%d", totalPods, basePending)
	if totalPods < 10 {
		t.Fatalf("need >= 10 pods for intensity test, got %d", totalPods)
	}

	// ── Low intensity (0.2): expect > 0 AND < totalPods pods affected ─────────────
	cLow := buildConstruct(t, cl)
	mcLow := &coretest.MetricCapture{}
	liveLow := map[string][]shape.LiveFailure{
		"pod_crashloop": {{Enabled: true, Intensity: 0.2, Scope: cl.Name}},
	}
	tickWorld(t, cLow, liveWorld(mcLow, &coretest.LogCapture{}, liveLow, nil))

	pendingLow := pendingPodCount(mcLow)
	t.Logf("low-intensity (0.2): pendingPods=%d (out of %d total)", pendingLow, totalPods)

	// Must affect at least 1 pod (AT-LEAST-1 floor).
	if pendingLow <= basePending {
		t.Errorf("pod_crashloop intensity=0.2: pendingPods=%d not above baseline %d; expect at least 1 pod affected", pendingLow, basePending)
	}
	// Must NOT affect all pods — that would mean the implementation is still binary (all-or-nothing).
	if pendingLow >= totalPods {
		t.Errorf("pod_crashloop intensity=0.2: ALL %d pods are Pending — intensity is being ignored (should affect a fraction, not all)", pendingLow)
	}

	// ── High intensity (1.0): expect all (or nearly all) pods affected ─────────────
	cHigh := buildConstruct(t, cl)
	mcHigh := &coretest.MetricCapture{}
	liveHigh := map[string][]shape.LiveFailure{
		"pod_crashloop": {{Enabled: true, Intensity: 1.0, Scope: cl.Name}},
	}
	tickWorld(t, cHigh, liveWorld(mcHigh, &coretest.LogCapture{}, liveHigh, nil))

	pendingHigh := pendingPodCount(mcHigh)
	t.Logf("high-intensity (1.0): pendingPods=%d (out of %d total)", pendingHigh, totalPods)

	// At intensity 1.0 virtually all pods should be affected. Allow a tiny tolerance
	// (churn pods would already be Pending, but startup churn doesn't count against this).
	// Require at least 95% of pods are in Pending.
	minExpected := int(float64(totalPods) * 0.95)
	if pendingHigh < minExpected {
		t.Errorf("pod_crashloop intensity=1.0: only %d/%d pods Pending (want >= %d, i.e. >=95%%)", pendingHigh, totalPods, minExpected)
	}
}
