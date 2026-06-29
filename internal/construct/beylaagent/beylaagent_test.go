// SPDX-License-Identifier: AGPL-3.0-only

package beylaagent

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/beyla"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
)

func TestBeylaAgent_InterfaceContract(t *testing.T) {
	c, err := Build(&Config{Mode: string(beyla.ModeKubernetes), Cluster: "demo", Node: "node-1"}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := c.Kind(); got != "beyla_agent" {
		t.Fatalf("Kind()=%q want beyla_agent", got)
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Fatalf("Signals()=%v want [Metrics]", sigs)
	}
	if c.Interval() < 60*time.Second {
		t.Fatalf("Interval()=%v want >=60s", c.Interval())
	}
}

func tickCapture(t *testing.T, cfg *Config) *coretest.MetricCapture {
	t.Helper()
	c, err := Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc
}

func TestBeylaAgent_BuildInfoGauge(t *testing.T) {
	mc := tickCapture(t, &Config{
		Mode: "kubernetes", Cluster: "demo", Node: "node-1",
		Version: "1.9.0", Revision: "abc123",
	})

	bi := mc.Find(beyla.MetricInternalBuildInfo)
	if len(bi) == 0 {
		t.Fatalf("missing %s", beyla.MetricInternalBuildInfo)
	}
	s := bi[0]
	if s.Value != 1 {
		t.Errorf("%s value=%v want 1", beyla.MetricInternalBuildInfo, s.Value)
	}
	for _, k := range []string{"goarch", "goos", "goversion", "revision", "version"} {
		if _, ok := s.Labels[k]; !ok {
			t.Errorf("%s missing build label %q; got %v", beyla.MetricInternalBuildInfo, k, s.Labels)
		}
	}
	if s.Labels["version"] != "1.9.0" {
		t.Errorf("version=%q want 1.9.0", s.Labels["version"])
	}
	if s.Labels["revision"] != "abc123" {
		t.Errorf("revision=%q want abc123", s.Labels["revision"])
	}
}

func TestBeylaAgent_GaugesAndCounters(t *testing.T) {
	mc := tickCapture(t, &Config{Mode: "kubernetes", Cluster: "demo", Node: "node-1"})

	// instrumented_processes is a gauge.
	if got := mc.Find(beyla.MetricInstrumentedProcesses); len(got) == 0 {
		t.Errorf("missing %s", beyla.MetricInstrumentedProcesses)
	}

	// map entries / max entries are gauges.
	for _, name := range []string{beyla.MetricBPFMapEntries, beyla.MetricBPFMapMaxEntries} {
		if got := mc.Find(name); len(got) == 0 {
			t.Errorf("missing gauge %s", name)
		}
	}

	// counters via state.Add (KindCounter).
	for _, name := range []string{
		beyla.MetricBPFProbeExecutions,
		beyla.MetricBPFProbeLatency,
		beyla.MetricOTelMetricExports,
		beyla.MetricOTelTraceExports,
		beyla.MetricPromHTTPRequests,
		beyla.MetricInstrumentationErrors,
		beyla.MetricBPFNetworkPackets,
		beyla.MetricBPFNetworkIgnoredPkts,
	} {
		got := mc.Find(name)
		if len(got) == 0 {
			t.Errorf("missing counter %s", name)
		}
	}
}

func TestBeylaAgent_NoLabelNetworkPacketCounters(t *testing.T) {
	mc := tickCapture(t, &Config{Mode: "standalone", Host: "vm-1"})
	for _, name := range []string{beyla.MetricBPFNetworkPackets, beyla.MetricBPFNetworkIgnoredPkts} {
		got := mc.Find(name)
		if len(got) == 0 {
			t.Fatalf("missing %s", name)
		}
		// per signals/beyla.md these carry NO labels (identity stamping must not add any).
		if len(got[0].Labels) != 0 {
			t.Errorf("%s must have no labels; got %v", name, got[0].Labels)
		}
	}
}

func TestBeylaAgent_KubernetesLabels(t *testing.T) {
	mc := tickCapture(t, &Config{Mode: "kubernetes", Cluster: "demo", Node: "node-1"})
	// A labelled internal metric should carry k8s identity + job.
	got := mc.Find(beyla.MetricInstrumentedProcesses)
	if len(got) == 0 {
		t.Fatalf("missing %s", beyla.MetricInstrumentedProcesses)
	}
	l := got[0].Labels
	if l[beyla.LabelK8sClusterName] != "demo" {
		t.Errorf("k8s_cluster_name=%q want demo", l[beyla.LabelK8sClusterName])
	}
	if l[beyla.LabelK8sNodeName] != "node-1" {
		t.Errorf("k8s_node_name=%q want node-1", l[beyla.LabelK8sNodeName])
	}
	if l[beyla.LabelJob] != "integrations/beyla" {
		t.Errorf("job=%q want integrations/beyla", l[beyla.LabelJob])
	}
}

func TestBeylaAgent_StandaloneOmitsK8s(t *testing.T) {
	mc := tickCapture(t, &Config{Mode: "standalone", Host: "vm-1"})
	got := mc.Find(beyla.MetricInstrumentedProcesses)
	if len(got) == 0 {
		t.Fatalf("missing %s", beyla.MetricInstrumentedProcesses)
	}
	l := got[0].Labels
	if _, ok := l[beyla.LabelK8sClusterName]; ok {
		t.Errorf("standalone must omit k8s_cluster_name; got %v", l)
	}
	if _, ok := l[beyla.LabelK8sNodeName]; ok {
		t.Errorf("standalone must omit k8s_node_name; got %v", l)
	}
	if l[beyla.LabelHostName] != "vm-1" {
		t.Errorf("host_name=%q want vm-1", l[beyla.LabelHostName])
	}
}

func TestBeylaAgent_ModeDefaultsKubernetes(t *testing.T) {
	// Empty mode defaults to kubernetes.
	mc := tickCapture(t, &Config{Cluster: "demo", Node: "node-1"})
	got := mc.Find(beyla.MetricInstrumentedProcesses)
	if len(got) == 0 {
		t.Fatalf("missing %s", beyla.MetricInstrumentedProcesses)
	}
	if got[0].Labels[beyla.LabelJob] != "integrations/beyla" {
		t.Errorf("default mode should be kubernetes with job=integrations/beyla; got %v", got[0].Labels)
	}
}

func TestBeylaAgent_NoAvoidedServices(t *testing.T) {
	// The agent construct must NOT emit beyla_avoided_services (that is the lane's job).
	mc := tickCapture(t, &Config{Mode: "kubernetes", Cluster: "demo", Node: "node-1"})
	if got := mc.Find(beyla.MetricAvoidedServices); len(got) != 0 {
		t.Errorf("beyla_agent must NOT emit %s (lane owns it); got %d series", beyla.MetricAvoidedServices, len(got))
	}
}

// ── Per-series value variation ────────────────────────────────────────────────

// TestBeylaAgent_PeerSeriesDistinct asserts that peer series (same metric name, different
// label combos) emit DISTINCT values — not lockstep identical constants.
func TestBeylaAgent_PeerSeriesDistinct(t *testing.T) {
	// Use 4 instrumented processes so we get 4 peer series for instrumentation_errors.
	cfg := &Config{Mode: "kubernetes", Cluster: "demo", Node: "node-1", InstrumentedProcesses: 4}
	c, err := Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// beyla_bpf_probe_executions: 2 probes → 2 peer series. Values must differ.
	probeExec := mc.Find(beyla.MetricBPFProbeExecutions)
	if len(probeExec) < 2 {
		t.Fatalf("%s: expected >=2 series, got %d", beyla.MetricBPFProbeExecutions, len(probeExec))
	}
	if probeExec[0].Value == probeExec[1].Value {
		t.Errorf("%s: peer series have identical value=%.4f — seriesVar not applied",
			beyla.MetricBPFProbeExecutions, probeExec[0].Value)
	}

	// beyla_bpf_probe_latency: 2 probes → 2 peer series. Values must differ.
	probeLatency := mc.Find(beyla.MetricBPFProbeLatency)
	if len(probeLatency) < 2 {
		t.Fatalf("%s: expected >=2 series, got %d", beyla.MetricBPFProbeLatency, len(probeLatency))
	}
	if probeLatency[0].Value == probeLatency[1].Value {
		t.Errorf("%s: peer series have identical value=%.4f — seriesVar not applied",
			beyla.MetricBPFProbeLatency, probeLatency[0].Value)
	}

	// beyla_bpf_map_entries: 2 maps → 2 peer series. Values must differ.
	mapEntries := mc.Find(beyla.MetricBPFMapEntries)
	if len(mapEntries) < 2 {
		t.Fatalf("%s: expected >=2 series, got %d", beyla.MetricBPFMapEntries, len(mapEntries))
	}
	if mapEntries[0].Value == mapEntries[1].Value {
		t.Errorf("%s: peer series have identical value=%.4f — seriesVar not applied",
			beyla.MetricBPFMapEntries, mapEntries[0].Value)
	}

	// beyla_instrumentation_errors: 4 processes → 4 peer series. Not all identical.
	instrErrors := mc.Find(beyla.MetricInstrumentationErrors)
	if len(instrErrors) < 2 {
		t.Fatalf("%s: expected >=2 series, got %d", beyla.MetricInstrumentationErrors, len(instrErrors))
	}
	allSame := true
	v0 := instrErrors[0].Value
	for _, s := range instrErrors[1:] {
		if s.Value != v0 {
			allSame = false
			break
		}
	}
	if allSame {
		t.Errorf("%s: all peer series have identical value=%.4f — seriesVar not applied",
			beyla.MetricInstrumentationErrors, v0)
	}
}

// TestBeylaAgent_SeriesDrift asserts that per-series values drift across 30 ticks at
// 13-minute steps — at least 5 distinct values per tracked metric over the run.
func TestBeylaAgent_SeriesDrift(t *testing.T) {
	cfg := &Config{Mode: "kubernetes", Cluster: "demo", Node: "node-1"}
	c, err := Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	base := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	const ticks = 30
	const step = 13 * time.Minute

	// Track distinct values per metric (pick first series of each).
	seenProbeExec := map[float64]struct{}{}
	seenMapEntries := map[float64]struct{}{}

	for i := range ticks {
		now := base.Add(time.Duration(i) * step)
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		if err := c.Tick(context.Background(), now, world); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		if ss := mc.Find(beyla.MetricBPFProbeExecutions); len(ss) > 0 {
			seenProbeExec[ss[0].Value] = struct{}{}
		}
		if ss := mc.Find(beyla.MetricBPFMapEntries); len(ss) > 0 {
			seenMapEntries[ss[0].Value] = struct{}{}
		}
	}

	const minDistinct = 5
	if got := len(seenProbeExec); got < minDistinct {
		t.Errorf("%s: only %d distinct values over %d ticks (want >=%d) — no drift",
			beyla.MetricBPFProbeExecutions, got, ticks, minDistinct)
	}
	if got := len(seenMapEntries); got < minDistinct {
		t.Errorf("%s: only %d distinct values over %d ticks (want >=%d) — no drift",
			beyla.MetricBPFMapEntries, got, ticks, minDistinct)
	}
}
