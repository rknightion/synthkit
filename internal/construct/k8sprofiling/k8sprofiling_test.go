// SPDX-License-Identifier: AGPL-3.0-only

package k8sprofiling_test

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/k8sprofiling"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/pyroscope"
	"github.com/rknightion/synthkit/internal/shape"
	psink "github.com/rknightion/synthkit/internal/sink/pyroscope"
)

// PyroscopeCapture is a test core.PyroscopeWriter that records every Write call.
type PyroscopeCapture struct {
	Batches [][]psink.Series
}

func (pc *PyroscopeCapture) Write(_ context.Context, series []psink.Series) error {
	cp := make([]psink.Series, len(series))
	copy(cp, series)
	pc.Batches = append(pc.Batches, cp)
	return nil
}

// allSeries flattens all captured batches into a single slice.
func (pc *PyroscopeCapture) allSeries() []psink.Series {
	var out []psink.Series
	for _, b := range pc.Batches {
		out = append(out, b...)
	}
	return out
}

// labelVal returns the value of key in a series' Labels, or "".
func labelVal(s psink.Series, key string) string {
	for _, lp := range s.Labels {
		if lp.Name == key {
			return lp.Value
		}
	}
	return ""
}

// hasLabel reports whether a label key is present in a series.
func hasLabel(s psink.Series, key string) bool {
	for _, lp := range s.Labels {
		if lp.Name == key {
			return true
		}
	}
	return false
}

// testWorld returns a *core.World wired to pc with a default shape engine and no metrics/logs.
func testWorld(pc *PyroscopeCapture) *core.World {
	return &core.World{
		Shape:     shape.New("", nil),
		Pyroscope: pc,
	}
}

// testCluster returns a cluster with 2 workloads, 2 nodes, and profiling feature enabled.
func testCluster(profilingEnabled bool) *fixture.Cluster {
	features := map[string]bool{}
	if profilingEnabled {
		features["profiling"] = true
	}
	return &fixture.Cluster{
		Name: "test-cluster",
		Nodes: []fixture.Node{
			{Hostname: "ip-10-0-0-1.us-east-1.compute.internal"},
			{Hostname: "ip-10-0-0-2.us-east-1.compute.internal"},
		},
		Workloads: []fixture.Workload{
			{
				Name:      "frontend",
				Namespace: "default",
				Replicas:  2,
				PodNames:  []string{"frontend-abc12", "frontend-def34"},
				NodeIdx:   []int{0, 1},
			},
			{
				Name:      "backend",
				Namespace: "backend",
				Replicas:  1,
				PodNames:  []string{"backend-xyz99"},
				NodeIdx:   []int{0},
			},
		},
		K8sMonitoring: fixture.K8sMonitoring{
			Enabled:  true,
			Features: features,
		},
	}
}

// testClusterWithFeatures returns a cluster with 2 workloads having the specified features and
// runtimes.  wl0Runtime and wl1Runtime are applied to the first and second workload respectively.
func testClusterWithFeatures(features map[string]bool, wl0Runtime, wl1Runtime string) *fixture.Cluster {
	return &fixture.Cluster{
		Name: "test-cluster",
		Nodes: []fixture.Node{
			{Hostname: "ip-10-0-0-1.us-east-1.compute.internal"},
			{Hostname: "ip-10-0-0-2.us-east-1.compute.internal"},
		},
		Workloads: []fixture.Workload{
			{
				Name:      "frontend",
				Namespace: "default",
				Replicas:  2,
				PodNames:  []string{"frontend-abc12", "frontend-def34"},
				NodeIdx:   []int{0, 1},
				Runtime:   wl0Runtime,
			},
			{
				Name:      "backend",
				Namespace: "backend",
				Replicas:  1,
				PodNames:  []string{"backend-xyz99"},
				NodeIdx:   []int{0},
				Runtime:   wl1Runtime,
			},
		},
		K8sMonitoring: fixture.K8sMonitoring{
			Enabled:  true,
			Features: features,
		},
	}
}

// ── (a) One series per pod ──────────────────────────────────────────────────────────

