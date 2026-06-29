// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"slices"
	"testing"
	"time"

	pprofpb "github.com/rknightion/synthkit/internal/pyroscope/pprofpb"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/pyroscope"
	"github.com/rknightion/synthkit/internal/shape"
	psink "github.com/rknightion/synthkit/internal/sink/pyroscope"
)

// pyroCapture is a core.PyroscopeWriter test double that records every Write call.
type pyroCapture struct {
	batches [][]psink.Series
}

func (c *pyroCapture) Write(_ context.Context, series []psink.Series) error {
	cp := make([]psink.Series, len(series))
	copy(cp, series)
	c.batches = append(c.batches, cp)
	return nil
}

// all flattens every captured series across all Write calls.
func (c *pyroCapture) all() []psink.Series {
	var out []psink.Series
	for _, b := range c.batches {
		out = append(out, b...)
	}
	return out
}

// labelValue returns the value of a named label from a Series, or "".
func pyroLabelValue(s psink.Series, name string) string {
	for _, lp := range s.Labels {
		if lp.Name == name {
			return lp.Value
		}
	}
	return ""
}

// spanIDsInProfile decodes all span_id sample-label values from a pprof Profile.
func spanIDsInProfile(prof *pprofpb.Profile) []string {
	if prof == nil {
		return nil
	}
	var spanKeyIdx int64 = -1
	for i, s := range prof.StringTable {
		if s == "span_id" {
			spanKeyIdx = int64(i)
			break
		}
	}
	if spanKeyIdx < 0 {
		return nil
	}
	set := map[string]bool{}
	for _, sample := range prof.Sample {
		for _, lbl := range sample.Label {
			if lbl.Key == spanKeyIdx {
				if int(lbl.Str) < len(prof.StringTable) {
					set[prof.StringTable[lbl.Str]] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	return out
}

// buildPyroApp builds an app workload with:
//   - "web" entry node (type=web, runtime=go, pyroscope enabled + span_profiles)
//   - "db" leaf node (type=db, runtime=go)
//
// Returns the workload and a populated ledger with at least one request seeded.
func buildPyroApp(t *testing.T) (*Workload, *ledger.Ledger) {
	t.Helper()
	cfg := &Config{
		Traffic: Traffic{OffPeakRPS: 10, PeakRPS: 50},
		Services: []ServiceNode{
			{
				Name:    "web",
				Type:    "web",
				Runtime: "go",
				Entry:   true,
				Calls:   []string{"db"},
				Pyroscope: &pyroscope.ProfilingCfg{
					Enabled:      true,
					Runtime:      "go",
					SpanProfiles: true,
				},
			},
			{
				Name:    "db",
				Type:    "db",
				Runtime: "go",
			},
		},
	}
	w := buildApp(t, cfg)

	eng := shape.New("", nil)
	led := ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())

	// Seed at least one request so span-profile golden-thread checks are non-vacuous.
	_ = eng // eng used for shape only (ledger.New holds its own reference)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	for range 5 {
		led.Mint(now)
	}
	return w, led
}

// worldWithPyro builds a World wired to the given captures including a PyroscopeWriter.
func worldWithPyro(mc *coretest.MetricCapture, pc *pyroCapture, led *ledger.Ledger) *core.World {
	world := coretest.World(mc, nil, nil)
	world.Pyroscope = pc
	world.Ledger = led
	return world
}

// ── Tests ──────────────────────────────────────────────────────────────────────────────────────

// TestAppPyroscope_SignalsContainsPyroscopeProfiles (a): Signals() includes PyroscopeProfiles
// when at least one node has pyroscope enabled in sdk mode.
func TestAppPyroscope_SignalsContainsPyroscopeProfiles(t *testing.T) {
	w, _ := buildPyroApp(t)
	sigs := w.Signals()
	if !slices.Contains(sigs, core.PyroscopeProfiles) {
		t.Fatalf("Signals() does not contain PyroscopeProfiles: %v", sigs)
	}
}

// TestAppPyroscope_SignalsScrapedModeOmitted: scraped mode must NOT advertise PyroscopeProfiles.
func TestAppPyroscope_SignalsScrapedModeOmitted(t *testing.T) {
	cfg := &Config{
		Services: []ServiceNode{
			{
				Name:  "web",
				Type:  "web",
				Entry: true,
				Pyroscope: &pyroscope.ProfilingCfg{
					Enabled: true,
					Mode:    "scraped",
					Runtime: "go",
				},
			},
		},
	}
	w := buildApp(t, cfg)
	sigs := w.Signals()
	if slices.Contains(sigs, core.PyroscopeProfiles) {
		t.Fatal("scraped mode must NOT include PyroscopeProfiles in Signals()")
	}
}

// TestAppPyroscope_SignalsNoPyroConfig: no pyroscope config on any node → no PyroscopeProfiles.
func TestAppPyroscope_SignalsNoPyroConfig(t *testing.T) {
	cfg := &Config{
		Services: []ServiceNode{
			{Name: "web", Type: "web", Entry: true},
		},
	}
	w := buildApp(t, cfg)
	sigs := w.Signals()
	if slices.Contains(sigs, core.PyroscopeProfiles) {
		t.Fatal("no pyroscope config must NOT include PyroscopeProfiles")
	}
}

// TestAppPyroscope_WebNodeEmitsProcessCPUAndGoroutines (b): after Tick with a populated ledger,
// the pyro capture includes series for process_cpu and goroutines profile types for the web node.
func TestAppPyroscope_WebNodeEmitsProcessCPUAndGoroutines(t *testing.T) {
	w, led := buildPyroApp(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := worldWithPyro(mc, pc, led)

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	series := pc.all()
	if len(series) == 0 {
		t.Fatal("no Pyroscope series emitted")
	}

	// Collect profile types for "web" service.
	webProfileTypes := map[string]bool{}
	for _, s := range series {
		if pyroLabelValue(s, "service_name") == "web" {
			pt := pyroLabelValue(s, "__profile_type__")
			if pt != "" {
				webProfileTypes[pt] = true
			}
		}
	}

	wantTypes := []string{
		"process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		"goroutines:goroutine:count:goroutine:count",
	}
	for _, want := range wantTypes {
		if !webProfileTypes[want] {
			t.Errorf("web node missing __profile_type__=%q (got %v)", want, webProfileTypes)
		}
	}
}

// TestAppPyroscope_ServiceNameIsNodeName (b cont.): service_name label equals the node's name.
func TestAppPyroscope_ServiceNameIsNodeName(t *testing.T) {
	w, led := buildPyroApp(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := worldWithPyro(mc, pc, led)

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	for _, s := range pc.all() {
		svc := pyroLabelValue(s, "service_name")
		if svc != "web" {
			t.Errorf("series service_name=%q want \"web\" (only web node has pyroscope enabled)", svc)
		}
	}
}

// TestAppPyroscope_GoSpyLabel (c): for runtime=go, every emitted series carries pyroscope_spy=gospy.
func TestAppPyroscope_GoSpyLabel(t *testing.T) {
	w, led := buildPyroApp(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := worldWithPyro(mc, pc, led)

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	series := pc.all()
	if len(series) == 0 {
		t.Fatal("no Pyroscope series emitted")
	}
	for _, s := range series {
		if pyroLabelValue(s, "pyroscope_spy") != "gospy" {
			t.Errorf("series %q missing pyroscope_spy=gospy (labels: %v)",
				pyroLabelValue(s, "__profile_type__"), s.Labels)
		}
	}
}

// TestAppPyroscope_SpanProfileGoldenThread (d): span_id sample-label values in emitted profiles
// must be drawn from the seeded ledger requests — never invented.
func TestAppPyroscope_SpanProfileGoldenThread(t *testing.T) {
	w, led := buildPyroApp(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	// Collect the full set of valid span IDs from the ledger.
	reqs := led.ActiveFor(w.Name(), now, interval)
	if len(reqs) == 0 {
		t.Skip("no active requests in ledger window")
	}
	validSpanIDs := map[string]bool{}
	for _, r := range reqs {
		if r.SpanID != "" {
			validSpanIDs[r.SpanID] = true
		}
		for _, c := range r.Calls {
			if c.SpanID != "" {
				validSpanIDs[c.SpanID] = true
			}
			if c.PeerSpanID != "" {
				validSpanIDs[c.PeerSpanID] = true
			}
		}
	}

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := worldWithPyro(mc, pc, led)

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	series := pc.all()
	if len(series) == 0 {
		t.Fatal("no Pyroscope series emitted")
	}

	var sawSpanLabel bool
	for _, s := range series {
		ids := spanIDsInProfile(s.Profile)
		for _, sid := range ids {
			sawSpanLabel = true
			if !validSpanIDs[sid] {
				t.Errorf("profile %q contains span_id=%q not in any minted request (invented ID)",
					pyroLabelValue(s, "__profile_type__"), sid)
			}
		}
	}
	// The ledger is seeded with non-empty SpanIDs and the web node has span_profiles=true,
	// so span labels MUST appear in at least one emitted profile.
	if !sawSpanLabel {
		t.Fatal("no span_id labels found in emitted profiles — expected span labels from seeded ledger (span_profiles=true)")
	}
}

// TestAppPyroscope_EntryNodeSpanIDIsRootSpanID: for the entry node, span IDs used in profiles
// must be the root SpanIDs from the minted requests (r.SpanID — the backend server span).
func TestAppPyroscope_EntryNodeSpanIDIsRootSpanID(t *testing.T) {
	w, led := buildPyroApp(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	reqs := led.ActiveFor(w.Name(), now, interval)
	if len(reqs) == 0 {
		t.Skip("no active requests in ledger window")
	}
	rootSpanIDs := map[string]bool{}
	for _, r := range reqs {
		if r.SpanID != "" {
			rootSpanIDs[r.SpanID] = true
		}
	}

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := worldWithPyro(mc, pc, led)

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	var checked bool
	for _, s := range pc.all() {
		if pyroLabelValue(s, "service_name") != "web" {
			continue
		}
		ids := spanIDsInProfile(s.Profile)
		for _, sid := range ids {
			checked = true
			if !rootSpanIDs[sid] {
				t.Errorf("entry node (web) profile contains span_id=%q which is not a root SpanID", sid)
			}
		}
	}
	// The ledger is seeded and span_profiles=true on the web/entry node, so span labels
	// MUST appear in at least one profile for the entry node.
	if !checked {
		t.Fatal("no span_id labels in entry node profiles — expected span labels from seeded ledger (span_profiles=true)")
	}
}

// TestAppPyroscope_NilWorldPyroscope: when world.Pyroscope is nil, tickProfiles is a no-op.
func TestAppPyroscope_NilWorldPyroscope(t *testing.T) {
	w, led := buildPyroApp(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	// world.Pyroscope intentionally nil

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick with nil Pyroscope must not error: %v", err)
	}
}

// TestAppPyroscope_DbNodeNotEmitted: the db node has no pyroscope config, so it must not
// produce any profile series.
func TestAppPyroscope_DbNodeNotEmitted(t *testing.T) {
	w, led := buildPyroApp(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := worldWithPyro(mc, pc, led)

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	for _, s := range pc.all() {
		if pyroLabelValue(s, "service_name") == "db" {
			t.Error("db node (no pyroscope config) must not emit profile series")
		}
	}
}

// TestSpanIDForNode_EntryNode: spanIDForNode returns r.SpanID for the entry node.
func TestSpanIDForNode_EntryNode(t *testing.T) {
	r := &ledger.Request{}
	r.SpanID = "aabbccdd11223344"
	r.Calls = []ledger.Call{
		{Target: "db", SpanID: "deadbeef00112233", PeerSpanID: ""},
	}
	got := spanIDForNode(r, "web", "web", true)
	if got != r.SpanID {
		t.Errorf("entry node: got %q want %q", got, r.SpanID)
	}
}

// TestSpanIDForNode_ServerSpanNode: for a non-entry node with serverSpan=true and a non-empty
// PeerSpanID, spanIDForNode returns the PeerSpanID.
func TestSpanIDForNode_ServerSpanNode(t *testing.T) {
	r := &ledger.Request{}
	r.SpanID = "rootspanid111111"
	r.Calls = []ledger.Call{
		{Target: "api", SpanID: "clientspan222222", PeerSpanID: "serverspan333333"},
	}
	got := spanIDForNode(r, "api", "web", true)
	if got != "serverspan333333" {
		t.Errorf("server span node: got %q want %q", got, "serverspan333333")
	}
}

// TestSpanIDForNode_LeafNode: for a leaf (db) with serverSpan=false, spanIDForNode returns
// the CLIENT span (Call.SpanID), not PeerSpanID.
func TestSpanIDForNode_LeafNode(t *testing.T) {
	r := &ledger.Request{}
	r.SpanID = "rootspanid111111"
	r.Calls = []ledger.Call{
		{Target: "db", SpanID: "clientspan444444", PeerSpanID: ""},
	}
	got := spanIDForNode(r, "db", "web", false)
	if got != "clientspan444444" {
		t.Errorf("leaf node: got %q want %q", got, "clientspan444444")
	}
}

// TestSpanIDForNode_Fallback: when no call matches nodeName, fall back to r.SpanID.
func TestSpanIDForNode_Fallback(t *testing.T) {
	r := &ledger.Request{}
	r.SpanID = "rootspanid555555"
	r.Calls = []ledger.Call{
		{Target: "other", SpanID: "otherclient666666"},
	}
	got := spanIDForNode(r, "missing", "web", true)
	if got != r.SpanID {
		t.Errorf("fallback: got %q want %q", got, r.SpanID)
	}
}

// buildPyroAppRuntime builds an app workload whose single entry node has the given runtime.
func buildPyroAppRuntime(t *testing.T, runtime string) (*Workload, *ledger.Ledger) {
	t.Helper()
	cfg := &Config{
		Traffic: Traffic{OffPeakRPS: 10, PeakRPS: 50},
		Services: []ServiceNode{
			{
				Name:    "web",
				Type:    "web",
				Runtime: runtime,
				Entry:   true,
				Pyroscope: &pyroscope.ProfilingCfg{
					Enabled: true,
					Runtime: runtime,
				},
			},
		},
	}
	w := buildApp(t, cfg)
	eng := shape.New("", nil)
	led := ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	for range 5 {
		led.Mint(now)
	}
	return w, led
}

// TestAppPyroscope_JVMSDKEmitsOnlyProcessCPU: a JVM-runtime app node in SDK-push mode must
// emit ONLY process_cpu (no async-profiler types) with NO version, NO pyroscope_spy, NO source.
func TestAppPyroscope_JVMSDKEmitsOnlyProcessCPU(t *testing.T) {
	w, led := buildPyroAppRuntime(t, "jvm")
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := worldWithPyro(mc, pc, led)

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	series := pc.all()
	if len(series) == 0 {
		t.Fatal("jvm runtime must emit at least one series (process_cpu)")
	}

	// Forbidden async-profiler type selectors (Alloy-only, must not appear in SDK push).
	forbidden := map[string]bool{
		"memory:alloc_in_new_tlab_bytes:bytes:space:bytes":   true,
		"memory:alloc_in_new_tlab_objects:count:space:bytes": true,
		"mutex:contentions:count:mutex:count":                true,
		"mutex:delay:nanoseconds:mutex:count":                true,
	}

	for _, s := range series {
		pt := pyroLabelValue(s, "__profile_type__")
		if forbidden[pt] {
			t.Errorf("jvm SDK-push emitted async-profiler type %q — that type is Alloy-only", pt)
		}
		if pt != "process_cpu:cpu:nanoseconds:cpu:nanoseconds" {
			t.Errorf("jvm SDK-push emitted unexpected type %q (must be process_cpu only)", pt)
		}
		if v := pyroLabelValue(s, "version"); v != "" {
			t.Errorf("jvm SDK-push series carries version=%q (must be absent for uncaptured runtime)", v)
		}
		if v := pyroLabelValue(s, "pyroscope_spy"); v != "" {
			t.Errorf("jvm SDK-push series carries pyroscope_spy=%q (must be absent)", v)
		}
		if v := pyroLabelValue(s, "source"); v != "" {
			t.Errorf("jvm SDK-push series carries source=%q (must be absent — SDK push has no source)", v)
		}
	}
}

// TestAppPyroscope_PythonSDKLabels: a Python-runtime app node must emit process_cpu only,
// carry language=python, carry NO version, and carry NO pyroscope_spy.
func TestAppPyroscope_PythonSDKLabels(t *testing.T) {
	w, led := buildPyroAppRuntime(t, "python")
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := worldWithPyro(mc, pc, led)

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	series := pc.all()
	if len(series) == 0 {
		t.Fatal("python runtime must emit at least one series (process_cpu)")
	}

	for _, s := range series {
		pt := pyroLabelValue(s, "__profile_type__")
		if pt != "process_cpu:cpu:nanoseconds:cpu:nanoseconds" {
			t.Errorf("python SDK-push emitted unexpected type %q (must be process_cpu only)", pt)
		}
		if got := pyroLabelValue(s, "language"); got != "python" {
			t.Errorf("python SDK-push series missing language=python (got %q)", got)
		}
		if v := pyroLabelValue(s, "version"); v != "" {
			t.Errorf("python SDK-push series carries version=%q (must be absent per pyroscope-io 1.0.11 reality)", v)
		}
		if v := pyroLabelValue(s, "pyroscope_spy"); v != "" {
			t.Errorf("python SDK-push series carries pyroscope_spy=%q (must be absent)", v)
		}
	}
}

// ── Incident-responsive profiling tests ──────────────────────────────────────────

// pyroTotalByType sums sample values for series whose __profile_type__ starts with ptNamePrefix.
func pyroTotalByType(series []psink.Series, ptNamePrefix string) int64 {
	var total int64
	for _, s := range series {
		pt := pyroLabelValue(s, "__profile_type__")
		if len(pt) < len(ptNamePrefix) || pt[:len(ptNamePrefix)] != ptNamePrefix {
			continue
		}
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

// tickAppProfilesWithMode builds a Go-runtime app workload, activates a failure-mode incident
// scoped to the node name "web" (AxisService scope), and returns emitted series.
func tickAppProfilesWithMode(t *testing.T, incidentSpec string) []psink.Series {
	t.Helper()
	cfg := &Config{
		Traffic: Traffic{OffPeakRPS: 10, PeakRPS: 50},
		Services: []ServiceNode{
			{
				Name:    "web",
				Type:    "web",
				Runtime: "go",
				Entry:   true,
				Pyroscope: &pyroscope.ProfilingCfg{
					Enabled: true,
					Runtime: "go",
				},
			},
		},
	}
	w := buildApp(t, cfg)
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)

	var eng *shape.Engine
	if incidentSpec != "" {
		eng = shape.New("", []string{incidentSpec})
	} else {
		eng = shape.New("", nil)
	}
	led := ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())

	pc := &pyroCapture{}
	world := &core.World{Shape: eng, Pyroscope: pc, Ledger: led}
	w.tickProfiles(context.Background(), now, world)
	return pc.all()
}

// TestAppPyroscope_MemoryLeakRaisesMemoryProfiles: memory_leak incident on the "web" node
// must raise memory profile totals, leaving process_cpu unchanged.
func TestAppPyroscope_MemoryLeakRaisesMemoryProfiles(t *testing.T) {
	// AxisService scope = node name ("web").
	incident := "memory_leak@2026-06-15T09:00:00Z/2h#1.0@web"
	base := tickAppProfilesWithMode(t, "")
	hot := tickAppProfilesWithMode(t, incident)

	baseMem := pyroTotalByType(base, "memory")
	hotMem := pyroTotalByType(hot, "memory")
	if hotMem <= baseMem {
		t.Errorf("memory_leak must raise memory totals: base=%d hot=%d", baseMem, hotMem)
	}

	baseCPU := pyroTotalByType(base, "process_cpu")
	hotCPU := pyroTotalByType(hot, "process_cpu")
	if hotCPU != baseCPU {
		t.Errorf("memory_leak must not affect process_cpu: base=%d hot=%d", baseCPU, hotCPU)
	}
}

// TestAppPyroscope_LockContentionRaisesMutexBlock: lock_contention incident on "web" must raise
// mutex + block totals, leaving memory unchanged.
func TestAppPyroscope_LockContentionRaisesMutexBlock(t *testing.T) {
	incident := "lock_contention@2026-06-15T09:00:00Z/2h#1.0@web"
	base := tickAppProfilesWithMode(t, "")
	hot := tickAppProfilesWithMode(t, incident)

	baseMutex := pyroTotalByType(base, "mutex")
	hotMutex := pyroTotalByType(hot, "mutex")
	if hotMutex <= baseMutex {
		t.Errorf("lock_contention must raise mutex totals: base=%d hot=%d", baseMutex, hotMutex)
	}

	baseBlock := pyroTotalByType(base, "block")
	hotBlock := pyroTotalByType(hot, "block")
	if hotBlock <= baseBlock {
		t.Errorf("lock_contention must raise block totals: base=%d hot=%d", baseBlock, hotBlock)
	}

	baseMem := pyroTotalByType(base, "memory")
	hotMem := pyroTotalByType(hot, "memory")
	if hotMem != baseMem {
		t.Errorf("lock_contention must not affect memory: base=%d hot=%d", baseMem, hotMem)
	}
}

// TestAppPyroscope_GoroutineLeakRaisesGoroutineProfiles: goroutine_leak incident on "web" must
// raise goroutines profile totals, leaving process_cpu unchanged.
func TestAppPyroscope_GoroutineLeakRaisesGoroutineProfiles(t *testing.T) {
	incident := "goroutine_leak@2026-06-15T09:00:00Z/2h#1.0@web"
	base := tickAppProfilesWithMode(t, "")
	hot := tickAppProfilesWithMode(t, incident)

	baseGoro := pyroTotalByType(base, "goroutines")
	hotGoro := pyroTotalByType(hot, "goroutines")
	if hotGoro <= baseGoro {
		t.Errorf("goroutine_leak must raise goroutines totals: base=%d hot=%d", baseGoro, hotGoro)
	}

	baseCPU := pyroTotalByType(base, "process_cpu")
	hotCPU := pyroTotalByType(hot, "process_cpu")
	if hotCPU != baseCPU {
		t.Errorf("goroutine_leak must not affect process_cpu: base=%d hot=%d", baseCPU, hotCPU)
	}
}

// TestAppPyroscope_IncidentDoesNotChangeInventory: failure modes must not alter the label set
// or series count (I32).
func TestAppPyroscope_IncidentDoesNotChangeInventory(t *testing.T) {
	base := tickAppProfilesWithMode(t, "")
	// All modes at full intensity on "web".
	hot := tickAppProfilesWithMode(t, "memory_leak@2026-06-15T09:00:00Z/2h#1.0@web")

	if len(hot) != len(base) {
		t.Errorf("failure mode must not change series count: base=%d hot=%d", len(base), len(hot))
	}
	// Verify __profile_type__ set is identical.
	baseTypes := make(map[string]bool, len(base))
	hotTypes := make(map[string]bool, len(hot))
	for _, s := range base {
		baseTypes[pyroLabelValue(s, "__profile_type__")] = true
	}
	for _, s := range hot {
		hotTypes[pyroLabelValue(s, "__profile_type__")] = true
	}
	for pt := range baseTypes {
		if !hotTypes[pt] {
			t.Errorf("incident removed profile type %q from inventory", pt)
		}
	}
	for pt := range hotTypes {
		if !baseTypes[pt] {
			t.Errorf("incident added unexpected profile type %q to inventory", pt)
		}
	}
}
