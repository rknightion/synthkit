// SPDX-License-Identifier: AGPL-3.0-only

package sigil

// sigil_eval_* metric names (final pre-mangled Prometheus form, emitted via promrw).
// These are the eval/score metric families captured from live sigil stacks (signals/sigil.md).
// Names are VERBATIM — never invent. All ten live-confirmed on emea-cloud-demokit 2026-06-30.
const (
	MetricEvalScoresTotal          = "sigil_eval_scores_total"
	MetricEvalScoreValuesTotal     = "sigil_eval_score_values_total"
	MetricEvalExecutionsTotal      = "sigil_eval_executions_total"
	MetricEvalRuleActionFiresTotal = "sigil_eval_rule_action_fires_total"
	MetricEvalDurationSeconds      = "sigil_eval_duration_seconds"
	MetricEvalJudgeDurationSeconds = "sigil_eval_judge_duration_seconds"
	MetricEvalJudgeTokensTotal     = "sigil_eval_judge_tokens_total"
	MetricEvalJudgeRequestsTotal   = "sigil_eval_judge_requests_total"
	MetricEvalJudgeErrorsTotal     = "sigil_eval_judge_errors_total" // live-captured 2026-06-30 (was undocumented)
	MetricEvalQueueDepth           = "sigil_eval_queue_depth"
)

// EvalDurationBuckets are the live-captured histogram boundaries for sigil_eval_duration_seconds +
// sigil_eval_judge_duration_seconds (base-2 from 0.01s — NOT the genai base-2 second set). Verbatim
// from emea-cloud-demokit live capture 2026-06-30.
var EvalDurationBuckets = []float64{0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92}
