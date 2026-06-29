// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// aiBinding is a web_service binding whose downstream hops form a nested AI chain:
// backend → workflow → agent → gateway → model (gpt-4o), plus a second bedrock model hop
// under the workflow. Exercises every AI stamper + the Path-B gateway span + bedrock logs.
func aiBinding() core.Binding {
	return core.Binding{
		Name:    "test-api",
		Env:     coretest.Env(),
		Cluster: coretest.Cluster(),
		Calls: []fixture.CallTarget{
			{Kind: fixture.KindWorkflow, AI: &fixture.AICall{Op: genai.OpInvokeWorkflow, Subject: "rag-pipeline"}, ParentHop: -1},
			{Kind: fixture.KindAgent, AI: &fixture.AICall{Op: genai.OpInvokeAgent, Subject: "planner"}, ParentHop: 0},
			{Kind: fixture.KindLLMGateway, AI: &fixture.AICall{Op: genai.OpChat, Subject: "portkey", Model: "gpt-4o", Provider: "azure-openai"}, ParentHop: 1},
			{Kind: fixture.KindLLMModel, AI: &fixture.AICall{Op: genai.OpChat, Model: "gpt-4o", Provider: "azure-openai"}, ParentHop: 2},
			{Kind: fixture.KindLLMModel, AI: &fixture.AICall{Op: genai.OpChat, Model: "amazon.titan-text", Provider: "bedrock"}, ParentHop: 0},
		},
	}
}

func buildAIWS(t *testing.T) (*Workload, *ledger.Ledger) {
	t.Helper()
	cfg := NewConfig().(*Config)
	cfg.Tracing = true
	w, err := build(cfg, aiBinding())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	eng := shape.New("", nil)
	led := ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())
	return w.(*Workload), led
}

