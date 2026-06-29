// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNavDropdownWithNavLinks verifies that a dashboard rendered after WithNavLinks
// contains the expected link properties in the GA v2 JSON output.
func TestNavDropdownWithNavLinks(t *testing.T) {
	d, err := NewDashboard("links-test-uid", "Links Test")
	if err != nil {
		t.Fatalf("NewDashboard: %v", err)
	}

	WithNavLinks(&d, NavDropdown("X", []string{"acme-ws1"}))

	out, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Parse the rendered JSON to verify structure.
	var top map[string]any
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	s := string(out)

	// Title must be present.
	if !strings.Contains(s, `"X"`) {
		t.Errorf("link title %q not found in rendered JSON: %s", "X", s)
	}

	// asDropdown must be true.
	if !strings.Contains(s, `"asDropdown": true`) {
		t.Errorf(`"asDropdown": true not found in rendered JSON: %s`, s)
	}

	// The tag must appear.
	if !strings.Contains(s, `"acme-ws1"`) {
		t.Errorf(`tag "acme-ws1" not found in rendered JSON: %s`, s)
	}

	// Link type must be "dashboards".
	if !strings.Contains(s, `"type": "dashboards"`) {
		t.Errorf(`"type": "dashboards" not found in rendered JSON: %s`, s)
	}
}

// TestNavDropdownKeepTimeAndNoVars confirms keepTime / includeVars / targetBlank defaults.
func TestNavDropdownKeepTimeAndNoVars(t *testing.T) {
	d, _ := NewDashboard("links-kv-uid", "KV Test")
	WithNavLinks(&d, NavDropdown("Nav", []string{"tag1"}))
	out, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `"keepTime": true`) {
		t.Errorf(`"keepTime": true not found: %s`, s)
	}
	if !strings.Contains(s, `"includeVars": false`) {
		t.Errorf(`"includeVars": false not found: %s`, s)
	}
	if !strings.Contains(s, `"targetBlank": false`) {
		t.Errorf(`"targetBlank": false not found: %s`, s)
	}
}
