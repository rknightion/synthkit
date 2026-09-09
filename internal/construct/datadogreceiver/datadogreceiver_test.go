// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

type otlpMetricCapture struct{ resources []otlp.MetricResource }

func (c *otlpMetricCapture) Write(_ context.Context, resources []otlp.MetricResource) error {
	c.resources = append(c.resources, resources...)
	return nil
}

type capturedEnvelope struct {
	Name                   string         `json:"name"`
	Instrument             string         `json:"instrument"`
	AggregationTemporality string         `json:"aggregation_temporality"`
	IsMonotonic            bool           `json:"is_monotonic"`
	Unit                   string         `json:"unit"`
	ResourceAttributes     []capturedAttr `json:"resource_attributes"`
	DatapointAttributes    []capturedAttr `json:"datapoint_attributes"`
	ResourceSchemaURL      string         `json:"resource_schema_url"`
	ScopeSchemaURL         string         `json:"scope_schema_url"`
	Scope                  capturedScope  `json:"scope"`
}

type capturedAttr struct {
	Key string `json:"key"`
}
type capturedScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type capturedArtifact struct {
	MetricEnvelopes []capturedEnvelope `json:"metric_envelopes"`
}

func TestObservedExampleEnvelope(t *testing.T) {
	constructed, err := Build(&Config{
		HostName: "agent-node", ServiceName: "example-service", Source: "agent", IncrementsPerMinute: 0.5,
	}, &fixture.Set{Cluster: coretest.Cluster()})
	if err != nil {
		t.Fatal(err)
	}
	c := constructed.(*Construct)
	if got, want := c.Signals(), []core.SignalClass{core.OTLPMetrics}; !equalSignals(got, want) {
		t.Fatalf("Signals() = %v, want %v", got, want)
	}

	capture := &otlpMetricCapture{}
	w := &core.World{OTLPMetrics: capture}
	first := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), first, w); err != nil {
		t.Fatal(err)
	}
	if err := c.Tick(context.Background(), first.Add(30*time.Second), w); err != nil {
		t.Fatal(err)
	}
	examples := metricResourcesByName(capture.resources)[exampleMetricIncrement]
	if got, want := len(examples), 2; got != want {
		t.Fatalf("example metric resources = %d, want %d", got, want)
	}
	firstResource, secondResource := examples[0], examples[1]
	if got, want := firstResource.Scope, (otlp.Scope{Name: translatorScope, Version: translatorVersion}); got != want {
		t.Fatalf("scope = %#v, want %#v", got, want)
	}
	if firstResource.ResourceSchemaURL != "" {
		t.Fatalf("resource schema URL = %q, want absent", firstResource.ResourceSchemaURL)
	}
	if got, want := sortedKeys(firstResource.Attrs), []string{"host.name", "service.name", "source"}; !equalStrings(got, want) {
		t.Fatalf("resource keys = %v, want %v", got, want)
	}
	m := firstResource.Metrics[0]
	if m.Name != exampleMetricIncrement || m.Kind != otlp.MetricSum || m.Monotonic || m.Temporality != otlp.TemporalityCumulative || m.Unit != "" {
		t.Fatalf("metric contract = %#v", m)
	}
	if got := sortedKeys(m.Numbers[0].Attrs); len(got) != 0 {
		t.Fatalf("datapoint attributes = %v, want none", got)
	}
	if got, want := m.Numbers[0].Value, 0.0; got != want {
		t.Fatalf("first cumulative value = %v, want %v", got, want)
	}
	if got, want := secondResource.Metrics[0].Numbers[0].Value, 0.25; got != want {
		t.Fatalf("second cumulative value = %v, want %v", got, want)
	}
	if got, want := secondResource.Metrics[0].Numbers[0].Start, first; !got.Equal(want) {
		t.Fatalf("second start = %v, want %v", got, want)
	}
	if err := matchesCapturedEnvelope(firstResource, capturedExampleEnvelope(t)); err != nil {
		t.Fatalf("native envelope differs from retained evidence: %v", err)
	}
}

// TestCPUFixtureMechanicsEnvelope is the first bounded fixture-mechanics group.
// The capture establishes every native envelope; fixture capacity supplies the
// synthetic CPU mechanics without importing the host construct.
func TestCPUFixtureMechanicsEnvelope(t *testing.T) {
	constructed, err := Build(&Config{
		HostName: "agent-node", ServiceName: "example-service", Source: "agent", IncrementsPerMinute: 0.5,
	}, &fixture.Set{Cluster: coretest.Cluster()})
	if err != nil {
		t.Fatal(err)
	}
	capture := &otlpMetricCapture{}
	if err := constructed.Tick(context.Background(), time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), &core.World{OTLPMetrics: capture}); err != nil {
		t.Fatal(err)
	}
	got := metricResourcesByName(capture.resources)
	for _, name := range cpuFixtureMechanicNames {
		resources := got[name]
		if len(resources) == 0 {
			t.Errorf("CPU fixture mechanic %q was not emitted", name)
			continue
		}
		if err := matchesCapturedEnvelope(resources[0], capturedEnvelopeByName(t, name)[0]); err != nil {
			t.Errorf("CPU fixture mechanic %q envelope: %v", name, err)
		}
	}
}

