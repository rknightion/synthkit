// SPDX-License-Identifier: AGPL-3.0-only

// Package forge is composition-layer tooling that converts a captured capture.Inventory
// into a blueprint skeleton, an LLM prompt, and a coverage report. It MAY import
// blueprint/core/runner/capture; it is NOT a construct and MUST NOT be added to any
// construct/workload package.
package forge

import (
	"fmt"
	"strings"

	"github.com/rknightion/synthkit/internal/capture"
	"github.com/rknightion/synthkit/internal/core"
)

// Skeleton is a marshal-clean SUBSET of a blueprint, used only to render the prompt.
// blueprint.Decl is decode-only (yaml.Node fields, unmarshalers without marshalers,
// untagged fields) so marshalling it produces YAML the loader rejects (B1). Skeleton
// has explicit yaml tags + omitempty and contains only what the deterministic mapper
// can fill confidently; the model completes the rest.
type Skeleton struct {
	Name         string    `yaml:"name"`
	Environments []SkelEnv `yaml:"environments"`
}

// SkelEnv is one environment entry inside a Skeleton.
type SkelEnv struct {
	Name    string       `yaml:"name"`
	Cloud   *SkelCloud   `yaml:"cloud,omitempty"`
	Cluster *SkelCluster `yaml:"cluster,omitempty"`
}

// SkelCloud carries the cloud-account identity. AccountID and VpcID are always
// populated with placeholder values so blueprint.Load never rejects on missing fields.
type SkelCloud struct {
	Provider  string `yaml:"provider"`
	AccountID string `yaml:"account_id"`
	Region    string `yaml:"region"`
	VpcID     string `yaml:"vpc_id"`
}

// SkelCluster is the kubernetes cluster block.
type SkelCluster struct {
	Type          string          `yaml:"type"`
	Name          string          `yaml:"name"`
	NodeGroups    []SkelNodeGroup `yaml:"node_groups"`
	K8sMonitoring *SkelK8sMon     `yaml:"k8s_monitoring,omitempty"`
	Platform      *SkelPlatform   `yaml:"platform,omitempty"`
	Addons        []string        `yaml:"addons,omitempty"` // scalar form (AddonRef accepts it)
}

// SkelNodeGroup is one node group inside SkelCluster.
type SkelNodeGroup struct {
	Name         string `yaml:"name"`
	InstanceType string `yaml:"instance_type"`
	Desired      int    `yaml:"desired"`
	Provisioner  string `yaml:"provisioner,omitempty"`
	OS           string `yaml:"os,omitempty"`
}

// SkelK8sMon is the k8s_monitoring block; only emitted when the capture says monitoring is present.
type SkelK8sMon struct {
	Enabled      bool   `yaml:"enabled"`
	Alloy        bool   `yaml:"alloy,omitempty"`
	AlloyVersion string `yaml:"alloy_version,omitempty"`
	ChartVersion string `yaml:"chart_version,omitempty"`
}

// SkelPlatform carries the optional kubernetes_version. Omitted when capture K8sVersion is empty.
type SkelPlatform struct {
	KubernetesVersion string `yaml:"kubernetes_version,omitempty"`
}

// Gap records something the deterministic mapper could not resolve confidently.
// Category ∈ "workload" | "addon" | "service".
type Gap struct {
	Category string
	Name     string
	Evidence []string
	Reason   string
}

