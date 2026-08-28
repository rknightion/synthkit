// SPDX-License-Identifier: AGPL-3.0-only

package host

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

type nativeMetricCapture struct {
	resources []otlp.MetricResource
}

func (c *nativeMetricCapture) Write(_ context.Context, resources []otlp.MetricResource) error {
	c.resources = append(c.resources, resources...)
	return nil
}

func buildNativeHost(t *testing.T, h *fixture.Host, enabled bool) core.Construct {
	t.Helper()
	cfg := &Config{}
	if enabled {
		cfg.OTel = &OTelObs{Metrics: true}
	}
	c, err := Build(cfg, &fixture.Set{Seed: "bp:host:" + h.Hostname, Host: h})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

func tickNativeHost(t *testing.T, c core.Construct, at time.Time, native *nativeMetricCapture) []promrw.Series {
	t.Helper()
	metrics := &captureWriter{}
	w := &core.World{Shape: shape.New("", nil), Metrics: metrics}
	if native != nil {
		w.OTLPMetrics = native
	}
	if err := c.Tick(context.Background(), at, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return metrics.series
}

func TestNativeOTLPMetricsOffLeavesPrometheusLaneByteUnchanged(t *testing.T) {
	h := &fixture.Host{Hostname: "native-off", OS: "linux", Profile: "integration", NumCPU: 4, MemTotal: 8 << 30}
	at := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)

	legacy := buildNativeHost(t, h, false)
	legacySeries := tickNativeHost(t, legacy, at, nil)

	off := buildNativeHost(t, h, false)
	native := &nativeMetricCapture{}
	offSeries := tickNativeHost(t, off, at, native)

	if len(native.resources) != 0 {
		t.Fatalf("otel.metrics=false emitted %d native resources", len(native.resources))
	}
	if !reflect.DeepEqual(sortedPromSeries(legacySeries), sortedPromSeries(offSeries)) {
		t.Fatalf("otel.metrics=false changed the Prometheus series payload (legacy=%d off=%d)", len(legacySeries), len(offSeries))
	}
	if got := off.Signals(); !reflect.DeepEqual(got, []core.SignalClass{core.Metrics, core.Logs}) {
		t.Fatalf("Signals() with otel.metrics=false = %v, want [metrics logs]", got)
	}

	explicitOff, err := Build(&Config{OTel: &OTelObs{Metrics: false}}, &fixture.Set{
		Seed: "bp:host:" + h.Hostname, Host: h,
	})
	if err != nil {
		t.Fatalf("Build explicit false: %v", err)
	}
	explicitNative := &nativeMetricCapture{}
	tickNativeHost(t, explicitOff, at, explicitNative)
	if len(explicitNative.resources) != 0 {
		t.Fatalf("explicit otel.metrics=false emitted %d native resources", len(explicitNative.resources))
	}
}

func sortedPromSeries(in []promrw.Series) []promrw.Series {
	out := append([]promrw.Series(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		return promSeriesKey(out[i]) < promSeriesKey(out[j])
	})
	return out
}

func promSeriesKey(series promrw.Series) string {
	keys := make([]string, 0, len(series.Labels))
	for key := range series.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	labels := make([]string, 0, len(keys))
	for _, key := range keys {
		labels = append(labels, key+"="+series.Labels[key])
	}
	return fmt.Sprintf("%s\x00%s\x00%.17g\x00%d\x00%s", series.Name, strings.Join(labels, "\x00"), series.Value, series.Kind, series.T.UTC().Format(time.RFC3339Nano))
}

func TestNativeOTLPMetricsCapturedSurface(t *testing.T) {
	h := &fixture.Host{Hostname: "native-linux", OS: "linux", Profile: "integration", NumCPU: 2, MemTotal: 8 << 30}
	c := buildNativeHost(t, h, true)
	if got := c.Signals(); !reflect.DeepEqual(got, []core.SignalClass{core.Metrics, core.Logs, core.OTLPMetrics}) {
		t.Fatalf("Signals() with otel.metrics=true = %v, want native metrics lane", got)
	}

	cap := &nativeMetricCapture{}
	tickNativeHost(t, c, time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC), cap)
	if len(cap.resources) != 1 {
		t.Fatalf("native resources = %d, want one standalone host resource", len(cap.resources))
	}
	resource := cap.resources[0]
	if got := resource.Attrs["host.name"]; got != h.Hostname {
		t.Errorf("host.name resource attr = %v, want %q", got, h.Hostname)
	}
	if got := resource.Attrs["os.type"]; got != "linux" {
		t.Errorf("os.type resource attr = %v, want linux", got)
	}
	for _, key := range []string{"k8s.cluster.name", "k8s.node.name"} {
		if _, ok := resource.Attrs[key]; ok {
			t.Errorf("standalone host resource unexpectedly carries %q", key)
		}
	}
	if resource.Scope != nativeScope() {
		t.Errorf("scope = %+v, want %+v", resource.Scope, nativeScope())
	}

	wantNames := []string{
		"system.cpu.load_average.15m", "system.cpu.load_average.1m", "system.cpu.load_average.5m",
		"system.cpu.logical.count", "system.cpu.time",
		"system.disk.io", "system.disk.io_time", "system.disk.merged", "system.disk.operation_time",
		"system.disk.operations", "system.disk.pending_operations", "system.disk.weighted_io_time",
		"system.memory.limit", "system.memory.usage",
		"system.network.connections", "system.network.dropped", "system.network.errors",
		"system.network.io", "system.network.packets",
	}
	gotNames := make([]string, 0, len(resource.Metrics))
	byName := make(map[string]otlp.Metric, len(resource.Metrics))
	for _, metric := range resource.Metrics {
		gotNames = append(gotNames, metric.Name)
		byName[metric.Name] = metric
	}
	sort.Strings(gotNames)
	wantSorted := append([]string(nil), wantNames...)
	sort.Strings(wantSorted)
	if !reflect.DeepEqual(gotNames, wantSorted) {
		t.Fatalf("native family set = %v, want %v", gotNames, wantSorted)
	}
	if _, ok := byName["system.filesystem.usage"]; ok {
		t.Fatal("system.filesystem.usage is not present in the captured contract and must not be synthesized")
	}

	monotonic := map[string]bool{
		"system.cpu.time": true,
		"system.disk.io":  true, "system.disk.io_time": true, "system.disk.merged": true,
		"system.disk.operation_time": true, "system.disk.operations": true, "system.disk.weighted_io_time": true,
		"system.network.dropped": true, "system.network.errors": true,
		"system.network.io": true, "system.network.packets": true,
	}
	nonMonotonic := map[string]bool{
		"system.cpu.logical.count": true, "system.disk.pending_operations": true,
		"system.memory.limit": true, "system.memory.usage": true,
		"system.network.connections": true,
	}
	for name, metric := range byName {
		if len(metric.Numbers) == 0 {
			t.Errorf("%s has no datapoints", name)
		}
		switch {
		case monotonic[name]:
			if metric.Kind != otlp.MetricSum || !metric.Monotonic || metric.Temporality != otlp.TemporalityCumulative {
				t.Errorf("%s shape = kind %v monotonic %v temporality %v, want monotonic cumulative Sum", name, metric.Kind, metric.Monotonic, metric.Temporality)
			}
		case nonMonotonic[name]:
			if metric.Kind != otlp.MetricSum || metric.Monotonic || metric.Temporality != otlp.TemporalityCumulative {
				t.Errorf("%s shape = kind %v monotonic %v temporality %v, want non-monotonic cumulative Sum", name, metric.Kind, metric.Monotonic, metric.Temporality)
			}
		default:
			if metric.Kind != otlp.MetricGauge {
				t.Errorf("%s shape = kind %v, want Gauge", name, metric.Kind)
			}
		}
	}

	assertPointAttrs(t, byName["system.cpu.time"], "state", nativeLinuxCPUStates)
	assertPointAttrs(t, byName["system.memory.usage"], "state", nativeMemoryStates)
	assertPointAttrs(t, byName["system.network.connections"], "state", nativeTCPStates)
	assertPointAttrs(t, byName["system.disk.io"], "direction", nativeDiskDirections)
	assertPointAttrs(t, byName["system.network.io"], "direction", nativeNetworkDirections)
	for _, point := range byName["system.network.connections"].Numbers {
		if point.Attrs["protocol"] != "tcp" {
			t.Errorf("network connection protocol = %v, want tcp", point.Attrs["protocol"])
		}
	}
}

