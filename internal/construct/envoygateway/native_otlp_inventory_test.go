// SPDX-License-Identifier: AGPL-3.0-only

package envoygateway

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// TestNativeDescriptorTablesMatchCaptures keeps the checked-in descriptor tables
// tied to the exact capture records even while datapoint emission is parked at
// the missing resource/attribute/bounds evidence boundary.
func TestNativeDescriptorTablesMatchCaptures(t *testing.T) {
	dataWant := readNativeCapture(t, "envoy-gateway-otlp-fc9ca6cd2ec0eba6.md", "## Full name and instrument list")
	controlWant := readNativeCapture(t, "beyla-envoygateway-otlp-588571dc6a53c4e4.md", "## SK-87 control plane")
	assertNativeCaptureCounts(t, dataWant, 162, map[otlp.MetricKind]int{
		otlp.MetricSum:       78,
		otlp.MetricGauge:     69,
		otlp.MetricHistogram: 15,
	})
	assertNativeCaptureCounts(t, controlWant, 12, map[otlp.MetricKind]int{
		otlp.MetricSum:       7,
		otlp.MetricGauge:     1,
		otlp.MetricHistogram: 4,
	})
	assertNativeDescriptors(t, nativeDataPlaneMetrics, dataWant)
	assertNativeDescriptors(t, nativeGatewayMetrics, controlWant)
}

func assertNativeCaptureCounts(t *testing.T, inventory map[string]otlp.MetricKind, wantNames int, want map[otlp.MetricKind]int) {
	t.Helper()
	if len(inventory) != wantNames {
		t.Errorf("capture has %d names, want %d", len(inventory), wantNames)
	}
	got := map[otlp.MetricKind]int{}
	for _, kind := range inventory {
		got[kind]++
	}
	for kind, expected := range want {
		if got[kind] != expected {
			t.Errorf("capture instrument %v count = %d, want %d", kind, got[kind], expected)
		}
	}
}

func assertNativeDescriptors(t *testing.T, descriptors []nativeMetricSpec, want map[string]otlp.MetricKind) {
	t.Helper()
	got := make(map[string]otlp.MetricKind, len(descriptors))
	for _, descriptor := range descriptors {
		if previous, ok := got[descriptor.Name]; ok && previous != descriptor.Kind {
			t.Errorf("descriptor %q has inconsistent kinds %v and %v", descriptor.Name, previous, descriptor.Kind)
		}
		got[descriptor.Name] = descriptor.Kind
	}
	if len(got) != len(want) {
		t.Errorf("descriptor count = %d, want %d", len(got), len(want))
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("descriptor %q = %v, want %v", name, got[name], kind)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected descriptor %q", name)
		}
	}
}

func readNativeCapture(t *testing.T, filename, heading string) map[string]otlp.MetricKind {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed while locating capture records")
	}
	path := filepath.Join(filepath.Dir(source), "../../../e2e/lab/captures", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture %s: %v", path, err)
	}

	active, fenced := false, false
	inventory := make(map[string]otlp.MetricKind)
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if !active && strings.HasPrefix(line, heading) {
			active = true
			continue
		}
		if !active {
			continue
		}
		if !fenced {
			if line == "```text" {
				fenced = true
			}
			continue
		}
		if strings.HasPrefix(line, "```") {
			break
		}
		fields := strings.Fields(strings.TrimSuffix(line, "```"))
		if len(fields) != 2 {
			continue
		}
		kind, ok := nativeCaptureKind(fields[1])
		if !ok {
			t.Fatalf("capture %s: unknown instrument %q in line %q", filename, fields[1], line)
		}
		inventory[fields[0]] = kind
	}
	if !active || !fenced || len(inventory) == 0 {
		t.Fatalf("capture %s: heading %q did not contain a metric inventory", filename, heading)
	}
	return inventory
}

func nativeCaptureKind(raw string) (otlp.MetricKind, bool) {
	switch raw {
	case "Gauge":
		return otlp.MetricGauge, true
	case "Sum":
		return otlp.MetricSum, true
	case "Histogram":
		return otlp.MetricHistogram, true
	default:
		return 0, false
	}
}
