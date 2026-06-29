// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import "strings"

// unitFor derives a Grafana unit string from a metric family base name + its instrument
// kind. DEFAULT only — MetricsOptions.UnitByFamily overrides it per family (Task 2).
//
// Histograms default to seconds: most synthkit histograms are latencies, and several
// (traces_spanmetrics_latency) carry no unit suffix, so seconds is the right default with
// explicit suffix overrides for ms/bytes/token families. Gauges default unitless (diverse).
// Counters are graphed as a rate; bytes/size counters → Bps, everything else → short (a
// generic count/sec has no truthful built-in unit, and asserting reqps would mislabel
// non-request counters — authors set the right unit via UnitByFamily).
//
// Gauge timestamp carve-out: several real gauges ending `_seconds` are Unix INSTANTS, not
// durations (process_start_time_seconds, node_boot_time_seconds, *_timestamp_seconds) — a
// plain "s" would render them as ~1.7 billion seconds. The precise instant suffixes
// (_timestamp_seconds / _start_time_seconds / _boot_time_seconds) map to dateTimeAsIso,
// while genuine durations (_time_seconds, _duration_seconds) keep "s".
func unitFor(name string, inst InstrumentKind) string {
	switch inst {
	case Counter:
		if strings.HasSuffix(name, "_bytes") || strings.HasSuffix(name, "_bytes_total") || strings.Contains(name, "_size_") {
			return "Bps"
		}
		return "short"
	case Gauge:
		switch {
		case strings.HasSuffix(name, "_timestamp_seconds") || strings.HasSuffix(name, "_start_time_seconds") || strings.HasSuffix(name, "_boot_time_seconds"):
			return "dateTimeAsIso"
		case strings.HasSuffix(name, "_seconds"):
			return "s"
		case strings.HasSuffix(name, "_milliseconds") || strings.HasSuffix(name, "_ms"):
			return "ms"
		case strings.HasSuffix(name, "_bytes"):
			return "bytes"
		case strings.HasSuffix(name, "_ratio"):
			return "percentunit"
		case strings.HasSuffix(name, "_percent"):
			return "percent"
		default:
			return "short"
		}
	default: // HistogramClassic / HistogramNative — unit of the quantile VALUE
		switch {
		case strings.HasSuffix(name, "_milliseconds") || strings.HasSuffix(name, "_ms"):
			return "ms"
		case strings.HasSuffix(name, "_bytes"):
			return "bytes"
		case strings.HasSuffix(name, "_token_usage") || strings.HasSuffix(name, "_tokens"):
			return "short"
		default:
			return "s" // latency default (incl. traces_spanmetrics_latency, which has no suffix)
		}
	}
}
