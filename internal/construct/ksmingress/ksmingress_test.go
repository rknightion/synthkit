// SPDX-License-Identifier: AGPL-3.0-only

package ksmingress_test

// ksmingress_test.go — construct invariant tests for the ksm_ingress construct.
//
// Test inventory:
//   (a) Exact series name inventory — all expected names present, no extras.
//   (b) Every series carries `cluster` label (I16 mandatory injection).
//   (c) Default ingress derivation from Cluster.Workloads when config is empty.
//   (d) Explicit config ingresses override defaults.
//   (e) kube_ingress_tls emitted only when tls=true; omitted when tls=false (I13).
//   (f) Build error on nil Cluster.
//   (g) Kind / Signals / Interval metadata.
//   (h) Scope is ScopeSubstrate (registration check).

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/ksmingress"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func tickOnce(t *testing.T, c core.Construct) *coretest.MetricCapture {
	t.Helper()
	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return cap
}

func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	c, err := ksmingress.New(&ksmingress.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("ksmingress.New: %v", err)
	}
	return c
}

// ── (a) Exact series inventory ────────────────────────────────────────────────

// expectedCoreNames lists the series names that must be present regardless of TLS.
var expectedCoreNames = func() []string {
	names := []string{
		"kube_ingress_info",
		"kube_ingress_path",
		"kube_ingress_created",
		"kube_ingress_labels",
		"kube_ingress_annotations",
		"kube_ingress_metadata_resource_version",
	}
	sort.Strings(names)
	return names
}()

func TestSeriesInventory(t *testing.T) {
	c := buildDefault(t)
	cap := tickOnce(t, c)
	names := cap.Names()

	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}

	for _, n := range expectedCoreNames {
		if !got[n] {
			t.Errorf("MISSING series: %s", n)
		}
	}

	// kube_ingress_tls should be absent for the default cluster (no TLS configured).
	if got["kube_ingress_tls"] {
		t.Errorf("kube_ingress_tls present but no TLS configured in default cluster workloads")
	}

	// No unexpected series.
	allowed := map[string]bool{
		"kube_ingress_info":                      true,
		"kube_ingress_path":                      true,
		"kube_ingress_tls":                       true, // allowed when TLS present
		"kube_ingress_created":                   true,
		"kube_ingress_labels":                    true,
		"kube_ingress_annotations":               true,
		"kube_ingress_metadata_resource_version": true,
	}
	for n := range got {
		if !allowed[n] {
			t.Errorf("UNEXPECTED series: %s", n)
		}
	}
}

// ── (b) Every series carries `cluster` label (I16) ────────────────────────────

// TestClusterLabelOnAllSeries asserts that cluster (and k8s_cluster_name) is present on
// every emitted series. This is the load-bearing I16 invariant: KSM kube_ingress_* by
// default only carries {namespace, ingress} — our construct injects cluster to prevent
// series collisions across multi-cluster deployments.
func TestClusterLabelOnAllSeries(t *testing.T) {
	cl := coretest.Cluster()
	c, err := ksmingress.New(&ksmingress.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: cl,
	})
	if err != nil {
		t.Fatalf("ksmingress.New: %v", err)
	}

	cap := tickOnce(t, c)
	all := cap.All()

	if len(all) == 0 {
		t.Fatal("no series emitted")
	}

	for _, s := range all {
		if s.Labels["cluster"] == "" {
			t.Errorf("series %q missing `cluster` label (I16 violation): labels=%v", s.Name, s.Labels)
		}
		if s.Labels["k8s_cluster_name"] == "" {
			t.Errorf("series %q missing `k8s_cluster_name` label: labels=%v", s.Name, s.Labels)
		}
		if s.Labels["cluster"] != cl.Name {
			t.Errorf("series %q cluster=%q, want %q", s.Name, s.Labels["cluster"], cl.Name)
		}
	}
}

// ── (c) Default ingress derivation from Cluster.Workloads ─────────────────────

// TestDefaultIngressDerivation verifies that when Config.Ingresses is empty, one
// kube_ingress_info series is emitted per Cluster.Workload entry with name/namespace
// matching the workload.
func TestDefaultIngressDerivation(t *testing.T) {
	cl := coretest.Cluster()
	c, err := ksmingress.New(&ksmingress.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: cl,
	})
	if err != nil {
		t.Fatalf("ksmingress.New: %v", err)
	}

	cap := tickOnce(t, c)

	// Build map: ingress name → namespace from emitted kube_ingress_info.
	infoSeries := cap.Find("kube_ingress_info")
	emittedIngresses := map[string]string{} // name → namespace
	for _, s := range infoSeries {
		emittedIngresses[s.Labels["ingress"]] = s.Labels["namespace"]
	}

	// Every workload must have a corresponding ingress.
	if len(cl.Workloads) == 0 {
		t.Fatal("test cluster has no workloads — cannot validate default derivation")
	}
	for i, wl := range cl.Workloads {
		ns, ok := emittedIngresses[wl.Name]
		if !ok {
			t.Errorf("workload[%d] name=%q: no kube_ingress_info series derived", i, wl.Name)
			continue
		}
		if ns != wl.Namespace {
			t.Errorf("workload[%d] name=%q: ingress namespace=%q, want %q", i, wl.Name, ns, wl.Namespace)
		}
	}

	// Count must equal workload count.
	if len(emittedIngresses) != len(cl.Workloads) {
		t.Errorf("emitted ingress count %d != workload count %d", len(emittedIngresses), len(cl.Workloads))
	}
}

