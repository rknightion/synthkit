// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	nativesigil "github.com/rknightion/synthkit/internal/sigil"
	sigilv1 "github.com/rknightion/synthkit/internal/sink/sigil/v1"
)

func TestCurrentLaneAGoldenPayloads(t *testing.T) {
	var requests []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Error(err)
			return
		}
		requests = append(requests, payload)
		w.WriteHeader(http.StatusAccepted)
		if r.URL.Path == "/api/v1/scores:export" {
			_, _ = w.Write([]byte(`{"accepted":1,"results":[{"score_id":"score","accepted":true}]}`))
		}
	}))
	defer srv.Close()
	sink, err := New(srv.URL, "tenant", "test-token", false)
	if err != nil {
		t.Fatal(err)
	}
	value := false
	batch := nativesigil.Export{
		Generations: []nativesigil.Generation{{ID: "g", ConversationID: "c", Mode: "SYNC", OperationName: "generateText", Provider: "anthropic", Model: "model", AgentName: "agent", AgentVersion: "v1", Input: []nativesigil.Message{{Role: "MESSAGE_ROLE_SYSTEM", Parts: []nativesigil.Part{{Text: "instructions"}}}, {Role: "MESSAGE_ROLE_USER", Parts: []nativesigil.Part{{Media: &nativesigil.Media{Kind: "image", URL: "https://example.com/image.png", MIMEType: "image/png", Name: "sample"}}}}}, Usage: nativesigil.Usage{Input: 20, Output: 8, Total: 28, CacheRead: 10, CacheWrite: 2, Reasoning: 3, InputSemantics: "TOKEN_INPUT_SEMANTICS_INCLUSIVE"}, Metadata: map[string]any{"agento11y.sdk.name": "sdk-go", "agento11y.user.id": "user"}}},
		Scores:      []nativesigil.Score{{ScoreID: " score ", GenerationID: "g", EvaluatorID: "check", ScoreKey: "safe", Bool: &value, HasPassed: true, Passed: false, ReportRole: "diagnostic"}},
	}
	if err := sink.Write(context.Background(), []nativesigil.Export{batch}); err != nil {
		t.Fatal(err)
	}
	wantJSON := []string{
		`{"generations":[{"id":"g","conversation_id":"c","mode":"GENERATION_MODE_SYNC","operation_name":"generateText","model":{"provider":"anthropic","name":"model"},"agent_name":"agent","agent_version":"v1","input":[{"role":"MESSAGE_ROLE_SYSTEM","parts":[{"text":"instructions"}]},{"role":"MESSAGE_ROLE_USER","parts":[{"media":{"kind":"image","url":"https://example.com/image.png","mime_type":"image/png","name":"sample"}}]}],"usage":{"input_tokens":"20","output_tokens":"8","total_tokens":"28","cache_read_input_tokens":"10","cache_write_input_tokens":"2","reasoning_tokens":"3","input_semantics":"TOKEN_INPUT_SEMANTICS_INCLUSIVE"},"metadata":{"agento11y.sdk.name":"sdk-go","agento11y.user.id":"user"}}]}`,
		`{"scores":[{"score_id":"score","generation_id":"g","evaluator_id":"check","evaluator_version":"synthkit-v1","score_key":"safe","value":{"bool":false},"passed":false,"report_role":"diagnostic"}]}`,
	}
	if len(requests) != len(wantJSON) {
		t.Fatalf("requests=%d, want 2", len(requests))
	}
	for i, text := range wantJSON {
		var want map[string]any
		if err := json.Unmarshal([]byte(text), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(requests[i], want) {
			got, _ := json.Marshal(requests[i])
			t.Errorf("Lane A golden %d\ngot %s\nwant %s", i, got, text)
		}
	}
}

func TestCurrentExporterContract(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-Scope-OrgID") != "tenant" {
			t.Errorf("missing Basic-mode tenant header: %v", r.Header)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	sink, err := New(srv.URL, "tenant", "test-token", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Write(context.Background(), []nativesigil.Export{{Generations: []nativesigil.Generation{{ID: "g"}}}}); err != nil {
		t.Fatal(err)
	}
	if got := string((&sigilv1.Generation{}).ProtoReflect().Descriptor().FullName()); got != "agento11y.v1.Generation" {
		t.Errorf("generation namespace = %s", got)
	}
	before := calls.Load()
	for _, id := range []string{strings.Repeat("x", 129), "__grafana_reserved", "\u200b__GRAFÁNA_reserved", "＿＿grafana_reserved"} {
		value := true
		err := sink.Write(context.Background(), []nativesigil.Export{{Scores: []nativesigil.Score{{ScoreID: id, EvaluatorID: "check", ScoreKey: "safe", Bool: &value}}}})
		if err == nil {
			t.Errorf("invalid score id admitted: %q", id)
		}
	}
	if calls.Load() != before {
		t.Errorf("invalid score payload reached HTTP: %d calls", calls.Load()-before)
	}
}
