// SPDX-License-Identifier: AGPL-3.0-only

package envoygateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

type envoyNativeCapture struct {
	resources []otlp.MetricResource
}

func (c *envoyNativeCapture) Write(_ context.Context, resources []otlp.MetricResource) error {
	c.resources = append(c.resources, resources...)
	return nil
}

type envoyCapture struct {
	Planes map[string]envoyCapturePlane `json:"planes"`
}

type envoyCapturePlane struct {
	Metrics            []envoyCaptureMetric `json:"metrics"`
	ResourceAttributes map[string][]string  `json:"resource_attributes"`
	ResourceSchemaURLs []string             `json:"resource_schema_urls"`
	Scopes             []envoyCaptureScope  `json:"scopes"`
}

type envoyCaptureScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type envoyCaptureMetric struct {
	Name           string              `json:"name"`
	Instrument     []string            `json:"instrument"`
	Unit           []string            `json:"unit"`
	Temporality    []string            `json:"temporality"`
	Monotonic      []bool              `json:"monotonic"`
	DatapointAttrs map[string][]string `json:"datapoint_attributes"`
	ExplicitBounds [][]float64         `json:"explicit_bounds"`
}

// TestNativeOTLPContractMatchesFrozenCapture compares both emitted resources against the
// current machine contract. In particular, this is a set-diff over names, wire metadata,
// histogram bounds, and every family-local datapoint attribute/value set; it does not accept a
// broad union of attribute keys or a Prometheus-derived approximation.
func TestNativeOTLPContractMatchesFrozenCapture(t *testing.T) {
	capture := readEnvoyNativeCapture(t)
	now := time.Date(2026, 9, 5, 22, 20, 0, 0, time.UTC)
	native := &envoyNativeCapture{}
	c := buildNativeContractConstruct(t, &Config{
		ProxyTelemetry:   &TelemetryConfig{OTelSink: true},
		GatewayTelemetry: &TelemetryConfig{OTelSink: true},
	})
	metrics := &coretest.MetricCapture{}
	w := coretest.World(metrics, nil, nil)
	w.OTLPMetrics = native
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(native.resources) != 2 {
		t.Fatalf("native resources=%d, want one resource for each selected plane", len(native.resources))
	}

	for _, resource := range native.resources {
		planeName := nativePlaneForResource(resource)
		plane, ok := capture.Planes[planeName]
		if !ok {
			t.Fatalf("emitted resource does not identify a captured plane: attrs=%v", resource.Attrs)
		}
		assertNativeResource(t, planeName, resource, plane)
		assertNativeMetrics(t, planeName, resource.Metrics, plane.Metrics)
	}
}

func TestNativeOTLPGatesSelectOnlyTheirPlane(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cfg   *Config
		plane string
		count int
	}{
		{name: "gateway", cfg: &Config{GatewayTelemetry: &TelemetryConfig{OTelSink: true}}, plane: "control_plane", count: 16},
		{name: "proxy", cfg: &Config{ProxyTelemetry: &TelemetryConfig{OTelSink: true}}, plane: "data_plane", count: 206},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native := &envoyNativeCapture{}
			c := buildNativeContractConstruct(t, tc.cfg)
			w := coretest.World(nil, nil, nil)
			w.OTLPMetrics = native
			if err := c.Tick(context.Background(), time.Date(2026, 9, 5, 22, 20, 0, 0, time.UTC), w); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			if len(native.resources) != 1 {
				t.Fatalf("native resources=%d, want one", len(native.resources))
			}
			if got := nativePlaneForResource(native.resources[0]); got != tc.plane {
				t.Fatalf("native plane=%q, want %q", got, tc.plane)
			}
			if got := len(native.resources[0].Metrics); got != tc.count {
				t.Fatalf("native metric count=%d, want %d", got, tc.count)
			}
		})
	}
}

