// SPDX-License-Identifier: AGPL-3.0-only

package profiles

import (
	"testing"

	"github.com/rknightion/synthkit/internal/telemetryspec"
)

func TestPythonRuntimeProfile(t *testing.T) {
	p, ok := Lookup("runtime_python")
	if !ok {
		t.Fatal("profile \"runtime_python\" not found in catalog")
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("profile \"runtime_python\" Validate() failed: %v", err)
	}
	if len(p.Metrics) < 3 {
		t.Fatalf("expected at least 3 metrics, got %d", len(p.Metrics))
	}
	wantNames := []string{
		"python_gc_objects_collected_total",
		"process_resident_memory_bytes",
		"process_cpu_seconds_total",
	}
	byName := make(map[string]telemetryspec.MetricSpec, len(p.Metrics))
	for _, m := range p.Metrics {
		byName[m.Name] = m
	}
	for _, name := range wantNames {
		if _, ok := byName[name]; !ok {
			t.Errorf("metric %q not found in runtime_python profile", name)
		}
	}
	// python_gc_objects_collected_total must be a counter with a generation label
	if m, ok := byName["python_gc_objects_collected_total"]; ok {
		if m.Instrument != telemetryspec.InstrumentCounter {
			t.Errorf("python_gc_objects_collected_total: want counter, got %q", m.Instrument)
		}
		if _, hasGen := m.Labels["generation"]; !hasGen {
			t.Error("python_gc_objects_collected_total: missing generation label")
		}
	}
}
