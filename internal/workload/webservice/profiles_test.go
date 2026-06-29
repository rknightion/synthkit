// SPDX-License-Identifier: AGPL-3.0-only

package webservice

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
func labelValue(s psink.Series, name string) string {
	for _, lp := range s.Labels {
		if lp.Name == name {
			return lp.Value
		}
	}
	return ""
}

// hasLabel reports whether a Series carries the named label with the given value.
func hasLabel(s psink.Series, name, value string) bool {
	return labelValue(s, name) == value
}

// spanIDsInProfile decodes all span_id sample-label values from a pprof Profile.
// It walks the string table to resolve label Str indices.
func spanIDsInProfile(prof *pprofpb.Profile) []string {
	if prof == nil {
		return nil
	}
	// Find the string index for "span_id".
	var spanKeyIdx int64 = -1
	for i, s := range prof.StringTable {
		if s == "span_id" {
			spanKeyIdx = int64(i)
			break
		}
	}
	if spanKeyIdx < 0 {
		return nil // no span_id key in string table
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

// buildPyroWS builds a web_service workload with Pyroscope enabled (runtime=go,
// span_profiles=true) and a real ledger. Returns the workload and the ledger.
func buildPyroWS(t *testing.T) (*Workload, *ledger.Ledger) {
	t.Helper()
	cfg := NewConfig().(*Config)
	cfg.Tracing = true
	cfg.Pyroscope = &pyroscope.ProfilingCfg{
		Enabled:      true,
		Runtime:      "go",
		SpanProfiles: true,
	}
	w, err := build(cfg, testBinding(nil))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	eng := shape.New("", nil)
	led := ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())
	return w.(*Workload), led
}

// TestPyroscopeSignalsContainsPyroscopeProfiles (a): Signals() includes PyroscopeProfiles
// when pyroscope is enabled in sdk mode.
func TestPyroscopeSignalsContainsPyroscopeProfiles(t *testing.T) {
	w, _ := buildPyroWS(t)
	sigs := w.Signals()
	if !slices.Contains(sigs, core.PyroscopeProfiles) {
		t.Fatalf("Signals() does not contain PyroscopeProfiles: %v", sigs)
	}
}

// TestPyroscopeSignalsScrapedModeOmitted: scraped mode must NOT advertise PyroscopeProfiles
// (the runner must not wire a push sink when Alloy owns collection).
func TestPyroscopeSignalsScrapedModeOmitted(t *testing.T) {
	cfg := NewConfig().(*Config)
	cfg.Pyroscope = &pyroscope.ProfilingCfg{Enabled: true, Mode: "scraped", Runtime: "go"}
	w, err := build(cfg, testBinding(nil))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	sigs := w.(core.Workload).Signals()
	if slices.Contains(sigs, core.PyroscopeProfiles) {
		t.Fatal("scraped mode must NOT include PyroscopeProfiles in Signals()")
	}
}

// TestPyroscopeSignalsNilConfig: nil pyroscope config omits PyroscopeProfiles.
func TestPyroscopeSignalsNilConfig(t *testing.T) {
	cfg := NewConfig().(*Config)
	w, err := build(cfg, testBinding(nil))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	sigs := w.(core.Workload).Signals()
	if slices.Contains(sigs, core.PyroscopeProfiles) {
		t.Fatal("nil Pyroscope config must NOT include PyroscopeProfiles")
	}
}

// TestPyroscopeTickEmitsExpectedProfileTypes (b): after Tick with a populated ledger, the
// pyro capture holds series for process_cpu:cpu:nanoseconds:cpu:nanoseconds (CPU) and
// goroutines:goroutine:count:goroutine:count (goroutines) by __profile_type__.
func TestPyroscopeTickEmitsExpectedProfileTypes(t *testing.T) {
	w, led := buildPyroWS(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	world.Pyroscope = pc

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	series := pc.all()
	if len(series) == 0 {
		t.Fatal("no Pyroscope series emitted")
	}

	profileTypes := map[string]bool{}
	for _, s := range series {
		pt := labelValue(s, "__profile_type__")
		if pt != "" {
			profileTypes[pt] = true
		}
	}

	wantTypes := []string{
		"process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		"goroutines:goroutine:count:goroutine:count",
	}
	for _, want := range wantTypes {
		if !profileTypes[want] {
			t.Errorf("missing __profile_type__=%q in emitted series (got %v)", want, profileTypes)
		}
	}
}

// TestPyroscopeGoSpyLabel (c): for runtime=go, every emitted series must carry
// pyroscope_spy=gospy.
func TestPyroscopeGoSpyLabel(t *testing.T) {
	w, led := buildPyroWS(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	world.Pyroscope = pc

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	series := pc.all()
	if len(series) == 0 {
		t.Fatal("no Pyroscope series emitted")
	}
	for _, s := range series {
		if !hasLabel(s, "pyroscope_spy", "gospy") {
			t.Errorf("series %q missing pyroscope_spy=gospy (labels: %v)",
				labelValue(s, "__profile_type__"), s.Labels)
		}
	}
}

// TestPyroscopeNonGoRuntimeNoSpy: for a non-go runtime (e.g. python), pyroscope_spy must
// NOT be present.
func TestPyroscopeNonGoRuntimeNoSpy(t *testing.T) {
	cfg := NewConfig().(*Config)
	cfg.Pyroscope = &pyroscope.ProfilingCfg{
		Enabled: true,
		Runtime: "python",
	}
	w, err := build(cfg, testBinding(nil))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ws := w.(*Workload)

	eng := shape.New("", nil)
	led := ledger.New(eng, 0, 0)
	led.AddMinter(ws.Minter())
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	world.Pyroscope = pc

	if err := ws.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	for _, s := range pc.all() {
		if labelValue(s, "pyroscope_spy") != "" {
			t.Errorf("non-go runtime emitted pyroscope_spy=%q (must be absent)",
				labelValue(s, "pyroscope_spy"))
		}
	}
}

// TestPyroscopeSpanProfilesOnlyCPU: real Go span profiles label only the CPU profile. The Go SDK
// attaches span context via runtime/pprof.SetGoroutineLabels, which is captured solely by the CPU
// profile ("Only CPU profiling is supported" per Pyroscope's Go span-profiles docs). So span_id
// sample labels must ride ONLY on process_cpu profiles — never on memory/block/mutex/goroutines.
func TestPyroscopeSpanProfilesOnlyCPU(t *testing.T) {
	w, led := buildPyroWS(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)
	if len(led.ActiveFor(w.name, now, interval)) == 0 {
		t.Skip("no active requests in ledger window — skip")
	}

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	world.Pyroscope = pc
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	var sawCPUSpan bool
	for _, s := range pc.all() {
		if len(spanIDsInProfile(s.Profile)) == 0 {
			continue
		}
		if name := labelValue(s, "__name__"); name != "process_cpu" {
			t.Errorf("span_id labels found on __name__=%q profile — Go span profiles are CPU-only", name)
		} else {
			sawCPUSpan = true
		}
	}
	if !sawCPUSpan {
		t.Fatal("expected span_id labels on the process_cpu profile (span_profiles=true, ledger seeded)")
	}
}

// TestPyroscopeSpanProfileCorrelation (d): REQUEST CORRELATION — every span_id sample-label
// value found inside emitted profiles must be one of the SpanIDs from the seeded ledger
// requests. This validates that span correlation never invents IDs.
func TestPyroscopeSpanProfileCorrelation(t *testing.T) {
	w, led := buildPyroWS(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	// Mint until we have at least one request.
	mintNonEmpty(t, led, now, false)

	// Collect the SpanIDs that were actually minted into the ledger.
	reqs := led.ActiveFor(w.name, now, interval)
	if len(reqs) == 0 {
		t.Skip("no active requests in ledger window — skip request-correlation check")
	}
	minted := map[string]bool{}
	for _, r := range reqs {
		if r.SpanID != "" {
			minted[r.SpanID] = true
		}
	}
	if len(minted) == 0 {
		t.Skip("no non-empty SpanIDs in ledger — skip")
	}

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	world.Pyroscope = pc

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	series := pc.all()
	if len(series) == 0 {
		t.Fatal("no Pyroscope series emitted")
	}

	// Decode every span_id label value from every emitted profile and assert it is in the
	// minted set.
	var sawSpanLabel bool
	for _, s := range series {
		ids := spanIDsInProfile(s.Profile)
		for _, sid := range ids {
			sawSpanLabel = true
			if !minted[sid] {
				t.Errorf("profile for __profile_type__=%q contains span_id=%q not in minted ledger set %v",
					labelValue(s, "__profile_type__"), sid, minted)
			}
		}
	}
	// The ledger is seeded with non-empty SpanIDs (minted is non-empty by this point) and
	// span_profiles=true is set, so span labels MUST appear in at least one profile.
	if !sawSpanLabel {
		t.Fatal("no span_id labels found in emitted profiles — expected span labels from seeded ledger (span_profiles=true)")
	}
}

// TestPyroscopeNilWorldPyroscope: when world.Pyroscope is nil, tickProfiles is a no-op and
// Tick does not panic.
func TestPyroscopeNilWorldPyroscope(t *testing.T) {
	w, led := buildPyroWS(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)

	mc := &coretest.MetricCapture{}
	// world.Pyroscope intentionally left nil
	world := coretest.World(mc, nil, nil)
	world.Ledger = led

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick with nil Pyroscope must not error: %v", err)
	}
}

// TestPyroscopeServiceNameLabel: service_name label must equal the workload's name.
func TestPyroscopeServiceNameLabel(t *testing.T) {
	w, led := buildPyroWS(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	world.Pyroscope = pc

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	for _, s := range pc.all() {
		if got := labelValue(s, "service_name"); got != w.name {
			t.Errorf("series service_name=%q want %q", got, w.name)
		}
	}
}

// buildPyroWSRuntime is a helper that builds a web_service workload with the given runtime.
func buildPyroWSRuntime(t *testing.T, runtime string) (*Workload, *ledger.Ledger) {
	t.Helper()
	cfg := NewConfig().(*Config)
	cfg.Pyroscope = &pyroscope.ProfilingCfg{
		Enabled: true,
		Runtime: runtime,
	}
	w, err := build(cfg, testBinding(nil))
	if err != nil {
		t.Fatalf("build(%s): %v", runtime, err)
	}
	ws := w.(*Workload)
	eng := shape.New("", nil)
	led := ledger.New(eng, 0, 0)
	led.AddMinter(ws.Minter())
	return ws, led
}

// TestPyroscopeJVMSDKEmitsOnlyProcessCPU: a JVM-runtime SDK-push node must emit ONLY
// process_cpu (no async-profiler types) and must carry NO version, NO pyroscope_spy, NO source.
// The rich JVM types (TLAB alloc, Java mutex) come exclusively via the Alloy pyroscope.java lane.
func TestPyroscopeJVMSDKEmitsOnlyProcessCPU(t *testing.T) {
	ws, led := buildPyroWSRuntime(t, "jvm")
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	world.Pyroscope = pc

	if err := ws.Tick(context.Background(), now, world); err != nil {
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
		"mutex:contentions:count:mutex:count":                true, // Java mutex period
		"mutex:delay:nanoseconds:mutex:count":                true, // Java mutex period
	}

	for _, s := range series {
		pt := labelValue(s, "__profile_type__")
		if forbidden[pt] {
			t.Errorf("jvm SDK-push emitted async-profiler type %q — that type is Alloy-only", pt)
		}
		// Must be process_cpu only.
		if pt != "process_cpu:cpu:nanoseconds:cpu:nanoseconds" {
			t.Errorf("jvm SDK-push emitted unexpected type %q (must be process_cpu only)", pt)
		}
		// No version label.
		if v := labelValue(s, "version"); v != "" {
			t.Errorf("jvm SDK-push series carries version=%q (must be absent for uncaptured runtime)", v)
		}
		// No pyroscope_spy label.
		if v := labelValue(s, "pyroscope_spy"); v != "" {
			t.Errorf("jvm SDK-push series carries pyroscope_spy=%q (must be absent)", v)
		}
		// No source label (SDK-push discriminator).
		if v := labelValue(s, "source"); v != "" {
			t.Errorf("jvm SDK-push series carries source=%q (must be absent — SDK push has no source)", v)
		}
	}
}

// TestPyroscopePythonSDKLabels: a Python-runtime SDK-push node must emit process_cpu only,
// carry language=python, carry NO version, and carry NO pyroscope_spy.
func TestPyroscopePythonSDKLabels(t *testing.T) {
	ws, led := buildPyroWSRuntime(t, "python")
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)

	mc := &coretest.MetricCapture{}
	pc := &pyroCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	world.Pyroscope = pc

	if err := ws.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	series := pc.all()
	if len(series) == 0 {
		t.Fatal("python runtime must emit at least one series (process_cpu)")
	}

	for _, s := range series {
		pt := labelValue(s, "__profile_type__")
		// Python SDK emits process_cpu only.
		if pt != "process_cpu:cpu:nanoseconds:cpu:nanoseconds" {
			t.Errorf("python SDK-push emitted unexpected type %q (must be process_cpu only)", pt)
		}
		// language=python must be present.
		if got := labelValue(s, "language"); got != "python" {
			t.Errorf("python SDK-push series missing language=python (got %q)", got)
		}
		// No version label.
		if v := labelValue(s, "version"); v != "" {
			t.Errorf("python SDK-push series carries version=%q (must be absent per pyroscope-io 1.0.11 reality)", v)
		}
		// No pyroscope_spy.
		if v := labelValue(s, "pyroscope_spy"); v != "" {
			t.Errorf("python SDK-push series carries pyroscope_spy=%q (must be absent — Python SDK does not set spy)", v)
		}
	}
}

// ── Incident-responsive profiling tests ──────────────────────────────────────────

// profileTotalWS sums all sample values across all series emitted.
func profileTotalWS(series []psink.Series) int64 {
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

// profileTotalByTypeWS returns total sample values for series whose __profile_type__ label
// starts with ptNamePrefix (e.g. "memory" matches all memory:* selectors).
func profileTotalByTypeWS(series []psink.Series, ptNamePrefix string) int64 {
	var total int64
	for _, s := range series {
		pt := labelValue(s, "__profile_type__")
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

// tickProfilingWithMode builds a Go-runtime web_service workload, optionally activates a
// failure mode via a scheduled incident scoped to the workload name, and returns the emitted
// series from one tickProfiles call.
func tickProfilingWithMode(t *testing.T, incidentSpec string) []psink.Series {
	t.Helper()
	cfg := NewConfig().(*Config)
	cfg.Pyroscope = &pyroscope.ProfilingCfg{Enabled: true, Runtime: "go"}
	w, err := build(cfg, testBinding(nil))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ws := w.(*Workload)

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	var eng *shape.Engine
	if incidentSpec != "" {
		eng = shape.New("", []string{incidentSpec})
	} else {
		eng = shape.New("", nil)
	}

	pc := &pyroCapture{}
	world := &core.World{Shape: eng, Pyroscope: pc}
	ws.tickProfiles(context.Background(), now, world)
	return pc.all()
}

// TestPyroscopeMemoryLeakRaisesMemoryProfiles checks that memory_leak raises memory profile
// totals while leaving process_cpu unchanged.
func TestPyroscopeMemoryLeakRaisesMemoryProfiles(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	// Incident scoped to the workload name ("test-api" per testBinding).
	incident := "memory_leak@2026-06-15T09:00:00Z/2h#1.0@test-api"

	base := tickProfilingWithMode(t, "")
	hot := tickProfilingWithMode(t, incident)

	_ = now // captured in tickProfilingWithMode

	baseMemTotal := profileTotalByTypeWS(base, "memory")
	hotMemTotal := profileTotalByTypeWS(hot, "memory")
	if hotMemTotal <= baseMemTotal {
		t.Errorf("memory_leak must raise memory profile totals: base=%d hot=%d", baseMemTotal, hotMemTotal)
	}

	// process_cpu must be unaffected by memory_leak.
	baseCPUTotal := profileTotalByTypeWS(base, "process_cpu")
	hotCPUTotal := profileTotalByTypeWS(hot, "process_cpu")
	if hotCPUTotal != baseCPUTotal {
		t.Errorf("memory_leak must not affect process_cpu total: base=%d hot=%d", baseCPUTotal, hotCPUTotal)
	}
}

// TestPyroscopeLockContentionRaisesMutexBlock checks that lock_contention raises mutex + block
// profile totals while leaving memory unchanged.
func TestPyroscopeLockContentionRaisesMutexBlock(t *testing.T) {
	incident := "lock_contention@2026-06-15T09:00:00Z/2h#1.0@test-api"

	base := tickProfilingWithMode(t, "")
	hot := tickProfilingWithMode(t, incident)

	baseMutex := profileTotalByTypeWS(base, "mutex")
	hotMutex := profileTotalByTypeWS(hot, "mutex")
	if hotMutex <= baseMutex {
		t.Errorf("lock_contention must raise mutex total: base=%d hot=%d", baseMutex, hotMutex)
	}

	baseBlock := profileTotalByTypeWS(base, "block")
	hotBlock := profileTotalByTypeWS(hot, "block")
	if hotBlock <= baseBlock {
		t.Errorf("lock_contention must raise block total: base=%d hot=%d", baseBlock, hotBlock)
	}

	// Memory must be unaffected.
	baseMem := profileTotalByTypeWS(base, "memory")
	hotMem := profileTotalByTypeWS(hot, "memory")
	if hotMem != baseMem {
		t.Errorf("lock_contention must not affect memory total: base=%d hot=%d", baseMem, hotMem)
	}
}

// TestPyroscopeGoroutineLeakRaisesGoroutineProfiles checks that goroutine_leak raises goroutine
// profile totals while leaving process_cpu unchanged.
func TestPyroscopeGoroutineLeakRaisesGoroutineProfiles(t *testing.T) {
	incident := "goroutine_leak@2026-06-15T09:00:00Z/2h#1.0@test-api"

	base := tickProfilingWithMode(t, "")
	hot := tickProfilingWithMode(t, incident)

	baseGoro := profileTotalByTypeWS(base, "goroutines")
	hotGoro := profileTotalByTypeWS(hot, "goroutines")
	if hotGoro <= baseGoro {
		t.Errorf("goroutine_leak must raise goroutines total: base=%d hot=%d", baseGoro, hotGoro)
	}

	// process_cpu must be unaffected.
	baseCPU := profileTotalByTypeWS(base, "process_cpu")
	hotCPU := profileTotalByTypeWS(hot, "process_cpu")
	if hotCPU != baseCPU {
		t.Errorf("goroutine_leak must not affect process_cpu total: base=%d hot=%d", baseCPU, hotCPU)
	}
}
