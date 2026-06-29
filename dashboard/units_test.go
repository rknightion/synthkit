// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import "testing"

func TestUnitFor(t *testing.T) {
	cases := []struct {
		name string
		inst InstrumentKind
		want string
	}{
		{"traces_spanmetrics_latency", HistogramNative, "s"}, // no suffix → seconds default
		{"http_request_duration_seconds", HistogramClassic, "s"},
		{"llm_request_duration_milliseconds", HistogramClassic, "ms"},
		{"portkey_processing_time_excluding_last_byte_ms", HistogramClassic, "ms"},
		{"coredns_dns_request_size_bytes", HistogramClassic, "bytes"},
		{"gen_ai_client_token_usage", HistogramClassic, "short"},
		{"kube_pod_status_ready", Gauge, "short"},
		{"node_memory_MemAvailable_bytes", Gauge, "bytes"},
		{"some_duration_seconds", Gauge, "s"}, // duration ending _seconds but NOT a timestamp suffix → stays "s"
		{"process_start_time_seconds", Gauge, "dateTimeAsIso"},
		{"node_boot_time_seconds", Gauge, "dateTimeAsIso"},
		{"certmanager_certificate_expiration_timestamp_seconds", Gauge, "dateTimeAsIso"},
		{"cache_hit_ratio", Gauge, "percentunit"},
		{"cpu_percent", Gauge, "percent"},
		{"portkey_requests_total", Counter, "short"},
		{"egress_bytes_total", Counter, "Bps"},
		{"traces_spanmetrics_size_total", Counter, "Bps"}, // byte-valued size counter
	}
	for _, c := range cases {
		if got := unitFor(c.name, c.inst); got != c.want {
			t.Errorf("unitFor(%q, %v) = %q, want %q", c.name, c.inst, got, c.want)
		}
	}
}
