// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/pushhook"
	nativesigil "github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/queue"
)

// capturedRequest records one inbound request to the test server.
type capturedRequest struct {
	path   string
	auth   string
	body   []byte
	parsed map[string]any
}

// testServer starts an httptest.Server that records all POST requests and
// returns 202 with {"accepted":true}. Requests are appended to *reqs.
func testServer(t *testing.T, reqs *[]capturedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("test server: read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)

		*reqs = append(*reqs, capturedRequest{
			path:   r.URL.Path,
			auth:   r.Header.Get("Authorization"),
			body:   body,
			parsed: parsed,
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		if r.URL.Path == "/api/v1/scores:export" {
			_, _ = w.Write([]byte(`{"results":[{"score_id":"score-001","accepted":true,"status":"accepted"}],"accepted":1,"duplicates":0,"rejected":0}`))
			return
		}
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
}

func TestSink_Write_SendsGenerations(t *testing.T) {
	var reqs []capturedRequest
	srv := testServer(t, &reqs)
	defer srv.Close()

	sink, err := New(srv.URL, "tenant-123", "super-secret-token", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	gen := nativesigil.Generation{
		ID:             "gen-001",
		ConversationID: "conv-abc",
		OperationName:  "generateText",
		Mode:           "SYNC",
		Provider:       "anthropic",
		Model:          "claude-opus-4-5",
		Usage: nativesigil.Usage{
			Input:  100,
			Output: 200,
			Total:  300,
		},
	}

	exports := []nativesigil.Export{
		{
			Generations: []nativesigil.Generation{gen},
			ConvKey:     "conv-abc",
		},
	}

	if err := sink.Write(context.Background(), exports); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Should have exactly one request (generations only; no steps or scores)
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}

	req := reqs[0]

	// Path
	if req.path != "/api/v1/generations:export" {
		t.Errorf("path: want /api/v1/generations:export, got %q", req.path)
	}

	// Auth header: Basic base64(tenant:token)
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("tenant-123:super-secret-token"))
	if req.auth != wantAuth {
		t.Errorf("Authorization: want %q, got %q", wantAuth, req.auth)
	}

	// Body contains the generation
	body := string(req.body)
	if !strings.Contains(body, `"gen-001"`) {
		t.Errorf("body missing generation id: %s", body)
	}
	if !strings.Contains(body, `"conv-abc"`) {
		t.Errorf("body missing conversation_id: %s", body)
	}
	if !strings.Contains(body, `"GENERATION_MODE_SYNC"`) {
		t.Errorf("body missing mode enum string: %s", body)
	}
	// operation_name must use snake_case (UseProtoNames:true)
	if !strings.Contains(body, `"operation_name"`) {
		t.Errorf("body missing operation_name (snake_case): %s", body)
	}
}

func TestSink_Write_SendsWorkflowStepsAndScores(t *testing.T) {
	var reqs []capturedRequest
	srv := testServer(t, &reqs)
	defer srv.Close()

	sink, err := New(srv.URL, "tenant-123", "tok", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	numVal := 0.95
	exports := []nativesigil.Export{
		{
			WorkflowSteps: []nativesigil.WorkflowStep{
				{ID: "step-001", ConversationID: "conv-abc", StepName: "route"},
			},
			Scores: []nativesigil.Score{
				{ScoreID: "score-001", GenerationID: "gen-001", Number: &numVal},
			},
			ConvKey: "conv-abc",
		},
	}

	if err := sink.Write(context.Background(), exports); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(reqs) != 2 {
		t.Fatalf("expected 2 requests (workflow-steps + scores), got %d", len(reqs))
	}

	paths := make(map[string]bool)
	for _, r := range reqs {
		paths[r.path] = true
	}
	if !paths["/api/v1/workflow-steps:export"] {
		t.Error("missing /api/v1/workflow-steps:export")
	}
	if !paths["/api/v1/scores:export"] {
		t.Error("missing /api/v1/scores:export")
	}
}

// TestSink_Write_ScoreIncludesRequiredEvaluatorVersion models the strict Sigil
// score-ingest validation. The real contract requires evaluator_version, but the
// native score producer intentionally does not need to know transport defaults.
func TestSink_Write_ScoreIncludesRequiredEvaluatorVersion(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/scores:export" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		scores, _ := got["scores"].([]any)
		if len(scores) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"exactly one score is required"}`))
			return
		}
		score, _ := scores[0].(map[string]any)
		if score["evaluator_version"] == nil || score["evaluator_version"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"evaluator_version is required"}`))
			return
		}
		if _, legacy := score["has_passed"]; legacy {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unknown field has_passed"}`))
			return
		}
		if passed, ok := score["passed"].(bool); !ok || passed {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"explicit passed false was not preserved"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"results":[{"score_id":"score-001","accepted":true,"status":"accepted"}],"accepted":1,"duplicates":0,"rejected":0}`))
	}))
	defer srv.Close()

	sink, err := New(srv.URL, "tenant-123", "tok", false)
	if err != nil {
		t.Fatal(err)
	}
	value := 0.95
	err = sink.Write(context.Background(), []nativesigil.Export{{
		ConvKey: "conv-abc",
		Scores: []nativesigil.Score{{
			ScoreID: "score-001", GenerationID: "gen-001", EvaluatorID: "helpfulness", ScoreKey: "helpfulness", Number: &value,
			HasPassed: true, Passed: false,
		}},
	}})
	if err != nil {
		t.Fatalf("strict Sigil score ingest rejected the exact payload: HTTP 400 evaluator_version is required; payload=%v; error=%v", got, err)
	}
}

func TestSink_Write_RejectsScoreResponseWithRejectedItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/scores:export" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"results":[{"score_id":"score-001","accepted":false,"status":"rejected","error":"evaluator_version is required"}],"accepted":0,"duplicates":0,"rejected":1}`))
	}))
	defer srv.Close()

	sink, err := New(srv.URL, "tenant-123", "tok", false)
	if err != nil {
		t.Fatal(err)
	}
	value := 0.95
	err = sink.Write(context.Background(), []nativesigil.Export{{
		ConvKey: "conv-abc",
		Scores:  []nativesigil.Score{{ScoreID: "score-001", GenerationID: "gen-001", EvaluatorID: "helpfulness", ScoreKey: "helpfulness", Number: &value}},
	}})
	if got := operationalerr.CodeOf(err); got != operationalerr.CodeRejected {
		t.Fatalf("202 response with rejected score produced code %q, want %q", got, operationalerr.CodeRejected)
	}
}

