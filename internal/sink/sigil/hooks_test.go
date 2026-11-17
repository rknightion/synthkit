// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/pushhook"
	nativesigil "github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
)

// Vendored from the agento11y SDK at the contract pin; see testdata/hooks/PROVENANCE.txt.
const (
	fixturePreflightSHA256 = "54bf35f1a475bb0b5025b6402a0b100ea4e342e7be939efc9061407a870c9369"
	fixtureResponsesSHA256 = "12880fe2e189aea721fb233fe5db3a97f003dea1f63cde687fe96009bac6df22"
)

func hookFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "hooks", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// canonicalJSON re-marshals a document with sorted keys and no insignificant whitespace.
// The SDK conformance README compares parsed structures, not raw bytes, so this is the
// strictest comparison the contract allows: equality of these bytes is equality of the
// structure, with no normalization of values.
func canonicalJSON(t *testing.T, data []byte) []byte {
	t.Helper()
	var doc any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHookFixturesMatchPinnedHashes(t *testing.T) {
	for name, want := range map[string]string{"request-preflight.json": fixturePreflightSHA256, "responses.json": fixtureResponsesSHA256} {
		sum := sha256.Sum256(hookFixture(t, name))
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Fatalf("%s sha256 = %s, want the vendored SDK 75a1c548 hash %s; never edit a conformance fixture", name, got, want)
		}
	}
}

// TestHookRequestWireMatchesPreflightFixture decodes the SDK's preflight body into the
// wire types with unknown fields rejected, then re-encodes it. Equality proves every
// fixture field has a field here and each field carries the fixture's encoding: kinds,
// embedded JSON for tool payloads, base64 for tool schemas.
func TestHookRequestWireMatchesPreflightFixture(t *testing.T) {
	fixture := hookFixture(t, "request-preflight.json")
	var req hookEvaluateRequest
	dec := json.NewDecoder(bytes.NewReader(fixture))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("fixture has a field the wire types do not model: %v", err)
	}
	got, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonicalJSON(t, got), canonicalJSON(t, fixture)) {
		t.Fatalf("re-encoded fixture differs:\n got %s\nwant %s", canonicalJSON(t, got), canonicalJSON(t, fixture))
	}
}

