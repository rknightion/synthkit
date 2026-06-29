// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderProducesV2APIVersion(t *testing.T) {
	d, err := NewDashboard("test-uid", "Test")
	if err != nil {
		t.Fatalf("NewDashboard: %v", err)
	}
	out, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var top map[string]any
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	api, _ := top["apiVersion"].(string)
	if !strings.HasPrefix(api, "dashboard.grafana.app/v2") {
		t.Errorf("apiVersion = %q, want dashboard.grafana.app/v2*", api)
	}
}

// A Folder UID is stamped as the grafana.app/folder metadata annotation (and the name is preserved).
func TestRenderFolderAnnotation(t *testing.T) {
	d, _ := NewDashboard("test-uid", "Test")
	d.Folder = "acme-ws1"
	out, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	for _, want := range []string{`"grafana.app/folder": "acme-ws1"`, `"name": "test-uid"`} {
		if !strings.Contains(s, want) {
			t.Errorf("folder render missing %q: %s", want, s)
		}
	}
	// Without a folder, no folder annotation appears.
	d2, _ := NewDashboard("u2", "T2")
	out2, _ := Render(d2)
	if strings.Contains(string(out2), "grafana.app/folder") {
		t.Errorf("no folder set, but folder annotation present")
	}
}
