// SPDX-License-Identifier: AGPL-3.0-only

package profiles

// runtime_go — Go language runtime scrape metrics.
//
// Metric names sourced from:
//   - go_goroutines: signals/fm.md [slug: fm-fleet] — v: ok (live-confirmed via Alloy/kubelet scrape)
//   - go_memstats_heap_inuse_bytes: signals/fm.md [slug: fm-fleet] — v: ok
//   - process_resident_memory_bytes: signals/k8s.md (node-exporter process_* self-metrics) — v: ok
//   - process_cpu_seconds_total: signals/k8s.md + signals/langsmith.md — v: ok

import "github.com/rknightion/synthkit/internal/telemetryspec"

func init() {
	register(goRuntimeProfile())
}

func goRuntimeProfile() telemetryspec.Profile {
	return telemetryspec.Profile{
		Name: "runtime_go",
		Metrics: []telemetryspec.MetricSpec{
			{
				// Source: signals/fm.md [slug: fm-fleet] — go_goroutines, v: ok
				// Standard Go runtime metric exposed by all Go Prometheus clients.
				Name:       "go_goroutines",
				Instrument: telemetryspec.InstrumentGauge,
				Unit:       "goroutines",
				Labels:     map[string]telemetryspec.ValueModel{},
				Value: telemetryspec.ValueModel{
					// Typical Go service: ~50–200 goroutines; incident-aware shape
					Normal: &telemetryspec.Normal{Mean: 80, Stddev: 30},
				},
			},
			{
				// Source: signals/fm.md [slug: fm-fleet] — go_memstats_heap_inuse_bytes, v: ok
				// Go runtime heap memory currently in use (bytes).
				Name:       "go_memstats_heap_inuse_bytes",
				Instrument: telemetryspec.InstrumentGauge,
				Unit:       "bytes",
				Labels:     map[string]telemetryspec.ValueModel{},
				Value: telemetryspec.ValueModel{
					// Typical Go service heap: ~30–80 MiB in use; incident-aware shape
					Shape: &telemetryspec.ShapeModel{Base: 52_428_800}, // 50 MiB
				},
			},
			{
				// Source: signals/k8s.md (node-exporter process_* self-metrics) — v: ok
				// Also: signals/langsmith.md (Python services) — same metric name is cross-language.
				Name:       "process_resident_memory_bytes",
				Instrument: telemetryspec.InstrumentGauge,
				Unit:       "bytes",
				Labels:     map[string]telemetryspec.ValueModel{},
				Value: telemetryspec.ValueModel{
					// Typical Go service RSS: ~60–150 MiB; incident-aware shape
					Shape: &telemetryspec.ShapeModel{Base: 83_886_080}, // 80 MiB
				},
			},
			{
				// Source: signals/k8s.md (node-exporter self-metrics) + signals/langsmith.md — v: ok
				// Cumulative CPU time consumed by this process (seconds). Counter: emit per-tick increment.
				Name:       "process_cpu_seconds_total",
				Instrument: telemetryspec.InstrumentCounter,
				Unit:       "seconds",
				Labels:     map[string]telemetryspec.ValueModel{},
				Value: telemetryspec.ValueModel{
					// Per-tick CPU increment: typically 0.05–0.5s per 5s tick for a moderate service
					FloatRange: &telemetryspec.FloatRange{Min: 0.05, Max: 0.5},
				},
			},
		},
	}
}