// TestHookRequestBuiltFromGoValuesMatchesPreflightFixture builds the SDK's body from typed
// values rather than from the fixture itself, so the encoders (not just the tags) are checked.
func TestHookRequestBuiltFromGoValuesMatchesPreflightFixture(t *testing.T) {
	schema := func(s string) []byte { return []byte(s) }
	req := hookEvaluateRequest{
		Phase: "preflight",
		Context: hookContext{
			AgentName: "conformance-agent", AgentVersion: "1.2.3",
			Model:          &hookModel{Provider: "anthropic", Name: "claude-sonnet-4"},
			Tags:           map[string]string{"env": "test", "team": "agent-observability"},
			ConversationID: "conv-hooks-conformance",
			TraceID:        "0123456789abcdef0123456789abcdef",
			SpanID:         "0123456789abcdef",
		},
		Input: hookInput{
			SystemPrompt: "You are a careful assistant.",
			Messages: []hookMessage{
				{Role: "user", Parts: []hookPart{{Kind: "text", Text: "Delete the cache directory under /tmp."}}},
				{Role: "assistant", Parts: []hookPart{
					{Kind: "thinking", Thinking: "The request is destructive, so inspect the directory first."},
					{Kind: "tool_call", ToolCall: &hookToolCall{ID: "call-read", Name: "read_file", InputJSON: json.RawMessage(`{"path":"/tmp/cache/manifest.json"}`)}},
					{Kind: "tool_call", ToolCall: &hookToolCall{ID: "call-bash", Name: "Bash", InputJSON: json.RawMessage(`{"command":"rm -rf /tmp/cache"}`)}},
				}},
				{Role: "tool", Name: "read_file", Parts: []hookPart{{Kind: "tool_result", ToolResult: &hookToolResult{
					ToolCallID: "call-read", Name: "read_file", Content: "3 entries", ContentJSON: hookRequestPayloadJSON([]byte(`{"entries":3}`)),
				}}}},
				{Role: "tool", Name: "Bash", Parts: []hookPart{{Kind: "tool_result", ToolResult: &hookToolResult{
					ToolCallID: "call-bash", Name: "Bash", IsError: true,
					Content: "rm: cannot remove '/tmp/cache': Permission denied", ContentJSON: hookRequestPayloadJSON([]byte(`{"exit_code":1}`)),
				}}}},
			},
			Tools: []hookTool{
				{Name: "Bash", Description: "Run a shell command.", Type: "function", InputSchemaJSON: schema(`{"type":"object","properties":{"command":{"type":"string","description":"Shell command to run."}},"required":["command"]}`)},
				{Name: "read_file", Description: "Read a file from disk.", Type: "function", InputSchemaJSON: schema(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
			},
			ConversationPreview: "user: Delete the cache directory under /tmp.",
		},
	}
	got, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	want := hookFixture(t, "request-preflight.json")
	if !bytes.Equal(canonicalJSON(t, got), canonicalJSON(t, want)) {
		t.Fatalf("request differs from the SDK fixture:\n got %s\nwant %s", canonicalJSON(t, got), canonicalJSON(t, want))
	}
}

type hookServer struct {
	srv     *httptest.Server
	mu      sync.Mutex
	bodies  [][]byte
	headers []http.Header
	paths   []string
	methods []string
}

// newHookServer replies to every request with status and body.
func newHookServer(t *testing.T, status int, body string) *hookServer {
	t.Helper()
	hs := &hookServer{}
	hs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		hs.mu.Lock()
		hs.bodies = append(hs.bodies, data)
		hs.headers = append(hs.headers, r.Header.Clone())
		hs.paths = append(hs.paths, r.URL.Path)
		hs.methods = append(hs.methods, r.Method)
		hs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(hs.srv.Close)
	return hs
}

func (hs *hookServer) count() int {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	return len(hs.bodies)
}

func newTestHooks(t *testing.T, url string) *Hooks {
	t.Helper()
	h, err := NewHooks(url, "tenant-1", "tok-secret")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// fixtureStage and fixtureToolResult are the SDK fixture's context and its Bash tool result,
// the one a planner produces and BeforeToolResult guards.
func fixtureStage() nativesigil.GenerationStage {
	return nativesigil.GenerationStage{
		GenerationID: "gen-1", ConversationID: "conv-hooks-conformance",
		TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef",
		AgentName: "conformance-agent", AgentVersion: "1.2.3",
		Provider: "anthropic", Model: "claude-sonnet-4",
	}
}

func fixtureToolResult() nativesigil.ToolResult {
	return nativesigil.ToolResult{
		ToolCallID: "call-bash", Name: "Bash", IsError: true,
		Content:     "rm: cannot remove '/tmp/cache': Permission denied",
		ContentJSON: []byte(`{"exit_code":1}`),
	}
}

// TestBeforeToolResultSendsFixtureShapedBody drives the real transport and compares the body
// that reaches the server with the matching slice of the SDK fixture: the fixture's context
// (minus tags, which the stage does not carry) and its Bash tool-result message.
func TestBeforeToolResultSendsFixtureShapedBody(t *testing.T) {
	var fixture struct {
		Phase   string                     `json:"phase"`
		Context map[string]json.RawMessage `json:"context"`
		Input   struct {
			Messages []json.RawMessage `json:"messages"`
		} `json:"input"`
	}
	if err := json.Unmarshal(hookFixture(t, "request-preflight.json"), &fixture); err != nil {
		t.Fatal(err)
	}
	delete(fixture.Context, "tags")
	want, err := json.Marshal(map[string]any{
		"phase":   fixture.Phase,
		"context": fixture.Context,
		"input":   map[string]any{"messages": []json.RawMessage{fixture.Input.Messages[3]}},
	})
	if err != nil {
		t.Fatal(err)
	}

	hs := newHookServer(t, 200, `{"action":"allow","evaluations":[]}`)
	h := newTestHooks(t, hs.srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	decision, err := h.BeforeToolResult(ctx, fixtureStage(), fixtureToolResult())
	if err != nil || !decision.Allowed || decision.Transformed != nil {
		t.Fatalf("decision = %+v, err = %v; want a plain allow", decision, err)
	}
	if hs.count() != 1 {
		t.Fatalf("requests = %d, want exactly 1", hs.count())
	}
	if !bytes.Equal(canonicalJSON(t, hs.bodies[0]), canonicalJSON(t, want)) {
		t.Fatalf("body differs from the SDK fixture slice:\n got %s\nwant %s", canonicalJSON(t, hs.bodies[0]), canonicalJSON(t, want))
	}
	hdr := hs.headers[0]
	if hs.methods[0] != http.MethodPost || hs.paths[0] != "/api/v1/hooks:evaluate" {
		t.Fatalf("request = %s %s", hs.methods[0], hs.paths[0])
	}
	if hdr.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("tenant-1:tok-secret")) || hdr.Get("X-Scope-OrgID") != "tenant-1" || hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("auth headers = %v", hdr)
	}
	ms, err := strconv.Atoi(hdr.Get("X-Agento11y-Hook-Timeout-Ms"))
	if err != nil || ms < 1 || ms > 7000 {
		t.Fatalf("timeout header = %q, want the caller's remaining budget (1..7000 ms)", hdr.Get("X-Agento11y-Hook-Timeout-Ms"))
	}
}

func TestHookTimeoutHeaderDefaultsAndClamps(t *testing.T) {
	if ms, err := hookTimeoutMillis(context.Background(), 0); err != nil || ms != 15000 {
		t.Fatalf("no deadline, no client timeout: %d, %v; want the SDK default 15000", ms, err)
	}
	if ms, err := hookTimeoutMillis(context.Background(), 4*time.Second); err != nil || ms != 4000 {
		t.Fatalf("no deadline: %d, %v; want the client timeout 4000", ms, err)
	}
	long, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	if ms, err := hookTimeoutMillis(long, time.Hour); err != nil || ms != 119999 {
		t.Fatalf("1h deadline and timeout: %d, %v; want the server maximum 119999", ms, err)
	}
	if ms, err := hookTimeoutMillis(long, 2*time.Second); err != nil || ms != 2000 {
		t.Fatalf("1h deadline, 2s client timeout: %d, %v; want the smaller, 2000", ms, err)
	}
	past, cancelPast := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelPast()
	if _, err := hookTimeoutMillis(past, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired deadline err = %v", err)
	}
}

// TestHookTimeoutHeaderIsBoundedByTheClientHTTPTimeout sends through the real transport: a
// caller with a long budget must not advertise more than the client will wait.
func TestHookTimeoutHeaderIsBoundedByTheClientHTTPTimeout(t *testing.T) {
	hs := newHookServer(t, 200, `{"action":"allow"}`)
	h := newTestHooks(t, hs.srv.URL)
	h.SetDelivery(httpretry.Delivery{Gate: ha.AlwaysLeader{}, HTTPTimeout: 3 * time.Second, RetryMaxElapsed: 3 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	if _, err := h.BeforeToolResult(ctx, fixtureStage(), fixtureToolResult()); err != nil {
		t.Fatal(err)
	}
	ms, err := strconv.Atoi(hs.headers[0].Get("X-Agento11y-Hook-Timeout-Ms"))
	if err != nil || ms < 1 || ms > 3000 {
		t.Fatalf("timeout header = %q, want at most the 3000 ms HTTP timeout", hs.headers[0].Get("X-Agento11y-Hook-Timeout-Ms"))
	}
	// A shorter caller budget still wins.
	short, cancelShort := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancelShort()
	if _, err := h.BeforeToolResult(short, fixtureStage(), fixtureToolResult()); err != nil {
		t.Fatal(err)
	}
	if ms, err := strconv.Atoi(hs.headers[1].Get("X-Agento11y-Hook-Timeout-Ms")); err != nil || ms < 1 || ms > 500 {
		t.Fatalf("timeout header = %q, want at most the caller's 500 ms", hs.headers[1].Get("X-Agento11y-Hook-Timeout-Ms"))
	}
}

// TestHookRefusalsBeforeSendingEmitNoEvent: the Observe field documents one event per
// admitted evaluation request, so a refusal before any send is silent.
func TestHookRefusalsBeforeSendingEmitNoEvent(t *testing.T) {
	hs := newHookServer(t, 200, `{"action":"allow"}`)
	h := newTestHooks(t, hs.srv.URL)
	var events int
	h.Observe = func(context.Context, pushhook.Event) { events++ }

	oversized := fixtureToolResult()
	oversized.Content = strings.Repeat("x", maxHookRequestBytes)
	if _, err := h.BeforeToolResult(context.Background(), fixtureStage(), oversized); err == nil {
		t.Fatal("oversized body must be refused")
	}
	// A deadline under 1 ms is a refusal, not a request.
	tiny, cancel := context.WithTimeout(context.Background(), 200*time.Microsecond)
	defer cancel()
	if _, err := h.BeforeToolResult(tiny, fixtureStage(), fixtureToolResult()); err == nil {
		t.Fatal("a sub-millisecond budget must be refused")
	}
	if events != 0 || hs.count() != 0 {
		t.Fatalf("events = %d, requests = %d; refusals before sending must be silent", events, hs.count())
	}
	// Control: an admitted request does emit.
	if _, err := h.BeforeToolResult(context.Background(), fixtureStage(), fixtureToolResult()); err != nil || events != 1 {
		t.Fatalf("admitted request: err = %v, events = %d", err, events)
	}
}

func TestHookRequestKeepsUnparsablePayloadVisibleAsString(t *testing.T) {
	hs := newHookServer(t, 200, `{"action":"allow"}`)
	h := newTestHooks(t, hs.srv.URL)
	r := fixtureToolResult()
	r.ContentJSON = []byte(`{"partial":`)
	if _, err := h.BeforeToolResult(context.Background(), fixtureStage(), r); err != nil {
		t.Fatal(err)
	}
	var sent struct {
		Input struct {
			Messages []struct {
				Parts []struct {
					ToolResult struct {
						ContentJSON string `json:"content_json"`
					} `json:"tool_result"`
				} `json:"parts"`
			} `json:"messages"`
		} `json:"input"`
	}
	if err := json.Unmarshal(hs.bodies[0], &sent); err != nil {
		t.Fatal(err)
	}
	if got := sent.Input.Messages[0].Parts[0].ToolResult.ContentJSON; got != `{"partial":` {
		t.Fatalf("content_json = %q, want the unparsable text as a JSON string", got)
	}
}

func TestHookResponseFixturesMapToDecisions(t *testing.T) {
	var responses map[string]json.RawMessage
	if err := json.Unmarshal(hookFixture(t, "responses.json"), &responses); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		check func(*testing.T, nativesigil.ToolResultDecision)
	}{
		{"allow", func(t *testing.T, d nativesigil.ToolResultDecision) {
			if !d.Allowed || d.Transformed != nil {
				t.Fatalf("decision = %+v, want allow with no transform", d)
			}
		}},
		{"deny", func(t *testing.T, d nativesigil.ToolResultDecision) {
			if d.Allowed || d.Transformed != nil {
				t.Fatalf("decision = %+v, want a not-allowed zero decision", d)
			}
		}},
		{"allow_with_transformed_input", func(t *testing.T, d nativesigil.ToolResultDecision) {
			if !d.Allowed || d.Transformed == nil {
				t.Fatalf("decision = %+v, want allow with the transformed Bash result", d)
			}
			tr := d.Transformed
			if tr.ToolCallID != "call-bash" || tr.Name != "Bash" || !tr.IsError ||
				tr.Content != "rm: cannot remove '/tmp/cache': Permission denied" || string(tr.ContentJSON) != `{"exit_code":1}` {
				t.Fatalf("transformed = %+v (content_json %s)", tr, tr.ContentJSON)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, ok := responses[tc.name]
			if !ok {
				t.Fatalf("fixture lacks %q", tc.name)
			}
			hs := newHookServer(t, 200, string(raw))
			d, err := newTestHooks(t, hs.srv.URL).BeforeToolResult(context.Background(), fixtureStage(), fixtureToolResult())
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, d)
		})
	}
}

func TestHookDecisionsFailClosed(t *testing.T) {
	bashResult := func(content string) string {
		return `{"kind":"tool_result","tool_result":{"tool_call_id":"call-bash","name":"Bash","content":"` + content + `"}}`
	}
	message := func(parts ...string) string {
		return `{"role":"tool","name":"Bash","parts":[` + strings.Join(parts, ",") + `]}`
	}
	cases := []struct {
		name string
		body string
	}{
		{"empty action", `{}`},
		{"unknown action", `{"action":"warn"}`},
		{"uppercase action", `{"action":"ALLOW"}`},
		{"transform with no tool result", `{"action":"allow","transformed_input":{"messages":[{"role":"user","parts":[{"kind":"text","text":"x"}]}]}}`},
		{"transform with another call id", `{"action":"allow","transformed_input":{"messages":[` + message(`{"kind":"tool_result","tool_result":{"tool_call_id":"call-other","name":"Bash"}}`) + `]}}`},
		{"transform with another tool name", `{"action":"allow","transformed_input":{"messages":[` + message(`{"kind":"tool_result","tool_result":{"tool_call_id":"call-bash","name":"Other"}}`) + `]}}`},
		{"transform with two matches", `{"action":"allow","transformed_input":{"messages":[` + message(bashResult("a"), bashResult("b")) + `]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hs := newHookServer(t, 200, tc.body)
			d, err := newTestHooks(t, hs.srv.URL).BeforeToolResult(context.Background(), fixtureStage(), fixtureToolResult())
			if err != nil || d.Allowed || d.Transformed != nil {
				t.Fatalf("decision = %+v, err = %v; want a zero decision and no error (fail_open must not reopen it)", d, err)
			}
		})
	}
}

func TestHookEmptyTransformedInputIsNotATransform(t *testing.T) {
	hs := newHookServer(t, 200, `{"action":"allow","transformed_input":{},"evaluations":[]}`)
	d, err := newTestHooks(t, hs.srv.URL).BeforeToolResult(context.Background(), fixtureStage(), fixtureToolResult())
	if err != nil || !d.Allowed || d.Transformed != nil {
		t.Fatalf("decision = %+v, err = %v", d, err)
	}
}

func TestHookTransformedPayloadDecodingFollowsSDKRule(t *testing.T) {
	// base64 of non-JSON text is kept as a JSON string; a plain JSON string stays a document.
	cases := []struct{ name, raw, want string }{
		{"base64 json", `"eyJhIjoxfQ=="`, `{"a":1}`},
		{"base64 text", `"aGVsbG8gd29ybGQ="`, `"hello world"`},
		{"embedded json", `{"a":1}`, `{"a":1}`},
		{"non-base64 text", `"not base64 !"`, `"not base64 !"`},
		{"empty string", `""`, ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"action":"allow","transformed_input":{"messages":[{"role":"tool","name":"Bash","parts":[{"kind":"tool_result","tool_result":{"tool_call_id":"call-bash","name":"Bash","content_json":` + tc.raw + `}}]}]}}`
			hs := newHookServer(t, 200, body)
			d, err := newTestHooks(t, hs.srv.URL).BeforeToolResult(context.Background(), fixtureStage(), fixtureToolResult())
			if err != nil || !d.Allowed || d.Transformed == nil {
				t.Fatalf("decision = %+v, err = %v", d, err)
			}
			if got := string(d.Transformed.ContentJSON); got != tc.want {
				t.Fatalf("content_json = %q, want %q", got, tc.want)
			}
			if len(d.Transformed.ContentJSON) > 0 && !json.Valid(d.Transformed.ContentJSON) {
				t.Fatalf("content_json %q is not valid JSON", d.Transformed.ContentJSON)
			}
		})
	}
}

func TestHookTransportFailuresAreErrorsNotDecisions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"server error", 500, `internal error with SECRET-CONTENT`},
		{"bad request", 400, `invalid body with SECRET-CONTENT`},
		{"unauthorized", 401, ``},
		{"accepted is not ok", 202, `{"action":"allow"}`},
		{"undecodable", 200, `not json SECRET-CONTENT`},
		{"empty body", 200, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hs := newHookServer(t, tc.status, tc.body)
			d, err := newTestHooks(t, hs.srv.URL).BeforeToolResult(context.Background(), fixtureStage(), fixtureToolResult())
			if err == nil || d.Allowed || d.Transformed != nil {
				t.Fatalf("decision = %+v, err = %v; want an error and a zero decision", d, err)
			}
			if strings.Contains(err.Error(), "SECRET-CONTENT") || strings.Contains(err.Error(), "rm: cannot remove") {
				t.Fatalf("error leaks content: %v", err)
			}
			if hs.count() != 1 {
				t.Fatalf("requests = %d; a verdict must never be retried", hs.count())
			}
		})
	}
}

