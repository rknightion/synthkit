// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// TestNumberScoreUsesRubricScale locks the M1 fix: number evaluators must score on the rubric scale
// (1..5 for threshold≤5, 1..10 otherwise) — NOT [0,1) — so `passed` is not trivially always-false
// against a threshold like 4 or 7, and the distribution spans the threshold.
func TestNumberScoreUsesRubricScale(t *testing.T) {
	for _, tc := range []struct{ thr, max float64 }{{4, 5}, {7, 10}} {
		ev := EvalDecl{Name: fmt.Sprintf("judge-%v", tc.thr), ValueType: "number", Threshold: tc.thr}
		pass, total := 0, 300
		for i := 0; i < total; i++ {
			v, passed := scoreValue(fmt.Sprintf("gen-%d", i), ev, 0)
			if v < 1 || v > tc.max {
				t.Fatalf("thr=%v value %v outside rubric [1,%v]", tc.thr, v, tc.max)
			}
			if v != math.Trunc(v) {
				t.Fatalf("thr=%v value %v not an integer rubric point", tc.thr, v)
			}
			if passed != (v >= tc.thr) {
				t.Fatalf("thr=%v passed=%v but value=%v", tc.thr, passed, v)
			}
			if passed {
				pass++
			}
		}
		if pass == 0 || pass == total {
			t.Fatalf("thr=%v degenerate pass rate %d/%d — number scores must span the threshold", tc.thr, pass, total)
		}
	}
}

// TestEvalRuleSampleRateOne: a rule matching an agent at sample_rate=1 produces one Score per
// matched generation, with a deterministic value.
func TestEvalRuleSampleRateOne(t *testing.T) {
	evals := []EvalDecl{
		{Name: "helpfulness", Kind: "llm_judge", ScoreKey: "helpfulness", ValueType: "number", Threshold: 0.6, JudgeModel: "us.amazon.nova-pro-v1:0"},
	}
	rules := []RuleDecl{
		{Name: "score-coding", SampleRate: 1, MatchAgent: []string{"claude-*"}, Evaluators: []string{"helpfulness"}},
	}
	e := newEvalEngine(evals, rules)

	gens := []sigil.Generation{
		{ID: "gen-a", ConversationID: "conv-1", TraceID: "t1", SpanID: "s1"},
		{ID: "gen-b", ConversationID: "conv-1", TraceID: "t2", SpanID: "s2"},
	}
	agent := codingAgent() // name claude-code, matches claude-*

	st := state.NewState()
	scores := e.scoreConversation(agent, gens, st, 0)
	if len(scores) != len(gens) {
		t.Fatalf("got %d scores, want %d (one per matched generation)", len(scores), len(gens))
	}
	for i, s := range scores {
		if s.GenerationID != gens[i].ID {
			t.Fatalf("score %d GenerationID=%q, want %q", i, s.GenerationID, gens[i].ID)
		}
		if s.EvaluatorID != "helpfulness" {
			t.Fatalf("score %d EvaluatorID=%q", i, s.EvaluatorID)
		}
		if s.Number == nil {
			t.Fatalf("score %d: number value not set for number evaluator", i)
		}
	}

	// Deterministic value: re-score yields identical numbers.
	scores2 := e.scoreConversation(agent, gens, state.NewState(), 0)
	for i := range scores {
		if *scores[i].Number != *scores2[i].Number {
			t.Fatalf("score %d non-deterministic: %v vs %v", i, *scores[i].Number, *scores2[i].Number)
		}
		if scores[i].Passed != scores2[i].Passed {
			t.Fatalf("score %d passed non-deterministic", i)
		}
	}

	// sigil_eval_* metric observations landed.
	series := st.Collect(time.Now())
	if !hasSeries(series, sigil.MetricEvalScoresTotal) {
		t.Fatalf("expected %s series", sigil.MetricEvalScoresTotal)
	}
	if !hasSeries(series, sigil.MetricEvalJudgeRequestsTotal) {
		t.Fatalf("expected %s series (llm_judge)", sigil.MetricEvalJudgeRequestsTotal)
	}
}

// TestEvalRuleNonMatchingAgent: a rule whose match_agent does not match emits nothing.
func TestEvalRuleNonMatchingAgent(t *testing.T) {
	e := newEvalEngine(
		[]EvalDecl{{Name: "acc", Kind: "heuristic", ScoreKey: "accuracy", ValueType: "bool"}},
		[]RuleDecl{{Name: "r1", SampleRate: 1, MatchAgent: []string{"soc-*"}, Evaluators: []string{"acc"}}},
	)
	gens := []sigil.Generation{{ID: "g1", ConversationID: "c1"}}
	scores := e.scoreConversation(codingAgent(), gens, state.NewState(), 0)
	if len(scores) != 0 {
		t.Fatalf("non-matching agent: got %d scores, want 0", len(scores))
	}
}

