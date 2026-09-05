// SPDX-License-Identifier: AGPL-3.0-only

package forge

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/capture"
	"github.com/rknightion/synthkit/internal/runner"
	"gopkg.in/yaml.v3"
)

func sampleInventory() *capture.Inventory {
	return &capture.Inventory{
		Clusters: []capture.Cluster{{
			Name:       "shopprod",
			Provider:   "eks",
			Region:     "us-west-2",
			K8sVersion: "1.31",
			NodeGroups: []capture.NodeGroup{{Name: "general", InstanceType: "m6i.large", Count: 3, Provisioner: "managed", OS: "linux"}},
			Addons: []capture.Addon{
				{Kind: "karpenter", Detected: "karpenter", Evidence: "helm-annotation"},
				{Kind: "", Detected: "istiod", Evidence: "deployment"}, // unmodelled
			},
			Workloads: []capture.Workload{{Name: "checkout", Namespace: "shop", Kind: "Deployment", Replicas: 3, Images: []string{"shop/checkout:1.2"}}},
		}},
	}
}

func TestMapSkeletonClusterAndAddons(t *testing.T) {
	reg := runner.Catalog()
	sk, gaps := MapSkeleton(sampleInventory(), reg)

	if sk.Name != "shopprod-capture" {
		t.Fatalf("name: %q", sk.Name)
	}
	env := sk.Environments[0]
	if env.Cloud == nil || env.Cloud.Provider != "aws" || env.Cloud.Region != "us-west-2" {
		t.Fatalf("cloud: %+v", env.Cloud)
	}
	if env.Cloud.AccountID == "" || env.Cloud.VpcID == "" {
		t.Fatalf("required placeholders missing: %+v", env.Cloud)
	}
	if env.Cluster == nil || env.Cluster.Name != "shopprod" || len(env.Cluster.NodeGroups) != 1 {
		t.Fatalf("cluster: %+v", env.Cluster)
	}
	if env.Cluster.NodeGroups[0].InstanceType != "m6i.large" || env.Cluster.NodeGroups[0].Desired != 3 {
		t.Fatalf("node group: %+v", env.Cluster.NodeGroups[0])
	}
	if len(env.Cluster.Addons) != 1 || env.Cluster.Addons[0] != "karpenter" {
		t.Fatalf("addons: %+v", env.Cluster.Addons)
	}
	if !hasGap(gaps, "addon", "istiod") || !hasGap(gaps, "workload", "checkout") {
		t.Fatalf("missing expected gaps: %+v", gaps)
	}
}

// TestSkeletonLoads is the real acceptance gate (B3): the deterministic skeleton, rendered to YAML,
// MUST load through blueprint.Load with the real registry. Placeholders make cloud identity valid;
// workloads are gaps (not in the skeleton) so they don't gate the load.
func TestSkeletonLoads(t *testing.T) {
	reg := runner.Catalog()
	sk, _ := MapSkeleton(sampleInventory(), reg)
	y, err := yaml.Marshal(sk)
	if err != nil {
		t.Fatalf("marshal skeleton: %v", err)
	}
	if _, err := blueprint.Load(y, reg); err != nil {
		t.Fatalf("skeleton must load, got: %v\n---\n%s", err, y)
	}
}

func hasGap(gaps []Gap, cat, name string) bool {
	for _, g := range gaps {
		if g.Category == cat && g.Name == name {
			return true
		}
	}
	return false
}

// TestMapSkeletonDedupesAddonKind guards SKT-0012.04 finding 2: two workloads of the same product
// (different Detected names, same construct Kind, as capture's own addonKindTable can legitimately
// produce — e.g. "argo-cd" and "argocd" both resolving to "argocd") must not double-declare the
// addon in the skeleton. Duplicate declarations validate and load, so only a check catches them.
func TestMapSkeletonDedupesAddonKind(t *testing.T) {
	reg := runner.Catalog()
	inv := &capture.Inventory{
		Clusters: []capture.Cluster{{
			Name: "dupprod",
			Addons: []capture.Addon{
				{Kind: "argocd", Detected: "argo-cd", Evidence: "namespace"},
				{Kind: "argocd", Detected: "argocd", Evidence: "helm-annotation"},
			},
		}},
	}
	sk, _ := MapSkeleton(inv, reg)
	addons := sk.Environments[0].Cluster.Addons
	if len(addons) != 1 || addons[0] != "argocd" {
		t.Fatalf("expected exactly one deduplicated %q addon, got %+v", "argocd", addons)
	}
}

// TestMapSkeletonResolvesKnownUnmappedNames guards SKT-0012.04 finding 1: a detected name capture
// left unmapped (Kind == "") but that this build's catalog can actually model (via the forge-side
// supplemental table) must be mapped to its construct, not reported as a roadmap gap.
func TestMapSkeletonResolvesKnownUnmappedNames(t *testing.T) {
	reg := runner.Catalog()
	inv := &capture.Inventory{
		Clusters: []capture.Cluster{{
			Name: "alloyprod",
			Addons: []capture.Addon{
				{Kind: "", Detected: "grafana-k8s-monitoring-alloy-metrics", Evidence: "helm-annotation"},
				{Kind: "", Detected: "grafana-k8s-monitoring-alloy-daemon", Evidence: "helm-annotation"},
			},
		}},
	}
	sk, gaps := MapSkeleton(inv, reg)
	addons := sk.Environments[0].Cluster.Addons
	if len(addons) != 1 || addons[0] != "alloy_health" {
		t.Fatalf("expected the alloy-family names resolved and deduplicated to %q, got %+v", "alloy_health", addons)
	}
	if hasGap(gaps, "addon", "grafana-k8s-monitoring-alloy-metrics") || hasGap(gaps, "addon", "grafana-k8s-monitoring-alloy-daemon") {
		t.Fatalf("resolved names must not still be reported as gaps: %+v", gaps)
	}
}

