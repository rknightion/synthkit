// SPDX-License-Identifier: AGPL-3.0-only

// cadvisor_test.go — tests for EmitContainer + EmitMachine (Task 2.5).
package nodeexp

import (
	"testing"

	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// testContainer returns a representative Container for tests.
// k8s-style: Labels has {container,id,image,name,pod,namespace,node}; NetLabels drops
// container and adds interface. docker-style: both Labels and NetLabels = {container}.
func testContainerK8s() Container {
	return Container{
		CPURequest: 0.25,
		MemLimit:   128 * 1024 * 1024,
		Labels: map[string]string{
			"container": "app",
			"id":        "/kubepods/burstable/pod1234/app",
			"image":     "ghcr.io/synthkit/app:latest",
			"name":      "000000000123",
			"namespace": "default",
			"node":      "camden",
			"pod":       "app-0",
		},
		NetLabels: map[string]string{
			// k8s net: NO container, WITH interface
			"id":        "/kubepods/burstable/pod1234/app",
			"image":     "ghcr.io/synthkit/app:latest",
			"interface": "eth0",
			"name":      "000000000123",
			"namespace": "default",
			"node":      "camden",
			"pod":       "app-0",
		},
	}
}

func testContainerDocker() Container {
	return Container{
		CPURequest: 0.5,
		MemLimit:   256 * 1024 * 1024,
		Labels: map[string]string{
			"container": "nginx",
		},
		NetLabels: map[string]string{
			"container": "nginx",
		},
	}
}

// testCadvisorBase returns a minimal base label map for cadvisor tests.
func testCadvisorBase() map[string]string {
	return map[string]string{
		"job":      "integrations/kubernetes/cadvisor",
		"instance": "camden",
	}
}

// testDockerBase returns the base label map for docker cadvisor.
func testDockerBase() map[string]string {
	return map[string]string{
		"job":      "integrations/docker",
		"instance": "camden",
	}
}

// emitContainerAndCollect runs EmitContainer once and returns all collected series.
func emitContainerAndCollect(c Container, prof CadvisorProfile) []promrw.Series {
	st := state.NewState()
	EmitContainer(st, testCadvisorBase(), c, prof, 0.5, 60, 2, testEngine())
	return st.Collect(testNow())
}

// cadvisorSeriesByName indexes collected series by metric name.
func cadvisorSeriesByName(series []promrw.Series) map[string][]promrw.Series {
	m := make(map[string][]promrw.Series)
	for _, s := range series {
		m[s.Name] = append(m[s.Name], s)
	}
	return m
}

// ── (a) CPU counter grows across ticks ───────────────────────────────────────

// TestCadvisorCPUCounterMonotonic asserts that container_cpu_usage_seconds_total is a
// cumulative counter: calling EmitContainer twice on the same state increases the value.
func TestCadvisorCPUCounterMonotonic(t *testing.T) {
	st := state.NewState()
	c := testContainerK8s()
	sh := testEngine()

	EmitContainer(st, testCadvisorBase(), c, CadvisorK8s, 0.5, 60, 2, sh)
	series1 := st.Collect(testNow())
	var cpu1 float64
	for _, s := range series1 {
		if s.Name == "container_cpu_usage_seconds_total" {
			cpu1 += s.Value
		}
	}
	if cpu1 == 0 {
		t.Fatal("container_cpu_usage_seconds_total is 0 after first EmitContainer — counter not emitted")
	}

	EmitContainer(st, testCadvisorBase(), c, CadvisorK8s, 0.5, 60, 2, sh)
	series2 := st.Collect(testNow())
	var cpu2 float64
	for _, s := range series2 {
		if s.Name == "container_cpu_usage_seconds_total" {
			cpu2 += s.Value
		}
	}

	if cpu2 <= cpu1 {
		t.Errorf("container_cpu_usage_seconds_total did not grow: tick1=%.4f tick2=%.4f", cpu1, cpu2)
	}
}

// ── (b) CadvisorK8s emitted set matches k8s names ────────────────────────────

// TestCadvisorK8sEmittedSet asserts that CadvisorK8s emits the expected k8s names
// (cfs_periods, working_set, rss, cache present) and docker-only names are absent.
func TestCadvisorK8sEmittedSet(t *testing.T) {
	series := emitContainerAndCollect(testContainerK8s(), CadvisorK8s)
	byName := cadvisorSeriesByName(series)

	mustHave := []string{
		"container_cpu_cfs_periods_total",
		"container_cpu_cfs_throttled_periods_total",
		"container_memory_working_set_bytes",
		"container_memory_rss",
		"container_memory_cache",
		"container_fs_reads_total",
		"container_fs_writes_total",
	}
	for _, name := range mustHave {
		if _, ok := byName[name]; !ok {
			t.Errorf("CadvisorK8s: expected %q to be emitted but it was not", name)
		}
	}

	// docker-only names must be absent under CadvisorK8s
	dockerOnly := []string{
		"container_fs_usage_bytes",
		"container_last_seen",
		"container_spec_memory_reservation_limit_bytes",
		"container_network_receive_errors_total",
		"container_network_transmit_errors_total",
	}
	for _, name := range dockerOnly {
		if _, ok := byName[name]; ok {
			t.Errorf("CadvisorK8s: docker-only %q must NOT be emitted but it was", name)
		}
	}
}

// ── (c) CadvisorDocker emitted set matches docker allowlist ──────────────────

// TestCadvisorDockerEmittedSet asserts that CadvisorDocker emits docker-specific names
// and excludes names that are k8s-only (not in docker allowlist).
func TestCadvisorDockerEmittedSet(t *testing.T) {
	series := emitContainerAndCollect(testContainerDocker(), CadvisorDocker)
	byName := cadvisorSeriesByName(series)

	// Docker allowlist must be present.
	dockerMustHave := []string{
		"container_cpu_usage_seconds_total",
		"container_fs_reads_total",
		"container_fs_usage_bytes",
		"container_fs_writes_total",
		"container_last_seen",
		"container_memory_usage_bytes",
		"container_network_receive_bytes_total",
		"container_network_receive_errors_total",
		"container_network_receive_packets_dropped_total",
		"container_network_transmit_bytes_total",
		"container_network_transmit_errors_total",
		"container_network_transmit_packets_dropped_total",
		"container_spec_memory_reservation_limit_bytes",
	}
	for _, name := range dockerMustHave {
		if _, ok := byName[name]; !ok {
			t.Errorf("CadvisorDocker: expected %q to be emitted but it was not", name)
		}
	}

	// k8s-only names (NOT in docker allowlist) must be absent.
	// These are names in k8sCadvisorNames but NOT in dockerCadvisorNames.
	k8sOnly := []string{
		"container_cpu_cfs_periods_total",
		"container_cpu_cfs_throttled_periods_total",
		"container_fs_reads_bytes_total",
		"container_fs_writes_bytes_total",
		"container_memory_working_set_bytes",
		"container_memory_cache",
		"container_memory_rss",
		"container_memory_swap",
		"container_network_receive_packets_total",
		"container_network_transmit_packets_total",
	}
	for _, name := range k8sOnly {
		if _, ok := byName[name]; ok {
			t.Errorf("CadvisorDocker: k8s-only %q must NOT be emitted but it was", name)
		}
	}
}

// ── (d) network series use NetLabels; container-scoped series use Labels ─────

// TestCadvisorNetworkLabelsVsContainerLabels asserts that:
//   - network series carry the labels from c.NetLabels (no "container" for k8s; has "interface")
//   - cpu series carry the labels from c.Labels (has "container")
func TestCadvisorNetworkLabelsVsContainerLabels(t *testing.T) {
	// k8s container: Labels has "container", NetLabels has "interface" but NOT "container".
	c := testContainerK8s()
	series := emitContainerAndCollect(c, CadvisorK8s)

	var cpuSeries, netSeries []promrw.Series
	for _, s := range series {
		if s.Name == "container_cpu_usage_seconds_total" {
			cpuSeries = append(cpuSeries, s)
		}
		if s.Name == "container_network_receive_bytes_total" {
			netSeries = append(netSeries, s)
		}
	}

	if len(cpuSeries) == 0 {
		t.Fatal("container_cpu_usage_seconds_total not emitted")
	}
	if len(netSeries) == 0 {
		t.Fatal("container_network_receive_bytes_total not emitted")
	}

	// CPU series must have "container" label (from c.Labels).
	if _, ok := cpuSeries[0].Labels["container"]; !ok {
		t.Error("cpu series missing 'container' label (expected from c.Labels)")
	}

	// Network series must NOT have "container" label (k8s NetLabels excludes it).
	if _, ok := netSeries[0].Labels["container"]; ok {
		t.Error("network series has 'container' label but k8s NetLabels should omit it")
	}

	// Network series must have "interface" label (from c.NetLabels).
	if _, ok := netSeries[0].Labels["interface"]; !ok {
		t.Error("network series missing 'interface' label (expected from k8s c.NetLabels)")
	}
}

// TestCadvisorDockerNetworkLabelsMatchContainer asserts that for docker, NetLabels = Labels
// so network series carry "container" (docker style).
func TestCadvisorDockerNetworkLabelsMatchContainer(t *testing.T) {
	c := testContainerDocker()
	series := emitContainerAndCollect(c, CadvisorDocker)

	for _, s := range series {
		if s.Name == "container_network_receive_bytes_total" {
			if v, ok := s.Labels["container"]; !ok || v == "" {
				t.Errorf("docker network series: expected 'container' label, got labels=%v", s.Labels)
			}
			return
		}
	}
	t.Error("container_network_receive_bytes_total not emitted for docker container")
}

// ── (e) EmitMachine ───────────────────────────────────────────────────────────

// TestEmitMachineK8sEmitsMachineMemoryOnly asserts that k8s profile emits only
// machine_memory_bytes (no machine_scrape_error, no up).
func TestEmitMachineK8sEmitsMachineMemoryOnly(t *testing.T) {
	base := map[string]string{
		"job":      "integrations/kubernetes/cadvisor",
		"instance": "camden",
		"node":     "camden",
	}
	st := state.NewState()
	EmitMachine(st, base, 8<<30, CadvisorK8s)
	series := st.Collect(testNow())

	byName := cadvisorSeriesByName(series)

	if _, ok := byName["machine_memory_bytes"]; !ok {
		t.Error("machine_memory_bytes not emitted for CadvisorK8s")
	}

	if _, ok := byName["machine_scrape_error"]; ok {
		t.Error("machine_scrape_error must not be emitted for CadvisorK8s")
	}
	if _, ok := byName["up"]; ok {
		t.Error("up must not be emitted for CadvisorK8s")
	}
}

// TestEmitMachineDockerEmitsScraperAndUp asserts that docker profile emits
// machine_memory_bytes, machine_scrape_error=0, and up=1.
func TestEmitMachineDockerEmitsScraperAndUp(t *testing.T) {
	base := testDockerBase()
	st := state.NewState()
	EmitMachine(st, base, 16<<30, CadvisorDocker)
	series := st.Collect(testNow())

	byName := cadvisorSeriesByName(series)

	if _, ok := byName["machine_memory_bytes"]; !ok {
		t.Error("machine_memory_bytes not emitted for CadvisorDocker")
	}
	if s, ok := byName["machine_scrape_error"]; !ok {
		t.Error("machine_scrape_error not emitted for CadvisorDocker")
	} else if s[0].Value != 0 {
		t.Errorf("machine_scrape_error should be 0, got %v", s[0].Value)
	}
	if s, ok := byName["up"]; !ok {
		t.Error("up not emitted for CadvisorDocker")
	} else if s[0].Value != 1 {
		t.Errorf("up should be 1, got %v", s[0].Value)
	}
}

// TestEmitMachineMemoryValue asserts that machine_memory_bytes carries the provided memTotal.
func TestEmitMachineMemoryValue(t *testing.T) {
	const memTotal = 32 << 30 // 32 GiB
	st := state.NewState()
	EmitMachine(st, testDockerBase(), memTotal, CadvisorDocker)
	for _, s := range st.Collect(testNow()) {
		if s.Name == "machine_memory_bytes" {
			if s.Value != memTotal {
				t.Errorf("machine_memory_bytes = %v, want %v", s.Value, float64(memTotal))
			}
			return
		}
	}
	t.Error("machine_memory_bytes not emitted")
}

// ── (f) counter-vs-gauge classification ──────────────────────────────────────

// cadvisorCounterNames is the authoritative set of names emitted as counters (st.Add)
// by EmitContainer, derived from cadvisor.go on 2026-06-17.
var cadvisorCounterNames = []string{
	"container_cpu_usage_seconds_total",
	"container_cpu_cfs_periods_total",
	"container_cpu_cfs_throttled_periods_total",
	"container_fs_reads_bytes_total",
	"container_fs_writes_bytes_total",
	"container_fs_reads_total",
	"container_fs_writes_total",
	"container_network_receive_bytes_total",
	"container_network_transmit_bytes_total",
	"container_network_receive_packets_total",
	"container_network_transmit_packets_total",
	"container_network_receive_packets_dropped_total",
	"container_network_transmit_packets_dropped_total",
	// docker-only error counters
	"container_network_receive_errors_total",
	"container_network_transmit_errors_total",
}

// cadvisorGaugeNames is the authoritative set of names emitted as gauges (st.Set)
// by EmitContainer + EmitMachine, derived from cadvisor.go on 2026-06-17.
var cadvisorGaugeNames = []string{
	"container_memory_working_set_bytes",
	"container_memory_cache",
	"container_memory_rss",
	"container_memory_swap",
	"container_memory_usage_bytes",
	// docker-only gauges
	"container_fs_usage_bytes",
	"container_last_seen",
	"container_spec_memory_reservation_limit_bytes",
	// machine series (EmitMachine)
	"machine_memory_bytes",
	"machine_scrape_error",
	"up",
}

// TestCadvisorCounterGaugeClassification asserts that counters use KindCounter and
// gauges use KindGauge. Uses a union-broad profile (k8s keepset is the superset for
// most names; we also test docker-only names with CadvisorDocker).
func TestCadvisorCounterGaugeClassification(t *testing.T) {
	// Use CadvisorK8s for the broad counter/gauge set from the k8s source, then
	// additionally collect docker-only names using CadvisorDocker.
	st := state.NewState()
	c := testContainerK8s()
	// Use a docker-shaped container for docker-only names.
	cd := testContainerDocker()

	EmitContainer(st, testCadvisorBase(), c, CadvisorK8s, 0.5, 60, 2, testEngine())
	EmitContainer(st, testDockerBase(), cd, CadvisorDocker, 0.5, 60, 2, testEngine())
	EmitMachine(st, testDockerBase(), 16<<30, CadvisorDocker)
	all := st.Collect(testNow())

	// Build per-name Kind index.
	nameKind := make(map[string]promrw.Kind)
	for _, s := range all {
		if existing, seen := nameKind[s.Name]; seen {
			if existing != s.Kind {
				t.Errorf("metric %q appears with conflicting Kinds: %v and %v", s.Name, existing, s.Kind)
			}
		} else {
			nameKind[s.Name] = s.Kind
		}
	}

	for _, name := range cadvisorCounterNames {
		k, ok := nameKind[name]
		if !ok {
			continue // not emitted under these profiles — skip
		}
		if k != promrw.KindCounter {
			t.Errorf("counter %q classified as Kind=%v (expected KindCounter)", name, k)
		}
	}

	for _, name := range cadvisorGaugeNames {
		k, ok := nameKind[name]
		if !ok {
			continue
		}
		if k != promrw.KindGauge {
			t.Errorf("gauge %q classified as Kind=%v (expected KindGauge)", name, k)
		}
	}

	// Sanity: no name appears in both lists.
	counterSet := make(map[string]bool, len(cadvisorCounterNames))
	for _, n := range cadvisorCounterNames {
		counterSet[n] = true
	}
	for _, n := range cadvisorGaugeNames {
		if counterSet[n] {
			t.Errorf("test data error: %q appears in both cadvisorCounterNames and cadvisorGaugeNames", n)
		}
	}
}

// TestEmitContainerBaseNotMutated asserts that the caller's base map and c.Labels/c.NetLabels
// are never mutated by EmitContainer.
func TestEmitContainerBaseNotMutated(t *testing.T) {
	base := testCadvisorBase()
	origBaseLen := len(base)

	c := testContainerK8s()
	origLabelsLen := len(c.Labels)
	origNetLabelsLen := len(c.NetLabels)

	st := state.NewState()
	EmitContainer(st, base, c, CadvisorK8s, 0.5, 60, 2, testEngine())

	if len(base) != origBaseLen {
		t.Errorf("base map mutated: len changed from %d to %d", origBaseLen, len(base))
	}
	if len(c.Labels) != origLabelsLen {
		t.Errorf("c.Labels mutated: len changed from %d to %d", origLabelsLen, len(c.Labels))
	}
	if len(c.NetLabels) != origNetLabelsLen {
		t.Errorf("c.NetLabels mutated: len changed from %d to %d", origNetLabelsLen, len(c.NetLabels))
	}
}

// TestEmitContainerK8sAllNamesInKeepSet asserts that every emitted name under CadvisorK8s
// is in keepSetCadvisor(CadvisorK8s).
func TestEmitContainerK8sAllNamesInKeepSet(t *testing.T) {
	ks := keepSetCadvisor(CadvisorK8s)
	series := emitContainerAndCollect(testContainerK8s(), CadvisorK8s)
	for _, s := range series {
		if !ks[s.Name] {
			t.Errorf("CadvisorK8s: emitted %q which is not in keepSetCadvisor(CadvisorK8s)", s.Name)
		}
	}
}

// TestEmitContainerDockerAllNamesInKeepSet asserts that every emitted name under CadvisorDocker
// is in keepSetCadvisor(CadvisorDocker).
func TestEmitContainerDockerAllNamesInKeepSet(t *testing.T) {
	ks := keepSetCadvisor(CadvisorDocker)
	series := emitContainerAndCollect(testContainerDocker(), CadvisorDocker)
	for _, s := range series {
		if !ks[s.Name] {
			t.Errorf("CadvisorDocker: emitted %q which is not in keepSetCadvisor(CadvisorDocker)", s.Name)
		}
	}
}
