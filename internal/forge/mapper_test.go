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
