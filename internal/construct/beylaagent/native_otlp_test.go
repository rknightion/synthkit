// SPDX-License-Identifier: AGPL-3.0-only

package beylaagent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

type beylaNativeMetricCapture struct {
	resources []otlp.MetricResource
}

func (c *beylaNativeMetricCapture) Write(_ context.Context, resources []otlp.MetricResource) error {
	c.resources = append(c.resources, resources...)
	return nil
}

func TestBeylaAgentNativeOTLPContract(t *testing.T) {
	c, err := Build(&Config{
		InternalMetrics: &InternalMetricsConfig{Exporter: internalMetricsExporterOTLP},
		Mode:            "kubernetes",
		Cluster:         "demo",
		Node:            "node-1",
	}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := c.Signals(); !reflect.DeepEqual(got, []core.SignalClass{core.OTLPMetrics}) {
		t.Fatalf("Signals()=%v want [otlp_metrics]", got)
	}

	legacy := &coretest.MetricCapture{}
	native := &beylaNativeMetricCapture{}
	world := coretest.World(legacy, nil, nil)
	world.OTLPMetrics = native
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := legacy.All(); len(got) != 0 {
		t.Fatalf("OTLP exporter wrote %d Prometheus series; want none", len(got))
	}
	if len(native.resources) != 1 {
		t.Fatalf("native resources=%d want one", len(native.resources))
	}

	want := readCapturedBeylaMetrics(t)
	metrics := native.resources[0].Metrics
	if len(metrics) != len(want) {
		t.Fatalf("native metric count=%d want %d: %#v", len(metrics), len(want), metrics)
	}
	for i, expected := range want {
		got := metrics[i]
		if got.Name != expected.name || got.Kind != expected.kind || got.Unit != expected.unit {
			t.Errorf("metric[%d]=(%q,%v,%q), want (%q,%v,%q)", i, got.Name, got.Kind, got.Unit, expected.name, expected.kind, expected.unit)
		}
		if len(got.Numbers) == 0 {
			t.Errorf("metric[%d] %q has no datapoints", i, got.Name)
		}
		if got.Kind == otlp.MetricSum {
			if !got.Monotonic || got.Temporality != otlp.TemporalityCumulative {
				t.Errorf("metric[%d] %q sum shape=(monotonic=%v temporality=%v), want monotonic cumulative", i, got.Name, got.Monotonic, got.Temporality)
			}
			for _, point := range got.Numbers {
				if !point.Start.Equal(now) || !point.Time.Equal(now) {
					t.Errorf("metric[%d] %q point times=(%s,%s), want start/time=%s", i, got.Name, point.Start, point.Time, now)
				}
			}
		} else {
			for _, point := range got.Numbers {
				if !point.Start.IsZero() || !point.Time.Equal(now) {
					t.Errorf("metric[%d] %q gauge point=%+v, want no start and time=%s", i, got.Name, point, now)
				}
			}
		}
	}
	wantResource := map[string]any{
		"service.name":             "opentelemetry-ebpf-instrumentation",
		"telemetry.sdk.language":   "go",
		"telemetry.sdk.name":       "beyla",
		"telemetry.sdk.version":    "v1.44.0",
		"telemetry.distro.name":    "opentelemetry-ebpf-instrumentation",
		"telemetry.distro.version": defaultVersion,
	}
	if got := native.resources[0].Attrs["service.instance.id"]; !validUUIDString(got) {
		t.Fatalf("service.instance.id=%v is not UUID-shaped", got)
	}
	for key, expected := range wantResource {
		if got := native.resources[0].Attrs[key]; got != expected {
			t.Errorf("resource attribute %q=%v, want %v", key, got, expected)
		}
	}
	if len(native.resources[0].Attrs) != len(wantResource)+1 {
		t.Errorf("resource attributes=%v, want exactly Beyla source attributes plus instance id", native.resources[0].Attrs)
	}
	if scope := native.resources[0].Scope; scope.Name != "obi_internal" || scope.Version != "" {
		t.Fatalf("instrumentation scope=%#v, want {Name:obi_internal Version:}", scope)
	}
	if native.resources[0].PreserveEmptyScope {
		t.Fatal("native resource must not preserve an empty scope after source scope resolution")
	}

	assertNativeAttrs(t, nativeMetric(t, native.resources[0], "beyla.internal.build.info"), map[string]any{
		"obi.goarch":    runtime.GOARCH,
		"obi.goos":      runtime.GOOS,
		"obi.goversion": runtime.Version(),
		"obi.revision":  defaultRevision,
		"obi.version":   defaultVersion,
	})
	assertNativeAttrsContains(t, nativeMetric(t, native.resources[0], "beyla.bpf.probe.executions"), map[string]any{
		"bpf.probe.id": "1", "bpf.probe.name": "kprobe_tcp_sendmsg", "bpf.probe.type": "kprobe",
	})
	assertNativeAttrKeys(t, nativeMetric(t, native.resources[0], "beyla.bpf.probe.executions"), []string{
		"bpf.probe.id", "bpf.probe.name", "bpf.probe.type",
	})
	assertNativeAttrKeys(t, nativeMetric(t, native.resources[0], "beyla.bpf.map.entries_total"), []string{
		"bpf.map.id", "bpf.map.name", "bpf.map.type",
	})
}

func readCapturedBeylaMetrics(t *testing.T) []struct {
	name string
	kind otlp.MetricKind
	unit string
} {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed while locating the Beyla capture")
	}
	capturePath := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "e2e", "lab", "captures", "beyla-envoygateway-otlp-588571dc6a53c4e4.md")
	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read Beyla capture %s: %v", capturePath, err)
	}
	inSection := false
	var out []struct {
		name string
		kind otlp.MetricKind
		unit string
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## SK-86") {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(line, "### ") {
			break
		}
		if !inSection {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 || !strings.HasPrefix(fields[0], "beyla.") || fields[2] != "unit:" {
			continue
		}
		var kind otlp.MetricKind
		switch fields[1] {
		case "Gauge":
			kind = otlp.MetricGauge
		case "Sum":
			kind = otlp.MetricSum
		default:
			t.Fatalf("unsupported instrument %q in Beyla capture line %q", fields[1], line)
		}
		unit := fields[3]
		if unit == "(empty)" {
			unit = ""
		}
		out = append(out, struct {
			name string
			kind otlp.MetricKind
			unit string
		}{name: fields[0], kind: kind, unit: unit})
	}
	if len(out) == 0 {
		t.Fatalf("Beyla capture contains no SK-86 metric records")
	}
	return out
}

