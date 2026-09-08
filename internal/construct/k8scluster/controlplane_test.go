// SPDX-License-Identifier: AGPL-3.0-only

// controlplane_test.go — tests for the emitApiServer / emitScheduler /
// emitControllerManager metric families (Lane I).
package k8scluster_test

import (
	"context"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/k8scluster"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// ── local helpers ────────────────────────────────────────────────────────────────────

// cpCluster returns a coretest.Cluster() with the requested ControlPlane flags set.
func cpCluster(apiserver, scheduler, controllerMgr bool) *fixture.Cluster {
	cl := coretest.Cluster()
	cl.K8sMonitoring.ControlPlane = fixture.ControlPlane{
		ApiServer:             apiserver,
		KubeScheduler:         scheduler,
		KubeControllerManager: controllerMgr,
	}
	return cl
}

// cpTick builds a construct from cl and runs one Tick; returns the metric capture.
func cpTick(t *testing.T, cl *fixture.Cluster) *coretest.MetricCapture {
	t.Helper()
	c, err := k8scluster.New(nil, &fixture.Set{Cluster: cl, Env: cl.Env, Cloud: cl.Cloud})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := coretest.World(mc, lc, nil)
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc
}

// ── apiserver unit tests ──────────────────────────────────────────────────────────────

// TestApiServerHeadlineFamilies asserts that the core apiserver families are present
// when ApiServer=true, and that they carry the correct job and cluster labels.
func TestApiServerHeadlineFamilies(t *testing.T) {
	mc := cpTick(t, cpCluster(true, false, false))

	for _, nm := range []string{
		"apiserver_request_total",
		"apiserver_request_duration_seconds_bucket",
		"apiserver_current_inflight_requests",
	} {
		if !hasSeries(mc, nm) {
			t.Errorf("apiserver: %q not emitted", nm)
		}
	}

	wantJob := "integrations/kubernetes/kube-apiserver"
	wantCluster := "test-prod-use1"

	for _, nm := range []string{"apiserver_request_total", "apiserver_current_inflight_requests"} {
		for _, s := range mc.Find(nm) {
			if s.Labels["job"] != wantJob {
				t.Errorf("%s: job=%q, want %q", nm, s.Labels["job"], wantJob)
				break
			}
			if s.Labels["cluster"] != wantCluster {
				t.Errorf("%s: cluster=%q, want %q", nm, s.Labels["cluster"], wantCluster)
				break
			}
		}
	}
}

// TestApiServerNoBlueprintLabel asserts no blueprint label on apiserver series (ScopeSubstrate).
func TestApiServerNoBlueprintLabel(t *testing.T) {
	mc := cpTick(t, cpCluster(true, false, false))
	for _, s := range mc.All() {
		if s.Labels["job"] == "integrations/kubernetes/kube-apiserver" {
			if _, ok := s.Labels["blueprint"]; ok {
				t.Errorf("apiserver series %q must NOT carry 'blueprint' label", s.Name)
			}
		}
	}
}

// TestApiServerInflightRequestKinds asserts both mutating and readOnly inflight gauges.
func TestApiServerInflightRequestKinds(t *testing.T) {
	mc := cpTick(t, cpCluster(true, false, false))
	kinds := distinctVals(mc, "apiserver_current_inflight_requests", "request_kind")
	for _, k := range []string{"mutating", "readOnly"} {
		if !kinds[k] {
			t.Errorf("apiserver_current_inflight_requests: request_kind=%q not found", k)
		}
	}
}

// TestApiServerEtcdHistogram asserts etcd_request_duration_seconds histogram is emitted.
func TestApiServerEtcdHistogram(t *testing.T) {
	mc := cpTick(t, cpCluster(true, false, false))
	if !hasSeries(mc, "etcd_request_duration_seconds_bucket") {
		t.Error("etcd_request_duration_seconds_bucket: not emitted")
	}
}

// TestApiServerEtcdObservedHistogramBounds pins the RKE2 2026-09 observed
// etcd-request buckets from the apiserver metrics endpoint.
func TestApiServerEtcdObservedHistogramBounds(t *testing.T) {
	mc := cpTick(t, cpCluster(true, false, false))
	want := []float64{.005, .025, .05, .1, .2, .4, .6, .8, 1, 1.25, 1.5, 2, 3, 4, 5, 6, 8, 10, 15, 20, 30, 45, 60}
	assertHistogramBounds(t, mc, "etcd_request_duration_seconds_bucket", want)
}

// ── scheduler unit tests ──────────────────────────────────────────────────────────────

// TestSchedulerHeadlineFamilies asserts core scheduler families are present when KubeScheduler=true.
func TestSchedulerHeadlineFamilies(t *testing.T) {
	mc := cpTick(t, cpCluster(false, true, false))

	for _, nm := range []string{
		"scheduler_pending_pods",
		"scheduler_scheduling_attempt_duration_seconds_bucket",
	} {
		if !hasSeries(mc, nm) {
			t.Errorf("scheduler: %q not emitted", nm)
		}
	}

	wantJob := "kube-scheduler"
	wantCluster := "test-prod-use1"

	for _, nm := range []string{"scheduler_pending_pods", "scheduler_schedule_attempts_total"} {
		for _, s := range mc.Find(nm) {
			if s.Labels["job"] != wantJob {
				t.Errorf("%s: job=%q, want %q", nm, s.Labels["job"], wantJob)
				break
			}
			if s.Labels["cluster"] != wantCluster {
				t.Errorf("%s: cluster=%q, want %q", nm, s.Labels["cluster"], wantCluster)
				break
			}
		}
	}
}

// TestSchedulerNoBlueprintLabel asserts no blueprint label on scheduler series.
func TestSchedulerNoBlueprintLabel(t *testing.T) {
	mc := cpTick(t, cpCluster(false, true, false))
	for _, s := range mc.All() {
		if s.Labels["job"] == "kube-scheduler" {
			if _, ok := s.Labels["blueprint"]; ok {
				t.Errorf("scheduler series %q must NOT carry 'blueprint' label", s.Name)
			}
		}
	}
}

// TestSchedulerPendingPodQueues asserts all four queue values are emitted.
func TestSchedulerPendingPodQueues(t *testing.T) {
	mc := cpTick(t, cpCluster(false, true, false))
	queues := distinctVals(mc, "scheduler_pending_pods", "queue")
	for _, q := range []string{"active", "backoff", "unschedulable", "gated"} {
		if !queues[q] {
			t.Errorf("scheduler_pending_pods: queue=%q not found", q)
		}
	}
}

// TestSchedulerObservedHistogramBounds pins the Rancher 2026-09 scheduler
// attempt-duration buckets. The capture established the emitter buckets before
// Grafana Cloud's read-path labels are applied.
func TestSchedulerObservedHistogramBounds(t *testing.T) {
	mc := cpTick(t, cpCluster(false, true, false))
	want := []float64{.001, .002, .004, .008, .016, .032, .064, .128, .256, .512, 1.024, 2.048, 4.096, 8.192, 16.384}
	assertHistogramBounds(t, mc, "scheduler_scheduling_attempt_duration_seconds_bucket", want)
}

// ── controller-manager unit tests ────────────────────────────────────────────────────

// TestControllerManagerWorkqueueDepth asserts workqueue_depth is emitted with the expected
// controller names when KubeControllerManager=true.
func TestControllerManagerWorkqueueDepth(t *testing.T) {
	mc := cpTick(t, cpCluster(false, false, true))

	if !hasSeries(mc, "workqueue_depth") {
		t.Fatal("workqueue_depth: not emitted")
	}

	// Must carry job=kube-controller-manager
	wantJob := "kube-controller-manager"
	wantCluster := "test-prod-use1"
	found := false
	for _, s := range mc.Find("workqueue_depth") {
		if s.Labels["job"] == wantJob {
			found = true
			if s.Labels["cluster"] != wantCluster {
				t.Errorf("workqueue_depth: cluster=%q, want %q", s.Labels["cluster"], wantCluster)
			}
			break
		}
	}
	if !found {
		t.Errorf("workqueue_depth: no series with job=%q", wantJob)
	}
}

// TestControllerManagerNoBlueprintLabel asserts no blueprint label on controller-manager series.
func TestControllerManagerNoBlueprintLabel(t *testing.T) {
	mc := cpTick(t, cpCluster(false, false, true))
	for _, s := range mc.All() {
		if s.Labels["job"] == "kube-controller-manager" {
			if _, ok := s.Labels["blueprint"]; ok {
				t.Errorf("controller-manager series %q must NOT carry 'blueprint' label", s.Name)
			}
		}
	}
}

// TestControllerManagerWorkqueueNames asserts multiple workqueue controller names are present.
func TestControllerManagerWorkqueueNames(t *testing.T) {
	mc := cpTick(t, cpCluster(false, false, true))
	// Filter to controller-manager workqueue_depth series
	names := map[string]bool{}
	for _, s := range mc.Find("workqueue_depth") {
		if s.Labels["job"] == "kube-controller-manager" {
			names[s.Labels["name"]] = true
		}
	}
	for _, want := range []string{"node", "replicaset", "daemonset", "deployment", "disruption"} {
		if !names[want] {
			t.Errorf("workqueue_depth{job=kube-controller-manager}: name=%q not found", want)
		}
	}
}

// TestControllerManagerObservedWorkqueueHistogramBounds pins the RKE2 2026-09
// controller-manager workqueue profile. Asserts labels are read-path
// enrichment and deliberately do not become emitter labels.
func TestControllerManagerObservedWorkqueueHistogramBounds(t *testing.T) {
	mc := cpTick(t, cpCluster(false, false, true))
	want := []float64{1e-8, 1e-7, 1e-6, 9.999999999999999e-6, 9.999999999999999e-5, .001, .01, .1, 1, 10}
	for _, name := range []string{
		"workqueue_queue_duration_seconds_bucket",
		"workqueue_work_duration_seconds_bucket",
	} {
		assertHistogramBounds(t, mc, name, want)
	}
}

// ── integration test: control_plane all-false gating ─────────────────────────────────

// TestControlPlaneGatingAllFalse builds a cluster with all ControlPlane flags false (the default)
// and asserts that NONE of the control-plane headline families appear after a full Tick. This
// proves the call-site gating in k8scluster.go works correctly.
func TestControlPlaneGatingAllFalse(t *testing.T) {
	// coretest.Cluster() has all ControlPlane flags zero (false).
	cl := coretest.Cluster()
	// Paranoia check: confirm they are indeed false.
	cp := cl.K8sMonitoring.ControlPlane
	if cp.ApiServer || cp.KubeScheduler || cp.KubeControllerManager {
		t.Fatal("test premise: coretest.Cluster() should have all ControlPlane flags false")
	}

	c, err := k8scluster.New(nil, &fixture.Set{Cluster: cl, Env: cl.Env, Cloud: cl.Cloud})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := coretest.World(mc, lc, nil)
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	for _, nm := range []string{
		"apiserver_request_total",
		"scheduler_pending_pods",
		"workqueue_depth", // could appear from any enabled component — must be absent when all false
	} {
		// workqueue_depth is shared across all three components; if all are disabled, none present.
		// We check the specific names that only come from the control plane emitters.
		if nm == "workqueue_depth" {
			// Check specifically for kube-controller-manager and kube-scheduler jobs.
			for _, s := range mc.Find(nm) {
				if s.Labels["job"] == "kube-controller-manager" || s.Labels["job"] == "kube-scheduler" {
					t.Errorf("ControlPlane all-false: %q with job=%q must be absent", nm, s.Labels["job"])
				}
			}
			continue
		}
		if hasSeries(mc, nm) {
			t.Errorf("ControlPlane all-false: %q must be absent, but series were emitted", nm)
		}
	}

	// Also check: apiserver-specific families absent.
	for _, nm := range []string{
		"apiserver_request_duration_seconds_bucket",
		"apiserver_current_inflight_requests",
		"etcd_request_duration_seconds_bucket",
		"scheduler_scheduling_attempt_duration_seconds_bucket",
		"scheduler_schedule_attempts_total",
	} {
		if hasSeries(mc, nm) {
			t.Errorf("ControlPlane all-false: %q must be absent", nm)
		}
	}
}

func assertHistogramBounds(t *testing.T, mc *coretest.MetricCapture, name string, want []float64) {
	t.Helper()
	type boundGroup struct {
		finite []float64
		sawInf bool
	}
	groups := make(map[string]*boundGroup)
	for _, series := range mc.Find(name) {
		le, ok := series.Labels["le"]
		if !ok {
			t.Fatalf("%s: bucket series missing le label", name)
		}
		sig := histogramLabelSig(series.Labels)
		group := groups[sig]
		if group == nil {
			group = &boundGroup{}
			groups[sig] = group
		}
		if le == "+Inf" {
			if group.sawInf {
				t.Fatalf("%s{%s}: duplicate +Inf bucket", name, sig)
			}
			group.sawInf = true
			continue
		}
		if group.sawInf {
			t.Fatalf("%s{%s}: finite bucket %q emitted after +Inf", name, sig, le)
		}
		bound, err := strconv.ParseFloat(le, 64)
		if err != nil {
			t.Fatalf("%s{%s}: invalid le=%q: %v", name, sig, le, err)
		}
		group.finite = append(group.finite, bound)
	}
	if len(groups) == 0 {
		t.Fatalf("%s: no bucket series found", name)
	}
	for sig, group := range groups {
		if !group.sawInf {
			t.Fatalf("%s{%s}: missing +Inf bucket", name, sig)
		}
		if len(group.finite) != len(want) {
			t.Fatalf("%s{%s}: bounds=%v, want %v", name, sig, group.finite, want)
		}
		for i := range want {
			if i > 0 && group.finite[i] <= group.finite[i-1] {
				t.Fatalf("%s{%s}: bounds are not strictly ascending: %v", name, sig, group.finite)
			}
			if group.finite[i] != want[i] {
				t.Fatalf("%s{%s}: bounds=%v, want %v", name, sig, group.finite, want)
			}
		}
	}
}

func histogramLabelSig(labels map[string]string) string {
	keys := make([]string, 0, len(labels)-1)
	for key := range labels {
		if key != "le" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var sig string
	for _, key := range keys {
		sig += key + "=" + labels[key] + ";"
	}
	return sig
}
