// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

type appNativeMetricCapture struct {
	resources []otlp.MetricResource
}

func (c *appNativeMetricCapture) Write(_ context.Context, resources []otlp.MetricResource) error {
	c.resources = append(c.resources, resources...)
	return nil
}

func appNativeConfig(enabled bool, profiles []string) *Config {
	gauge, counter, histValue := 3.0, 2.0, 0.75
	zone := "blue"
	return &Config{
		OTel: func() *OTelObs {
			if !enabled {
				return nil
			}
			return &OTelObs{Metrics: true}
		}(),
		Services: []ServiceNode{{
			Name:     "api",
			Type:     "web",
			Runtime:  "go",
			Entry:    true,
			Profiles: profiles,
			Metrics: []telemetryspec.MetricSpec{
				{Name: "app_queue_depth", Instrument: telemetryspec.InstrumentGauge, Unit: "{item}", Value: telemetryspec.ValueModel{Const: &gauge}, Labels: map[string]telemetryspec.ValueModel{"zone": {ConstStr: &zone}}},
				{Name: "app_requests_total", Instrument: telemetryspec.InstrumentCounter, Unit: "{request}", Value: telemetryspec.ValueModel{Const: &counter}, Labels: map[string]telemetryspec.ValueModel{"zone": {ConstStr: &zone}}},
				{Name: "app_request_duration", Instrument: telemetryspec.InstrumentHistogram, Unit: "s", Value: telemetryspec.ValueModel{Const: &histValue}, Buckets: []float64{0.5, 1}, Labels: map[string]telemetryspec.ValueModel{"zone": {ConstStr: &zone}}},
			},
		}},
	}
}

func appNativeWorld(mc *coretest.MetricCapture, native *appNativeMetricCapture) *core.World {
	w := &core.World{Shape: shape.New("", nil), EmitSpanMetrics: false}
	if mc != nil {
		w.Metrics = mc
	}
	if native != nil {
		w.OTLPMetrics = native
	}
	return w
}

func appNativeMetric(t *testing.T, resource otlp.MetricResource, name string) otlp.Metric {
	t.Helper()
	for _, metric := range resource.Metrics {
		if metric.Name == name {
			return metric
		}
	}
	t.Fatalf("native metric %q not found", name)
	return otlp.Metric{}
}

func TestAppNativeSignalsFollowExplicitSwitch(t *testing.T) {
	off := buildApp(t, appNativeConfig(false, nil))
	if slices.Contains(off.Signals(), core.OTLPMetrics) {
		t.Fatal("absent otel.metrics switch must not declare native metrics")
	}

	on := buildApp(t, appNativeConfig(true, nil))
	if !slices.Contains(on.Signals(), core.OTLPMetrics) {
		t.Fatal("otel.metrics=true must declare native metrics")
	}

	cfg := appNativeConfig(false, nil)
	cfg.OTel = &OTelObs{Metrics: false}
	if slices.Contains(buildApp(t, cfg).Signals(), core.OTLPMetrics) {
		t.Fatal("otel.metrics=false must not declare native metrics")
	}
}

