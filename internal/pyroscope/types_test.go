// SPDX-License-Identifier: AGPL-3.0-only

package pyroscope

import "testing"

func TestProfileTypeSelectors(t *testing.T) {
	if ProcessCPU.Selector() != "process_cpu:cpu:nanoseconds:cpu:nanoseconds" {
		t.Fatalf("cpu=%q", ProcessCPU.Selector())
	}
	if MemoryInuseSpace.Selector() != "memory:inuse_space:bytes:space:bytes" {
		t.Fatalf("inuse=%q", MemoryInuseSpace.Selector())
	}
	// Java mutex PERIOD unit is mutex:count, NOT Go's contentions:count (real capture).
	if JavaMutexContentions.Selector() != "mutex:contentions:count:mutex:count" {
		t.Fatalf("java mutex=%q", JavaMutexContentions.Selector())
	}
	if len(RuntimeTypes("go")) == 0 || len(RuntimeTypes("jvm")) == 0 {
		t.Fatal("runtime sets")
	}
	if len(EBPFTypes()) != 1 || EBPFTypes()[0].Selector() != ProcessCPU.Selector() {
		t.Fatal("eBPF = process_cpu only")
	}
}

func TestRuntimeTypesAll(t *testing.T) {
	goTypes := RuntimeTypes("go")
	// go runtime must include cpu, memory, goroutine, mutex, block families
	wantGo := []ProfileType{
		ProcessCPU, ProcessCPUSamples,
		MemoryAllocObjects, MemoryAllocSpace, MemoryInuseObjects, MemoryInuseSpace,
		GoroutinesSDK,
		MutexContentions, MutexDelay,
		BlockContentions, BlockDelay,
	}
	if len(goTypes) != len(wantGo) {
		t.Fatalf("go runtime types count: got %d want %d", len(goTypes), len(wantGo))
	}
	for i, pt := range wantGo {
		if goTypes[i].Selector() != pt.Selector() {
			t.Fatalf("go[%d]: got %q want %q", i, goTypes[i].Selector(), pt.Selector())
		}
	}

	jvmTypes := RuntimeTypes("jvm")
	wantJVM := []ProfileType{
		ProcessCPU,
		MemoryAllocInNewTLABBytes, MemoryAllocInNewTLABObjects,
		JavaMutexContentions, JavaMutexDelay,
	}
	if len(jvmTypes) != len(wantJVM) {
		t.Fatalf("jvm runtime types count: got %d want %d", len(jvmTypes), len(wantJVM))
	}

	// Single-type runtimes
	for _, rt := range []string{"python", "node", "dotnet", "ebpf", "unknown"} {
		types := RuntimeTypes(rt)
		if len(types) != 1 || types[0].Selector() != ProcessCPU.Selector() {
			t.Fatalf("runtime %q: expected only process_cpu, got %d types", rt, len(types))
		}
	}
}

func TestSDKRuntimeTypes(t *testing.T) {
	// Go SDK: must return 11 types including GoroutinesSDK (plural) but NOT the Alloy
	// async-profiler types (MemoryAllocInNewTLABBytes, JavaMutexContentions).
	goTypes := SDKRuntimeTypes("go")
	wantGo := []ProfileType{
		ProcessCPU, ProcessCPUSamples,
		MemoryAllocObjects, MemoryAllocSpace, MemoryInuseObjects, MemoryInuseSpace,
		GoroutinesSDK,
		MutexContentions, MutexDelay,
		BlockContentions, BlockDelay,
	}
	if len(goTypes) != len(wantGo) {
		t.Fatalf("SDKRuntimeTypes(go): got %d types want %d", len(goTypes), len(wantGo))
	}
	for i, pt := range wantGo {
		if goTypes[i].Selector() != pt.Selector() {
			t.Fatalf("SDKRuntimeTypes(go)[%d]: got %q want %q", i, goTypes[i].Selector(), pt.Selector())
		}
	}

	// JVM SDK: UNCAPTURED — must return ONLY process_cpu (not the async-profiler set).
	jvmTypes := SDKRuntimeTypes("jvm")
	if len(jvmTypes) != 1 || jvmTypes[0].Selector() != ProcessCPU.Selector() {
		t.Fatalf("SDKRuntimeTypes(jvm): expected [process_cpu] only, got %d types: %v", len(jvmTypes), jvmTypes)
	}
	// Specifically: must NOT contain the async-profiler types.
	for _, pt := range jvmTypes {
		if pt.Selector() == JavaMutexContentions.Selector() {
			t.Fatalf("SDKRuntimeTypes(jvm) must NOT contain JavaMutexContentions — that is Alloy-only")
		}
		if pt.Selector() == MemoryAllocInNewTLABBytes.Selector() {
			t.Fatalf("SDKRuntimeTypes(jvm) must NOT contain MemoryAllocInNewTLABBytes — that is Alloy-only")
		}
	}

	// Python SDK: process_cpu only.
	pyTypes := SDKRuntimeTypes("python")
	if len(pyTypes) != 1 || pyTypes[0].Selector() != ProcessCPU.Selector() {
		t.Fatalf("SDKRuntimeTypes(python): expected [process_cpu] only, got %d types", len(pyTypes))
	}

	// Uncaptured runtimes: all return [process_cpu] only.
	for _, rt := range []string{"node", "dotnet", "unknown", ""} {
		types := SDKRuntimeTypes(rt)
		if len(types) != 1 || types[0].Selector() != ProcessCPU.Selector() {
			t.Fatalf("SDKRuntimeTypes(%q): expected [process_cpu] only, got %d types", rt, len(types))
		}
	}
}

func TestProfileTypePeriods(t *testing.T) {
	// CPU types: 10_000_000 ns = 100Hz
	if ProcessCPU.Period != 10_000_000 {
		t.Fatalf("ProcessCPU period: got %d want 10_000_000", ProcessCPU.Period)
	}
	// memory types: 524288
	if MemoryAllocSpace.Period != 524288 {
		t.Fatalf("MemoryAllocSpace period: got %d want 524288", MemoryAllocSpace.Period)
	}
	// count-period types: 1
	if MutexContentions.Period != 1 {
		t.Fatalf("MutexContentions period: got %d want 1", MutexContentions.Period)
	}
}

func TestGoMutexVsJavaMutexPeriod(t *testing.T) {
	// Go mutex: period type is contentions:count
	if MutexContentions.PeriodType != "contentions" || MutexContentions.PeriodUnit != "count" {
		t.Fatalf("Go MutexContentions: period=%s:%s want contentions:count", MutexContentions.PeriodType, MutexContentions.PeriodUnit)
	}
	// Java mutex: period type is mutex:count (real capture difference)
	if JavaMutexContentions.PeriodType != "mutex" || JavaMutexContentions.PeriodUnit != "count" {
		t.Fatalf("JavaMutexContentions: period=%s:%s want mutex:count", JavaMutexContentions.PeriodType, JavaMutexContentions.PeriodUnit)
	}
}
