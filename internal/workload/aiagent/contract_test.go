// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/state"
)

// Pinned SDK generationSpanAttributes is the proprietary-export span contract,
// not the experimental OTel-generation-export contract. Expected wire keys are
// independent literals so a stale vocabulary constant cannot bless a rename.
func TestCurrentGenerationSpanWireArrays(t *testing.T) {
	agent := codingAgent()
	gens, _, resources, _ := buildConversation(ResourceID{}, agent, fixedReq("array-wire", time.Minute))
	if len(gens) == 0 {
		t.Fatal("no generations")
	}
	found := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		defer zr.Close()
		raw, err := io.ReadAll(zr)
		if err != nil {
			t.Error(err)
			return
		}
		for len(raw) > 0 {
			num, typ, n := protowire.ConsumeTag(raw)
			if n < 0 || num != 1 || typ != protowire.BytesType {
				t.Error("invalid OTLP envelope")
				return
			}
			raw = raw[n:]
			data, n := protowire.ConsumeBytes(raw)
			if n < 0 {
				t.Error("invalid resource bytes")
				return
			}
			raw = raw[n:]
			var resource tracepb.ResourceSpans
			if err := proto.Unmarshal(data, &resource); err != nil {
				t.Error(err)
				return
			}
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					for _, attr := range span.Attributes {
						if attr.Key == "gen_ai.response.finish_reasons" {
							found = true
							values := attr.Value.GetArrayValue().GetValues()
							if len(values) != 1 || values[0].GetStringValue() == "" {
								t.Errorf("finish_reasons must be OTLP string array, got %v", attr.Value)
							}
						}
					}
				}
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := otlp.New(srv.URL, "tenant", "test-token", false).Write(context.Background(), resources); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("no finish reasons on exported generation spans")
	}
}

func TestCurrentGenerationSpanGolden(t *testing.T) {
	agent := codingAgent()
	gen := sigil.Generation{ID: "g", ConversationID: "c", OperationName: "generateText", Provider: "anthropic", Model: "requested", ResponseModel: "returned", ResponseID: "response", AgentName: agent.Name, AgentVersion: agent.Version, StopReason: "end_turn", Usage: sigil.Usage{Input: 20, Output: 8, CacheRead: 10, CacheWrite: 2, Reasoning: 3}}
	attrs := rootSpanAttrs(agent, fixedReq("c", time.Second), gen)
	// User and declared tags are checked separately: these are content/context
	// fields rather than the SDK's generation identity golden.
	delete(attrs, "user.id")
	for k := range agent.Tags {
		delete(attrs, "agento11y.tag."+k)
	}
	got, _ := json.Marshal(attrs)
	want := `{"agento11y.generation.id":"g","agento11y.sdk.name":"sdk-go","gen_ai.agent.name":"claude-code","gen_ai.agent.version":"2.1.0","gen_ai.conversation.id":"c","gen_ai.operation.name":"generateText","gen_ai.provider.name":"anthropic","gen_ai.request.model":"requested","gen_ai.response.finish_reasons":["end_turn"],"gen_ai.response.id":"response","gen_ai.response.model":"returned","gen_ai.usage.cache_read_input_tokens":10,"gen_ai.usage.cache_write_input_tokens":2,"gen_ai.usage.input_tokens":20,"gen_ai.usage.output_tokens":8,"gen_ai.usage.reasoning_tokens":3}`
	if string(got) != want {
		t.Errorf("Lane B golden\ngot  %s\nwant %s", got, want)
	}
}

func TestCurrentConversationContract(t *testing.T) {
	agent := codingAgent()
	gens, _, resources, obs := buildConversation(ResourceID{}, agent, fixedReq("contract-inclusive", time.Minute))
	if resources[0].Scope.Name != "agento11y.claude-code" || resources[0].Attrs["service.name"] != "agento11y" {
		t.Errorf("plugin identity: %v / %v", resources[0].Scope, resources[0].Attrs)
	}
	st := state.NewState()
	for i, g := range gens {
		if g.Usage.Input < g.Usage.CacheRead+g.Usage.CacheWrite || g.Usage.Output < g.Usage.Reasoning || g.Usage.Total != g.Usage.Input+g.Usage.Output {
			t.Errorf("generation %d non-inclusive usage: %+v", i, g.Usage)
		}
		accumulate(st, obs[i])
	}
	found := false
	for _, s := range st.Collect(time.Unix(0, 0)) {
		if s.Name == metricTokenUsage+"_count" {
			found = true
			if s.Labels["gen_ai_agent_name"] != "claude-code" || s.Labels["gen_ai_agent_version"] != "2.1.0" || s.Labels["gen_ai_token_semantics"] != "inclusive" {
				t.Errorf("client token labels: %v", s.Labels)
			}
		}
	}
	if !found {
		t.Fatal("no client token observations")
	}
	codex := agent
	codex.Name = "codex"
	codex.Archetype = sigil.ArchetypeCodingCodex
	codexGens, _, codexSpans, _ := buildConversation(ResourceID{}, codex, fixedReq("contract-codex", time.Minute))
	if codexSpans[0].Scope.Name != "agento11y.codex" {
		t.Errorf("Codex plugin scope=%s", codexSpans[0].Scope.Name)
	}
	for _, g := range codexGens {
		if g.Usage.InputSemantics != "" || g.Usage.CacheWrite != 0 || g.ThinkingEnabled != nil {
			t.Errorf("Codex mapper fields fabricated: %+v / %v", g.Usage, g.ThinkingEnabled)
		}
	}
}

