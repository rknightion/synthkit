// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"strings"
	"testing"

	canonical "github.com/rknightion/synthkit/internal/inventory"
)

const sampleDump = `== metrics: series name → label keys ==
node_cpu_seconds_total  {[cluster instance mode]}
up  {[instance job]}
== metrics: 2 distinct series names ==

== logs: source → stream labels / structured metadata ==
app  stream=[cluster namespace] meta=[trace_id]

== traces: service → resource attrs / span names / span attrs ==
checkout
  resource=[service.name k8s.cluster.name]
  spans=[GET /cart POST /checkout]
  attrs=[http.method]

== sigil: ingest kind → operation names ==
generations  ops=[generateText streamText]
workflow_steps  ops=[]
scores  ops=[]
== sigil: generations=10 workflow_steps=5 scores=3 ==

=== PYROSCOPE === profile type → label keys ==
process_cpu:cpu:nanoseconds:cpu:nanoseconds  {[__name__ service_name]}
=== PYROSCOPE: 1 distinct profile types ===
`

func TestParseDumpIntoCanonicalSchema(t *testing.T) {
	schema, err := ParseDump(strings.NewReader(sampleDump))
	if err != nil {
		t.Fatal(err)
	}
	if len(schema.Metrics) != 2 || len(schema.Logs) != 1 || len(schema.Traces) != 1 || len(schema.Sigil) != 3 || len(schema.Profiles) != 1 {
		t.Fatalf("unexpected inventory counts: metrics=%d logs=%d traces=%d sigil=%d profiles=%d", len(schema.Metrics), len(schema.Logs), len(schema.Traces), len(schema.Sigil), len(schema.Profiles))
	}
	if missing := schema.Subset(schema); len(missing) != 0 {
		t.Fatalf("self subset=%v", missing)
	}
}

func TestParseDumpFoldsOnlyProvenClassicHistogramFamilies(t *testing.T) {
	dump := `== metrics: series name → label keys ==
aws_rds_cpuutilization_sum  {[region]}
aws_rds_cpuutilization_sample_count  {[region]}
request_duration_seconds_bucket  {[job le]}
request_duration_seconds_sum  {[job]}
request_duration_seconds_count  {[job]}
== metrics: 5 distinct series names ==

== otlp metrics: series name → attribute keys ==
http.server.active_requests  {[http.request.method service.name url.scheme]}
http.server.request.duration  {[http.request.method http.route http.response.status_code service.name]}
== otlp metrics: 2 distinct series names ==
`
	schema, err := ParseDump(strings.NewReader(dump))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"aws_rds_cpuutilization_sample_count",
		"aws_rds_cpuutilization_sum",
		"http.server.active_requests",
		"http.server.request.duration",
		"request_duration_seconds",
	}
	got := make([]string, 0, len(schema.Metrics))
	for _, metric := range schema.Metrics {
		got = append(got, metric.Name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("metric names=%v, want %v", got, want)
	}
	if histogram := schema.Metrics[4].Histogram; histogram == nil || !histogram.Classic {
		t.Fatalf("proved classic histogram lost its representation: %#v", schema.Metrics[4])
	}
}

func TestSubsetReportsMissingMetric(t *testing.T) {
	expected, err := ParseDump(strings.NewReader(sampleDump))
	if err != nil {
		t.Fatal(err)
	}
	received := expected
	received.Metrics = received.Metrics[:1]
	missing := expected.Subset(received)
	if len(missing) != 1 || !strings.HasPrefix(missing[0], "metric: ") {
		t.Fatalf("missing=%v", missing)
	}
}

func TestParseDumpSplitsCapturedPodLogsFromManifests(t *testing.T) {
	dump := `== logs: source → stream labels / structured metadata ==
  stream=[app_kubernetes_io_name cluster container flags job k8s_cluster_name namespace service_name service_namespace stream] meta=[pod service_instance_id]
  stream=[action cluster instance job k8s_cluster_name k8s_kind k8s_namespace_name] meta=[k8s_pod_name]
`
	schema, err := ParseDump(strings.NewReader(dump))
	if err != nil {
		t.Fatal(err)
	}
	if len(schema.Logs) != 2 {
		t.Fatalf("logs=%v, want separate pod-log and manifest entries", schema.Logs)
	}
	var podLogs, manifests bool
	for i := range schema.Logs {
		switch schema.Logs[i].Source {
		case canonical.LogFamilyPodLogs:
			podLogs = true
		case canonical.LogFamilyKubernetesManifests:
			manifests = true
		}
	}
	if !podLogs || !manifests {
		t.Fatalf("logs=%v, want sources %s and %s", schema.Logs, canonical.LogFamilyPodLogs, canonical.LogFamilyKubernetesManifests)
	}
}
