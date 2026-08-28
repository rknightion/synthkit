// SPDX-License-Identifier: AGPL-3.0-only

// podlogs_test.go — pod-log emission over both real transports.
//
// PodLogsMethod is the cluster-level transport selector: "opentelemetry" is native OTLP
// (podLogsViaOpenTelemetry, /v1/logs), "kubernetes_api"/"loki" are Loki-native
// (podLogsViaLoki). The load-bearing property is that both carry identical content.
package k8scluster_test

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// otlpLogCapture is a core.OTLPLogWriter that records every resource block.
type otlpLogCapture struct {
	mu        sync.Mutex
	Resources []otlp.LogResource
}

func (c *otlpLogCapture) Write(_ context.Context, resources []otlp.LogResource) error {
	c.mu.Lock()
	c.Resources = append(c.Resources, resources...)
	c.mu.Unlock()
	return nil
}

// tickBothLanes ticks a cluster with BOTH log lanes wired and returns what each received.
func tickBothLanes(t *testing.T, cl *fixture.Cluster) ([]loki.Stream, []otlp.LogResource) {
	t.Helper()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	oc := &otlpLogCapture{}
	w := coretest.World(mc, lc, nil)
	w.OTLPLogs = oc
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return lc.Streams, oc.Resources
}

// clusterWithPodLogs builds the standard test cluster with the pod_logs gate and method set.
func clusterWithPodLogs(method string, featureOn bool) *fixture.Cluster {
	cl := coretest.Cluster()
	cl.K8sMonitoring.Features = map[string]bool{"pod_logs": featureOn}
	cl.K8sMonitoring.PodLogsMethod = method
	return cl
}

