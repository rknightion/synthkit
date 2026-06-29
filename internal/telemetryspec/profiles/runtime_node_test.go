// SPDX-License-Identifier: AGPL-3.0-only

package profiles

import (
	"testing"

	"github.com/rknightion/synthkit/internal/telemetryspec"
)

func TestNodeRuntimeProfile(t *testing.T) {
	p, ok := Lookup("runtime_node")
	if !ok {
		t.Fatal("profile \"runtime_node\" not found in catalog")
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("profile \"runtime_node\" Validate() failed: %v", err)
	}
	if len(p.Metrics) < 3 {
		t.Fatalf("expected at least 3 metrics, got %d", len(p.Metrics))
	}
	wantNames := []string{
		"nodejs_eventloop_lag_seconds",
		"nodejs_heap_size_used_bytes",
		"process_cpu_seconds_total",
	}
	byName := make(map[string]telemetryspec.MetricSpec, len(p.Metrics))
	for _, m := range p.Metrics {
		byName[m.Name] = m
	}
	for _, name := range wantNames {
		if _, ok := byName[name]; !ok {
			t.Errorf("metric %q not found in runtime_node profile", name)
		}
	}
	if m, ok := byName["nodejs_eventloop_lag_seconds"]; ok && m.Instrument != telemetryspec.InstrumentGauge {
		t.Errorf("nodejs_eventloop_lag_seconds: want gauge, got %q", m.Instrument)
	}
}