func TestAppNativeInlineMetricsPreserveShapeAndIdentityBoundary(t *testing.T) {
	w := buildApp(t, appNativeConfig(true, nil))
	native := &appNativeMetricCapture{}
	mc := &coretest.MetricCapture{}
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	if err := w.Tick(context.Background(), now, appNativeWorld(mc, native)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(native.resources) != 1 {
		t.Fatalf("native resources = %d, want one node resource", len(native.resources))
	}
	resource := native.resources[0]
	if got := resource.Attrs["service.name"]; got != "api" {
		t.Errorf("service.name = %v, want api", got)
	}
	if got := resource.Attrs["k8s.cluster.name"]; got != "test-prod-use1" {
		t.Errorf("k8s.cluster.name = %v, want test-prod-use1", got)
	}
	if got := resource.Attrs["deployment.environment.name"]; got != "prod" {
		t.Errorf("deployment.environment.name = %v, want prod", got)
	}

	if got := []string{resource.Metrics[0].Name, resource.Metrics[1].Name, resource.Metrics[2].Name}; !reflect.DeepEqual(got, []string{"app_queue_depth", "app_requests_total", "app_request_duration"}) {
		t.Fatalf("native metric order = %v, want declaration order", got)
	}
	gauge := appNativeMetric(t, resource, "app_queue_depth")
	if gauge.Kind != otlp.MetricGauge || gauge.Unit != "{item}" || len(gauge.Numbers) != 1 {
		t.Fatalf("gauge shape = %+v", gauge)
	}
	if !gauge.Numbers[0].Start.IsZero() || gauge.Numbers[0].Value != 3 {
		t.Errorf("gauge point = %+v, want value 3 and no start", gauge.Numbers[0])
	}

	counter := appNativeMetric(t, resource, "app_requests_total")
	if counter.Kind != otlp.MetricSum || !counter.Monotonic || counter.Temporality != otlp.TemporalityCumulative || counter.Unit != "{request}" {
		t.Fatalf("counter shape = %+v", counter)
	}
	if len(counter.Numbers) != 1 || counter.Numbers[0].Value != 2 || !counter.Numbers[0].Start.Equal(now) {
		t.Errorf("counter point = %+v, want cumulative value 2 and start %s", counter.Numbers, now)
	}

	hist := appNativeMetric(t, resource, "app_request_duration")
	if hist.Kind != otlp.MetricHistogram || hist.Temporality != otlp.TemporalityCumulative || hist.Unit != "s" || len(hist.Histograms) != 1 {
		t.Fatalf("histogram shape = %+v", hist)
	}
	hp := hist.Histograms[0]
	if !hp.Start.Equal(now) || hp.Count != 1 || hp.Sum != 0.75 || !reflect.DeepEqual(hp.Bounds, []float64{0.5, 1}) || !reflect.DeepEqual(hp.BucketCounts, []uint64{0, 1, 0}) {
		t.Errorf("histogram point = %+v", hp)
	}
	if got := hp.Attrs["zone"]; got != "blue" {
		t.Errorf("DSL label zone = %v, want blue", got)
	}
	for _, key := range []string{"service", "service_name", "namespace", "cluster", "job"} {
		if _, ok := hp.Attrs[key]; ok {
			t.Errorf("identity label %q leaked into native datapoint attrs", key)
		}
	}

	// Counter/histogram state is cumulative and keeps the same native start across ticks.
	native2 := &appNativeMetricCapture{}
	if err := w.Tick(context.Background(), now.Add(time.Minute), appNativeWorld(nil, native2)); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	second := appNativeMetric(t, native2.resources[0], "app_requests_total")
	if second.Numbers[0].Value != 4 || !second.Numbers[0].Start.Equal(now) {
		t.Errorf("second counter point = %+v, want value 4 with stable start %s", second.Numbers[0], now)
	}
}

func TestAppNativeProfileMetricsRemainPromrwOnly(t *testing.T) {
	w := buildApp(t, appNativeConfig(true, []string{"runtime_go"}))
	native := &appNativeMetricCapture{}
	mc := &coretest.MetricCapture{}
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	if err := w.Tick(context.Background(), now, appNativeWorld(mc, native)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(native.resources) != 1 {
		t.Fatalf("native resources = %d, want one resource for inline metrics", len(native.resources))
	}
	for _, metric := range native.resources[0].Metrics {
		if metric.Name == "process_cpu_seconds_total" || metric.Name == "go_goroutines" {
			t.Fatalf("catalog profile metric %q was re-emitted as native", metric.Name)
		}
	}
	if len(mc.Find("process_cpu_seconds_total")) == 0 {
		t.Fatal("catalog profile metric did not remain on promrw")
	}
}

func TestAppNativeNilWriterIsSafe(t *testing.T) {
	w := buildApp(t, appNativeConfig(true, nil))
	if err := w.Tick(context.Background(), time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC), &core.World{Shape: shape.New("", nil)}); err != nil {
		t.Fatalf("Tick with nil writers: %v", err)
	}
}

func TestAppNativeSwitchLeavesPromrwPayloadUnchanged(t *testing.T) {
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	off := buildApp(t, appNativeConfig(false, nil))
	offCapture := &coretest.MetricCapture{}
	if err := off.Tick(context.Background(), now, appNativeWorld(offCapture, nil)); err != nil {
		t.Fatalf("off Tick: %v", err)
	}

	on := buildApp(t, appNativeConfig(true, nil))
	onCapture := &coretest.MetricCapture{}
	if err := on.Tick(context.Background(), now, appNativeWorld(onCapture, &appNativeMetricCapture{})); err != nil {
		t.Fatalf("on Tick: %v", err)
	}
	offSeries, onSeries := offCapture.All(), onCapture.All()
	for _, series := range [][]promrw.Series{offSeries, onSeries} {
		sort.Slice(series, func(i, j int) bool {
			left := series[i].Name + "|" + state.LabelSig(series[i].Labels)
			right := series[j].Name + "|" + state.LabelSig(series[j].Labels)
			return left < right
		})
	}
	if !reflect.DeepEqual(offSeries, onSeries) {
		t.Fatalf("enabling native OTLP changed the Prometheus payload\noff=%#v\non=%#v", offSeries, onSeries)
	}
}

// Keep the test's type assertion explicit: native OTLP is a distinct writer seam from promrw.
var _ core.OTLPMetricWriter = (*appNativeMetricCapture)(nil)
var _ core.MetricWriter = (*coretest.MetricCapture)(nil)