func TestSink_Write_RejectsScoreAcknowledgementsForDifferentIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"results":[{"score_id":"score-001","accepted":true,"status":"accepted"},{"score_id":"score-001","accepted":true,"status":"accepted"}],"accepted":2,"duplicates":0,"rejected":0}`))
	}))
	defer srv.Close()

	sink, err := New(srv.URL, "tenant-123", "tok", false)
	if err != nil {
		t.Fatal(err)
	}
	value := 0.95
	err = sink.Write(context.Background(), []nativesigil.Export{{
		ConvKey: "conv-abc",
		Scores: []nativesigil.Score{
			{ScoreID: "score-001", GenerationID: "gen-001", EvaluatorID: "helpfulness", ScoreKey: "helpfulness", Number: &value},
			{ScoreID: "score-002", GenerationID: "gen-002", EvaluatorID: "helpfulness", ScoreKey: "helpfulness", Number: &value},
		},
	}})
	if got := operationalerr.CodeOf(err); got != operationalerr.CodeRejected {
		t.Fatalf("mismatched score acknowledgements produced code %q, want %q", got, operationalerr.CodeRejected)
	}
}

func TestSink_Write_PreservesHTTPStatusWhenResponseIsOversized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBodyBytes+1)))
	}))
	defer srv.Close()

	sink, err := New(srv.URL, "tenant-123", "tok", false)
	if err != nil {
		t.Fatal(err)
	}
	var observed pushhook.Event
	sink.Observe = func(_ context.Context, event pushhook.Event) { observed = event }
	err = sink.Write(context.Background(), []nativesigil.Export{{Generations: []nativesigil.Generation{{ID: "gen-001", Mode: "SYNC"}}}})
	if err == nil {
		t.Fatal("oversized response was accepted")
	}
	if observed.Status != http.StatusAccepted {
		t.Fatalf("observed status=%d, want %d", observed.Status, http.StatusAccepted)
	}
}

// TestSink_SustainedCombinedQueuesDeliverWithoutLoss runs bounded repeated
// windows for the Sigil and a second independent delivery lane. It guards the
// B10 false-pass where Sigil score rejection made its queue report sustained
// loss even though the other lane kept making progress.
func TestSink_SustainedCombinedQueuesDeliverWithoutLoss(t *testing.T) {
	var mu sync.Mutex
	acceptedScores := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/scores:export" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var request struct {
			Scores []struct {
				ScoreID          string `json:"score_id"`
				EvaluatorVersion string `json:"evaluator_version"`
			} `json:"scores"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		results := make([]map[string]any, 0, len(request.Scores))
		for _, score := range request.Scores {
			if score.EvaluatorVersion == "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"evaluator_version is required"}`))
				return
			}
			results = append(results, map[string]any{"score_id": score.ScoreID, "accepted": true, "status": "accepted"})
		}
		mu.Lock()
		acceptedScores += len(request.Scores)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "accepted": len(results), "duplicates": 0, "rejected": 0})
	}))
	defer srv.Close()

	sink, err := New(srv.URL, "tenant-123", "tok", false)
	if err != nil {
		t.Fatal(err)
	}
	var sigilEvents, metricsEvents []queue.FlushEvent
	obs := func(events *[]queue.FlushEvent) queue.Observer {
		return queueObserver{flushed: func(event queue.FlushEvent) {
			mu.Lock()
			*events = append(*events, event)
			mu.Unlock()
		}}
	}
	sigilQueue := queue.New(queue.Options{Shards: 1, BatchMax: 4, Deadline: time.Hour, Capacity: 64, Sink: "sigil"}, sink.Write, func(nativesigil.Export) uint64 { return 0 }, obs(&sigilEvents))
	metricsDelivered := 0
	metricsQueue := queue.New(queue.Options{Shards: 1, BatchMax: 8, Deadline: time.Hour, Capacity: 64, Sink: "metrics"}, func(_ context.Context, batch []int) error {
		mu.Lock()
		metricsDelivered += len(batch)
		mu.Unlock()
		return nil
	}, func(int) uint64 { return 0 }, obs(&metricsEvents))

	const windows = 12
	for window := 0; window < windows; window++ {
		exports := make([]nativesigil.Export, 0, 4)
		for i := 0; i < 4; i++ {
			value := float64(window*4+i) / 100
			exports = append(exports, nativesigil.Export{ConvKey: "conv", Scores: []nativesigil.Score{{
				ScoreID: fmt.Sprintf("score-%d-%d", window, i), GenerationID: "gen", EvaluatorID: "helpfulness", ScoreKey: "helpfulness", Number: &value,
			}}})
		}
		if err := sigilQueue.Write(context.Background(), exports); err != nil {
			t.Fatal(err)
		}
		if err := metricsQueue.Write(context.Background(), []int{window*8 + 1, window*8 + 2, window*8 + 3, window*8 + 4, window*8 + 5, window*8 + 6, window*8 + 7, window*8 + 8}); err != nil {
			t.Fatal(err)
		}
		if err := sigilQueue.Flush(context.Background()); err != nil {
			t.Fatalf("sigil window %d: %v", window, err)
		}
		if err := metricsQueue.Flush(context.Background()); err != nil {
			t.Fatalf("metrics window %d: %v", window, err)
		}
	}
	sigilQueue.Drain(context.Background())
	metricsQueue.Drain(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if acceptedScores != windows*4 || metricsDelivered != windows*8 {
		t.Fatalf("delivered scores=%d metrics=%d, want %d/%d", acceptedScores, metricsDelivered, windows*4, windows*8)
	}
	for _, event := range append(sigilEvents, metricsEvents...) {
		if event.Dropped != 0 || event.Code != operationalerr.CodeNone {
			t.Fatalf("delivery loss event: %+v", event)
		}
	}
	if sigilQueue.Depth() != 0 || metricsQueue.Depth() != 0 {
		t.Fatalf("queues did not drain: sigil=%d metrics=%d", sigilQueue.Depth(), metricsQueue.Depth())
	}
}

