// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/failuremode"
)

func TestRunDerivesIDsAndContinuesAfterFailures(t *testing.T) {
	activated := []string{}
	schema := control.Schema{Scenarios: []control.ScenarioInfo{
		{Blueprint: "bp", Name: "missing", Effects: []control.EffectInfo{{Mode: "uncovered"}}},
		{Blueprint: "bp", Name: "node", Effects: []control.EffectInfo{{Mode: "node_not_ready", Target: "cluster-a"}}},
	}}
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/control/schema":
			_ = json.NewEncoder(w).Encode(schema)
		case "/control/scenarios/activate", "/control/scenarios/deactivate":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if r.URL.Path == "/control/scenarios/activate" {
				activated = append(activated, body["scenario"])
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer controlServer.Close()
	queryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"result": []any{map[string]any{"value": []any{0, "1"}}}},
		})
	}))
	defer queryServer.Close()

	r, err := run(context.Background(), config{controlURL: controlServer.URL, promURL: queryServer.URL, settle: 0, minChange: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	if r.ScenarioCount != 2 || len(r.Dispositions) != 2 {
		t.Fatalf("report = %+v", r)
	}
	if len(activated) != 2 || activated[0] != "bp/missing" || activated[1] != "bp/node" {
		t.Fatalf("activated = %v, want every schema-derived scenario", activated)
	}
	if r.Dispositions[0].ID != "bp/missing" || r.Dispositions[0].Verdict != "fail" {
		t.Fatalf("first disposition = %+v", r.Dispositions[0])
	}
	if r.Dispositions[1].ID != "bp/node" {
		t.Fatalf("second disposition = %+v", r.Dispositions[1])
	}
}

func TestMovementAndClearSemantics(t *testing.T) {
	if !moved("increase", 0, 1, 0.2) || !moved("decrease", 10, 7, 0.2) {
		t.Fatal("expected directional movements to pass")
	}
	if moved("increase", 10, 11, 0.2) || moved("decrease", 10, 9, 0.2) {
		t.Fatal("insufficient movement passed")
	}
	if !near(0, 0.1, 0.2) || near(1, 1.3, 0.2) {
		t.Fatal("near baseline semantics incorrect")
	}
}

func TestQueryLabelValueEscapesSchemaNames(t *testing.T) {
	if got, want := queryLabelValue("quote\" slash\\"), "quote\\\" slash\\\\"; got != want {
		t.Fatalf("queryLabelValue = %q, want %q", got, want)
	}
}

func TestObserveRejectsNonFiniteQueryValues(t *testing.T) {
	queryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"result": []any{map[string]any{"value": []any{0, "NaN"}}}},
		})
	}))
	defer queryServer.Close()
	_, err := observe(context.Background(), queryServer.Client(), config{promURL: queryServer.URL}, failuremode.Assertion{Mode: "latency_storm", QueryAPI: "prometheus", Query: "vector(0)"}, "", "")
	if err == nil {
		t.Fatal("non-finite query value was accepted")
	}
}

// resourceFixture is a stand-in control plane and query API for one resource-axis mode. While the
// scenario is active, the carrying series reads active; otherwise it reads baseline. A fixture with
// effect false never moves anything, which is the "mode is not active" case.
type resourceFixture struct {
	t        *testing.T
	effect   bool
	sticky   bool // the carrying series stays elevated after deactivation
	activate int
	active   bool
	queries  []string
}

func (f *resourceFixture) servers() (controlURL, promURL string, closeFn func()) {
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/control/scenarios/activate":
			f.active = true
			f.activate++
		case "/control/scenarios/deactivate":
			f.active = false
		}
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.queries = append(f.queries, r.URL.Query().Get("query"))
		elevated := f.effect && (f.active || f.sticky && f.activate > 0)
		value := "1"
		if elevated {
			value = "100" // above every per-mode absolute floor
		}
		series := func(labels map[string]string, v string) map[string]any {
			return map[string]any{"metric": labels, "value": []any{0, v}}
		}
		result := []any{
			series(map[string]string{"service": "steady"}, "2"),
			series(map[string]string{"service": "carrier"}, value),
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"result": result}})
	}))
	return controlServer.URL, promServer.URL, func() { controlServer.Close(); promServer.Close() }
}

