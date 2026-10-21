// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"

	"slices"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sigil"
	"gopkg.in/yaml.v3"
)

const pipelineYAML = `
otel: {metrics: true}
services:
  - name: coordinator
    type: agent
    runtime: go
    namespace: test
    entry: true
    calls: [source, producer, checker, finisher]
    agent_observability:
      name: planner
      sdk: sdk-go
      provider: openai
      models: [gpt-4.1-mini]
      corpus: regulated_document_authoring
      content_facets: [protocol]
      hooks: {enabled: true, phases: [preflight], timeout_ms: 100}
      tools:
        - {name: retrieve_source_note, kind: tool_result, facets: [redaction, source_note_injection]}
  - name: source
    type: retrieval
    runtime: python
    namespace: test
    agent_observability:
      name: retriever
      sdk: sdk-python
      corpus: regulated_document_authoring
      tools:
        - {name: retrieve_source_note, kind: tool_result, facets: [redaction, source_note_injection]}
  - name: producer
    type: agent
    runtime: python
    namespace: test
    agent_observability: &model
      name: writer
      sdk: sdk-python
      provider: openai
      models: [gpt-4.1-mini]
      corpus: regulated_document_authoring
      content_facets: [protocol]
  - name: checker
    type: agent
    runtime: python
    namespace: test
    agent_observability:
      <<: *model
      name: checker
  - name: finisher
    type: agent
    runtime: python
    namespace: test
    agent_observability:
      <<: *model
      name: finalizer
agent_pipeline:
  name: test-flow
  evaluation_mode: real
  request_kinds: [{name: long, weight: 1}, {name: short, weight: 1}]
  stages:
    - {name: plan, service: coordinator, operation: generation}
    - {name: fetch, service: source, operation: retrieval, caller_stage: plan}
    - {name: draft, service: producer, operation: generation}
    - {name: review, service: checker, operation: generation}
    - {name: finish, service: finisher, operation: generation, request_kinds: [long]}
  review: {stage: review, revise_stage: draft, max_revisions: 2}
  outcomes:
    - {name: ready, weight: 7, revisions: 0, review_passed: true}
    - {name: revised, weight: 2, revisions: 1, review_passed: true}
    - {name: manual, weight: 1, revisions: 2, review_passed: false}
  escalated_outcome: manual
  ratings: {sample_rate: 0.02}
`

type pipelineCapture struct {
	exports []sigil.Export
	ratings []sigil.ConversationRatingInput
}

func (c *pipelineCapture) Write(_ context.Context, e []sigil.Export) error {
	c.exports = append(c.exports, e...)
	return nil
}
func (c *pipelineCapture) WriteConversationRating(_ context.Context, _ string, r sigil.ConversationRatingInput) error {
	c.ratings = append(c.ratings, r)
	return nil
}

type pipelineStages struct {
	calls int
	after int
}

