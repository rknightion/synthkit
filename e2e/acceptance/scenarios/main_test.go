// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