func runResourceMode(t *testing.T, mode string, f *resourceFixture) disposition {
	t.Helper()
	f.t = t
	controlURL, promURL, closeFn := f.servers()
	defer closeFn()
	sc := control.ScenarioInfo{Blueprint: "bp", Name: "causal", Effects: []control.EffectInfo{{Mode: mode, Target: "declared-resource-name", Intensity: 0.7}}}
	return exercise(context.Background(), &http.Client{}, config{controlURL: controlURL, promURL: promURL, minChange: 0.2}, sc)
}

var resourceAxisModes = []string{
	"pool_exhausted", "concurrency_reduced", "quota_reduced", "consumer_slowdown",
	"dependency_slow", "dependency_unavailable", "credential_expired", "rule_regression",
}

func TestResourceAxisModesPassWhenActiveAndFailWhenNot(t *testing.T) {
	for _, mode := range resourceAxisModes {
		t.Run(mode, func(t *testing.T) {
			if _, ok := failuremode.AssertionFor([]string{mode}); !ok {
				t.Fatalf("no covering assertion for %s", mode)
			}
			active := &resourceFixture{effect: true}
			if d := runResourceMode(t, mode, active); d.Verdict != "pass" || d.Mode != mode || d.Baseline == nil || d.Active == nil || d.Cleared == nil {
				t.Fatalf("active fixture disposition = %+v, want a pass for %s with baseline, active and cleared values", d, mode)
			}
			for _, q := range active.queries {
				if strings.Contains(q, "{{") {
					t.Fatalf("query still holds a placeholder: %q", q)
				}
				if strings.Contains(q, "traces_") && !strings.Contains(q, `blueprint="bp"`) {
					t.Fatalf("blueprint-scoped query is not scoped to the scenario's blueprint: %q", q)
				}
			}
			inert := &resourceFixture{effect: false}
			if d := runResourceMode(t, mode, inert); d.Verdict != "fail" || !strings.Contains(d.Detail, "did not increase") && !strings.Contains(d.Detail, "no series increase") {
				t.Fatalf("inert fixture disposition = %+v, want a movement failure for %s", d, mode)
			}
		})
	}
}

func TestResourceAxisModesFailWhenDeactivationDoesNotClear(t *testing.T) {
	for _, mode := range resourceAxisModes {
		t.Run(mode, func(t *testing.T) {
			d := runResourceMode(t, mode, &resourceFixture{effect: true, sticky: true})
			if d.Verdict != "fail" || !strings.Contains(d.Detail, "deactivation did not return") {
				t.Fatalf("sticky fixture disposition = %+v, want a clearance failure for %s", d, mode)
			}
		})
	}
}

// An unrelated series moving is not the mode's effect: the per-series assertions need a series
// present on both sides to move by the threshold, not any change in the result set.
func TestAnySeriesIgnoresASeriesThatOnlyAppears(t *testing.T) {
	var polls int
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer controlServer.Close()
	promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		polls++
		result := []any{map[string]any{"metric": map[string]string{"service": "steady"}, "value": []any{0, "2"}}}
		if polls > 1 {
			result = append(result, map[string]any{"metric": map[string]string{"service": "newcomer"}, "value": []any{0, "9"}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"result": result}})
	}))
	defer promServer.Close()
	sc := control.ScenarioInfo{Blueprint: "bp", Name: "causal", Effects: []control.EffectInfo{{Mode: "dependency_slow", Target: "r"}}}
	d := exercise(context.Background(), &http.Client{}, config{controlURL: controlServer.URL, promURL: promServer.URL, minChange: 0.2}, sc)
	if d.Verdict != "fail" {
		t.Fatalf("disposition = %+v, want a failure: only a newly appearing series differed", d)
	}
}

func TestSeriesKeyIgnoresNameAndOrdersLabels(t *testing.T) {
	got := seriesKey(map[string]string{"service": "a", "__name__": "m", "env": "x"})
	if want := "env=x,service=a"; got != want {
		t.Fatalf("seriesKey = %q, want %q", got, want)
	}
}

// A moved series that stops being reported after deactivation has not stayed elevated, but the
// report must not invent a cleared value for it.
func TestAnySeriesDoesNotInventAClearedValue(t *testing.T) {
	var polls int
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer controlServer.Close()
	promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		polls++
		var result []any
		switch polls {
		case 1:
			result = []any{map[string]any{"metric": map[string]string{"service": "carrier"}, "value": []any{0, "1"}}}
		case 2:
			result = []any{map[string]any{"metric": map[string]string{"service": "carrier"}, "value": []any{0, "5"}}}
		default:
			result = []any{map[string]any{"metric": map[string]string{"service": "other"}, "value": []any{0, "1"}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"result": result}})
	}))
	defer promServer.Close()
	sc := control.ScenarioInfo{Blueprint: "bp", Name: "causal", Effects: []control.EffectInfo{{Mode: "dependency_slow", Target: "r"}}}
	d := exercise(context.Background(), &http.Client{}, config{controlURL: controlServer.URL, promURL: promServer.URL, minChange: 0.2}, sc)
	if d.Verdict != "pass" || d.Cleared != nil {
		t.Fatalf("disposition = %+v, want a pass with no cleared value", d)
	}
}