func TestHookOversizedResponseIsAnError(t *testing.T) {
	hs := newHookServer(t, 200, `{"action":"allow","pad":"`+strings.Repeat("x", maxHookResponseBytes)+`"}`)
	if _, err := newTestHooks(t, hs.srv.URL).BeforeToolResult(context.Background(), fixtureStage(), fixtureToolResult()); err == nil {
		t.Fatal("a response over 4 MiB must be an error")
	}
}

func TestHookOversizedRequestIsRefusedBeforeAnyRequest(t *testing.T) {
	hs := newHookServer(t, 200, `{"action":"allow"}`)
	r := fixtureToolResult()
	r.Content = strings.Repeat("x", maxHookRequestBytes)
	if _, err := newTestHooks(t, hs.srv.URL).BeforeToolResult(context.Background(), fixtureStage(), r); err == nil || hs.count() != 0 {
		t.Fatalf("err = %v, requests = %d; want a refusal with no request", err, hs.count())
	}
}

func TestHookContextExpiryIsTerminalAndSilent(t *testing.T) {
	hs := newHookServer(t, 200, `{"action":"allow"}`)
	h := newTestHooks(t, hs.srv.URL)
	var events []pushhook.Event
	h.Observe = func(_ context.Context, ev pushhook.Event) { events = append(events, ev) }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if d, err := h.BeforeToolResult(ctx, fixtureStage(), fixtureToolResult()); !errors.Is(err, context.Canceled) || d.Allowed {
		t.Fatalf("decision = %+v, err = %v", d, err)
	}
	if hs.count() != 0 || len(events) != 0 {
		t.Fatalf("requests = %d, events = %d; a cancelled call must do nothing", hs.count(), len(events))
	}
}

