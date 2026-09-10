// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
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
	if got := metrics["system.uptime"][1].Metrics[0].Numbers[0].Value; got != 30 {
		t.Errorf("system.uptime after 30s = %v, want 30", got)
	}
}

func TestFixtureMechanicsArithmeticVariationAndDeterminism(t *testing.T) {
	first := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	times := []time.Time{first, first.Add(15 * time.Second), first.Add(30 * time.Second)}
	run := func() []otlp.MetricResource {
		constructed, err := Build(&Config{
			HostName: "agent-node", ServiceName: "example-service", Source: "agent", IncrementsPerMinute: 0.5,
		}, &fixture.Set{Cluster: coretest.Cluster()})
		if err != nil {
			t.Fatal(err)
		}
		capture := &otlpMetricCapture{}
		world := &core.World{OTLPMetrics: capture, Shape: shape.New("", nil)}
		for _, now := range times {
			if err := constructed.Tick(context.Background(), now, world); err != nil {
				t.Fatal(err)
			}
		}
		return capture.resources
	}
	resources := run()
	if again := run(); !reflect.DeepEqual(resources, again) {
		t.Fatal("fixture mechanics changed for the same shape seed and tick sequence")
	}

	metrics := metricResourcesByName(resources)
	for tick := range times {
		cpu := fixtureCPUSample(metrics, tick)
		if got, want := cpu.user+cpu.system+cpu.iowait+cpu.idle+cpu.stolen, 100.0; math.Abs(got-want) > 1e-9 {
			t.Errorf("tick %d CPU partition = %.12f, want %.12f", tick, got, want)
		}
		// The Agent's system percentage includes IRQ + softirq, which it also
		// reports separately as interrupt; guest is likewise outside its denominator.
		if got, want := cpu.user+cpu.system+cpu.interrupt+cpu.iowait+cpu.idle+cpu.stolen+cpu.guest, 100+cpu.interrupt+cpu.guest; math.Abs(got-want) > 1e-9 {
			t.Errorf("tick %d Agent CPU identity = %.12f, want %.12f", tick, got, want)
		}
		assertFixtureMechanicsInBand(t, metrics, tick)
		coreCountValue := metricValue(metrics, "system.cpu.num_cores", tick)
		coreCount := int(coreCountValue)
		if coreCount < 1 || float64(coreCount) != coreCountValue {
			t.Fatalf("tick %d CPU core count = %v, want a positive integer", tick, coreCountValue)
		}
		wantCoreCounts := make(map[string]int, coreCount)
		for core := 0; core < coreCount; core++ {
			wantCoreCounts[strconv.Itoa(core)] = 1
		}
		for _, name := range cpuCoreTotalMetricNames {
			if got := fixtureCoreCounts(metrics, tick, name); !reflect.DeepEqual(got, wantCoreCounts) {
				t.Errorf("tick %d %s cores = %v, want %v", tick, name, got, wantCoreCounts)
			}
		}
		for core := 0; core < coreCount; core++ {
			totals := fixtureCoreTotals(metrics, tick, strconv.Itoa(core))
			if got, want := len(totals), len(cpuCoreTotalMetricNames); got != want {
				t.Errorf("tick %d core %d totals = %d, want %d", tick, core, got, want)
				continue
			}
			var sum float64
			for _, value := range totals {
				sum += value
			}
			if got, want := sum, times[tick].Sub(first).Seconds(); math.Abs(got-want) > 1e-9 {
				t.Errorf("tick %d core %d total CPU time = %.12f, want elapsed %.12f", tick, core, got, want)
			}
			if tick > 0 {
				previous := fixtureCoreTotals(metrics, tick-1, strconv.Itoa(core))
				for name, value := range totals {
					if value < previous[name] {
						t.Errorf("tick %d core %d %s fell from %.12f to %.12f", tick, core, name, previous[name], value)
					}
				}
			}
		}
		if tick > 0 {
			if got, before := metricValue(metrics, "system.cpu.context_switches", tick), metricValue(metrics, "system.cpu.context_switches", tick-1); got <= before {
				t.Errorf("tick %d context switches = %v, want increase from %v", tick, got, before)
			}
		}
		for _, window := range []string{"1", "5", "15"} {
			if got, want := metricValue(metrics, "system.load.norm."+window, tick), metricValue(metrics, "system.load."+window, tick)/metricValue(metrics, "system.cpu.num_cores", tick); math.Abs(got-want) > 1e-12 {
				t.Errorf("tick %d normalized load %s = %.12f, want %.12f", tick, window, got, want)
			}
		}
		if got, want := metricValue(metrics, "system.mem.used", tick), metricValue(metrics, "system.mem.total", tick)-metricValue(metrics, "system.mem.free", tick); math.Abs(got-want) > 1e-12 {
			t.Errorf("tick %d used memory = %.12f, want %.12f", tick, got, want)
		}
		if got, want := metricValue(metrics, "system.mem.pct_usable", tick), metricValue(metrics, "system.mem.usable", tick)/metricValue(metrics, "system.mem.total", tick); math.Abs(got-want) > 1e-12 {
			t.Errorf("tick %d usable memory fraction = %.12f, want %.12f", tick, got, want)
		}
	}
	if got, then := metricValue(metrics, "system.cpu.idle", 2), metricValue(metrics, "system.cpu.idle", 1); got == then {
		t.Fatal("CPU idle stayed frozen across distinct ticks")
	}
	if got, then := metricValue(metrics, "system.mem.free", 2), metricValue(metrics, "system.mem.free", 1); got == then {
		t.Fatal("free memory stayed frozen across distinct ticks")
	}
	if got, then := metricValue(metrics, "system.load.1", 2), metricValue(metrics, "system.load.1", 1); got == then {
		t.Fatal("one-minute load stayed frozen across distinct ticks")
	}

	bad := append([]otlp.MetricResource(nil), resources...)
	badByName := metricResourcesByName(bad)
	badResource := badByName["system.cpu.idle"][2]
	badResource.Metrics = append([]otlp.Metric(nil), badResource.Metrics...)
	badResource.Metrics[0].Numbers = append([]otlp.NumberPoint(nil), badResource.Metrics[0].Numbers...)
	badResource.Metrics[0].Numbers[0].Value = 101
	badByName["system.cpu.idle"][2] = badResource
	if err := fixtureMechanicsBandError(badByName, 2); err == nil {
		t.Fatal("fixture band control accepted CPU idle outside [0,100]")
	}
}