// MapSkeleton deterministically maps a capture.Inventory to a marshal-clean Skeleton
// and a list of Gaps for things the LLM must fill. Pure function — no I/O.
//
// Key synthesis rules (B2):
//   - AccountID is always "000000000000" (placeholder — capture cannot know it; keeps blueprint.Load happy).
//   - VpcID is always "vpc-PLACEHOLDER" (same reason).
//   - Provider is always "aws"; non-eks and undetermined providers still emit Provider:"aws"
//     plus an explicit Gap so the AWS-only placeholder cannot be mistaken for detection.
//   - Cluster.Type is always "eks" (v1 only supports EKS).
//   - NodeGroup.Desired = max(count, 1) so the blueprint loads without a zero-desired group.
func MapSkeleton(inv *capture.Inventory, reg *core.Registry) (*Skeleton, []Gap) {
	var gaps []Gap

	sk := &Skeleton{}

	for _, cl := range inv.Clusters {
		// Blueprint name
		if sk.Name == "" {
			sk.Name = cl.Name + "-capture"
		}

		// Cloud block
		cloud := &SkelCloud{
			Provider:  "aws",
			AccountID: "000000000000",
			VpcID:     "vpc-PLACEHOLDER",
			Region:    cl.Region,
		}
		// The v1 skeleton is AWS-only, so retain its loadable AWS placeholder while making every
		// non-EKS or undetermined capture result explicit. An empty provider is treated as
		// undetermined for inventories assembled by older callers that predate the capture value.
		provider := cl.Provider
		if provider == "" {
			provider = capture.ProviderUndetermined
		}
		if provider != "eks" {
			reason := fmt.Sprintf("provider %q is unsupported by the v1 AWS-only skeleton; set a plausible AWS account/region or extend the catalog", provider)
			if provider == capture.ProviderUndetermined {
				reason = "provider undetermined; no supported provider label family was captured, so the AWS skeleton is only a placeholder"
			}
			gaps = append(gaps, Gap{
				Category: "addon",
				Name:     provider + "-provider",
				Evidence: []string{"provider=" + provider},
				Reason:   reason,
			})
		}

		// Node groups
		var ngs []SkelNodeGroup
		for _, ng := range cl.NodeGroups {
			desired := max(ng.Count, 1)
			ngs = append(ngs, SkelNodeGroup{
				Name:         ng.Name,
				InstanceType: ng.InstanceType,
				Desired:      desired,
				Provisioner:  ng.Provisioner,
				OS:           ng.OS,
			})
		}

		// k8s_monitoring — only set when the capture says it is present
		var k8sMon *SkelK8sMon
		if cl.Monitoring.K8sMonitoring {
			k8sMon = &SkelK8sMon{
				Enabled:      true,
				Alloy:        cl.Monitoring.Alloy,
				AlloyVersion: cl.Monitoring.AlloyVersion,
				ChartVersion: cl.Monitoring.ChartVersion,
			}
		}

		// Platform — only when K8sVersion is present
		var platform *SkelPlatform
		if cl.K8sVersion != "" {
			platform = &SkelPlatform{KubernetesVersion: cl.K8sVersion}
		}

		// Addons: registered kinds → scalar addon list; unknown → Gap.
		// seenKinds dedupes by construct KIND, not by Detected name: capture's own addonKindTable
		// can legitimately map two different detected names (e.g. "argo-cd" and "argocd") to the
		// same kind, and two workloads of one product must still declare that addon exactly once
		// (SKT-0012.04 finding 2 — a duplicate validates and loads, so only a check catches it).
		var addons []string
		seenKinds := map[string]bool{}
		detectedAddons := map[string]bool{}
		for _, a := range cl.Addons {
			detectedAddons[a.Detected] = true
			kind := a.Kind
			if kind == "" {
				// OpenCost is a detected product, but its modeled cost surface is an option on the
				// existing k8s_cluster substrate rather than a standalone addon declaration. Keep
				// that name out of the skeleton and report it as an unmapped name only when the
				// registered catalog confirms the substrate exists.
				if surface := addonSurfaceConstruct(a.Detected, reg); surface != "" {
					gaps = append(gaps, Gap{
						Category: "addon",
						Name:     a.Detected,
						Evidence: []string{a.Evidence},
						Reason:   fmt.Sprintf("unmapped name: surface is modeled by construct kind %q", surface),
					})
					continue
				}
				// Capture left this unmapped. Before treating it as a genuine roadmap gap, check
				// the forge-side supplemental table for names this build's catalog can already
				// model but capture's own name table doesn't yet know about (SKT-0012.04 finding
				// 1 — this is what stops a fixable mapping bug reading as missing-construct work).
				kind = resolveAddonKind(a.Detected)
			}
			if kind == "" {
				gaps = append(gaps, Gap{
					Category: "addon",
					Name:     a.Detected,
					Evidence: []string{a.Evidence},
					Reason:   reasonNoConstruct,
				})
				continue
			}
			if _, ok := reg.Construct(kind); !ok {
				gaps = append(gaps, Gap{
					Category: "addon",
					Name:     a.Detected,
					Evidence: []string{a.Evidence},
					Reason:   fmt.Sprintf("construct kind %q not registered", kind),
				})
				continue
			}
			if seenKinds[kind] {
				continue
			}
			seenKinds[kind] = true
			addons = append(addons, kind)
		}
		// Capture owns product discovery. Keep the former image-based Crossplane check only as a
		// narrow fallback for provider workloads whose name and namespace do not match capture's
		// table; do not duplicate a product that capture already detected.
		gaps = append(gaps, detectCrossplaneImageFallback(cl.Workloads, detectedAddons)...)

		// Workloads → Gaps (the model classifies them; mapper records evidence)
		for _, w := range cl.Workloads {
			ev := []string{
				fmt.Sprintf("namespace=%s", w.Namespace),
				fmt.Sprintf("replicas=%d", w.Replicas),
				fmt.Sprintf("runs_on=%s", cl.Name),
			}
			for _, img := range w.Images {
				ev = append(ev, fmt.Sprintf("image=%s", img))
			}
			if len(w.Ports) > 0 {
				ev = append(ev, fmt.Sprintf("ports=%v", w.Ports))
			}
			if len(w.ProbePaths) > 0 {
				ev = append(ev, fmt.Sprintf("probes=%v", w.ProbePaths))
			}
			gaps = append(gaps, Gap{
				Category: "workload",
				Name:     w.Name,
				Evidence: ev,
			})
		}

		// ExternalName services → Gaps
		for _, svc := range cl.Services {
			if svc.Type == "ExternalName" {
				gaps = append(gaps, Gap{
					Category: "service",
					Name:     svc.Name,
					Evidence: []string{fmt.Sprintf("external=%s", svc.ExternalName)},
					Reason:   "external dependency — candidate db/cache call target",
				})
			}
		}

		cluster := &SkelCluster{
			Type:          "eks",
			Name:          cl.Name,
			NodeGroups:    ngs,
			K8sMonitoring: k8sMon,
			Platform:      platform,
			Addons:        addons,
		}

		env := SkelEnv{
			Name:    "prod",
			Cloud:   cloud,
			Cluster: cluster,
		}
		sk.Environments = append(sk.Environments, env)
	}

	// Ensure a non-empty name even for an empty inventory
	if sk.Name == "" {
		sk.Name = "capture"
	}

	return sk, gaps
}