func TestHookTimeoutSurfacesContextDeadline(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(slow.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	d, err := newTestHooks(t, slow.URL).BeforeToolResult(ctx, fixtureStage(), fixtureToolResult())
	if err == nil || d.Allowed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("decision = %+v, err = %v; want the caller's deadline", d, err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("returned after %v; the caller's deadline was not honoured", time.Since(start))
	}
}

func TestHookNotLeaderIsFencedWithoutARequest(t *testing.T) {
	hs := newHookServer(t, 200, `{"action":"allow"}`)
	h := newTestHooks(t, hs.srv.URL)
	h.SetDelivery(httpretry.Delivery{Gate: notLeaderGate{}, HTTPTimeout: time.Second, RetryMaxElapsed: time.Second})
	d, err := h.BeforeToolResult(context.Background(), fixtureStage(), fixtureToolResult())
	if !errors.Is(err, ha.ErrNotLeader) || d.Allowed || hs.count() != 0 {
		t.Fatalf("decision = %+v, err = %v, requests = %d", d, err, hs.count())
	}
}

type notLeaderGate struct{}

func (notLeaderGate) Role() ha.Role { return ha.RoleStandby }
func (notLeaderGate) Do(context.Context, ha.Operation, func(context.Context) error) error {
	return ha.ErrNotLeader
}

func TestHookAfterGenerationSendsNothing(t *testing.T) {
	hs := newHookServer(t, 200, `{"action":"deny"}`)
	h := newTestHooks(t, hs.srv.URL)
	if err := h.AfterGeneration(context.Background(), nativesigil.Generation{ID: "g"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.AfterGeneration(ctx, nativesigil.Generation{ID: "g"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled AfterGeneration err = %v", err)
	}
	if hs.count() != 0 {
		t.Fatalf("AfterGeneration sent %d requests; D19 admits no postflight call", hs.count())
	}
}

func TestHookObserverReportsOutcomesWithoutContent(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantItems  int
		wantStatus int
		wantCode   operationalerr.Code
	}{
		{"allow", 200, `{"action":"allow"}`, 1, 200, operationalerr.CodeNone},
		{"deny", 200, `{"action":"deny","rule_id":"r","reason":"SECRET-CONTENT"}`, 1, 200, operationalerr.CodeNone},
		{"server error", 503, `SECRET-CONTENT`, 0, 503, operationalerr.CodeRejected},
		{"unauthorized", 401, `SECRET-CONTENT`, 0, 401, operationalerr.CodeAuthentication},
		{"undecodable", 200, `SECRET-CONTENT`, 0, 200, operationalerr.CodeRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hs := newHookServer(t, tc.status, tc.body)
			h := newTestHooks(t, hs.srv.URL)
			var events []pushhook.Event
			h.Observe = func(_ context.Context, ev pushhook.Event) { events = append(events, ev) }
			_, _ = h.BeforeToolResult(context.Background(), fixtureStage(), fixtureToolResult())
			if len(events) != 1 {
				t.Fatalf("events = %d, want exactly 1", len(events))
			}
			ev := events[0]
			if ev.Sink != "sigil_hooks" || ev.Items != tc.wantItems || ev.Status != tc.wantStatus || ev.ErrorCode != tc.wantCode || ev.DryRun || ev.Duration < 0 {
				t.Fatalf("event = %+v", ev)
			}
			if got := (pushhook.Acknowledgement{}); ev.Acknowledgement != got {
				t.Fatalf("hook events must not claim a delivery acknowledgement: %+v", ev.Acknowledgement)
			}
		})
	}
}

func TestHooksConcurrentUse(t *testing.T) {
	hs := newHookServer(t, 200, `{"action":"allow"}`)
	h := newTestHooks(t, hs.srv.URL)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d, err := h.BeforeToolResult(context.Background(), fixtureStage(), fixtureToolResult()); err != nil || !d.Allowed {
				t.Errorf("decision = %+v, err = %v", d, err)
			}
		}()
	}
	wg.Wait()
	if hs.count() != 16 {
		t.Fatalf("requests = %d, want 16", hs.count())
	}
}

