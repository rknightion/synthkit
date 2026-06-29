// SPDX-License-Identifier: AGPL-3.0-only

package pyroscope

import (
	"testing"
	"time"

	pprofpb "github.com/rknightion/synthkit/internal/pyroscope/pprofpb"
)

var t0 = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

func totalValue(p *pprofpb.Profile) int64 {
	var s int64
	for _, sm := range p.Sample {
		for _, v := range sm.Value {
			s += v
		}
	}
	return s
}

func funcNameSet(p *pprofpb.Profile) map[string]bool {
	m := map[string]bool{}
	for _, f := range p.Function {
		m[p.StringTable[f.Name]] = true
	}
	return m
}

func someSampleHasStrLabel(p *pprofpb.Profile, key, val string) bool {
	ki, vi := int64(-1), int64(-1)
	for i, s := range p.StringTable {
		if s == key {
			ki = int64(i)
		}
		if s == val {
			vi = int64(i)
		}
	}
	if ki < 0 || vi < 0 {
		return false
	}
	for _, sm := range p.Sample {
		for _, l := range sm.Label {
			if l.Key == ki && l.Str == vi {
				return true
			}
		}
	}
	return false
}

func TestBuildProfileShape(t *testing.T) {
	b := NewBuilder("go", "acme-api")
	p1 := b.Build(ProcessCPU, Load{Factor: 1.0, Now: t0})
	if len(p1.StringTable) == 0 || p1.StringTable[0] != "" {
		t.Fatal("string_table[0] must be empty string")
	}
	if len(p1.SampleType) != 1 || p1.StringTable[p1.SampleType[0].Type] != "cpu" {
		t.Fatalf("sample_type")
	}
	if len(p1.Sample) == 0 || len(p1.Location) == 0 || len(p1.Function) == 0 {
		t.Fatal("empty profile")
	}
	p2 := b.Build(ProcessCPU, Load{Factor: 2.0, Now: t0})
	if totalValue(p2) <= totalValue(p1) {
		t.Fatal("higher load → higher total")
	}
	// structural determinism (I32): same function-name set across builds at different times
	pLater := b.Build(ProcessCPU, Load{Factor: 1.0, Now: t0.Add(time.Minute)})
	a, c := funcNameSet(p1), funcNameSet(pLater)
	if len(a) != len(c) {
		t.Fatal("function set must be run-stable")
	}
	for k := range a {
		if !c[k] {
			t.Fatalf("function set drifted: %q", k)
		}
	}
}

func TestSpanProfileSamples(t *testing.T) {
	b := NewBuilder("go", "acme-api")
	p := b.BuildWithSpans(ProcessCPU, Load{Factor: 1, Now: t0}, []string{"abc123"})
	if !someSampleHasStrLabel(p, "span_id", "abc123") {
		t.Fatal("span_id must ride as a Sample.Label referencing the string table")
	}
}

// TestBuildDoesNotPolluteCacheWithSpans verifies that Build after BuildWithSpans
// does not carry span labels in the cached structure.
func TestBuildDoesNotPolluteCacheWithSpans(t *testing.T) {
	b := NewBuilder("go", "acme-api")
	_ = b.BuildWithSpans(ProcessCPU, Load{Factor: 1, Now: t0}, []string{"span-xyz"})
	plain := b.Build(ProcessCPU, Load{Factor: 1, Now: t0})
	if someSampleHasStrLabel(plain, "span_id", "span-xyz") {
		t.Fatal("Build after BuildWithSpans must not carry span labels (cache pollution)")
	}
}

// TestBuildThenBuildWithSpansKeepsSampleType reproduces the live cardinality bug. The per-tick
// workload loop calls Build(pt) THEN BuildWithSpans(pt) on the SAME builder. A prior bug let
// Build intern the SampleType/PeriodType strings into the SHARED cache index at positions past
// len(cache.stringTable); BuildWithSpans then cloned that inconsistent (table,index) pair and
// appended the span strings into exactly those stale slots — silently rewriting SampleType/
// PeriodType to point at "span_id" + the hex span value. That surfaced in the profiles store as
// spurious per-span profile-types like `block:span_id:59767e7c8d1dfa39::` and
// `goroutines:span_id:<hex>:span_id:<hex>` (unbounded cardinality: one per distinct span id).
func TestBuildThenBuildWithSpansKeepsSampleType(t *testing.T) {
	for _, pt := range []ProfileType{ProcessCPU, BlockContentions, GoroutinesSDK, MutexContentions} {
		b := NewBuilder("go", "acme-api")
		_ = b.Build(pt, Load{Factor: 1, Now: t0}) // under the bug, this pollutes the shared cache index
		p := b.BuildWithSpans(pt, Load{Factor: 1, Now: t0}, []string{"59767e7c8d1dfa39"})

		if got := p.StringTable[p.SampleType[0].Type]; got != pt.SampleType {
			t.Errorf("pt=%s: SampleType.Type resolved to %q, want %q (span string leaked into the ValueType)", pt.Name, got, pt.SampleType)
		}
		if got := p.StringTable[p.SampleType[0].Unit]; got != pt.SampleUnit {
			t.Errorf("pt=%s: SampleType.Unit resolved to %q, want %q", pt.Name, got, pt.SampleUnit)
		}
		if got := p.StringTable[p.PeriodType.Type]; got != pt.PeriodType {
			t.Errorf("pt=%s: PeriodType.Type resolved to %q, want %q", pt.Name, got, pt.PeriodType)
		}
		if got := p.StringTable[p.PeriodType.Unit]; got != pt.PeriodUnit {
			t.Errorf("pt=%s: PeriodType.Unit resolved to %q, want %q", pt.Name, got, pt.PeriodUnit)
		}
		// The golden thread must survive: span_id still rides as a Sample.Label.
		if !someSampleHasStrLabel(p, "span_id", "59767e7c8d1dfa39") {
			t.Errorf("pt=%s: span_id sample label missing after fix", pt.Name)
		}
	}
}

