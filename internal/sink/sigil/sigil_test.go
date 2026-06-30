// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nativesigil "github.com/rknightion/synthkit/internal/sigil"
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

func TestSink_Write_DryRun_DoesNotHitServer(t *testing.T) {
	var reqs []capturedRequest
	srv := testServer(t, &reqs)
	defer srv.Close()

	sink, err := New(srv.URL, "tenant-123", "tok", true /* dryRun */)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

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