// podLogStreamsForMethod returns only the Loki streams that are pod logs: substrate log streams
// (events/manifests/journal) all carry an "integrations/..." job.
func podLogStreamsForMethod(t *testing.T, method string, featureOn bool) []loki.Stream {
	t.Helper()
	streams, _ := tickBothLanes(t, clusterWithPodLogs(method, featureOn))
	var pod []loki.Stream
	for _, s := range streams {
		if strings.HasPrefix(s.Labels["job"], "integrations/") {
			continue
		}
		pod = append(pod, s)
	}
	return pod
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedStringKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── native OTLP transport ────────────────────────────────────────────────────────────

// corpusResourceAttrs is the resource-attribute key set captured at collector egress in
// reality-corpus/k8s/k3d-lab.json (source=k8s_pod_logs, transport=otlp_logs; k8s-monitoring
// 4.4.0 with podLogsViaOpenTelemetry.enabled: true, 2026-08-25). The mixed naming — dotted OTel
// names beside the flat `cluster` and `app_kubernetes_io_name` — is what the collector really
// sends and must survive verbatim.
var corpusResourceAttrs = []string{
	"app_kubernetes_io_name",
	"cluster",
	"k8s.cluster.name",
	"k8s.container.name",
	"k8s.deployment.name",
	"k8s.namespace.name",
	"k8s.node.name",
	"k8s.pod.name",
	"service.instance.id",
	"service.name",
	"service.namespace",
}

// corpusRecordAttrs is the record-attribute key set: the same corpus entry's structured metadata.
// The filelog `container` parser puts exactly these two on the log RECORD (the k8s identity goes
// on the resource), and Loki drops every record attribute into structured metadata.
var corpusRecordAttrs = []string{"log.iostream", "logtag"}

// TestPodLogsOTLPNativeShape verifies the native OTLP transport reproduces the captured shape:
// exactly the corpus resource attributes on the resource, exactly the corpus record attributes
// on the record, and no leakage in either direction.
func TestPodLogsOTLPNativeShape(t *testing.T) {
	cl := clusterWithPodLogs("opentelemetry", true)
	_, resources := tickBothLanes(t, cl)

	if len(resources) == 0 {
		t.Fatal("otlp pod logs: no LogResource emitted")
	}
	for _, r := range resources {
		if got := sortedKeys(r.Attrs); !equalStrings(got, corpusResourceAttrs) {
			t.Errorf("resource attrs = %v, want %v", got, corpusResourceAttrs)
		}
		if r.Attrs["cluster"] != cl.Name || r.Attrs["k8s.cluster.name"] != cl.Name {
			t.Errorf("cluster identity wrong: cluster=%v k8s.cluster.name=%v want %q",
				r.Attrs["cluster"], r.Attrs["k8s.cluster.name"], cl.Name)
		}
		// The destination promotes resource attributes; a record attribute must never appear there.
		for _, k := range corpusRecordAttrs {
			if _, ok := r.Attrs[k]; ok {
				t.Errorf("record attribute %q must not be a resource attribute", k)
			}
		}
		if len(r.Records) != 1 {
			t.Fatalf("want exactly one record per pod resource, got %d", len(r.Records))
		}
		rec := r.Records[0]
		if got := sortedKeys(rec.Attrs); !equalStrings(got, corpusRecordAttrs) {
			t.Errorf("record attrs = %v, want %v", got, corpusRecordAttrs)
		}
		if rec.Attrs["log.iostream"] != "stdout" || rec.Attrs["logtag"] != "F" {
			t.Errorf("record attrs values = %v, want log.iostream=stdout logtag=F", rec.Attrs)
		}
		if rec.Body == "" {
			t.Error("record has empty body")
		}
		if rec.Time.IsZero() || rec.ObservedTime.IsZero() {
			t.Errorf("record timestamps unset: time=%v observed=%v", rec.Time, rec.ObservedTime)
		}
		// The captured pipeline runs no severity parser; Loki derives detected_level from the body.
		if rec.Severity != otlp.SeverityUnspecified || rec.SeverityText != "" {
			t.Errorf("severity must stay unspecified (no severity parser in the captured pipeline), got %v/%q",
				rec.Severity, rec.SeverityText)
		}
		// Pod logs scraped off stdout carry no trace correlation.
		if rec.TraceID != "" || rec.SpanID != "" {
			t.Errorf("pod log record must carry no trace correlation, got trace=%q span=%q", rec.TraceID, rec.SpanID)
		}
		// A real filelog pod log carries an empty instrumentation scope — see internal/sink/otlp/logs.go.
		if r.Scope.Name != "" {
			t.Errorf("scope name must stay empty, got %q", r.Scope.Name)
		}
	}
}

// TestPodLogsOTLPNativeEmitsNoLokiStreams is the defect fix: selecting the OpenTelemetry
// transport must actually change transport, not just re-label a Loki push.
func TestPodLogsOTLPNativeEmitsNoLokiStreams(t *testing.T) {
	streams := podLogStreamsForMethod(t, "opentelemetry", true)
	if len(streams) != 0 {
		t.Errorf("native OTLP transport must push no pod-log Loki streams, got %d e.g. %v",
			len(streams), streams[0].Labels)
	}
}

// TestPodLogsDefaultMethodIsOTLPNative verifies the empty selector takes the chart default.
func TestPodLogsDefaultMethodIsOTLPNative(t *testing.T) {
	_, resources := tickBothLanes(t, clusterWithPodLogs("", true))
	if len(resources) == 0 {
		t.Fatal("default method: expected native OTLP pod logs, got none")
	}
}

// TestPodLogsSignalsDeclareOTLPLane verifies the construct declares core.OTLPLogs only when the
// cluster declared the native OTLP transport — the runner wires the lane from this.
func TestPodLogsSignalsDeclareOTLPLane(t *testing.T) {
	declares := func(cl *fixture.Cluster) bool {
		for _, s := range buildConstruct(t, cl).Signals() {
			if s == core.OTLPLogs {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		method string
		on     bool
		want   bool
	}{
		{"opentelemetry", true, true},
		{"", true, true},
		{"kubernetes_api", true, false},
		{"loki", true, false},
		{"none", true, false},
		{"opentelemetry", false, false},
	} {
		if got := declares(clusterWithPodLogs(tc.method, tc.on)); got != tc.want {
			t.Errorf("method=%q feature=%v: declares OTLPLogs = %v, want %v", tc.method, tc.on, got, tc.want)
		}
	}
}

// TestPodLogsOTLPNativeNilLaneIsInert verifies a cluster whose OTLPLogs writer was never wired
// (runner ran without the sink) degrades to emitting nothing rather than panicking or falling
// back to a different transport behind the operator's back.
func TestPodLogsOTLPNativeNilLaneIsInert(t *testing.T) {
	cl := clusterWithPodLogs("opentelemetry", true)
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := coretest.World(mc, lc, nil) // no OTLPLogs
	if err := c.Tick(context.Background(), time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC), w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	for _, s := range lc.Streams {
		if strings.HasPrefix(s.Labels["job"], "integrations/") {
			continue
		}
		t.Errorf("nil OTLP lane must not fall back to a Loki pod-log push: %v", s.Labels)
	}
}

// ── Both transports carry identical content (AC #6) ──────────────────────────────────

// contentKey is the transport-independent identity of one log line.
type contentKey struct{ ns, pod, container, body string }

// TestPodLogsTransportsCarryIdenticalContent is the load-bearing property: switching transport
// re-shapes the observable and never adds, drops, or alters a log line.
func TestPodLogsTransportsCarryIdenticalContent(t *testing.T) {
	_, resources := tickBothLanes(t, clusterWithPodLogs("opentelemetry", true))
	classic := podLogStreamsForMethod(t, "kubernetes_api", true)

	otlpContent := map[contentKey]int{}
	for _, r := range resources {
		for _, rec := range r.Records {
			otlpContent[contentKey{
				ns:        r.Attrs["k8s.namespace.name"].(string),
				pod:       r.Attrs["k8s.pod.name"].(string),
				container: r.Attrs["k8s.container.name"].(string),
				body:      rec.Body,
			}]++
		}
	}
	lokiContent := map[contentKey]int{}
	for _, s := range classic {
		for _, line := range s.Lines {
			lokiContent[contentKey{
				ns:        s.Labels["namespace"],
				pod:       line.Meta["pod"],
				container: s.Labels["container"],
				body:      line.Body,
			}]++
		}
	}

	if len(otlpContent) == 0 {
		t.Fatal("no OTLP pod-log content captured")
	}
	if len(otlpContent) != len(lokiContent) {
		t.Errorf("distinct log lines: otlp=%d loki=%d", len(otlpContent), len(lokiContent))
	}
	for k, n := range otlpContent {
		if m := lokiContent[k]; m != n {
			t.Errorf("line %+v: otlp count %d, loki count %d", k, n, m)
		}
	}
	for k, n := range lokiContent {
		if _, ok := otlpContent[k]; !ok {
			t.Errorf("line %+v present only on the loki transport (count %d)", k, n)
		}
	}
}

// ── Loki-native transport ────────────────────────────────────────────────────────────

// TestPodLogsClassicShape verifies kubernetes_api method: streams have job, namespace,
// container, but NO pod identity stream label.
func TestPodLogsClassicShape(t *testing.T) {
	streams := podLogStreamsForMethod(t, "kubernetes_api", true)

	var podStreams []loki.Stream
	for _, s := range streams {
		if _, ok := s.Labels["namespace"]; ok {
			podStreams = append(podStreams, s)
		}
	}
	if len(podStreams) == 0 {
		t.Fatal("classic pod logs: no pod streams with namespace label found")
	}

	for _, s := range podStreams {
		for _, req := range []string{"job", "namespace", "container", "cluster", "k8s_cluster_name"} {
			if _, ok := s.Labels[req]; !ok {
				t.Errorf("classic pod log stream missing label %q (labels: %v)", req, s.Labels)
			}
		}
		if _, ok := s.Labels["k8s_pod_name"]; ok {
			t.Errorf("classic pod log stream must NOT have k8s_pod_name (labels: %v)", s.Labels)
		}
		if want := s.Labels["namespace"] + "/" + s.Labels["container"]; s.Labels["job"] != want {
			t.Errorf("classic job = %q, want %q", s.Labels["job"], want)
		}
		if len(s.Lines) == 0 {
			t.Errorf("classic pod log stream has no lines (labels: %v)", s.Labels)
		}
	}
}

// TestPodLogsClassicStructuredMeta verifies the classic transport carries structured metadata
// on the wire (the thing the OTLP transport leaves to the destination).
func TestPodLogsClassicStructuredMeta(t *testing.T) {
	streams := podLogStreamsForMethod(t, "loki", true)
	found := false
	for _, s := range streams {
		if _, ok := s.Labels["namespace"]; !ok {
			continue
		}
		for _, line := range s.Lines {
			found = true
			for _, req := range []string{"pod", "service_instance_id"} {
				if _, ok := line.Meta[req]; !ok {
					t.Errorf("classic pod log line missing %q in structured meta: %v", req, line.Meta)
				}
			}
			if _, ok := line.Meta["k8s_pod_name"]; ok {
				t.Errorf("classic pod log line must not carry k8s_pod_name metadata: %v", line.Meta)
			}
		}
	}
	if !found {
		t.Fatal("classic pod logs: no lines captured")
	}
}

// TestPodLogsClassicCapturedShape pins the Loki-native podLogsViaLoki wire contract. The shared
// builder owns this shape: pod identity is metadata (not stream identity), flags is present, and
// neither the OTLP destination's promoted k8s_pod_name nor an inferred detected_level leaks onto
// the native stream.
func TestPodLogsClassicCapturedShape(t *testing.T) {
	streams := podLogStreamsForMethod(t, "loki", true)
	wantLabels := []string{
		"app_kubernetes_io_name", "cluster", "container", "flags", "job", "k8s_cluster_name",
		"namespace", "service_name", "service_namespace", "stream",
	}
	wantMeta := []string{"pod", "service_instance_id"}
	if len(streams) == 0 {
		t.Fatal("classic pod logs: no streams captured")
	}
	for i, stream := range streams {
		if got := sortedStringKeys(stream.Labels); !equalStrings(got, wantLabels) {
			t.Errorf("stream %d labels = %v, want exactly %v", i, got, wantLabels)
		}
		for _, forbidden := range []string{"pod", "service_instance_id", "detected_level", "k8s_pod_name"} {
			if _, ok := stream.Labels[forbidden]; ok {
				t.Errorf("stream %d must not carry %q as a label: %v", i, forbidden, stream.Labels)
			}
		}
		if stream.Labels["flags"] != "F" {
			t.Errorf("stream %d flags = %q, want F", i, stream.Labels["flags"])
		}
		for j, line := range stream.Lines {
			if got := sortedStringKeys(line.Meta); !equalStrings(got, wantMeta) {
				t.Errorf("stream %d line %d metadata = %v, want exactly %v", i, j, got, wantMeta)
			}
			if line.Meta["pod"] == "" || line.Meta["service_instance_id"] == "" {
				t.Errorf("stream %d line %d metadata values must be non-empty: %v", i, j, line.Meta)
			}
		}
	}
}

// ── Gating ───────────────────────────────────────────────────────────────────────────

// TestPodLogsFeatureOff verifies that with pod_logs off, NEITHER lane emits pod logs.
func TestPodLogsFeatureOff(t *testing.T) {
	streams, resources := tickBothLanes(t, clusterWithPodLogs("opentelemetry", false))
	if len(resources) != 0 {
		t.Errorf("feature off: unexpected OTLP pod-log resources: %d", len(resources))
	}
	for _, s := range streams {
		if strings.HasPrefix(s.Labels["job"], "integrations/") {
			continue // substrate streams (events/manifests/journal), not pod logs
		}
		t.Errorf("feature off: unexpected pod log stream: %v", s.Labels)
	}
}

// TestPodLogsMethodNone verifies method=="none" emits nothing on either lane.
func TestPodLogsMethodNone(t *testing.T) {
	streams, resources := tickBothLanes(t, clusterWithPodLogs("none", true))
	if len(resources) != 0 {
		t.Errorf("method=none: unexpected OTLP pod-log resources: %d", len(resources))
	}
	for _, s := range streams {
		if strings.HasPrefix(s.Labels["job"], "integrations/") {
			continue
		}
		t.Errorf("method=none: unexpected pod log stream: %v", s.Labels)
	}
}

// TestPodLogsUnknownMethodIsInert verifies an unrecognised selector degrades safely rather than
// silently picking a transport.
func TestPodLogsUnknownMethodIsInert(t *testing.T) {
	streams, resources := tickBothLanes(t, clusterWithPodLogs("carrier-pigeon", true))
	if len(resources) != 0 {
		t.Errorf("unknown method: unexpected OTLP pod-log resources: %d", len(resources))
	}
	for _, s := range streams {
		if strings.HasPrefix(s.Labels["job"], "integrations/") {
			continue
		}
		t.Errorf("unknown method: unexpected pod log stream: %v", s.Labels)
	}
}

// Ensure fixture.ControlPlane is usable (compile-time check).
var _ fixture.ControlPlane