func (s *pipelineStages) BeforeToolResult(_ context.Context, _ sigil.GenerationStage, r sigil.ToolResult) (sigil.ToolResultDecision, error) {
	s.calls++
	r.Content = "[REDACTED]"
	r.ContentJSON = nil
	return sigil.ToolResultDecision{Allowed: true, Transformed: &r}, nil
}
func (s *pipelineStages) AfterGeneration(context.Context, sigil.Generation) error {
	s.after++
	return nil
}
func pipelineConfig(t *testing.T) *Config {
	t.Helper()
	c := NewConfig().(*Config)
	if err := yaml.Unmarshal([]byte(pipelineYAML), c); err != nil {
		t.Fatal(err)
	}
	return c
}
func pipelineBinding(c *Config, s sigil.GenerationStages, r sigil.ConversationRatingWriter) core.Binding {
	cl := &fixture.Cluster{Name: "test-cluster"}
	for _, n := range c.Services {
		cl.Workloads = append(cl.Workloads, fixture.Workload{Name: n.Name, Namespace: "test", PodNames: []string{n.Name + "-actual-hash"}, Controller: "deployment"})
	}
	return core.Binding{Name: "pipeline-test", Cluster: cl, GenerationStages: s, Ratings: r}
}
func pipelineRequest(i int) *ledger.Request {
	r := &ledger.Request{Correlation: ledger.NewCorrelationFromSeed(string(rune(i + 1))), Duration: time.Minute, Start: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	return r
}
func TestAgentPipelineMissingWire(t *testing.T) {
	c := pipelineConfig(t)
	sink := &pipelineCapture{}
	stages := &pipelineStages{}
	w, err := Registration().Build(c, pipelineBinding(c, stages, sink))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(w.Signals(), core.Sigil) {
		t.Fatal("missing native agent generation lane")
	}
	if err = w.ProjectBatch(context.Background(), time.Now(), &core.World{Sigil: sink, Traces: &coretest.TraceCapture{}}, []*ledger.Request{pipelineRequest(0)}); err != nil {
		t.Fatal(err)
	}
	if len(sink.exports) == 0 || stages.calls != 1 {
		t.Fatalf("missing incremental generation/tool wire: exports=%d hooks=%d", len(sink.exports), stages.calls)
	}
}
func TestAgentPipelineSelectorRed(t *testing.T) {
	c := pipelineConfig(t)
	raw, _ := yaml.Marshal(c)
	_ = raw
	// Unknown facet sets must reject, not silently select arbitrary records or fallback text.
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(pipelineYAML), &fields); err != nil {
		t.Fatal(err)
	}
	svcs := fields["services"].([]any)
	svcs[0].(map[string]any)["agent_observability"].(map[string]any)["content_facets"] = []string{"no-such-facet"}
	b, _ := yaml.Marshal(fields)
	if err := yaml.Unmarshal(b, c); err != nil {
		t.Fatal(err)
	}
	if _, err := Registration().Build(c, pipelineBinding(c, &pipelineStages{}, &pipelineCapture{})); err == nil {
		t.Fatal("missing fail-closed model content selector")
	}
}

func pipelineGenerations(c *pipelineCapture) []sigil.Generation {
	var out []sigil.Generation
	for _, e := range c.exports {
		out = append(out, e.Generations...)
		if len(e.Scores) > 0 {
			panic("backend scores forged")
		}
	}
	return out
}
func pipelineTerminal(c *pipelineCapture) string {
	for i := len(c.exports) - 1; i >= 0; i-- {
		for _, s := range c.exports[i].WorkflowSteps {
			if v, ok := s.OutputState["outcome"].(string); ok {
				return v
			}
		}
	}
	return ""
}
func runPipeline(t *testing.T, c *Config, st sigil.GenerationStages, sink *pipelineCapture, r *ledger.Request) (*Workload, *core.World, error) {
	t.Helper()
	raw, err := Registration().Build(c, pipelineBinding(c, st, sink))
	if err != nil {
		t.Fatal(err)
	}
	w := raw.(*Workload)
	world := &core.World{Sigil: sink, Traces: &coretest.TraceCapture{}, Shape: shape.New("", nil), Metrics: &coretest.MetricCapture{}, OTLPMetrics: &appNativeMetricCapture{}}
	err = w.ProjectBatch(context.Background(), r.Start, world, []*ledger.Request{r})
	return w, world, err
}

type decisionStages struct {
	before           func(context.Context, sigil.GenerationStage, sigil.ToolResult) (sigil.ToolResultDecision, error)
	after            func(context.Context, sigil.Generation) error
	calls, completed int
	retained         *sigil.ToolResult
}