func TestNewHooksAndRatingsRejectBadEndpoints(t *testing.T) {
	for _, endpoint := range []string{"", "   ", "no-scheme.example", "ftp://example.test", "http://"} {
		if _, err := NewHooks(endpoint, "t", "k"); err == nil {
			t.Fatalf("NewHooks(%q) accepted", endpoint)
		}
		if _, err := NewRatings(endpoint, "t", "k"); err == nil {
			t.Fatalf("NewRatings(%q) accepted", endpoint)
		}
	}
	if h, err := NewHooks("https://example.test/", "t", "k"); err != nil || h.endpoint != "https://example.test" {
		t.Fatalf("trailing slash: %+v, %v", h, err)
	}
}

func TestHookRedirectIsNeverFollowed(t *testing.T) {
	var target atomicCounter
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { target.inc() }))
	t.Cleanup(elsewhere.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)
	if d, err := newTestHooks(t, redirector.URL).BeforeToolResult(context.Background(), fixtureStage(), fixtureToolResult()); err == nil || d.Allowed {
		t.Fatalf("decision = %+v, err = %v; a redirect is not a verdict", d, err)
	}
	if target.get() != 0 {
		t.Fatal("the redirect target received a request carrying credentials")
	}
}

type atomicCounter struct {
	mu sync.Mutex
	n  int
}

