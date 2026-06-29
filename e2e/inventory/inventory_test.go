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
`

func TestParseDump(t *testing.T) {
	s, err := ParseDump(strings.NewReader(sampleDump))
	if err != nil {
		t.Fatalf("ParseDump: %v", err)
	}
	if got := s.Metrics["node_cpu_seconds_total"]; strings.Join(got, ",") != "cluster,instance,mode" {
		t.Errorf("metric labels: %v", got)
	}
	if _, ok := s.Metrics["up"]; !ok {
		t.Errorf("missing series 'up'")
	}
	if got := s.LogSources["app"]; strings.Join(got, ",") != "cluster,namespace" {
		t.Errorf("log stream keys: %v", got)
	}
	// Correlation is at SERVICE level only — span names with spaces are unrecoverable from
	// the -dump format, so we assert service presence, not exact span strings.
	if _, ok := s.Traces["checkout"]; !ok {
		t.Errorf("missing trace service 'checkout': %v", s.Traces)
	}
}

func TestSubsetTraceServiceMissing(t *testing.T) {
	expected := Schema{Traces: map[string][]string{"a": nil, "b": nil}}
	of := Schema{Traces: map[string][]string{"a": nil}}
	missing := expected.Subset(of)
	if len(missing) != 1 || !strings.Contains(missing[0], "b") {
		t.Fatalf("expected one finding for missing trace service 'b', got %v", missing)
	}
}

func TestSubsetReportsMissing(t *testing.T) {
	a := Schema{Metrics: map[string][]string{"x": nil, "y": nil}}
	b := Schema{Metrics: map[string][]string{"x": nil}}
	missing := a.Subset(b)
	if len(missing) != 1 || !strings.Contains(missing[0], "y") {
		t.Fatalf("expected 'y' missing, got %v", missing)
	}
}
