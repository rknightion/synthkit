// SPDX-License-Identifier: AGPL-3.0-only

package dashgen

import (
	"strings"
	"testing"
)

func TestInventoryFromDashboardEnumeratesPanelsAndQueries(t *testing.T) {
	raw := []byte(`{
  "metadata":{"name":"demo-metrics"},
  "spec":{"elements":{
    "text":{"kind":"Panel","spec":{"title":"Read me","data":{"kind":"QueryGroup","spec":{"queries":[]}}}},
    "latency":{"kind":"Panel","spec":{"title":"Latency","data":{"kind":"QueryGroup","spec":{"queries":[{"kind":"PanelQuery","spec":{"query":{"kind":"DataQuery","group":"prometheus","version":"v0","datasource":{"name":"target-prometheus"},"spec":{"refId":"A","expr":"histogram_quantile(0.95, rate(traces_spanmetrics_latency[$__rate_interval]))"}}}}]}}}}
  }}
}`)
	inventory, err := InventoryFromDashboard(raw)
	if err != nil {
		t.Fatalf("InventoryFromDashboard: %v", err)
	}
	if len(inventory.Panels) != 2 {
		t.Fatalf("panels = %d, want 2", len(inventory.Panels))
	}
	latency := inventory.Panels[0]
	if latency.ID != "latency" || len(latency.Queries) != 1 {
		t.Fatalf("latency panel = %+v", latency)
	}
	query := latency.Queries[0]
	if query.DatasourceName != "target-prometheus" || query.DatasourceGroup != "prometheus" {
		t.Errorf("datasource = %q/%q, want target-prometheus/prometheus", query.DatasourceName, query.DatasourceGroup)
	}
	if !strings.Contains(query.Expression, "traces_spanmetrics_latency") {
		t.Errorf("expression = %q, want native histogram family", query.Expression)
	}
}

func TestInventoryFromDashboardReadsRefIDFromPanelQuery(t *testing.T) {
	raw := []byte(`{
  "metadata":{"name":"demo"},
  "spec":{"elements":{"latency":{"kind":"Panel","spec":{"title":"Latency","data":{"kind":"QueryGroup","spec":{"queries":[
    {"kind":"PanelQuery","spec":{"refId":"A","query":{"kind":"DataQuery","group":"prometheus","datasource":{"name":"prom"},"spec":{"expr":"up"}}}},
    {"kind":"PanelQuery","spec":{"refId":"B","query":{"kind":"DataQuery","group":"prometheus","datasource":{"name":"prom"},"spec":{"expr":"rate(requests_total[5m])"}}}}
  ]}}}}}}}`)
	inventory, err := InventoryFromDashboard(raw)
	if err != nil {
		t.Fatalf("InventoryFromDashboard: %v", err)
	}
	if got := []string{inventory.Panels[0].Queries[0].RefID, inventory.Panels[0].Queries[1].RefID}; got[0] != "A" || got[1] != "B" {
		t.Fatalf("ref IDs = %v, want [A B]", got)
	}
}

func TestVerifyRequiresCompleteObservationsAndClassifies(t *testing.T) {
	inventory := PanelInventory{Panels: []Panel{
		{Dashboard: "demo", ID: "header", Title: "Read me"},
		{Dashboard: "demo", ID: "latency", Title: "Latency", Queries: []Query{{RefID: "A", DatasourceGroup: "prometheus", DatasourceName: "target-prometheus", Expression: "up"}}},
		{Dashboard: "demo", ID: "logs", Title: "Logs", Queries: []Query{{RefID: "A", DatasourceGroup: "loki", DatasourceName: "target-loki", Expression: "{source=\"app\"}"}}},
		{Dashboard: "demo", ID: "broken", Title: "Broken", Queries: []Query{{RefID: "A", DatasourceGroup: "prometheus", DatasourceName: "gone", Expression: "bad"}}},
	}}
	observations := ObservationFile{Observations: []Observation{
		{Dashboard: "demo", PanelID: "latency", RefID: "A", State: "empty", Evidence: "HTTP 200; returned no frames"},
		{Dashboard: "demo", PanelID: "logs", RefID: "A", State: "errored", Evidence: "parse error at line 1"},
		{Dashboard: "demo", PanelID: "broken", RefID: "A", State: "errored", Evidence: "datasource not found: gone"},
	}}
	report, err := Verify(inventory, observations)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := report.Panels[3].Disposition; got != "errored" {
		t.Errorf("broken disposition = %q, want errored", got)
	}
	if got := report.Panels[3].Classification; got != "missing_datasource" {
		t.Errorf("broken classification = %q, want missing_datasource", got)
	}
	if got := report.Panels[2].Classification; got != "query_defect" {
		t.Errorf("logs classification = %q, want query_defect", got)
	}
	if got := report.Panels[1].Classification; got != "emission_gap" {
		t.Errorf("latency classification = %q, want emission_gap", got)
	}

	_, err = Verify(inventory, ObservationFile{})
	if err == nil || !strings.Contains(err.Error(), "missing observation") {
		t.Fatalf("incomplete Verify error = %v, want missing observation", err)
	}
}