// TestMemoryLoadFixtureMechanicsEnvelope covers the idle capacity, load, and
// uptime families. Their values satisfy the Agent's documented arithmetic while
// avoiding kernel-state values that the fixture does not declare.
func TestMemoryLoadFixtureMechanicsEnvelope(t *testing.T) {
	constructed, err := Build(&Config{
		HostName: "agent-node", ServiceName: "example-service", Source: "agent", IncrementsPerMinute: 0.5,
	}, &fixture.Set{Cluster: coretest.Cluster()})
	if err != nil {
		t.Fatal(err)
	}
	capture := &otlpMetricCapture{}
	if err := constructed.Tick(context.Background(), time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), &core.World{OTLPMetrics: capture}); err != nil {
		t.Fatal(err)
	}
	assertCapturedArtifactEnvelopes(t, capture.resources, memoryLoadFixtureMechanicNames)
}

// TestAllFixtureMechanicsUseNativeArtifactEnvelopes is the direct immutable-
// artifact proof for every implemented fixture-mechanics family. It deliberately
// checks native placement rather than the flattened compatibility projection.
func TestAllFixtureMechanicsUseNativeArtifactEnvelopes(t *testing.T) {
	constructed, err := Build(&Config{
		HostName: "agent-node", ServiceName: "example-service", Source: "agent", IncrementsPerMinute: 0.5,
	}, &fixture.Set{Cluster: coretest.Cluster()})
	if err != nil {
		t.Fatal(err)
	}
	capture := &otlpMetricCapture{}
	if err := constructed.Tick(context.Background(), time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), &core.World{OTLPMetrics: capture}); err != nil {
		t.Fatal(err)
	}
	assertCapturedArtifactEnvelopes(t, capture.resources, allFixtureMechanicNames())
	got := metricResourcesByName(capture.resources)
	want := map[string]bool{exampleMetricIncrement: true}
	for _, name := range allFixtureMechanicNames() {
		want[name] = true
	}
	if len(got) != len(want) {
		t.Fatalf("emitted family count = %d, want %d; got %v", len(got), len(want), sortedMapKeys(got))
	}
	for name := range want {
		if len(got[name]) == 0 {
			t.Errorf("expected emitted family %q is absent", name)
		}
	}
}

func TestFixtureCapacityUsesSelectedClusterNode(t *testing.T) {
	cluster := coretest.Cluster()
	cluster.Nodes[0].InstanceType = "m6i.large"
	constructed, err := Build(&Config{
		HostName: "agent-node", ServiceName: "example-service", Source: "agent", IncrementsPerMinute: 0.5,
	}, &fixture.Set{Cluster: cluster})
	if err != nil {
		t.Fatal(err)
	}
	capture := &otlpMetricCapture{}
	first := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	if err := constructed.Tick(context.Background(), first, &core.World{OTLPMetrics: capture}); err != nil {
		t.Fatal(err)
	}
	if err := constructed.Tick(context.Background(), first.Add(30*time.Second), &core.World{OTLPMetrics: capture}); err != nil {
		t.Fatal(err)
	}
	metrics := metricResourcesByName(capture.resources)
	if got, want := metrics["system.cpu.num_cores"][0].Metrics[0].Numbers[0].Value, 2.0; got != want {
		t.Errorf("system.cpu.num_cores = %v, want selected node capacity %v", got, want)
	}
	memoryMiB := fixture.LookupInstanceSpec("m6i.large").MemBytes / (1024 * 1024)
	if got, want := metrics["system.mem.total"][0].Metrics[0].Numbers[0].Value, memoryMiB; got != want {
		t.Errorf("system.mem.total = %v, want selected node capacity %v", got, want)
	}
	for _, name := range []string{"system.mem.free", "system.mem.usable"} {
		if got, want := metrics[name][0].Metrics[0].Numbers[0].Value, memoryMiB; got != want {
			t.Errorf("%s = %v, want idle-host capacity %v", name, got, want)
		}
	}
	if got := metrics["system.mem.used"][0].Metrics[0].Numbers[0].Value; got != 0 {
		t.Errorf("system.mem.used = %v, want 0 for idle host", got)
	}
	if got := metrics["system.mem.pct_usable"][0].Metrics[0].Numbers[0].Value; got != 1 {
		t.Errorf("system.mem.pct_usable = %v, want 1 for idle host", got)
	}
	if got := metrics["system.cpu.idle"][0].Metrics[0].Numbers[0].Value; got != 100 {
		t.Errorf("system.cpu.idle = %v, want 100 for idle host", got)
	}
	if got := metrics["system.cpu.user"][0].Metrics[0].Numbers[0].Value; got != 0 {
		t.Errorf("system.cpu.user = %v, want 0 for idle host", got)
	}
	if got := metrics["system.uptime"][1].Metrics[0].Numbers[0].Value; got != 30 {
		t.Errorf("system.uptime after 30s = %v, want 30", got)
	}
	for _, resource := range metrics["system.cpu.idle.total"] {
		if got := resource.Metrics[0].Numbers[0].Value; got != 0 && got != 30 {
			t.Errorf("system.cpu.idle.total = %v, want synthetic uptime value", got)
		}
	}
	for _, resource := range metrics["system.cpu.context_switches"] {
		if got := resource.Metrics[0].Numbers[0].Value; got != 0 {
			t.Errorf("system.cpu.context_switches = %v, want 0 without work", got)
		}
	}
}

