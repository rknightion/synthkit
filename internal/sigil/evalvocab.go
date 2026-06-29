// SPDX-License-Identifier: AGPL-3.0-only

package sigil

// sigil_eval_* metric names (final pre-mangled Prometheus form, emitted via promrw).
// These are the eval/score metric families captured from live sigil stacks (signals/sigil.md).
// Names are VERBATIM — never invent.
const (
	MetricEvalScoresTotal          = "sigil_eval_scores_total"
	MetricEvalScoreValuesTotal     = "sigil_eval_score_values_total"
	MetricEvalExecutionsTotal      = "sigil_eval_executions_total"
	MetricEvalRuleActionFiresTotal = "sigil_eval_rule_action_fires_total"
	MetricEvalDurationSeconds      = "sigil_eval_duration_seconds"
	MetricEvalJudgeDurationSeconds = "sigil_eval_judge_duration_seconds"
	MetricEvalJudgeTokensTotal     = "sigil_eval_judge_tokens_total"
	MetricEvalJudgeRequestsTotal   = "sigil_eval_judge_requests_total"
	MetricEvalQueueDepth           = "sigil_eval_queue_depth"
)
