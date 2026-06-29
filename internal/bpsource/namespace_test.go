// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import "testing"

func TestNamespace(t *testing.T) {
	cases := []struct{ ns, name, want string }{
		{"custom", "myapp", "custom/myapp"},
		{"team-a", "fleet", "team-a/fleet"},
		{"Weird NS!", "x", "weird-ns/x"}, // sanitized
		{"", "x", "custom/x"},            // empty ns defaults to custom
	}
	for _, c := range cases {
		if got := Namespace(c.ns, c.name); got != c.want {
			t.Errorf("Namespace(%q,%q)=%q want %q", c.ns, c.name, got, c.want)
		}
	}
}

func TestSanitizeNS(t *testing.T) {
	if got := SanitizeNS("Team A/B"); got != "team-a-b" {
		t.Errorf("SanitizeNS=%q want team-a-b", got)
	}
	if got := SanitizeNS(""); got != "custom" {
		t.Errorf("SanitizeNS('')=%q want custom", got)
	}
}
