// SPDX-License-Identifier: AGPL-3.0-only

package sigil

// agento11y_eval_* metric names (final pre-mangled Prometheus form, emitted via promrw).
// These are the eval/score metric families captured from live sigil stacks (signals/sigil.md).
// Names are verbatim from the 2026-09-06 reference capture. Judge errors and queue depth
// are confirmed by the current upstream definition but were not observed in this capture.
const (
	MetricEvalScoresTotal          = "agento11y_eval_scores_total"
	MetricEvalScoreValuesTotal     = "agento11y_eval_score_values_total"
	MetricEvalExecutionsTotal      = "agento11y_eval_executions_total"
	MetricEvalRuleActionFiresTotal = "agento11y_eval_rule_action_fires_total"
	MetricEvalDurationSeconds      = "agento11y_eval_duration_seconds"
	MetricEvalJudgeDurationSeconds = "agento11y_eval_judge_duration_seconds"
	MetricEvalJudgeTokensTotal     = "agento11y_eval_judge_tokens_total"
	MetricEvalJudgeRequestsTotal   = "agento11y_eval_judge_requests_total"
	MetricEvalJudgeErrorsTotal     = "agento11y_eval_judge_errors_total" // current upstream definition; no error sample in the new capture
	MetricEvalQueueDepth           = "agento11y_eval_queue_depth"
)

// EvalDurationBuckets is the current backend worker execution histogram,
// sourced from bc963fb09 sigil/internal/eval/worker/metrics.go.
var EvalDurationBuckets = []float64{0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92, 163.84, 327.68}

// JudgeDurationBuckets remains shorter: sigil/internal/inference/service.go.
var JudgeDurationBuckets = []float64{0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92}
