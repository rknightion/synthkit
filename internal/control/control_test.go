// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/rknightion/synthkit/internal/pushstatus"
)

func TestStateRoundTripsZeroValues(t *testing.T) {
	// I24: zero/false knob values MUST survive marshal→unmarshal (no omitempty).
	s := State{
		VolumeMultiplier:   0, // deliberately zero
		DisabledBlueprints: []string{},
		Failures: map[string]FailureSetting{
			"latency_spike": {Enabled: false, Intensity: 0, Scope: ""},
		},
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"volume_multiplier", "disabled_blueprints", "enabled", "intensity", "scope"} {
		if !bytes.Contains(b, []byte(`"`+key+`"`)) {
			t.Fatalf("zero-valued knob %q dropped from JSON (omitempty bug, I24): %s", key, b)
		}
	}
	var back State
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, back) {
		t.Fatalf("round trip mutated state:\n%+v\n%+v", s, back)
	}
}

func TestStateRoundTripIncludesNewFieldsZeroValued(t *testing.T) {
	s := DefaultState()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// I24: zero/empty values must serialise (no omitempty).
	for _, key := range []string{`"active_scenarios"`, `"scaling"`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("missing %s in serialised default state:\n%s", key, data)
		}
	}
	var back State
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.ActiveScenarios == nil || back.Scaling == nil {
		t.Errorf("nil after round-trip: scenarios=%v scaling=%v", back.ActiveScenarios, back.Scaling)
	}
}

func TestDefaultStateIncludesConstructAndKindKeys(t *testing.T) {
	s := DefaultState()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// I24: zero/empty values must serialise (no omitempty).
	for _, key := range []string{`"disabled_constructs"`, `"disabled_kinds"`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("missing %s in serialised default state:\n%s", key, data)
		}
	}
	var back State
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.DisabledConstructs == nil || back.DisabledKinds == nil {
		t.Errorf("nil after round-trip: constructs=%v kinds=%v", back.DisabledConstructs, back.DisabledKinds)
	}
}

func TestSpanMetricsEnabledOptIn(t *testing.T) {
	s := DefaultState()
	if s.SpanMetricsEnabled("bp-a") {
		t.Fatal("default OFF: no blueprint should be span-metrics-enabled")
	}
	s.SpanMetricsBlueprints = []string{"bp-a"}
	if !s.SpanMetricsEnabled("bp-a") {
		t.Fatal("bp-a opted in must be enabled")
	}
	if s.SpanMetricsEnabled("bp-b") {
		t.Fatal("bp-b not opted in must be disabled")
	}
}

func TestSpanMetricsBlueprintsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json") // NewStore takes a FILE path, not a dir (M6)
	store := NewStore(path)                          // single return value (no error)
	store.Update(func(s *State) { s.SpanMetricsBlueprints = []string{"bp-x"} })
	reopened := NewStore(path)
	if !reopened.Snapshot().SpanMetricsEnabled("bp-x") {
		t.Fatal("SpanMetricsBlueprints must persist + round-trip (I24)")
	}
}

func TestConstructKeyFormat(t *testing.T) {
	if got := ConstructKey("initech", "rds", "newco-db"); got != "initech/rds:newco-db" {
		t.Fatalf("ConstructKey = %q, want initech/rds:newco-db", got)
	}
}

func TestValidateConstructs(t *testing.T) {
	schema := Schema{Constructs: []ConstructInfo{{Blueprint: "initech", Kind: "rds", Name: "newco-db", Enabled: true}}}
	if err := validateConstructs([]string{"initech/rds:newco-db"}, schema); err != nil {
		t.Fatalf("known construct rejected: %v", err)
	}
	if err := validateConstructs([]string{"initech/rds:ghost"}, schema); err == nil {
		t.Fatalf("unknown construct must be rejected")
	}
}

func TestValidateKinds(t *testing.T) {
	schema := Schema{Kinds: []string{"rds", "cloudflare"}}
	if err := validateKinds([]string{"rds"}, schema); err != nil {
		t.Fatalf("known kind rejected: %v", err)
	}
	if err := validateKinds([]string{"ghost"}, schema); err == nil {
		t.Fatalf("unknown kind must be rejected")
	}
}

