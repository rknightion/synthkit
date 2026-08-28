// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster_test

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/k8scluster"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

type otlpMetricCapture struct {
	resources []otlp.MetricResource
}

func (c *otlpMetricCapture) Write(_ context.Context, resources []otlp.MetricResource) error {
	c.resources = append(c.resources, resources...)
	return nil
}

func buildNativeConstruct(t *testing.T, enabled bool) core.Construct {
	t.Helper()
	cl := coretest.Cluster()
	ds := fixture.Workload{Name: "alloy-logs", Namespace: "monitoring", Controller: "daemonset", Container: "alloy"}
	ds.PodNames = fixture.WorkloadPodNames(cl.Name, ds, cl.Nodes)
	for idx := range cl.Nodes {
		ds.NodeIdx = append(ds.NodeIdx, idx)
	}
	cl.SubstrateWorkloads = append(cl.SubstrateWorkloads, ds)
	cfg := &k8scluster.Config{OTel: &k8scluster.OTelObs{Metrics: enabled}}
	c, err := k8scluster.New(cfg, &fixture.Set{Cluster: cl, Env: cl.Env, Cloud: cl.Cloud})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func tickNative(t *testing.T, c core.Construct, at time.Time, out *otlpMetricCapture) *coretest.MetricCapture {
	t.Helper()
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, &coretest.LogCapture{}, nil)
	if out != nil {
		w.OTLPMetrics = out
	}
	if err := c.Tick(context.Background(), at, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc
}

func TestNativeOTLPMetricsSwitchPreservesPromRWWhenOff(t *testing.T) {
	cl := coretest.Cluster()
	legacy, err := k8scluster.New(nil, &fixture.Set{Cluster: cl, Env: cl.Env, Cloud: cl.Cloud})
	if err != nil {
		t.Fatalf("New legacy: %v", err)
	}
	off := buildNativeConstruct(t, false)

	if containsSignal(legacy.Signals(), core.OTLPMetrics) || containsSignal(off.Signals(), core.OTLPMetrics) {
		t.Fatal("absent/false otel.metrics must not declare OTLPMetrics")
	}
	at := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	legacyMetrics := tickNative(t, legacy, at, nil)
	offMetrics := tickNative(t, off, at, nil)
	if !reflect.DeepEqual(legacyMetrics.Names(), offMetrics.Names()) {
		t.Fatal("otel.metrics=false changed the existing Prometheus family set")
	}
	for _, name := range legacyMetrics.Names() {
		if !reflect.DeepEqual(legacyMetrics.LabelKeys(name), offMetrics.LabelKeys(name)) {
			t.Fatalf("otel.metrics=false changed Prometheus labels for %s", name)
		}
	}
}

func TestNativeOTLPMetricsObservedSurfaceAndWireKinds(t *testing.T) {
	c := buildNativeConstruct(t, true)
	if !containsSignal(c.Signals(), core.OTLPMetrics) {
		t.Fatal("otel.metrics=true must declare OTLPMetrics")
	}

	// A declared lane with no wired writer is safe; the runner deliberately leaves writers nil
	// for signal classes an instance did not receive.
	tickNative(t, c, time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC), nil)

	out := &otlpMetricCapture{}
	tickNative(t, c, time.Date(2026, 8, 28, 10, 1, 0, 0, time.UTC), out)
	if len(out.resources) == 0 {
		t.Fatal("otel.metrics=true emitted no MetricResource")
	}

	wantNames := nativeObservedNames()
	gotNames := map[string]bool{}
	for _, resource := range out.resources {
		if resource.Attrs["k8s.cluster.name"] == "" {
			t.Errorf("resource omitted k8s.cluster.name: %#v", resource.Attrs)
		}
		for _, metric := range resource.Metrics {
			gotNames[metric.Name] = true
			if strings.HasPrefix(metric.Name, "kube_") || strings.HasPrefix(metric.Name, "container_") {
				t.Errorf("Prometheus-shaped family was wrapped in OTLP: %q", metric.Name)
			}
			switch metric.Name {
			case "container.cpu.time", "k8s.pod.cpu.time", "k8s.pod.network.io", "k8s.pod.network.errors", "k8s.node.cpu.time", "k8s.node.network.io", "k8s.node.network.errors":
				if metric.Kind != otlp.MetricSum || !metric.Monotonic || metric.Temporality != otlp.TemporalityCumulative {
					t.Errorf("%s wire kind = kind %v monotonic %v temporality %v, want monotonic cumulative Sum", metric.Name, metric.Kind, metric.Monotonic, metric.Temporality)
				}
			case "k8s.container.status.reason":
				if metric.Kind != otlp.MetricSum || metric.Monotonic || metric.Temporality != otlp.TemporalityCumulative {
					t.Errorf("%s wire kind = kind %v monotonic %v temporality %v, want non-monotonic cumulative Sum", metric.Name, metric.Kind, metric.Monotonic, metric.Temporality)
				}
			default:
				if metric.Kind != otlp.MetricGauge {
					t.Errorf("%s wire kind = %v, want Gauge", metric.Name, metric.Kind)
				}
			}
		}
	}
	if !reflect.DeepEqual(sortedSet(gotNames), sortedSet(wantNames)) {
		t.Errorf("native family set mismatch\n got: %v\nwant: %v", sortedSet(gotNames), sortedSet(wantNames))
	}
}