func assertNativeAttrs(t *testing.T, metric otlp.Metric, want map[string]any) {
	t.Helper()
	for _, point := range metric.Numbers {
		if !reflect.DeepEqual(point.Attrs, want) {
			t.Errorf("%s datapoint attrs=%v, want %v", metric.Name, point.Attrs, want)
		}
	}
}

func assertNativeAttrsContains(t *testing.T, metric otlp.Metric, want map[string]any) {
	t.Helper()
	for _, point := range metric.Numbers {
		if reflect.DeepEqual(point.Attrs, want) {
			return
		}
	}
	t.Errorf("%s datapoint attrs do not contain %v: %v", metric.Name, want, metric.Numbers)
}

func assertNativeAttrKeys(t *testing.T, metric otlp.Metric, want []string) {
	t.Helper()
	wantSet := make(map[string]struct{}, len(want))
	for _, key := range want {
		wantSet[key] = struct{}{}
	}
	for _, point := range metric.Numbers {
		gotSet := make(map[string]struct{}, len(point.Attrs))
		for key := range point.Attrs {
			gotSet[key] = struct{}{}
		}
		if !reflect.DeepEqual(gotSet, wantSet) {
			t.Errorf("%s datapoint attr keys=%v, want %v", metric.Name, gotSet, wantSet)
		}
	}
}

func validUUIDString(value any) bool {
	s, ok := value.(string)
	if !ok || len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return s[14] == '5' && (s[19] == '8' || s[19] == '9' || s[19] == 'a' || s[19] == 'b')
}

