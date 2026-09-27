// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"net/url"
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

func TestGenerateInfinityActionMode(t *testing.T) {
	out := t.TempDir()
	err := generate(opts{
		writeBaseURL: "https://control.example.test:8443",
		dsName:       "synthkit-control",
		dsUID:        "synthkit-control-uid",
		actionMode:   actionModeInfinity,
		outDir:       out,
		blueprints:   "../../blueprints",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(out, "synthkit-customer-control.json"))
	if err != nil {
		t.Fatalf("dashboard not written: %v", err)
	}
	var generated any
	if err := json.Unmarshal(b, &generated); err != nil {
		t.Fatalf("decode generated dashboard: %v", err)
	}
	var actions []map[string]any
	collectActions(generated, &actions)
	if len(actions) == 0 {
		t.Fatal("infinity dashboard contains no actions")
	}
	gotPaths := map[string]bool{}
	for i, action := range actions {
		if action["type"] != "infinity" {
			t.Errorf("action %d type = %v, want infinity", i, action["type"])
		}
		options, ok := action["infinity"].(map[string]any)
		if !ok {
			t.Errorf("action %d has no infinity options", i)
			continue
		}
		if options["datasourceUid"] != "synthkit-control-uid" {
			t.Errorf("action %d datasourceUid = %v, want synthkit-control-uid", i, options["datasourceUid"])
		}
		if options["method"] != "POST" {
			t.Errorf("action %d method = %v, want POST", i, options["method"])
		}
		if !hasJSONContentType(options) {
			t.Errorf("action %d lacks a Content-Type: application/json header", i)
		}
		actionURL, ok := options["url"].(string)
		if !ok {
			t.Errorf("action %d url = %v, want an absolute URL", i, options["url"])
			continue
		}
		parsed, err := url.Parse(actionURL)
		if err != nil || parsed.Scheme == "" || parsed.Host != "control.example.test:8443" {
			t.Errorf("action %d URL %q is not absolute with the configured host", i, actionURL)
			continue
		}
		if parsed.Path != "/control/load" && parsed.Path != "/control/scenarios" {
			t.Errorf("action %d path = %q, want /control/load or /control/scenarios", i, parsed.Path)
		}
		gotPaths[parsed.Path] = true
	}
	for _, path := range []string{"/control/load", "/control/scenarios"} {
		if !gotPaths[path] {
			t.Errorf("infinity actions do not include %s", path)
		}
	}
}

func collectActions(value any, out *[]map[string]any) {
	switch value := value.(type) {
	case map[string]any:
		if actionType, ok := value["type"].(string); ok && (actionType == "fetch" || actionType == "infinity") {
			*out = append(*out, value)
		}
		for _, child := range value {
			collectActions(child, out)
		}
	case []any:
		for _, child := range value {
			collectActions(child, out)
		}
	}
}

func hasJSONContentType(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		if headers, ok := value["headers"].([]any); ok {
			for _, header := range headers {
				pair, ok := header.([]any)
				if ok && len(pair) == 2 && pair[0] == "Content-Type" && pair[1] == "application/json" {
					return true
				}
			}
		}
		for _, child := range value {
			if hasJSONContentType(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if hasJSONContentType(child) {
				return true
			}
		}
	}
	return false
}

func TestActionModeValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		o       opts
		wantErr string
	}{
		{"default fetch", opts{}, ""},
		{"explicit fetch", opts{actionMode: actionModeFetch}, ""},
		{"infinity complete", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "http://h"}, ""},
		{"infinity without uid", opts{actionMode: actionModeInfinity, writeBaseURL: "http://h"}, "requires -ds-uid and -write-base-url"},
		{"infinity without URL", opts{actionMode: actionModeInfinity, dsUID: "u"}, "requires -ds-uid and -write-base-url"},
		{"infinity relative URL", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "/control"}, "must be an absolute HTTP(S) URL without query or fragment"},
		{"infinity unsupported URL scheme", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "ftp://h/control"}, "must be an absolute HTTP(S) URL without query or fragment"},
		{"infinity URL query", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https://h/control?tenant=one"}, "must be an absolute HTTP(S) URL without query or fragment"},
		{"infinity empty URL query", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https://h/control?"}, "must be an absolute HTTP(S) URL without query or fragment"},
		{"infinity URL fragment", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https://h/control#section"}, "must be an absolute HTTP(S) URL without query or fragment"},
		{"infinity empty URL fragment", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https://h/control#"}, "must be an absolute HTTP(S) URL without query or fragment"},
		{"unknown mode", opts{actionMode: "browser"}, "must be fetch or infinity"},
	} {
		err := tc.o.validate()
		if (err == nil) != (tc.wantErr == "") || (err != nil && !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: validate() = %v, want error containing %q", tc.name, err, tc.wantErr)
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