func TestOneSeriesPerPod(t *testing.T) {
	cl := testCluster(true)
	c, err := k8sprofiling.New(nil, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pc := &PyroscopeCapture{}
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, testWorld(pc)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Total pods across workloads: frontend (2) + backend (1) = 3.
	got := pc.allSeries()
	if len(got) != 3 {
		t.Errorf("expected 3 series (one per pod), got %d", len(got))
	}

	// Each series must have a non-empty pod label.
	for _, s := range got {
		if v := labelVal(s, "pod"); v == "" {
			t.Errorf("series missing pod label: %v", s.Labels)
		}
	}
}

// ── (b) Required labels: source, __name__, no blueprint, no span_id ───────────────

func TestRequiredLabels(t *testing.T) {
	cl := testCluster(true)
	c, err := k8sprofiling.New(nil, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pc := &PyroscopeCapture{}
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, testWorld(pc)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	got := pc.allSeries()
	if len(got) == 0 {
		t.Fatal("no series emitted")
	}

	for _, s := range got {
		// Must have source=alloy/pyroscope.ebpf
		if v := labelVal(s, "source"); v != "alloy/pyroscope.ebpf" {
			t.Errorf("source=%q, want alloy/pyroscope.ebpf", v)
		}
		// Must have __name__=process_cpu
		if v := labelVal(s, "__name__"); v != "process_cpu" {
			t.Errorf("__name__=%q, want process_cpu", v)
		}
		// Must NOT carry blueprint label (ScopeSubstrate)
		if hasLabel(s, "blueprint") {
			t.Errorf("series must not carry blueprint label (ScopeSubstrate): %v", s.Labels)
		}
		// Must NOT carry span_id (eBPF lane is not span-correlated)
		if hasLabel(s, "span_id") {
			t.Errorf("series must not carry span_id label (eBPF is not span-correlated): %v", s.Labels)
		}
		// Must have cluster and k8s_cluster_name
		if v := labelVal(s, "cluster"); v != cl.Name {
			t.Errorf("cluster=%q, want %q", v, cl.Name)
		}
		if v := labelVal(s, "k8s_cluster_name"); v != cl.Name {
			t.Errorf("k8s_cluster_name=%q, want %q", v, cl.Name)
		}
		// Must have a non-nil Profile
		if s.Profile == nil {
			t.Errorf("series has nil Profile")
		}
	}
}

// ── (c) Profiling gate: Features["profiling"]=false ⇒ no emission ─────────────────

func TestProfilingGateOff(t *testing.T) {
	// Absent features map (nil Features).
	cl := testCluster(false)
	c, err := k8sprofiling.New(nil, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pc := &PyroscopeCapture{}
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, testWorld(pc)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if got := pc.allSeries(); len(got) != 0 {
		t.Errorf("expected 0 series when profiling feature is off, got %d", len(got))
	}
}

// TestProfilingGateAbsentFeatures tests the nil Features map case.
func TestProfilingGateAbsentFeatures(t *testing.T) {
	cl := testCluster(false)
	cl.K8sMonitoring.Features = nil // nil map
	c, err := k8sprofiling.New(nil, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pc := &PyroscopeCapture{}
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, testWorld(pc)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if got := pc.allSeries(); len(got) != 0 {
		t.Errorf("expected 0 series when Features is nil, got %d", len(got))
	}
}

// ── (d) nil Pyroscope writer ⇒ no panic, returns nil ─────────────────────────────

func TestNilPyroscopeWriter(t *testing.T) {
	cl := testCluster(true)
	c, err := k8sprofiling.New(nil, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := &core.World{Shape: shape.New("", nil)} // Pyroscope is nil
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick with nil Pyroscope: %v", err)
	}
}

// ── (e) nil Cluster ⇒ New returns error ──────────────────────────────────────────

func TestBuildNilCluster(t *testing.T) {
	_, err := k8sprofiling.New(nil, &fixture.Set{})
	if err == nil {
		t.Error("expected error when fx.Cluster is nil, got nil")
	}
}

// ── (f) Kind / Signals / Interval metadata ────────────────────────────────────────

func TestKindAndSignals(t *testing.T) {
	cl := testCluster(true)
	c, err := k8sprofiling.New(nil, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Kind() != "k8s_profiling" {
		t.Errorf("Kind()=%q, want k8s_profiling", c.Kind())
	}
	sigs := c.Signals()
	hasProfiles := false
	for _, s := range sigs {
		if s == core.PyroscopeProfiles {
			hasProfiles = true
		}
	}
	if !hasProfiles {
		t.Error("Signals() does not include PyroscopeProfiles")
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v, want 60s", c.Interval())
	}
}

// ── (g) Node label matches the node hostname in the fixture ──────────────────────

func TestNodeLabelMatchesFixture(t *testing.T) {
	cl := testCluster(true)
	c, err := k8sprofiling.New(nil, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pc := &PyroscopeCapture{}
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, testWorld(pc)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Build map of node hostnames from the fixture.
	nodeHostnames := map[string]bool{}
	for _, n := range cl.Nodes {
		nodeHostnames[n.Hostname] = true
	}

	for _, s := range pc.allSeries() {
		node := labelVal(s, "node")
		if node == "" {
			t.Errorf("series missing node label: %v", s.Labels)
			continue
		}
		if !nodeHostnames[node] {
			t.Errorf("series node=%q not in fixture nodes %v", node, nodeHostnames)
		}
	}
}

// ── (h) eBPF language label ────────────────────────────────────────────────────────
//
// A workload with Runtime="python" must have language=python in its eBPF series.
// A workload with Runtime="" must NOT have a language label at all (I13).

func TestEBPFLanguageLabel(t *testing.T) {
	cl := testClusterWithFeatures(
		map[string]bool{"profiling": true},
		"python", // wl0 (frontend) — Runtime=python
		"",       // wl1 (backend)  — Runtime="" (omit language)
	)
	c, err := k8sprofiling.New(nil, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pc := &PyroscopeCapture{}
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, testWorld(pc)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	got := pc.allSeries()
	if len(got) == 0 {
		t.Fatal("no series emitted")
	}

	for _, s := range got {
		pod := labelVal(s, "pod")
		lang := labelVal(s, "language")
		langPresent := hasLabel(s, "language")

		// frontend pods (wl0, Runtime=python) must have language=python.
		if pod == "frontend-abc12" || pod == "frontend-def34" {
			if lang != "python" {
				t.Errorf("pod %s: want language=python, got %q (present=%v)", pod, lang, langPresent)
			}
		}

		// backend pod (wl1, Runtime="") must NOT have language label (I13).
		if pod == "backend-xyz99" {
			if langPresent {
				t.Errorf("pod %s: language label must be absent when Runtime is empty, got language=%q", pod, lang)
			}
		}
	}
}

// ── (i) pprof-scrape lane ─────────────────────────────────────────────────────────
//
// Features{profiling_pprof:true} + Runtime="go" workload must emit:
//   - goroutine:goroutine:count:goroutine:count (SINGULAR, not plural goroutines)
//   - source=alloy/pyroscope.pprof
//   - profiles_grafana_com_scrape=true
//   - NO blueprint label
//
// A Runtime="jvm" workload with the same feature must emit NO pprof series.

func TestPprofLane_GoWorkload(t *testing.T) {
	cl := testClusterWithFeatures(
		map[string]bool{"profiling_pprof": true},
		"go",  // wl0 (frontend) — Go runtime → must get pprof series
		"jvm", // wl1 (backend)  — JVM runtime → must NOT get pprof series
	)
	c, err := k8sprofiling.New(nil, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pc := &PyroscopeCapture{}
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, testWorld(pc)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	got := pc.allSeries()
	if len(got) == 0 {
		t.Fatal("no series emitted")
	}

	// Collect profile type selectors emitted per workload.
	frontendSelectors := map[string]bool{}
	backendSelectors := map[string]bool{}
	for _, s := range got {
		svcName := labelVal(s, "service_name")
		pt := labelVal(s, "__profile_type__")
		if svcName == "frontend" {
			frontendSelectors[pt] = true
		}
		if svcName == "backend" {
			backendSelectors[pt] = true
		}
	}

	// JVM workload must emit NO pprof series.
	if len(backendSelectors) != 0 {
		t.Errorf("jvm workload (backend) must emit no pprof series, got selectors: %v", backendSelectors)
	}

	// Go workload must emit goroutine SINGULAR.
	singularSelector := pyroscope.GoroutinePprof.Selector() // goroutine:goroutine:count:goroutine:count
	if !frontendSelectors[singularSelector] {
		t.Errorf("pprof lane: want %q in emitted profile types for go workload, got: %v", singularSelector, frontendSelectors)
	}

	// Go workload must NOT emit goroutines PLURAL (SDK path).
	pluralSelector := pyroscope.GoroutinesSDK.Selector() // goroutines:goroutine:count:goroutine:count
	if frontendSelectors[pluralSelector] {
		t.Errorf("pprof lane: must NOT emit plural %q (SDK path), got it in: %v", pluralSelector, frontendSelectors)
	}

	// Verify required labels on all Go-workload series.
	for _, s := range got {
		if labelVal(s, "service_name") != "frontend" {
			continue
		}
		if v := labelVal(s, "source"); v != "alloy/pyroscope.pprof" {
			t.Errorf("source=%q, want alloy/pyroscope.pprof", v)
		}
		if v := labelVal(s, "profiles_grafana_com_scrape"); v != "true" {
			t.Errorf("profiles_grafana_com_scrape=%q, want true", v)
		}
		if hasLabel(s, "blueprint") {
			t.Errorf("pprof series must not carry blueprint label (ScopeSubstrate): %v", s.Labels)
		}
		if hasLabel(s, "span_id") {
			t.Errorf("pprof series must not carry span_id: %v", s.Labels)
		}
		if s.Profile == nil {
			t.Errorf("pprof series has nil Profile")
		}
	}
}

// ── (j) Java lane ─────────────────────────────────────────────────────────────────
//
// Features{profiling_java:true} + Runtime="jvm" workload must emit:
//   - source=alloy/pyroscope.java
//   - pyroscope_spy=alloy.java
//   - jfr_event=itimer
//   - service_instance_id=<namespace>.<pod>.<container>
//   - mutex:contentions:count:mutex:count (Java period unit)
//   - NO blueprint label

func TestJavaLane(t *testing.T) {
	cl := testClusterWithFeatures(
		map[string]bool{"profiling_java": true},
		"go",  // wl0 (frontend) — NOT JVM → must emit no java series
		"jvm", // wl1 (backend)  — JVM → must get java series
	)
	c, err := k8sprofiling.New(nil, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pc := &PyroscopeCapture{}
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, testWorld(pc)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	got := pc.allSeries()
	if len(got) == 0 {
		t.Fatal("no series emitted")
	}

	// Go workload must emit no java series.
	for _, s := range got {
		if labelVal(s, "service_name") == "frontend" {
			t.Errorf("go workload (frontend) must emit no java series, got: %v", s.Labels)
		}
	}

	// All emitted series should be for backend (JVM).
	jvmSelectors := map[string]bool{}
	for _, s := range got {
		if labelVal(s, "service_name") != "backend" {
			continue
		}
		jvmSelectors[labelVal(s, "__profile_type__")] = true

		// Required labels.
		if v := labelVal(s, "source"); v != "alloy/pyroscope.java" {
			t.Errorf("source=%q, want alloy/pyroscope.java", v)
		}
		if v := labelVal(s, "pyroscope_spy"); v != "alloy.java" {
			t.Errorf("pyroscope_spy=%q, want alloy.java", v)
		}
		if v := labelVal(s, "jfr_event"); v != "itimer" {
			t.Errorf("jfr_event=%q, want itimer", v)
		}
		// service_instance_id = <namespace>.<pod>.<container>
		// backend workload: namespace=backend, pod=backend-xyz99, container=backend
		wantSvcInstanceID := "backend.backend-xyz99.backend"
		if v := labelVal(s, "service_instance_id"); v != wantSvcInstanceID {
			t.Errorf("service_instance_id=%q, want %q", v, wantSvcInstanceID)
		}
		if hasLabel(s, "blueprint") {
			t.Errorf("java series must not carry blueprint label (ScopeSubstrate): %v", s.Labels)
		}
		if hasLabel(s, "span_id") {
			t.Errorf("java series must not carry span_id: %v", s.Labels)
		}
		if s.Profile == nil {
			t.Errorf("java series has nil Profile")
		}
	}

	// Must include Java mutex contention type (mutex:contentions:count:mutex:count).
	javaMutexSelector := pyroscope.JavaMutexContentions.Selector() // mutex:contentions:count:mutex:count
	if !jvmSelectors[javaMutexSelector] {
		t.Errorf("java lane: want %q in emitted types, got: %v", javaMutexSelector, jvmSelectors)
	}
}

// ── (k) Independent feature toggles — none set ⇒ no emission ────────────────────

func TestAllFeatureGatesOff(t *testing.T) {
	cl := testClusterWithFeatures(map[string]bool{}, "go", "jvm")
	c, err := k8sprofiling.New(nil, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pc := &PyroscopeCapture{}
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, testWorld(pc)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := pc.allSeries(); len(got) != 0 {
		t.Errorf("expected 0 series with no feature gates set, got %d", len(got))
	}
}

// ── (l) Multiple lanes simultaneously ────────────────────────────────────────────
//
// When profiling + profiling_pprof + profiling_java are ALL enabled, each applicable
// lane fires: eBPF for all pods, pprof for go pods, java for jvm pods.

func TestMultipleLanesTogether(t *testing.T) {
	cl := testClusterWithFeatures(
		map[string]bool{
			"profiling":       true,
			"profiling_pprof": true,
			"profiling_java":  true,
		},
		"go",  // wl0 (frontend, 2 pods): eBPF + pprof (no java)
		"jvm", // wl1 (backend, 1 pod):  eBPF + java (no pprof)
	)
	c, err := k8sprofiling.New(nil, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pc := &PyroscopeCapture{}
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, testWorld(pc)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	got := pc.allSeries()
	if len(got) == 0 {
		t.Fatal("no series emitted with all three lanes enabled")
	}

	// Count emitter sources.
	sourceCount := map[string]int{}
	for _, s := range got {
		src := labelVal(s, "source")
		sourceCount[src]++
	}

	// eBPF: 3 pods (2 frontend + 1 backend), 1 type each → 3 eBPF series.
	if sourceCount["alloy/pyroscope.ebpf"] != 3 {
		t.Errorf("eBPF series: want 3, got %d", sourceCount["alloy/pyroscope.ebpf"])
	}
	// pprof: 2 go pods × len(RuntimeTypes("go")) series each — just assert > 0.
	if sourceCount["alloy/pyroscope.pprof"] == 0 {
		t.Error("pprof lane: want >0 series for go workload, got 0")
	}
	// java: 1 jvm pod × len(RuntimeTypes("jvm")) series — just assert > 0.
	if sourceCount["alloy/pyroscope.java"] == 0 {
		t.Error("java lane: want >0 series for jvm workload, got 0")
	}
}

// ── (m) cpu_hotspot failure mode amplifies process_cpu total ──────────────────────
//
// When the cpu_hotspot mode is active for the cluster, the process_cpu profile total
// must be strictly greater than without the mode. The mode is activated via a scheduled
// incident window that covers the test's "now" time.

func TestCPUHotspotModeRaisesProcessCPUTotal(t *testing.T) {
	cl := testCluster(true) // features["profiling"]=true → eBPF lane (process_cpu only)

	c, err := k8sprofiling.New(nil, &fixture.Set{Cluster: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)

	// Baseline: no failure mode.
	pcBase := &PyroscopeCapture{}
	baseWorld := testWorld(pcBase)
	if err := c.Tick(context.Background(), now, baseWorld); err != nil {
		t.Fatalf("Tick (baseline): %v", err)
	}
	baseTotal := profileTotal(pcBase.allSeries())

	// Active: cpu_hotspot scheduled to cover `now`, scoped to the cluster name.
	// Format: mode@<RFC3339>/dur#intensity@scope
	incident := "cpu_hotspot@2026-06-15T09:00:00Z/2h#1.0@" + cl.Name
	pcHot := &PyroscopeCapture{}
	hotWorld := &core.World{
		Shape:     shape.New("", []string{incident}),
		Pyroscope: pcHot,
	}
	if err := c.Tick(context.Background(), now, hotWorld); err != nil {
		t.Fatalf("Tick (cpu_hotspot): %v", err)
	}
	hotTotal := profileTotal(pcHot.allSeries())

	if hotTotal <= baseTotal {
		t.Errorf("cpu_hotspot mode must raise process_cpu total: base=%d hot=%d", baseTotal, hotTotal)
	}
}

// profileTotal sums the Value fields across all samples in all series.
func profileTotal(series []psink.Series) int64 {
	var total int64
	for _, s := range series {
		if s.Profile == nil {
			continue
		}
		for _, sm := range s.Profile.Sample {
			for _, v := range sm.Value {
				total += v
			}
		}
	}
	return total
}
