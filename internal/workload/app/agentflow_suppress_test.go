// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// A backend node composing the gen_ai_client profile AND an agentic_flow must emit EXACTLY ONE chat
// span (the agent-flow leaf, parented under invoke_agent) — the profile's flat chat SpanSpec is
// suppressed — while the gen_ai_client METRICS still emit.
func TestAgentFlowSuppressesProfileChatSpan(t *testing.T) {
	cfg := &Config{
		Traffic: Traffic{OffPeakRPS: 20, PeakRPS: 50},
		Models:  []ModelChoice{{Model: "gpt-4o", Provider: "azure-openai"}},
		Services: []ServiceNode{
			{Name: "web-fe", Type: "frontend", Entry: true, Calls: []string{"backend"}},
			{
				Name: "backend", Type: "web", Runtime: "python",
				Profiles: []string{"gen_ai_client"},
				AgenticFlow: &AgenticFlow{
					Workflow: "assistant-graph",
					Agents:   []AgentDecl{{Name: "planner", Tools: []string{"doc_search"}}},
				},
			},
		},
	}
	w := buildApp(t, cfg)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, &coretest.LogCapture{}, &coretest.TraceCapture{})
	r := w.m.mintOne(now, world.Shape)
	tc := world.Traces.(*coretest.TraceCapture)
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	// Exactly one chat span across all resources, parented under the invoke_agent span.
	var chats []otlp.Span
	var agentSpanID string
	for _, res := range tc.Resources {
		for _, sp := range res.Spans {
			switch sp.Attrs[genai.AttrOperationName] {
			case genai.OpChat:
				chats = append(chats, sp)
			case genai.OpInvokeAgent:
				agentSpanID = sp.SpanID
			}
		}
	}
	if len(chats) != 1 {
		t.Fatalf("chat span count = %d, want exactly 1 (profile chat must be suppressed)", len(chats))
	}
	if chats[0].ParentID != agentSpanID {
		t.Errorf("chat ParentID = %q, want invoke_agent span %q (the flow leaf, not the flat profile child)", chats[0].ParentID, agentSpanID)
	}

	// gen_ai_client metrics still emit (the profile's MetricSpecs are kept by the suppression).
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	found := false
	for _, n := range mc.Names() {
		if strings.HasPrefix(n, "gen_ai_client_token_usage") { // histogram → _count/_sum/_bucket suffixes
			found = true
			break
		}
	}
	if !found {
		t.Errorf("gen_ai_client_token_usage* metric missing — suppression wrongly dropped profile metrics; got %v", mc.Names())
	}
}