func TestStorePersistsAtomicallyAndReloads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control-state.json")
	st := NewStore(path)
	st.Update(func(s *State) {
		s.VolumeMultiplier = 2.5
		s.SetFailure("error_burst", FailureSetting{Enabled: true, Intensity: 0.7, Scope: "api"})
	})
	// A fresh store from the same path sees the persisted state.
	st2 := NewStore(path)
	got := st2.Snapshot()
	if got.VolumeMultiplier != 2.5 {
		t.Fatalf("persisted volume lost: %+v", got)
	}
	if f := got.Failures["error_burst"]; !f.Enabled || f.Intensity != 0.7 || f.Scope != "api" {
		t.Fatalf("persisted failure lost: %+v", got.Failures)
	}
	// No stray temp files left behind (atomic rename).
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("non-atomic save left temp files: %v", entries)
	}
}

func TestStoreDefaults(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	s := st.Snapshot()
	if s.VolumeMultiplier != 1.0 {
		t.Fatalf("default volume = %v, want 1.0", s.VolumeMultiplier)
	}
	if s.Failures == nil || s.DisabledBlueprints == nil {
		t.Fatalf("maps/slices must be non-nil for round-trip stability: %+v", s)
	}
}

func TestHandlerStateAndApply(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	var applied []State
	h := NewHandler(st, func(s State) { applied = append(applied, s) }, "")

	// GET /control/state is side-effect-free.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/control/state", nil))
	if rec.Code != 200 || len(applied) != 0 {
		t.Fatalf("GET state: code=%d applied=%d", rec.Code, len(applied))
	}

	// POST /control/load applies + persists.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/load", strings.NewReader(`{"volume_multiplier": 3}`)))
	if rec.Code != 200 {
		t.Fatalf("POST load: %d %s", rec.Code, rec.Body)
	}
	if len(applied) != 1 || applied[0].VolumeMultiplier != 3 {
		t.Fatalf("apply hook: %+v", applied)
	}

	// POST /control/failures merges by mode.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/failures", strings.NewReader(`{"latency_spike": {"enabled": true, "intensity": 0.5, "scope": "api"}}`)))
	if rec.Code != 200 {
		t.Fatalf("POST failures: %d %s", rec.Code, rec.Body)
	}
	if f := st.Snapshot().Failures["latency_spike"]; !f.Enabled || f.Intensity != 0.5 {
		t.Fatalf("failure not stored: %+v", st.Snapshot().Failures)
	}

	// POST /control/reset returns to defaults.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/reset", nil))
	if rec.Code != 200 || st.Snapshot().VolumeMultiplier != 1.0 || len(st.Snapshot().Failures) != 0 {
		t.Fatalf("reset: %+v", st.Snapshot())
	}
}

func TestHandlerSchemaListsKnobs(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	h := NewHandler(st, nil, "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/control/schema", nil))
	body := rec.Body.String()
	for _, k := range []string{"volume_multiplier", "failures", "disabled_blueprints"} {
		if !strings.Contains(body, k) {
			t.Fatalf("schema missing knob %q: %s", k, body)
		}
	}
}

