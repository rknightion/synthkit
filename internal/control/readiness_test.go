// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/pushstatus"
)

func TestEvaluateReadinessFreshLaneIsRedUntilFirstSuccess(t *testing.T) {
	got := EvaluateReadiness(ReadinessInput{
		ProcessRunning:       true,
		HTTPServing:          true,
		Blueprints:           BlueprintReadiness{Loaded: 2, Skipped: 1, Active: 2},
		PersistedState:       PersistedStateReadiness{Writable: true},
		LiveDeliveryExpected: true,
		RequiredLanes:        []string{"promrw"},
		Lanes: []pushstatus.LaneStatus{{
			Name: "promrw", Configured: true, State: pushstatus.LaneNotAttempted,
		}},
	})
	if got.Ready || got.LiveReady {
		t.Fatalf("fresh lane must not make readiness green: %+v", got)
	}
	if got.Blueprints.Loaded != 2 || got.Blueprints.Skipped != 1 || got.Blueprints.Active != 2 {
		t.Fatalf("blueprint counts lost: %+v", got.Blueprints)
	}
	if !containsReason(got.Reasons, "promrw") || !containsReason(got.Reasons, "not_attempted") {
		t.Fatalf("expected first-attempt reason, got %v", got.Reasons)
	}
}

func TestEvaluateReadinessBecomesGreenAfterAllLiveLanesSucceed(t *testing.T) {
	got := EvaluateReadiness(ReadinessInput{
		ProcessRunning:       true,
		HTTPServing:          true,
		Blueprints:           BlueprintReadiness{Loaded: 1, Active: 1},
		PersistedState:       PersistedStateReadiness{Writable: true},
		LiveDeliveryExpected: true,
		RequiredLanes:        []string{"loki", "promrw"},
		Lanes: []pushstatus.LaneStatus{
			{Name: "promrw", Configured: true, State: pushstatus.LaneSuccess, LiveReady: true},
			{Name: "loki", Configured: true, State: pushstatus.LaneSuccess, LiveReady: true},
		},
	})
	if !got.Ready || !got.LiveReady || len(got.Reasons) != 0 {
		t.Fatalf("all live lanes succeeded: %+v", got)
	}
}

func TestEvaluateReadinessSetupRequiredIsOperationalButNotLive(t *testing.T) {
	got := EvaluateReadiness(ReadinessInput{
		ProcessRunning:       true,
		HTTPServing:          true,
		SetupRequired:        true,
		Blueprints:           BlueprintReadiness{},
		PersistedState:       PersistedStateReadiness{Writable: true},
		LiveDeliveryExpected: true,
	})
	if !got.Ready || got.LiveReady || !got.SetupRequired {
		t.Fatalf("setup mode must be operational but not live-delivery-ready: %+v", got)
	}
	if got.Lanes == nil {
		t.Fatal("setup readiness lanes must serialize as an empty array, not null")
	}
	if !containsReason(got.Reasons, "no blueprints selected") {
		t.Fatalf("setup mode must explain the operator action: %v", got.Reasons)
	}
}

func TestEvaluateReadinessRejectsNoActiveBlueprintAndUnwritableState(t *testing.T) {
	got := EvaluateReadiness(ReadinessInput{
		ProcessRunning:       true,
		HTTPServing:          true,
		Blueprints:           BlueprintReadiness{Loaded: 3, Skipped: 2, Active: 0},
		PersistedState:       PersistedStateReadiness{Writable: false, Error: "permission denied"},
		LiveDeliveryExpected: true,
		RequiredLanes:        []string{"promrw"},
		Lanes:                []pushstatus.LaneStatus{{Name: "promrw", Configured: true, State: pushstatus.LaneSuccess, LiveReady: true}},
	})
	if got.Ready {
		t.Fatalf("no active blueprint and unwritable state must fail: %+v", got)
	}
	if !containsReason(got.Reasons, "no intended blueprint") || !containsReason(got.Reasons, "permission denied") {
		t.Fatalf("missing hard-gate reasons: %v", got.Reasons)
	}
}

