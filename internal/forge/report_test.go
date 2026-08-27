// SPDX-License-Identifier: AGPL-3.0-only

package forge

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/runner"
)

// TestCoverageReportDistinguishesRoadmapFromDrift guards SKT-0012.04 finding 1: "no construct
// exists at all" and "capture tagged a construct kind this build doesn't have registered" are
// different bugs routed to different fixes, so the report must never fold them into one bucket.
func TestCoverageReportDistinguishesRoadmapFromDrift(t *testing.T) {
	reg := runner.Catalog()
	sk := &Skeleton{Environments: []SkelEnv{{Cluster: &SkelCluster{}}}}
	gaps := []Gap{
		{Category: "addon", Name: "istiod", Evidence: []string{"deployment"}, Reason: "no matching construct"},
		{Category: "addon", Name: "renamed-thing", Evidence: []string{"helm-annotation"}, Reason: `construct kind "retired_kind" not registered`},
	}
	rep := CoverageReport(sk, gaps, reg)

	roadmapIdx := strings.Index(rep, "No construct exists")
	driftIdx := strings.Index(rep, "not registered")
	if roadmapIdx == -1 || driftIdx == -1 {
		t.Fatalf("expected both a roadmap section and a registry-drift section, got:\n%s", rep)
	}
	istiodIdx := strings.Index(rep, "istiod")
	renamedIdx := strings.Index(rep, "renamed-thing")
	if istiodIdx == -1 || renamedIdx == -1 {
		t.Fatalf("expected both gap names present, got:\n%s", rep)
	}
	// istiod (no construct at all) must land in the roadmap section, before the drift section.
	if !(roadmapIdx < istiodIdx && istiodIdx < driftIdx) {
		t.Fatalf("expected %q classified under the roadmap section, not drift, got:\n%s", "istiod", rep)
	}
	// renamed-thing (construct kind not registered) must land in the drift section, after it starts.
	if !(driftIdx < renamedIdx) {
		t.Fatalf("expected %q classified under the registry-drift section, not roadmap, got:\n%s", "renamed-thing", rep)
	}
}

// TestCoverageReportStatesDetectedAndSkipped guards SKT-0012.04 finding 3: a recognised platform
// product with no construct must read as "detected, deliberately unmodelled", not silence that
// could be misread as "not present in the cluster".
func TestCoverageReportStatesDetectedAndSkipped(t *testing.T) {
	reg := runner.Catalog()
	sk := &Skeleton{Environments: []SkelEnv{{Cluster: &SkelCluster{}}}}
	gaps := []Gap{
		{Category: "addon", Name: "crossplane", Evidence: []string{"crossplane", "crossplane-rbac-manager"},
			Reason: "no matching construct (recognised platform product, not surfaced by addon detection)"},
	}
	rep := CoverageReport(sk, gaps, reg)
	if !strings.Contains(rep, "crossplane") {
		t.Fatalf("expected the recognised product named in the report, got:\n%s", rep)
	}
	if !strings.Contains(rep, "Detected") && !strings.Contains(rep, "detected") {
		t.Fatalf("expected the report to say this WAS detected, not stay silent, got:\n%s", rep)
	}
}

func TestCoverageReportMatchedCountReflectsDedupedSkeletonAddons(t *testing.T) {
	reg := runner.Catalog()
	sk := &Skeleton{Environments: []SkelEnv{{Cluster: &SkelCluster{Addons: []string{"argocd", "cert_manager"}}}}}
	rep := CoverageReport(sk, nil, reg)
	if !strings.Contains(rep, "addons matched to constructs: 2") {
		t.Fatalf("expected matched count 2 from the deduplicated skeleton addons, got:\n%s", rep)
	}
}
