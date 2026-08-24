// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"strings"
	"testing"
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
