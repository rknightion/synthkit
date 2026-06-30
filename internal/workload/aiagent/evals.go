// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"math"
	"path"

	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/state"
)

// sigil_eval_* metric LABEL keys, live-captured from emea-cloud-demokit 2026-06-30. These are the
// OTLP→Prom translated convention the stack actually uses — DELIBERATELY different from the
// gen_ai_client_* labels (e.g. eval uses gen_ai_agent_name + gen_ai_request_provider, where the
// client metrics use agent_name + gen_ai_provider_name). Never invent — see signals/sigil.md.
const (
	evalLabelEvaluator     = "evaluator"
	evalLabelEvaluatorKind = "evaluator_kind"
	evalLabelRule          = "rule"
	evalLabelGenAIAgent    = "gen_ai_agent_name"
	evalLabelGenAIModel    = "gen_ai_request_model"
	evalLabelGenAIProvider = "gen_ai_request_provider"
	evalLabelEvalModel     = "eval_ai_request_model" // the (versioned) judge model on score/exec metrics
	evalLabelScoreKey      = "score_key"
	evalLabelPassed        = "passed"      // 3-valued: true|false|unknown
	evalLabelScoreValue    = "score_value" // bool/string scores only
	evalLabelStatus        = "status"      // executions/judge_requests: success|failed; queue_depth: queued|failed
	evalLabelResult        = "result"      // rule_action_fires: no_actions
	evalLabelModel         = "model"       // judge_* metrics
	evalLabelProvider      = "provider"    // judge_* metrics
	evalLabelDirection     = "direction"   // judge_tokens: input|output
	evalLabelErrorType     = "error_type"  // judge_errors
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
// conversation's scores are stable for a given run's ids. regress (0..1) is the active
// eval_quality_regression intensity: it biases scores toward failing without breaking determinism.
// Failed generations (CallError set) are not scored — there is no completion to evaluate. Returns
// the emitted Scores.
func (e *evalEngine) scoreConversation(agent AgentDecl, gens []sigil.Generation, st *state.State, regress float64) []sigil.Score {
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
				if gen.CallError != "" {
					continue // a failed generation produced no completion to evaluate
				}
				if !sampled(gen.ID, evName, rule.SampleRate) {
					continue
				}
				score, passed := scoreValue(gen.ID, ev, regress)
				out = append(out, buildScore(agent, ev, rule, gen, score, passed))
				accumulateEval(st, gen, ev, rule, passed)
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

// scoreValue returns a deterministic (value, passed) for a (generation, evaluator). regress (0..1) is
// the active eval_quality_regression intensity (0 ⇒ baseline): it shifts the distribution toward
// failing, deterministically per genID (no wall-clock), so two -dump runs still agree.
//   - number: scaled to a realistic rubric range (1..5 for threshold≤5, else 1..10), skewed toward
//     the upper range so MOST generations clear the threshold — matching observed online-eval
//     distributions. `value` is on the rubric scale (not [0,1)), so it compares to the threshold.
//     Under regression the center drops (≈0.45 of the scale at full intensity) so more turns fail.
//   - bool/string: a [0,1) draw skewed to pass ~90% (detection-style safety evals rarely flag);
//     under regression the pass bar rises so the flagged rate climbs. buildScore turns a non-pass
//     into the flagged value (Bool=detected=!passed).
func scoreValue(genID string, ev EvalDecl, regress float64) (float64, bool) {
	if regress < 0 {
		regress = 0
	} else if regress > 1 {
		regress = 1
	}
	u := seedUnit(genID, "score-"+ev.Name)
	if ev.ValueType == "number" {
		maxScale := 5.0
		if ev.Threshold > 5 {
			maxScale = 10.0
		}
		base := 0.55 - 0.45*regress              // baseline 0.55 → 0.10 at full regression
		v := math.Round(maxScale * (base + 0.42*u)) // rounded to a rubric point
		v = math.Max(1, math.Min(maxScale, v))
		return v, v >= ev.Threshold
	}
	return u, u >= (0.1 + 0.6*regress) // bool/string: ~90% pass baseline → ~30% pass at full regression
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

// accumulateEval folds the sigil_eval_* observations for one scoring event into st, with the label
// shapes LIVE-CAPTURED from emea-cloud-demokit 2026-06-30 (NOT the backend-code short names — the
// stack uses the OTLP→Prom translated convention). Key realities encoded here:
//   - identity labels: evaluator / evaluator_kind / rule / gen_ai_agent_name / gen_ai_request_model /
//     gen_ai_request_provider, plus eval_ai_request_model (the judge model) for llm_judge.
//   - scores_total carries score_key + passed (3-valued: true|false|unknown — unknown for string
//     scores with no pass_value). NO agent_version (absent live).
//   - score_values_total is emitted ONLY for bool/string scores (numeric scores are not enumerated as
//     label values — unbounded cardinality), and adds score_value (the value as a string).
//   - executions_total carries status (success), NOT passed.
//   - rule_action_fires_total is {rule, result}; result=no_actions absent action rules.
//   - queue_depth is {status}; judge_* metrics are {model, provider} (+ status/direction/error_type).
func accumulateEval(st *state.State, gen sigil.Generation, ev EvalDecl, rule RuleDecl, passed bool) {
	// Shared identity labels (scores_total / score_values_total / executions_total / duration_seconds).
	ident := map[string]string{
		evalLabelEvaluator:     ev.Name,
		evalLabelEvaluatorKind: ev.Kind,
		evalLabelRule:          rule.Name,
		evalLabelGenAIAgent:    gen.AgentName,
	}
	if gen.Model != "" {
		ident[evalLabelGenAIModel] = gen.Model
	}
	if gen.Provider != "" {
		ident[evalLabelGenAIProvider] = gen.Provider
	}
	if ev.JudgeModel != "" {
		ident[evalLabelEvalModel] = ev.JudgeModel // eval_ai_request_model = the judge model
	}

	passedStr := evalPassedString(ev, passed)

	scoresLabels := cloneLabels(ident)
	scoresLabels[evalLabelScoreKey] = ev.ScoreKey
	scoresLabels[evalLabelPassed] = passedStr
	st.Add(sigil.MetricEvalScoresTotal, scoresLabels, 1)

	// score_values_total: bool/string scores ONLY (numeric values are not enumerated as labels).
	if sv, ok := evalScoreValue(ev, passed); ok {
		valueLabels := cloneLabels(scoresLabels)
		valueLabels[evalLabelScoreValue] = sv
		st.Add(sigil.MetricEvalScoreValuesTotal, valueLabels, 1)
	}

	// executions_total: identity + status (eval-execution outcome), NOT passed.
	execLabels := cloneLabels(ident)
	execLabels[evalLabelStatus] = "success"
	st.Add(sigil.MetricEvalExecutionsTotal, execLabels, 1)

	// rule_action_fires_total: {rule, result}. No action rules configured ⇒ result=no_actions.
	st.Add(sigil.MetricEvalRuleActionFiresTotal, map[string]string{evalLabelRule: rule.Name, evalLabelResult: "no_actions"}, 1)

	// duration_seconds: identity labels (live buckets).
	st.Observe(sigil.MetricEvalDurationSeconds, cloneLabels(ident), sigil.EvalDurationBuckets, state.LEDotZero, evalDurationSec(ev))

	// NOTE: sigil_eval_queue_depth ({status} only — see evalvocab.go) is DELIBERATELY NOT emitted.
	// It is a backend-GLOBAL gauge with zero per-backend identity in the live schema, so emitting it
	// from more than one ai_agent fleet in a single push would produce a duplicate series (Mimir
	// rejects identical name+labels). Every other eval family carries gen_ai_agent_name/evaluator/
	// rule/model and so disambiguates across fleets. Documented in signals/sigil.md as captured reality.

	if ev.Kind == "llm_judge" {
		// Judge metrics: {model, provider} (the evaluator label is GC-aggregated away on the stack).
		judge := map[string]string{evalLabelModel: ev.JudgeModel, evalLabelProvider: ev.JudgeProvider}
		st.Observe(sigil.MetricEvalJudgeDurationSeconds, cloneLabels(judge), sigil.EvalDurationBuckets, state.LEDotZero, evalDurationSec(ev)*0.8)
		// tokens by direction (input/output).
		for _, dir := range []string{"input", "output"} {
			jt := cloneLabels(judge)
			jt[evalLabelDirection] = dir
			st.Add(sigil.MetricEvalJudgeTokensTotal, jt, float64(100+seedHash(ev.Name, "jtok-"+dir)%700))
		}
		jr := cloneLabels(judge)
		jr[evalLabelStatus] = "success"
		st.Add(sigil.MetricEvalJudgeRequestsTotal, jr, 1)
		// judge_errors_total: rare (seeded ~2%); error_type=unknown (the only live value).
		if seedUnit(gen.ID, "judge-err-"+ev.Name) < 0.02 {
			je := cloneLabels(judge)
			je[evalLabelErrorType] = "unknown"
			st.Add(sigil.MetricEvalJudgeErrorsTotal, je, 1)
		}
	}
}

// evalPassedString maps the pass outcome to the live 3-valued passed label: string-typed scores
// carry no pass_value, so they report "unknown"; number/bool report true|false.
func evalPassedString(ev EvalDecl, passed bool) string {
	if ev.ValueType == "string" {
		return "unknown"
	}
	if passed {
		return "true"
	}
	return "false"
}

// evalScoreValue returns the score_value label (the actual value as a string) for score_values_total,
// emitted ONLY for bool/string scores. For bool detection evals the value is the DETECTION (= !passed,
// matching buildScore's Bool=!passed). Numeric scores return ok=false (not enumerated as labels).
func evalScoreValue(ev EvalDecl, passed bool) (string, bool) {
	switch ev.ValueType {
	case "bool":
		if passed {
			return "false", true // pass ⇒ not detected
		}
		return "true", true
	case "string":
		if passed {
			return "pass", true
		}
		return "fail", true
	default:
		return "", false // number: absent from score_values_total
	}
}

// evalDurationSec derives a small deterministic eval wall-clock duration per evaluator.
func evalDurationSec(ev EvalDecl) float64 {
	return 0.05 + seedUnit(ev.Name, "evaldur")*1.5
}
