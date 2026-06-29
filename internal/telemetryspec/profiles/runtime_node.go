// SPDX-License-Identifier: AGPL-3.0-only

package profiles

// runtime_node — Node.js runtime scrape metrics.
//
// Metric names sourced from:
//   - nodejs_eventloop_lag_seconds: standard prom-client ≥14 metric for event-loop lag.
//     Not yet found in signals/ — PENDING SK-62 in cantfind.md.
//     Note: signals/portkey.md uses node_eventloop_lag_seconds (older prom-client name);
//     nodejs_* prefix is the current prom-client convention.
//   - nodejs_heap_size_used_bytes: standard prom-client heap gauge.
//     Not yet found in signals/ — PENDING SK-63 in cantfind.md.
//   - process_cpu_seconds_total: signals/k8s.md + signals/langsmith.md — v: ok.

import "github.com/rknightion/synthkit/internal/telemetryspec"

func init() {
	register(nodeRuntimeProfile())
}

func nodeRuntimeProfile() telemetryspec.Profile {
	return telemetryspec.Profile{
		Name: "runtime_node",
		Metrics: []telemetryspec.MetricSpec{
			{
				// Source: prom-client ≥14 standard Node.js metric (event-loop lag in seconds).
				// PENDING SK-62: nodejs_ prefix form not yet found in signals/;
				// signals/portkey.md has node_eventloop_lag_seconds (older form); see cantfind.md.
				Name:       "nodejs_eventloop_lag_seconds", // PENDING: cantfind SK-62
				Instrument: telemetryspec.InstrumentGauge,
				Unit:       "seconds",
				Labels:     map[string]telemetryspec.ValueModel{},
				Value: telemetryspec.ValueModel{
					// Healthy Node.js event loop lag: ~0–5ms; spikes under load
					Normal: &telemetryspec.Normal{Mean: 0.003, Stddev: 0.002},
				},
			},
			{
				// Source: prom-client standard Node.js heap used bytes gauge.
				// PENDING SK-63: name not yet found in signals/; see cantfind.md.
				Name:       "nodejs_heap_size_used_bytes", // PENDING: cantfind SK-63
				Instrument: telemetryspec.InstrumentGauge,
				Unit:       "bytes",
				Labels:     map[string]telemetryspec.ValueModel{},
				Value: telemetryspec.ValueModel{
					// Typical Node.js heap used: ~30–100 MiB; incident-aware shape
					Shape: &telemetryspec.ShapeModel{Base: 52_428_800}, // 50 MiB
				},
			},
			{
				// Source: signals/k8s.md (node-exporter process_* self-metrics) + signals/langsmith.md
				// v: ok (cross-language standard process exporter metric)
				Name:       "process_cpu_seconds_total",
				Instrument: telemetryspec.InstrumentCounter,
				Unit:       "seconds",
				Labels:     map[string]telemetryspec.ValueModel{},
				Value: telemetryspec.ValueModel{
					// Node.js is single-threaded event loop; CPU increment per tick ~0.05–0.3s
					FloatRange: &telemetryspec.FloatRange{Min: 0.05, Max: 0.3},
				},
			},
		},
	}
}