func TestCORSEchoesRequestHeaders(t *testing.T) {
	// I26: a fixed allow-list rejects Grafana's x-grafana-device-id — echo instead.
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	h := NewHandler(st, nil, "")
	req := httptest.NewRequest("OPTIONS", "/control/state", nil)
	req.Header.Set("Origin", "https://g.example.net")
	req.Header.Set("Access-Control-Request-Headers", "content-type, x-grafana-device-id")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "content-type, x-grafana-device-id" {
		t.Fatalf("CORS must echo requested headers, got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://g.example.net" {
		t.Fatalf("CORS must echo origin, got %q", got)
	}
}

func TestGETNeverMutates(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	h := NewHandler(st, nil, "")
	before := st.Snapshot()
	for _, p := range []string{"/control/state", "/control/schema", "/control/ui"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code >= 500 {
			t.Fatalf("GET %s: %d", p, rec.Code)
		}
	}
	if !reflect.DeepEqual(before, st.Snapshot()) {
		t.Fatalf("a GET mutated state")
	}
}

func TestPOSTUnknownFieldRejected(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	h := NewHandler(st, nil, "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/load", strings.NewReader(`{"volume_multiplyer": 3}`)))
	if rec.Code != 400 {
		t.Fatalf("typo'd knob must 400, got %d %s", rec.Code, rec.Body)
	}
}

func handlerVar(h http.Handler) http.Handler { return h } // keep http import honest

// fakeSchemaSource serves a fixed derived schema (one scenario t/db_storm, one scalable workload
// target api [1,50] default 2) for the dynamic-schema + scenario/scaling handler tests.
type fakeSchemaSource struct {
	schema  Schema
	sources map[string]string // blueprint name → raw YAML (for BlueprintSource)
}

func (f fakeSchemaSource) ControlSchema() Schema { return f.schema }

func (f fakeSchemaSource) BlueprintSource(name string) (string, bool) {
	s, ok := f.sources[name]
	return s, ok
}

func testSchema() Schema {
	return Schema{
		Blueprints: []string{"t"},
		Modes:      []ModeInfo{{Name: "connection_saturation", Axis: "database", Help: "x"}},
		Targets: []TargetInfo{
			{Blueprint: "t", Name: "api", Axis: "workload", Scalable: &ScalableInfo{Dimension: "replicas", Min: 1, Max: 50, Default: 2, Current: 2}},
			{Blueprint: "t", Name: "app-db", Axis: "database"},
		},
		Scenarios: []ScenarioInfo{{Blueprint: "t", Name: "db_storm", Title: "DB storm"}},
		Constructs: []ConstructInfo{
			{Blueprint: "t", Kind: "rds", Name: "app-db", Enabled: true},
		},
		Kinds: []string{"rds"},
	}
}

// TestHandlerConstructsEndpoint covers POST /control/constructs: valid key → 200 + replace;
// unknown key → 400; absent SchemaSource → 400.
func TestHandlerConstructsEndpoint(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	h := NewHandler(st, nil, "", fakeSchemaSource{schema: testSchema()})

	// Unknown construct key → 400.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/constructs", strings.NewReader(`{"disabled_constructs":["t/rds:ghost"]}`)))
	if rec.Code != 400 {
		t.Fatalf("unknown construct must 400, got %d %s", rec.Code, rec.Body)
	}

	// Valid key → 200 + persisted (replace).
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/constructs", strings.NewReader(`{"disabled_constructs":["t/rds:app-db"]}`)))
	if rec.Code != 200 {
		t.Fatalf("valid construct must 200, got %d %s", rec.Code, rec.Body)
	}
	if got := st.Snapshot().DisabledConstructs; len(got) != 1 || got[0] != "t/rds:app-db" {
		t.Fatalf("disabled construct not persisted: %v", got)
	}

	// No SchemaSource → 400.
	hNo := NewHandler(st, nil, "")
	rec = httptest.NewRecorder()
	hNo.ServeHTTP(rec, httptest.NewRequest("POST", "/control/constructs", strings.NewReader(`{"disabled_constructs":["t/rds:app-db"]}`)))
	if rec.Code != 400 {
		t.Fatalf("constructs without schema source must 400, got %d", rec.Code)
	}
}

// TestHandlerKindsEndpoint covers POST /control/kinds: valid kind → 200; unknown → 400;
// absent SchemaSource → 400.
func TestHandlerKindsEndpoint(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	h := NewHandler(st, nil, "", fakeSchemaSource{schema: testSchema()})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/kinds", strings.NewReader(`{"disabled_kinds":["ghost"]}`)))
	if rec.Code != 400 {
		t.Fatalf("unknown kind must 400, got %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/kinds", strings.NewReader(`{"disabled_kinds":["rds"]}`)))
	if rec.Code != 200 {
		t.Fatalf("valid kind must 200, got %d %s", rec.Code, rec.Body)
	}
	if got := st.Snapshot().DisabledKinds; len(got) != 1 || got[0] != "rds" {
		t.Fatalf("disabled kind not persisted: %v", got)
	}

	hNo := NewHandler(st, nil, "")
	rec = httptest.NewRecorder()
	hNo.ServeHTTP(rec, httptest.NewRequest("POST", "/control/kinds", strings.NewReader(`{"disabled_kinds":["rds"]}`)))
	if rec.Code != 400 {
		t.Fatalf("kinds without schema source must 400, got %d", rec.Code)
	}
}

// TestHandlerBlueprintSourceEndpoint covers GET /control/blueprint: known → 200 + YAML body;
// unknown → 404; no source provider → 404.
func TestHandlerBlueprintSourceEndpoint(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	src := fakeSchemaSource{schema: testSchema(), sources: map[string]string{"t": "name: t\n"}}
	h := NewHandler(st, nil, "", src)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/control/blueprint?blueprint=t", nil))
	if rec.Code != 200 {
		t.Fatalf("known blueprint must 200, got %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "name: t") {
		t.Fatalf("body missing YAML: %s", rec.Body)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/control/blueprint?blueprint=ghost", nil))
	if rec.Code != 404 {
		t.Fatalf("unknown blueprint must 404, got %d", rec.Code)
	}

	// No source provider (SchemaSource that is not a BlueprintSourcer not wired at all).
	hNo := NewHandler(st, nil, "")
	rec = httptest.NewRecorder()
	hNo.ServeHTTP(rec, httptest.NewRequest("GET", "/control/blueprint?blueprint=t", nil))
	if rec.Code != 404 {
		t.Fatalf("no source provider must 404, got %d", rec.Code)
	}
}

// TestHandlerSchemaDynamicWhenSourceSet proves GET /control/schema marshals the SchemaSource's
// derived schema (modes/targets/scenarios) when one is wired, and falls back to Descriptors() when
// none is.
func TestHandlerSchemaDynamicWhenSourceSet(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	h := NewHandler(st, nil, "", fakeSchemaSource{schema: testSchema()})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/control/schema", nil))
	body := rec.Body.String()
	for _, want := range []string{"db_storm", "connection_saturation", `"api"`, "scenarios", "targets"} {
		if !strings.Contains(body, want) {
			t.Fatalf("dynamic schema missing %q: %s", want, body)
		}
	}

	// No source → static descriptor catalogue.
	hStatic := NewHandler(st, nil, "")
	rec = httptest.NewRecorder()
	hStatic.ServeHTTP(rec, httptest.NewRequest("GET", "/control/schema", nil))
	if b := rec.Body.String(); !strings.Contains(b, "volume_multiplier") || strings.Contains(b, "db_storm") {
		t.Fatalf("static fallback schema wrong: %s", b)
	}
}

// TestHandlerScenariosEndpoint covers POST /control/scenarios: unknown id → 400; known id → 200 +
// persisted; absent SchemaSource → 400.
func TestHandlerScenariosEndpoint(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	h := NewHandler(st, nil, "", fakeSchemaSource{schema: testSchema()})

	// Unknown scenario id → 400.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/scenarios", strings.NewReader(`{"active_scenarios":["t/ghost"]}`)))
	if rec.Code != 400 {
		t.Fatalf("unknown scenario must 400, got %d %s", rec.Code, rec.Body)
	}

	// Known id → 200 + persisted.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/scenarios", strings.NewReader(`{"active_scenarios":["t/db_storm"]}`)))
	if rec.Code != 200 {
		t.Fatalf("known scenario must 200, got %d %s", rec.Code, rec.Body)
	}
	if got := st.Snapshot().ActiveScenarios; len(got) != 1 || got[0] != "t/db_storm" {
		t.Fatalf("scenario not persisted: %v", got)
	}

	// No SchemaSource → endpoint unavailable (400).
	hNo := NewHandler(st, nil, "")
	rec = httptest.NewRecorder()
	hNo.ServeHTTP(rec, httptest.NewRequest("POST", "/control/scenarios", strings.NewReader(`{"active_scenarios":["t/db_storm"]}`)))
	if rec.Code != 400 {
		t.Fatalf("scenarios without schema source must 400, got %d", rec.Code)
	}
}

// TestHandlerScalingEndpoint covers POST /control/scaling: non-scalable target → 400; out-of-bounds
// → 400; in-bounds → 200 + merged into state.
func TestHandlerScalingEndpoint(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	h := NewHandler(st, nil, "", fakeSchemaSource{schema: testSchema()})

	// Scaling targets are keyed by qualified "blueprint/name" id (matches the scenario id form).

	// Non-scalable target → 400.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/scaling", strings.NewReader(`{"t/app-db":3}`)))
	if rec.Code != 400 {
		t.Fatalf("non-scalable target must 400, got %d %s", rec.Code, rec.Body)
	}

	// Bare (unqualified) name no longer matches a target → 400.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/scaling", strings.NewReader(`{"api":12}`)))
	if rec.Code != 400 {
		t.Fatalf("bare unqualified name must 400, got %d %s", rec.Code, rec.Body)
	}

	// Out-of-bounds count → 400.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/scaling", strings.NewReader(`{"t/api":99}`)))
	if rec.Code != 400 {
		t.Fatalf("out-of-bounds count must 400, got %d %s", rec.Code, rec.Body)
	}

	// In-bounds → 200 + merged.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/scaling", strings.NewReader(`{"t/api":12}`)))
	if rec.Code != 200 {
		t.Fatalf("in-bounds count must 200, got %d %s", rec.Code, rec.Body)
	}
	if got := st.Snapshot().Scaling["t/api"]; got != 12 {
		t.Fatalf("scaling not persisted: %v", st.Snapshot().Scaling)
	}
}

// TestHandlerFailuresWarnsNotRejects proves the failures POST accepts an unknown mode/target (the
// escape hatch) rather than rejecting it, even with a SchemaSource wired.
func TestHandlerFailuresWarnsNotRejects(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	h := NewHandler(st, nil, "", fakeSchemaSource{schema: testSchema()})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/failures",
		strings.NewReader(`{"never_heard_of_it":{"enabled":true,"intensity":0.5,"scope":"nope"}}`)))
	if rec.Code != 200 {
		t.Fatalf("unknown failure mode must be accepted (warned, not rejected), got %d %s", rec.Code, rec.Body)
	}
	if f := st.Snapshot().Failures["never_heard_of_it"]; !f.Enabled {
		t.Fatalf("failure not stored: %+v", st.Snapshot().Failures)
	}
}

// basicHeader builds an Authorization: Basic header for user:pass.
func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// TestRequireToken covers all requireToken cases: HTTP Basic auth enforced on POST
// (fixed username "control", password = CONTROL_TOKEN), GET always open, and a
// WWW-Authenticate challenge on 401 so the browser pops its native credential dialog.
func TestRequireToken(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	const tok = "s3cret"
	h := NewHandler(st, nil, tok)

	// GET /control/state is always open regardless of token.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/control/state", nil))
	if rec.Code != 200 {
		t.Fatalf("GET without token must be 200, got %d", rec.Code)
	}

	// POST without Authorization → 401, and MUST carry a Basic challenge so Chrome prompts.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/load", strings.NewReader(`{"volume_multiplier": 2}`)))
	if rec.Code != 401 {
		t.Fatalf("POST without token must be 401, got %d", rec.Code)
	}
	if ch := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(ch, "Basic ") {
		t.Fatalf("401 must carry a Basic WWW-Authenticate challenge, got %q", ch)
	}

	// POST with the old Bearer scheme → 401 (no longer accepted).
	req := httptest.NewRequest("POST", "/control/load", strings.NewReader(`{"volume_multiplier": 2}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("POST with Bearer scheme must be 401, got %d", rec.Code)
	}

	// POST with correct user:password via Basic → not 401.
	req = httptest.NewRequest("POST", "/control/load", strings.NewReader(`{"volume_multiplier": 2}`))
	req.Header.Set("Authorization", basicHeader("control", tok))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == 401 {
		t.Fatalf("POST with correct Basic creds must not be 401, got %d", rec.Code)
	}

	// POST with correct password but wrong username → 401 (fixed username enforced).
	req = httptest.NewRequest("POST", "/control/load", strings.NewReader(`{"volume_multiplier": 2}`))
	req.Header.Set("Authorization", basicHeader("admin", tok))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("POST with wrong username must be 401, got %d", rec.Code)
	}

	// POST with wrong password → 401.
	req = httptest.NewRequest("POST", "/control/load", strings.NewReader(`{"volume_multiplier": 2}`))
	req.Header.Set("Authorization", basicHeader("control", "wrongpass"))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("POST with wrong password must be 401, got %d", rec.Code)
	}

	// token=="" → POST passes without any Authorization header.
	hOpen := NewHandler(st, nil, "")
	rec = httptest.NewRecorder()
	hOpen.ServeHTTP(rec, httptest.NewRequest("POST", "/control/reset", nil))
	if rec.Code == 401 {
		t.Fatalf("token=empty: POST must not be 401, got %d", rec.Code)
	}
}

// TestCORSAllowMethodsDropsPost verifies OPTIONS preflight returns methods without POST.
func TestCORSAllowMethodsDropsPost(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	h := NewHandler(st, nil, "")
	req := httptest.NewRequest("OPTIONS", "/control/state", nil)
	req.Header.Set("Origin", "https://g.example.net")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	methods := rec.Header().Get("Access-Control-Allow-Methods")
	if strings.Contains(methods, "POST") {
		t.Fatalf("Allow-Methods must not include POST, got %q", methods)
	}
	if !strings.Contains(methods, "GET") || !strings.Contains(methods, "OPTIONS") {
		t.Fatalf("Allow-Methods must include GET and OPTIONS, got %q", methods)
	}
}

// TestDecodeStrictBodyLimit verifies a payload larger than maxBodyBytes is rejected with 400.
func TestDecodeStrictBodyLimit(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	h := NewHandler(st, nil, "")
	// Build a JSON object larger than 1 MiB.
	big := bytes.Repeat([]byte("x"), maxBodyBytes+1)
	body := fmt.Sprintf(`{"volume_multiplier": %s}`, big)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/load", strings.NewReader(body)))
	if rec.Code != 400 {
		t.Fatalf("oversized body must be 400, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestFailuresCardinalityCap verifies filling to maxFailureModes succeeds but one more → 400.
func TestFailuresCardinalityCap(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	h := NewHandler(st, nil, "")

	// Fill to cap via individual POSTs (each POST merges, so cap accumulates across calls).
	// Build a single body with exactly maxFailureModes keys.
	body := make(map[string]FailureSetting, maxFailureModes)
	for i := 0; i < maxFailureModes; i++ {
		body[fmt.Sprintf("mode_%d", i)] = FailureSetting{Enabled: true, Intensity: 0.1}
	}
	b, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/failures", bytes.NewReader(b)))
	if rec.Code != 200 {
		t.Fatalf("exactly %d failure modes must succeed: %d %s", maxFailureModes, rec.Code, rec.Body)
	}

	// One more distinct key → 400.
	over := map[string]FailureSetting{"mode_overflow": {Enabled: true, Intensity: 0.1}}
	b2, _ := json.Marshal(over)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("POST", "/control/failures", bytes.NewReader(b2)))
	if rec2.Code != 400 {
		t.Fatalf("exceeding %d failure modes must be 400, got %d %s", maxFailureModes, rec2.Code, rec2.Body)
	}
}

// TestConcurrentUpdatesNoLostWrite verifies persist-inside-mutex: under concurrent Updates the
// persisted file always reflects an in-memory snapshot (no torn write). Run with -race.
func TestConcurrentUpdatesNoLostWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st := NewStore(path)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		v := float64(i) + 1
		go func() {
			defer wg.Done()
			st.Update(func(s *State) { s.VolumeMultiplier = v })
		}()
	}
	wg.Wait()

	// In-memory snapshot must match what's on disk.
	mem := st.Snapshot()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var disk State
	if err := json.Unmarshal(data, &disk); err != nil {
		t.Fatal(err)
	}
	if mem.VolumeMultiplier != disk.VolumeMultiplier {
		t.Fatalf("in-memory (%v) != persisted (%v) after concurrent Updates", mem.VolumeMultiplier, disk.VolumeMultiplier)
	}
}

func TestPersistHealthRecordsErrorOnUnwritablePath(t *testing.T) {
	// A path whose parent dir does not exist forces CreateTemp to fail.
	st := NewStore(filepath.Join(t.TempDir(), "nope", "control-state.json"))
	st.Update(func(s *State) { s.VolumeMultiplier = 2 })
	h := st.PersistHealth()
	if h.LastError == "" || h.LastErrorMs == 0 {
		t.Fatalf("expected persist error recorded, got %+v", h)
	}
	if h.LastOKMs != 0 {
		t.Fatalf("expected no successful persist, got %+v", h)
	}
}

func TestPersistHealthRecordsSuccess(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "control-state.json"))
	st.Update(func(s *State) { s.VolumeMultiplier = 3 })
	h := st.PersistHealth()
	if h.LastOKMs == 0 || h.LastError != "" {
		t.Fatalf("expected clean persist, got %+v", h)
	}
}

func TestStatusEndpoint(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "control-state.json"))
	store.Update(func(s *State) {}) // force one successful persist
	h := NewHandler(store, nil, "").SetStatus(StatusSources{
		Sinks: func() []pushstatus.SinkStat {
			return []pushstatus.SinkStat{{Sink: "promrw", LastSuccessMs: 1700, Pushes: 5}}
		},
		DryRun: true,
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/control/status", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var got StatusReport
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sinks) != 1 || got.Sinks[0].Sink != "promrw" || !got.DryRun {
		t.Fatalf("bad report: %+v", got)
	}
	if got.Persist.LastOKMs == 0 {
		t.Fatalf("persist health missing: %+v", got.Persist)
	}
}

func TestStatusEndpointIsUnguarded(t *testing.T) {
	// GET status carries no mutation, so it must work without a token even when one is set.
	store := NewStore(filepath.Join(t.TempDir(), "control-state.json"))
	h := NewHandler(store, nil, "secret").SetStatus(StatusSources{Sinks: func() []pushstatus.SinkStat { return nil }})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/control/status", nil))
	if rec.Code != 200 {
		t.Fatalf("status guarded unexpectedly: %d", rec.Code)
	}
}

// TestChangeObserverFires asserts the SetChangeObserver audit hook fires once per successful
// mutation with the new snapshot, and not at all on side-effect-free GETs — the seam main.go uses
// to emit self-obs config_change events.
func TestChangeObserverFires(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "x.json"))
	var changes []State
	h := NewHandler(st, nil, "").SetChangeObserver(func(s State) { changes = append(changes, s) })

	// GET must not fire the observer.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/control/state", nil))
	if len(changes) != 0 {
		t.Fatalf("GET fired change observer %d times", len(changes))
	}

	// POST /control/load fires once with the new volume.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/load", strings.NewReader(`{"volume_multiplier": 2.5}`)))
	if rec.Code != 200 {
		t.Fatalf("POST load: %d %s", rec.Code, rec.Body)
	}
	if len(changes) != 1 || changes[0].VolumeMultiplier != 2.5 {
		t.Fatalf("change observer: %+v", changes)
	}
}

func TestRuntimeIncidentsRoundTrip(t *testing.T) {
	in := DefaultState()
	if in.RuntimeIncidents == nil {
		t.Fatal("DefaultState().RuntimeIncidents must be non-nil (empty slice), got nil")
	}
	in.RuntimeIncidents = append(in.RuntimeIncidents, RuntimeIncident{
		ID: "rt-1", Blueprint: "starter", Mode: "latency_spike",
		Target: "starter-api", At: "15:04", For: "30m", Intensity: 0.8,
	})
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out State
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.RuntimeIncidents) != 1 || out.RuntimeIncidents[0] != in.RuntimeIncidents[0] {
		t.Fatalf("round-trip mismatch: got %+v", out.RuntimeIncidents)
	}
	// I24: a zero-intensity entry must survive (no omitempty drops it).
	if !strings.Contains(string(data), `"runtime_incidents"`) {
		t.Fatalf("runtime_incidents key missing from JSON: %s", data)
	}
}

// TestBlueprintSourcesRoundTrip asserts that BlueprintSources persists through a Store
// save/load, and that an absent field defaults to a non-nil empty slice (I24).
func TestBlueprintSourcesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewStore(path)

	// DefaultState must initialise to a non-nil empty slice.
	if s := store.Snapshot(); s.BlueprintSources == nil {
		t.Fatal("DefaultState().BlueprintSources must be non-nil []SourceView{}, got nil")
	}

	src := SourceView{
		ID:          "my-repo",
		Name:        "My Repo",
		Namespace:   "myns",
		URL:         "https://github.com/example/repo",
		Ref:         "refs/heads/main",
		Subpath:     "blueprints",
		TokenEnvVar: "MY_GH_TOKEN",
		LastSHA:     "abc123",
		LastFetchMs: 1718700000000,
		LastErr:     "",
	}
	store.Update(func(s *State) { s.BlueprintSources = []SourceView{src} })

	// Reload from disk.
	reopened := NewStore(path)
	got := reopened.Snapshot().BlueprintSources
	if len(got) != 1 {
		t.Fatalf("expected 1 source after reload, got %d: %+v", len(got), got)
	}
	if got[0] != src {
		t.Fatalf("source mismatch after round-trip:\n  got  %+v\n  want %+v", got[0], src)
	}

	// A snapshot without blueprint_sources field must default to non-nil [].
	// Write a JSON snapshot that omits the field entirely.
	bare := `{"volume_multiplier":1.0,"disabled_blueprints":[],"failures":{},"active_scenarios":[],"scaling":{},"disabled_constructs":[],"disabled_kinds":[],"span_metrics_blueprints":[],"runtime_incidents":[]}`
	barePath := filepath.Join(t.TempDir(), "bare.json")
	if err := os.WriteFile(barePath, []byte(bare), 0o600); err != nil {
		t.Fatal(err)
	}
	storeFromBare := NewStore(barePath)
	if s := storeFromBare.Snapshot(); s.BlueprintSources == nil {
		t.Fatal("absent blueprint_sources must default to non-nil []SourceView{}, got nil")
	}
	if len(storeFromBare.Snapshot().BlueprintSources) != 0 {
		t.Fatalf("absent blueprint_sources must default to empty slice, got %v", storeFromBare.Snapshot().BlueprintSources)
	}
}

func TestScheduleSpec(t *testing.T) {
	cases := []struct {
		mode, target, at, forD string
		intensity              float64
		want                   string
	}{
		{"latency_spike", "starter-api", "15:04", "30m", 0.8, "latency_spike@15:04/30m#0.8@starter-api"},
		{"error_burst", "", "2026-06-22T15:00", "45m", 0, "error_burst@2026-06-22T15:00/45m"},
		{"latency_spike", "prod", "every10m", "2m", 1, "latency_spike@every10m/2m#1@prod"},
	}
	for _, c := range cases {
		if got := ScheduleSpec(c.mode, c.target, c.at, c.forD, c.intensity); got != c.want {
			t.Errorf("ScheduleSpec(%q,%q,%q,%q,%v) = %q, want %q", c.mode, c.target, c.at, c.forD, c.intensity, got, c.want)
		}
	}
}

func TestCloneStateRuntimeIncidentsIndependent(t *testing.T) {
	s := DefaultState()
	s.RuntimeIncidents = append(s.RuntimeIncidents, RuntimeIncident{ID: "rt-1", Mode: "x"})
	c := cloneState(s)
	c.RuntimeIncidents[0].Mode = "mutated"
	if s.RuntimeIncidents[0].Mode != "x" {
		t.Fatal("cloneState must deep-copy RuntimeIncidents; original was mutated")
	}
}
