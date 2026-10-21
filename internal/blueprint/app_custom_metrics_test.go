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
		"missing source": strings.ReplaceAll(valid, "source: https://prometheus.io/docs/practices/naming/", ""),
		"blank source":   strings.ReplaceAll(valid, "source: https://prometheus.io/docs/practices/naming/", "source: '   '"),
		"high-card key":  strings.ReplaceAll(valid, "method: {const_str: GET}", "trace_id: {const_str: fixed}"),
		"high-card ref":  strings.ReplaceAll(valid, "method: {const_str: GET}", "method: {ref: trace_id}"),
		"unknown field":  strings.ReplaceAll(valid, "instrument: counter", "instrument: counter\n            typo: true"),
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
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("unsafe declaration: want %q error, got %v", want, err)
			}
		})
	}
}
