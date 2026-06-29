// SPDX-License-Identifier: AGPL-3.0-only

package profiles

// runtime_jvm — JVM language runtime scrape metrics.
//
// Metric names sourced from:
//   - jvm_memory_used_bytes: OTel JVM semconv (micrometer/OpenTelemetry JVM instrumentation).
//     Not yet found in signals/ — PENDING SK-60 in cantfind.md.
//   - jvm_gc_pause_seconds: micrometer/Spring Boot standard JVM GC pause histogram.
//     Not yet found in signals/ — PENDING SK-61 in cantfind.md.
//   - process_cpu_seconds_total: signals/k8s.md + signals/langsmith.md — v: ok.

import "github.com/rknightion/synthkit/internal/telemetryspec"

func init() {
	register(jvmRuntimeProfile())
}

func jvmRuntimeProfile() telemetryspec.Profile {
	return telemetryspec.Profile{
		Name: "runtime_jvm",
		Metrics: []telemetryspec.MetricSpec{
			{
				// Source: OTel JVM semconv / micrometer JVM instrumentation.
				// PENDING SK-60: name not yet found in signals/; see cantfind.md.
				Name:       "jvm_memory_used_bytes", // PENDING: cantfind SK-60
				Instrument: telemetryspec.InstrumentGauge,
				Unit:       "bytes",
				Labels: map[string]telemetryspec.ValueModel{
					// area: heap vs non-heap memory pool type
					"area": {
						Enum: []telemetryspec.EnumEntry{
							{Value: "heap", Weight: 0.6},
							{Value: "nonheap", Weight: 0.4},
						},
					},
				},
				Value: telemetryspec.ValueModel{
					// Typical JVM heap used: ~200–400 MiB; incident-aware shape
					Shape: &telemetryspec.ShapeModel{Base: 262_144_000}, // ~250 MiB
				},
			},
			{
				// Source: micrometer/Spring Boot standard JVM GC pause histogram.
				// PENDING SK-61: name not yet found in signals/; see cantfind.md.
				Name:       "jvm_gc_pause_seconds", // PENDING: cantfind SK-61
				Instrument: telemetryspec.InstrumentHistogram,
				Unit:       "seconds",
				Labels: map[string]telemetryspec.ValueModel{
					// action: GC cause / type
					"action": {
						Enum: []telemetryspec.EnumEntry{
							{Value: "end of minor GC", Weight: 0.8},
							{Value: "end of major GC", Weight: 0.2},
						},
					},
					// cause: what triggered the GC
					"cause": {
						Enum: []telemetryspec.EnumEntry{
							{Value: "G1 Young Generation", Weight: 0.7},
							{Value: "G1 Old Generation", Weight: 0.15},
							{Value: "Metadata GC Threshold", Weight: 0.15},
						},
					},
				},
				Value: telemetryspec.ValueModel{
					// GC pause duration: mean ~20ms minor, tail ~200ms major
					Normal: &telemetryspec.Normal{Mean: 0.035, Stddev: 0.025},
				},
				// Standard seconds histogram buckets covering sub-ms to multi-second GC pauses
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
				LEStyle: telemetryspec.LEStyleDotZero,
			},
			{
				// Source: signals/k8s.md (node-exporter process_* self-metrics) + signals/langsmith.md
				// v: ok (cross-language standard process exporter metric)
				Name:       "process_cpu_seconds_total",
				Instrument: telemetryspec.InstrumentCounter,
				Unit:       "seconds",
				Labels:     map[string]telemetryspec.ValueModel{},
				Value: telemetryspec.ValueModel{
					// JVM services tend to use more CPU than Go; 0.1–1.0s per 5s tick typical
					FloatRange: &telemetryspec.FloatRange{Min: 0.1, Max: 1.0},
				},
			},
		},
	}
}