// TestBuildPeriodAndTime checks the metadata fields are set correctly.
func TestBuildPeriodAndTime(t *testing.T) {
	b := NewBuilder("go", "acme-api")
	p := b.Build(ProcessCPU, Load{Factor: 1.0, Now: t0})
	if p.TimeNanos != t0.UnixNano() {
		t.Fatalf("TimeNanos: got %d want %d", p.TimeNanos, t0.UnixNano())
	}
	if p.DurationNanos != 60_000_000_000 {
		t.Fatalf("DurationNanos: got %d want 60s in ns", p.DurationNanos)
	}
	if p.Period != ProcessCPU.Period {
		t.Fatalf("Period: got %d want %d", p.Period, ProcessCPU.Period)
	}
	if p.PeriodType == nil {
		t.Fatal("PeriodType must be set")
	}
	if p.StringTable[p.PeriodType.Type] != ProcessCPU.PeriodType {
		t.Fatalf("PeriodType.Type: got %q want %q", p.StringTable[p.PeriodType.Type], ProcessCPU.PeriodType)
	}
}

// TestBuildAllRuntimes checks that we can build profiles for every supported runtime without panic.
func TestBuildAllRuntimes(t *testing.T) {
	for _, rt := range []string{"go", "jvm", "python", "node", "dotnet", "ebpf"} {
		types := RuntimeTypes(rt)
		b := NewBuilder(rt, "svc-"+rt)
		for _, pt := range types {
			p := b.Build(pt, Load{Factor: 1.0, Now: t0})
			if len(p.Sample) == 0 {
				t.Fatalf("runtime=%s pt=%s: no samples", rt, pt.Selector())
			}
		}
	}
}

// TestModeAmpScalesHotLeaf checks that a cpu_hotspot mode amplifies total value beyond the baseline.
func TestModeAmpScalesHotLeaf(t *testing.T) {
	b := NewBuilder("go", "acme-api")
	base := b.Build(ProcessCPU, Load{Factor: 1.0, Now: t0})
	hot := b.Build(ProcessCPU, Load{Factor: 1.0, Now: t0, Modes: map[string]float64{"cpu_hotspot": 1.0}})
	if totalValue(hot) <= totalValue(base) {
		t.Fatal("cpu_hotspot mode must increase total value vs baseline")
	}
}

// TestModeAmpMemoryLeak checks that memory_leak raises memory profile totals but not process_cpu.
func TestModeAmpMemoryLeak(t *testing.T) {
	b := NewBuilder("go", "acme-api")
	modes := map[string]float64{"memory_leak": 1.0}

	// memory profile: with mode active > without.
	baseMemory := b.Build(MemoryInuseSpace, Load{Factor: 1.0, Now: t0})
	hotMemory := b.Build(MemoryInuseSpace, Load{Factor: 1.0, Now: t0, Modes: modes})
	if totalValue(hotMemory) <= totalValue(baseMemory) {
		t.Error("memory_leak must raise memory profile total")
	}

	// process_cpu: mode must NOT change the total (non-matching mode).
	baseCPU := b.Build(ProcessCPU, Load{Factor: 1.0, Now: t0})
	hotCPU := b.Build(ProcessCPU, Load{Factor: 1.0, Now: t0, Modes: modes})
	if totalValue(hotCPU) != totalValue(baseCPU) {
		t.Errorf("memory_leak must not affect process_cpu total: base=%d hot=%d", totalValue(baseCPU), totalValue(hotCPU))
	}
}

