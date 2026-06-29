// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// agentFlowCfg: fe (entry) → backend (web, with an agentic_flow). The backend is reached as a CALLEE
// so its structural span is the SERVER span; the agent-flow subtree must hang under it.
func agentFlowCfg(omitChat bool) *Config {
	return &Config{
		Traffic: Traffic{OffPeakRPS: 20, PeakRPS: 50},
		Models:  []ModelChoice{{Model: "claude-3.5-sonnet", Provider: "bedrock"}},
		Services: []ServiceNode{
			{Name: "web-fe", Type: "frontend", Entry: true, Calls: []string{"backend"}},
			{
				Name: "backend", Type: "web", Runtime: "python",
				AgenticFlow: &AgenticFlow{
					Workflow: "assistant-graph",
					OmitChat: omitChat,
					Agents: []AgentDecl{
						{Name: "planner", Tools: []string{"doc_search", "retriever"}},
						{Name: "checker", Tools: []string{"validator"}},
					},
				},
			},
		},
	}
}

func TestBackendEmitsAgentFlowSpans(t *testing.T) {
	w := buildApp(t, agentFlowCfg(false))
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	world := coretest.World(&coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{})
	r := w.m.mintOne(now, world.Shape)
	tc := world.Traces.(*coretest.TraceCapture)
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	// Collect backend-resource spans + locate the backend SERVER span.
	var backendSpans []otlp.Span
	var serverSpanID string
	for _, res := range tc.Resources {
		svc, _ := res.Attrs["service.name"].(string)
		if svc != "backend" {
			continue
		}
		for _, sp := range res.Spans {
			backendSpans = append(backendSpans, sp)
			if sp.Kind == otlp.KindServer {
				serverSpanID = sp.SpanID
			}
		}
	}
	if serverSpanID == "" {
		t.Fatal("no backend SERVER span")
	}

	ops := map[string]otlp.Span{}
	for _, sp := range backendSpans {
		if op, _ := sp.Attrs[genai.AttrOperationName].(string); op != "" {
			ops[op] = sp
		}
		if sp.TraceID != r.TraceID {
			t.Errorf("agent-flow span %q traceID=%s, want %s", sp.Name, sp.TraceID, r.TraceID)
		}
	}
	for _, op := range []string{genai.OpInvokeWorkflow, genai.OpInvokeAgent, genai.OpExecuteTool, genai.OpChat} {
		if _, ok := ops[op]; !ok {
			t.Errorf("backend missing a %q span", op)
		}
	}
	// workflow span parents to the backend SERVER span (the nesting root).
	if wf := ops[genai.OpInvokeWorkflow]; wf.ParentID != serverSpanID {
		t.Errorf("invoke_workflow ParentID=%q, want backend SERVER span %q", wf.ParentID, serverSpanID)
	}
	// kinds: workflow/agent/tool INTERNAL, chat CLIENT.
	for _, op := range []string{genai.OpInvokeWorkflow, genai.OpInvokeAgent, genai.OpExecuteTool} {
		if ops[op].Kind != otlp.KindInternal {
			t.Errorf("%s kind=%v, want Internal", op, ops[op].Kind)
		}
	}
	if ops[genai.OpChat].Kind != otlp.KindClient {
		t.Errorf("chat kind=%v, want Client", ops[genai.OpChat].Kind)
	}
}

func TestBackendAgentFlowOmitChat(t *testing.T) {
	w := buildApp(t, agentFlowCfg(true))
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	world := coretest.World(&coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{})
	r := w.m.mintOne(now, world.Shape)
	tc := world.Traces.(*coretest.TraceCapture)
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}
	for _, res := range tc.Resources {
		for _, sp := range res.Spans {
			if op, _ := sp.Attrs[genai.AttrOperationName].(string); op == genai.OpChat {
				t.Errorf("OmitChat=true but emitted a chat span %q", sp.Name)
			}
		}
	}
}