func TestNativeOTLPCumulativeTimesRemainStable(t *testing.T) {
	c := buildNativeContractConstruct(t, &Config{
		ProxyTelemetry:   &TelemetryConfig{OTelSink: true},
		GatewayTelemetry: &TelemetryConfig{OTelSink: true},
	})
	first, second := &envoyNativeCapture{}, &envoyNativeCapture{}
	t0 := time.Date(2026, 9, 5, 22, 20, 0, 0, time.UTC)
	for _, tc := range []struct {
		at  time.Time
		out *envoyNativeCapture
	}{
		{at: t0, out: first},
		{at: t0.Add(time.Minute), out: second},
	} {
		w := coretest.World(nil, nil, nil)
		w.OTLPMetrics = tc.out
		if err := c.Tick(context.Background(), tc.at, w); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	for _, plane := range []string{"control_plane", "data_plane"} {
		a := nativeResourceByPlane(t, first.resources, plane)
		b := nativeResourceByPlane(t, second.resources, plane)
		for i := range a.Metrics {
			if a.Metrics[i].Kind == otlp.MetricSum {
				if len(a.Metrics[i].Numbers) == 0 || len(b.Metrics[i].Numbers) == 0 {
					t.Fatalf("%s/%s has no Sum datapoints", plane, a.Metrics[i].Name)
				}
				if !a.Metrics[i].Numbers[0].Start.Equal(t0) || !b.Metrics[i].Numbers[0].Start.Equal(t0) {
					t.Errorf("%s/%s Sum start times=%s,%s, want %s", plane, a.Metrics[i].Name,
						a.Metrics[i].Numbers[0].Start, b.Metrics[i].Numbers[0].Start, t0)
				}
				if b.Metrics[i].Numbers[0].Value <= a.Metrics[i].Numbers[0].Value {
					t.Errorf("%s/%s Sum did not accumulate: first=%v second=%v", plane, a.Metrics[i].Name,
						a.Metrics[i].Numbers[0].Value, b.Metrics[i].Numbers[0].Value)
				}
			}
		}
	}
}

func TestNativeGaugeValuesUsePlausiblePrometheusLaneMagnitudes(t *testing.T) {
	tests := []struct {
		name    string
		factor  float64
		elapsed float64
		want    float64
	}{
		{name: "wasm_cache_entries", factor: 1.5, elapsed: 60, want: 0},
		{name: "control_plane.connected_state", factor: 1.5, elapsed: 60, want: 1},
		{name: "cluster.membership_total", factor: 1.5, elapsed: 60, want: 2},
		{name: "server.concurrency", factor: 1.5, elapsed: 60, want: 4},
		{name: "server.memory_allocated", factor: 1.5, elapsed: 60, want: 32 * 1024 * 1024},
		{name: "server.uptime", factor: 1.5, elapsed: 120, want: 120},
		{name: "cluster.upstream_cx_active", factor: 1.5, elapsed: 60, want: 1.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nativeGaugeValue(tt.name, tt.factor, tt.elapsed); got != tt.want {
				t.Errorf("nativeGaugeValue(%q, %v, %v) = %v, want %v", tt.name, tt.factor, tt.elapsed, got, tt.want)
			}
		})
	}
}

func readEnvoyNativeCapture(t *testing.T) envoyCapture {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed while locating Envoy Gateway capture")
	}
	path := filepath.Join(filepath.Dir(source), "../../../e2e/lab/captures/envoy-gateway-otlp-a5b65705f1b85dee.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture %s: %v", path, err)
	}
	var capture envoyCapture
	if err := json.Unmarshal(data, &capture); err != nil {
		t.Fatalf("decode capture %s: %v", path, err)
	}
	return capture
}