// TestModeAmpLockContention checks that lock_contention raises mutex/block totals but not memory.
func TestModeAmpLockContention(t *testing.T) {
	b := NewBuilder("go", "acme-api")
	modes := map[string]float64{"lock_contention": 1.0}

	// mutex profile: with mode active > without.
	baseMutex := b.Build(MutexContentions, Load{Factor: 1.0, Now: t0})
	hotMutex := b.Build(MutexContentions, Load{Factor: 1.0, Now: t0, Modes: modes})
	if totalValue(hotMutex) <= totalValue(baseMutex) {
		t.Error("lock_contention must raise mutex profile total")
	}

	// block profile: with mode active > without.
	baseBlock := b.Build(BlockContentions, Load{Factor: 1.0, Now: t0})
	hotBlock := b.Build(BlockContentions, Load{Factor: 1.0, Now: t0, Modes: modes})
	if totalValue(hotBlock) <= totalValue(baseBlock) {
		t.Error("lock_contention must raise block profile total")
	}

	// memory profile: must NOT be affected (non-matching mode).
	baseMemory := b.Build(MemoryInuseSpace, Load{Factor: 1.0, Now: t0})
	hotMemory := b.Build(MemoryInuseSpace, Load{Factor: 1.0, Now: t0, Modes: modes})
	if totalValue(hotMemory) != totalValue(baseMemory) {
		t.Errorf("lock_contention must not affect memory profile total: base=%d hot=%d", totalValue(baseMemory), totalValue(hotMemory))
	}
}

// TestModeAmpGoroutineLeak checks that goroutine_leak raises goroutines/goroutine totals but not memory.
func TestModeAmpGoroutineLeak(t *testing.T) {
	b := NewBuilder("go", "acme-api")
	modes := map[string]float64{"goroutine_leak": 1.0}

	// goroutines (SDK plural) profile: with mode active > without.
	baseGoro := b.Build(GoroutinesSDK, Load{Factor: 1.0, Now: t0})
	hotGoro := b.Build(GoroutinesSDK, Load{Factor: 1.0, Now: t0, Modes: modes})
	if totalValue(hotGoro) <= totalValue(baseGoro) {
		t.Error("goroutine_leak must raise goroutines (SDK) profile total")
	}

	// goroutine (pprof singular) profile: with mode active > without.
	baseGoroP := b.Build(GoroutinePprof, Load{Factor: 1.0, Now: t0})
	hotGoroP := b.Build(GoroutinePprof, Load{Factor: 1.0, Now: t0, Modes: modes})
	if totalValue(hotGoroP) <= totalValue(baseGoroP) {
		t.Error("goroutine_leak must raise goroutine (pprof) profile total")
	}

	// memory profile: must NOT be affected.
	baseMemory := b.Build(MemoryInuseSpace, Load{Factor: 1.0, Now: t0})
	hotMemory := b.Build(MemoryInuseSpace, Load{Factor: 1.0, Now: t0, Modes: modes})
	if totalValue(hotMemory) != totalValue(baseMemory) {
		t.Errorf("goroutine_leak must not affect memory total: base=%d hot=%d", totalValue(baseMemory), totalValue(hotMemory))
	}
}

// TestModeAmpNilModes checks that nil/empty Modes → no amplification (factor 1).
func TestModeAmpNilModes(t *testing.T) {
	b := NewBuilder("go", "acme-api")
	base := b.Build(MemoryInuseSpace, Load{Factor: 1.0, Now: t0})
	nilModes := b.Build(MemoryInuseSpace, Load{Factor: 1.0, Now: t0, Modes: nil})
	emptyModes := b.Build(MemoryInuseSpace, Load{Factor: 1.0, Now: t0, Modes: map[string]float64{}})
	if totalValue(nilModes) != totalValue(base) {
		t.Errorf("nil Modes must not change total: base=%d got=%d", totalValue(base), totalValue(nilModes))
	}
	if totalValue(emptyModes) != totalValue(base) {
		t.Errorf("empty Modes must not change total: base=%d got=%d", totalValue(base), totalValue(emptyModes))
	}
}

// TestModeAmpInventoryUnchanged checks that modes do not add/remove series (I32: inventory stable).
// The function + location sets must be identical regardless of mode.
func TestModeAmpInventoryUnchanged(t *testing.T) {
	b := NewBuilder("go", "acme-api")
	modes := map[string]float64{
		"memory_leak":     1.0,
		"lock_contention": 1.0,
		"goroutine_leak":  1.0,
		"cpu_hotspot":     1.0,
	}
	for _, pt := range RuntimeTypes("go") {
		base := b.Build(pt, Load{Factor: 1.0, Now: t0})
		hot := b.Build(pt, Load{Factor: 1.0, Now: t0, Modes: modes})

		// Same number of samples.
		if len(hot.Sample) != len(base.Sample) {
			t.Errorf("pt=%s: sample count changed with modes: base=%d hot=%d", pt.Selector(), len(base.Sample), len(hot.Sample))
		}
		// Same function set.
		if len(hot.Function) != len(base.Function) {
			t.Errorf("pt=%s: function count changed with modes: base=%d hot=%d", pt.Selector(), len(base.Function), len(hot.Function))
		}
	}
}
