// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/dashgen"
)

const otlpNativeBlueprint = "../../blueprints/otlp-native.yaml"

func TestOTLPNativeCLIAddsHTTPDashboard(t *testing.T) {
	e := load(t, generated(t, otlpNativeBlueprint))
	raw, ok := e.tree["otlp-native-http.json"]
	if !ok {
		t.Fatal("otlp-native generation did not emit its HTTP dashboard")
	}
	inv, err := dashgen.InventoryFromDashboard(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"request-rate":    false,
		"error-ratio":     false,
		"latency":         false,
		"active-requests": false,
	}
	for _, panel := range inv.Panels {
		if _, required := want[panel.ID]; required {
			if len(panel.Queries) == 0 {
				t.Errorf("HTTP panel %s has no query", panel.ID)
			}
			want[panel.ID] = true
		}
		for _, q := range panel.Queries {
			if q.DatasourceGroup != "prometheus" || q.DatasourceName != "target-prometheus" {
				t.Errorf("query %s/%s datasource = %q/%q", panel.ID, q.RefID, q.DatasourceGroup, q.DatasourceName)
			}
			for _, required := range []string{"service_name", "otlp-api-enriched", "otlp-api-naked", "deployment_environment_name", "target_info", "and on (job, instance)"} {
				if !strings.Contains(q.Expression, required) {
					t.Errorf("HTTP query %s/%s omits %s: %s", panel.ID, q.RefID, required, q.Expression)
				}
			}
			if strings.Contains(q.Expression, "$__rate_interval") || (panel.ID != "active-requests" && !strings.Contains(q.Expression, "[5m]")) {
				t.Errorf("minute-cadence native query does not use its literal five-minute window: %s", q.Expression)
			}
			if strings.Contains(q.Expression, `blueprint=`) || strings.Contains(q.Expression, "k8s_") || strings.Contains(q.Expression, "traces_") {
				t.Errorf("native HTTP query assumes an unshared or backend-generated label/family: %s", q.Expression)
			}
			if panel.ID == "latency" && (!strings.Contains(q.Expression, "_bucket{") || !strings.Contains(q.Expression, "le")) {
				t.Errorf("explicit OTLP histogram is not queried as projected classic buckets: %s", q.Expression)
			}
			if panel.ID == "active-requests" && strings.Contains(q.Expression, "rate(") {
				t.Errorf("non-monotonic active requests were treated as a counter: %s", q.Expression)
			}
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("missing HTTP panel %s", id)
		}
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	latencyUnits := 0
	jsonStrings(doc, func(path, value string) {
		if strings.HasSuffix(path, "/unit") && strings.Contains(path, "latency") && value != "s" {
			t.Errorf("latency unit = %q, want seconds", value)
		}
		if strings.HasSuffix(path, "/unit") && strings.Contains(path, "latency") {
			latencyUnits++
		}
	})
	if latencyUnits == 0 {
		t.Fatal("latency panel has no explicit seconds unit")
	}
}

func TestOTLPNativeCLIIdentityAndDatasourceContract(t *testing.T) {
	legacy := load(t, generated(t, otlpNativeBlueprint))
	alphaDir := generated(t, otlpNativeBlueprint, "-namespace", "alpha")
	alpha := load(t, alphaDir)
	beta := load(t, generated(t, otlpNativeBlueprint, "-namespace", "beta"))
	repeated := readTree(t, generated(t, otlpNativeBlueprint, "-namespace=alpha"))
	for name, raw := range alpha.tree {
		if !bytes.Equal(raw, repeated[name]) {
			t.Errorf("%s differs on repeated CLI generation", name)
		}
		if _, exists := beta.tree[name]; exists {
			t.Errorf("namespaces both write %s", name)
		}
	}
	seen := map[string]bool{}
	for _, e := range []emitted{legacy, alpha, beta} {
		for _, uid := range append(e.dashboardUIDs(), e.folderUID) {
			if seen[uid] {
				t.Errorf("resource UID %q collides", uid)
			}
			seen[uid] = true
		}
		for uid, doc := range e.dashboards {
			jsonStrings(doc, func(path, value string) {
				for _, match := range dashLinkPattern.FindAllStringSubmatch(value, -1) {
					if _, found := e.dashboards[match[1]]; !found {
						t.Errorf("%s %s links outside its generated namespace: %s", uid, path, value)
					}
				}
			})
		}
	}
	var inventory dashgen.PanelInventory
	if err := json.Unmarshal(alpha.tree["otlp-native-alpha-panel-inventory.json"], &inventory); err != nil {
		t.Fatal(err)
	}
	for name, raw := range alpha.tree {
		if !strings.HasSuffix(name, "-http.json") {
			continue
		}
		actual, err := dashgen.InventoryFromDashboard(raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, panel := range actual.Panels {
			found := false
			for _, listed := range inventory.Panels {
				if listed.Dashboard == panel.Dashboard && listed.ID == panel.ID {
					found = reflect.DeepEqual(listed, panel)
					break
				}
			}
			if !found {
				t.Errorf("native panel %s/%s inventory differs from written resource", panel.Dashboard, panel.ID)
			}
		}
	}
	out := t.TempDir()
	res := runCLI(t, "-blueprint", otlpNativeBlueprint, "-out", out, "-datasource", "prometheus=bound-prometheus")
	if res.code != 0 {
		t.Fatalf("explicit datasource generation: %s%s", res.stdout, tail(res.stderr))
	}
	raw, err := os.ReadFile(filepath.Join(out, "otlp-native-http.json"))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := dashgen.InventoryFromDashboard(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, panel := range bound.Panels {
		for _, q := range panel.Queries {
			if q.DatasourceName != "bound-prometheus" {
				t.Errorf("explicit datasource not reflected in %s/%s: %q", panel.ID, q.RefID, q.DatasourceName)
			}
		}
	}
}

func TestOTLPNativeCLIRefusesMissingNativeInstruments(t *testing.T) {
	raw, err := os.ReadFile(otlpNativeBlueprint)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte("metrics: true"), []byte("metrics: false"))
	fixture := filepath.Join(t.TempDir(), "native-disabled.yaml")
	if err := os.WriteFile(fixture, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	res := runCLI(t, dashArgs(fixture, out)...)
	if res.code == 0 || !strings.Contains(res.stderr, "did not emit the required native HTTP instruments") {
		t.Fatalf("disabled native lane was accepted or refused for wrong cause (exit %d): %s", res.code, tail(res.stderr))
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("refused native dashboard left partial output (stat %v)", err)
	}
}