// ── (d) Explicit config ingresses ─────────────────────────────────────────────

// TestExplicitIngressConfig verifies that explicit Config.Ingresses override default
// derivation: only the configured ingresses are emitted.
func TestExplicitIngressConfig(t *testing.T) {
	cl := coretest.Cluster()
	cfg := &ksmingress.Config{
		Ingresses: []ksmingress.IngressConfig{
			{Name: "my-api", Namespace: "api", Host: "api.example.com",
				Path: "/v1", ServiceName: "my-api-svc", ServicePort: 8080},
			{Name: "my-admin", ServiceName: "my-admin-svc"},
		},
	}
	c, err := ksmingress.New(cfg, &fixture.Set{Seed: "test", Cluster: cl})
	if err != nil {
		t.Fatalf("ksmingress.New: %v", err)
	}

	cap := tickOnce(t, c)
	infoSeries := cap.Find("kube_ingress_info")

	names := map[string]bool{}
	for _, s := range infoSeries {
		names[s.Labels["ingress"]] = true
	}

	if !names["my-api"] {
		t.Error("kube_ingress_info missing for configured ingress 'my-api'")
	}
	if !names["my-admin"] {
		t.Error("kube_ingress_info missing for configured ingress 'my-admin'")
	}
	// Default-derived workload ingresses must NOT be present.
	for _, wl := range cl.Workloads {
		if names[wl.Name] {
			t.Errorf("kube_ingress_info emitted for workload %q but explicit config should override defaults", wl.Name)
		}
	}
}

// ── (e) TLS conditional emission ─────────────────────────────────────────────

// TestTLSConditionalEmission verifies:
//   - kube_ingress_tls is emitted when tls=true.
//   - kube_ingress_tls is absent when tls=false (I13: absent dimension omitted, not "").
func TestTLSConditionalEmission(t *testing.T) {
	cl := coretest.Cluster()

	// Build with TLS-enabled ingress.
	cfgTLS := &ksmingress.Config{
		Ingresses: []ksmingress.IngressConfig{
			{Name: "tls-ing", ServiceName: "tls-svc", TLS: true},
		},
	}
	cTLS, err := ksmingress.New(cfgTLS, &fixture.Set{Seed: "test", Cluster: cl})
	if err != nil {
		t.Fatalf("ksmingress.New (TLS): %v", err)
	}
	capTLS := tickOnce(t, cTLS)
	if len(capTLS.Find("kube_ingress_tls")) == 0 {
		t.Error("kube_ingress_tls must be emitted when tls=true")
	}

	// Build with TLS-disabled ingress.
	cfgNoTLS := &ksmingress.Config{
		Ingresses: []ksmingress.IngressConfig{
			{Name: "plain-ing", ServiceName: "plain-svc", TLS: false},
		},
	}
	cNoTLS, err := ksmingress.New(cfgNoTLS, &fixture.Set{Seed: "test", Cluster: cl})
	if err != nil {
		t.Fatalf("ksmingress.New (noTLS): %v", err)
	}
	capNoTLS := tickOnce(t, cNoTLS)
	if len(capNoTLS.Find("kube_ingress_tls")) > 0 {
		t.Error("kube_ingress_tls must NOT be emitted when tls=false (I13)")
	}
}

// ── (f) Build error on nil Cluster ───────────────────────────────────────────

func TestBuildErrorOnNilCluster(t *testing.T) {
	_, err := ksmingress.New(&ksmingress.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: nil,
	})
	if err == nil {
		t.Fatal("expected error when Cluster is nil, got nil")
	}
}

// ── (g) Kind / Signals / Interval metadata ───────────────────────────────────

func TestMetadata(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "ksm_ingress" {
		t.Errorf("Kind() = %q, want %q", c.Kind(), "ksm_ingress")
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals() = %v, want [Metrics]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval() = %v, want 60s", c.Interval())
	}
}

// ── (h) Scope is ScopeSubstrate ──────────────────────────────────────────────

func TestRegistrationScope(t *testing.T) {
	reg := ksmingress.Registration()
	if reg.Scope != core.ScopeSubstrate {
		t.Errorf("Registration().Scope = %v, want ScopeSubstrate", reg.Scope)
	}
	if reg.Kind != "ksm_ingress" {
		t.Errorf("Registration().Kind = %q, want %q", reg.Kind, "ksm_ingress")
	}
}
