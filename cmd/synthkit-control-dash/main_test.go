// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/dashboard"
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
	// read URL should be baked in. This fixture uses an HTTPS write-base-url.
	if strings.Contains(s, "http://") {
		t.Errorf("dashboard JSON contains a plain http:// URL despite this HTTPS fixture")
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

func TestControlPlaneOmitsUnemittedHTTPLatency(t *testing.T) {
	o := opts{writeBaseURL: "http://control.test", dsName: "control", dsUID: "control", actionMode: actionModeInfinity,
		layout: layoutControlPlane, promUID: "prom", lokiUID: "loki", blueprints: "testdata"}
	o.folder = "training"
	d, err := buildControlPlaneDashboard(o)
	if err != nil {
		t.Fatal(err)
	}
	b, err := dashboard.Render(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `http_server_request_duration_seconds_sum{blueprint=\"control-plane-fixture\",service=\"browser\"}`) ||
		strings.Contains(string(b), `http_server_request_duration_seconds_sum{service=\"browser\"`) {
		t.Fatal("unemitted HTTP server histogram queried for browser")
	}
	for _, want := range []string{`"from": "now-3h"`, `"grafana.app/folder"`, `/control/state`, `/control/readiness`,
		`/control/scenarios/activate`, `/control/scenarios/deactivate`, `"collapse": true`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	scenarios, err := loadScenarios(o.blueprints)
	if err != nil {
		t.Fatal(err)
	}
	var actions []map[string]any
	collectActions(doc, &actions)
	for _, sc := range scenarios {
		body := `{"scenario":"` + sc.id() + `"}`
		for _, path := range []string{"/control/scenarios/activate", "/control/scenarios/deactivate"} {
			found := false
			for _, a := range actions {
				i, ok := a["infinity"].(map[string]any)
				if !ok {
					continue
				}
				url, ok := i["url"].(string)
				if ok && strings.HasSuffix(url, path) && i["body"] == body {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("missing %s action for %s", path, sc.id())
			}
		}
	}
	var exprs []string
	collectExprs(doc, &exprs)
	if len(exprs) == 0 {
		t.Fatal("no impact chart queries")
	}

}

func TestShippedScenarioImpactQueriesUseDerivedFamilies(t *testing.T) {
	scenarios, err := loadScenarios("../../blueprints")
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) == 0 {
		t.Fatal("no shipped scenarios")
	}
	surface, err := deriveImpact(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	metric := regexp.MustCompile(`rate\(([a-zA-Z_][a-zA-Z_0-9]*?)(?:_sum|_count)\{`)
	logSource := regexp.MustCompile(`source="([^"]+)"`)
	seen := map[string]bool{}
	for _, sc := range scenarios {
		m := surface.manifests[sc.Blueprint]
		if m == nil {
			t.Errorf("no derived manifest for %s", sc.Blueprint)
			continue
		}
		for _, target := range sc.Targets {
			key := sc.Blueprint + "\x00" + target
			if seen[key] {
				continue
			}
			seen[key] = true
			q := surface.queries[key]
			if q.latency != "" {
				matches := metric.FindAllStringSubmatch(q.latency, -1)
				if len(matches) != 2 {
					t.Errorf("%s: latency query must use one histogram's sum and count: %s", key, q.latency)
				}
				for _, match := range matches {
					if _, ok := m.Metric(match[1]); !ok {
						t.Errorf("%s: unemitted metric family %s", key, match[1])
					}
				}
			}
			if q.errors != "" {
				match := logSource.FindStringSubmatch(q.errors)
				if len(match) < 2 {
					t.Errorf("%s: log query lacks source: %s", key, q.errors)
					continue
				}
				found := false
				for _, source := range m.LogSources {
					if source.Source == match[1] {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("%s: unemitted log source %s", key, match[1])
				}
			}
		}
	}
}

func collectExprs(v any, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		if e, ok := x["expr"].(string); ok {
			*out = append(*out, e)
		}
		for _, child := range x {
			collectExprs(child, out)
		}
	case []any:
		for _, child := range x {
			collectExprs(child, out)
		}
	}
}

func TestGenerateInfinityActionMode(t *testing.T) {
	out := t.TempDir()
	err := generate(opts{
		writeBaseURL: "https://control.example.test:8443/",
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
		if strings.Contains(parsed.Path, "//control") {
			t.Errorf("action %d URL %q contains //control after a trailing base slash", i, actionURL)
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
		{"infinity complete", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https://h"}, ""},
		{"infinity HTTP URL", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "http://h"}, ""},
		{"infinity HTTPS URL", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https://h"}, ""},
		{"infinity uppercase HTTP scheme", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "HTTP://h"}, ""},
		{"fetch control path", opts{actionMode: actionModeFetch, writeBaseURL: "https://h.example/control"}, "-write-base-url must not end in /control; pass the base URL, synthkit-control-dash appends /control/... itself"},
		{"fetch HTTP control path with slash", opts{actionMode: actionModeFetch, writeBaseURL: "http://h.example/control/"}, "-write-base-url must not end in /control; pass the base URL, synthkit-control-dash appends /control/... itself"},
		{"infinity control path", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https://h.example/control"}, "-write-base-url must not end in /control; pass the base URL, synthkit-control-dash appends /control/... itself"},
		{"infinity HTTP control path with slash", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "http://h.example/control/"}, "-write-base-url must not end in /control; pass the base URL, synthkit-control-dash appends /control/... itself"},
		{"fetch base URL without path", opts{actionMode: actionModeFetch, writeBaseURL: "https://h.example"}, ""},
		{"fetch base URL with slash", opts{actionMode: actionModeFetch, writeBaseURL: "https://h.example/"}, ""},
		{"fetch base URL with application path", opts{actionMode: actionModeFetch, writeBaseURL: "https://h.example/synthkit"}, ""},
		{"fetch relative URL", opts{actionMode: actionModeFetch, writeBaseURL: "synthkit"}, ""},
		{"fetch malformed URL", opts{actionMode: actionModeFetch, writeBaseURL: "https://h.example/%zz"}, "-write-base-url must be a valid URL"},
		{"infinity malformed URL", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https://h.example/%zz"}, "-write-base-url must be an absolute HTTP(S) URL without credentials, query or fragment in infinity mode"},
		{"infinity URL credentials", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https://user:pass@h/control"}, "-write-base-url must be an absolute HTTP(S) URL without credentials, query or fragment in infinity mode"},
		{"infinity URL username and password", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https://user:pw@host"}, "-write-base-url must be an absolute HTTP(S) URL without credentials, query or fragment in infinity mode"},
		{"infinity without uid", opts{actionMode: actionModeInfinity, writeBaseURL: "http://h"}, "requires -ds-uid and -write-base-url"},
		{"infinity without URL", opts{actionMode: actionModeInfinity, dsUID: "u"}, "requires -ds-uid and -write-base-url"},
		{"infinity relative URL", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "/control"}, "-write-base-url must be an absolute HTTP(S) URL without credentials, query or fragment in infinity mode"},
		{"infinity empty host", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https:///control"}, "-write-base-url must be an absolute HTTP(S) URL without credentials, query or fragment in infinity mode"},
		{"infinity unsupported URL scheme", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "ftp://h/control"}, "-write-base-url must be an absolute HTTP(S) URL without credentials, query or fragment in infinity mode"},
		{"infinity URL query", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https://h/control?tenant=one"}, "-write-base-url must be an absolute HTTP(S) URL without credentials, query or fragment in infinity mode"},
		{"infinity empty URL query", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https://h/control?"}, "-write-base-url must be an absolute HTTP(S) URL without credentials, query or fragment in infinity mode"},
		{"infinity URL fragment", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https://h/control#section"}, "-write-base-url must be an absolute HTTP(S) URL without credentials, query or fragment in infinity mode"},
		{"infinity empty URL fragment", opts{actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "https://h/control#"}, "-write-base-url must be an absolute HTTP(S) URL without credentials, query or fragment in infinity mode"},
		{"unknown mode", opts{actionMode: "browser"}, "must be fetch or infinity"},
		{"control-plane complete", opts{layout: layoutControlPlane, actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "http://h", promUID: "p", lokiUID: "l"}, ""},
		{"control-plane needs infinity", opts{layout: layoutControlPlane, promUID: "p", lokiUID: "l"}, "requires -action-mode infinity"},
		{"control-plane needs prom", opts{layout: layoutControlPlane, actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "http://h", lokiUID: "l"}, "requires -prom-uid and -loki-uid"},
		{"control-plane needs loki", opts{layout: layoutControlPlane, actionMode: actionModeInfinity, dsUID: "u", writeBaseURL: "http://h", promUID: "p"}, "requires -prom-uid and -loki-uid"},
		{"unknown layout", opts{layout: "grid"}, "-layout must be customer or control-plane"},
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
		{"http://host:8088//", "/control/state", "http://host:8088//control/state"},
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
