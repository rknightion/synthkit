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

const sampleDumpWithSigil = `== metrics: series name → label keys ==
node_cpu_seconds_total  {[cluster instance mode]}
== metrics: 1 distinct series names ==

== logs: source → stream labels / structured metadata ==
app  stream=[cluster namespace] meta=[trace_id]

== traces: service → resource attrs / span names / span attrs ==
checkout
  resource=[service.name]
  spans=[GET /cart]
  attrs=[http.method]

== sigil: ingest kind → operation names ==
generations  ops=[generateText streamText]
workflow_steps  ops=[]
scores  ops=[]
== sigil: generations=10 workflow_steps=5 scores=3 ==
`

func TestParseDump_Sigil(t *testing.T) {
	s, err := ParseDump(strings.NewReader(sampleDumpWithSigil))
	if err != nil {
		t.Fatalf("ParseDump: %v", err)
	}

	// Sigil kinds must be present
	if _, ok := s.Sigil["generations"]; !ok {
		t.Errorf("missing sigil kind 'generations': %v", s.Sigil)
	}
	if _, ok := s.Sigil["workflow_steps"]; !ok {
		t.Errorf("missing sigil kind 'workflow_steps': %v", s.Sigil)
	}
	if _, ok := s.Sigil["scores"]; !ok {
		t.Errorf("missing sigil kind 'scores': %v", s.Sigil)
	}

	// Operation names under generations must be parsed
	ops := s.Sigil["generations"]
	opSet := map[string]bool{}
	for _, op := range ops {
		opSet[op] = true
	}
	if !opSet["generateText"] {
		t.Errorf("missing 'generateText' in generation ops: %v", ops)
	}
	if !opSet["streamText"] {
		t.Errorf("missing 'streamText' in generation ops: %v", ops)
	}

	// Other sections must still parse correctly
	if _, ok := s.Metrics["node_cpu_seconds_total"]; !ok {
		t.Errorf("metrics section broken after sigil parse: %v", s.Metrics)
	}
	if _, ok := s.LogSources["app"]; !ok {
		t.Errorf("logs section broken after sigil parse: %v", s.LogSources)
	}
}

func TestSubset_SigilKindMissing(t *testing.T) {
	expected := Schema{
		Sigil: map[string][]string{
			"generations":   {"generateText"},
			"workflow_steps": nil,
			"scores":        nil,
		},
	}
	received := Schema{
		Sigil: map[string][]string{
			"generations": {"generateText"},
			// workflow_steps and scores absent
		},
	}
	missing := expected.Subset(received)
	if len(missing) != 2 {
		t.Fatalf("expected 2 missing sigil kinds, got %v", missing)
	}
	kindSet := map[string]bool{}
	for _, m := range missing {
		kindSet[m] = true
	}
	if !kindSet["sigil: workflow_steps"] {
		t.Errorf("expected 'sigil: workflow_steps' in missing: %v", missing)
	}
	if !kindSet["sigil: scores"] {
		t.Errorf("expected 'sigil: scores' in missing: %v", missing)
	}
}

func TestSubset_SigilKindPresent(t *testing.T) {
	schema := Schema{
		Sigil: map[string][]string{
			"generations": {"generateText"},
		},
	}
	missing := schema.Subset(schema)
	if len(missing) > 0 {
		t.Errorf("self-subset should be empty, got %v", missing)
	}
}

func TestParseDump_SigilSection_NoSigil(t *testing.T) {
	// Dumbs without sigil section must still parse (newSchema gives empty Sigil map).
	s, err := ParseDump(strings.NewReader(sampleDump))
	if err != nil {
		t.Fatalf("ParseDump: %v", err)
	}
	if len(s.Sigil) != 0 {
		t.Errorf("expected empty Sigil without sigil section, got %v", s.Sigil)
	}
}
