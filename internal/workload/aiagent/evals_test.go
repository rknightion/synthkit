// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"fmt"
	"math"
	"sort"
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

	// agento11y_eval_* metric observations landed.
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

// TestEvalLabelShapeMatchesLiveCapture locks the agento11y_eval_* label shapes to the emea-cloud-demokit
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
		t.Fatal("no agento11y_eval_scores_total series")
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
		t.Errorf("agento11y_eval_queue_depth should NOT be emitted (backend-global, would collide), got %v", qd)
	}

	// score_values_total exists (the bool evaluator) and carries score_value.
	if sv := labelsOf(sigil.MetricEvalScoreValuesTotal); sv == nil || !has(sv, "score_value") {
		t.Errorf("score_values_total missing or lacks score_value: %v", sv)
	}
}

// TestEvalMetricsMatchReferenceCapture pins the 2026-09-06 tenant read-back.
// It exercises the emitter directly, without loading an agent blueprint or sending traffic.
func TestEvalMetricsMatchReferenceCapture(t *testing.T) {
	for _, kind := range []string{"heuristic", "llm_judge"} {
		t.Run(kind, func(t *testing.T) {
			st := state.NewState()
			gen := sigil.Generation{ID: "capture-contract", AgentName: "reference", Model: "scored-model", Provider: "model-provider"}
			ev := EvalDecl{Name: "check", Kind: kind, ScoreKey: "result", ValueType: "bool"}
			if kind == "llm_judge" {
				ev.JudgeModel = "judge-model"
				ev.JudgeProvider = "judge-provider"
			}
			accumulateEval(st, gen, ev, RuleDecl{Name: "rule"}, true)
			expected := map[string]bool{
				"agento11y_eval_scores_total":            false,
				"agento11y_eval_score_values_total":      false,
				"agento11y_eval_executions_total":        false,
				"agento11y_eval_duration_seconds_count":  false,
				"agento11y_eval_rule_action_fires_total": false,
			}
			for _, series := range st.Collect(time.Unix(0, 0)) {
				if _, wanted := expected[series.Name]; wanted {
					expected[series.Name] = true
					if series.Name == "agento11y_eval_rule_action_fires_total" {
						continue
					}
					if got := series.Labels["eval_ai_request_model"]; got != gen.Model {
						t.Errorf("%s eval_ai_request_model=%q, want scored model %q", series.Name, got, gen.Model)
					}
					_, hasRole := series.Labels["evaluator_role"]
					wantsRole := series.Name == "agento11y_eval_scores_total" || series.Name == "agento11y_eval_score_values_total"
					if hasRole != wantsRole || (hasRole && series.Labels["evaluator_role"] != "outcome") {
						t.Errorf("%s evaluator_role shape differs from capture: %v", series.Name, series.Labels)
					}
					for _, forbidden := range []string{"model", "provider", "agent_version"} {
						if _, exists := series.Labels[forbidden]; exists {
							t.Errorf("%s unexpectedly carries %s", series.Name, forbidden)
						}
					}
				}
			}
			for name, seen := range expected {
				if !seen {
					t.Errorf("missing captured family %s", name)
				}
			}
		})
	}
}

func TestEvalEnqueueAndJudgeCostFamiliesMatchCapture(t *testing.T) {
	gen := sigil.Generation{
		ID:        "captured-eval",
		AgentName: "reference-agent",
		Model:     "scored-model",
		Provider:  "scored-provider",
		Usage:     sigil.Usage{Input: 916, Output: 84},
	}
	rule := RuleDecl{Name: "captured-rule", SampleRate: 1, MatchAgent: []string{"reference-agent"}, Evaluators: []string{"judge", "heuristic"}}
	e := newEvalEngine([]EvalDecl{
		{Name: "judge", Kind: "llm_judge", ScoreKey: "quality", ValueType: "number", JudgeModel: "claude-haiku-4-5", JudgeProvider: "bedrock"},
		{Name: "heuristic", Kind: "heuristic", ScoreKey: "toxicity", ValueType: "bool"},
	}, []RuleDecl{rule})

	st := state.NewState()
	scores := e.scoreConversation(AgentDecl{Name: "reference-agent"}, []sigil.Generation{gen}, st, 0)
	if len(scores) != 2 {
		t.Fatalf("got %d scores, want one per evaluator", len(scores))
	}
	series := st.Collect(time.Unix(0, 0))

	for _, kind := range []string{"llm_judge", "heuristic"} {
		s := findSeries(series, metricEvalEnqueueTotal, map[string]string{
			evalLabelEvaluatorKind: kind,
			evalLabelRule:          rule.Name,
		})
		if s == nil {
			t.Fatalf("missing enqueue series for %s", kind)
		}
		if s.Value != 1 {
			t.Errorf("enqueue %s value=%v, want 1", kind, s.Value)
		}
		if got, want := labelKeys(s.Labels), []string{evalLabelEvaluatorKind, evalLabelRule}; !equalStrings(got, want) {
			t.Errorf("enqueue %s labels=%v, want keys %v", kind, got, want)
		}
	}

	cost := findSeries(series, metricEvalJudgeCostUSDTotal, map[string]string{
		evalLabelEvaluator:     "judge",
		evalLabelEvaluatorKind: "llm_judge",
		evalLabelRule:          rule.Name,
		evalLabelGenAIAgent:    gen.AgentName,
		evalLabelGenAIModel:    gen.Model,
		evalLabelGenAIProvider: gen.Provider,
		evalLabelModel:         "claude-haiku-4-5",
		evalLabelProvider:      "bedrock",
	})
	if cost == nil {
		t.Fatal("missing judge cost series")
	}
	if math.Abs(cost.Value-0.001336) > 1e-12 {
		t.Fatalf("judge cost=%0.12f, want 0.001336 from 916 input and 84 output tokens", cost.Value)
	}
	wantCostKeys := []string{
		evalLabelEvaluator, evalLabelEvaluatorKind, evalLabelGenAIAgent, evalLabelGenAIModel,
		evalLabelGenAIProvider, evalLabelModel, evalLabelProvider, evalLabelRule,
	}
	sort.Strings(wantCostKeys)
	if got := labelKeys(cost.Labels); !equalStrings(got, wantCostKeys) {
		t.Errorf("judge cost labels=%v, want keys %v", got, wantCostKeys)
	}

	if heuristicCost := findSeries(series, metricEvalJudgeCostUSDTotal, map[string]string{evalLabelEvaluator: "heuristic"}); heuristicCost != nil {
		t.Fatalf("heuristic evaluator emitted judge cost: %+v", heuristicCost)
	}
}

