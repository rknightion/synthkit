// SPDX-License-Identifier: AGPL-3.0-only

package pyroscope

import "strings"

// ProfileType describes a single pprof profile type as captured from a real Grafana Pyroscope stack.
// The five string fields match the selector components returned by the Pyroscope series API.
// Period is the canonical sampling period for the type (e.g. 10_000_000 ns = 100 Hz for CPU).
type ProfileType struct {
	Name       string
	SampleType string
	SampleUnit string
	PeriodType string
	PeriodUnit string
	Period     int64
}

// Selector returns the colon-joined profile-type selector used by the Pyroscope query API:
// "<name>:<sample_type>:<sample_unit>:<period_type>:<period_unit>".
func (p ProfileType) Selector() string {
	return strings.Join([]string{p.Name, p.SampleType, p.SampleUnit, p.PeriodType, p.PeriodUnit}, ":")
}

// ── Go SDK / Alloy-scraped profile types ─────────────────────────────────────

// ProcessCPU is the canonical CPU wall-time profile emitted by both the Go SDK push path and
// the eBPF agent. Period = 10_000_000 ns = 100 Hz.
var ProcessCPU = ProfileType{
	Name: "process_cpu", SampleType: "cpu", SampleUnit: "nanoseconds",
	PeriodType: "cpu", PeriodUnit: "nanoseconds",
	Period: 10_000_000,
}

// ProcessCPUSamples is the sample-count variant of the CPU profile (Go SDK).
var ProcessCPUSamples = ProfileType{
	Name: "process_cpu", SampleType: "samples", SampleUnit: "count",
	PeriodType: "cpu", PeriodUnit: "nanoseconds",
	Period: 10_000_000,
}

// ── Memory ───────────────────────────────────────────────────────────────────

var MemoryAllocObjects = ProfileType{
	Name: "memory", SampleType: "alloc_objects", SampleUnit: "count",
	PeriodType: "space", PeriodUnit: "bytes",
	Period: 524288,
}

var MemoryAllocSpace = ProfileType{
	Name: "memory", SampleType: "alloc_space", SampleUnit: "bytes",
	PeriodType: "space", PeriodUnit: "bytes",
	Period: 524288,
}

var MemoryInuseObjects = ProfileType{
	Name: "memory", SampleType: "inuse_objects", SampleUnit: "count",
	PeriodType: "space", PeriodUnit: "bytes",
	Period: 524288,
}

var MemoryInuseSpace = ProfileType{
	Name: "memory", SampleType: "inuse_space", SampleUnit: "bytes",
	PeriodType: "space", PeriodUnit: "bytes",
	Period: 524288,
}

// ── Goroutines ───────────────────────────────────────────────────────────────

// GoroutinesSDK is the goroutine profile emitted via the Go SDK push path (plural "goroutines" name).
var GoroutinesSDK = ProfileType{
	Name: "goroutines", SampleType: "goroutine", SampleUnit: "count",
	PeriodType: "goroutine", PeriodUnit: "count",
	Period: 1,
}

// GoroutinePprof is the goroutine profile collected by Alloy pprof scrape (singular "goroutine" name).
var GoroutinePprof = ProfileType{
	Name: "goroutine", SampleType: "goroutine", SampleUnit: "count",
	PeriodType: "goroutine", PeriodUnit: "count",
	Period: 1,
}

// ── Go mutex / block ─────────────────────────────────────────────────────────

// MutexContentions is the Go mutex contention count. Period type = contentions:count (Go SDK).
var MutexContentions = ProfileType{
	Name: "mutex", SampleType: "contentions", SampleUnit: "count",
	PeriodType: "contentions", PeriodUnit: "count",
	Period: 1,
}

var MutexDelay = ProfileType{
	Name: "mutex", SampleType: "delay", SampleUnit: "nanoseconds",
	PeriodType: "contentions", PeriodUnit: "count",
	Period: 1,
}

var BlockContentions = ProfileType{
	Name: "block", SampleType: "contentions", SampleUnit: "count",
	PeriodType: "contentions", PeriodUnit: "count",
	Period: 1,
}

var BlockDelay = ProfileType{
	Name: "block", SampleType: "delay", SampleUnit: "nanoseconds",
	PeriodType: "contentions", PeriodUnit: "count",
	Period: 1,
}

// ── JVM-specific types ───────────────────────────────────────────────────────

// MemoryAllocInNewTLABBytes is a JVM TLAB allocation profile (bytes).
var MemoryAllocInNewTLABBytes = ProfileType{
	Name: "memory", SampleType: "alloc_in_new_tlab_bytes", SampleUnit: "bytes",
	PeriodType: "space", PeriodUnit: "bytes",
	Period: 524288,
}

