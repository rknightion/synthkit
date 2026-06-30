// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"math"
	"path"

	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/state"
)

// evalEngine holds the resolved evaluator + rule apparatus for a workload (built once at Build).
type evalEngine struct {
	evaluators map[string]EvalDecl // by name
	rules      []RuleDecl
}

func newEvalEngine(evals []EvalDecl, rules []RuleDecl) *evalEngine {
	byName := make(map[string]EvalDecl, len(evals))
	for _, e := range evals {
		byName[e.Name] = e
	}
	return &evalEngine{evaluators: byName, rules: rules}
}

// scoreConversation samples each rule that matches the agent and, for every deterministically
// sampled generation, emits a sigil.Score (Lane A) and accumulates the sigil_eval_* observations
// into st (Lane C). The score value/passed are deterministic per (generationID, evaluatorID) so a
// conversation's scores are stable for a given run's ids. Returns the emitted Scores.
func (e *evalEngine) scoreConversation(agent AgentDecl, gens []sigil.Generation, st *state.State) []sigil.Score {
	if e == nil || len(e.rules) == 0 || len(gens) == 0 {
		return nil
	}
	var out []sigil.Score
	for _, rule := range e.rules {
		if !ruleMatchesAgent(rule, agent.Name) {
			continue
		}
		for _, evName := range rule.Evaluators {
			ev, ok := e.evaluators[evName]
			if !ok {
				continue
			}
			for gi := range gens {
				gen := gens[gi]
				if !sampled(gen.ID, evName, rule.SampleRate) {
					continue
				}
				score, passed := scoreValue(gen.ID, ev)
				out = append(out, buildScore(agent, ev, rule, gen, score, passed))
				accumulateEval(st, agent, ev, rule, passed)
			}
		}
	}
	return out
}

// ruleMatchesAgent reports whether any of a rule's match_agent globs match the agent name. An empty
// match list matches all agents.
func ruleMatchesAgent(rule RuleDecl, agentName string) bool {
	if len(rule.MatchAgent) == 0 {
		return true
	}
	for _, g := range rule.MatchAgent {
		if g == agentName {
			return true
		}
		if ok, _ := path.Match(g, agentName); ok {
			return true
		}
	}
	return false
}

// sampled deterministically decides whether a generation is sampled by a rule at sampleRate, keyed
// on (generationID, evaluatorName) so it is stable per run. sampleRate>=1 always samples.
func sampled(genID, evName string, sampleRate float64) bool {
	if sampleRate >= 1 {
		return true
	}
	if sampleRate <= 0 {
		return false
	}
	return seedUnit(genID, "sample-"+evName) < sampleRate
}

// scoreValue returns a deterministic (value, passed) for a (generation, evaluator).
//   - number: scaled to a realistic rubric range (1..5 for threshold≤5, else 1..10), skewed toward
//     the upper range so MOST generations clear the threshold — matching observed online-eval
//     distributions. `value` is on the rubric scale (not [0,1)), so it compares to the threshold.
//   - bool/string: a [0,1) draw skewed to pass ~90% (detection-style safety evals rarely flag);
//     buildScore turns a non-pass into the flagged value (Bool=detected=!passed).
func scoreValue(genID string, ev EvalDecl) (float64, bool) {
	u := seedUnit(genID, "score-"+ev.Name)
	if ev.ValueType == "number" {
		maxScale := 5.0
		if ev.Threshold > 5 {
			maxScale = 10.0
		}
		v := math.Round(maxScale * (0.55 + 0.42*u)) // ~0.55..0.97 of the scale, rounded to a rubric point
		v = math.Max(1, math.Min(maxScale, v))
		return v, v >= ev.Threshold
	}
	return u, u >= 0.1 // bool/string: ~90% pass (rarely flagged)
}

// buildScore assembles a sigil.Score for a sampled generation.
func buildScore(agent AgentDecl, ev EvalDecl, rule RuleDecl, gen sigil.Generation, value float64, passed bool) sigil.Score {
	num := value
	score := sigil.Score{
		ScoreID:        uuidLike(gen.ID, "score-"+ev.Name),
		GenerationID:   gen.ID,
		ConversationID: gen.ConversationID,
		TraceID:        gen.TraceID,
		SpanID:         gen.SpanID,
		EvaluatorID:    ev.Name,
		RuleID:         rule.Name,
		ScoreKey:       ev.ScoreKey,
		HasPassed:      true,
		Passed:         passed,
		CreatedAt:      gen.EndedAt,
		Source:         sigil.ScoreSource{Kind: "online"},
	}
	switch ev.ValueType {
	case "bool":
		// Detection-style safety evals: the bool is the DETECTION (true = flagged), so a passing
		// check means NOT detected. Bool = !passed (most generations: false/safe, passed=true).
		detected := !passed
		score.Bool = &detected
	case "string":
		if passed {
			score.String = "pass"
		} else {
			score.String = "fail"
		}
	default: // number
		score.Number = &num
	}
	return score
}

// accumulateEval folds the sigil_eval_* observations for one scoring event into st. counters via Add,
// the queue-depth via Set, and the judge histograms/counters for llm_judge evaluators.
func accumulateEval(st *state.State, agent AgentDecl, ev EvalDecl, rule RuleDecl, passed bool) {
	base := map[string]string{
		"evaluator_name": ev.Name,
		"evaluator_kind": ev.Kind,
		"score_key":      ev.ScoreKey,
		labelAgentName:   agent.Name,
	}
	if agent.Version != "" {
		base[labelAgentVersion] = agent.Version
	}
	passedStr := "false"
	if passed {
		passedStr = "true"
	}

	st.Add(sigil.MetricEvalScoresTotal, base, 1)

	valueLabels := cloneLabels(base)
	valueLabels["passed"] = passedStr
	st.Add(sigil.MetricEvalScoreValuesTotal, valueLabels, 1)

	execLabels := map[string]string{
		"evaluator_name": ev.Name,
		"evaluator_kind": ev.Kind,
		"passed":         passedStr,
	}
	st.Add(sigil.MetricEvalExecutionsTotal, execLabels, 1)

	ruleLabels := map[string]string{"rule_name": rule.Name, "evaluator_name": ev.Name}
	st.Add(sigil.MetricEvalRuleActionFiresTotal, ruleLabels, 1)

	durLabels := map[string]string{"evaluator_name": ev.Name, "evaluator_kind": ev.Kind}
	st.Observe(sigil.MetricEvalDurationSeconds, durLabels, genai.OpDurationBuckets, state.LEDotZero, evalDurationSec(ev))

	// queue depth (instantaneous gauge — a small bounded value).
	st.Set(sigil.MetricEvalQueueDepth, map[string]string{"evaluator_name": ev.Name}, float64(seedHash(ev.Name, "queue")%8))

	if ev.Kind == "llm_judge" {
		judgeLabels := map[string]string{"evaluator_name": ev.Name, "judge_model": ev.JudgeModel}
		st.Observe(sigil.MetricEvalJudgeDurationSeconds, judgeLabels, genai.OpDurationBuckets, state.LEDotZero, evalDurationSec(ev)*0.8)
		st.Add(sigil.MetricEvalJudgeTokensTotal, judgeLabels, float64(200+seedHash(ev.Name, "jtok")%800))
		st.Add(sigil.MetricEvalJudgeRequestsTotal, judgeLabels, 1)
	}
}

// evalDurationSec derives a small deterministic eval wall-clock duration per evaluator.
func evalDurationSec(ev EvalDecl) float64 {
	return 0.05 + seedUnit(ev.Name, "evaldur")*1.5
}
