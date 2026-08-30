// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/k8scluster"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

func buildPodChurnConstruct(t *testing.T, cl *fixture.Cluster, rate int) core.Construct {
	t.Helper()
	c, err := k8scluster.New(&k8scluster.Config{SeriesChurnPerMinute: rate}, &fixture.Set{
		Cluster: cl,
		Env:     cl.Env,
		Cloud:   cl.Cloud,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func podChurnCluster(method string) *fixture.Cluster {
	cl := coretest.Cluster()
	cl.K8sMonitoring.Features = map[string]bool{"pod_logs": true}
	cl.K8sMonitoring.PodLogsMethod = method
	return cl
}

func tickPodChurn(t *testing.T, c core.Construct, now time.Time) (*coretest.MetricCapture, *coretest.LogCapture, *otlpLogCapture) {
	t.Helper()
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	oc := &otlpLogCapture{}
	w := coretest.World(mc, lc, nil)
	w.OTLPLogs = oc
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick(%v): %v", now, err)
	}
	return mc, lc, oc
}

func appMetricPods(mc *coretest.MetricCapture, namespace string) map[string]bool {
	got := map[string]bool{}
	for _, s := range mc.Find("kube_pod_info") {
		if s.Labels["namespace"] == namespace {
			got[s.Labels["pod"]] = true
		}
	}
	return got
}

func appLokiPods(lc *coretest.LogCapture, namespace string) map[string]bool {
	got := map[string]bool{}
	for _, stream := range lc.Streams {
		if stream.Labels["namespace"] != namespace || stream.Labels["app_kubernetes_io_name"] == "" {
			continue
		}
		for _, line := range stream.Lines {
			if pod := line.Meta["pod"]; pod != "" {
				got[pod] = true
			}
		}
	}
	return got
}

func appOTLPLogPods(oc *otlpLogCapture, namespace string) map[string]bool {
	got := map[string]bool{}
	for _, resource := range oc.Resources {
		if resource.Attrs["k8s.namespace.name"] != namespace || resource.Attrs["k8s.deployment.name"] == nil {
			continue
		}
		if pod, ok := resource.Attrs["k8s.pod.name"].(string); ok {
			got[pod] = true
		}
	}
	return got
}

func difference(left, right map[string]bool) map[string]bool {
	out := map[string]bool{}
	for value := range left {
		if !right[value] {
			out[value] = true
		}
	}
	return out
}

func sortedPodSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for pod := range set {
		out = append(out, pod)
	}
	sort.Strings(out)
	return out
}

func TestPodLifecycleChurnRejectsNegativeRate(t *testing.T) {
	_, err := k8scluster.New(&k8scluster.Config{SeriesChurnPerMinute: -1}, &fixture.Set{Cluster: coretest.Cluster()})
	if err == nil {
		t.Fatal("negative series_churn_per_minute must be rejected")
	}
}

// TestPodLifecycleChurnRetiresAcrossSignals proves that one lifecycle boundary is coherent across
// the k8s substrate's metric, Loki-native, and OTLP-log projections. The old pod disappears from
// every projection in the same tick, exactly one replacement appears, and the active count stays
// bounded instead of accumulating generations.
func TestPodLifecycleChurnRetiresAcrossSignals(t *testing.T) {
	const (
		namespace = "test-api"
		rate      = 1
	)
	lokiC := buildPodChurnConstruct(t, podChurnCluster("loki"), rate)
	otlpC := buildPodChurnConstruct(t, podChurnCluster("opentelemetry"), rate)
	start := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	m0, l0, _ := tickPodChurn(t, lokiC, start)
	_, _, o0 := tickPodChurn(t, otlpC, start)
	m1, l1, _ := tickPodChurn(t, lokiC, start.Add(time.Minute))
	_, _, o1 := tickPodChurn(t, otlpC, start.Add(time.Minute))

	firstMetric := appMetricPods(m0, namespace)
	secondMetric := appMetricPods(m1, namespace)
	firstLoki := appLokiPods(l0, namespace)
	secondLoki := appLokiPods(l1, namespace)
	firstOTLP := appOTLPLogPods(o0, namespace)
	secondOTLP := appOTLPLogPods(o1, namespace)
	if len(firstMetric) != 2 || len(secondMetric) != 2 {
		t.Fatalf("metric active pod counts = %d then %d, want 2 then 2", len(firstMetric), len(secondMetric))
	}
	for name, got := range map[string]map[string]bool{
		"first Loki": firstLoki, "second Loki": secondLoki,
		"first OTLP": firstOTLP, "second OTLP": secondOTLP,
	} {
		if len(got) != 2 {
			t.Fatalf("%s active pod count = %d, want 2", name, len(got))
		}
	}
	for signal, got := range map[string]map[string]bool{
		"first Loki": firstLoki, "first OTLP": firstOTLP,
		"second Loki": secondLoki, "second OTLP": secondOTLP,
	} {
		want := firstMetric
		if signal[:6] == "second" {
			want = secondMetric
		}
		if len(difference(want, got)) != 0 || len(difference(got, want)) != 0 {
			t.Errorf("%s pod set = %v, want %v", signal, sortedPodSet(got), sortedPodSet(want))
		}
	}

	retired := difference(firstMetric, secondMetric)
	replaced := difference(secondMetric, firstMetric)
	if len(retired) != rate || len(replaced) != rate {
		t.Fatalf("one-minute turnover retired=%v replaced=%v, want %d each", retired, replaced, rate)
	}
	for _, series := range m1.All() {
		if series.Labels["namespace"] == namespace && retired[series.Labels["pod"]] {
			t.Errorf("retired pod %q still has metric %q: %v", series.Labels["pod"], series.Name, series.Labels)
		}
	}
}

// TestPodLifecycleChurnStaysBoundedAcrossMultipleBoundaries guards against retaining every
// generation in State. A new identity may appear at each minute, but the active set remains the
// declared replica count.
func TestPodLifecycleChurnStaysBoundedAcrossMultipleBoundaries(t *testing.T) {
	lokiC := buildPodChurnConstruct(t, podChurnCluster("loki"), 1)
	otlpC := buildPodChurnConstruct(t, podChurnCluster("opentelemetry"), 1)
	start := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	seen := map[string]bool{}
	for minute := 0; minute < 8; minute++ {
		mc, lc, _ := tickPodChurn(t, lokiC, start.Add(time.Duration(minute)*time.Minute))
		_, _, oc := tickPodChurn(t, otlpC, start.Add(time.Duration(minute)*time.Minute))
		metricPods := appMetricPods(mc, "test-api")
		if len(metricPods) != 2 || len(appLokiPods(lc, "test-api")) != 2 || len(appOTLPLogPods(oc, "test-api")) != 2 {
			t.Fatalf("minute %d active pod count exceeded bound: metric=%v loki=%v otlp=%v",
				minute, sortedPodSet(metricPods), sortedPodSet(appLokiPods(lc, "test-api")),
				sortedPodSet(appOTLPLogPods(oc, "test-api")))
		}
		for pod := range metricPods {
			seen[pod] = true
		}
	}
	if len(seen) <= 2 {
		t.Fatalf("churn produced no replacement identity across 8 minutes: %v", sortedPodSet(seen))
	}
}

// TestPodLifecycleChurnIsDeterministic verifies that the same cluster seed and tick schedule
// produce the same replacement names and UIDs in independent construct instances.
func TestPodLifecycleChurnIsDeterministic(t *testing.T) {
	start := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	left := buildPodChurnConstruct(t, coretest.Cluster(), 1)
	right := buildPodChurnConstruct(t, coretest.Cluster(), 1)
	for minute := 0; minute < 5; minute++ {
		lm, _, _ := tickPodChurn(t, left, start.Add(time.Duration(minute)*time.Minute))
		rm, _, _ := tickPodChurn(t, right, start.Add(time.Duration(minute)*time.Minute))
		lp := appMetricPods(lm, "test-api")
		rp := appMetricPods(rm, "test-api")
		if len(difference(lp, rp)) != 0 || len(difference(rp, lp)) != 0 {
			t.Fatalf("minute %d replacement sets differ: left=%v right=%v", minute, sortedPodSet(lp), sortedPodSet(rp))
		}
		leftUIDs := map[string]string{}
		for _, series := range lm.Find("kube_pod_info") {
			if series.Labels["namespace"] == "test-api" {
				leftUIDs[series.Labels["pod"]] = series.Labels["uid"]
			}
		}
		for _, series := range rm.Find("kube_pod_info") {
			if series.Labels["namespace"] == "test-api" && leftUIDs[series.Labels["pod"]] != series.Labels["uid"] {
				t.Errorf("minute %d pod %q UID differs: left=%q right=%q", minute, series.Labels["pod"], leftUIDs[series.Labels["pod"]], series.Labels["uid"])
			}
		}
	}
}

func TestStatefulSetLifecycleKeepsOrdinalAndVolumeButReplacesUID(t *testing.T) {
	cl := coretest.Cluster()
	cl.Workloads = []fixture.Workload{{
		Name: "orders", Namespace: "data", Controller: "statefulset", Replicas: 1,
		PodNames: []string{"orders-0"}, NodeIdx: []int{0}, VolumeClaims: []string{"data"},
	}}
	c := buildPodChurnConstruct(t, cl, 1)
	start := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	first, _, _ := tickPodChurn(t, c, start)
	second, _, _ := tickPodChurn(t, c, start.Add(time.Minute))

	uidFor := func(capture *coretest.MetricCapture) string {
		for _, series := range capture.Find("kube_pod_info") {
			if series.Labels["namespace"] == "data" && series.Labels["pod"] == "orders-0" {
				return series.Labels["uid"]
			}
		}
		return ""
	}
	firstUID, secondUID := uidFor(first), uidFor(second)
	if firstUID == "" || secondUID == "" || firstUID == secondUID {
		t.Fatalf("StatefulSet replacement UIDs = %q then %q, want two non-empty distinct values", firstUID, secondUID)
	}
	for _, series := range second.Find("kube_pod_info") {
		if series.Labels["namespace"] == "data" && series.Labels["pod"] != "orders-0" {
			t.Fatalf("StatefulSet ordinal name changed to %q", series.Labels["pod"])
		}
		if series.Labels["uid"] == firstUID {
			t.Fatalf("retired StatefulSet UID %q remained in second tick", firstUID)
		}
	}
	pvcSet := func(capture *coretest.MetricCapture) map[string]bool {
		out := map[string]bool{}
		for _, series := range capture.Find("kube_persistentvolumeclaim_info") {
			if series.Labels["namespace"] == "data" {
				out[series.Labels["persistentvolumeclaim"]] = true
			}
		}
		return out
	}
	if got, want := sortedPodSet(pvcSet(second)), sortedPodSet(pvcSet(first)); !equalStrings(got, want) {
		t.Fatalf("StatefulSet PVC identity changed: first=%v second=%v", want, got)
	}
}
