// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

type captureOTLP struct{ got []otlp.MetricResource }

func (c *captureOTLP) Write(_ context.Context, rs []otlp.MetricResource) error {
	c.got = append(c.got, rs...)
	return nil
}

// newOTLPTestWorkload builds a web_service workload with the OTLP metrics lane enabled.
// It reuses the existing testBinding helper (from projection_test.go) which gives a real
// cluster placement + db hop, matching the pattern used across the rest of the package.
func newOTLPTestWorkload(t *testing.T, mode string) (*Workload, *core.World, *captureOTLP) {
	t.Helper()
	cfg := NewConfig().(*Config)
	cfg.OTel = &OTelObs{Metrics: true, Mode: mode}
	// testBinding returns a core.Binding with name="test-api", env=coretest.Env(),
	// cluster=coretest.Cluster(), and a db call — the standard fixture for this package.
	b := testBinding(nil)
	// Use "checkout" as the service name (per-brief intent) via an overridden binding.
	b.Name = "checkout"
	// Cluster workload placement uses "test-api"; keep that so the node/pod resolution holds.
	// The identity resolution falls back to name-derived pod ("checkout-0") when there is no
	// matching placement by name, which is acceptable for these OTLP-focused tests.
	w, err := build(cfg, b)
	if err != nil {
		t.Fatal(err)
	}
	cap := &captureOTLP{}
	world := coretest.World(&coretest.MetricCapture{}, nil, nil)
	world.OTLPMetrics = cap
	return w.(*Workload), world, cap
}

// TestOTLPResourceHostArchMatchesNode: enriched host.arch must come from the placed node's
// instance type (via fixture.LookupInstanceSpec().KubeArch()), matching the k8s node's own
// kubernetes.io/arch — NOT a hardcoded constant. m8g.xlarge is a catalogue Graviton (arm64) type.
func TestOTLPResourceHostArchMatchesNode(t *testing.T) {
	b := testBinding(nil) // name="test-api", placed on coretest.Cluster() nodes 0/1
	for i := range b.Cluster.Nodes {
		b.Cluster.Nodes[i].InstanceType = "m8g.xlarge"
	}
	cfg := NewConfig().(*Config)
	cfg.OTel = &OTelObs{Metrics: true, Mode: otelModeK8sMonitoring}
	w, err := build(cfg, b)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.(*Workload).otelResourceAttrs(otelModeK8sMonitoring)["host.arch"]; got != "arm64" {
		t.Errorf("host.arch = %v, want arm64 (placed node is m8g.xlarge)", got)
	}
}

// TestOTLPResourceHostArchDefaultsAmd64WhenUnplaced: with no matching cluster placement the
// node (hence its arch) cannot be resolved, so host.arch falls back to amd64.
func TestOTLPResourceHostArchDefaultsAmd64WhenUnplaced(t *testing.T) {
	b := testBinding(nil)
	b.Name = "unplaced-svc" // no placement by this name in the test cluster
	cfg := NewConfig().(*Config)
	cfg.OTel = &OTelObs{Metrics: true, Mode: otelModeK8sMonitoring}
	w, err := build(cfg, b)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.(*Workload).otelResourceAttrs(otelModeK8sMonitoring)["host.arch"]; got != "amd64" {
		t.Errorf("host.arch = %v, want amd64 fallback (no node placement)", got)
	}
}

