// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

const customMetricBlueprint = `name: custom-proof
environments:
  - name: prod
    cluster:
      type: baremetal
      name: custom-proof-cluster
      emit: false
      nodes:
        - {hostname: fixture-node, ip: 192.0.2.1, cpus: 4, memory_gib: 16, arch: x86_64}
      platform:
        os_image: Ubuntu 24.04 LTS
        os_id: ubuntu
        container_runtime: containerd://2.1.7
        kubelet_version: v1.34.1
        kubernetes_version: v1.34.1
        kernel_version: 6.8.0
workloads:
  - type: app
    name: fixture-app
    runs_on: custom-proof-cluster
    otel: {metrics: %t}
    services:
      - name: api
        type: web
        custom_metrics:
          - name: http_requests_total
            source: https://prometheus.io/docs/practices/naming/
            instrument: counter
            labels: {method: {const_str: GET}}
            value: {const: 2}
          - name: http_request_duration_seconds
            source: https://prometheus.io/docs/practices/histograms/
            instrument: histogram
            unit: s
            labels: {method: {const_str: GET}}
            buckets: [0.5, 1]
            value: {const: 0.75}
`

type customNativeCapture struct{ resources []otlp.MetricResource }

func (c *customNativeCapture) Write(_ context.Context, rs []otlp.MetricResource) error {
	c.resources = append(c.resources, rs...)
	return nil
}

// This fixture owns its generic metric name and bounded dimension; it is not a catalog signal.
func TestCustomMetricIncidentOnset(t *testing.T) {
	prefix, _, _ := strings.Cut(fmt.Sprintf(customMetricBlueprint, true), "    services:")
	metric := `        custom_metrics:
          - name: declared_backlog
            source: 'fixture contract: instantaneous pending items by bounded queue'
            instrument: gauge
            labels: {queue: {enum: [{value: primary, weight: 1}, {value: secondary, weight: 1}]}}
            value: {shape: {base: 0, mode: error_spike, incident_value: 12}}
          - name: declared_legacy_shape
            source: 'fixture contract: legacy multiplicative gauge'
            instrument: gauge
            value: {shape: {base: 4, mode: error_spike}}
`
	src := prefix + "    services:\n      - name: api\n        type: web\n        entry: true\n        calls: [worker]\n" + metric +
		"      - name: worker\n        type: web\n" + metric + `incidents:
  - {kind: error_spike, target: api, at: '2026-06-17T11:00:00Z', for: 2m, intensity: 1}
  - {kind: error_spike, target: api, at: '2026-06-17T11:02:00Z', for: 1m, intensity: 0.25}
  - {kind: error_spike, at: '2026-06-17T11:04:00Z', for: 1m, intensity: 0.5}
  - {kind: latency_storm, target: api, at: '2026-06-17T11:05:00Z', for: 1m, intensity: 1}
`
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(src), reg)
	if err != nil {
		t.Fatal(err)
	}
	wi := res.Workloads[0]
	wr, _ := reg.Workload(wi.Kind)
	w, err := wr.Build(wi.Config, core.Binding{Name: wi.Name, Env: wi.Env, Cluster: wi.Cluster})
	if err != nil {
		t.Fatal(err)
	}
	native := &customNativeCapture{}
	world := &core.World{OTLPMetrics: native, Shape: shape.New("UTC", res.Incidents)}
	start := time.Date(2026, 6, 17, 11, 0, 0, 0, time.UTC)
	for _, step := range []struct {
		name         string
		offset       time.Duration
		volume       float64
		api          float64
		worker       float64
		legacyAPI    float64
		legacyWorker float64
	}{
		{"baseline", -time.Minute, 1, 0, 0, 4, 4},
		{"onset", 0, 1, 12, 0, 12, 4},
		{"paused during incident", time.Minute, 0, 0, 0, 0, 0},
		{"fractional intensity", 2 * time.Minute, 1, 3, 0, 6, 4},
		{"zero intensity", 3 * time.Minute, 1, 0, 0, 4, 4},
		{"unscoped incident and scaled volume", 4 * time.Minute, 2, 12, 12, 16, 16},
		{"recovery with unrelated mode", 5 * time.Minute, 1, 0, 0, 4, 4},
	} {
		t.Run(step.name, func(t *testing.T) {
			mc := &coretest.MetricCapture{}
			world.Metrics = mc
			world.Shape.SetVolumeMultiplier(step.volume)
			// Exercise a resolved zero intensity through the existing live input seam. The
			// public schedule encoder currently treats authored 0 as its omitted/default 1.
			world.Shape.Live = nil
			if step.name == "zero intensity" {
				world.Shape.Live = func(mode string) []shape.LiveFailure {
					if mode == "error_spike" {
						return []shape.LiveFailure{{Enabled: true, Intensity: 0, Scope: "api"}}
					}
					return nil
				}
			}
			native.resources = nil
			if err := w.Tick(context.Background(), start.Add(step.offset), world); err != nil {
				t.Fatal(err)
			}
			ss := mc.Find("declared_backlog")
			if len(ss) != 4 {
				t.Fatalf("want same four series even at zero: %+v", ss)
			}
			for _, s := range ss {
				want := step.api
				if s.Labels["service"] == "worker" {
					want = step.worker
				} else if s.Labels["service"] != "api" {
					t.Fatalf("unexpected service: %+v", s)
				}
				if s.Value != want || (s.Labels["queue"] != "primary" && s.Labels["queue"] != "secondary") {
					t.Fatalf("%+v want %v in bounded queue domain", s, want)
				}
			}
			legacy := mc.Find("declared_legacy_shape")
			if len(legacy) != 2 {
				t.Fatalf("legacy series: %+v", legacy)
			}
			for _, s := range legacy {
				want := step.legacyAPI
				if s.Labels["service"] == "worker" {
					want = step.legacyWorker
				}
				if s.Value != want {
					t.Fatalf("legacy output changed: %+v want %v", s, want)
				}
			}
			points := 0
			for _, r := range native.resources {
				for _, m := range r.Metrics {
					if m.Name != "declared_backlog" {
						continue
					}
					want := step.api
					if r.Attrs["service.name"] == "worker" {
						want = step.worker
					}
					for _, p := range m.Numbers {
						points++
						if m.Kind != otlp.MetricGauge || p.Value != want || len(p.Attrs) != 1 {
							t.Fatalf("native gauge: %+v want %v", m, want)
						}
					}
				}
			}
			if points != 4 {
				t.Fatalf("want four native gauge points, got %d", points)
			}
		})
	}
}