func TestFixtureSeriesVariationIsReceiverScoped(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	world := &core.World{Shape: shape.New("", nil)}
	first := &Construct{hostName: "agent-node-a"}
	second := &Construct{hostName: "agent-node-b"}
	firstValue := first.fixtureSeriesVar(world, now, "system.mem.free", 0.10, 0.04)
	if again := first.fixtureSeriesVar(world, now, "system.mem.free", 0.10, 0.04); firstValue != again {
		t.Fatalf("receiver-scoped variation changed for the same identity: %v then %v", firstValue, again)
	}
	if secondValue := second.fixtureSeriesVar(world, now, "system.mem.free", 0.10, 0.04); firstValue == secondValue {
		t.Fatalf("shared shape engine gave distinct receivers the same variation: %v", firstValue)
	}
}

type fixtureCPUValues struct{ user, system, interrupt, iowait, idle, stolen, guest float64 }

func fixtureCPUSample(metrics map[string][]otlp.MetricResource, tick int) fixtureCPUValues {
	return fixtureCPUValues{
		user: metricValue(metrics, "system.cpu.user", tick), system: metricValue(metrics, "system.cpu.system", tick),
		interrupt: metricValue(metrics, "system.cpu.interrupt", tick), iowait: metricValue(metrics, "system.cpu.iowait", tick),
		idle: metricValue(metrics, "system.cpu.idle", tick), stolen: metricValue(metrics, "system.cpu.stolen", tick), guest: metricValue(metrics, "system.cpu.guest", tick),
	}
}

func fixtureCoreTotals(metrics map[string][]otlp.MetricResource, tick int, core string) map[string]float64 {
	out := map[string]float64{}
	wantTime := metrics["system.cpu.idle"][tick].Metrics[0].Numbers[0].Time
	for _, name := range cpuCoreTotalMetricNames {
		for _, resource := range metrics[name] {
			point := resource.Metrics[0].Numbers[0]
			if point.Attrs["core"] == core && point.Time.Equal(wantTime) {
				out[name] = point.Value
			}
		}
	}
	return out
}