var cpuFixtureMechanicNames = []string{
	"system.cpu.context_switches", "system.cpu.guest", "system.cpu.guest.total", "system.cpu.guestnice.total",
	"system.cpu.idle", "system.cpu.idle.total", "system.cpu.interrupt", "system.cpu.iowait", "system.cpu.iowait.total",
	"system.cpu.irq.total", "system.cpu.nice.total", "system.cpu.num_cores", "system.cpu.softirq.total",
	"system.cpu.steal.total", "system.cpu.stolen", "system.cpu.system", "system.cpu.system.total", "system.cpu.user", "system.cpu.user.total",
}

var memoryLoadFixtureMechanicNames = []string{
	"system.load.1", "system.load.15", "system.load.5", "system.load.norm.1", "system.load.norm.15", "system.load.norm.5",
	"system.mem.total", "system.mem.free", "system.mem.used", "system.mem.usable", "system.mem.pct_usable", "system.uptime",
}

func allFixtureMechanicNames() []string {
	all := make([]string, 0, len(cpuFixtureMechanicNames)+len(memoryLoadFixtureMechanicNames))
	all = append(all, cpuFixtureMechanicNames...)
	all = append(all, memoryLoadFixtureMechanicNames...)
	return all
}

func sortedMapKeys(values map[string][]otlp.MetricResource) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestBuildRefusesStandaloneOrUnsourcedRate(t *testing.T) {
	valid := &Config{HostName: "agent-node", ServiceName: "example-service", Source: "agent", IncrementsPerMinute: 0.5}
	if _, err := Build(valid, nil); err == nil {
		t.Fatal("Build accepted a path without a Kubernetes fixture")
	}
	for _, rate := range []float64{0, math.NaN(), math.Inf(1)} {
		invalid := *valid
		invalid.IncrementsPerMinute = rate
		if _, err := Build(&invalid, &fixture.Set{Cluster: coretest.Cluster()}); err == nil {
			t.Errorf("Build accepted invalid rate %v", rate)
		}
	}
}

// TestNativeEnvelopeComparisonRejectsSwappedPlacement is the negative control for
// the compatibility corpus projection: a flattened key union cannot observe this
// defect, but the native-envelope proof must reject it.
func TestNativeEnvelopeComparisonRejectsSwappedPlacement(t *testing.T) {
	constructed, err := Build(&Config{HostName: "agent-node", ServiceName: "example-service", Source: "agent", IncrementsPerMinute: 0.5}, &fixture.Set{Cluster: coretest.Cluster()})
	if err != nil {
		t.Fatal(err)
	}
	capture := &otlpMetricCapture{}
	if err := constructed.Tick(context.Background(), time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), &core.World{OTLPMetrics: capture}); err != nil {
		t.Fatal(err)
	}
	bad := capture.resources[0]
	bad.Attrs = map[string]any{}
	bad.Metrics = append([]otlp.Metric(nil), bad.Metrics...)
	bad.Metrics[0].Numbers = append([]otlp.NumberPoint(nil), bad.Metrics[0].Numbers...)
	bad.Metrics[0].Numbers[0].Attrs = map[string]any{"host.name": "agent-node", "service.name": "example-service", "source": "agent"}
	if err := matchesCapturedEnvelope(bad, capturedExampleEnvelope(t)); err == nil {
		t.Fatal("native envelope comparison accepted resource keys moved to the datapoint")
	}
}

func capturedExampleEnvelope(t *testing.T) capturedEnvelope {
	return capturedEnvelopeByName(t, exampleMetricIncrement)[0]
}