func TestEvaluateReadinessMarksDryRunAsConfiguredButNotLiveReady(t *testing.T) {
	got := EvaluateReadiness(ReadinessInput{
		ProcessRunning:       true,
		HTTPServing:          true,
		Blueprints:           BlueprintReadiness{Loaded: 1, Active: 1},
		PersistedState:       PersistedStateReadiness{Writable: true},
		LiveDeliveryExpected: false,
		RequiredLanes:        []string{"promrw"},
		Lanes: []pushstatus.LaneStatus{{
			Name: "promrw", Configured: true, Disabled: true, DisabledReason: "dry_run", State: pushstatus.LaneDisabled,
		}},
	})
	if got.Ready || got.LiveReady || !containsReason(got.Reasons, "live delivery is disabled") {
		t.Fatalf("dry run must not be live-ready: %+v", got)
	}
}

func TestEvaluateReadinessUsesActiveBlueprintFeedsAsDenominator(t *testing.T) {
	got := EvaluateReadiness(ReadinessInput{
		ProcessRunning:       true,
		HTTPServing:          true,
		Blueprints:           BlueprintReadiness{Loaded: 1, Active: 1},
		PersistedState:       PersistedStateReadiness{Writable: true},
		LiveDeliveryExpected: true,
		RequiredLanes:        []string{"promrw"},
		Lanes: []pushstatus.LaneStatus{
			{Name: "promrw", Configured: true, State: pushstatus.LaneSuccess, LiveReady: true},
			{Name: "otlplogs", Configured: true, State: pushstatus.LaneNotAttempted},
			{Name: "historical", Configured: false, State: pushstatus.LaneUnconfigured},
		},
	})
	if !got.Ready || !got.LiveReady || containsReason(got.Reasons, "otlplogs") || containsReason(got.Reasons, "historical") {
		t.Fatalf("lane outside the current active-blueprint denominator held readiness red: %+v", got)
	}
}

func containsReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, want) {
			return true
		}
	}
	return false
}

func TestEvaluateReadinessHAStandbyBootstrap(t *testing.T) {
	in := ReadinessInput{
		ProcessRunning: true, HTTPServing: true,
		Blueprints:           BlueprintReadiness{Loaded: 1, Active: 1},
		HA:                   &HAReadiness{Standby: true, ConfigLoaded: true, RunnerBuilt: true, PreflightPassed: true},
		LiveDeliveryExpected: true, RequiredLanes: []string{"promrw"},
		Lanes: []pushstatus.LaneStatus{{Name: "promrw", Configured: true, State: pushstatus.LaneNotAttempted}},
	}
	got := EvaluateReadiness(in)
	if !got.Ready || got.LiveReady || got.PersistedState.Writable || len(got.Reasons) != 0 {
		t.Fatalf("bootstrapped standby must be ready without state writes or pushes: %+v", got)
	}
	for _, missing := range []string{"config", "runner", "preflight", "activation", "termination", "process", "http"} {
		t.Run(missing, func(t *testing.T) {
			copy := in
			facts := *in.HA
			copy.HA = &facts
			switch missing {
			case "config":
				facts.ConfigLoaded = false
			case "runner":
				facts.RunnerBuilt = false
			case "preflight":
				facts.PreflightPassed = false
			case "activation", "termination":
				facts.Transitioning = true
			case "process":
				copy.ProcessRunning = false
			case "http":
				copy.HTTPServing = false
			}
			if report := EvaluateReadiness(copy); report.Ready || report.LiveReady || len(report.ReasonCodes) == 0 {
				t.Fatalf("missing %s must hold standby red: %+v", missing, report)
			}
		})
	}
}

