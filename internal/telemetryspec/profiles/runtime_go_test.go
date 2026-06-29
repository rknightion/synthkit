// SPDX-License-Identifier: AGPL-3.0-only

package profiles

import (
	"testing"

	"github.com/rknightion/synthkit/internal/telemetryspec"
)

func TestGoRuntimeProfile(t *testing.T) {
	p, ok := Lookup("runtime_go")
	if !ok {
		t.Fatal("profile \"runtime_go\" not found in catalog")
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("profile \"runtime_go\" Validate() failed: %v", err)
	}
	if len(p.Metrics) < 4 {
		t.Fatalf("expected at least 4 metrics, got %d", len(p.Metrics))
	}
	wantNames := []string{
		"go_goroutines",
		"go_memstats_heap_inuse_bytes",
		"process_resident_memory_bytes",
		"process_cpu_seconds_total",
	}
	byName := make(map[string]telemetryspec.MetricSpec, len(p.Metrics))
	for _, m := range p.Metrics {
		byName[m.Name] = m
	}
	for _, name := range wantNames {
		if _, ok := byName[name]; !ok {
			t.Errorf("metric %q not found in runtime_go profile", name)
		}
	}
	if m, ok := byName["process_cpu_seconds_total"]; ok && m.Instrument != telemetryspec.InstrumentCounter {
		t.Errorf("process_cpu_seconds_total: want counter, got %q", m.Instrument)
	}
}