func TestCustomMetricBlueprintLoadAndRun(t *testing.T) {
	reg := runner.Catalog()
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			res, err := blueprint.Load([]byte(fmt.Sprintf(customMetricBlueprint, enabled)), reg)
			if err != nil {
				t.Fatal(err)
			}
			wi := res.Workloads[0]
			wr, _ := reg.Workload(wi.Kind)
			w, err := wr.Build(wi.Config, core.Binding{Name: wi.Name, Env: wi.Env, Cluster: wi.Cluster})
			if err != nil {
				t.Fatal(err)
			}
			mc, native := &coretest.MetricCapture{}, &customNativeCapture{}
			world := &core.World{Metrics: mc, OTLPMetrics: native, Shape: shape.New("", nil)}
			now := time.Date(2026, 6, 17, 11, 0, 0, 0, time.UTC)
			for tick := 1; tick <= 2; tick++ {
				mc = &coretest.MetricCapture{}
				world.Metrics = mc
				native.resources = nil
				if err := w.Tick(context.Background(), now.Add(time.Duration(tick-1)*time.Minute), world); err != nil {
					t.Fatal(err)
				}
				for name, want := range map[string]float64{"http_requests_total": float64(tick * 2), "http_request_duration_seconds_count": float64(tick), "http_request_duration_seconds_sum": float64(tick) * 0.75} {
					ss := mc.Find(name)
					if len(ss) != 1 || ss[0].Value != want || ss[0].Labels["method"] != "GET" || ss[0].Labels["service"] != "api" {
						t.Fatalf("%s: %+v want %v", name, ss, want)
					}
					if _, ok := ss[0].Labels["source"]; ok {
						t.Fatal("source leaked to wire")
					}
				}
				buckets := mc.Find("http_request_duration_seconds_bucket")
				if len(buckets) != 3 {
					t.Fatalf("buckets: %+v", buckets)
				}
				for _, b := range buckets {
					want := float64(tick)
					if b.Labels["le"] == "0.5" {
						want = 0
					}
					if b.Value != want {
						t.Fatalf("bucket: %+v", b)
					}
				}
				if !enabled {
					if len(native.resources) != 0 {
						t.Fatal("native emitted when disabled")
					}
					continue
				}
				if len(native.resources) != 1 || native.resources[0].Attrs["service.name"] != "api" {
					t.Fatalf("resource: %+v", native.resources)
				}
				metrics := native.resources[0].Metrics
				if len(metrics) != 2 {
					t.Fatalf("metrics: %+v", metrics)
				}
				counter, hist := metrics[0], metrics[1]
				if counter.Kind != otlp.MetricSum || !counter.Monotonic || counter.Temporality != otlp.TemporalityCumulative || counter.Numbers[0].Value != float64(tick*2) || !counter.Numbers[0].Start.Equal(now) {
					t.Fatalf("counter: %+v", counter)
				}
				hp := hist.Histograms[0]
				if hist.Kind != otlp.MetricHistogram || hist.Temporality != otlp.TemporalityCumulative || hp.Count != uint64(tick) || hp.Sum != float64(tick)*0.75 || !hp.Start.Equal(now) || !reflect.DeepEqual(hp.BucketCounts, []uint64{0, uint64(tick), 0}) {
					t.Fatalf("hist: %+v", hist)
				}
				if hp.Attrs["method"] != "GET" || len(hp.Attrs) != 1 || len(counter.Numbers[0].Attrs) != 1 {
					t.Fatalf("native labels: %+v %+v", hp.Attrs, counter.Numbers[0].Attrs)
				}
			}
		})
	}
}