// reasonNoConstruct is the Gap.Reason for a detected name this build's catalog genuinely has no
// construct for. It is compared against verbatim by CoverageReport to route a gap into the
// roadmap section — keep it exact wherever it is set.
const reasonNoConstruct = "no matching construct"

// addonNamePattern is one forge-side, name-prefix supplemental mapping from a detected addon
// name capture's own addonKindTable does not (yet) recognise, to a construct kind this build's
// catalog already ships. Add here — never in reasonNoConstruct's caller directly — only when the
// construct genuinely exists (verify against the live registry/catalog first): a name with no
// real construct stays unmapped and surfaces honestly as a roadmap gap. A genuinely new
// detection name belongs in capture's addonKindTable instead; this table exists because forge
// cannot edit that file across the capture/forge trust boundary, only report on it.
var addonNamePatterns = []struct {
	Prefix string
	Kind   string
}{
	// The k8s-monitoring Helm chart splits Alloy into 4 per-purpose Deployments/DaemonSets
	// (daemon/metrics/receiver/singleton), none of which capture's addonKindTable recognises by
	// name. All four are the same Alloy self-telemetry surface the alloy_health construct models
	// (confirmed live against the EKS lab cluster, SKT-0012 validation run 2026-08-27).
	{Prefix: "grafana-k8s-monitoring-alloy-", Kind: "alloy_health"},
}

// resolveAddonKind checks the supplemental table for a detected name capture left unmapped.
// Returns "" when nothing matches — the caller then reports it as a genuine roadmap gap.
func resolveAddonKind(detected string) string {
	for _, p := range addonNamePatterns {
		if strings.HasPrefix(detected, p.Prefix) {
			return p.Kind
		}
	}
	return ""
}

// addonSurfaceConstruct maps a detected product name to the registered construct that models
// that product's signal surface when the name is not itself a standalone addon kind. It is a
// classification table, not a detector: capture owns discovering the name. OpenCost's metrics are
// emitted by the k8s_cluster substrate's opencost switch. The runner-controller candidate is kept
// here only so the catalog check below proves that its no-construct verdict is still warranted.
var addonSurfaceConstructs = map[string]string{
	"opencost":                  "k8s_cluster",
	"actions-runner-controller": "actions_runner_controller",
}

func addonSurfaceConstruct(detected string, reg *core.Registry) string {
	surface := addonSurfaceConstructs[detected]
	if surface == "" || reg == nil {
		return ""
	}
	if _, ok := reg.Construct(surface); !ok {
		return ""
	}
	return surface
}

func detectCrossplaneImageFallback(workloads []capture.Workload, detected map[string]bool) []Gap {
	if detected["crossplane"] {
		return nil
	}
	var evidence []string
	for _, w := range workloads {
		for _, image := range w.Images {
			if isCrossplaneRegistryImage(image) {
				evidence = append(evidence, fmt.Sprintf("%s image=%s", w.Name, image))
				break
			}
		}
	}
	if len(evidence) == 0 {
		return nil
	}
	return []Gap{{
		Category: "addon",
		Name:     "crossplane",
		Evidence: evidence,
		Reason:   "no matching construct (forge image fallback; capture did not recognize a Crossplane name or namespace)",
	}}
}

func isCrossplaneRegistryImage(image string) bool {
	registry, _, ok := strings.Cut(image, "/")
	if !ok {
		return false
	}
	registry = strings.ToLower(registry)
	if host, port, hasPort := strings.Cut(registry, ":"); hasPort {
		if port == "" {
			return false
		}
		for _, r := range port {
			if r < '0' || r > '9' {
				return false
			}
		}
		registry = host
	}

	return registry == "crossplane.io" ||
		registry == "upbound.io" ||
		strings.HasSuffix(registry, ".crossplane.io") ||
		strings.HasSuffix(registry, ".upbound.io")
}
