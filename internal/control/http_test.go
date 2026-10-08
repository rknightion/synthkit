// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/rknightion/synthkit/internal/ha"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHASlowMutationBodyCannotCrashOrAcknowledgeSuccess(t *testing.T) {
	gate := ha.NewGate()
	store, err := NewHAStore(filepath.Join(t.TempDir(), "state.json"), gate)
	if err != nil {
		t.Fatal(err)
	}
	_ = gate.Activate(context.Background(), func(ctx context.Context) error { return store.ProbeWriteContext(ctx) })
	var mu sync.Mutex
	crashed := false
	h := NewHandler(store, func(State) { t.Error("incomplete body applied") }, "").SetHA(gate, ha.Bounded{Timeout: 100 * time.Millisecond, Margin: 100 * time.Millisecond, Crash: func() { mu.Lock(); crashed = true; mu.Unlock() }})
	srv := httptest.NewServer(h)
	defer srv.Close()
	conn, err := net.DialTimeout("tcp", srv.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, err = io.WriteString(conn, "POST /control/load HTTP/1.1\r\nHost: local\r\nContent-Length: 100\r\nContent-Type: application/json\r\n\r\n{")
	if err != nil {
		t.Fatal(err)
	}
	response, readErr := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "POST"})
	if response != nil {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode == 200 || strings.Contains(string(body), "not_leader") {
			t.Fatalf("slow body produced false success/leadership result: %d %s", response.StatusCode, body)
		}
	} else if readErr == nil {
		t.Fatal("no observed response or bounded connection close")
	}
	joined, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := gate.Wait(joined); err != nil {
		t.Fatal("body reader abandoned", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if crashed || gate.Role() != ha.RoleLeader || store.Snapshot().VolumeMultiplier != 1 {
		t.Fatal("slow client crashed or mutated the leader")
	}
}

func TestHAStandbyAuthenticatesBeforeFenceAndNeverReadsBody(t *testing.T) {
	gate := ha.NewGate()
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewHAStore(path, gate)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store, func(State) { t.Fatal("standby ApplyControl") }, "password").SetHA(gate, ha.Bounded{Timeout: 2 * time.Second, Margin: time.Second, Crash: func() { t.Error("unexpected watchdog") }})
	for _, route := range []string{"/control/load", "/control/reset", "/control/failures", "/control/incidents", "/control/blueprints/custom", "/control/blueprints/sources", "/control/blueprints/sources/fetch", "/control/blueprints/validate", "/control/restart"} {
		request := httptest.NewRequest("POST", route, strings.NewReader("invalid body"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request)
		if w.Code != 401 {
			t.Fatalf("%s auth bypass: %d", route, w.Code)
		}
		request = httptest.NewRequest("POST", route, &unreadableHABody{t: t})
		request.SetBasicAuth("control", "password")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, request)
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"code": "not_leader"`) {
			t.Fatalf("%s not fenced: %d %s", route, w.Code, w.Body.String())
		}
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/control/reset", strings.NewReader("{}"))
	req.SetBasicAuth("control", "password")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatal("real HTTP mutation not fenced")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("standby wrote control state", err)
	}
	if err := store.ProbeWrite(); !errors.Is(err, ha.ErrNotLeader) {
		t.Fatal("standby ProbeWrite not fenced", err)
	}
}

func TestHAMutationWaitIncludesOrderedApplyAndEvent(t *testing.T) {
	gate := ha.NewGate()
	store, err := NewHAStore(filepath.Join(t.TempDir(), "state.json"), gate)
	if err != nil {
		t.Fatal(err)
	}
	_ = gate.Activate(context.Background(), func(ctx context.Context) error { return store.ProbeWriteContext(ctx) })
	entered := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var applied []State
	events := 0
	h := NewHandler(store, func(s State) {
		mu.Lock()
		n := len(applied)
		applied = append(applied, s)
		mu.Unlock()
		if n == 0 {
			close(entered)
			<-release
		}
	}, "").SetHA(gate, ha.Bounded{Timeout: 2 * time.Second, Margin: time.Second, Crash: func() { t.Error("unexpected watchdog") }}).SetChangeObserver(func(State) { mu.Lock(); events++; mu.Unlock() })
	srv := httptest.NewServer(h)
	defer srv.Close()
	client := &http.Client{Timeout: time.Second}
	done := make(chan int, 2)
	post := func(path, body string) {
		resp, err := client.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Error(err)
			done <- 0
			return
		}
		resp.Body.Close()
		done <- resp.StatusCode
	}
	go post("/control/load", `{"volume_multiplier":2}`)
	<-entered
	go post("/control/blueprints", `{"disabled_blueprints":["test"]}`)
	wait, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if !errors.Is(gate.Wait(wait), context.DeadlineExceeded) {
		t.Fatal("Wait omitted late ApplyControl/event")
	}
	close(release)
	for i := 0; i < 2; i++ {
		if code := <-done; code != 200 {
			t.Fatal("mutation failed", code)
		}
	}
	joined, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := gate.Wait(joined); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if events != 2 || len(applied) != 2 || applied[1].VolumeMultiplier != 2 || len(applied[1].DisabledBlueprints) != 1 {
		t.Fatalf("disjoint edits/application ordering/events lost: events=%d applied=%v", events, applied)
	}
}

type unreadableHABody struct{ t *testing.T }

func (b *unreadableHABody) Read([]byte) (int, error) {
	b.t.Fatal("standby read mutation body")
	return 0, nil
}

func TestHAFilePreparationAndPersistFailure(t *testing.T) {
	gate := ha.NewGate()
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewHAStore(path, gate)
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Activate(context.Background(), func(ctx context.Context) error { return store.ProbeWriteContext(ctx) }); err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store, func(State) { t.Fatal("failed persist applied") }, "").SetHA(gate, ha.Bounded{Timeout: 2 * time.Second, Margin: time.Second, Crash: func() { t.Error("unexpected watchdog") }})
	req := httptest.NewRequest("POST", "/control/load", strings.NewReader(`{"volume_multiplier":2}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 503 || store.Snapshot().VolumeMultiplier != before.VolumeMultiplier {
		t.Fatalf("failed file persist acknowledged/published: %d %s", w.Code, w.Body.String())
	}
}

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
	greq, _ := http.NewRequest(http.MethodGet, srv.URL+"/control/incidents", nil)
	greq.SetBasicAuth("control", "tok")
	gresp, _ := http.DefaultClient.Do(greq)
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
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/control/incidents", nil)
	req.SetBasicAuth("control", "tok")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 404 {
		t.Fatalf("GET without source should be 404, got %d", resp.StatusCode)
	}
}
