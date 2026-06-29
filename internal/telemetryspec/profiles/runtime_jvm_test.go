// SPDX-License-Identifier: AGPL-3.0-only

package profiles

import (
	"testing"

	"github.com/rknightion/synthkit/internal/telemetryspec"
)

func TestJVMRuntimeProfile(t *testing.T) {
	p, ok := Lookup("runtime_jvm")
	if !ok {
		t.Fatal("profile \"runtime_jvm\" not found in catalog")
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("profile \"runtime_jvm\" Validate() failed: %v", err)
	}
	if len(p.Metrics) < 3 {
		t.Fatalf("expected at least 3 metrics, got %d", len(p.Metrics))
	}
	wantNames := []string{
		"jvm_memory_used_bytes",
		"jvm_gc_pause_seconds",
		"process_cpu_seconds_total",
	}
	byName := make(map[string]telemetryspec.MetricSpec, len(p.Metrics))
	for _, m := range p.Metrics {
		byName[m.Name] = m
	}
	for _, name := range wantNames {
		if _, ok := byName[name]; !ok {
			t.Errorf("metric %q not found in runtime_jvm profile", name)
		}
	}
	if m, ok := byName["jvm_gc_pause_seconds"]; ok {
		if m.Instrument != telemetryspec.InstrumentHistogram {
			t.Errorf("jvm_gc_pause_seconds: want histogram, got %q", m.Instrument)
		}
		if len(m.Buckets) == 0 {
			t.Error("jvm_gc_pause_seconds: missing buckets")
		}
	}
}