func TestEvaluateReadinessHALeaderDeliveryUnchanged(t *testing.T) {
	in := ReadinessInput{
		ProcessRunning: true, HTTPServing: true,
		Blueprints:           BlueprintReadiness{Loaded: 1, Active: 1},
		PersistedState:       PersistedStateReadiness{Writable: true},
		HA:                   &HAReadiness{ConfigLoaded: true, RunnerBuilt: true, PreflightPassed: true},
		LiveDeliveryExpected: true, RequiredLanes: []string{"promrw"},
		Lanes: []pushstatus.LaneStatus{{Name: "promrw", Configured: true, State: pushstatus.LaneNotAttempted}},
	}
	if got := EvaluateReadiness(in); got.Ready || got.LiveReady || !containsReason(got.Reasons, "not_attempted") {
		t.Fatalf("leader with never-pushed lane must stay red: %+v", got)
	}
	in.Lanes[0].State, in.Lanes[0].LiveReady = pushstatus.LaneSuccess, true
	if got := EvaluateReadiness(in); !got.Ready || !got.LiveReady {
		t.Fatalf("leader with fresh delivery must be ready: %+v", got)
	}
	in.HA.Transitioning = true
	if got := EvaluateReadiness(in); got.Ready || got.LiveReady {
		t.Fatalf("terminating leader must not be ready: %+v", got)
	}
}

func TestEvaluateReadinessNonHAUnchanged(t *testing.T) {
	in := ReadinessInput{
		ProcessRunning: true, HTTPServing: true,
		Blueprints:           BlueprintReadiness{Loaded: 1, Active: 1},
		LiveDeliveryExpected: true, RequiredLanes: []string{"promrw"},
		Lanes: []pushstatus.LaneStatus{{Name: "promrw", Configured: true, State: pushstatus.LaneNotAttempted}},
	}
	if got := EvaluateReadiness(in); got.Ready || !containsReason(got.Reasons, "not writable") || !containsReason(got.Reasons, "not_attempted") {
		t.Fatalf("non-HA still requires writable state and first delivery: %+v", got)
	}
	in.PersistedState.Writable = true
	in.Lanes[0].State, in.Lanes[0].LiveReady = pushstatus.LaneSuccess, true
	if got := EvaluateReadiness(in); !got.Ready || !got.LiveReady {
		t.Fatalf("non-HA fresh delivery must remain ready: %+v", got)
	}
}

// fixedBlueprints is a BlueprintLister whose names are synthetic.
type fixedBlueprints struct {
	names []string
	calls int
}

func (f *fixedBlueprints) Blueprints() []string { f.calls++; return f.names }

// standbyGate is a leader gate that never holds leadership.
type standbyGate struct{}

func (standbyGate) Role() ha.Role { return ha.RoleStandby }
func (standbyGate) Do(context.Context, ha.Operation, func(context.Context) error) error {
	return ha.ErrNotLeader
}

// servingReport is a running, serving, non-setup report with sensitive operational detail that must
// never reach the public probe.
func servingReport() ReadinessReport {
	return EvaluateReadiness(ReadinessInput{
		ProcessRunning:       true,
		HTTPServing:          true,
		Blueprints:           BlueprintReadiness{Loaded: 3, Skipped: 1, Active: 2},
		PersistedState:       PersistedStateReadiness{Writable: true, Error: "secret filesystem path"},
		LiveDeliveryExpected: true,
		RequiredLanes:        []string{"private-sink"},
		Lanes: []pushstatus.LaneStatus{{
			Name: "private-sink", Configured: true, State: pushstatus.LaneNotAttempted,
			LastError: "private endpoint failure",
		}},
	})
}

func getReadiness(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	// No Authorization header: the probe is public even when CONTROL_TOKEN is set.
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/control/readiness", nil))
	return rec
}

// goldenProbeOff is the exact public probe body for servingReport before this setting existed.
const goldenProbeOff = `{"running":true,"http_ready":true,"ready":false,"live_ready":false,"setup_required":false,` +
	`"blueprints":{"loaded":3,"skipped":1,"active":2},"persisted_state":{"writable":true},` +
	`"reason_codes":["delivery_not_attempted"]}` + "\n"