func fixtureCoreCounts(metrics map[string][]otlp.MetricResource, tick int, name string) map[string]int {
	counts := map[string]int{}
	wantTime := metrics["system.cpu.idle"][tick].Metrics[0].Numbers[0].Time
	for _, resource := range metrics[name] {
		point := resource.Metrics[0].Numbers[0]
		if point.Time.Equal(wantTime) {
			core, ok := point.Attrs["core"].(string)
			if !ok {
				core = "<non-string-core>"
			}
			counts[core]++
		}
	}
	return counts
}

func metricValue(metrics map[string][]otlp.MetricResource, name string, tick int) float64 {
	return metrics[name][tick].Metrics[0].Numbers[0].Value
}

func assertFixtureMechanicsInBand(t *testing.T, metrics map[string][]otlp.MetricResource, tick int) {
	t.Helper()
	if err := fixtureMechanicsBandError(metrics, tick); err != nil {
		t.Error(err)
	}
}

func fixtureMechanicsBandError(metrics map[string][]otlp.MetricResource, tick int) error {
	for _, name := range []string{"system.cpu.guest", "system.cpu.idle", "system.cpu.interrupt", "system.cpu.iowait", "system.cpu.stolen", "system.cpu.system", "system.cpu.user"} {
		value := metricValue(metrics, name, tick)
		if value < 0 || value > 100 {
			return fmt.Errorf("%s = %v outside [0,100]", name, value)
		}
	}
	if usable, total := metricValue(metrics, "system.mem.usable", tick), metricValue(metrics, "system.mem.total", tick); usable < 0 || usable > total {
		return fmt.Errorf("usable memory = %v outside [0,%v]", usable, total)
	}
	return nil
}

var cpuFixtureMechanicNames = []string{
	"system.cpu.context_switches", "system.cpu.guest", "system.cpu.guest.total", "system.cpu.guestnice.total",
	"system.cpu.idle", "system.cpu.idle.total", "system.cpu.interrupt", "system.cpu.iowait", "system.cpu.iowait.total",
	"system.cpu.irq.total", "system.cpu.nice.total", "system.cpu.num_cores", "system.cpu.softirq.total",
	"system.cpu.steal.total", "system.cpu.stolen", "system.cpu.system", "system.cpu.system.total", "system.cpu.user", "system.cpu.user.total",
}

var cpuCoreTotalMetricNames = []string{
	"system.cpu.guest.total", "system.cpu.guestnice.total", "system.cpu.idle.total",
	"system.cpu.iowait.total", "system.cpu.irq.total", "system.cpu.nice.total",
	"system.cpu.softirq.total", "system.cpu.steal.total", "system.cpu.system.total", "system.cpu.user.total",
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

// TestFixtureEnvelopeComparisonRejectsPlacementMutation proves the immutable native
// envelope check also protects a fixture family, not only the example declaration.
func TestFixtureEnvelopeComparisonRejectsPlacementMutation(t *testing.T) {
	constructed, err := Build(&Config{HostName: "agent-node", ServiceName: "example-service", Source: "agent", IncrementsPerMinute: 0.5}, &fixture.Set{Cluster: coretest.Cluster()})
	if err != nil {
		t.Fatal(err)
	}
	capture := &otlpMetricCapture{}
	if err := constructed.Tick(context.Background(), time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), &core.World{OTLPMetrics: capture, Shape: shape.New("", nil)}); err != nil {
		t.Fatal(err)
	}
	bad := metricResourcesByName(capture.resources)["system.cpu.context_switches"][0]
	bad.Attrs = map[string]any{}
	bad.Metrics = append([]otlp.Metric(nil), bad.Metrics...)
	bad.Metrics[0].Numbers = append([]otlp.NumberPoint(nil), bad.Metrics[0].Numbers...)
	bad.Metrics[0].Numbers[0].Attrs = map[string]any{"host.name": "agent-node", "source": "agent"}
	if err := matchesCapturedEnvelope(bad, capturedEnvelopeByName(t, "system.cpu.context_switches")[0]); err == nil {
		t.Fatal("fixture envelope comparison accepted resource keys moved to the datapoint")
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