func buildNativeContractConstruct(t *testing.T, cfg *Config) core.Construct {
	t.Helper()
	cl := coretest.Cluster()
	wls := fixture.AddonWorkloads("envoy_gateway")
	for i := range wls {
		wls[i].PodNames = fixture.WorkloadPodNames("native-contract", wls[i], cl.Nodes)
		wls[i].NodeIdx = make([]int, wls[i].Replicas)
		for p := range wls[i].Replicas {
			wls[i].NodeIdx[p] = p % len(cl.Nodes)
		}
	}
	cl.SubstrateWorkloads = wls
	c, err := New(cfg, &fixture.Set{Seed: "native-contract", Cluster: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func nativePlaneForResource(resource otlp.MetricResource) string {
	if _, ok := resource.Attrs["service.name"]; ok {
		return "control_plane"
	}
	return "data_plane"
}

func nativeResourceByPlane(t *testing.T, resources []otlp.MetricResource, plane string) otlp.MetricResource {
	t.Helper()
	for _, resource := range resources {
		if nativePlaneForResource(resource) == plane {
			return resource
		}
	}
	t.Fatalf("native resource for %s not found", plane)
	return otlp.MetricResource{}
}

func assertNativeResource(t *testing.T, planeName string, got otlp.MetricResource, want envoyCapturePlane) {
	t.Helper()
	wantAttrs := make(map[string]any, len(want.ResourceAttributes))
	for key, values := range want.ResourceAttributes {
		if len(values) != 1 {
			t.Fatalf("%s resource attribute %q has %d captured values; resource metadata is incomplete", planeName, key, len(values))
		}
		wantAttrs[key] = values[0]
	}
	if !reflect.DeepEqual(got.Attrs, wantAttrs) {
		t.Errorf("%s resource attrs=%v, want exactly %v", planeName, got.Attrs, wantAttrs)
	}
	if len(want.Scopes) != 1 {
		t.Fatalf("%s captured scope count=%d, want one", planeName, len(want.Scopes))
	}
	if got.Scope.Name != want.Scopes[0].Name || got.Scope.Version != want.Scopes[0].Version {
		t.Errorf("%s scope=%+v, want %+v", planeName, got.Scope, want.Scopes[0])
	}
	wantEmptyScope := want.Scopes[0].Name == "" && want.Scopes[0].Version == ""
	if got.PreserveEmptyScope != wantEmptyScope {
		t.Errorf("%s PreserveEmptyScope=%v, want %v", planeName, got.PreserveEmptyScope, wantEmptyScope)
	}
	if len(want.ResourceSchemaURLs) != 1 {
		t.Fatalf("%s captured resource schema URL count=%d, want one", planeName, len(want.ResourceSchemaURLs))
	}
	if got.ResourceSchemaURL != want.ResourceSchemaURLs[0] {
		t.Errorf("%s ResourceSchemaURL=%q, want %q", planeName, got.ResourceSchemaURL, want.ResourceSchemaURLs[0])
	}
}

func assertNativeMetrics(t *testing.T, planeName string, got []otlp.Metric, want []envoyCaptureMetric) {
	t.Helper()
	gotByName := make(map[string]otlp.Metric, len(got))
	for _, metric := range got {
		if _, exists := gotByName[metric.Name]; exists {
			t.Errorf("%s emitted duplicate family %q", planeName, metric.Name)
		}
		gotByName[metric.Name] = metric
	}
	wantByName := make(map[string]envoyCaptureMetric, len(want))
	for _, metric := range want {
		if len(metric.Instrument) != 1 {
			t.Fatalf("%s/%s instrument metadata is incomplete: %v", planeName, metric.Name, metric.Instrument)
		}
		if _, exists := wantByName[metric.Name]; exists {
			t.Fatalf("%s capture repeats family %q", planeName, metric.Name)
		}
		wantByName[metric.Name] = metric
	}
	if len(gotByName) != len(wantByName) {
		t.Errorf("%s family count=%d, want %d", planeName, len(gotByName), len(wantByName))
	}
	for name, expected := range wantByName {
		metric, ok := gotByName[name]
		if !ok {
			t.Errorf("%s missing captured family %q", planeName, name)
			continue
		}
		assertNativeMetric(t, planeName, metric, expected)
	}
	for name := range gotByName {
		if _, ok := wantByName[name]; !ok {
			t.Errorf("%s emitted family not in capture %q", planeName, name)
		}
	}
}

func assertNativeMetric(t *testing.T, planeName string, got otlp.Metric, want envoyCaptureMetric) {
	t.Helper()
	wantKind := nativeCaptureKind(want.Instrument[0])
	if got.Kind != wantKind {
		t.Errorf("%s/%s kind=%v, want %v", planeName, want.Name, got.Kind, wantKind)
	}
	wantUnit := firstCaptureString(want.Unit)
	if got.Unit != wantUnit {
		t.Errorf("%s/%s unit=%q, want %q", planeName, want.Name, got.Unit, wantUnit)
	}
	if got.Kind == otlp.MetricSum {
		if !got.Monotonic || !firstCaptureBool(want.Monotonic) {
			t.Errorf("%s/%s monotonic=%v, want captured monotonic=%v", planeName, want.Name, got.Monotonic, firstCaptureBool(want.Monotonic))
		}
		if got.Temporality != otlp.TemporalityCumulative || firstCaptureString(want.Temporality) != "Cumulative" {
			t.Errorf("%s/%s temporality=%v, want cumulative", planeName, want.Name, got.Temporality)
		}
		if len(got.Numbers) == 0 {
			t.Errorf("%s/%s has no Sum datapoints", planeName, want.Name)
		}
	} else if got.Kind == otlp.MetricGauge {
		if len(got.Numbers) == 0 {
			t.Errorf("%s/%s has no Gauge datapoints", planeName, want.Name)
		}
	} else if got.Kind == otlp.MetricHistogram {
		if firstCaptureString(want.Temporality) != "Cumulative" {
			t.Errorf("%s/%s histogram temporality is not fully specified: %v", planeName, want.Name, want.Temporality)
		}
		if len(want.ExplicitBounds) != 1 {
			t.Fatalf("%s/%s histogram bounds metadata is incomplete: %v", planeName, want.Name, want.ExplicitBounds)
		}
		if len(got.Histograms) == 0 {
			t.Errorf("%s/%s has no Histogram datapoints", planeName, want.Name)
		}
		for _, point := range got.Histograms {
			if !reflect.DeepEqual(point.Bounds, want.ExplicitBounds[0]) {
				t.Errorf("%s/%s bounds=%v, want %v", planeName, want.Name, point.Bounds, want.ExplicitBounds[0])
			}
			if len(point.BucketCounts) != len(point.Bounds)+1 {
				t.Errorf("%s/%s bucket count length=%d, want %d", planeName, want.Name, len(point.BucketCounts), len(point.Bounds)+1)
			}
		}
	}
	assertNativeDatapointAttrs(t, planeName, got, want)
}

func assertNativeDatapointAttrs(t *testing.T, planeName string, got otlp.Metric, want envoyCaptureMetric) {
	t.Helper()
	gotValues := make(map[string]map[string]struct{})
	add := func(attrs map[string]any) {
		for key, value := range attrs {
			set := gotValues[key]
			if set == nil {
				set = make(map[string]struct{})
				gotValues[key] = set
			}
			set[fmt.Sprint(value)] = struct{}{}
		}
	}
	switch got.Kind {
	case otlp.MetricGauge, otlp.MetricSum:
		for _, point := range got.Numbers {
			add(point.Attrs)
		}
	case otlp.MetricHistogram:
		for _, point := range got.Histograms {
			add(point.Attrs)
		}
	}
	wantValues := make(map[string]map[string]struct{}, len(want.DatapointAttrs))
	for key, values := range want.DatapointAttrs {
		set := make(map[string]struct{}, len(values))
		for _, value := range values {
			set[value] = struct{}{}
		}
		wantValues[key] = set
	}
	if !reflect.DeepEqual(gotValues, wantValues) {
		t.Errorf("%s/%s datapoint attribute values=%v, want exactly %v", planeName, want.Name, gotValues, wantValues)
	}
}

func nativeCaptureKind(instrument string) otlp.MetricKind {
	switch instrument {
	case "Gauge":
		return otlp.MetricGauge
	case "Sum":
		return otlp.MetricSum
	case "Histogram":
		return otlp.MetricHistogram
	default:
		panic("unknown Envoy Gateway capture instrument: " + instrument)
	}
}

func firstCaptureString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func firstCaptureBool(values []bool) bool {
	if len(values) == 0 {
		return false
	}
	return values[0]
}