func TestCurrentEvalRoleAndTagContract(t *testing.T) {
	rule := RuleDecl{Name: "rule", SampleRate: 1, ExecutionMode: "sequential", Evaluators: []string{"gate", "verdict"}, FilterableTagKeys: []string{"agento11y.tag.team.name"}}
	evals := []EvalDecl{{Name: "gate", Kind: "heuristic", ScoreKey: "safe", ValueType: "number", Threshold: 0}, {Name: "verdict", Kind: "heuristic", ScoreKey: "quality", ValueType: "number", Threshold: 0}}
	e := newEvalEngine(evals, []RuleDecl{rule})
	st := state.NewState()
	scores := e.scoreConversation(AgentDecl{Name: "agent"}, []sigil.Generation{{ID: "g", AgentName: "agent", Tags: map[string]string{"team.name": "writers", "ignored": "never-a-label"}}}, st, 0)
	if len(scores) != 2 {
		t.Fatalf("scores=%d, want gate and verdict", len(scores))
	}
	found := map[string]string{}
	for _, s := range st.Collect(time.Unix(0, 0)) {
		if s.Name == "agento11y_eval_scores_total" {
			found[s.Labels["evaluator"]] = s.Labels["evaluator_role"]
			if s.Labels["agento11y_eval_tag_team_name"] != "writers" {
				t.Errorf("filterable tag absent: %v", s.Labels)
			}
			if _, ok := s.Labels["agento11y_eval_tag_ignored"]; ok {
				t.Errorf("unconfigured tag leaked")
			}
		}
	}
	if !reflect.DeepEqual(found, map[string]string{"gate": "gate", "verdict": "outcome"}) {
		t.Errorf("positional roles=%v", found)
	}
	// Sequential sampling chooses the generation once, not each evaluator:
	// an outcome must never execute after an unsampled gate.
	rule.SampleRate = 0.5
	generations := make([]sigil.Generation, 64)
	for i := range generations {
		generations[i] = sigil.Generation{ID: fmt.Sprintf("sequential-%d", i)}
	}
	byGeneration := map[string]int{}
	for _, score := range newEvalEngine(evals, []RuleDecl{rule}).scoreConversation(AgentDecl{Name: "agent"}, generations, state.NewState(), 0) {
		byGeneration[score.GenerationID]++
	}
	for id, n := range byGeneration {
		if n != 2 {
			t.Errorf("generation %s has %d scores; sequential sample must include both passing stages", id, n)
		}
	}
	rule.SampleRate = 1
	// A deterministic failing gate must prevent the terminal evaluation.
	evals[0].Threshold = 100
	e = newEvalEngine(evals, []RuleDecl{rule})
	if got := len(e.scoreConversation(AgentDecl{Name: "agent"}, []sigil.Generation{{ID: "g"}}, state.NewState(), 0)); got != 1 {
		t.Errorf("failing gate produced %d scores", got)
	}
	for _, keys := range [][]string{{"team-name", "team_name"}, {""}} {
		_, err := build(&Config{Rules: []RuleDecl{{Name: "invalid", FilterableTagKeys: keys}}}, core.Binding{})
		if err == nil {
			t.Errorf("invalid filterable tag keys admitted: %v", keys)
		}
	}
}