func assertPointAttrs(t *testing.T, metric otlp.Metric, key string, want []string) {
	t.Helper()
	got := make(map[string]bool)
	for _, point := range metric.Numbers {
		value, ok := point.Attrs[key].(string)
		if !ok || value == "" {
			t.Errorf("%s point missing string %s: %#v", metric.Name, key, point.Attrs)
			continue
		}
		got[value] = true
	}
	wantSet := make(map[string]bool, len(want))
	for _, value := range want {
		wantSet[value] = true
	}
	if !reflect.DeepEqual(got, wantSet) {
		t.Errorf("%s %s values = %v, want %v", metric.Name, key, got, wantSet)
	}
}

func TestNativeOTLPMetricsCPUStateSetIsPlatformSpecific(t *testing.T) {
	cases := []struct {
		name   string
		os     string
		states []string
		wantOS string
	}{
		{name: "linux", os: "linux", states: nativeLinuxCPUStates, wantOS: "linux"},
		{name: "windows", os: "windows", states: nativeNonLinuxCPUStates, wantOS: "windows"},
		{name: "macos", os: "darwin", states: nativeNonLinuxCPUStates, wantOS: "darwin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &fixture.Host{Hostname: "native-" + tc.name, OS: tc.os, Profile: "integration", NumCPU: 1, MemTotal: 8 << 30}
			cap := &nativeMetricCapture{}
			tickNativeHost(t, buildNativeHost(t, h, true), time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC), cap)
			if len(cap.resources) != 1 {
				t.Fatalf("resources = %d, want one", len(cap.resources))
			}
			resource := cap.resources[0]
			if resource.Attrs["os.type"] != tc.wantOS {
				t.Errorf("os.type = %v, want %s", resource.Attrs["os.type"], tc.wantOS)
			}
			var cpu otlp.Metric
			for _, metric := range resource.Metrics {
				if metric.Name == "system.cpu.time" {
					cpu = metric
					break
				}
			}
			if len(cpu.Numbers) != len(tc.states) {
				t.Fatalf("system.cpu.time points = %d, want %d", len(cpu.Numbers), len(tc.states))
			}
			assertPointAttrs(t, cpu, "state", tc.states)
			for _, point := range cpu.Numbers {
				if point.Attrs["state"] == "nice" || point.Attrs["state"] == "softirq" || point.Attrs["state"] == "steal" || point.Attrs["state"] == "wait" {
					if tc.os != "linux" {
						t.Errorf("non-Linux CPU state %q fabricated", point.Attrs["state"])
					}
				}
			}
		})
	}
}

