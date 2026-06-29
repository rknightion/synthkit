// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"encoding/json"
	"testing"
)

func TestRenderRules(t *testing.T) {
	groups := []RuleGroup{
		{
			Name:      "test-group",
			FolderUID: "folder-abc",
			Recordings: []RecordingRule{
				{
					UID:         "rec-001",
					Record:      "job:http_requests:rate5m",
					Datasource:  "mimir",
					Expr:        `rate(http_requests_total[5m])`,
					IntervalSec: 60,
				},
			},
			Alerts: []AlertRule{
				{
					UID:        "alert-001",
					Title:      "High Error Rate",
					Datasource: "mimir",
					Expr:       `rate(http_errors_total[5m]) > 0.05`,
					Threshold:  0.05,
					Op:         "gt",
					For:        "5m",
					Severity:   "critical",
					Labels:     map[string]string{"team": "platform"},
					Paused:     false,
				},
			},
		},
	}

	data, err := RenderRules("test-scenario", groups)
	if err != nil {
		t.Fatalf("RenderRules error: %v", err)
	}

	// Must be valid JSON
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, data)
	}

	// Top-level must have "groups" key
	groupsRaw, ok := raw["groups"]
	if !ok {
		t.Fatal("output missing top-level 'groups' key")
	}
	groupsSlice, ok := groupsRaw.([]interface{})
	if !ok || len(groupsSlice) != 1 {
		t.Fatalf("expected 1 group, got %v", groupsRaw)
	}

	group := groupsSlice[0].(map[string]interface{})
	rules, ok := group["rules"].([]interface{})
	if !ok || len(rules) != 2 {
		t.Fatalf("expected 2 rules in group, got %v", group["rules"])
	}

	// First rule is the recording rule — must have record.metric
	recRule := rules[0].(map[string]interface{})
	recField, ok := recRule["record"].(map[string]interface{})
	if !ok {
		t.Fatalf("recording rule missing 'record' field: %v", recRule)
	}
	if metric, _ := recField["metric"].(string); metric != "job:http_requests:rate5m" {
		t.Errorf("record.metric = %q, want %q", metric, "job:http_requests:rate5m")
	}

	// Second rule is the alert — must have title, no record field
	alertRule := rules[1].(map[string]interface{})
	if title, _ := alertRule["title"].(string); title != "High Error Rate" {
		t.Errorf("alert title = %q, want %q", title, "High Error Rate")
	}
	if _, hasRecord := alertRule["record"]; hasRecord {
		t.Error("alert rule must not have 'record' field")
	}
}

func TestRenderRulesRecordingIsInstant(t *testing.T) {
	// Grafana-managed recording rules MUST evaluate as instant queries — a range
	// query returns a matrix ("timeseries-multi") that remote-write rejects with 422.
	groups := []RuleGroup{
		{
			Name:      "g",
			FolderUID: "f",
			Recordings: []RecordingRule{
				{UID: "r1", Record: "llm_retries_total", Datasource: "loki", Expr: `sum(count_over_time({source="portkey"} | json | retry_count > 0 [5m]))`},
			},
		},
	}
	data, err := RenderRules("s", groups)
	if err != nil {
		t.Fatalf("RenderRules error: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	group := raw["groups"].([]interface{})[0].(map[string]interface{})
	rule := group["rules"].([]interface{})[0].(map[string]interface{})
	query := rule["data"].([]interface{})[0].(map[string]interface{})
	if qt, _ := query["queryType"].(string); qt != "instant" {
		t.Errorf("recording query queryType = %q, want %q", qt, "instant")
	}
	model := query["model"].(map[string]interface{})
	if qt, _ := model["queryType"].(string); qt != "instant" {
		t.Errorf("recording model.queryType = %q, want %q", qt, "instant")
	}
	if inst, _ := model["instant"].(bool); !inst {
		t.Errorf("recording model.instant = %v, want true", model["instant"])
	}
}

func TestRenderRulesDefaultInterval(t *testing.T) {
	groups := []RuleGroup{
		{
			Name:      "no-interval",
			FolderUID: "folder-xyz",
			Recordings: []RecordingRule{
				{
					UID:        "rec-002",
					Record:     "job:foo:rate",
					Datasource: "loki",
					Expr:       `rate({job="foo"}[5m])`,
					// IntervalSec zero → should default to 300
				},
			},
		},
	}

	data, err := RenderRules("test-scenario", groups)
	if err != nil {
		t.Fatalf("RenderRules error: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	group := raw["groups"].([]interface{})[0].(map[string]interface{})
	if interval, _ := group["interval"].(float64); interval != 300 {
		t.Errorf("interval = %v, want 300 (default)", interval)
	}

	// datasourceUid must be the Loki placeholder
	rule := group["rules"].([]interface{})[0].(map[string]interface{})
	dataArr := rule["data"].([]interface{})
	query := dataArr[0].(map[string]interface{})
	if uid, _ := query["datasourceUid"].(string); uid != "${LOKI_DATASOURCE_UID}" {
		t.Errorf("datasourceUid = %q, want ${LOKI_DATASOURCE_UID}", uid)
	}
}
