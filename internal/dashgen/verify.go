// SPDX-License-Identifier: AGPL-3.0-only

package dashgen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PanelInventory is the machine-readable contract between dashboard generation and
// live verification. It is deliberately independent of gcx: the operator can use
// their stack's approved read-only query path, then feed the observations back to
// Verify without copying queries by hand.
type PanelInventory struct {
	Panels []Panel `json:"panels"`
}

// Panel identifies one generated panel and every data query it declares. Text and
// other query-free panels are represented with an empty Queries slice.
type Panel struct {
	Dashboard string  `json:"dashboard"`
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Queries   []Query `json:"queries"`
}

// Query is a rendered dashboard data query, including its explicitly configured
// datasource and the expression (when the datasource exposes one).
type Query struct {
	RefID           string `json:"ref_id"`
	DatasourceGroup string `json:"datasource_group"`
	DatasourceName  string `json:"datasource_name"`
	Expression      string `json:"expression"`
}

// Observation is one read-only live-query outcome. State must be rendered, empty,
// or errored. Evidence is carried verbatim into the report so an empty verdict is
// auditable rather than an assertion based on a dashboard screenshot.
type Observation struct {
	Dashboard string `json:"dashboard"`
	PanelID   string `json:"panel_id"`
	RefID     string `json:"ref_id"`
	State     string `json:"state"`
	Evidence  string `json:"evidence"`
}

// ObservationFile keeps the hand-off file extensible without making the verifier
// depend on a particular Grafana or gcx response schema.
type ObservationFile struct {
	Observations []Observation `json:"observations"`
}

// Verdict is the panel-level result. Classification is populated for empty and
// errored panels: emission_gap for a successful empty query, query_defect for a
// query error, and missing_datasource for datasource-resolution evidence.
type Verdict struct {
	Dashboard      string        `json:"dashboard"`
	PanelID        string        `json:"panel_id"`
	Title          string        `json:"title"`
	Disposition    string        `json:"disposition"`
	Classification string        `json:"classification,omitempty"`
	Queries        []QueryResult `json:"queries"`
}

// QueryResult joins the rendered query to its observed live outcome.
type QueryResult struct {
	Query
	State          string `json:"state"`
	Classification string `json:"classification,omitempty"`
	Evidence       string `json:"evidence"`
}

// Report is the complete panel-by-panel verification result.
type Report struct {
	Panels []Verdict `json:"panels"`
}

// InventoryFromDashboard extracts panels and DataQuery entries from a rendered v2
// dashboard manifest. It intentionally reads the JSON shape emitted by the
// Foundation SDK, avoiding a second mutable dashboard representation.
func InventoryFromDashboard(raw []byte) (PanelInventory, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return PanelInventory{}, fmt.Errorf("decode dashboard manifest: %w", err)
	}
	metadata, _ := root["metadata"].(map[string]any)
	dashboard, _ := metadata["name"].(string)
	if dashboard == "" {
		return PanelInventory{}, fmt.Errorf("dashboard manifest has no metadata.name")
	}
	spec, _ := root["spec"].(map[string]any)
	elements, _ := spec["elements"].(map[string]any)
	if elements == nil {
		return PanelInventory{}, fmt.Errorf("dashboard %q has no spec.elements", dashboard)
	}

	panelIDs := make([]string, 0, len(elements))
	for id := range elements {
		panelIDs = append(panelIDs, id)
	}
	sort.Strings(panelIDs)

	out := PanelInventory{Panels: make([]Panel, 0, len(panelIDs))}
	for _, id := range panelIDs {
		element, _ := elements[id].(map[string]any)
		if element["kind"] != "Panel" {
			continue
		}
		panelSpec, _ := element["spec"].(map[string]any)
		title, _ := panelSpec["title"].(string)
		p := Panel{Dashboard: dashboard, ID: id, Title: title}
		walkQueries(panelSpec, "", func(q map[string]any, inheritedRefID string) {
			group, _ := q["group"].(string)
			if group == "" {
				return
			}
			datasource, _ := q["datasource"].(map[string]any)
			name, _ := datasource["name"].(string)
			qspec, _ := q["spec"].(map[string]any)
			expr, _ := qspec["expr"].(string)
			if expr == "" {
				expr, _ = qspec["query"].(string)
			}
			refID, _ := qspec["refId"].(string)
			if refID == "" {
				refID = inheritedRefID
			}
			p.Queries = append(p.Queries, Query{RefID: refID, DatasourceGroup: group, DatasourceName: name, Expression: expr})
		})
		sort.Slice(p.Queries, func(i, j int) bool {
			if p.Queries[i].RefID == p.Queries[j].RefID {
				return p.Queries[i].Expression < p.Queries[j].Expression
			}
			return p.Queries[i].RefID < p.Queries[j].RefID
		})
		out.Panels = append(out.Panels, p)
	}
	return out, nil
}

