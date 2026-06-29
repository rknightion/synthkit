// SPDX-License-Identifier: AGPL-3.0-only

package profiles_test

import (
	"testing"

	"github.com/rknightion/synthkit/internal/telemetryspec"
	"github.com/rknightion/synthkit/internal/telemetryspec/profiles"
)

func TestGatewayNativeScrapeRegistered(t *testing.T) {
	p, ok := profiles.Lookup("gateway_native_scrape")
	if !ok {
		t.Fatal("profile gateway_native_scrape not registered")
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("gateway_native_scrape Validate: %v", err)
	}
	if len(p.Metrics) == 0 {
		t.Fatal("expected at least one MetricSpec")
	}

	// Build a lookup by name for targeted assertions.
	byName := make(map[string]telemetryspec.MetricSpec, len(p.Metrics))
	for _, m := range p.Metrics {
		byName[m.Name] = m
	}

	// request_count must be a counter (signals/portkey.md).
	rc, ok := byName["request_count"]
	if !ok {
		t.Fatal("metric request_count missing")
	}
	if rc.Instrument != telemetryspec.InstrumentCounter {
		t.Errorf("request_count: expected counter, got %q", rc.Instrument)
	}

	// llm_token_sum and llm_cost_sum must be gauges (signals/portkey.md ⚠ GAUGE).
	for _, name := range []string{"llm_token_sum", "llm_cost_sum"} {
		m, ok := byName[name]
		if !ok {
			t.Errorf("metric %s missing", name)
			continue
		}
		if m.Instrument != telemetryspec.InstrumentGauge {
			t.Errorf("%s: expected gauge (GAUGE trap), got %q", name, m.Instrument)
		}
	}

	// All histograms must have strictly ascending buckets.
	for _, m := range p.Metrics {
		if m.Instrument != telemetryspec.InstrumentHistogram {
			continue
		}
		if len(m.Buckets) == 0 {
			t.Errorf("histogram %s: no buckets", m.Name)
			continue
		}
		for i := 1; i < len(m.Buckets); i++ {
			if m.Buckets[i] <= m.Buckets[i-1] {
				t.Errorf("histogram %s: buckets not strictly ascending at index %d (%v <= %v)",
					m.Name, i, m.Buckets[i], m.Buckets[i-1])
			}
		}
	}

	// Spot-check a few expected metric names from signals/portkey.md.
	for _, name := range []string{
		"http_request_duration_seconds",
		"llm_request_duration_milliseconds",
		"portkey_request_duration_milliseconds",
		"portkey_processing_time_excluding_last_byte_ms",
		"llm_last_byte_diff_duration_milliseconds",
		"authentication_duration_milliseconds",
		"api_key_rate_limit_check_duration_milliseconds",
		"pre_request_processing_duration_milliseconds",
		"post_request_processing_duration_milliseconds",
		"llm_cache_processing_duration_milliseconds",
		"grpc_req_conversion_duration_milliseconds",
		"node_process_cpu_user_seconds_total",
		"node_process_resident_memory_bytes",
		"node_eventloop_lag_seconds",
		"node_gc_duration_seconds",
	} {
		if _, ok := byName[name]; !ok {
			t.Errorf("metric %s missing", name)
		}
	}

	// cacheStatus on universal metrics must NOT include "simple_hit" or "semantic_hit"
	// (signals/portkey.md ⚠).
	rc2 := byName["request_count"]
	if cs, ok := rc2.Labels["cacheStatus"]; ok {
		for _, e := range cs.Enum {
			if e.Value == "simple_hit" || e.Value == "semantic_hit" {
				t.Errorf("cacheStatus must not contain %q on metrics (metrics-only trap — log body only)", e.Value)
			}
		}
	}
}
