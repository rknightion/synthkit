// SPDX-License-Identifier: AGPL-3.0-only

package langsmitheval

import "github.com/rknightion/synthkit/internal/failuremode"

// FailureModes are the failure modes the langsmith_eval construct responds to.
// Scoped to env (Axis=AxisCloud used for env-scoped infra-integration constructs)
// so a fired incident degrades evaluation quality for only the targeted env.
var FailureModes = []failuremode.Mode{
	{
		Name: "eval_quality_degraded",
		Axis: failuremode.AxisCloud,
		Help: "LangSmith eval quality regresses — faithfulness/completeness/relevance and retrieval scores drop while retry/fallback/HITL rates and error/pending run-outcomes climb",
	},
}
