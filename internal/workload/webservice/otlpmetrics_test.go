// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
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

func TestTickOTLPMetricsNilWriterNoop(t *testing.T) {
	w, _, _ := newOTLPTestWorkload(t, otelModeNaked)
	// A World with no OTLPMetrics writer must be a no-op (no panic, no error).
	emptyWorld := coretest.World(&coretest.MetricCapture{}, nil, nil) // OTLPMetrics is nil
	if err := w.tickOTLPMetrics(context.Background(), time.Unix(1, 0), emptyWorld, 10, nil); err != nil {
		t.Fatal(err)
	}
}