func (s *decisionStages) BeforeToolResult(ctx context.Context, g sigil.GenerationStage, r sigil.ToolResult) (sigil.ToolResultDecision, error) {
	s.calls++
	return s.before(ctx, g, r)
}
func (s *decisionStages) AfterGeneration(ctx context.Context, g sigil.Generation) error {
	s.completed++
	if s.retained != nil {
		s.retained.Content = "MUTATED-ORIGINAL"
		if len(s.retained.ContentJSON) > 0 {
			s.retained.ContentJSON[0] = 'X'
		}
	}
	if s.after != nil {
		return s.after(ctx, g)
	}
	return nil
}
func TestAgentPipelineBarrier(t *testing.T) {
	for _, mode := range []string{"redact", "deny", "late-deny", "zero", "error-closed", "error-open", "timeout-closed", "timeout-open", "cancel", "bad-identity"} {
		t.Run(mode, func(t *testing.T) {
			c := pipelineConfig(t)
			c.Services[0].AgentObservability.Hooks.FailOpen = strings.HasSuffix(mode, "open") || mode == "late-deny"
			c.AgentPipeline.Ratings.SampleRate = 1
			capture := &pipelineCapture{}
			st := &decisionStages{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			st.before = func(hctx context.Context, g sigil.GenerationStage, r sigil.ToolResult) (sigil.ToolResultDecision, error) {
				if g.AgentName != "planner" || g.Provider != "openai" || g.Model != "gpt-4.1-mini" || g.GenerationID == "" || g.TraceID == "" || g.SpanID == "" {
					t.Fatalf("untruthful producing generation: %+v", g)
				}
				if !strings.Contains(r.Content, "UNTRUSTED") || r.Name != "retrieve_source_note" || r.ToolCallID == "" {
					t.Fatalf("hook did not observe actual private result: %+v", r)
				}
				switch mode {
				case "late-deny":
					<-hctx.Done()
					return sigil.ToolResultDecision{Allowed: false}, nil
				case "deny":
					return sigil.ToolResultDecision{Allowed: false}, nil
				case "zero":
					return sigil.ToolResultDecision{}, nil
				case "error-closed", "error-open":
					return sigil.ToolResultDecision{}, errors.New("local transport failure")
				case "timeout-closed", "timeout-open":
					<-hctx.Done()
					return sigil.ToolResultDecision{}, hctx.Err()
				case "cancel":
					cancel()
					return sigil.ToolResultDecision{Allowed: true}, nil
				case "bad-identity":
					r.Name = "different"
					return sigil.ToolResultDecision{Allowed: true, Transformed: &r}, nil
				}
				r.Content = "SAFE-REPLACEMENT"
				r.ContentJSON = []byte(`{"safe":true}`)
				st.retained = &r
				return sigil.ToolResultDecision{Allowed: true, Transformed: &r}, nil
			}
			raw, err := Registration().Build(c, pipelineBinding(c, st, capture))
			if err != nil {
				t.Fatal(err)
			}
			w := raw.(*Workload)
			world := &core.World{Sigil: capture, Traces: &coretest.TraceCapture{}}
			r := pipelineRequest(4)
			err = w.ProjectBatch(ctx, r.Start, world, []*ledger.Request{r})
			if st.calls != 1 {
				t.Fatalf("calls=%d", st.calls)
			}
			gens := pipelineGenerations(capture)
			continued := mode == "redact" || mode == "error-open" || mode == "timeout-open"
			if continued {
				if err != nil || len(gens) < 3 {
					t.Fatalf("continuation err=%v gens=%d", err, len(gens))
				}
			} else if len(gens) != 1 {
				t.Fatalf("dependent model called after stop: %d", len(gens))
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel lost: %v", err)
			}
			if mode == "redact" {
				b, _ := json.Marshal(struct {
					E any
					T any
					R any
				}{capture.exports, world.Traces.(*coretest.TraceCapture).Resources, capture.ratings})
				for _, secret := range []string{"UNTRUSTED", "SUBJ-SYN", "MUTATED-ORIGINAL"} {
					if strings.Contains(string(b), secret) {
						t.Fatalf("original/alias leaked downstream: %s", secret)
					}
				}
				if !strings.Contains(string(b), "SAFE-REPLACEMENT") {
					t.Fatal("replacement never reached downstream")
				}
			}
			if mode == "deny" || mode == "late-deny" || mode == "zero" {
				if err != nil || pipelineTerminal(capture) != "manual" {
					t.Fatalf("deny not terminal escalation: %v", err)
				}
			}
			if st.completed != len(gens) {
				t.Fatalf("after generation count %d != actual completed %d", st.completed, len(gens))
			}
		})
	}
}
func TestAgentPipelineJSONAlias(t *testing.T) {
	c := pipelineConfig(t)
	c.Services[0].AgentObservability.Tools = []PipelineTool{{Name: "search_products", Kind: "tool_result"}}
	c.Services[1].AgentObservability.Corpus = sigil.ArchetypeGeneralOrchestration
	c.Services[1].AgentObservability.Tools = []PipelineTool{{Name: "search_products", Kind: "tool_result"}}
	sink := &pipelineCapture{}
	st := &decisionStages{}
	st.before = func(_ context.Context, _ sigil.GenerationStage, r sigil.ToolResult) (sigil.ToolResultDecision, error) {
		if len(r.ContentJSON) == 0 || !json.Valid(r.ContentJSON) || r.Content == "" {
			t.Fatal("both original representations did not reach evaluator")
		}
		r.Content = "safe"
		r.ContentJSON = []byte(`{"safe":true}`)
		st.retained = &r
		return sigil.ToolResultDecision{Allowed: true, Transformed: &r}, nil
	}
	_, _, err := runPipeline(t, c, st, sink, pipelineRequest(15))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range pipelineGenerations(sink) {
		for _, m := range g.Input {
			for _, p := range m.Parts {
				if p.ToolResult != nil {
					found = true
					if p.ToolResult.Content != "safe" || string(p.ToolResult.ContentJSON) != `{"safe":true}` {
						t.Fatal("transformed JSON alias mutated downstream carry")
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("no JSON tool carry observed")
	}
}

func TestAgentPipelineUnconditionalStagesDependency(t *testing.T) {
	c := pipelineConfig(t)
	c.Services[0].AgentObservability.Hooks.Enabled = false
	c.AgentPipeline.Ratings.SampleRate = 0
	if _, err := Registration().AgentDeclarations(c, core.Binding{}); err != nil {
		t.Fatal("pure discovery must not require runtime collaborator", err)
	}
	if _, err := Registration().Build(c, pipelineBinding(c, nil, nil)); err == nil {
		t.Fatal("optional disabled hooks/zero ratings bypassed construction fence")
	}
	stage := &pipelineStages{}
	sink := &pipelineCapture{}
	raw, err := Registration().Build(c, pipelineBinding(c, stage, nil))
	if err != nil {
		t.Fatal(err)
	}
	r := pipelineRequest(25)
	if err := raw.ProjectBatch(context.Background(), r.Start, &core.World{Sigil: sink, Traces: &coretest.TraceCapture{}}, []*ledger.Request{r}); err != nil {
		t.Fatal(err)
	}
	if stage.calls != 0 || stage.after == 0 || stage.after != len(pipelineGenerations(sink)) {
		t.Fatalf("disabled hooks still require exactly-once completed-generation collaborator: before=%d after=%d", stage.calls, stage.after)
	}
	// The successful edge injection is a component construction dependency,
	// never a receipt or proof of current/historical root custody admission.
}

// Unsupported output declarations must reject at Build with actual stage edges
// supplied, not accidentally at the missing-collaborator construction fence.
func TestAgentPipelineSupportedOutputBoundary(t *testing.T) {
	var inline ServiceNode
	if err := yaml.Unmarshal([]byte("logs: [{source: app, body: {message: {const_str: fixture}}}]"), &inline); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"supported", func(*Config) {}, ""},
		{"unbound graph node", func(c *Config) {
			c.Services = append(c.Services, ServiceNode{Name: "store", Type: "db"})
			c.Services[0].Calls = append(c.Services[0].Calls, "store")
		}, "every service must be bound"},
		{"unused bound service", func(c *Config) {
			s := pipelineClone(c.Services[1])
			s.Name = "spare"
			s.AgentObservability.Name = "spare"
			c.Services = append(c.Services, s)
			c.Services[0].Calls = append(c.Services[0].Calls, "spare")
		}, "unused agent binding"},
		{"inline logs", func(c *Config) { c.Services[1].Logs = inline.Logs }, "log output unsupported"},
		{"profile logs", func(c *Config) { c.Services[1].Profiles = []string{"gateway_export_log"} }, "log output unsupported"},
		{"pages", func(c *Config) { c.Services[0].Pages = []PageDecl{{Path: "/"}} }, "RUM output unsupported"},
		{"rum profile", func(c *Config) {
			c.Services[0].Profiles = []string{"rum_faro"}
		}, "RUM output unsupported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := pipelineConfig(t)
			tc.mutate(c)
			edge, sink := &pipelineStages{}, &pipelineCapture{}
			w, err := Registration().Build(c, pipelineBinding(c, edge, sink))
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("unsupported output must fail at named admission: want %q, got %v", tc.want, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				r := pipelineRequest(26)
				if err := w.ProjectBatch(context.Background(), r.Start, &core.World{Sigil: sink, Traces: &coretest.TraceCapture{}}, []*ledger.Request{r}); err != nil {
					t.Fatal(err)
				}
				if edge.after == 0 || edge.after != len(pipelineGenerations(sink)) {
					t.Fatal("valid baseline did not execute through explicit stage edge")
				}
			}
			// The same ordinary app declarations retain their existing admission.
			c.AgentPipeline = nil
			for i := range c.Services {
				c.Services[i].AgentObservability = nil
			}
			if _, err := Registration().Build(c, pipelineBinding(c, nil, nil)); err != nil {
				t.Errorf("ordinary app behavior changed: %v", err)
			}
		})
	}
}

func TestAgentPipelineAfterFailureAndNilCollaborators(t *testing.T) {
	c := pipelineConfig(t)
	if _, err := Registration().Build(c, pipelineBinding(c, nil, &pipelineCapture{})); err == nil {
		t.Fatal("nil required stages admitted")
	}
	if _, err := Registration().Build(c, pipelineBinding(c, &pipelineStages{}, nil)); err == nil {
		t.Fatal("nil rating writer admitted")
	}
	sink := &pipelineCapture{}
	st := &decisionStages{before: func(context.Context, sigil.GenerationStage, sigil.ToolResult) (sigil.ToolResultDecision, error) {
		t.Fatal("tool reached after generation callback failed")
		return sigil.ToolResultDecision{}, nil
	}, after: func(context.Context, sigil.Generation) error { return errors.New("after failed") }}
	_, _, err := runPipeline(t, c, st, sink, pipelineRequest(8))
	if err == nil || len(pipelineGenerations(sink)) != 0 {
		t.Fatal("failed completed generation published")
	}
}
func TestAgentPipelineSelectorsPolicyAndBounds(t *testing.T) {
	cases := map[string]func(*Config){
		"empty facets":         func(c *Config) { c.Services[0].AgentObservability.ContentFacets = nil },
		"duplicate facets":     func(c *Config) { c.Services[0].AgentObservability.ContentFacets = []string{"protocol", "protocol"} },
		"blank facet":          func(c *Config) { c.Services[0].AgentObservability.ContentFacets = []string{""} },
		"subset cannot select": func(c *Config) { c.Services[0].AgentObservability.ContentFacets = []string{"protocol", "other"} },
		"source hooks":         func(c *Config) { c.Services[1].AgentObservability.Hooks = c.Services[0].AgentObservability.Hooks },
		"postflight disabled": func(c *Config) {
			c.Services[0].AgentObservability.Hooks = PipelineHooks{Phases: []string{"postflight"}}
		},
		"duplicate phase":     func(c *Config) { c.Services[0].AgentObservability.Hooks.Phases = []string{"preflight", "preflight"} },
		"no phase":            func(c *Config) { c.Services[0].AgentObservability.Hooks.Phases = nil },
		"bad timeout":         func(c *Config) { c.Services[0].AgentObservability.Hooks.TimeoutMS = math.MaxInt64 },
		"python context tags": func(c *Config) { c.Services[2].AgentObservability.ContextTags = map[string]string{"team": "one"} },
		"tag collision": func(c *Config) {
			c.Services[0].AgentObservability.ClientTags = map[string]string{"team.name": "one", "team_name": "two"}
		},
		"skip review":        func(c *Config) { c.AgentPipeline.Stages[3].RequestKinds = []string{"long"} },
		"excess revisions":   func(c *Config) { c.AgentPipeline.Review.MaxRevisions = 4 },
		"impossible failure": func(c *Config) { c.AgentPipeline.Outcomes[0].ReviewPassed = false },
		"excess stages": func(c *Config) {
			for len(c.AgentPipeline.Stages) < 17 {
				n := c.AgentPipeline.Stages[2]
				n.Name = fmt.Sprint(len(c.AgentPipeline.Stages))
				c.AgentPipeline.Stages = append(c.AgentPipeline.Stages, n)
			}
		},
		"excess expanded attempts": func(c *Config) {
			for len(c.AgentPipeline.Stages) < 16 {
				s := c.AgentPipeline.Stages[2]
				s.Name = fmt.Sprint(len(c.AgentPipeline.Stages))
				c.AgentPipeline.Stages = append(c.AgentPipeline.Stages, s)
			}
			c.AgentPipeline.Review.Stage = c.AgentPipeline.Stages[15].Name
		},
		"large modeled content": func(c *Config) {
			c.Services[0].AgentObservability.GenerationTags = map[string]string{"export_only": strings.Repeat("X", 300000)}
		},
		"large metadata": func(c *Config) {
			c.Services[0].AgentObservability.Metadata = map[string]any{"content": strings.Repeat("X", pipelineContentLimit)}
		},
		"external":              func(c *Config) { c.Services[1].External = true },
		"missing agent binding": func(c *Config) { c.Services[2].AgentObservability = nil },
		"duplicate metric":      func(c *Config) { c.Services[0].Profiles = []string{"gen_ai_client"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := pipelineConfig(t)
			mutate(c)
			if _, err := Registration().Build(c, pipelineBinding(c, &pipelineStages{}, &pipelineCapture{})); err == nil {
				t.Fatal("invalid pipeline admitted")
			}
		})
	}
	c := pipelineConfig(t)
	b := pipelineBinding(c, &pipelineStages{}, &pipelineCapture{})
	b.Cluster.Workloads = b.Cluster.Workloads[1:]
	if _, err := Registration().Build(c, b); err == nil {
		t.Fatal("guessed pod admitted")
	}
}
func TestAgentPipelineSecondToolAndPlacement(t *testing.T) {
	c := pipelineConfig(t)
	// The caller and source each have a real second tool, but only their
	// intersection is an admitted retrieval invocation. No backend tool filter.
	c.Services[0].AgentObservability.Tools = []PipelineTool{{Name: "search_products", Kind: "tool_result"}, {Name: "get_cart", Kind: "tool_result"}}
	c.Services[1].AgentObservability.Corpus = sigil.ArchetypeGeneralOrchestration
	c.Services[1].AgentObservability.Tools = []PipelineTool{{Name: "search_products", Kind: "tool_result"}, {Name: "add_to_cart", Kind: "tool_result"}}
	actual := pipelineClone(*c)
	decls, err := Registration().AgentDeclarations(c, pipelineBinding(c, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(decls) != 5 || decls[1].Models != nil {
		t.Fatal("tools-only claim lost")
	}
	// Discovery must not load this invalid corpus; Build must.
	c.Services[0].AgentObservability.Corpus = "missing-corpus"
	if _, err := Registration().AgentDeclarations(c, core.Binding{}); err != nil {
		t.Fatal("structural callback sampled corpus")
	}
	c = pipelineConfig(t)
	b := pipelineBinding(c, &pipelineStages{}, &pipelineCapture{})
	b.Cluster.Workloads = append([]fixture.Workload{{Name: "producer", Namespace: "other", PodNames: []string{"WRONG"}}}, b.Cluster.Workloads...)
	raw, err := Registration().Build(c, b)
	if err != nil {
		t.Fatal(err)
	}
	w := raw.(*Workload)
	if got := w.identity(w.graph.byName["producer"]).podName; got != "producer-actual-hash" {
		t.Fatal(got)
	}
	c = &actual
	st := &pipelineStages{}
	sink := &pipelineCapture{}
	// Add another invocation but disable its *stage* using
	// a distinct request kind. Hook selection must be per admitted invocation.
	extra := c.AgentPipeline.Stages[1]
	extra.Name = "not-selected"
	extra.RequestKinds = []string{"short"}
	c.AgentPipeline.Stages = append(c.AgentPipeline.Stages, extra)
	c.AgentPipeline.RequestKinds = []PipelineKind{{Name: "long", Weight: 1}, {Name: "short", Weight: 0}}
	_, _, err = runPipeline(t, c, st, sink, pipelineRequest(9))
	if err != nil {
		t.Fatal(err)
	}
	if st.calls != 1 {
		t.Fatalf("nonselected source/caller second tool or invocation evaluated: %d", st.calls)
	}
	for _, g := range pipelineGenerations(sink) {
		for _, m := range g.Output {
			for _, part := range m.Parts {
				if part.ToolCall != nil && part.ToolCall.Name != "search_products" {
					t.Fatal("nonselected tool produced a call")
				}
			}
		}
	}
}
func TestAgentPipelineSeededOutcomesAndNative(t *testing.T) {
	c := pipelineConfig(t)
	st := &pipelineStages{}
	sink := &pipelineCapture{}
	raw, err := Registration().Build(c, pipelineBinding(c, st, sink))
	if err != nil {
		t.Fatal(err)
	}
	w := raw.(*Workload)
	counts := map[string]int{}
	modelCounts := map[string]uint64{}
	ratings := 0
	native := &appNativeMetricCapture{}
	world := &core.World{Sigil: sink, Traces: &coretest.TraceCapture{}, Shape: shape.New("", nil), OTLPMetrics: native}
	for i := range 10000 {
		r := pipelineRequest(i)
		sink.exports = nil
		sink.ratings = nil
		world.Traces = &coretest.TraceCapture{}
		if err := w.ProjectBatch(context.Background(), r.Start, world, []*ledger.Request{r}); err != nil {
			t.Fatal(err)
		}
		counts[pipelineTerminal(sink)]++
		ratings += len(sink.ratings)
		gs := pipelineGenerations(sink)
		for _, g := range gs {
			modelCounts[g.AgentName]++
			if g.TraceID != r.TraceID || g.AgentName == "retriever" {
				t.Fatal("wrong trace or fake source generation")
			}
			if strings.Contains(string(mustJSON(g.Input)), "UNTRUSTED") {
				t.Fatal("baseline model or safe carry sampled attack")
			}
		}
		if len(gs) > 8 {
			t.Fatal("unbounded review")
		}
	}
	for name, p := range map[string]float64{"ready": .7, "revised": .2, "manual": .1} {
		if math.Abs(float64(counts[name])/10000-p) > 5*math.Sqrt(p*(1-p)/10000) {
			t.Fatalf("weighted outcomes %+v", counts)
		}
	}
	if ratings < 130 || ratings > 270 {
		t.Fatalf("low-rate ratings %d", ratings)
	}
	if err := w.Tick(context.Background(), time.Now(), world); err != nil {
		t.Fatal(err)
	}
	if len(native.resources) != 4 {
		t.Fatalf("native resources %d, want executed model services only", len(native.resources))
	}
	for _, r := range native.resources {
		for _, m := range r.Metrics {
			if m.Name == pipelineDurationMetric {
				for _, point := range m.Histograms {
					agent := point.Attrs["gen_ai.agent.name"].(string)
					if point.Count != modelCounts[agent] {
						t.Fatalf("native count %d != actual model executions %d for %s", point.Count, modelCounts[agent], agent)
					}
				}
			}
		}
	}
	old := native.resources
	native.resources = nil
	if err := w.Tick(context.Background(), time.Now().Add(time.Minute), world); err != nil {
		t.Fatal(err)
	}
	for i, r := range native.resources {
		for j, m := range r.Metrics {
			for k, point := range m.Histograms {
				if point.Count != old[i].Metrics[j].Histograms[k].Count {
					t.Fatal("metric Tick invented execution")
				}
			}
		}
	}
	t.Logf("seeded outcomes=%v ratings=%d (10000 requests)", counts, ratings)
}
func TestAgentPipelineReplayConditionalAndRevision(t *testing.T) {
	for _, kind := range []string{"long", "short"} {
		t.Run(kind, func(t *testing.T) {
			c := pipelineConfig(t)
			for i := range c.AgentPipeline.RequestKinds {
				c.AgentPipeline.RequestKinds[i].Weight = 0
				if c.AgentPipeline.RequestKinds[i].Name == kind {
					c.AgentPipeline.RequestKinds[i].Weight = 1
				}
			}
			c.AgentPipeline.Outcomes = []PipelineOutcome{{Name: "ready", Weight: 1, Revisions: 2, ReviewPassed: true}, {Name: "manual", Revisions: 2}}
			r := pipelineRequest(20)
			a, b := &pipelineCapture{}, &pipelineCapture{}
			_, wa, err := runPipeline(t, c, &pipelineStages{}, a, r)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = runPipeline(t, c, &pipelineStages{}, b, r)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(a.exports, b.exports) || !reflect.DeepEqual(a.ratings, b.ratings) {
				t.Fatal("fixed request replay changed")
			}
			gens := pipelineGenerations(a)
			reviews := 0
			finished := false
			for _, g := range gens {
				if g.AgentName == "checker" {
					reviews++
				}
				finished = finished || g.AgentName == "finalizer"
			}
			if reviews != 3 || finished != (kind == "long") {
				t.Fatalf("reviews=%d finished=%v kind=%s", reviews, finished, kind)
			}
			ids := map[string]bool{}
			for _, res := range wa.Traces.(*coretest.TraceCapture).Resources {
				for _, span := range res.Spans {
					if ids[span.SpanID] {
						t.Fatal("revision reused span ID")
					}
					ids[span.SpanID] = true
				}
			}
		})
	}
}

func TestAgentPipelineSpanMetricsAndTags(t *testing.T) {
	c := pipelineConfig(t)
	a := c.Services[0].AgentObservability
	a.ClientTags = map[string]string{"team": "client", "client": "visible"}
	a.ContextTags = map[string]string{"team": "context"}
	a.GenerationTags = map[string]string{"team": "export", "generation": "export-only"}
	a.Metadata = map[string]any{"note": "export-metadata"}
	sink := &pipelineCapture{}
	w, world, err := runPipeline(t, c, &pipelineStages{}, sink, pipelineRequest(3))
	if err != nil {
		t.Fatal(err)
	}
	g := pipelineGenerations(sink)[0]
	if g.Tags["team"] != "export" {
		t.Fatal(g.Tags)
	}
	traces := world.Traces.(*coretest.TraceCapture).Resources
	found := false
	for _, r := range traces {
		for _, s := range r.Spans {
			if s.Attrs[sigil.AttrGenerationID] == g.ID {
				found = true
				if s.Attrs[sigil.TagPrefix+"team"] != "context" || s.Attrs[sigil.TagPrefix+"generation"] != nil {
					t.Fatal("tag channels conflated")
				}
			}
		}
	}
	if !found {
		t.Fatal("missing generation span")
	}
	if err = w.Tick(context.Background(), time.Now(), world); err != nil {
		t.Fatal(err)
	}
	for _, r := range world.OTLPMetrics.(*appNativeMetricCapture).resources {
		for _, m := range r.Metrics {
			for _, p := range m.Histograms {
				if p.Attrs[sigil.TagPrefix+"generation"] != nil || p.Attrs["conversation_id"] != nil {
					t.Fatal("export-only metric tag")
				}
			}
		}
	}
	before := len(sink.exports)
	world.EmitSpanMetrics = true
	if err = w.ProjectBatch(context.Background(), time.Now(), world, []*ledger.Request{pipelineRequest(4)}); err == nil {
		t.Fatal("local DFS spanmetrics admitted")
	}
	if len(sink.exports) != before {
		t.Fatal("exported before refusal")
	}
	if err = w.Tick(context.Background(), time.Now(), world); err == nil {
		t.Fatal("old DFS metric Tick ran")
	}
	// Canonical declaration slices must be fresh and structural, not mutable state.
	d1, err := Registration().AgentDeclarations(c, core.Binding{})
	if err != nil {
		t.Fatal(err)
	}
	d1[0].HookPolicy.Phases[0] = "changed"
	d2, _ := Registration().AgentDeclarations(c, core.Binding{})
	if reflect.DeepEqual(d1, d2) {
		t.Fatal("declaration output aliases configuration")
	}
}
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
