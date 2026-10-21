// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"math"
	"path"
	"strings"

	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	metricEvalEnqueueTotal      = "agento11y_eval_enqueue_total"
	metricEvalJudgeCostUSDTotal = "agento11y_eval_judge_cost_usd_total"
)

// Tenant-push eval labels from backend bc963fb09. Both SDK and eval families
// use gen_ai_agent_name; eval alone spells generation provider gen_ai_request_provider.
// The separate eval_ai_request_* labels identify the configured judge target.
const (
	evalLabelEvaluator     = "evaluator"
	evalLabelEvaluatorKind = "evaluator_kind"
	evalLabelRule          = "rule"
	evalLabelGenAIAgent    = "gen_ai_agent_name"
	evalLabelGenAIModel    = "gen_ai_request_model"
	evalLabelGenAIProvider = "gen_ai_request_provider"
	evalLabelEvalModel     = "eval_ai_request_model" // configured judge target, absent for non-judge
	evalLabelEvalProvider  = "eval_ai_request_provider"
	evalLabelScoreKey      = "score_key"
	evalLabelPassed        = "passed"     // 3-valued: true|false|unknown
	evalLabelStatus        = "status"     // executions/judge_requests: success|failed; queue_depth: queued|failed
	evalLabelResult        = "result"     // rule_action_fires: no_actions
	evalLabelModel         = "model"      // judge_* metrics
	evalLabelProvider      = "provider"   // judge_* metrics
	evalLabelErrorType     = "error_type" // judge_errors
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
// sampled generation, emits a sigil.Score (Lane A) and accumulates the agento11y_eval_* observations
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
		for gi := range gens {
			gen := gens[gi]
			for _, evName := range rule.Evaluators {
				ev, ok := e.evaluators[evName]
				if !ok {
					if rule.ExecutionMode == "sequential" {
						break
					} // never skip an unexecuted gate
					continue
				}
				if gen.CallError != "" {
					continue // a failed generation produced no completion to evaluate
				}
				sampleKey := evName
				if rule.ExecutionMode == "sequential" {
					sampleKey = rule.Name
				}
				if !sampled(gen.ID, sampleKey, rule.SampleRate) {
					continue
				}
				value, passed := scoreValue(gen.ID, ev, regress)
				emitted := buildScore(agent, ev, rule, gen, value, passed)
				out = append(out, emitted)
				accumulateEval(st, gen, ev, rule, emitted.Passed)
				if rule.ExecutionMode == "sequential" && !(emitted.HasPassed && emitted.Passed) {
					break
				}
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
		base := 0.55 - 0.45*regress                 // baseline 0.55 → 0.10 at full regression
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
		HasPassed:      ev.ValueType != "string",
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

// accumulateEval models a legacy synthetic eval event using the current push
// vocabulary. Backend-derived score_values and scrape-only judge_tokens are
// never synthesized. Real-rule blueprints must not invoke this synthetic path.
func accumulateEval(st *state.State, gen sigil.Generation, ev EvalDecl, rule RuleDecl, passed bool) {
	// The enqueue is one work item per sampled evaluator scoring event. Sampling happens in
	// scoreConversation before this function is called, so the counter equals scores by rule.
	st.Add(metricEvalEnqueueTotal, map[string]string{
		evalLabelEvaluatorKind: ev.Kind,
		evalLabelRule:          rule.Name,
	}, 1)

	// Shared identity labels (scores / executions / duration).
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
	if ev.Kind == "llm_judge" {
		if ev.JudgeModel != "" {
			ident[evalLabelEvalModel] = ev.JudgeModel
		}
		if ev.JudgeProvider != "" {
			ident[evalLabelEvalProvider] = ev.JudgeProvider
		}
	}
	for k, v := range evalTagLabels(rule.FilterableTagKeys, gen.Tags) {
		ident[k] = v
	}

	passedStr := evalPassedString(ev, passed)

	scoresLabels := cloneLabels(ident)
	scoresLabels[evalLabelScoreKey] = ev.ScoreKey
	scoresLabels["evaluator_role"] = evaluatorRole(rule, ev.Name)
	scoresLabels[evalLabelPassed] = passedStr
	st.Add(sigil.MetricEvalScoresTotal, scoresLabels, 1)

	// score_values_total is emitted by backend scores:export ingest. Never
	// synthesize it as well: that doubles the categorical score population.

	// executions_total: identity + status (eval-execution outcome), NOT passed.
	execLabels := cloneLabels(ident)
	execLabels[evalLabelStatus] = "success"
	st.Add(sigil.MetricEvalExecutionsTotal, execLabels, 1)

	// rule_action_fires_total: {rule, result}. No action rules configured ⇒ result=no_actions.
	st.Add(sigil.MetricEvalRuleActionFiresTotal, map[string]string{evalLabelRule: rule.Name, evalLabelResult: "no_actions"}, 1)

	// duration_seconds: identity labels (live buckets).
	st.Observe(sigil.MetricEvalDurationSeconds, cloneLabels(ident), sigil.EvalDurationBuckets, state.LEDotZero, evalDurationSec(ev))

	// NOTE: agento11y_eval_queue_depth ({status} only — see evalvocab.go) is DELIBERATELY NOT emitted.
	// It is a backend-GLOBAL gauge with zero per-backend identity in the live schema, so emitting it
	// from more than one ai_agent fleet in a single push would produce a duplicate series (Mimir
	// rejects identical name+labels). Every other eval family carries gen_ai_agent_name/evaluator/
	// rule/model and so disambiguates across fleets. Documented in signals/sigil.md as captured reality.

	if ev.Kind == "llm_judge" {
		// Judge metrics: {model, provider} (the evaluator label is GC-aggregated away on the stack).
		judge := map[string]string{}
		if ev.JudgeModel != "" {
			judge[evalLabelModel] = ev.JudgeModel
		}
		if ev.JudgeProvider != "" {
			judge[evalLabelProvider] = ev.JudgeProvider
		}
		st.Observe(sigil.MetricEvalJudgeDurationSeconds, cloneLabels(judge), sigil.JudgeDurationBuckets, state.LEDotZero, evalDurationSec(ev)*0.8)
		// Judge token usage is carried by the sampled generation's ledger-backed Usage. Keep the
		// input/output token observations and cost calculation on this same source of truth.
		inputTokens, outputTokens := gen.Usage.Input, gen.Usage.Output
		// Legacy judge_tokens is scrape-only upstream, absent from the W0
		// tenant lookback. Do not push it; usage remains local to cost math.
		jr := cloneLabels(judge)
		jr[evalLabelStatus] = "success"
		st.Add(sigil.MetricEvalJudgeRequestsTotal, jr, 1)
		if cost := judgeCostUSD(ev.JudgeModel, inputTokens, outputTokens); cost > 0 {
			costLabels := cloneLabels(ident)
			delete(costLabels, evalLabelEvalModel)
			delete(costLabels, evalLabelEvalProvider)
			for k := range costLabels {
				if strings.HasPrefix(k, "agento11y_eval_tag_") {
					delete(costLabels, k)
				}
			}
			if ev.JudgeModel != "" {
				costLabels[evalLabelModel] = ev.JudgeModel
			}
			if ev.JudgeProvider != "" {
				costLabels[evalLabelProvider] = ev.JudgeProvider
			}
			st.Add(metricEvalJudgeCostUSDTotal, costLabels, cost)
		}
		// judge_errors_total: rare (seeded ~2%); error_type=unknown (the only live value).
		if seedUnit(gen.ID, "judge-err-"+ev.Name) < 0.02 {
			je := cloneLabels(judge)
			je[evalLabelErrorType] = "unknown"
			st.Add(sigil.MetricEvalJudgeErrorsTotal, je, 1)
		}
	}
}

// judgeCostUSD prices one sampled judge call from its ledger-backed input/output token counts.
// Unknown model ids produce no cost series: a fabricated price is worse than an absent cost.
func judgeCostUSD(model string, inputTokens, outputTokens int64) float64 {
	if inputTokens < 0 {
		inputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}
	total := inputTokens + outputTokens
	if total == 0 {
		return 0
	}

	inputFraction := float64(inputTokens) / float64(total)
	return float64(total) * genai.BlendedCostPerToken(model, inputFraction)
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

// evalDurationSec derives a small deterministic eval wall-clock duration per evaluator.
func evalDurationSec(ev EvalDecl) float64 {
	return 0.05 + seedUnit(ev.Name, "evaldur")*1.5
}