func TestTickOTLPMetricsEmitsDurationAndActive(t *testing.T) {
	w, world, cap := newOTLPTestWorkload(t, otelModeNaked)
	routes := []routeStat{{route: "GET /v1/items", errorRate: 0.1}}
	if err := w.tickOTLPMetrics(context.Background(), time.Unix(1000, 0), world, 100, routes); err != nil {
		t.Fatal(err)
	}
	if len(cap.got) != 1 {
		t.Fatalf("got %d resources, want 1", len(cap.got))
	}
	res := cap.got[0]
	if res.Scope.Name != otelHTTPScopeName {
		t.Errorf("scope = %q, want %q", res.Scope.Name, otelHTTPScopeName)
	}
	if res.Attrs["service.name"] != "checkout" {
		t.Errorf("service.name = %v, want checkout", res.Attrs["service.name"])
	}
	// naked mode: no k8s.* resource attrs.
	if _, ok := res.Attrs["k8s.namespace.name"]; ok {
		t.Error("naked mode must not carry k8s.* attrs")
	}
	var dur, active *otlp.Metric
	for i := range res.Metrics {
		switch res.Metrics[i].Name {
		case otelDurationMetricName:
			dur = &res.Metrics[i]
		case otelActiveReqMetricName:
			active = &res.Metrics[i]
		}
	}
	if dur == nil || dur.Kind != otlp.MetricHistogram || len(dur.Histograms) == 0 {
		t.Fatalf("missing/invalid duration histogram: %+v", dur)
	}
	if dur.Unit != "s" {
		t.Errorf("duration unit = %q, want s", dur.Unit)
	}
	hp := dur.Histograms[0]
	if len(hp.BucketCounts) != len(hp.Bounds)+1 {
		t.Errorf("bucket/bounds mismatch: %d vs %d+1", len(hp.BucketCounts), len(hp.Bounds))
	}
	if hp.Start != w.otlpColdStart || hp.Start.IsZero() {
		t.Error("histogram must carry the stable cold-start time")
	}
	if active == nil || active.Kind != otlp.MetricSum || active.Monotonic {
		t.Fatalf("active_requests must be a non-monotonic Sum: %+v", active)
	}
	// Semconv: each active_requests datapoint must carry http.request.method + url.scheme.
	if len(active.Numbers) == 0 {
		t.Fatal("active_requests must have at least one NumberPoint")
	}
	for i, pt := range active.Numbers {
		if pt.Attrs["http.request.method"] == nil || pt.Attrs["http.request.method"] == "" {
			t.Errorf("active_requests point[%d]: missing http.request.method", i)
		}
		if pt.Attrs["url.scheme"] == nil || pt.Attrs["url.scheme"] == "" {
			t.Errorf("active_requests point[%d]: missing url.scheme", i)
		}
	}
	// The route is "GET /v1/items" → expect exactly one point attributed to GET.
	if len(active.Numbers) != 1 {
		t.Errorf("active_requests: want 1 point (one distinct method), got %d", len(active.Numbers))
	}
	if got := active.Numbers[0].Attrs["http.request.method"]; got != "GET" {
		t.Errorf("active_requests method = %v, want GET", got)
	}
	if got := active.Numbers[0].Attrs["url.scheme"]; got != "https" {
		t.Errorf("active_requests url.scheme = %v, want https", got)
	}
}

func TestNativeHTTPDurationConservesConfiguredTickVolume(t *testing.T) {
	for _, test := range []struct {
		name      string
		routes    int
		errorRate float64
	}{
		{"success_cohort_above_sample_budget", 1, 0},
		{"both_status_cohorts_above_sample_budget", 1, .5},
		{"route_and_status_remainders", 7, .1},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoints := make([]Endpoint, test.routes)
			for i := range endpoints {
				endpoints[i] = Endpoint{Route: fmt.Sprintf("GET /route/%d", i), ErrorRate: test.errorRate, P95ms: 120}
			}
			w, world, captured := nativeModelWorkload(t, "native-volume", 30, endpoints, "")
			now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
			// Fixed 30 requests/s over the public workload's minute metric interval:
			// this expectation is independent of the renderer's sampled observations.
			const callsPerTick = 30 * 60
			for tick := 1; tick <= 2; tick++ {
				if err := w.Tick(context.Background(), now.Add(time.Duration(tick-1)*time.Minute), world); err != nil {
					t.Fatal(err)
				}
				resource := captured.got[tick-1]
				metric := nativeDuration(t, resource)
				var count, errors uint64
				routeCounts := map[string]uint64{}
				for _, point := range metric.Histograms {
					count += point.Count
					routeCounts[point.Attrs["http.route"].(string)] += point.Count
					if point.Attrs["http.response.status_code"] == "500" {
						errors += point.Count
					}
					var buckets uint64
					for _, n := range point.BucketCounts {
						buckets += n
					}
					if buckets != point.Count || point.Sum <= 0 || point.Start != now {
						t.Errorf("invalid cumulative duration point: count=%d buckets=%d sum=%g start=%v", point.Count, buckets, point.Sum, point.Start)
					}
				}
				if count != uint64(tick*callsPerTick) {
					t.Errorf("tick %d native duration count=%d, want configured volume %d", tick, count, tick*callsPerTick)
				}
				for route, n := range routeCounts {
					if n < uint64(tick*(callsPerTick/test.routes)) || n > uint64(tick*((callsPerTick+test.routes-1)/test.routes)) {
						t.Errorf("route %s lost its share of configured volume: %d", route, n)
					}
				}
				if math.Abs(float64(errors)-float64(tick*callsPerTick)*test.errorRate) > float64(tick*test.routes) {
					t.Errorf("tick %d error count=%d does not preserve configured fraction %g of %d", tick, errors, test.errorRate, tick*callsPerTick)
				}
				if resource.Attrs["service.name"] != "native-volume" || metric.Unit != "s" || metric.Temporality != otlp.TemporalityCumulative {
					t.Errorf("native identity/unit/temporality changed: resource=%v metric=%+v", resource.Attrs, metric)
				}
			}
		})
	}
}

