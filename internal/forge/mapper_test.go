// SPDX-License-Identifier: AGPL-3.0-only

package forge

import (
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

// TestMapSkeletonFlagsRecognisedPlatformProductsWithNoConstruct guards SKT-0012.04 finding 3:
// a platform product capture's addon detector never recognises at all (so it never appears in
// cl.Addons) must still surface explicitly as "detected, no construct" rather than disappearing
// silently into the generic workload-gap pile.
func TestMapSkeletonFlagsRecognisedPlatformProductsWithNoConstruct(t *testing.T) {
	reg := runner.Catalog()
	inv := &capture.Inventory{
		Clusters: []capture.Cluster{{
			Name: "platformprod",
			Workloads: []capture.Workload{
				{Name: "crossplane", Namespace: "crossplane-system", Replicas: 2},
				{Name: "provider-grafana-abc123", Namespace: "crossplane-system", Replicas: 1, Images: []string{"xpkg.upbound.io/grafana/provider-grafana@sha256:deadbeef"}},
			},
		}},
	}
	_, gaps := MapSkeleton(inv, reg)
	if !hasGap(gaps, "addon", "crossplane") {
		t.Fatalf("expected an explicit addon-category gap naming the recognised platform product, got: %+v", gaps)
	}
	for _, g := range gaps {
		if g.Category == "addon" && g.Name == "crossplane" {
			if g.Reason == "" || g.Reason == "no matching construct" {
				t.Fatalf("expected the reason to say this was recognised/detected, not the generic unmapped reason, got %q", g.Reason)
			}
			if len(g.Evidence) < 2 {
				t.Fatalf("expected evidence aggregating both matched workloads, got %+v", g.Evidence)
			}
		}
	}
}