func walkQueries(v any, inheritedRefID string, visit func(map[string]any, string)) {
	switch node := v.(type) {
	case map[string]any:
		if node["kind"] == "PanelQuery" {
			if spec, ok := node["spec"].(map[string]any); ok {
				if refID, ok := spec["refId"].(string); ok {
					inheritedRefID = refID
				}
			}
		}
		if node["kind"] == "DataQuery" {
			visit(node, inheritedRefID)
		}
		for _, child := range node {
			walkQueries(child, inheritedRefID, visit)
		}
	case []any:
		for _, child := range node {
			walkQueries(child, inheritedRefID, visit)
		}
	}
}

// Verify requires one observation for every query in inventory and returns one
// verdict per panel. Query-free panels are rendered static content by construction.
func Verify(inventory PanelInventory, observations ObservationFile) (Report, error) {
	seen := make(map[string]Observation, len(observations.Observations))
	for _, observed := range observations.Observations {
		key := observationKey(observed.Dashboard, observed.PanelID, observed.RefID)
		if _, exists := seen[key]; exists {
			return Report{}, fmt.Errorf("duplicate observation for %s", key)
		}
		if !validState(observed.State) {
			return Report{}, fmt.Errorf("observation %s has invalid state %q", key, observed.State)
		}
		seen[key] = observed
	}

	report := Report{Panels: make([]Verdict, 0, len(inventory.Panels))}
	for _, panel := range inventory.Panels {
		verdict := Verdict{Dashboard: panel.Dashboard, PanelID: panel.ID, Title: panel.Title}
		if len(panel.Queries) == 0 {
			verdict.Disposition = "rendered"
			verdict.Queries = []QueryResult{}
			report.Panels = append(report.Panels, verdict)
			continue
		}
		for _, query := range panel.Queries {
			key := observationKey(panel.Dashboard, panel.ID, query.RefID)
			observed, ok := seen[key]
			if !ok {
				return Report{}, fmt.Errorf("missing observation for %s", key)
			}
			delete(seen, key)
			result := QueryResult{Query: query, State: observed.State, Evidence: observed.Evidence}
			if observed.State != "rendered" {
				result.Classification = classify(observed)
			}
			verdict.Queries = append(verdict.Queries, result)
		}
		verdict.Disposition, verdict.Classification = panelOutcome(verdict.Queries)
		report.Panels = append(report.Panels, verdict)
	}
	if len(seen) > 0 {
		keys := make([]string, 0, len(seen))
		for key := range seen {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return Report{}, fmt.Errorf("observations do not match generated queries: %s", strings.Join(keys, ", "))
	}
	return report, nil
}

func observationKey(dashboard, panel, refID string) string {
	return dashboard + "/" + panel + "/" + refID
}

func validState(state string) bool {
	return state == "rendered" || state == "empty" || state == "errored"
}

func classify(observed Observation) string {
	evidence := strings.ToLower(observed.Evidence)
	if strings.Contains(evidence, "datasource not found") || strings.Contains(evidence, "data source not found") || strings.Contains(evidence, "unknown datasource") {
		return "missing_datasource"
	}
	if observed.State == "errored" || strings.Contains(evidence, "parse error") || strings.Contains(evidence, "syntax error") || strings.Contains(evidence, "bad_data") || strings.Contains(evidence, "invalid query") {
		return "query_defect"
	}
	return "emission_gap"
}

func panelOutcome(queries []QueryResult) (string, string) {
	for _, query := range queries {
		if query.State == "errored" {
			return "errored", query.Classification
		}
	}
	for _, query := range queries {
		if query.State == "empty" {
			return "empty", query.Classification
		}
	}
	return "rendered", ""
}