// Custom declarations own a whole metric family per node, regardless of labels/units.
// Loading plus the registered Build is the public admission boundary under test.
func TestCustomMetricBlueprintRejectsNamespaceCollisions(t *testing.T) {
	counter := `          - name: http_requests_total
            source: https://prometheus.io/docs/practices/naming/
            instrument: counter
            unit: s
            labels: {method: {const_str: GET}}
            value: {const: 3}
`
	hist := `          - name: http_request_duration_seconds
            source: https://prometheus.io/docs/practices/histograms/
            instrument: histogram
            unit: s
            labels: {method: {const_str: GET}}
            buckets: [0.25, 2]
            value: {const: 0.75}
`
	valid := fmt.Sprintf(customMetricBlueprint, true)
	cases := map[string]string{
		"same-name incompatible units": valid + counter,
		"same-name disjoint labels":    valid + strings.ReplaceAll(strings.ReplaceAll(counter, "const_str: GET", "const_str: POST"), "            unit: s\n", ""),
		"histogram bounds":             valid + hist,
		"profile":                      strings.Replace(valid, "        custom_metrics:", "        profiles: [scraped_http_server]\n        custom_metrics:", 1) + strings.Replace(counter, "http_requests_total", "http_server_active_requests", 1),
		"legacy":                       strings.Replace(valid, "        custom_metrics:", "        metrics:\n"+strings.Replace(counter, "            source: https://prometheus.io/docs/practices/naming/\n", "", 1)+"        custom_metrics:", 1),
	}
	for _, suffix := range []string{"_bucket", "_sum", "_count"} {
		cases["histogram companion "+suffix] = valid + strings.Replace(counter, "http_requests_total", "http_request_duration_seconds"+suffix, 1)
	}
	// Reverse direction: the legacy scalar already owns the custom histogram's companion.
	cases["legacy histogram companion"] = strings.Replace(valid, "        custom_metrics:", "        metrics:\n"+strings.Replace(counter, "http_requests_total", "http_request_duration_seconds_count", 1)+"        custom_metrics:", 1)
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			reg := runner.Catalog()
			res, err := blueprint.Load([]byte(src), reg)
			if err == nil {
				wi := res.Workloads[0]
				wr, _ := reg.Workload(wi.Kind)
				_, err = wr.Build(wi.Config, core.Binding{Name: wi.Name, Env: wi.Env, Cluster: wi.Cluster})
			}
			if err == nil || !strings.Contains(err.Error(), "collid") {
				t.Fatalf("namespace collision must be rejected before ticking, got %v", err)
			}
		})
	}
}

func TestCustomMetricBlueprintLegacyOnlyAdmissionUnchanged(t *testing.T) {
	// Existing legacy duplicate declarations remain admitted; this feature only fences custom names.
	src := strings.Replace(fmt.Sprintf(customMetricBlueprint, true), "custom_metrics:", "metrics:", 1)
	src += `          - name: http_requests_total
            instrument: counter
            labels: {method: {const_str: GET}}
            value: {const: 3}
`
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(src), reg)
	if err != nil {
		t.Fatal(err)
	}
	wi := res.Workloads[0]
	wr, _ := reg.Workload(wi.Kind)
	if _, err := wr.Build(wi.Config, core.Binding{Name: wi.Name, Env: wi.Env, Cluster: wi.Cluster}); err != nil {
		t.Fatal(err)
	}
}

func TestCustomMetricBlueprintRejectsUnsafeDeclarations(t *testing.T) {
	valid := fmt.Sprintf(customMetricBlueprint, false)
	for name, src := range map[string]string{
		"missing source":       strings.ReplaceAll(valid, "source: https://prometheus.io/docs/practices/naming/", ""),
		"blank source":         strings.ReplaceAll(valid, "source: https://prometheus.io/docs/practices/naming/", "source: '   '"),
		"high-card key":        strings.ReplaceAll(valid, "method: {const_str: GET}", "trace_id: {const_str: fixed}"),
		"high-card ref":        strings.ReplaceAll(valid, "method: {const_str: GET}", "method: {ref: trace_id}"),
		"unknown field":        strings.ReplaceAll(valid, "instrument: counter", "instrument: counter\n            typo: true"),
		"invalid shape mode":   strings.Replace(valid, "value: {const: 2}", "value: {shape: {base: 0, incident_value: 12}}", 1),
		"invalid shape number": strings.Replace(valid, "value: {const: 2}", "value: {shape: {base: 0, mode: error_spike, incident_value: .nan}}", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := blueprint.Load([]byte(src), runner.Catalog())
			want := "source"
			if strings.HasPrefix(name, "high-card") {
				want = "high-card"
			}
			if name == "unknown field" {
				want = "typo"
			}
			if strings.HasPrefix(name, "invalid shape") {
				want = "shape"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("unsafe declaration: want %q error, got %v", want, err)
			}
		})
	}
}
