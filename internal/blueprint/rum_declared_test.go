// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// cfgNode parses a workload-config YAML string into the mapping *yaml.Node that rumDeclared receives.
func cfgNode(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		t.Fatalf("unexpected yaml shape")
	}
	return doc.Content[0]
}

// TestRumDeclared covers BOTH RUM-declaration styles: the web_service `rum: true` field AND the app
// workload's entry-node `rum_faro` profile (the latter regressed — the app declares RUM via a frontend
// node's profile, not a config flag, so rumDeclared must detect it or the runner never wires the sink).
func TestRumDeclared(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"web_service rum:true", "rum: true\nroutes: [GET /]", true},
		{"web_service rum:false", "rum: false", false},
		{"web_service no rum", "routes: [GET /]", false},
		{"app entry node with rum_faro", `
traffic: {peak_rps: 5}
services:
  - {name: fe, type: frontend, entry: true, profiles: [rum_faro], calls: [be]}
  - {name: be, type: web}
`, true},
		{"app no rum_faro profile", `
services:
  - {name: fe, type: frontend, entry: true, profiles: [scraped_http_server]}
  - {name: be, type: web}
`, false},
		{"app rum_faro on NON-entry node only", `
services:
  - {name: fe, type: frontend, entry: true, profiles: [rum_faro_NOPE]}
  - {name: be, type: web, profiles: [rum_faro]}
`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rumDeclared(cfgNode(t, c.src)); got != c.want {
				t.Errorf("rumDeclared = %v, want %v", got, c.want)
			}
		})
	}
}
