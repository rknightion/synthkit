// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import "testing"

// TestFlattenKey: files directly under the subpath keep their name; nested files flatten "/"→"-"
// so distinct files sharing a base name across sub-directories don't collide in flat git/<id>/.
func TestFlattenKey(t *testing.T) {
	cases := []struct {
		entryPath, prefix, want string
	}{
		{"svc.yaml", "", "svc.yaml"},                      // root, no subpath
		{"blueprints/svc.yaml", "blueprints", "svc.yaml"}, // directly under subpath
		{"a/svc.yaml", "", "a-svc.yaml"},                  // nested under root
		{"b/svc.yaml", "", "b-svc.yaml"},                  // sibling — distinct from a/svc.yaml
		{"bp/a/svc.yaml", "bp", "a-svc.yaml"},             // nested under subpath
		{"bp/b/svc.yaml", "bp", "b-svc.yaml"},             // sibling under subpath — distinct
	}
	for _, c := range cases {
		got := flattenKey(c.entryPath, c.prefix)
		if got != c.want {
			t.Errorf("flattenKey(%q,%q)=%q want %q", c.entryPath, c.prefix, got, c.want)
		}
	}
	// The two same-base nested pairs must produce DISTINCT keys (the bug this fixes).
	if flattenKey("a/svc.yaml", "") == flattenKey("b/svc.yaml", "") {
		t.Fatal("nested same-base files must not collide after flattening")
	}
	if flattenKey("bp/a/svc.yaml", "bp") == flattenKey("bp/b/svc.yaml", "bp") {
		t.Fatal("nested same-base files under a subpath must not collide")
	}
}