type queueObserver struct{ flushed func(queue.FlushEvent) }

func (o queueObserver) EnqueueBlocked(string, time.Duration) {}
func (o queueObserver) FlushObserved(event queue.FlushEvent) {
	if o.flushed != nil {
		o.flushed(event)
	}
}

func TestSink_Write_DryRun_DoesNotHitServer(t *testing.T) {
	var reqs []capturedRequest
	srv := testServer(t, &reqs)
	defer srv.Close()

	sink, err := New(srv.URL, "tenant-123", "tok", true /* dryRun */)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var observed pushhook.Event
	sink.Observe = func(_ context.Context, event pushhook.Event) { observed = event }

	gen := nativesigil.Generation{
		ID:             "gen-dry-001",
		ConversationID: "conv-dry",
		Mode:           "SYNC",
	}
	numVal := 1.0
	exports := []nativesigil.Export{
		{
			Generations: []nativesigil.Generation{gen, gen},
			Scores: []nativesigil.Score{
				{ScoreID: "s1", Number: &numVal},
				{ScoreID: "s2", Number: &numVal},
				{ScoreID: "s3", Number: &numVal},
			},
			WorkflowSteps: []nativesigil.WorkflowStep{
				{ID: "ws-1"},
			},
			ConvKey: "conv-dry",
		},
	}

	if err := sink.Write(context.Background(), exports); err != nil {
		t.Fatalf("Write (dry-run): %v", err)
	}

	// Server must not have been hit
	if len(reqs) != 0 {
		t.Errorf("dry-run: expected 0 server requests, got %d", len(reqs))
	}

	// Inventory must reflect the counts
	inv := sink.Inventory()
	if inv.Generations != 2 {
		t.Errorf("Inventory.Generations: want 2, got %d", inv.Generations)
	}
	if inv.Scores != 3 {
		t.Errorf("Inventory.Scores: want 3, got %d", inv.Scores)
	}
	if inv.WorkflowSteps != 1 {
		t.Errorf("Inventory.WorkflowSteps: want 1, got %d", inv.WorkflowSteps)
	}
	if observed.Sink != "sigil" || observed.Items != 6 || !observed.DryRun || observed.ErrorCode != operationalerr.CodeNone {
		t.Fatalf("observed = %+v", observed)
	}
}