// zeroBaselineRun drives one scenario whose only moving series is "carrier", reading the given
// values on the baseline, active and cleared observations, beside an unrelated steady series.
func zeroBaselineRun(t *testing.T, mode string, baseline, active, cleared string) disposition {
	t.Helper()
	var polls int
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer controlServer.Close()
	promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		polls++
		value := []string{baseline, active, cleared}[min(polls, 3)-1]
		result := []any{map[string]any{"metric": map[string]string{"queue": "carrier"}, "value": []any{0, value}}}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"result": result}})
	}))
	defer promServer.Close()
	sc := control.ScenarioInfo{Blueprint: "bp", Name: "causal", Effects: []control.EffectInfo{{Mode: mode, Target: "r", Intensity: 0.7}}}
	return exercise(context.Background(), &http.Client{}, config{controlURL: controlServer.URL, promURL: promServer.URL, minChange: 0.2}, sc)
}

// A series that sits at zero when healthy and blips to a small positive value is unrelated noise:
// the proportional rule alone accepts any positive value over a zero baseline.
func TestZeroBaselineNeedsTheAbsoluteFloor(t *testing.T) {
	for _, mode := range []string{"consumer_slowdown", "concurrency_reduced"} {
		a, _ := failuremode.AssertionFor([]string{mode})
		if a.MinActive <= 0 {
			t.Fatalf("%s has no absolute floor", mode)
		}
		below := strconv.FormatFloat(a.MinActive/2, 'f', -1, 64)
		at := strconv.FormatFloat(a.MinActive, 'f', -1, 64)
		t.Run(mode+"/blip fails", func(t *testing.T) {
			if d := zeroBaselineRun(t, mode, "0", below, "0"); d.Verdict != "fail" || !strings.Contains(d.Detail, "increase") {
				t.Fatalf("0 -> %s -> 0 disposition = %+v, want a movement failure", below, d)
			}
		})
		t.Run(mode+"/true positive passes", func(t *testing.T) {
			if d := zeroBaselineRun(t, mode, "0", at, "0"); d.Verdict != "pass" {
				t.Fatalf("0 -> %s -> 0 disposition = %+v, want a pass", at, d)
			}
		})
		t.Run(mode+"/residual under the floor clears", func(t *testing.T) {
			if d := zeroBaselineRun(t, mode, "0", "200", below); d.Verdict != "pass" {
				t.Fatalf("0 -> 200 -> %s disposition = %+v, want a pass (back under the floor)", below, d)
			}
		})
		t.Run(mode+"/still elevated fails to clear", func(t *testing.T) {
			if d := zeroBaselineRun(t, mode, "0", "200", at); d.Verdict != "fail" || !strings.Contains(d.Detail, "deactivation did not return") {
				t.Fatalf("0 -> 200 -> %s disposition = %+v, want a clearance failure", at, d)
			}
		})
	}
}

// A non-zero baseline keeps the proportional rule (the floor only adds to it), and an assertion
// with no floor keeps the old semantics exactly.
func TestFloorKeepsTheProportionalRuleForNonZeroBaselines(t *testing.T) {
	a := failuremode.Assertion{Direction: failuremode.DirectionIncrease, MinActive: 30}
	if movedFor(a, 40, 45, 0.2) {
		t.Fatal("a 12% rise over a non-zero baseline passed")
	}
	if !movedFor(a, 40, 50, 0.2) {
		t.Fatal("a 25% rise above the floor failed")
	}
	if movedFor(a, 0, 29, 0.2) || !movedFor(a, 0, 30, 0.2) {
		t.Fatal("zero baseline must need the floor exactly")
	}
	plain := failuremode.Assertion{Direction: failuremode.DirectionIncrease}
	if !movedFor(plain, 0, 0.001, 0.2) || !clearedFor(plain, 10, 11, 0.2) {
		t.Fatal("an assertion without a floor changed behaviour")
	}
}
