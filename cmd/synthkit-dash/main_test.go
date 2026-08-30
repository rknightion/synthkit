// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateWritesFiles(t *testing.T) {
	out := t.TempDir()
	datasources := datasourceFlags{"prometheus": "target-prometheus", "loki": "target-loki", "tempo": "target-tempo"}
	if err := generate("../../blueprints/k8s-full-stack.yaml", out, "", "", datasources); err != nil {
		t.Fatalf("generate: %v", err)
	}
	entries, _ := os.ReadDir(out)
	if len(entries) == 0 {
		t.Fatal("no dashboards written")
	}
	// the index is always written (filename is the blueprint name)
	if _, err := os.Stat(filepath.Join(out, "k8s-full-stack-index.json")); err != nil {
		t.Errorf("index not written: %v", err)
	}
	// the per-blueprint metrics dashboard is always written
	if _, err := os.Stat(filepath.Join(out, "k8s-full-stack-metrics.json")); err != nil {
		t.Errorf("metrics dashboard not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "k8s-full-stack-dashboards-folder.json")); err != nil {
		t.Errorf("folder resource not written: %v", err)
	}
	inventoryPath := filepath.Join(out, "k8s-full-stack-panel-inventory.json")
	inventory, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatalf("panel inventory not written: %v", err)
	}
	if !strings.Contains(string(inventory), `"panels"`) {
		t.Errorf("panel inventory has no panels: %s", inventory)
	}
	// every file is valid GA v2 JSON
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-folder.json") || strings.HasSuffix(e.Name(), "-panel-inventory.json") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(out, e.Name()))
		if !strings.Contains(string(b), "dashboard.grafana.app/v2") {
			t.Errorf("%s is not GA v2", e.Name())
		}
	}
}

func TestBindDatasourcesRequiresEveryRenderedGroup(t *testing.T) {
	raw := []byte(`{"spec":{"elements":{"panel":{"kind":"Panel","spec":{"data":{"kind":"QueryGroup","spec":{"queries":[{"spec":{"query":{"kind":"DataQuery","group":"prometheus","version":"v0","spec":{"expr":"up"}}}}]}}}}}}}`)
	if _, err := bindDatasources(raw, datasourceFlags{}); err == nil || !strings.Contains(err.Error(), "prometheus") {
		t.Fatalf("bindDatasources missing mapping error = %v, want prometheus", err)
	}
	bound, err := bindDatasources(raw, datasourceFlags{"prometheus": "target-prometheus"})
	if err != nil {
		t.Fatalf("bindDatasources: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(bound, &doc); err != nil {
		t.Fatal(err)
	}
	encoded := string(bound)
	if !strings.Contains(encoded, `"datasource": {`) || !strings.Contains(encoded, `"name": "target-prometheus"`) {
		t.Errorf("bound query has no explicit datasource: %s", encoded)
	}
}
