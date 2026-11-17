// SPDX-License-Identifier: AGPL-3.0-only

package openrouter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rknightion/synthkit/internal/chatsim/director"
)

// The test server is a loopback httptest server: no external network is touched.
func TestRequestShapeAndRetry(t *testing.T) {
	var bodies []map[string]any
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("path %s auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		bodies = append(bodies, m)
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"lines\":[],\"beats_done\":[]}"}}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`)
	}))
	defer srv.Close()
	temp := 0.7
	c, err := New(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "vendor/model-1", Temperature: &temp, MaxTokens: 400, ReasoningEffort: "none", ZDR: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Complete(context.Background(), director.Prompt{System: "s", User: "u", SchemaName: "director_turn", Schema: map[string]any{"type": "object"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != `{"lines":[],"beats_done":[]}` || calls != 2 || c.Usage().PromptTokens != 10 {
		t.Fatalf("out=%+v calls=%d usage=%+v", out, calls, c.Usage())
	}
	b := bodies[1]
	prov := b["provider"].(map[string]any)
	if prov["require_parameters"] != true || prov["data_collection"] != "deny" || prov["zdr"] != true {
		t.Fatalf("provider = %v", prov)
	}
	rf := b["response_format"].(map[string]any)
	js := rf["json_schema"].(map[string]any)
	if rf["type"] != "json_schema" || js["strict"] != true || js["name"] != "director_turn" {
		t.Fatalf("response_format = %v", rf)
	}
	if b["reasoning"].(map[string]any)["effort"] != "none" || b["model"] != "vendor/model-1" {
		t.Fatalf("body = %v", b)
	}
}

func TestNoRetryOn400AndOmitReasoning(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if _, ok := m["reasoning"]; ok {
			t.Errorf("reasoning sent despite omit")
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":400,"message":"no endpoints support the requested parameters"}}`)
	}))
	defer srv.Close()
	c, _ := New(Config{APIKey: "k", BaseURL: srv.URL, Model: "m", ReasoningEffort: "omit"})
	if _, err := c.Complete(context.Background(), director.Prompt{}); err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d, want one failed call", err, calls)
	}
}