func TestSink_Write_ObservesSanitizedFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`reflected-secret`))
	}))
	defer srv.Close()
	sink, err := New(srv.URL, "tenant", "reflected-secret", false)
	if err != nil {
		t.Fatal(err)
	}
	var observed pushhook.Event
	sink.Observe = func(_ context.Context, event pushhook.Event) { observed = event }
	err = sink.Write(context.Background(), []nativesigil.Export{{Generations: []nativesigil.Generation{{ID: "g1", Mode: "SYNC"}}}})
	if operationalerr.CodeOf(err) != operationalerr.CodeAuthentication {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "reflected-secret") {
		t.Fatal("returned error leaked secret")
	}
	if observed.Sink != "sigil" || observed.Items != 0 || observed.Status != http.StatusUnauthorized || observed.ErrorCode != operationalerr.CodeAuthentication {
		t.Fatalf("observed = %+v", observed)
	}
}

func TestSink_Write_ReportsOnlyItemsDeliveredBeforeFailure(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	sink, err := New(srv.URL, "tenant", "token", false)
	if err != nil {
		t.Fatal(err)
	}
	var observed pushhook.Event
	sink.Observe = func(_ context.Context, event pushhook.Event) { observed = event }
	err = sink.Write(context.Background(), []nativesigil.Export{{
		Generations:   []nativesigil.Generation{{ID: "g1", Mode: "SYNC"}},
		WorkflowSteps: []nativesigil.WorkflowStep{{ID: "s1"}},
	}})
	if operationalerr.CodeOf(err) != operationalerr.CodeAuthentication {
		t.Fatalf("error = %v", err)
	}
	if observed.Items != 1 || observed.ErrorCode != operationalerr.CodeAuthentication {
		t.Fatalf("observed = %+v", observed)
	}
}

func TestSink_Write_AuthHeader(t *testing.T) {
	var reqs []capturedRequest
	srv := testServer(t, &reqs)
	defer srv.Close()

	tenantID := "my-tenant"
	token := "my-secret-token"

	sink, err := New(srv.URL, tenantID, token, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	exports := []nativesigil.Export{
		{
			Generations: []nativesigil.Generation{
				{ID: "g1", Mode: "SYNC"},
			},
		},
	}

	if err := sink.Write(context.Background(), exports); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}

	// Verify Basic auth = base64(tenantID:token)
	expectedCreds := tenantID + ":" + token
	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(expectedCreds))
	if reqs[0].auth != expectedAuth {
		t.Errorf("Authorization header:\n  want: %q\n   got: %q", expectedAuth, reqs[0].auth)
	}
}

func TestSink_New_EmptyEndpointError(t *testing.T) {
	_, err := New("", "tenant", "token", false)
	if err == nil {
		t.Error("expected error for empty endpoint, got nil")
	}
}

func TestSink_Write_SkipsEmptySlices(t *testing.T) {
	var reqs []capturedRequest
	srv := testServer(t, &reqs)
	defer srv.Close()

	sink, err := New(srv.URL, "t", "k", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Empty export — no sub-slices populated
	exports := []nativesigil.Export{{ConvKey: "cv"}}

	if err := sink.Write(context.Background(), exports); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Nothing to send — server should not be called
	if len(reqs) != 0 {
		t.Errorf("expected 0 requests for empty export, got %d", len(reqs))
	}
}
