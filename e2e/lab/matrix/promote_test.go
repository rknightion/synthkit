// SPDX-License-Identifier: AGPL-3.0-only

package matrix

import (
	"reflect"
	"testing"

	"github.com/rknightion/synthkit/internal/inventory"
)

func TestPromoteRetainsOnlyContractFixedAttributeValues(t *testing.T) {
	candidate := inventory.New()
	candidate.AddMetric("system.network.io", inventory.TransportOTLPMetrics, inventory.InstrumentCounter,
		map[string]string{"direction": "receive", "device": "eth0", "k8s.node.name": "k3d-lab-agent-0", "os.type": "linux"}, nil)

	promoted, err := PromoteCandidate(candidate, PromoteOptions{MetricPrefixes: []string{"system."}})
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted.Metrics) != 1 {
		t.Fatalf("metrics=%d, want the one selected family", len(promoted.Metrics))
	}
	for _, attribute := range promoted.Metrics[0].Labels {
		_, contractFixed := ContractFixedAttribute(attribute.Key)
		switch {
		case contractFixed && attribute.ValuesElided:
			t.Fatalf("%s: elided a value set fixed by the producer's own contract", attribute.Key)
		case !contractFixed && !attribute.ValuesElided:
			t.Fatalf("%s: retained %v, which is one deployment's choice and not a value space", attribute.Key, attribute.Values)
		}
	}
}

// A prefix that matches nothing would otherwise write a well-formed corpus document recording a
// capture that never happened, and the corpus is the repository's only ground truth.
func TestPromoteRefusesToWriteAnEmptyMetricDocument(t *testing.T) {
	candidate := inventory.New()
	candidate.AddMetric("system.cpu.time", inventory.TransportOTLPMetrics, inventory.InstrumentCounter, map[string]string{"os.type": "linux"}, nil)
	if _, err := PromoteCandidate(candidate, PromoteOptions{MetricPrefixes: []string{"k8s."}}); err == nil {
		t.Fatal("promoted a document with no matching family, want a refusal")
	}
}

func TestPromoteAllMetricsAppliesDeclaredLabAndScrapeExclusions(t *testing.T) {
	candidate := inventory.New()
	for _, name := range []string{
		"go_gc_duration_seconds", "process_cpu_seconds_total", "prober_probe_total",
		"synthkit_lab_requests_total", "synthkit_lab_up",
		"scrape_duration_seconds", "scrape_samples_scraped", "up", "uptime_seconds",
	} {
		candidate.AddMetric(name, inventory.TransportPrometheusRW2, inventory.InstrumentGauge, nil, nil)
	}

	promoted, err := PromoteCandidate(candidate, PromoteOptions{
		Metrics:               true,
		ExcludeMetricPrefixes: []string{"synthkit_lab_", "scrape_"},
		ExcludeMetricNames:    []string{"up"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(promoted.Metrics))
	for _, metric := range promoted.Metrics {
		got = append(got, metric.Name)
	}
	want := []string{"go_gc_duration_seconds", "prober_probe_total", "process_cpu_seconds_total", "uptime_seconds"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("promoted metrics=%v, want genuine producer families %v", got, want)
	}
}

// A capture names a log stream after whichever workload happened to write a line inside the
// window. Splitting the corpus by that is capture-instance identity at the source level, so two
// producers' pod-log evidence would never compare.
func TestPromoteFoldsPodLogStreamsIntoOneCanonicalSource(t *testing.T) {
	candidate := inventory.New()
	candidate.AddLog("lab-catalog", inventory.TransportOTLPLogs,
		map[string]string{"k8s.pod.name": "lab-catalog-1", "service.name": "lab-catalog"}, []string{"log.file.path"})
	candidate.AddLog("coredns", inventory.TransportOTLPLogs,
		map[string]string{"k8s.pod.name": "coredns-1", "container.image.tag": "1.14.3"}, []string{"log.iostream"})

	promoted, err := PromoteCandidate(candidate, PromoteOptions{FoldPodLogs: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted.Logs) != 1 {
		t.Fatalf("logs=%d, want one folded stream", len(promoted.Logs))
	}
	folded := promoted.Logs[0]
	if folded.Source != canonicalPodLogSource {
		t.Fatalf("source=%q, want the canonical pod-log identifier", folded.Source)
	}
	if len(folded.StreamLabels) != 3 {
		t.Fatalf("stream labels=%d, want the union of both streams' keys", len(folded.StreamLabels))
	}
	for _, label := range folded.StreamLabels {
		if !label.ValuesElided {
			t.Fatalf("%s: retained %v; every pod-log stream label is deployment identity", label.Key, label.Values)
		}
	}
	if len(folded.StructuredMetadataKeys) != 2 {
		t.Fatalf("structured metadata keys=%v, want the union of both streams'", folded.StructuredMetadataKeys)
	}
}