func (c *atomicCounter) inc() { c.mu.Lock(); c.n++; c.mu.Unlock() }
func (c *atomicCounter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// TestSynthkitNeverEmitsBackendHookFamilies is the doc-0004 producer rule as a test: the
// backend emits every agento11y_hook_* series and every guard workflow step from the real
// call, and synthkit only triggers it. No non-test source may name the family, and the
// hook and rating clients must send nothing but their own two routes.
func TestSynthkitNeverEmitsBackendHookFamilies(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	needles := []string{"agento11y_hook_", "agento11y.guard"}
	var hits []string
	scanned := 0
	for _, dir := range []string{"cmd", "internal", "e2e"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			scanned++
			for _, needle := range needles {
				if strings.Contains(string(data), needle) {
					hits = append(hits, strings.TrimPrefix(path, root+string(filepath.Separator))+" ("+needle+")")
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Positive control: an empty walk (wrong root, renamed directories) must not pass silently.
	if scanned < 50 {
		t.Fatalf("scanned only %d non-test .go files under %s; the scan is not looking at the tree", scanned, root)
	}
	if len(hits) != 0 {
		t.Fatalf("non-test source names a backend-only family literal %v: %v", needles, hits)
	}

	// Behavioural half: one hook call and one rating write reach only their own routes.
	seen := map[string]int{}
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Method+" "+r.URL.Path]++
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/ratings") {
			_, _ = w.Write([]byte(`{"rating":{"rating_id":"rat-1","conversation_id":"conv-1","rating":"CONVERSATION_RATING_VALUE_GOOD"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"action":"deny"}`))
	}))
	t.Cleanup(srv.Close)
	if _, err := newTestHooks(t, srv.URL).BeforeToolResult(context.Background(), fixtureStage(), fixtureToolResult()); err != nil {
		t.Fatal(err)
	}
	ratings, err := NewRatings(srv.URL, "t", "k")
	if err != nil {
		t.Fatal(err)
	}
	if err := ratings.WriteConversationRating(context.Background(), "conv-1", nativesigil.ConversationRatingInput{RatingID: "rat-1", Rating: nativesigil.ConversationRatingValueGood}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := map[string]int{"POST /api/v1/hooks:evaluate": 1, "POST /api/v1/conversations/conv-1/ratings": 1}
	if len(seen) != len(want) {
		t.Fatalf("routes hit = %v, want only %v", seen, want)
	}
	for route, n := range want {
		if seen[route] != n {
			t.Fatalf("routes hit = %v, want %v", seen, want)
		}
	}
}