func TestEvalEnqueueEqualsSampledScoresPerRule(t *testing.T) {
	e := newEvalEngine(
		[]EvalDecl{{Name: "judge", Kind: "llm_judge", ScoreKey: "quality", ValueType: "number", JudgeModel: "claude-haiku-4-5", JudgeProvider: "bedrock"}},
		[]RuleDecl{
			{Name: "half", SampleRate: 0.5, MatchAgent: []string{"agent"}, Evaluators: []string{"judge"}},
			{Name: "all", SampleRate: 1, MatchAgent: []string{"agent"}, Evaluators: []string{"judge"}},
		},
	)
	gens := make([]sigil.Generation, 8)
	for i := range gens {
		gens[i] = sigil.Generation{ID: fmt.Sprintf("sample-%d", i), AgentName: "agent"}
	}
	st := state.NewState()
	scores := e.scoreConversation(AgentDecl{Name: "agent"}, gens, st, 0)
	wantByRule := map[string]float64{}
	for _, score := range scores {
		wantByRule[score.RuleID]++
	}
	for _, rule := range []string{"half", "all"} {
		got := findSeries(st.Collect(time.Unix(0, 0)), metricEvalEnqueueTotal, map[string]string{
			evalLabelEvaluatorKind: "llm_judge",
			evalLabelRule:          rule,
		})
		want := wantByRule[rule]
		if want == 0 {
			if got != nil {
				t.Errorf("rule %q emitted enqueue series with no scores: %+v", rule, got)
			}
			continue
		}
		if got == nil {
			t.Fatalf("rule %q missing enqueue series for %v scores", rule, want)
		}
		if got.Value != want {
			t.Errorf("rule %q enqueue=%v, want sampled score count %v", rule, got.Value, want)
		}
	}
}

func TestEvalJudgeCostAccumulatesInState(t *testing.T) {
	gen := sigil.Generation{
		ID:        "cumulative-eval",
		AgentName: "agent",
		Model:     "scored-model",
		Provider:  "scored-provider",
		Usage:     sigil.Usage{Input: 916, Output: 84},
	}
	ev := EvalDecl{Name: "judge", Kind: "llm_judge", JudgeModel: "claude-haiku-4-5", JudgeProvider: "bedrock"}
	rule := RuleDecl{Name: "rule"}
	st := state.NewState()
	accumulateEval(st, gen, ev, rule, true)
	accumulateEval(st, gen, ev, rule, true)

	cost := findSeries(st.Collect(time.Unix(0, 0)), metricEvalJudgeCostUSDTotal, nil)
	if cost == nil {
		t.Fatal("missing cumulative judge cost series")
	}
	if math.Abs(cost.Value-0.002672) > 1e-12 {
		t.Fatalf("cumulative judge cost=%0.12f, want 0.002672", cost.Value)
	}
}

func findSeries(series []promrw.Series, name string, labels map[string]string) *promrw.Series {
	for i := range series {
		if series[i].Name != name {
			continue
		}
		match := true
		for key, want := range labels {
			if series[i].Labels[key] != want {
				match = false
				break
			}
		}
		if match {
			return &series[i]
		}
	}
	return nil
}

func labelKeys(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func equalStrings(a, b []string) bool {
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