// TestMapSkeletonClassifiesCapturedPlatformProducts guards SKT-0012.06: product discovery belongs
// to capture, while forge classifies the resulting addon entries against the registered catalog.
// OpenCost has a modeled surface on k8s_cluster, whereas the other named products remain genuine
// no-construct gaps until a standalone construct is registered.
func TestMapSkeletonClassifiesCapturedPlatformProducts(t *testing.T) {
	reg := runner.Catalog()
	inv := &capture.Inventory{
		Clusters: []capture.Cluster{{
			Name: "platformprod",
			Addons: []capture.Addon{
				{Detected: "crossplane", Evidence: "namespace"},
				{Detected: "external-secrets", Evidence: "deployment"},
				{Detected: "actions-runner-controller", Evidence: "namespace"},
				{Detected: "github2otel", Evidence: "deployment"},
				{Detected: "opencost", Evidence: "namespace"},
			},
		}},
	}
	sk, gaps := MapSkeleton(inv, reg)
	if len(sk.Environments[0].Cluster.Addons) != 0 {
		t.Fatalf("unmodelled platform products must not become addon declarations: %+v", sk.Environments[0].Cluster.Addons)
	}
	for _, product := range []string{"crossplane", "external-secrets", "actions-runner-controller", "github2otel"} {
		gap, ok := findGap(gaps, "addon", product)
		if !ok {
			t.Errorf("expected no-construct gap for %q, got: %+v", product, gaps)
			continue
		}
		if gap.Reason != reasonNoConstruct {
			t.Errorf("%q reason = %q, want %q", product, gap.Reason, reasonNoConstruct)
		}
	}
	opencost, ok := findGap(gaps, "addon", "opencost")
	if !ok {
		t.Fatal("expected OpenCost unmapped-name gap")
	}
	if !strings.Contains(opencost.Reason, "unmapped name") || !strings.Contains(opencost.Reason, "k8s_cluster") {
		t.Fatalf("OpenCost reason = %q, want explicit existing k8s_cluster surface", opencost.Reason)
	}
	if _, ok := reg.Construct("k8s_cluster"); !ok {
		t.Fatal("catalog must register k8s_cluster before OpenCost can be classified as unmapped")
	}
	if _, ok := reg.Construct("actions_runner_controller"); ok {
		t.Fatal("catalog unexpectedly contains a standalone actions-runner-controller construct")
	}
}

func TestMapSkeletonRetainsCrossplaneImageFallback(t *testing.T) {
	reg := runner.Catalog()
	inv := &capture.Inventory{Clusters: []capture.Cluster{{
		Name: "crossplane-image-only",
		Workloads: []capture.Workload{{
			Name:      "provider-grafana-generated",
			Namespace: "platform",
			Images:    []string{"xpkg.upbound.io/grafana/provider-grafana@sha256:placeholder"},
		}},
	}}}
	_, gaps := MapSkeleton(inv, reg)
	gap, ok := findGap(gaps, "addon", "crossplane")
	if !ok {
		t.Fatal("expected the forge Crossplane image fallback to retain the product gap")
	}
	if !strings.Contains(gap.Reason, "forge image fallback") || len(gap.Evidence) != 1 {
		t.Fatalf("Crossplane fallback gap = %+v, want one image evidence and fallback reason", gap)
	}
}

func TestCrossplaneImageFallbackRequiresKnownRegistryHost(t *testing.T) {
	tests := []struct {
		name  string
		image string
		want  bool
	}{
		{name: "crossplane registry", image: "crossplane.io/provider/grafana:v1", want: true},
		{name: "upbound registry", image: "upbound.io/provider/grafana:v1", want: true},
		{name: "crossplane subdomain", image: "XPKG.CROSSPLANE.IO/provider/grafana:v1", want: true},
		{name: "upbound subdomain with port", image: "xpkg.upbound.io:8443/provider/grafana:v1", want: true},
		{name: "crossplane registry with port", image: "crossplane.io:443/provider/grafana:v1", want: true},
		{name: "spoofed crossplane host", image: "evilcrossplane.io/provider/grafana:v1", want: false},
		{name: "spoofed upbound host", image: "evilupbound.io/provider/grafana:v1", want: false},
		{name: "path only", image: "docker.io/path/crossplane.io/provider:grafana", want: false},
		{name: "crossplane suffix", image: "crossplane.io.evil/provider/grafana:v1", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCrossplaneRegistryImage(tt.image); got != tt.want {
				t.Fatalf("isCrossplaneRegistryImage(%q) = %v, want %v", tt.image, got, tt.want)
			}
		})
	}
}

func findGap(gaps []Gap, category, name string) (Gap, bool) {
	for _, gap := range gaps {
		if gap.Category == category && gap.Name == name {
			return gap, true
		}
	}
	return Gap{}, false
}

func TestMapSkeletonSurfacesUndeterminedProvider(t *testing.T) {
	reg := runner.Catalog()
	inv := &capture.Inventory{Clusters: []capture.Cluster{{
		Name:     "unknown-provider",
		Provider: capture.ProviderUndetermined,
	}}}
	_, gaps := MapSkeleton(inv, reg)
	gap, ok := findGap(gaps, "addon", "undetermined-provider")
	if !ok {
		t.Fatalf("expected an explicit undetermined-provider gap, got %+v", gaps)
	}
	if !strings.Contains(gap.Reason, "provider undetermined") {
		t.Fatalf("provider gap reason = %q, want explicit undetermined evidence", gap.Reason)
	}
}