func TestNativeOTLPCountersAreCumulativeWithStableStart(t *testing.T) {
	c := buildNativeConstruct(t, true)
	first := &otlpMetricCapture{}
	second := &otlpMetricCapture{}
	t0 := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	tickNative(t, c, t0, first)
	tickNative(t, c, t0.Add(time.Minute), second)

	a := firstNumber(first.resources, "container.cpu.time")
	b := firstNumber(second.resources, "container.cpu.time")
	if a == nil || b == nil {
		t.Fatal("container.cpu.time point missing")
	}
	if b.Value <= a.Value {
		t.Errorf("container.cpu.time did not accumulate: first=%v second=%v", a.Value, b.Value)
	}
	if !a.Start.Equal(b.Start) || !a.Start.Equal(t0) {
		t.Errorf("counter start changed: first=%s second=%s want=%s", a.Start, b.Start, t0)
	}
}

func containsSignal(signals []core.SignalClass, want core.SignalClass) bool {
	for _, signal := range signals {
		if signal == want {
			return true
		}
	}
	return false
}

func firstNumber(resources []otlp.MetricResource, name string) *otlp.NumberPoint {
	for _, resource := range resources {
		for _, metric := range resource.Metrics {
			if metric.Name == name && len(metric.Numbers) > 0 {
				return &metric.Numbers[0]
			}
		}
	}
	return nil
}

func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func nativeObservedNames() map[string]bool {
	names := []string{
		"container.cpu.time", "container.cpu.usage",
		"container.filesystem.available", "container.filesystem.capacity", "container.filesystem.usage",
		"container.memory.available", "container.memory.major_page_faults", "container.memory.page_faults", "container.memory.rss", "container.memory.usage", "container.memory.working_set",
		"k8s.pod.cpu.time", "k8s.pod.cpu.usage",
		"k8s.pod.filesystem.available", "k8s.pod.filesystem.capacity", "k8s.pod.filesystem.usage",
		"k8s.pod.memory.available", "k8s.pod.memory.major_page_faults", "k8s.pod.memory.page_faults", "k8s.pod.memory.rss", "k8s.pod.memory.usage", "k8s.pod.memory.working_set",
		"k8s.pod.network.io", "k8s.pod.network.errors",
		"k8s.node.cpu.time", "k8s.node.cpu.usage",
		"k8s.node.filesystem.available", "k8s.node.filesystem.capacity", "k8s.node.filesystem.usage",
		"k8s.node.memory.available", "k8s.node.memory.major_page_faults", "k8s.node.memory.page_faults", "k8s.node.memory.rss", "k8s.node.memory.usage", "k8s.node.memory.working_set",
		"k8s.node.network.io", "k8s.node.network.errors",
		"k8s.container.cpu_limit", "k8s.container.cpu_request", "k8s.container.memory_limit", "k8s.container.memory_request", "k8s.container.ready", "k8s.container.restarts", "k8s.container.status.reason",
		"k8s.pod.phase",
		"k8s.deployment.available", "k8s.deployment.desired",
		"k8s.replicaset.available", "k8s.replicaset.desired",
		"k8s.daemonset.current_scheduled_nodes", "k8s.daemonset.desired_scheduled_nodes", "k8s.daemonset.misscheduled_nodes", "k8s.daemonset.ready_nodes",
		"k8s.namespace.phase", "k8s.node.condition_ready",
	}
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}