func TestPublicReadinessByteIdenticalWhenBlueprintFlagsOff(t *testing.T) {
	report := servingReport()
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	store.Update(func(s *State) { s.DisabledBlueprints = []string{"alpha"} })
	lister := &fixedBlueprints{names: []string{"alpha", "beta"}}
	src := StatusSources{Readiness: func() ReadinessReport { return report }}

	cases := map[string]*Handler{
		"default":        NewHandler(store, nil, "secret").SetStatus(src),
		"explicit nil":   NewHandler(store, nil, "secret").SetStatus(src).SetReadinessBlueprintFlags(nil),
		"set then unset": NewHandler(store, nil, "secret").SetStatus(src).SetReadinessBlueprintFlags(lister).SetReadinessBlueprintFlags(nil),
	}
	for name, h := range cases {
		rec := getReadiness(t, h)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", name, rec.Code)
		}
		if got := rec.Body.String(); got != goldenProbeOff {
			t.Errorf("%s: probe is not byte-identical\n got: %q\nwant: %q", name, got, goldenProbeOff)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("%s: content-type = %q", name, ct)
		}
	}
	if lister.calls != 0 {
		t.Errorf("lister consulted %d times with the setting off", lister.calls)
	}
}

func TestPublicReadinessBlueprintFlagsExactFieldSet(t *testing.T) {
	report := servingReport()
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	store.Update(func(s *State) {
		s.DisabledBlueprints = []string{"beta"}
		s.Failures = map[string]FailureSetting{"private-failure-mode": {}}
		s.ActiveScenarios = []string{"private/scenario"}
	})
	lister := &fixedBlueprints{names: []string{"alpha", "beta", "gamma"}}
	h := NewHandler(store, nil, "secret").
		SetStatus(StatusSources{Readiness: func() ReadinessReport { return report }}).
		SetReadinessBlueprintFlags(lister)

	rec := getReadiness(t, h)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (blueprint flags must not change readiness)", rec.Code)
	}
	want := strings.TrimSuffix(goldenProbeOff, "}\n") + `,"blueprint_emitting":{"alpha":true,"beta":false,"gamma":true}}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("probe body mismatch\n got: %s\nwant: %s", got, want)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &top); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range top {
		keys = append(keys, k)
	}
	wantKeys := []string{"blueprint_emitting", "blueprints", "http_ready", "live_ready", "persisted_state", "reason_codes", "ready", "running", "setup_required"}
	if !sameSet(keys, wantKeys) {
		t.Fatalf("top-level fields = %v, want %v", keys, wantKeys)
	}
	// Each per-blueprint value is a bare boolean: nothing else can ride along per blueprint.
	var flags map[string]json.RawMessage
	if err := json.Unmarshal(top["blueprint_emitting"], &flags); err != nil {
		t.Fatal(err)
	}
	for name, raw := range flags {
		if string(raw) != "true" && string(raw) != "false" {
			t.Errorf("blueprint %q value %s is not a bare boolean", name, raw)
		}
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"private-sink", "private endpoint", "secret filesystem", "private-failure-mode", "private/scenario", `"lanes"`, `"reasons"`, `"error"`, ".yaml"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("probe exposed %q: %s", forbidden, body)
		}
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			return false
		}
	}
	return true
}

// TestPublicReadinessBlueprintNamingIsPlainLoadedNames pins the naming decision: the plain
// loaded blueprint name is the key, unloaded or stale names never appear, and an empty set is an
// empty object rather than null or absent.
func TestPublicReadinessBlueprintNamingIsPlainLoadedNames(t *testing.T) {
	report := servingReport()
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	store.Update(func(s *State) { s.DisabledBlueprints = []string{"retired-blueprint"} })
	h := NewHandler(store, nil, "").
		SetStatus(StatusSources{Readiness: func() ReadinessReport { return report }})

	probe := func(names []string) map[string]bool {
		h.SetReadinessBlueprintFlags(&fixedBlueprints{names: names})
		var got struct {
			Flags map[string]bool `json:"blueprint_emitting"`
		}
		rec := getReadiness(t, h)
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Flags == nil {
			t.Fatalf("blueprint_emitting absent or null: %s", rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "retired-blueprint") {
			t.Fatalf("a disabled name that is not loaded leaked: %s", rec.Body.String())
		}
		return got.Flags
	}
	if got := probe([]string{"payments-edge", "otlp-native"}); !reflect.DeepEqual(got, map[string]bool{"payments-edge": true, "otlp-native": true}) {
		t.Fatalf("flags = %v", got)
	}
	if got := probe(nil); len(got) != 0 {
		t.Fatalf("no loaded blueprints must give an empty object, got %v", got)
	}
}

func TestPublicReadinessBlueprintEmittingSemantics(t *testing.T) {
	loaded := []string{"alpha", "beta"}
	base := servingReport()
	mut := func(f func(*ReadinessReport)) ReadinessReport { r := base; f(&r); return r }
	cases := []struct {
		name    string
		report  ReadinessReport
		disable []string
		gate    ha.LeaderGate
		want    map[string]bool
	}{
		{"loaded and enabled emit", base, nil, nil, map[string]bool{"alpha": true, "beta": true}},
		{"disabled blueprint does not emit", base, []string{"alpha"}, nil, map[string]bool{"alpha": false, "beta": true}},
		{"every blueprint disabled", base, []string{"alpha", "beta"}, nil, map[string]bool{"alpha": false, "beta": false}},
		{"setup required emits nothing", mut(func(r *ReadinessReport) { r.SetupRequired = true }), nil, nil, map[string]bool{"alpha": false, "beta": false}},
		{"process not running", mut(func(r *ReadinessReport) { r.Running = false }), nil, nil, map[string]bool{"alpha": false, "beta": false}},
		{"http not serving", mut(func(r *ReadinessReport) { r.HTTPReady = false }), nil, nil, map[string]bool{"alpha": false, "beta": false}},
		{"ha bootstrap incomplete", mut(func(r *ReadinessReport) {
			r.ReasonCodes = append(r.ReasonCodes, ReadinessHABootstrapIncomplete)
		}), nil, nil, map[string]bool{"alpha": false, "beta": false}},
		{"ha transitioning", mut(func(r *ReadinessReport) {
			r.ReasonCodes = append(r.ReasonCodes, ReadinessHATransitioning)
		}), nil, nil, map[string]bool{"alpha": false, "beta": false}},
		{"ha standby", base, nil, standbyGate{}, map[string]bool{"alpha": false, "beta": false}},
		{"delivery trouble does not hide emission", mut(func(r *ReadinessReport) {
			r.ReasonCodes = append(r.ReasonCodes, ReadinessDeliveryError, ReadinessPersistedStateNotWritable)
			r.PersistedState.Writable = false
		}), nil, nil, map[string]bool{"alpha": true, "beta": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore(filepath.Join(t.TempDir(), "state.json"))
			store.Update(func(s *State) { s.DisabledBlueprints = tc.disable })
			report := tc.report
			h := NewHandler(store, nil, "").
				SetStatus(StatusSources{Readiness: func() ReadinessReport { return report }}).
				SetReadinessBlueprintFlags(&fixedBlueprints{names: loaded})
			if tc.gate != nil {
				h.SetHA(tc.gate, ha.Bounded{Timeout: time.Second})
			}
			var got struct {
				Flags map[string]bool `json:"blueprint_emitting"`
			}
			rec := getReadiness(t, h)
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Flags, tc.want) {
				t.Fatalf("flags = %v, want %v", got.Flags, tc.want)
			}
		})
	}
}

func TestStatusRouteIsUnchangedByBlueprintFlags(t *testing.T) {
	report := servingReport()
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	src := StatusSources{Readiness: func() ReadinessReport { return report }}
	status := func(h *Handler) string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/control/status", nil)
		req.SetBasicAuth("control", "secret")
		h.ServeHTTP(rec, req)
		return rec.Body.String()
	}
	off := status(NewHandler(store, nil, "secret").SetStatus(src))
	on := status(NewHandler(store, nil, "secret").SetStatus(src).SetReadinessBlueprintFlags(&fixedBlueprints{names: []string{"alpha"}}))
	if off != on || strings.Contains(on, "blueprint_emitting") {
		t.Fatalf("authenticated status changed by the public-probe setting\n off: %s\n  on: %s", off, on)
	}
}

// fakeSchema is a SchemaSource carrying only the construct inventory the probe reads.
type fakeSchema struct{ constructs []ConstructInfo }

func (f fakeSchema) ControlSchema() Schema { return Schema{Constructs: f.constructs} }

func TestPublicReadinessBlueprintEmittingConstructAndVolumeState(t *testing.T) {
	loaded := []string{"alpha", "beta", "bare"}
	constructs := []ConstructInfo{
		{Blueprint: "alpha", Kind: "k8s", Name: "c1"},
		{Blueprint: "alpha", Kind: "rds", Name: "db"},
		{Blueprint: "beta", Kind: "k8s", Name: "c2"},
	}
	cases := []struct {
		name   string
		mut    func(*State)
		schema bool
		want   map[string]bool
	}{
		{"nothing disabled", func(*State) {}, true, map[string]bool{"alpha": true, "beta": true, "bare": true}},
		{"every kind of a blueprint disabled", func(s *State) { s.DisabledKinds = []string{"k8s", "rds"} }, true,
			map[string]bool{"alpha": false, "beta": false, "bare": true}},
		{"one kind still enabled", func(s *State) { s.DisabledKinds = []string{"k8s"} }, true,
			map[string]bool{"alpha": true, "beta": false, "bare": true}},
		{"construct-level disable of the only construct", func(s *State) { s.DisabledConstructs = []string{ConstructKey("beta", "k8s", "c2")} }, true,
			map[string]bool{"alpha": true, "beta": false, "bare": true}},
		{"construct-level disable of one of two", func(s *State) { s.DisabledConstructs = []string{ConstructKey("alpha", "k8s", "c1")} }, true,
			map[string]bool{"alpha": true, "beta": true, "bare": true}},
		{"construct-level disable of every construct", func(s *State) {
			s.DisabledConstructs = []string{ConstructKey("alpha", "k8s", "c1"), ConstructKey("alpha", "rds", "db")}
		}, true, map[string]bool{"alpha": false, "beta": true, "bare": true}},
		{"no schema source keeps blueprint-level behaviour", func(s *State) { s.DisabledKinds = []string{"k8s", "rds"} }, false,
			map[string]bool{"alpha": true, "beta": true, "bare": true}},
		{"disabled blueprint stays false", func(s *State) { s.DisabledBlueprints = []string{"alpha"} }, true,
			map[string]bool{"alpha": false, "beta": true, "bare": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore(filepath.Join(t.TempDir(), "state.json"))
			store.Update(tc.mut)
			report := servingReport()
			var src []SchemaSource
			if tc.schema {
				src = append(src, fakeSchema{constructs: constructs})
			}
			h := NewHandler(store, nil, "", src...).
				SetStatus(StatusSources{Readiness: func() ReadinessReport { return report }}).
				SetReadinessBlueprintFlags(&fixedBlueprints{names: loaded})
			var got struct {
				Flags map[string]bool `json:"blueprint_emitting"`
			}
			rec := getReadiness(t, h)
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Flags, tc.want) {
				t.Fatalf("flags = %v, want %v", got.Flags, tc.want)
			}
		})
	}
}

// With the setting off a schema source must not be consulted and the probe stays byte-identical.
type countingSchema struct{ calls int }

func (c *countingSchema) ControlSchema() Schema { c.calls++; return Schema{} }

func TestPublicReadinessOffNeverConsultsSchema(t *testing.T) {
	cs := &countingSchema{}
	report := servingReport()
	h := NewHandler(NewStore(filepath.Join(t.TempDir(), "state.json")), nil, "secret", cs).
		SetStatus(StatusSources{Readiness: func() ReadinessReport { return report }})
	if got := getReadiness(t, h).Body.String(); got != goldenProbeOff {
		t.Fatalf("probe changed with a schema source attached: %q", got)
	}
	if cs.calls != 0 {
		t.Fatalf("schema consulted %d times with the setting off", cs.calls)
	}
}
