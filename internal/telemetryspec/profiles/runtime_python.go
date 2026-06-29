// SPDX-License-Identifier: AGPL-3.0-only

package profiles

// runtime_python — Python runtime scrape metrics.
//
// Metric names sourced from:
//   - python_gc_objects_collected_total: signals/langsmith.md [slug: langsmith-platform]
//     v: ok (standard prometheus_client Python GC counter, confirmed on LangSmith Python services).
//   - process_resident_memory_bytes: signals/k8s.md (node-exporter self-metrics) +
//     signals/langsmith.md [slug: langsmith-platform] — v: ok.
//   - process_cpu_seconds_total: signals/k8s.md + signals/langsmith.md — v: ok.

import "github.com/rknightion/synthkit/internal/telemetryspec"

func init() {
	register(pythonRuntimeProfile())
}

func pythonRuntimeProfile() telemetryspec.Profile {
	return telemetryspec.Profile{
		Name: "runtime_python",
		Metrics: []telemetryspec.MetricSpec{
			{
				// Source: signals/langsmith.md [slug: langsmith-platform] — v: ok
				// Standard prometheus_client Python GC counter (per generation).
				Name:       "python_gc_objects_collected_total",
				Instrument: telemetryspec.InstrumentCounter,
				Unit:       "count",
				Labels: map[string]telemetryspec.ValueModel{
					// generation: GC generation (0=young, 1=middle, 2=old)
					"generation": {
						Enum: []telemetryspec.EnumEntry{
							{Value: "0", Weight: 0.7},
							{Value: "1", Weight: 0.2},
							{Value: "2", Weight: 0.1},
						},
					},
				},
				Value: telemetryspec.ValueModel{
					// Per-tick GC objects collected: generation-0 collects most frequently
					IntRange: &telemetryspec.IntRange{Min: 50, Max: 5000},
				},
			},
			{
				// Source: signals/k8s.md (node-exporter process_* self-metrics) +
				// signals/langsmith.md [slug: langsmith-platform] — v: ok
				Name:       "process_resident_memory_bytes",
				Instrument: telemetryspec.InstrumentGauge,
				Unit:       "bytes",
				Labels:     map[string]telemetryspec.ValueModel{},
				Value: telemetryspec.ValueModel{
					// Typical Python service RSS: ~80–200 MiB; incident-aware shape
					Shape: &telemetryspec.ShapeModel{Base: 104_857_600}, // 100 MiB
				},
			},
			{
				// Source: signals/k8s.md (node-exporter self-metrics) + signals/langsmith.md
				// v: ok (cross-language standard process exporter metric)
				Name:       "process_cpu_seconds_total",
				Instrument: telemetryspec.InstrumentCounter,
				Unit:       "seconds",
				Labels:     map[string]telemetryspec.ValueModel{},
				Value: telemetryspec.ValueModel{
					// Python CPU usage per tick: 0.05–0.8s (GIL-limited, but multiprocess common)
					FloatRange: &telemetryspec.FloatRange{Min: 0.05, Max: 0.8},
				},
			},
		},
	}
}
