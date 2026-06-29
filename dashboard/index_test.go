// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"strings"
	"testing"
)

func TestIndexLinksConfiguredIntegrations(t *testing.T) {
	m := &Manifest{Blueprint: "initech", Label: "initech", Integrations: []IntegrationRef{{Kind: "k8s"}}}
	cfg := IntegrationsConfig{Targets: map[string]IntegrationTarget{
		"k8s": {DashboardUID: "k8s-monitoring", Title: "Kubernetes"},
	}}
	d, err := IndexDashboard(m, cfg)
	if err != nil {
		t.Fatalf("IndexDashboard: %v", err)
	}
	out, _ := Render(d)
	if !strings.Contains(string(out), "k8s-monitoring") {
		t.Errorf("index missing the k8s deep-link target uid")
	}
}

// TestIndexHasLayout is a regression guard: the GA v2 schema REJECTS a dashboard with no
// layout (the index failed live validation when it set none). Every index must carry one.
func TestIndexHasLayout(t *testing.T) {
	m := &Manifest{Blueprint: "initech", Label: "initech", Integrations: []IntegrationRef{{Kind: "k8s"}}}
	d, err := IndexDashboard(m, IntegrationsConfig{}) // even with NO links, it must have a layout
	if err != nil {
		t.Fatalf("IndexDashboard: %v", err)
	}
	out, _ := Render(d)
	if !strings.Contains(string(out), "GridLayout") {
		t.Errorf("index dashboard has no GridLayout — v2 schema requires exactly one layout")
	}
}

func TestIndexSkipsUnconfigured(t *testing.T) {
	m := &Manifest{Integrations: []IntegrationRef{{Kind: "aws"}}}
	d, err := IndexDashboard(m, IntegrationsConfig{})
	if err != nil {
		t.Fatalf("IndexDashboard: %v", err)
	}
	out, _ := Render(d)
	if strings.Contains(string(out), "/d/") {
		t.Errorf("index should have no links when nothing is configured")
	}
}

// TestIndexWiresDeepLinkVariables: when a link is emitted AND the estate has clusters/
// accounts, the index must carry the cluster/account template variables the deep-link URL
// interpolates ($cluster/$account) — otherwise the params pass through un-resolved.
func TestIndexWiresDeepLinkVariables(t *testing.T) {
	m := &Manifest{
		Blueprint:    "initech",
		Label:        "initech",
		Clusters:     []ClusterRef{{Name: "initech-prod-use1"}},
		Accounts:     []AccountRef{{ID: "111122223333"}},
		Integrations: []IntegrationRef{{Kind: "k8s"}},
	}
	cfg := IntegrationsConfig{Targets: map[string]IntegrationTarget{
		"k8s": {DashboardUID: "k8s-monitoring", Title: "Kubernetes"},
	}}
	d, err := IndexDashboard(m, cfg)
	if err != nil {
		t.Fatalf("IndexDashboard: %v", err)
	}
	out, _ := Render(d)
	s := string(out)
	// the cluster variable must be present with its estate-seeded value
	if !strings.Contains(s, "initech-prod-use1") {
		t.Errorf("index missing the estate-seeded cluster variable value")
	}
	if !strings.Contains(s, "111122223333") {
		t.Errorf("index missing the estate-seeded account variable value")
	}
}
