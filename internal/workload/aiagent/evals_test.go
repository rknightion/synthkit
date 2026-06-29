// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

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
	scores := e.scoreConversation(agent, gens, st)
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
	scores2 := e.scoreConversation(agent, gens, state.NewState())
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
	scores := e.scoreConversation(codingAgent(), gens, state.NewState())
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