// TestAIHopTraces (Task 11): AI hops emit gen_ai.* CLIENT spans with the right SpanName +
// attrs; the gateway hop emits a connected SERVER span (Path-B) in its own resource carrying
// portkey_trace_id; NO content-strip key ever appears.
func TestAIHopTraces(t *testing.T) {
	w, led := buildAIWS(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	batch := mintNonEmpty(t, led, now, false)

	tc := &coretest.TraceCapture{}
	world := coretest.World(nil, nil, tc)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	var sawWorkflow, sawAgent, sawGatewayClient, sawGatewayServer, sawModel bool
	for _, res := range tc.Resources {
		svc, _ := res.Attrs["service.name"].(string)
		for _, sp := range res.Spans {
			// No content key may ever appear on any span.
			for k := range sp.Attrs {
				if genai.IsContentKey(k) {
					t.Fatalf("span %q emitted content-strip key %q", sp.Name, k)
				}
			}
			op, _ := sp.Attrs[genai.AttrOperationName].(string)
			switch sp.Name {
			case "invoke_workflow rag-pipeline":
				sawWorkflow = true
				if op != genai.OpInvokeWorkflow {
					t.Errorf("workflow span op=%q", op)
				}
				if sp.Attrs[genai.AttrWorkflowName] != "rag-pipeline" {
					t.Errorf("workflow span missing gen_ai.workflow.name")
				}
			case "invoke_agent planner":
				sawAgent = true
				if sp.Attrs[genai.AttrAgentName] != "planner" {
					t.Errorf("agent span missing gen_ai.agent.name")
				}
			case "chat gpt-4o":
				// Could be the gateway CLIENT span (svc=test-api), the model CLIENT span, or
				// the gateway SERVER span (svc=portkey).
				if sp.Kind == otlp.KindServer && svc == "portkey" {
					sawGatewayServer = true
					if sp.Attrs["portkey_trace_id"] == nil || sp.Attrs["portkey_trace_id"] == "" {
						t.Errorf("gateway SERVER span missing portkey_trace_id")
					}
					if sp.Attrs[genai.AttrProviderName] != "azure-openai" {
						t.Errorf("gateway SERVER span missing provider")
					}
				}
				if sp.Kind == otlp.KindClient {
					if sp.Attrs[genai.AttrRequestModel] == "gpt-4o" {
						sawModel = true
					}
					if svc == "test-api" && sp.Attrs[genai.AttrProviderName] == "azure-openai" {
						sawGatewayClient = true
					}
				}
			}
		}
	}
	if !sawWorkflow || !sawAgent || !sawGatewayClient || !sawGatewayServer || !sawModel {
		t.Fatalf("missing AI spans: workflow=%v agent=%v gwClient=%v gwServer=%v model=%v",
			sawWorkflow, sawAgent, sawGatewayClient, sawGatewayServer, sawModel)
	}
}

// TestAIGenAIMetrics (Task 12): the metric lane emits gen_ai_client_*/gen_ai_server_*
// histograms with the right labels, AND the AI hops' span-metric CLIENT rows carry a
// gen_ai-shaped span_name (NOT "SELECT <model>" — the C2 trap).
func TestAIGenAIMetrics(t *testing.T) {
	w, _ := buildAIWS(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Client histograms present (expand to _bucket/_sum/_count).
	for _, base := range []string{
		genai.MetricClientTokenUsage, genai.MetricClientOpDuration,
		genai.MetricClientTTFC, genai.MetricClientTimePerOutputChunk,
		genai.MetricServerRequestDuration,
	} {
		if got := mc.Find(base + "_bucket"); len(got) == 0 {
			t.Errorf("missing gen_ai histogram %q", base+"_bucket")
		}
	}

	// token_usage carries operation/provider/request_model + token_type ∈ {input,output}.
	tt := map[string]bool{}
	for _, s := range mc.Find(genai.MetricClientTokenUsage + "_bucket") {
		tt[s.Labels[genai.LabelTokenType]] = true
		if s.Labels[genai.LabelOperationName] == "" || s.Labels[genai.LabelProviderName] == "" {
			t.Errorf("token_usage missing gen_ai labels: %v", s.Labels)
		}
	}
	if !tt[genai.TokenTypeInput] || !tt[genai.TokenTypeOutput] {
		t.Errorf("token_usage missing input/output token_type series: %v", tt)
	}

	// C2: no AI hop's span-metric CLIENT row may carry a "SELECT <model>" span_name; the
	// chat hops must read "chat gpt-4o", the agent "invoke_agent planner", etc.
	sawChatSpanName := false
	for _, s := range mc.Find("traces_spanmetrics_calls_total") {
		name := s.Labels["span_name"]
		if name == "SELECT gpt-4o" || name == "GET gpt-4o" || name == "SELECT amazon.titan-text" {
			t.Fatalf("AI hop got db-shaped span_name %q (C2 regression)", name)
		}
		if name == "chat gpt-4o" {
			sawChatSpanName = true
		}
	}
	if !sawChatSpanName {
		t.Error("no CLIENT span-metric row with span_name=\"chat gpt-4o\"")
	}
}

// TestAIServiceGraphNesting (Task 14): the emitted service-graph honors ParentHop, so a
// model nested via a gateway emits gateway→model (not backend→model).
func TestAIServiceGraphNesting(t *testing.T) {
	w, _ := buildAIWS(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	edges := map[[2]string]bool{}
	for _, s := range mc.Find("traces_service_graph_request_total") {
		edges[[2]string{s.Labels["client"], s.Labels["server"]}] = true
	}
	want := [][2]string{
		{"test-api", "rag-pipeline"}, // backend → workflow (ParentHop -1)
		{"rag-pipeline", "planner"},  // workflow → agent
		{"planner", "portkey"},       // agent → gateway
		{"portkey", "gpt-4o"},        // gateway → model (the nesting proof)
	}
	for _, e := range want {
		if !edges[e] {
			t.Errorf("missing nested service-graph edge %s→%s (got %v)", e[0], e[1], edges)
		}
	}
}

// TestAICorrelatedLogs (Task 13): the three AI log streams are emitted, each carrying the
// request-correlation keys in structured metadata; no high-card key is ever a stream label.
func TestAICorrelatedLogs(t *testing.T) {
	w, led := buildAIWS(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	batch := mintNonEmpty(t, led, now, false)

	lc := &coretest.LogCapture{}
	world := coretest.World(nil, lc, nil)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	highCard := map[string]bool{
		"trace_id": true, "span_id": true, "correlation_id": true,
		"portkey_trace_id": true, "run_id": true, "request_id": true, "session_id": true,
	}
	bySource := map[string]int{}
	sawRunID := false
	for _, st := range lc.Streams {
		src := st.Labels["source"]
		if src != "portkey" && src != "bedrock_invocation" && src != "langsmith-runs" {
			continue // app / callee streams handled by other tests
		}
		// No high-card key may be a stream label.
		for k := range st.Labels {
			if highCard[k] {
				t.Fatalf("source=%s stream label %q is high-card (must be structured metadata)", src, k)
			}
		}
		for _, ln := range st.Lines {
			bySource[src]++
			if ln.Meta["portkey_trace_id"] == "" || ln.Meta["trace_id"] == "" || ln.Meta["correlation_id"] == "" {
				t.Errorf("source=%s line missing request-correlation meta: %v", src, ln.Meta)
			}
			if src == "langsmith-runs" && ln.Meta["run_id"] != "" {
				sawRunID = true
			}
		}
	}
	for _, src := range []string{"portkey", "bedrock_invocation", "langsmith-runs"} {
		if bySource[src] == 0 {
			t.Errorf("no %s log lines emitted", src)
		}
	}
	if !sawRunID {
		t.Error("langsmith-runs line missing run_id in structured metadata")
	}
}

// TestPortkeyRetryFallbackRealism verifies that synthRetryFallback produces a realistic
// distribution across a large batch: majority retry_count==0 (≥60%), at least one
// retry_count>0, and at least one fallback==true.
func TestPortkeyRetryFallbackRealism(t *testing.T) {
	w, led := buildAIWS(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	// Accumulate portkey lines across enough batches to get a statistically meaningful sample.
	var allBodies []map[string]any
	const wantLines = 50
	for len(allBodies) < wantLines {
		batch := mintNonEmpty(t, led, now, false)
		now = now.Add(time.Minute) // advance clock so each Mint produces a fresh batch

		lc := &coretest.LogCapture{}
		world := coretest.World(nil, lc, nil)
		if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
			t.Fatalf("ProjectBatch: %v", err)
		}
		for _, st := range lc.Streams {
			if st.Labels["source"] != "portkey" {
				continue
			}
			for _, ln := range st.Lines {
				var body map[string]any
				if err := json.Unmarshal([]byte(ln.Body), &body); err != nil {
					t.Fatalf("portkey body not valid JSON: %v — body=%q", err, ln.Body)
				}
				allBodies = append(allBodies, body)
			}
		}
	}

	var zeroCount, nonZeroCount, fallbackCount int
	for _, body := range allBodies {
		rc, ok := body["retry_count"].(float64)
		if !ok {
			t.Fatalf("retry_count missing or wrong type in body: %v", body)
		}
		if rc == 0 {
			zeroCount++
		} else {
			nonZeroCount++
		}
		fb, ok := body["fallback"].(bool)
		if !ok {
			t.Fatalf("fallback missing or wrong type in body: %v", body)
		}
		if fb {
			fallbackCount++
		}
	}

	total := len(allBodies)
	if nonZeroCount == 0 {
		t.Error("all portkey lines have retry_count==0; expected some retry_count>0 (~10%)")
	}
	if fallbackCount == 0 {
		t.Error("no portkey line has fallback==true; expected ~3%")
	}
	zeroPct := float64(zeroCount) / float64(total)
	if zeroPct < 0.60 {
		t.Errorf("retry_count==0 rate = %.1f%%, want >60%% (got %d/%d)", zeroPct*100, zeroCount, total)
	}
}