func TestNativeOTLPMetricsCountersAccumulateWithStableStart(t *testing.T) {
	h := &fixture.Host{Hostname: "native-counter", OS: "linux", Profile: "integration", NumCPU: 1, MemTotal: 8 << 30}
	c := buildNativeHost(t, h, true)
	first, second := &nativeMetricCapture{}, &nativeMetricCapture{}
	t0 := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	tickNativeHost(t, c, t0, first)
	tickNativeHost(t, c, t0.Add(time.Minute), second)

	find := func(cap *nativeMetricCapture) otlp.NumberPoint {
		for _, resource := range cap.resources {
			for _, metric := range resource.Metrics {
				if metric.Name == "system.cpu.time" {
					for _, point := range metric.Numbers {
						if point.Attrs["cpu"] == "0" && point.Attrs["state"] == "user" {
							return point
						}
					}
				}
			}
		}
		return otlp.NumberPoint{}
	}
	a, b := find(first), find(second)
	if a.Time.IsZero() || b.Time.IsZero() {
		t.Fatal("system.cpu.time user point missing")
	}
	if b.Value <= a.Value {
		t.Errorf("system.cpu.time did not accumulate: first=%v second=%v", a.Value, b.Value)
	}
	if !a.Start.Equal(b.Start) || !a.Start.Equal(t0) {
		t.Errorf("counter start changed: first=%s second=%s want=%s", a.Start, b.Start, t0)
	}
}
