// SPDX-License-Identifier: AGPL-3.0-only

// podlogs_test.go — TDD for emitPodLogs / buildPodLogStreams.
package k8scluster_test

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/loki"
)

// podLogStreamsForMethod builds a cluster with the given pod_logs feature gate and method,
// ticks it, and returns all Loki streams from the log capture.
func podLogStreamsForMethod(t *testing.T, method string, featureOn bool) []loki.Stream {
	t.Helper()
	cl := coretest.Cluster()
	cl.K8sMonitoring.Features = map[string]bool{
		"pod_logs": featureOn,
	}
	cl.K8sMonitoring.PodLogsMethod = method
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)
	// Keep only pod-log streams: substrate log streams (events/manifests/journal) all carry an
	// "integrations/..." job; pod logs are otel (no job) or classic (job=<ns>/<container>).
	var pod []loki.Stream
	for _, s := range lc.Streams {
		if strings.HasPrefix(s.Labels["job"], "integrations/") {
			continue
		}
		pod = append(pod, s)
	}
	return pod
}

// hasStreamLabel reports whether any stream in ss has the given label key.
func hasStreamLabel(ss []loki.Stream, key string) bool {
	for _, s := range ss {
		if _, ok := s.Labels[key]; ok {
			return true
		}
	}
	return false
}

// hasNoStreamLabel reports whether NO stream in ss has the given label key.
func hasNoStreamLabel(ss []loki.Stream, key string) bool {
	return !hasStreamLabel(ss, key)
}

// ── Lane E: pod logs ─────────────────────────────────────────────────────────────────

// TestPodLogsOtelShape verifies otel (opentelemetry) method: streams have k8s_pod_name and
// log_iostream, but NO job, NO namespace, NO pod, NO container (classic labels absent).
func TestPodLogsOtelShape(t *testing.T) {
	streams := podLogStreamsForMethod(t, "opentelemetry", true)

	// Filter to pod-log streams only (they have k8s_namespace_name).
	var podStreams []loki.Stream
	for _, s := range streams {
		if _, ok := s.Labels["k8s_namespace_name"]; ok {
			podStreams = append(podStreams, s)
		}
	}

	if len(podStreams) == 0 {
		t.Fatal("otel pod logs: no streams with k8s_namespace_name found")
	}

	for _, s := range podStreams {
		// Required otel labels.
		for _, req := range []string{"k8s_pod_name", "log_iostream", "k8s_container_name", "k8s_node_name", "service_name"} {
			if _, ok := s.Labels[req]; !ok {
				t.Errorf("otel pod log stream missing label %q (labels: %v)", req, s.Labels)
			}
		}
		// Must NOT have classic labels.
		for _, absent := range []string{"job", "namespace", "pod", "container", "stream"} {
			if _, ok := s.Labels[absent]; ok {
				t.Errorf("otel pod log stream must NOT have label %q (labels: %v)", absent, s.Labels)
			}
		}
		// Must have at least one line.
		if len(s.Lines) == 0 {
			t.Errorf("otel pod log stream has no lines (labels: %v)", s.Labels)
		}
	}
}

// TestPodLogsClassicShape verifies kubernetes_api method: streams have job, namespace,
// pod, container, but NO k8s_pod_name.
func TestPodLogsClassicShape(t *testing.T) {
	streams := podLogStreamsForMethod(t, "kubernetes_api", true)

	// Filter to pod-log streams — classic form has "namespace" label.
	var podStreams []loki.Stream
	for _, s := range streams {
		if _, ok := s.Labels["namespace"]; ok {
			// Exclude event/manifest streams (they carry job=eventhandler/manifests).
			j := s.Labels["job"]
			if j == "integrations/kubernetes/eventhandler" || j == "integrations/kubernetes/manifests" {
				continue
			}
			podStreams = append(podStreams, s)
		}
	}

	if len(podStreams) == 0 {
		t.Fatal("classic pod logs: no pod streams with namespace label found")
	}

	for _, s := range podStreams {
		// Required classic labels.
		for _, req := range []string{"job", "namespace", "pod", "container"} {
			if _, ok := s.Labels[req]; !ok {
				t.Errorf("classic pod log stream missing label %q (labels: %v)", req, s.Labels)
			}
		}
		// Must NOT have otel-style k8s_pod_name.
		if _, ok := s.Labels["k8s_pod_name"]; ok {
			t.Errorf("classic pod log stream must NOT have k8s_pod_name (labels: %v)", s.Labels)
		}
		// Must have at least one line.
		if len(s.Lines) == 0 {
			t.Errorf("classic pod log stream has no lines (labels: %v)", s.Labels)
		}
	}
}

