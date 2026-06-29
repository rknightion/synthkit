// SPDX-License-Identifier: AGPL-3.0-only

package profiles

// scraped_http_server — OTel HTTP-server RED scrape metrics (NOT span-metrics).
//
// Metric names sourced from:
//   - signals/beyla.md [slug: beyla-red-application]: http_server_request_duration_seconds
//     is OTel semconv v1.38.0 Prometheus-mangled, live-confirmed reference cluster Beyla 3.20.0.
//     Buckets verified from Beyla bucket.go:22.
//   - http_server_active_requests: OTel HTTP semconv stable gauge for in-flight requests.
//     Name not yet found in signals/ — PENDING SK-59 in cantfind.md.

import "github.com/rknightion/synthkit/internal/telemetryspec"

func init() {
	register(httpServerProfile())
}

func httpServerProfile() telemetryspec.Profile {
	return telemetryspec.Profile{
		Name: "scraped_http_server",
		Metrics: []telemetryspec.MetricSpec{
			{
				// Source: signals/beyla.md [slug: beyla-red-application] — OTel semconv v1.38.0
				// live-confirmed reference cluster Beyla 3.20.0, 2026-06-15. Buckets verified from
				// bucket.go:22 (same as the standard OTel SDK HTTP duration set).
				Name:       "http_server_request_duration_seconds",
				Instrument: telemetryspec.InstrumentHistogram,
				Unit:       "seconds",
				Labels: map[string]telemetryspec.ValueModel{
					// http_request_method: OTel semconv label (live-confirmed, signals/beyla.md)
					"http_request_method": {
						Enum: []telemetryspec.EnumEntry{
							{Value: "GET", Weight: 0.7},
							{Value: "POST", Weight: 0.25},
							{Value: "PUT", Weight: 0.03},
							{Value: "DELETE", Weight: 0.02},
						},
					},
					// http_response_status_code: OTel semconv label (live-confirmed, signals/beyla.md)
					"http_response_status_code": {
						Enum: []telemetryspec.EnumEntry{
							{Value: "200", Weight: 0.9},
							{Value: "500", Weight: 0.05},
							{Value: "404", Weight: 0.03},
							{Value: "400", Weight: 0.02},
						},
					},
				},
				Value: telemetryspec.ValueModel{
					// Typical web request latency: mean 120ms, stddev 80ms (clamped ≥0)
					Normal: &telemetryspec.Normal{Mean: 0.12, Stddev: 0.08},
				},
				// Verified Beyla duration buckets from signals/beyla.md bucket.go:22
				Buckets: []float64{0, 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10},
				LEStyle: telemetryspec.LEStyleDotZero,
			},
			{
				// Source: OTel HTTP semconv — stable gauge for in-flight server requests.
				// PENDING SK-59: name not yet found in signals/; see cantfind.md.
				Name:       "http_server_active_requests", // PENDING: cantfind SK-59
				Instrument: telemetryspec.InstrumentGauge,
				Unit:       "requests",
				Labels:     map[string]telemetryspec.ValueModel{},
				Value: telemetryspec.ValueModel{
					// Incident-aware shape: base ~5 active requests, scales under load
					Shape: &telemetryspec.ShapeModel{Base: 5.0},
				},
			},
		},
	}
}