func TestBeylaAgentNativeOTLPSumsAccumulate(t *testing.T) {
	c, err := Build(&Config{InternalMetrics: &InternalMetricsConfig{Exporter: internalMetricsExporterOTLP}}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t0 := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	first := &beylaNativeMetricCapture{}
	second := &beylaNativeMetricCapture{}
	for _, tc := range []struct {
		at  time.Time
		out *beylaNativeMetricCapture
	}{
		{at: t0, out: first},
		{at: t0.Add(time.Minute), out: second},
	} {
		if err := c.Tick(context.Background(), tc.at, &core.World{OTLPMetrics: tc.out}); err != nil {
			t.Fatalf("Tick at %s: %v", tc.at, err)
		}
	}
	a := nativeMetric(t, first.resources[0], metricBPFProbeExecOTLP).Numbers[0]
	b := nativeMetric(t, second.resources[0], metricBPFProbeExecOTLP).Numbers[0]
	if b.Value <= a.Value {
		t.Fatalf("probe execution sum did not accumulate: first=%v second=%v", a.Value, b.Value)
	}
	if !a.Start.Equal(t0) || !b.Start.Equal(t0) {
		t.Fatalf("probe execution start times=(%s,%s), want both %s", a.Start, b.Start, t0)
	}
}

func TestBeylaAgentExporterDisabledEmitsNeitherSurface(t *testing.T) {
	c, err := Build(&Config{InternalMetrics: &InternalMetricsConfig{Exporter: internalMetricsExporterDisabled}}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := c.Signals(); len(got) != 0 {
		t.Fatalf("Signals()=%v want no signal classes", got)
	}
	legacy := &coretest.MetricCapture{}
	native := &beylaNativeMetricCapture{}
	world := coretest.World(legacy, nil, nil)
	world.OTLPMetrics = native
	if err := c.Tick(context.Background(), time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC), world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := legacy.All(); len(got) != 0 {
		t.Fatalf("disabled exporter wrote %d Prometheus series", len(got))
	}
	if len(native.resources) != 0 {
		t.Fatalf("disabled exporter wrote %d native resources", len(native.resources))
	}
}

func TestBeylaAgentUnknownExporterRejected(t *testing.T) {
	if _, err := Build(&Config{InternalMetrics: &InternalMetricsConfig{Exporter: "bogus"}}, nil); err == nil {
		t.Fatal("unknown internal_metrics.exporter was accepted")
	}
}

func TestBeylaAgentPrometheusDefaultRemainsByteIdentical(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	cases := []*Config{
		{Mode: "kubernetes", Cluster: "demo", Node: "node-1"},
		{InternalMetrics: &InternalMetricsConfig{Exporter: internalMetricsExporterPrometheus}, Mode: "kubernetes", Cluster: "demo", Node: "node-1"},
	}
	captures := make([]*coretest.MetricCapture, 0, len(cases))
	for _, cfg := range cases {
		c, err := Build(cfg, nil)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		capture := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), now, coretest.World(capture, nil, nil)); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		captures = append(captures, capture)
	}
	got := make([][]promrw.Series, len(captures))
	for i, capture := range captures {
		got[i] = append([]promrw.Series(nil), capture.All()...)
		sort.Slice(got[i], func(a, b int) bool {
			left := got[i][a].Name + "\x00" + state.LabelSig(got[i][a].Labels)
			right := got[i][b].Name + "\x00" + state.LabelSig(got[i][b].Labels)
			return left < right
		})
	}
	if !reflect.DeepEqual(got[0], got[1]) {
		t.Fatalf("default Prometheus payload changed when exporter was made explicit\ndefault=%#v\nexplicit=%#v", got[0], got[1])
	}
}

func nativeMetric(t *testing.T, resource otlp.MetricResource, name string) otlp.Metric {
	t.Helper()
	for _, metric := range resource.Metrics {
		if metric.Name == name {
			return metric
		}
	}
	t.Fatalf("native metric %q not found", name)
	return otlp.Metric{}
}

var _ core.OTLPMetricWriter = (*beylaNativeMetricCapture)(nil)
