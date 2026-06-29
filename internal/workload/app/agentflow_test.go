// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/semconv"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

func testFlowReq(spanID string) *ledger.Request {
	r := &ledger.Request{Model: "claude-3.5-sonnet", Provider: "bedrock"}
	r.CorrelationID = "corr-1"
	r.RequestID = "req-1"
	r.SessionID = "sess-1"
	r.TraceID = "trace-1"
	r.SpanID = spanID
	return r
}

func testFlow() *AgenticFlow {
	return &AgenticFlow{
		Workflow: "assistant-graph",
		Agents: []AgentDecl{
			{Name: "planner", Tools: []string{"doc_search", "retriever"}},
			{Name: "checker", Tools: []string{"validator"}},
		},
	}
}

// byName indexes emitted spans by name for assertions.
func byName(spans []otlp.Span) map[string]otlp.Span {
	m := make(map[string]otlp.Span, len(spans))
	for _, s := range spans {
		m[s.Name] = s
	}
	return m
}

func TestEmitAgentFlowTree(t *testing.T) {
	start := time.Unix(1700000000, 0)
	end := start.Add(2 * time.Second)
	spans := emitAgentFlow(testFlow(), testFlowReq("span-aaaa"), "PARENT", start, end)
	if len(spans) == 0 {
		t.Fatal("no spans emitted")
	}
	m := byName(spans)

	// workflow span: child of PARENT, INTERNAL, gen_ai workflow attrs.
	wf, ok := m["invoke_workflow assistant-graph"]
	if !ok {
		t.Fatalf("missing invoke_workflow span; got %v", names(spans))
	}
	if wf.ParentID != "PARENT" {
		t.Errorf("workflow ParentID = %q, want PARENT", wf.ParentID)
	}
	if wf.Kind != otlp.KindInternal {
		t.Errorf("workflow kind = %v, want Internal", wf.Kind)
	}
	if wf.Attrs[genai.AttrOperationName] != genai.OpInvokeWorkflow || wf.Attrs[genai.AttrWorkflowName] != "assistant-graph" {
		t.Errorf("workflow attrs wrong: %v", wf.Attrs)
	}

	// exactly one invoke_agent span, parented to the workflow span.
	var agent otlp.Span
	agentCount := 0
	for _, s := range spans {
		if s.Attrs[genai.AttrOperationName] == genai.OpInvokeAgent {
			agent = s
			agentCount++
		}
	}
	if agentCount != 1 {
		t.Fatalf("invoke_agent count = %d, want 1", agentCount)
	}
	if agent.ParentID != wf.SpanID {
		t.Errorf("agent ParentID = %q, want workflow span id %q", agent.ParentID, wf.SpanID)
	}
	if agent.Kind != otlp.KindInternal {
		t.Errorf("agent kind = %v, want Internal", agent.Kind)
	}
	agentName, _ := agent.Attrs[genai.AttrAgentName].(string)
	if agentName != "planner" && agentName != "checker" {
		t.Errorf("agent name = %q, want planner|checker", agentName)
	}

	// ≥1 execute_tool spans, parented to the agent span, tool ∈ that agent's pool.
	pool := map[string][]string{"planner": {"doc_search", "retriever"}, "checker": {"validator"}}[agentName]
	toolCount := 0
	for _, s := range spans {
		if s.Attrs[genai.AttrOperationName] != genai.OpExecuteTool {
			continue
		}
		toolCount++
		if s.ParentID != agent.SpanID {
			t.Errorf("tool ParentID = %q, want agent span id %q", s.ParentID, agent.SpanID)
		}
		if s.Kind != otlp.KindInternal {
			t.Errorf("tool kind = %v, want Internal", s.Kind)
		}
		tn, _ := s.Attrs[genai.AttrToolName].(string)
		if !contains(pool, tn) {
			t.Errorf("tool %q not in agent %q pool %v", tn, agentName, pool)
		}
	}
	if toolCount < 1 {
		t.Errorf("execute_tool count = %d, want ≥1", toolCount)
	}

	// exactly one chat leaf, parented to the agent, CLIENT, model/provider/token attrs.
	chat, ok := m["chat claude-3.5-sonnet"]
	if !ok {
		t.Fatalf("missing chat span; got %v", names(spans))
	}
	if chat.ParentID != agent.SpanID {
		t.Errorf("chat ParentID = %q, want agent span id %q", chat.ParentID, agent.SpanID)
	}
	if chat.Kind != otlp.KindClient {
		t.Errorf("chat kind = %v, want Client", chat.Kind)
	}
	if chat.Attrs[genai.AttrRequestModel] != "claude-3.5-sonnet" || chat.Attrs[genai.AttrProviderName] != "bedrock" {
		t.Errorf("chat model/provider wrong: %v", chat.Attrs)
	}
	if toInt(chat.Attrs[genai.AttrInputTokens]) <= 0 || toInt(chat.Attrs[genai.AttrOutputTokens]) <= 0 {
		t.Errorf("chat token attrs not positive: in=%v out=%v", chat.Attrs[genai.AttrInputTokens], chat.Attrs[genai.AttrOutputTokens])
	}

	// every span: shares trace id, carries the 3 correlation keys, NO content keys, valid interval.
	for _, s := range spans {
		if s.TraceID != "trace-1" {
			t.Errorf("span %q TraceID = %q, want trace-1", s.Name, s.TraceID)
		}
		for _, k := range []string{semconv.AttrCorrelationID, "request_id", "session_id"} {
			if _, ok := s.Attrs[k]; !ok {
				t.Errorf("span %q missing correlation key %q", s.Name, k)
			}
		}
		for k := range s.Attrs {
			if genai.IsContentKey(k) {
				t.Errorf("span %q stamps content key %q", s.Name, k)
			}
		}
		if !s.End.After(s.Start) {
			t.Errorf("span %q has non-positive duration (%v..%v)", s.Name, s.Start, s.End)
		}
		if s.Start.Before(start) || s.End.After(end) {
			t.Errorf("span %q [%v..%v] outside parent window [%v..%v]", s.Name, s.Start, s.End, start, end)
		}
	}
}

