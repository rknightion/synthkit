// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// renderMergePanel builds a MergeTablePanel and returns the JSON of the built panel
// so we can assert transformation content without needing a full dashboard layout.
func renderMergePanel(t *testing.T, title string, targets []*dashboardv2.TargetBuilder, opts OrganizeOptions) string {
	t.Helper()
	panel := MergeTablePanel(title, targets, opts)
	built, err := panel.Build()
	if err != nil {
		t.Fatalf("MergeTablePanel.Build: %v", err)
	}
	b, err := json.Marshal(built)
	if err != nil {
		t.Fatalf("json.Marshal panel: %v", err)
	}
	return string(b)
}

func TestMergeTablePanel_BasicTransformations(t *testing.T) {
	// Two instant PromQL queries that should be merged into one row-per-label table.
	targets := []*dashboardv2.TargetBuilder{
		PromTableTarget(`rate(http_requests_total{job="api"}[$__rate_interval])`, "A"),
		PromTableTarget(`avg(request_duration_seconds{job="api"})`, "B"),
	}
	opts := OrganizeOptions{
		Exclude: []string{"Time", "__name__"},
		Rename:  map[string]string{"Value #A": "Rate", "Value #B": "Avg Latency"},
		Order:   []string{"job", "Rate", "Avg Latency"},
	}

	json := renderMergePanel(t, "API Table", targets, opts)

	// Panel must carry both transformations.
	if !strings.Contains(json, `"merge"`) {
		t.Errorf("rendered JSON missing merge transformation")
	}
	if !strings.Contains(json, `"organize"`) {
		t.Errorf("rendered JSON missing organize transformation")
	}

	// Rename entries must appear.
	if !strings.Contains(json, "Value #A") {
		t.Errorf("rendered JSON missing original column name \"Value #A\"")
	}
	if !strings.Contains(json, "Avg Latency") {
		t.Errorf("rendered JSON missing renamed column \"Avg Latency\"")
	}

	// Exclude list must appear.
	if !strings.Contains(json, "Time") {
		t.Errorf("rendered JSON missing excluded column \"Time\"")
	}

	// Panel title must be present.
	if !strings.Contains(json, "API Table") {
		t.Errorf("rendered JSON missing panel title")
	}
}

func TestMergeTablePanel_TwoTargets_MultiInstant(t *testing.T) {
	// Verify that both targets are encoded with instant=true and format=table.
	targets := []*dashboardv2.TargetBuilder{
		PromTableTarget(`sum by (service) (rate(requests_total[$__rate_interval]))`, "A"),
		PromTableTarget(`histogram_quantile(0.95, rate(latency_bucket[$__rate_interval]))`, "B"),
	}
	opts := OrganizeOptions{
		Rename: map[string]string{"Value #A": "RPS", "Value #B": "p95"},
		Order:  []string{"service", "RPS", "p95"},
	}

	json := renderMergePanel(t, "Service Metrics", targets, opts)

	// Both instant queries must appear.
	if !strings.Contains(json, `"instant":true`) {
		t.Errorf("rendered JSON missing instant:true on at least one target")
	}
	if !strings.Contains(json, `"table"`) {
		t.Errorf("rendered JSON missing format:table on at least one target")
	}

	// organize renameByName must have both columns.
	if !strings.Contains(json, "Value #A") {
		t.Errorf("rendered JSON missing rename key \"Value #A\"")
	}
	if !strings.Contains(json, "p95") {
		t.Errorf("rendered JSON missing rename value \"p95\"")
	}
}

func TestMergeTablePanel_EmptyOrganize(t *testing.T) {
	// An empty OrganizeOptions must not panic and must still include the merge + organize transformations.
	targets := []*dashboardv2.TargetBuilder{
		PromTableTarget(`up`, "A"),
	}
	json := renderMergePanel(t, "Up", targets, OrganizeOptions{})

	if !strings.Contains(json, `"merge"`) {
		t.Errorf("rendered JSON missing merge transformation with empty OrganizeOptions")
	}
	if !strings.Contains(json, `"organize"`) {
		t.Errorf("rendered JSON missing organize transformation with empty OrganizeOptions")
	}
}

func TestPromTableTarget_Fields(t *testing.T) {
	// PromTableTarget must set the refID, format=table, and instant=true.
	tgt := PromTableTarget(`some_metric`, "C")
	built, err := tgt.Build()
	if err != nil {
		t.Fatalf("PromTableTarget.Build: %v", err)
	}
	b, err := json.Marshal(built)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"C"`) {
		t.Errorf("PromTableTarget JSON missing refID \"C\": %s", s)
	}
	if !strings.Contains(s, `"table"`) {
		t.Errorf("PromTableTarget JSON missing format \"table\": %s", s)
	}
	if !strings.Contains(s, `"instant":true`) {
		t.Errorf("PromTableTarget JSON missing instant:true: %s", s)
	}
}
