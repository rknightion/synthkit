// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"strings"

	"github.com/rknightion/synthkit/internal/fixture"
)

// observedSpanResourceAttrs returns only the collector-processed attributes measured on
// application span resources in the multi-cloud capture. The trace builder owns the base
// resource and calls this helper as an additive policy seam.
//
// The capture establishes two non-uniform rules:
//   - cloud.provider is application-SDK detection, not a substrate scope key. Only Node.js
//     services carried it on AKS/GKE; no EKS span did because the application SDK could not
//     complete its IMDSv2 token exchange.
//   - host.* and os.type identify the Alloy receiver, whose resourcedetection processor
//     overrides application identity. They are stable per collector/cluster, never per app.
//
// The policy deliberately models only the observed cloud.provider key. The capture did not
// establish a uniform cloud-resource envelope, so adding cloud.region, cloud.account.id, or
// another detected key here would fabricate a universal contract.
func (w *Workload) observedSpanResourceAttrs() map[string]any {
	if w.b.Cluster == nil || !w.b.Cluster.K8sMonitoring.Features["application_observability"] {
		return nil
	}

	attrs := w.collectorResourceAttrs()
	if provider := w.detectedSpanCloudProvider(); provider != "" {
		attrs["cloud.provider"] = provider
	}
	return attrs
}

// detectedSpanCloudProvider mirrors the measured minority/asymmetric application SDK
// detection rule. Runtime is a pod property from the resolved fixture, so it is not guessed
// from a workload name. Unknown substrates and runtimes intentionally omit the attribute.
func (w *Workload) detectedSpanCloudProvider() string {
	own := w.ownPlacement()
	if own == nil || own.Runtime != "node" || w.b.Cluster == nil {
		return ""
	}

	switch strings.ToLower(w.b.Cluster.Type) {
	case "aks":
		return "azure"
	case "gke":
		return "gcp"
	default:
		// EKS and all unknown substrate types are absent rather than sentinelled (I13).
		return ""
	}
}

// collectorResourceAttrs models the k8s-monitoring application's receiver, not an
// application host. Its deterministic synthetic identity is keyed only by the cluster seed;
// consequently every application in one cluster shares it and it contributes zero
// per-application cardinality. Values are synthetic rather than copied from a capture.
func (w *Workload) collectorResourceAttrs() map[string]any {
	cluster := w.b.Cluster
	if cluster == nil {
		return nil
	}
	seed := cluster.Seed
	if seed == "" {
		seed = w.b.Seed
	}
	if seed == "" {
		seed = cluster.Name
	}

	arch := "amd64"
	if len(cluster.Nodes) > 0 {
		if detected := fixture.LookupInstanceSpec(cluster.Nodes[0].InstanceType).KubeArch(); detected != "" {
			arch = detected
		}
	}

	return map[string]any{
		"host.name": "alloy-receiver-" + fixture.HexID(seed, 10, "collector", "deployment") + "-" + fixture.HexID(seed, 5, "collector", "pod"),
		"host.arch": arch,
		"os.type":   "linux",
	}
}
