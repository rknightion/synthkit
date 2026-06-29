// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"github.com/grafana/grafana-foundation-sdk/go/cog"
	dashboardv2 "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// NavDropdown returns a cross-dashboard dropdown link that surfaces all dashboards
// carrying every tag in tags. The dropdown preserves the current time range
// (KeepTime: true) and does NOT propagate template variables (IncludeVars: false)
// or open in a new tab (TargetBlank: false).
//
// Typical call:
//
//	NavDropdown("Acme AI dashboards", []string{"acme-ws1"})
//
// The returned builder is consumed by WithNavLinks.
func NavDropdown(title string, tags []string) cog.Builder[dashboardv2.DashboardLink] {
	return dashboardv2.NewDashboardLinkBuilder().
		Title(title).
		Type(dashboardv2.DashboardLinkTypeDashboards).
		AsDropdown(true).
		Tags(tags).
		KeepTime(true).
		IncludeVars(false).
		TargetBlank(false)
}

// WithNavLinks attaches one or more nav links to d.Builder via .Links(). Links are
// appended; each call replaces the full slice, so pass all desired links at once or
// call multiple times if you accumulate them.
//
// Recommended retrofit pattern for existing dashboard templates:
//
//  1. Tag the dashboard via d.Builder.Tags([]string{"acme-ws1"}) (or whatever shared tag).
//  2. After building panels/rows, call:
//     dashboard.WithNavLinks(&d, dashboard.NavDropdown("Related dashboards", []string{"acme-ws1"}))
//
// NewDashboard does NOT auto-inject nav links because no shared tag is currently set
// universally — auto-injection without a matching tag would produce an empty dropdown.
// Once all dashboards are tagged, a wiring pass can add auto-injection to NewDashboard.
func WithNavLinks(d *Dashboard, links ...cog.Builder[dashboardv2.DashboardLink]) {
	d.Builder.Links(links)
}
