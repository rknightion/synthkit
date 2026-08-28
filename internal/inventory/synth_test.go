// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

func TestFromSinksSplitsCapturedPodLogsFromManifests(t *testing.T) {
	sink := loki.New("", "", "", true)
	sink.Capture = true
	sink.Quiet = true
	now := time.Unix(1, 0)
	if err := sink.Write(context.Background(), []loki.Stream{
		{
			Labels: map[string]string{
				"app_kubernetes_io_name": "catalog",
				"cluster":                "lab",
				"container":              "catalog",
				"flags":                  "F",
				"job":                    "otel-demo/catalog",
				"k8s_cluster_name":       "lab",
				"namespace":              "otel-demo",
				"service_name":           "catalog",
				"service_namespace":      "otel-demo",
				"stream":                 "stdout",
			},
			Lines: []loki.Line{{T: now, Body: "pod log", Meta: map[string]string{
				"pod": "catalog-0", "service_instance_id": "catalog-0",
			}}},
		},
		{
			Labels: map[string]string{
				"action":             "manifest",
				"cluster":            "lab",
				"instance":           "alloy",
				"job":                "integrations/kubernetes/manifests",
				"k8s_cluster_name":   "lab",
				"k8s_kind":           "Pod",
				"k8s_namespace_name": "otel-demo",
			},
			Lines: []loki.Line{{T: now, Body: "manifest", Meta: map[string]string{
				"k8s_pod_name": "catalog-0",
			}}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	schema := FromSinks(nil, sink, nil, nil, nil, nil, nil)
	if len(schema.Logs) != 2 {
		t.Fatalf("logs=%v, want separate pod-log and manifest entries", schema.Logs)
	}
	var podLogs, manifests *Log
	for i := range schema.Logs {
		log := &schema.Logs[i]
		switch log.Source {
		case LogFamilyPodLogs:
			podLogs = log
		case LogFamilyKubernetesManifests:
			manifests = log
		}
	}
	if podLogs == nil || manifests == nil {
		t.Fatalf("logs=%v, want sources %q and %q", schema.Logs, LogFamilyPodLogs, LogFamilyKubernetesManifests)
	}
	if !containsLogMetadataKey(podLogs, "pod") || containsLogStreamKey(podLogs, "k8s_kind") {
		t.Fatalf("pod logs=%v, want pod metadata and no manifest label", podLogs)
	}
	if !containsLogStreamKey(manifests, "k8s_kind") || containsLogStreamKey(manifests, "container") {
		t.Fatalf("manifests=%v, want manifest label and no pod-log label", manifests)
	}
}

func containsLogStreamKey(log *Log, want string) bool {
	for _, attr := range log.StreamLabels {
		if attr.Key == want {
			return true
		}
	}
	return false
}

func containsLogMetadataKey(log *Log, want string) bool {
	for _, key := range log.StructuredMetadataKeys {
		if key == want {
			return true
		}
	}
	return false
}

func TestFromSinksClassifiesCapturedOTLPPodLogs(t *testing.T) {
	sink := otlp.NewLogs("", "", "", true)
	sink.Capture = true
	sink.Quiet = true
	if err := sink.Write(context.Background(), []otlp.LogResource{{
		Attrs: map[string]any{
			"cluster":             "lab",
			"k8s.cluster.name":    "lab",
			"k8s.namespace.name":  "otel-demo",
			"k8s.pod.name":        "catalog-0",
			"k8s.container.name":  "catalog",
			"service.name":        "catalog",
			"service.namespace":   "otel-demo",
			"service.instance.id": "catalog-0",
		},
		Records: []otlp.LogRecord{{Attrs: map[string]any{"log.iostream": "stdout"}}},
	}}); err != nil {
		t.Fatal(err)
	}

	schema := FromSinks(nil, nil, nil, nil, sink, nil, nil)
	if len(schema.Logs) != 1 {
		t.Fatalf("logs=%v, want one OTLP pod-log entry", schema.Logs)
	}
	log := schema.Logs[0]
	if log.Source != LogFamilyPodLogs || log.Transport != TransportOTLPLogs {
		t.Fatalf("log=%v, want source %q over %q", log, LogFamilyPodLogs, TransportOTLPLogs)
	}
	if !containsLogStreamKey(&log, "k8s.pod.name") || !containsLogMetadataKey(&log, "log.iostream") {
		t.Fatalf("log=%v, want resource identity and record metadata kept split", log)
	}
}

func TestFromSinksMergesClassicAndNativeHistogramFamily(t *testing.T) {
	prom := promrw.New("", "", "", true, nil)
	prom.Capture = true
	now := time.Unix(1, 0)
	if err := prom.Write(context.Background(), []promrw.Series{
		{Name: "request_duration_seconds_bucket", Labels: map[string]string{"job": "api", "le": "0.5"}, Kind: promrw.KindHistogram, T: now},
		{Name: "request_duration_seconds_sum", Labels: map[string]string{"job": "api"}, Kind: promrw.KindHistogram, T: now},
		{Name: "request_duration_seconds_count", Labels: map[string]string{"job": "api"}, Kind: promrw.KindHistogram, T: now},
		{Name: "request_duration_seconds", Labels: map[string]string{"job": "api"}, Kind: promrw.KindHistogram, Native: &promrw.NativeHistogram{Schema: 3}, T: now},
	}); err != nil {
		t.Fatal(err)
	}
	schema := FromSinks(prom, nil, nil, nil, nil, nil, nil)
	if len(schema.Metrics) != 1 {
		t.Fatalf("metrics=%v, want one logical family", schema.Metrics)
	}
	h := schema.Metrics[0].Histogram
	if h == nil || !h.Classic || !h.Native {
		t.Fatalf("histogram=%+v, want classic+native", h)
	}
	if len(h.BucketBounds) != 1 || h.BucketBounds[0] != 0.5 {
		t.Fatalf("bounds=%v", h.BucketBounds)
	}
	if len(h.NativeSchemas) != 1 || h.NativeSchemas[0] != 3 {
		t.Fatalf("native schemas=%v", h.NativeSchemas)
	}
}

func TestFromSinksProjectsSummaryKind(t *testing.T) {
	prom := promrw.New("", "", "", true, nil)
	prom.Capture = true
	if err := prom.Write(context.Background(), []promrw.Series{{
		Name: "go_gc_duration_seconds", Labels: map[string]string{"quantile": "0.5"},
		Kind: promrw.KindSummary, T: time.Unix(1, 0),
	}}); err != nil {
		t.Fatal(err)
	}

	schema := FromSinks(prom, nil, nil, nil, nil, nil, nil)
	if len(schema.Metrics) != 1 {
		t.Fatalf("metrics=%v, want exactly one family", schema.Metrics)
	}
	if got := schema.Metrics[0].InstrumentTypes; len(got) != 1 || got[0] != InstrumentSummary {
		t.Fatalf("instrument_types=%v, want [%s]", got, InstrumentSummary)
	}
}

func TestFromSinksPreservesNativeHistogramNameWithClassicSuffixText(t *testing.T) {
	prom := promrw.New("", "", "", true, nil)
	prom.Capture = true
	if err := prom.Write(context.Background(), []promrw.Series{{
		Name: "native_business_sum", Kind: promrw.KindHistogram,
		Native: &promrw.NativeHistogram{Schema: 3}, T: time.Unix(1, 0),
	}}); err != nil {
		t.Fatal(err)
	}
	schema := FromSinks(prom, nil, nil, nil, nil, nil, nil)
	if len(schema.Metrics) != 1 || schema.Metrics[0].Name != "native_business_sum" {
		t.Fatalf("metrics=%v", schema.Metrics)
	}
}

// TestFromSinksProjectsExponentialHistogram pins the projection of the OTLP exponential-histogram
// shape. Before this, addOTLPMetricResource had no case for the kind, so such a metric fell through
// to the gauge branch and was recorded with resource attributes only — losing every datapoint
// attribute. The e2e receiver already records it as a native histogram, so a mismatch here would
// fail the correlation check on instrument type the moment any lane emits the shape.
func TestFromSinksProjectsExponentialHistogram(t *testing.T) {
	sink := otlp.NewMetrics("", "", "", true)
	sink.Capture = true
	if err := sink.Write(context.Background(), []otlp.MetricResource{{
		Attrs: map[string]any{"service.name": "svc"},
		Metrics: []otlp.Metric{{
			Name: "http.server.request.duration",
			Kind: otlp.MetricExponentialHistogram,
			ExpHistograms: []otlp.ExponentialHistogramPoint{{
				Attrs:    map[string]any{"http.request.method": "GET"},
				Count:    3,
				Sum:      0.5,
				Scale:    3,
				Positive: otlp.ExponentialBuckets{Offset: 1, BucketCounts: []uint64{1, 2}},
			}},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	schema := FromSinks(nil, nil, nil, sink, nil, nil, nil)
	if len(schema.Metrics) != 1 {
		t.Fatalf("metrics=%v, want exactly one family", schema.Metrics)
	}
	m := schema.Metrics[0]
	if got := m.InstrumentTypes; len(got) != 1 || got[0] != InstrumentHistogram {
		t.Fatalf("instrument_types=%v, want [%s]", got, InstrumentHistogram)
	}
	if m.Histogram == nil || !m.Histogram.Native {
		t.Fatalf("histogram=%+v, want a native histogram", m.Histogram)
	}
	if got := m.Histogram.NativeSchemas; len(got) != 1 || got[0] != 3 {
		t.Fatalf("native_schemas=%v, want [3]", got)
	}
	// The datapoint attribute is the part the gauge fall-through used to drop.
	var keys []string
	for _, l := range m.Labels {
		keys = append(keys, l.Key)
	}
	if !slices.Contains(keys, "http.request.method") {
		t.Fatalf("labels=%v, want the datapoint attribute http.request.method preserved", keys)
	}
}