// Backend bc963fb09 rejects nonterminal gates without a pass condition and
// advances only on a persisted, explicitly present true verdict.
func TestSequentialGateAdmissionAndPersistedVerdict(t *testing.T) {
	verdict := EvalDecl{Name: "verdict", Kind: "heuristic", ScoreKey: "quality", ValueType: "number", Threshold: 0}
	for _, tc := range []struct {
		name string
		gate *EvalDecl
	}{
		{"missing", nil},
		{"passless-string", &EvalDecl{Name: "gate", Kind: "heuristic", ScoreKey: "info", ValueType: "string"}},
		{"unsupported-pass-condition", &EvalDecl{Name: "gate", Kind: "heuristic", ScoreKey: "info", ValueType: "unknown"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evals := []EvalDecl{verdict}
			if tc.gate != nil {
				evals = append(evals, *tc.gate)
			}
			rule := RuleDecl{Name: "sequential", ExecutionMode: "sequential", SampleRate: 1, Evaluators: []string{"gate", "verdict"}}
			if _, err := Registration().Build(&Config{Evaluators: evals, Rules: []RuleDecl{rule}}, core.Binding{}); err == nil {
				t.Error("invalid sequential gate admitted")
			}
		})
	}
	for _, kind := range []string{"number", "bool"} {
		gate := EvalDecl{Name: "gate", Kind: "heuristic", ScoreKey: "safe", ValueType: kind, Threshold: 4}
		rule := RuleDecl{Name: "valid", ExecutionMode: "sequential", SampleRate: 1, Evaluators: []string{"gate", "verdict"}}
		if _, err := Registration().Build(&Config{Evaluators: []EvalDecl{gate, verdict}, Rules: []RuleDecl{rule}}, core.Binding{}); err != nil {
			t.Fatalf("supported %s gate rejected: %v", kind, err)
		}
	}
	// Defensive execution must not skip a missing gate or turn an informational
	// string score's private random draw into an explicit persisted verdict.
	for _, missing := range []bool{false, true} {
		evals := []EvalDecl{verdict}
		if !missing {
			evals = append(evals, EvalDecl{Name: "gate", Kind: "heuristic", ScoreKey: "info", ValueType: "string"})
		}
		rule := RuleDecl{Name: "runtime", ExecutionMode: "sequential", SampleRate: 1, Evaluators: []string{"gate", "verdict"}}
		gens := make([]sigil.Generation, 64)
		for i := range gens {
			gens[i] = sigil.Generation{ID: fmt.Sprintf("passless-%d", i)}
		}
		scores := newEvalEngine(evals, []RuleDecl{rule}).scoreConversation(AgentDecl{Name: "agent"}, gens, state.NewState(), 0)
		want := len(gens)
		if missing {
			want = 0
		}
		if len(scores) != want {
			t.Errorf("missing=%v: emitted %d scores, want %d", missing, len(scores), want)
		}
		for _, score := range scores {
			if score.EvaluatorID != "gate" || score.HasPassed {
				t.Errorf("advanced past absent pass verdict: %+v", score)
				break
			}
		}
	}
}

// SDK 75a1c548 trims tag keys before emitting dotted attributes; Prometheus
// translation is lossy. Reject collisions before either cumulative state or
// native-key restoration can discard/swap one of the configured values.
func TestClientTagTranslatedCollisionsRejected(t *testing.T) {
	for _, tags := range []map[string]string{
		{"team.name": "alpha", "team_name": "beta"},
		{" team ": "alpha", "team": "beta"},
		{"team/name": "alpha", "team.name": "beta"},
	} {
		agent := codingAgent()
		agent.Tags = tags
		if _, err := Registration().Build(&Config{Agents: []AgentDecl{agent}}, core.Binding{}); err == nil {
			t.Errorf("colliding client keys admitted: %v", tags)
		}
	}
}

func TestCurrentEvalProducerContract(t *testing.T) {
	st := state.NewState()
	gen := sigil.Generation{ID: "g", AgentName: "agent", Model: "generation-model", Provider: "generation-provider", Usage: sigil.Usage{Input: 100, Output: 20}}
	accumulateEval(st, gen, EvalDecl{Name: "judge", Kind: "llm_judge", ScoreKey: "result", ValueType: "bool", JudgeModel: "judge-model", JudgeProvider: "judge-provider"}, RuleDecl{Name: "rule"}, true)
	accumulateEval(st, gen, EvalDecl{Name: "heuristic", Kind: "heuristic", ScoreKey: "safe", ValueType: "bool"}, RuleDecl{Name: "rule"}, true)
	for _, s := range st.Collect(time.Unix(0, 0)) {
		if s.Name == "agento11y_eval_score_values_total" || s.Name == "agento11y_eval_judge_tokens_total" {
			t.Errorf("backend-owned family emitted: %s", s.Name)
		}
		if s.Name == "agento11y_eval_scores_total" {
			if s.Labels["evaluator"] == "judge" && (s.Labels["eval_ai_request_model"] != "judge-model" || s.Labels["eval_ai_request_provider"] != "judge-provider") {
				t.Errorf("judge target: %v", s.Labels)
			}
			if s.Labels["evaluator"] == "heuristic" {
				if _, ok := s.Labels["eval_ai_request_model"]; ok {
					t.Errorf("heuristic has judge identity: %v", s.Labels)
				}
			}
		}
	}
	points := st.CollectHistos()
	for _, p := range points {
		if p.Name == "agento11y_eval_duration_seconds" && !reflect.DeepEqual(p.Bounds, []float64{.01, .02, .04, .08, .16, .32, .64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92, 163.84, 327.68}) {
			t.Errorf("eval duration bounds: %v", p.Bounds)
		}
	}
}