func nativeModelWorkload(t *testing.T, name string, rps float64, endpoints []Endpoint, seed string) (*Workload, *core.World, *captureOTLP) {
	t.Helper()
	cfg := NewConfig().(*Config)
	cfg.OTel = &OTelObs{Metrics: true, Mode: otelModeNaked}
	cfg.Traffic = Traffic{OffPeakRPS: rps, PeakRPS: rps}
	cfg.Endpoints = endpoints
	binding := testBinding(nil)
	binding.Name = name
	binding.Seed = seed
	w, err := build(cfg, binding)
	if err != nil {
		t.Fatal(err)
	}
	captured := &captureOTLP{}
	world := coretest.World(&coretest.MetricCapture{}, nil, nil)
	world.OTLPMetrics = captured
	return w.(*Workload), world, captured
}

func nativeDuration(t *testing.T, resource otlp.MetricResource) otlp.Metric {
	t.Helper()
	for _, metric := range resource.Metrics {
		if metric.Name == otelDurationMetricName {
			return metric
		}
	}
	t.Fatal("native HTTP emission omitted duration")
	return otlp.Metric{}
}

func TestNativeHTTPDurationUsesEndpointLatencyModel(t *testing.T) {
	for _, seed := range []string{"native-latency-a", "native-latency-b", "native-latency-c"} {
		t.Run(seed, func(t *testing.T) {
			endpoints := []Endpoint{
				{Route: "GET /fast", P95ms: 20},
				{Route: "GET /slow", P95ms: 800},
			}
			w, world, captured := nativeModelWorkload(t, "test-api", 30, endpoints, seed)
			if err := w.Tick(context.Background(), time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC), world); err != nil {
				t.Fatal(err)
			}
			metric := nativeDuration(t, captured.got[0])
			for _, point := range metric.Histograms {
				p95 := .020
				if point.Attrs["http.route"] == "/slow" {
					p95 = .800
				}
				// With sigma=.5, the source lognormal's mean is about half its
				// declared p95. This broad population band avoids pinning one RNG
				// draw while rejecting the unrelated fixed 20-200ms distribution.
				mean := point.Sum / float64(point.Count)
				if mean < .2*p95 || mean > p95 {
					t.Errorf("route %v native mean=%g seconds does not follow endpoint p95=%g seconds", point.Attrs["http.route"], mean, p95)
				}
			}
		})
	}
}

func TestNativeHTTPDurationSamplerIdentityAndIsolation(t *testing.T) {
	snapshot := func(seed string, sharedPrefix int) (string, string) {
		endpoints := []Endpoint{{Route: "GET /request", ErrorRate: .05, P95ms: 120}}
		w, world, captured := nativeModelWorkload(t, "test-api", 30, endpoints, seed)
		for range sharedPrefix {
			world.Shape.Float64()
		}
		if err := w.Tick(context.Background(), time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC), world); err != nil {
			t.Fatal(err)
		}
		metric := nativeDuration(t, captured.got[0])
		var points, shapes []string
		for _, point := range metric.Histograms {
			data, err := json.Marshal(point)
			if err != nil {
				t.Fatal(err)
			}
			points = append(points, string(data))
			shape, err := json.Marshal(struct {
				Attrs   map[string]any
				Count   uint64
				Bounds  []float64
				Buckets int
			}{point.Attrs, point.Count, point.Bounds, len(point.BucketCounts)})
			if err != nil {
				t.Fatal(err)
			}
			shapes = append(shapes, string(shape))
		}
		if len(points) == 0 {
			t.Fatal("public workload Tick emitted no native durations")
		}
		// CollectHistos iterates a map. JSON sorts attribute keys; sort complete
		// point rows as well so ordering cannot masquerade as a changed draw.
		sort.Strings(points)
		sort.Strings(shapes)
		metric.Histograms = nil
		header, err := json.Marshal(metric)
		if err != nil {
			t.Fatal(err)
		}
		return string(header) + "\n" + strings.Join(points, "\n"), strings.Join(shapes, "\n")
	}
	baseline, baselineShape := snapshot("native-stream-a", 0)
	sharedAdvanced, sharedAdvancedShape := snapshot("native-stream-a", 23)
	differentIdentity, differentIdentityShape := snapshot("native-stream-b", 0)
	if baseline != sharedAdvanced {
		t.Error("same binding identity changed native durations when only the shared RNG advanced")
	}
	if baseline == differentIdentity {
		t.Error("different binding seeds produced identical native duration draws")
	}
	if baselineShape != sharedAdvancedShape || baselineShape != differentIdentityShape {
		t.Error("RNG isolation or identity variation changed native duration counts, attributes or bucket shape")
	}
}

