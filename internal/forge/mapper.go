// SPDX-License-Identifier: AGPL-3.0-only

// Package forge is composition-layer tooling that converts a captured capture.Inventory
// into a blueprint skeleton, an LLM prompt, and a coverage report. It MAY import
// blueprint/core/runner/capture; it is NOT a construct and MUST NOT be added to any
// construct/workload package.
package forge

import (
	"fmt"
	"sort"
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
//   - Provider is always "aws"; non-eks providers still emit Provider:"aws" plus a Gap.
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
		// Non-eks providers: still model as aws (v1 limitation), but add a gap so the SE notices.
		if cl.Provider != "" && cl.Provider != "eks" {
			gaps = append(gaps, Gap{
				Category: "addon",
				Name:     cl.Provider + "-provider",
				Reason:   "v1 models AWS only; set a plausible AWS account/region or extend the catalog",
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
		for _, a := range cl.Addons {
			kind := a.Kind
			if kind == "" {
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

		// Platform products capture's own addon detector never recognises at all (so they never
		// appear in cl.Addons with any Kind, mapped or not) still deserve an explicit, honest line
		// rather than disappearing into the generic workload-gap pile (SKT-0012.04 finding 3).
		gaps = append(gaps, detectUnmodelledPlatformProducts(cl.Workloads)...)

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

// platformProductSignature recognises a well-known platform/operator product by its workload
// name or container image, for products this build has NO construct for at all (verified against
// the live catalog). Capture's own addon detector does not recognise these names or namespaces
// (SKT-0012.04 finding 3) — matching them here does not fix that detection gap, it only stops the
// coverage report going silent about a product that IS present in the cluster.
type platformProductSignature struct {
	Product         string
	NameSubstrings  []string
	ImageSubstrings []string
}

var unmodelledPlatformProducts = []platformProductSignature{
	{Product: "crossplane", NameSubstrings: []string{"crossplane"}, ImageSubstrings: []string{"crossplane.io", "upbound.io"}},
	{Product: "external-secrets", NameSubstrings: []string{"external-secrets"}},
	{Product: "actions-runner-controller", NameSubstrings: []string{"gha-rs-controller", "gha-runner-scale-set"}},
	{Product: "github2otel", NameSubstrings: []string{"github2otel"}},
	{Product: "opencost", NameSubstrings: []string{"opencost"}},
}

// detectUnmodelledPlatformProducts scans workloads for known platform-product signatures and
// returns one aggregated addon-category Gap per product actually seen, so the coverage report can
// state plainly that it was detected and deliberately left unmodelled.
func detectUnmodelledPlatformProducts(workloads []capture.Workload) []Gap {
	evidenceByProduct := map[string][]string{}
	for _, w := range workloads {
		for _, sig := range unmodelledPlatformProducts {
			if matchesPlatformProduct(w, sig) {
				evidenceByProduct[sig.Product] = append(evidenceByProduct[sig.Product], w.Name)
				break // one product match per workload is enough
			}
		}
	}
	var gaps []Gap
	for _, product := range sortedStringKeys(evidenceByProduct) {
		gaps = append(gaps, Gap{
			Category: "addon",
			Name:     product,
			Evidence: evidenceByProduct[product],
			Reason:   "no matching construct (recognised platform product, not surfaced by addon detection)",
		})
	}
	return gaps
}

func matchesPlatformProduct(w capture.Workload, sig platformProductSignature) bool {
	for _, sub := range sig.NameSubstrings {
		if strings.Contains(w.Name, sub) {
			return true
		}
	}
	for _, img := range w.Images {
		for _, sub := range sig.ImageSubstrings {
			if strings.Contains(img, sub) {
				return true
			}
		}
	}
	return false
}

func sortedStringKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