func capturedEnvelopeByName(t *testing.T, name string) []capturedEnvelope {
	t.Helper()
	path := filepath.Join("..", "..", "..", "e2e", "acceptance", "datadog-native-envelope-2026-09-08.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var artifact capturedArtifact
	if err := json.Unmarshal(b, &artifact); err != nil {
		t.Fatal(err)
	}
	var found []capturedEnvelope
	for _, envelope := range artifact.MetricEnvelopes {
		if envelope.Name == name {
			found = append(found, envelope)
		}
	}
	if len(found) == 0 {
		t.Fatalf("retained artifact has no %q envelope", name)
	}
	return found
}

func metricResourcesByName(resources []otlp.MetricResource) map[string][]otlp.MetricResource {
	byName := make(map[string][]otlp.MetricResource)
	for _, resource := range resources {
		for _, metric := range resource.Metrics {
			if len(resource.Metrics) != 1 {
				panic("datadog receiver test helper needs one metric per resource")
			}
			byName[metric.Name] = append(byName[metric.Name], resource)
		}
	}
	return byName
}

func assertCapturedArtifactEnvelopes(t *testing.T, resources []otlp.MetricResource, names []string) {
	t.Helper()
	got := metricResourcesByName(resources)
	for _, name := range names {
		wants := capturedEnvelopeByName(t, name)
		candidates := append([]otlp.MetricResource(nil), got[name]...)
		for _, want := range wants {
			matched := -1
			for i, candidate := range candidates {
				if err := matchesCapturedEnvelope(candidate, want); err == nil {
					matched = i
					break
				}
			}
			if matched < 0 {
				t.Errorf("no emitted envelope for %q matched native artifact shape %+v", name, want)
				continue
			}
			candidates = append(candidates[:matched], candidates[matched+1:]...)
		}
	}
}

func matchesCapturedEnvelope(resource otlp.MetricResource, want capturedEnvelope) error {
	if got := resource.Scope.Name; got != want.Scope.Name {
		return fmt.Errorf("scope name %q, want %q", got, want.Scope.Name)
	}
	if got := resource.Scope.Version; got != want.Scope.Version {
		return fmt.Errorf("scope version %q, want %q", got, want.Scope.Version)
	}
	if resource.ResourceSchemaURL != want.ResourceSchemaURL {
		return fmt.Errorf("resource schema URL %q, want %q", resource.ResourceSchemaURL, want.ResourceSchemaURL)
	}
	if want.ScopeSchemaURL != "" {
		return fmt.Errorf("captured nonempty scope schema URL %q is not representable by the current OTLP metric seam", want.ScopeSchemaURL)
	}
	if got, expected := sortedKeys(resource.Attrs), capturedKeys(want.ResourceAttributes); !equalStrings(got, expected) {
		return fmt.Errorf("resource keys %v, want %v", got, expected)
	}
	if len(resource.Metrics) != 1 {
		return fmt.Errorf("metrics %d, want 1", len(resource.Metrics))
	}
	metric := resource.Metrics[0]
	if metric.Name != want.Name || metric.Unit != want.Unit {
		return fmt.Errorf("name/unit %q/%q, want %q/%q", metric.Name, metric.Unit, want.Name, want.Unit)
	}
	switch want.Instrument {
	case "gauge":
		if metric.Kind != otlp.MetricGauge {
			return fmt.Errorf("instrument kind %q/%v, want gauge", want.Instrument, metric.Kind)
		}
	case "sum":
		if metric.Kind != otlp.MetricSum {
			return fmt.Errorf("instrument kind %q/%v, want sum", want.Instrument, metric.Kind)
		}
		if want.AggregationTemporality != "AGGREGATION_TEMPORALITY_CUMULATIVE" || metric.Temporality != otlp.TemporalityCumulative {
			return fmt.Errorf("temporality %q/%v, want cumulative", want.AggregationTemporality, metric.Temporality)
		}
		if metric.Monotonic != want.IsMonotonic {
			return fmt.Errorf("monotonic %v, want %v", metric.Monotonic, want.IsMonotonic)
		}
	default:
		return fmt.Errorf("unsupported captured instrument %q", want.Instrument)
	}
	if len(metric.Numbers) != 1 {
		return fmt.Errorf("number points %d, want 1", len(metric.Numbers))
	}
	if got, expected := sortedKeys(metric.Numbers[0].Attrs), capturedKeys(want.DatapointAttributes); !equalStrings(got, expected) {
		return fmt.Errorf("datapoint keys %v, want %v", got, expected)
	}
	return nil
}

func capturedKeys(attrs []capturedAttr) []string {
	keys := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		keys = append(keys, attr.Key)
	}
	sort.Strings(keys)
	return keys
}

func equalSignals(got, want []core.SignalClass) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