// MemoryAllocInNewTLABObjects is a JVM TLAB allocation profile (object count).
var MemoryAllocInNewTLABObjects = ProfileType{
	Name: "memory", SampleType: "alloc_in_new_tlab_objects", SampleUnit: "count",
	PeriodType: "space", PeriodUnit: "bytes",
	Period: 524288,
}

// JavaMutexContentions is the JVM mutex contention profile. NOTE: period type is mutex:count,
// NOT contentions:count — real capture distinguishes JVM from Go mutex period unit.
var JavaMutexContentions = ProfileType{
	Name: "mutex", SampleType: "contentions", SampleUnit: "count",
	PeriodType: "mutex", PeriodUnit: "count",
	Period: 1,
}

// JavaMutexDelay is the JVM mutex delay profile. Period type mutex:count (JVM).
var JavaMutexDelay = ProfileType{
	Name: "mutex", SampleType: "delay", SampleUnit: "nanoseconds",
	PeriodType: "mutex", PeriodUnit: "count",
	Period: 1,
}

// ── Runtime sets ─────────────────────────────────────────────────────────────

// RuntimeTypes returns the canonical profile-type set for the given runtime identifier.
// Callers may subset this list via the ProfilingCfg.Types field.
//
// Runtimes:
//   - "go"     → CPU (wall + samples), memory (alloc/inuse objects+space), goroutines (SDK), mutex, block
//   - "jvm"    → CPU, TLAB alloc (bytes+objects), JVM mutex (contentions+delay)
//   - "python", "node", "dotnet" → process_cpu only (current SDKs; richer families are PENDING)
//   - default  → process_cpu only
func RuntimeTypes(runtime string) []ProfileType {
	switch runtime {
	case "go":
		return []ProfileType{
			ProcessCPU, ProcessCPUSamples,
			MemoryAllocObjects, MemoryAllocSpace, MemoryInuseObjects, MemoryInuseSpace,
			GoroutinesSDK,
			MutexContentions, MutexDelay,
			BlockContentions, BlockDelay,
		}
	case "jvm":
		return []ProfileType{
			ProcessCPU,
			MemoryAllocInNewTLABBytes, MemoryAllocInNewTLABObjects,
			JavaMutexContentions, JavaMutexDelay,
		}
	case "python", "node", "dotnet":
		return []ProfileType{ProcessCPU}
	default:
		return []ProfileType{ProcessCPU}
	}
}

// EBPFTypes returns the profile types emitted by an eBPF agent (process_cpu only).
func EBPFTypes() []ProfileType {
	return []ProfileType{ProcessCPU}
}

// SDKRuntimeTypes returns the profile-type set for the SDK-push lane for the given runtime.
// This is distinct from RuntimeTypes: it reflects what real SDK clients actually push, not
// what Alloy async-profiler collectors produce.
//
// Key differences from RuntimeTypes:
//   - "jvm": RuntimeTypes returns the full async-profiler set (TLAB alloc + Java mutex);
//     SDKRuntimeTypes returns only [ProcessCPU] because JVM SDK push shapes are UNCAPTURED —
//     those richer types are exclusively produced by the Alloy pyroscope.java collector
//     (source=alloy/pyroscope.java + pyroscope_spy=alloy.java + jfr_event=itimer).
//     Emitting async-profiler types via SDK push would fabricate a shape no real estate produces.
//   - "python": only [ProcessCPU] (matches pyroscope-io 1.0.11 reality).
//   - "node", "dotnet": UNCAPTURED → conservative [ProcessCPU] only.
//
// Runtimes:
//   - "go"                     → full Go SDK set (11 types — same as RuntimeTypes("go"))
//   - "python"                 → [ProcessCPU] (pyroscope-io SDK reality)
//   - "jvm", "node", "dotnet"  → [ProcessCPU] (UNCAPTURED — conservative)
//   - default                  → [ProcessCPU]
func SDKRuntimeTypes(runtime string) []ProfileType {
	switch runtime {
	case "go":
		return []ProfileType{
			ProcessCPU, ProcessCPUSamples,
			MemoryAllocObjects, MemoryAllocSpace, MemoryInuseObjects, MemoryInuseSpace,
			GoroutinesSDK,
			MutexContentions, MutexDelay,
			BlockContentions, BlockDelay,
		}
	default:
		// python, jvm, node, dotnet — all UNCAPTURED (or captured as process_cpu only).
		return []ProfileType{ProcessCPU}
	}
}