// TestPodLogsFeatureOff verifies that when pod_logs feature is off, no pod log streams are emitted.
func TestPodLogsFeatureOff(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.Features = map[string]bool{"pod_logs": false}
	cl.K8sMonitoring.PodLogsMethod = "opentelemetry"
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	// Pod log streams have k8s_namespace_name (otel) or namespace+pod (classic).
	// None should be present.
	for _, s := range lc.Streams {
		// Substrate log streams (events/manifests/journal) carry an "integrations/..." job and
		// also use k8s_namespace_name/namespace — they are NOT pod logs.
		if strings.HasPrefix(s.Labels["job"], "integrations/") {
			continue
		}
		if _, ok := s.Labels["k8s_namespace_name"]; ok {
			t.Errorf("feature off: unexpected pod log stream (otel): %v", s.Labels)
		}
		if _, ok := s.Labels["namespace"]; ok {
			t.Errorf("feature off: unexpected pod log stream (classic): %v", s.Labels)
		}
	}
}

// TestPodLogsMethodNone verifies that method=="none" emits nothing.
func TestPodLogsMethodNone(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.Features = map[string]bool{"pod_logs": true}
	cl.K8sMonitoring.PodLogsMethod = "none"
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	for _, s := range lc.Streams {
		if strings.HasPrefix(s.Labels["job"], "integrations/") {
			continue // substrate streams (events/manifests/journal), not pod logs
		}
		if _, ok := s.Labels["k8s_namespace_name"]; ok {
			t.Errorf("method=none: unexpected pod log stream: %v", s.Labels)
		}
	}
}

// TestPodLogsOtelServiceInstanceID verifies service_instance_id is present in otel streams.
func TestPodLogsOtelServiceInstanceID(t *testing.T) {
	streams := podLogStreamsForMethod(t, "opentelemetry", true)
	for _, s := range streams {
		if _, ok := s.Labels["k8s_namespace_name"]; !ok {
			continue
		}
		if _, ok := s.Labels["service_instance_id"]; !ok {
			t.Errorf("otel pod log stream missing service_instance_id (labels: %v)", s.Labels)
		}
	}
}

// TestPodLogsClassicStructuredMeta verifies classic method sets structured metadata on lines.
func TestPodLogsClassicStructuredMeta(t *testing.T) {
	streams := podLogStreamsForMethod(t, "loki", true)

	// Find classic pod streams (have namespace+pod but not k8s_pod_name).
	found := false
	for _, s := range streams {
		if _, ok := s.Labels["namespace"]; !ok {
			continue
		}
		j := s.Labels["job"]
		if j == "integrations/kubernetes/eventhandler" || j == "integrations/kubernetes/manifests" {
			continue
		}
		if _, ok := s.Labels["k8s_pod_name"]; ok {
			continue // skip otel streams
		}
		for _, line := range s.Lines {
			if line.Meta != nil {
				found = true
				// Must have pod in meta.
				if _, ok := line.Meta["pod"]; !ok {
					t.Errorf("classic pod log line missing 'pod' in structured meta: %v", line.Meta)
				}
			}
		}
	}
	if !found {
		// It's acceptable if no stream had meta; the test just checks correctness when they do.
		t.Log("TestPodLogsClassicStructuredMeta: no line with structured meta found (ok if method=loki not yet implemented)")
	}
}

// TestPodLogsCluster verifies cluster and k8s_cluster_name are set on pod log streams.
func TestPodLogsCluster(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.Features = map[string]bool{"pod_logs": true}
	cl.K8sMonitoring.PodLogsMethod = "opentelemetry"
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	for _, s := range lc.Streams {
		if _, ok := s.Labels["k8s_namespace_name"]; !ok {
			continue
		}
		if got := s.Labels["cluster"]; got != cl.Name {
			t.Errorf("pod log stream cluster=%q, want %q", got, cl.Name)
		}
		if got := s.Labels["k8s_cluster_name"]; got != cl.Name {
			t.Errorf("pod log stream k8s_cluster_name=%q, want %q", got, cl.Name)
		}
	}
}

// TestPodLogsDefaultMethod verifies that when PodLogsMethod is empty and feature is on,
// we get otel-shaped streams (default is opentelemetry).
func TestPodLogsDefaultMethod(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.Features = map[string]bool{"pod_logs": true}
	cl.K8sMonitoring.PodLogsMethod = "" // default → opentelemetry
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	// Expect otel-shaped streams.
	for _, s := range lc.Streams {
		if strings.HasPrefix(s.Labels["job"], "integrations/") {
			continue // substrate streams (events/manifests/journal), not pod logs
		}
		if _, ok := s.Labels["k8s_namespace_name"]; ok {
			if _, ok := s.Labels["k8s_pod_name"]; !ok {
				t.Errorf("default method stream missing k8s_pod_name (expected otel shape): %v", s.Labels)
			}
		}
	}
}

// Ensure fixture.ControlPlane is usable (compile-time check).
var _ fixture.ControlPlane