func hasSeries(series []promrw.Series, name string) bool {
	for _, s := range series {
		if s.Name == name {
			return true
		}
	}
	return false
}

// TestEvalLabelShapeMatchesLiveCapture locks the sigil_eval_* label shapes to the emea-cloud-demokit
// live capture (2026-06-30): the OTLP→Prom convention (evaluator/rule/gen_ai_agent_name/…), NOT the
// backend short names. Guards against regressing to evaluator_name/agent_name/rule_name/judge_model.
func TestEvalLabelShapeMatchesLiveCapture(t *testing.T) {
	evals := []EvalDecl{
		{Name: "helpfulness", Kind: "llm_judge", ScoreKey: "helpfulness_score", ValueType: "number", Threshold: 4, JudgeModel: "claude-haiku-4-5", JudgeProvider: "bedrock"},
		{Name: "toxicity", Kind: "heuristic", ScoreKey: "toxicity", ValueType: "bool"},
	}
	rules := []RuleDecl{{Name: "chatservice.sampling", SampleRate: 1, MatchAgent: []string{"general_agent"}, Evaluators: []string{"helpfulness", "toxicity"}}}
	e := newEvalEngine(evals, rules)

	agent := AgentDecl{Name: "general_agent", Provider: "openai"}
	gens := []sigil.Generation{{ID: "gen-1", ConversationID: "c1", AgentName: "general_agent", Model: "gpt-4.1-nano", Provider: "openai"}}

	st := state.NewState()
	e.scoreConversation(agent, gens, st, 0)
	series := st.Collect(time.Now())

	labelsOf := func(name string) map[string]string {
		for _, s := range series {
			if s.Name == name {
				return s.Labels
			}
		}
		return nil
	}
	has := func(m map[string]string, k string) bool { _, ok := m[k]; return ok }

	// scores_total: live label convention, and NONE of the old backend short names.
	sc := labelsOf(sigil.MetricEvalScoresTotal)
	if sc == nil {
		t.Fatal("no sigil_eval_scores_total series")
	}
	for _, want := range []string{"evaluator", "evaluator_kind", "score_key", "rule", "gen_ai_agent_name", "gen_ai_request_model", "gen_ai_request_provider", "passed"} {
		if !has(sc, want) {
			t.Errorf("scores_total missing live label %q (have %v)", want, sc)
		}
	}
	for _, banned := range []string{"evaluator_name", "agent_name", "agent_version", "rule_name", "judge_model"} {
		if has(sc, banned) {
			t.Errorf("scores_total carries stale backend label %q — must use the live OTLP→Prom name", banned)
		}
	}
	if sc["gen_ai_agent_name"] != "general_agent" || sc["gen_ai_request_provider"] != "openai" {
		t.Errorf("scores_total identity wrong: %v", sc)
	}

	// executions_total: status, NOT passed.
	ex := labelsOf(sigil.MetricEvalExecutionsTotal)
	if !has(ex, "status") || has(ex, "passed") {
		t.Errorf("executions_total labels wrong (want status, not passed): %v", ex)
	}

	// rule_action_fires_total: {rule, result}.
	rf := labelsOf(sigil.MetricEvalRuleActionFiresTotal)
	if !has(rf, "rule") || !has(rf, "result") || has(rf, "evaluator_name") {
		t.Errorf("rule_action_fires_total labels wrong: %v", rf)
	}

	// judge_requests_total (llm_judge): {model, provider, status}.
	jr := labelsOf(sigil.MetricEvalJudgeRequestsTotal)
	if jr["model"] != "claude-haiku-4-5" || jr["provider"] != "bedrock" || !has(jr, "status") {
		t.Errorf("judge_requests_total labels wrong: %v", jr)
	}

	// queue_depth is a backend-global gauge ({status} only) that synthkit deliberately does NOT emit
	// (it would collide across fleets in one push); assert it is absent so we don't regress that.
	if qd := labelsOf(sigil.MetricEvalQueueDepth); qd != nil {
		t.Errorf("sigil_eval_queue_depth should NOT be emitted (backend-global, would collide), got %v", qd)
	}

	// score_values_total exists (the bool evaluator) and carries score_value.
	if sv := labelsOf(sigil.MetricEvalScoreValuesTotal); sv == nil || !has(sv, "score_value") {
		t.Errorf("score_values_total missing or lacks score_value: %v", sv)
	}
}