func TestNativeHTTPDurationScopedOnsetAndRecovery(t *testing.T) {
	endpoints := []Endpoint{{Route: "GET /request", P95ms: 120}}
	target, faultWorld, targetCapture := nativeModelWorkload(t, "native-target", 30, endpoints, "")
	control, controlWorld, controlCapture := nativeModelWorkload(t, "native-target", 30, endpoints, "")
	peer, peerWorld, peerCapture := nativeModelWorkload(t, "native-peer", 30, endpoints, "")
	controlPeer, controlPeerWorld, controlPeerCapture := nativeModelWorkload(t, "native-peer", 30, endpoints, "")
	// Model two workloads sharing one blueprint's RNG, as the real runner does.
	peerWorld.Shape = faultWorld.Shape
	controlPeerWorld.Shape = controlWorld.Shape
	enabled := false
	faultWorld.Shape.Live = func(mode string) []shape.LiveFailure {
		if mode != "latency_spike" {
			return nil
		}
		return []shape.LiveFailure{{Enabled: enabled, Intensity: 1, Scope: "native-target"}}
	}
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	var previousTarget, previousControl float64
	for phase, factor := range []float64{1, 4, 1} {
		enabled = phase == 1
		at := now.Add(time.Duration(phase) * time.Minute)
		for _, pair := range []struct {
			w     *Workload
			world *core.World
		}{{control, controlWorld}, {controlPeer, controlPeerWorld}, {target, faultWorld}, {peer, peerWorld}} {
			if err := pair.w.Tick(context.Background(), at, pair.world); err != nil {
				t.Fatal(err)
			}
		}
		got := nativeDuration(t, targetCapture.got[phase]).Histograms[0]
		baseline := nativeDuration(t, controlCapture.got[phase]).Histograms[0]
		increment, baselineIncrement := got.Sum-previousTarget, baseline.Sum-previousControl
		if math.Abs(increment-factor*baselineIncrement) > 1e-12*math.Max(1, factor*baselineIncrement) {
			t.Errorf("phase %d native duration increment=%g, want modeled factor %g * baseline %g", phase, increment, factor, baselineIncrement)
		}
		if got.Count != uint64((phase+1)*30*60) || got.Start != now {
			t.Errorf("phase %d duration lost cumulative counts/start: count=%d start=%v", phase, got.Count, got.Start)
		}
		if !reflect.DeepEqual(peerCapture.got[phase], controlPeerCapture.got[phase]) {
			t.Errorf("phase %d scoped latency incident changed the comparison workload or its shared-RNG position", phase)
		}
		previousTarget, previousControl = got.Sum, baseline.Sum
	}
}

func TestTickOTLPMetricsEnrichedAddsK8sAttrs(t *testing.T) {
	w, world, cap := newOTLPTestWorkload(t, otelModeK8sMonitoring)
	_ = w.tickOTLPMetrics(context.Background(), time.Unix(1000, 0), world, 10, []routeStat{{route: "GET /", errorRate: 0}})
	if len(cap.got) == 0 {
		t.Fatal("expected at least one MetricResource")
	}
	a := cap.got[0].Attrs
	for _, k := range []string{"k8s.namespace.name", "k8s.pod.name", "k8s.deployment.name", "host.name", "os.type"} {
		if _, ok := a[k]; !ok {
			t.Errorf("enriched mode missing %q", k)
		}
	}
}

