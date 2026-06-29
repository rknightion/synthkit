// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateWritesValidV2Dashboard(t *testing.T) {
	out := t.TempDir()
	err := generate(opts{
		writeBaseURL: "https://host",
		dsName:       "synthkit (Infinity)",
		outDir:       out,
		blueprints:   "../../blueprints",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	path := filepath.Join(out, "synthkit-customer-control.json")
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("dashboard not written: %v", rerr)
	}
	s := string(b)
	for _, want := range []string{
		"dashboard.grafana.app/v2",          // GA v2
		"/control/schema?audience=customer", // customer-projected Infinity read
		"/control/state",                    // live state read
		"/control/load",                     // volume preset POST
		"/control/scenarios",                // scenario activation POST
		"yesoreyeram-infinity-datasource",
		`"type": "actions"`,               // verified Actions-cell firing shape
		`"type": "fetch"`,                 // verified fetch action
		`"root_selector": "scenarios"`,    // incidents read sub-array
		`"selector": "blueprint"`,         // read columns present (incidents)
		`"selector": "volume_multiplier"`, // read columns present (state)
		`{\"active_scenarios\":[]}`,       // Clear all incidents fixed body (JSON-escaped in body string)
	} {
		if !strings.Contains(s, want) {
			t.Errorf("dashboard JSON missing %q", want)
		}
	}
	// Reads must be RELATIVE (resolved against the datasource Base URL), so no plain-http
	// read URL should be baked in. Writes use the https write-base-url, so only https:// is allowed.
	if strings.Contains(s, "http://") {
		t.Errorf("dashboard JSON contains a plain http:// URL — reads must be relative, writes https")
	}
	// No unreliable interpolation, no dead infinity-action shape.
	if strings.Contains(s, "${__data") {
		t.Errorf("dashboard JSON must not use ${__data} interpolation")
	}
	if strings.Contains(s, `"type": "infinity"`) {
		t.Errorf("dashboard JSON must not use type:infinity actions")
	}
	// One Activate button per enumerated scenario (+ Clear all). Verify against the live blueprints.
	scs, lerr := loadScenarios("../../blueprints")
	if lerr != nil {
		t.Fatalf("loadScenarios: %v", lerr)
	}
	for _, sc := range scs {
		body := `{\"active_scenarios\":[\"` + sc.id() + `\"]}`
		if !strings.Contains(s, body) {
			t.Errorf("missing fixed-body activate button for scenario %q (body %q)", sc.id(), body)
		}
	}
}

func TestLoadScenariosEnumeratesBlueprints(t *testing.T) {
	scs, err := loadScenarios("../../blueprints")
	if err != nil {
		t.Fatalf("loadScenarios: %v", err)
	}
	if len(scs) == 0 {
		t.Fatal("expected at least one scenario across blueprints")
	}
	for _, sc := range scs {
		if sc.Blueprint == "" || sc.Name == "" || sc.Title == "" {
			t.Errorf("scenario missing fields: %+v", sc)
		}
		if !strings.Contains(sc.id(), "/") {
			t.Errorf("scenario id must be <bp>/<name>: %q", sc.id())
		}
	}
}

func TestJoinURL(t *testing.T) {
	cases := []struct {
		base, path, want string
	}{
		{"http://host:8088", "/control/state", "http://host:8088/control/state"},
		{"http://host:8088/", "/control/state", "http://host:8088/control/state"},
		{"http://host:8088", "/control/schema?audience=customer", "http://host:8088/control/schema?audience=customer"},
		{"http://host:8088", "control/state", "http://host:8088/control/state"},
		{"", "/control/state", "/control/state"},
	}
	for _, c := range cases {
		got := joinURL(c.base, c.path)
		if got != c.want {
			t.Errorf("joinURL(%q, %q) = %q; want %q", c.base, c.path, got, c.want)
		}
	}
}
