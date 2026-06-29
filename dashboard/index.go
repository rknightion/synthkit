// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"fmt"
	"strings"

	"github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// IntegrationTarget is a per-stack vendor dashboard deep-link target.
type IntegrationTarget struct {
	DashboardUID string `yaml:"dashboard_uid"`
	Title        string `yaml:"title"`
}

// IntegrationsConfig maps integration kind → deep-link target. Loaded per-deploy from a
// YAML file passed to synthkit-dash; never committed with real UIDs.
type IntegrationsConfig struct {
	Targets map[string]IntegrationTarget `yaml:"targets"`
}

// IndexDashboard builds the thin landing dashboard: a links panel per configured
// integration, scoped to this blueprint's estate. Integrations with no configured target
// in cfg are silently skipped — no invented links are emitted.
func IndexDashboard(m *Manifest, cfg IntegrationsConfig) (Dashboard, error) {
	uid := m.Blueprint + "-index"
	title := m.Blueprint + " — index"
	d, err := NewDashboard(uid, title)
	if err != nil {
		return Dashboard{}, err
	}
	// A header panel always present → the index is self-documenting AND never empty (the v2
	// schema rejects a dashboard that has no layout / no panels).
	ids := []string{"index-header"}
	AddPanel(&d, "index-header", TextPanel(title, indexHeaderMarkdown(m, cfg)))

	linked := false
	for _, integ := range m.Integrations {
		tgt, ok := cfg.Targets[integ.Kind]
		if !ok {
			continue // no configured target for this kind → skip (no invented links)
		}
		id := "link-" + integ.Kind
		AddPanel(&d, id, linksPanel(tgt, m))
		ids = append(ids, id)
		linked = true
	}
	// Every dashboard MUST set a layout (v2 requirement) — a flat grid over the panels.
	WithGrid(&d, ids...)

	// Wire the template variables the deep-links interpolate ($cluster/$account), seeded
	// from the estate — only when at least one link references them, and only for the
	// dimensions the estate actually has.
	if linked {
		if len(m.Clusters) > 0 {
			d.Builder.CustomVariable(ClusterVar(m))
		}
		if len(m.Accounts) > 0 {
			d.Builder.CustomVariable(AccountVar(m))
		}
	}
	return d, nil
}

// indexHeaderMarkdown renders the index's self-documenting header: the blueprint, and each
// off-the-shelf integration the estate lights up with whether it is linked (configured) or
// awaiting a target in integrations.yaml.
func indexHeaderMarkdown(m *Manifest, cfg IntegrationsConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — integration index\n\n", m.Blueprint)
	if len(m.Integrations) == 0 {
		b.WriteString("_No off-the-shelf integrations detected for this blueprint._\n")
		return b.String()
	}
	b.WriteString("Off-the-shelf vendor dashboards for this estate:\n\n")
	for _, integ := range m.Integrations {
		if _, ok := cfg.Targets[integ.Kind]; ok {
			fmt.Fprintf(&b, "- **%s** — linked below\n", integ.Kind)
		} else {
			fmt.Fprintf(&b, "- **%s** — not configured (add a target to integrations.yaml)\n", integ.Kind)
		}
	}
	return b.String()
}

// linksPanel builds a text panel whose body is a markdown deep-link to the vendor
// dashboard UID with the blueprint scope pre-applied to the URL query params.
func linksPanel(tgt IntegrationTarget, m *Manifest) *dashboardv2.PanelBuilder {
	url := fmt.Sprintf("/d/%s?var-cluster=$cluster&var-account=$account", tgt.DashboardUID)
	if m.Blueprint != "" {
		url += fmt.Sprintf("&var-blueprint=%s", m.Blueprint)
	}
	markdown := fmt.Sprintf("[%s](%s)", tgt.Title, url)
	return TextPanel(tgt.Title, markdown)
}