// TestNativeOTLPGatewayPromotedResourceAttrs pins the exact resource-attribute subset the
// canonical dual-ingest capture measured as appearing on every native OTLP metric series. The
// gateway performs that promotion; synthkit sends these as resource attributes and keeps the
// converted promrw target_info path separate.
func TestNativeOTLPGatewayPromotedResourceAttrs(t *testing.T) {
	w, _, _ := newOTLPTestWorkload(t, otelModeK8sMonitoring)
	attrs := w.otelResourceAttrs(otelModeK8sMonitoring)
	want := []string{
		"k8s.deployment.name",
		"k8s.namespace.name",
		"k8s.pod.name",
		"service.name",
		"service.version",
	}
	for _, key := range want {
		if got, ok := attrs[key].(string); !ok || got == "" {
			t.Errorf("native OTLP resource attr %q = %v, want non-empty string", key, attrs[key])
		}
	}

	// These are resource attributes, not copied into promrw span-metric labels. The converted
	// path retains target_info for identity recovery, matching the capture's two-lane result.
	converted := w.spanMetricBase()
	for _, key := range []string{"k8s_pod_name", "k8s_deployment_name"} {
		if _, ok := converted[key]; ok {
			t.Errorf("converted promrw span metric unexpectedly carries %q", key)
		}
	}
	targetInfo := w.targetInfoLabels(false)
	for _, key := range []string{"service_name", "service_version", "k8s_namespace_name", "k8s_pod_name", "k8s_deployment_name"} {
		if targetInfo[key] == "" {
			t.Errorf("converted target_info omitted %q", key)
		}
	}
}

func TestObservedSpanResourceAttrsModelsMeasuredMinorityAndCollector(t *testing.T) {
	newWorkload := func(t *testing.T, name, clusterType, runtime string) *Workload {
		t.Helper()
		b := testBinding(nil)
		b.Name = name
		b.Seed = "observed-span-resource-policy"
		b.Cluster.Type = clusterType
		b.Cluster.Seed = "observed-span-resource-policy-cluster"
		b.Cluster.K8sMonitoring.Features = map[string]bool{"application_observability": true}
		b.Cluster.Workloads = []fixture.Workload{{Name: name, Runtime: runtime}}
		w, err := build(NewConfig(), b)
		if err != nil {
			t.Fatal(err)
		}
		return w.(*Workload)
	}

	disabled := newWorkload(t, "disabled-collector", "gke", "node")
	disabled.b.Cluster.K8sMonitoring.Features["application_observability"] = false
	if attrs := disabled.observedSpanResourceAttrs(); len(attrs) != 0 {
		t.Fatalf("disabled application-observability collector attrs=%v, want none", attrs)
	}

	cases := []struct {
		name, clusterType, runtime, wantProvider string
	}{
		{name: "eks-node", clusterType: "eks", runtime: "node"},
		{name: "aks-node", clusterType: "aks", runtime: "node", wantProvider: "azure"},
		{name: "gke-node", clusterType: "gke", runtime: "node", wantProvider: "gcp"},
		{name: "gke-go", clusterType: "gke", runtime: "go"},
		{name: "unknown-node", clusterType: "other", runtime: "node"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attrs := newWorkload(t, tc.name, tc.clusterType, tc.runtime).observedSpanResourceAttrs()
			got, _ := attrs["cloud.provider"].(string)
			if got != tc.wantProvider {
				t.Errorf("cloud.provider = %q, want %q", got, tc.wantProvider)
			}
			for _, key := range []string{"host.name", "host.arch", "os.type"} {
				if got, ok := attrs[key].(string); !ok || got == "" || got == "NA" {
					t.Errorf("collector resource attr %q = %v, want non-empty non-sentinel string", key, attrs[key])
				}
			}
		})
	}

	// Distinct applications in the same cluster share precisely one collector identity. That
	// rules out the former per-application host cardinality model and is deterministic.
	first := newWorkload(t, "first-service", "gke", "node").observedSpanResourceAttrs()
	second := newWorkload(t, "second-service", "gke", "node").observedSpanResourceAttrs()
	for _, key := range []string{"host.name", "host.arch", "os.type"} {
		if first[key] != second[key] {
			t.Errorf("collector %q differs by application: %v vs %v", key, first[key], second[key])
		}
	}
}

func TestTickOTLPMetricsNilWriterNoop(t *testing.T) {
	w, _, _ := newOTLPTestWorkload(t, otelModeNaked)
	// A World with no OTLPMetrics writer must be a no-op (no panic, no error).
	emptyWorld := coretest.World(&coretest.MetricCapture{}, nil, nil) // OTLPMetrics is nil
	if err := w.tickOTLPMetrics(context.Background(), time.Unix(1, 0), emptyWorld, 10, nil); err != nil {
		t.Fatal(err)
	}
}
