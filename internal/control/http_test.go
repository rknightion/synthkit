// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// schemaSourceFunc adapts a plain function to the SchemaSource interface.
type schemaSourceFunc func() Schema

func (f schemaSourceFunc) ControlSchema() Schema { return f() }

func TestSchemaRouteAudienceFilter(t *testing.T) {
	store := NewStore(t.TempDir() + "/control-state.json")
	src := schemaSourceFunc(func() Schema { return fullSchema() })
	h := NewHandler(store, nil, "", src)

	do := func(q string) Schema {
		req := httptest.NewRequest("GET", "/control/schema"+q, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
		var s Schema
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return s
	}

	if got := do(""); len(got.Kinds) == 0 {
		t.Error("operator (default) schema must include Kinds")
	}
	cust := do("?audience=customer")
	if len(cust.Kinds) != 0 || len(cust.Constructs) != 0 {
		t.Errorf("customer schema leaked operator fields: %+v", cust)
	}
	if cust.VolumeMultiplier.Key == "" || len(cust.Scenarios) == 0 {
		t.Errorf("customer schema missing volume/scenarios: %+v", cust)
	}
}

// TestHandlerSpanMetricsEndpoint covers POST /control/spanmetrics (opt-in, mirrors
// POST /control/blueprints): the posted list replaces SpanMetricsBlueprints, returns 200 +
// the new state, and GET /control/state reflects it.
func TestHandlerSpanMetricsEndpoint(t *testing.T) {
	store := NewStore(t.TempDir() + "/control-state.json")
	h := NewHandler(store, nil, "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/control/spanmetrics", strings.NewReader(`{"span_metrics_blueprints":["bp-a"]}`)))
	if rec.Code != 200 {
		t.Fatalf("POST spanmetrics: %d %s", rec.Code, rec.Body)
	}
	var out State
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.SpanMetricsEnabled("bp-a") {
		t.Fatalf("returned state must list bp-a: %+v", out.SpanMetricsBlueprints)
	}

	// GET /control/state reflects the persisted opt-in.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/control/state", nil))
	var got State
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.SpanMetricsEnabled("bp-a") {
		t.Fatalf("GET state must reflect span_metrics_blueprints: %+v", got.SpanMetricsBlueprints)
	}
}

type fakeIncidentSrc struct{ valErr error }

func (f fakeIncidentSrc) ControlSchema() Schema { return Schema{} } // also satisfies SchemaSource
func (f fakeIncidentSrc) ControlIncidents() []IncidentInfo {
	return []IncidentInfo{{Source: "declared", Blueprint: "starter", Mode: "latency_spike", ScheduleSpec: "latency_spike@15:04/30m"}}
}
func (f fakeIncidentSrc) ValidateRuntimeIncident(RuntimeIncident) error { return f.valErr }

func TestIncidentsPOSTGETDELETE(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "state.json"))
	h := NewHandler(store, func(State) {}, "tok", fakeIncidentSrc{})
	srv := httptest.NewServer(h)
	defer srv.Close()

	// POST creates and mints an ID.
	body := `{"blueprint":"starter","mode":"latency_spike","target":"starter-api","at":"15:04","for":"30m","intensity":0.8}`
	req, _ := http.NewRequest("POST", srv.URL+"/control/incidents", strings.NewReader(body))
	req.SetBasicAuth("control", "tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("POST: err=%v status=%d", err, resp.StatusCode)
	}
	var st State
	json.NewDecoder(resp.Body).Decode(&st)
	if len(st.RuntimeIncidents) != 1 || st.RuntimeIncidents[0].ID == "" {
		t.Fatalf("POST should create 1 incident with a minted ID, got %+v", st.RuntimeIncidents)
	}
	id := st.RuntimeIncidents[0].ID

	// GET lists declared (from the source).
	gresp, _ := http.Get(srv.URL + "/control/incidents")
	var infos []IncidentInfo
	json.NewDecoder(gresp.Body).Decode(&infos)
	if len(infos) != 1 || infos[0].Source != "declared" {
		t.Fatalf("GET should return the source's incidents, got %+v", infos)
	}

	// DELETE removes by id.
	dreq, _ := http.NewRequest("DELETE", srv.URL+"/control/incidents/"+id, nil)
	dreq.SetBasicAuth("control", "tok")
	dresp, _ := http.DefaultClient.Do(dreq)
	if dresp.StatusCode != 200 {
		t.Fatalf("DELETE status=%d", dresp.StatusCode)
	}
	if st2 := store.Snapshot(); len(st2.RuntimeIncidents) != 0 {
		t.Fatalf("DELETE should remove the incident, got %+v", st2.RuntimeIncidents)
	}
}

func TestIncidentsPOSTValidationRejected(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "state.json"))
	h := NewHandler(store, func(State) {}, "tok", fakeIncidentSrc{valErr: errors.New("bad mode")})
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/control/incidents", strings.NewReader(`{"blueprint":"x","mode":"nope","at":"15:04","for":"30m"}`))
	req.SetBasicAuth("control", "tok")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 400 {
		t.Fatalf("invalid POST should be 400, got %d", resp.StatusCode)
	}
}

func TestIncidentsGETUnavailableWithoutSource(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "state.json"))
	h := NewHandler(store, func(State) {}, "tok") // no IncidentSource
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/control/incidents")
	if resp.StatusCode != 404 {
		t.Fatalf("GET without source should be 404, got %d", resp.StatusCode)
	}
}
