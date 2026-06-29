// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import "github.com/rknightion/synthkit/internal/failuremode"

// FailureModes are the modes web_service workloads respond to (physics already in minter.go,
// scoped to the workload name via shape.Eval(now, mode, m.name)).
var FailureModes = []failuremode.Mode{
	{Name: "latency_spike", Axis: failuremode.AxisWorkload, Help: "elevated request latency (up to 4× at full intensity)"},
	{Name: "error_burst", Axis: failuremode.AxisWorkload, Help: "elevated 5xx error rate"},
	// Profiling failure modes — drive incident-responsive flamegraph amplification in tickProfiles.
	{Name: "cpu_hotspot", Axis: failuremode.AxisWorkload, Help: "elevated CPU concentrated in a hot frame (visible in process_cpu flamegraph)"},
	{Name: "memory_leak", Axis: failuremode.AxisWorkload, Help: "growing heap — raises memory inuse/alloc profile sample values"},
	{Name: "lock_contention", Axis: failuremode.AxisWorkload, Help: "elevated mutex/block contention — raises mutex and block profile sample values"},
	{Name: "goroutine_leak", Axis: failuremode.AxisWorkload, Help: "goroutine accumulation — raises goroutines/goroutine profile sample values"},
}
