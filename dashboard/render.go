// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"encoding/json"
	"strings"

	dashboardv2 "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
	"github.com/grafana/grafana-foundation-sdk/go/resource"
)

// Dashboard wraps a Foundation SDK v2 dashboard builder + its UID.
// The UID flows into the manifest via the Manifest(name, builder) call in Render —
// there is no .Uid() method on DashboardBuilder; name == UID in the resource metadata.
type Dashboard struct {
	UID     string
	Builder *dashboardv2.DashboardBuilder
	// Folder, when set, is the target Grafana folder UID; Render stamps it as the
	// "grafana.app/folder" metadata annotation so the dashboard lands in that folder (the folder
	// must already exist). Empty = the instance's General/root folder.
	Folder string
	// nextID assigns each panel a UNIQUE numeric panel id. Grafana keys panels by this id; if every
	// panel keeps the zero value, the renderer collapses them to one panel per tab (in BOTH the live
	// UI and headless snapshots). AddPanel increments this and stamps it on the panel.
	nextID int64
}

// Template is the contract a per-blueprint dashboard author implements. It receives the
// derived Manifest and returns one built dashboard. (FROZEN SEAM.)
type Template func(*Manifest) (Dashboard, error)

// NewDashboard starts a v2 dashboard builder with the given uid + title.
// The uid is stored on Dashboard and applied to the manifest in Render via
// dashboardv2.Manifest(d.UID, d.Builder) — NOT via any builder method (none exists).
func NewDashboard(uid, title string) (Dashboard, error) {
	b := dashboardv2.NewDashboardBuilder(title)
	d := Dashboard{UID: uid, Builder: b}
	// Auto cross-nav: tag the dashboard with its uid group and attach a same-group dropdown so
	// every dashboard in a set (acme-ws1 / acme-ws2 / initech / …) links to its peers
	// in the Grafana top bar — zero per-template churn.
	if tag := navGroupTag(uid); tag != "" {
		b.Tags([]string{tag})
		WithNavLinks(&d, NavDropdown("Related dashboards", []string{tag}))
	}
	return d, nil
}

// navGroupTag derives the cross-nav group tag from a dashboard uid. Acme dashboards group by
// workstream (acme-ws1-portkey → "acme-ws1"); everything else groups by its leading token
// (eval-overview → "eval", initech-foo → "initech"). Returns "" for an empty uid.
func navGroupTag(uid string) string {
	parts := strings.Split(uid, "-")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	if parts[0] == "acme" && len(parts) >= 2 {
		return parts[0] + "-" + parts[1]
	}
	return parts[0]
}

// Render builds the dashboard and marshals it to the GA v2 manifest JSON bytes.
// The apiVersion emitted is "dashboard.grafana.app/v2" (from dashboardv2.Manifest).
func Render(d Dashboard) ([]byte, error) {
	mb := dashboardv2.Manifest(d.UID, d.Builder)
	if d.Folder != "" {
		// Override the manifest metadata to keep the name AND add the folder annotation. The k8s-style
		// dashboard API places the resource by the "grafana.app/folder" annotation (folder UID).
		mb = mb.Metadata(resource.NewMetadataBuilder().
			Name(d.UID).
			Annotations(map[string]string{"grafana.app/folder": d.Folder}))
	}
	res, err := mb.Build()
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(res, "", "  ")
}