func TestEmitAgentFlowDeterministicAndVarying(t *testing.T) {
	start := time.Unix(1700000000, 0)
	end := start.Add(2 * time.Second)
	// same SpanID → identical agent + tool selection (names structure stable).
	a1 := names(emitAgentFlow(testFlow(), testFlowReq("span-x"), "P", start, end))
	a2 := names(emitAgentFlow(testFlow(), testFlowReq("span-x"), "P", start, end))
	if !equalStrs(a1, a2) {
		t.Errorf("same SpanID produced different span-name sets:\n %v\n %v", a1, a2)
	}
	// scan SpanIDs; at least one differs in selection from span-x (proves per-request variation).
	differs := false
	for _, sid := range []string{"span-y", "span-z", "span-q", "span-w", "span-7"} {
		if !equalStrs(a1, names(emitAgentFlow(testFlow(), testFlowReq(sid), "P", start, end))) {
			differs = true
			break
		}
	}
	if !differs {
		t.Error("no SpanID varied agent/tool selection — draw is not request-sensitive")
	}
}

func TestEmitAgentFlowOmitChat(t *testing.T) {
	start := time.Unix(1700000000, 0)
	end := start.Add(2 * time.Second)
	f := testFlow()
	f.OmitChat = true
	for _, s := range emitAgentFlow(f, testFlowReq("span-x"), "P", start, end) {
		if s.Attrs[genai.AttrOperationName] == genai.OpChat {
			t.Errorf("OmitChat=true but a chat span was emitted: %q", s.Name)
		}
	}
}

// --- tiny test helpers ---

func names(spans []otlp.Span) []string {
	out := make([]string, len(spans))
	for i, s := range spans {
		out[i] = s.Name
	}
	return out
}
func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func toInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}
