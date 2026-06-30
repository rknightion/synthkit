// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"time"

	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// Failure-mode names (AxisWorkload — targeted in a blueprint by the ai_agent workload INSTANCE name,
// the scope the workload passes to shape.Eval). Both are honoured in ProjectBatch.
const (
	modeProviderCallError     = "provider_call_error"
	modeEvalQualityRegression = "eval_quality_regression"
)

// providerCallErrorMsg is the synthetic Generation.CallError stamped on a failed turn (drives Lane-B
// status=ERROR via spanStatus + the Lane-A call_error). The error_type/error_category metric label
// VALUES below are real Anthropic/OpenAI provider error vocabulary (synthetic fault injection — the
// label NAMES error_type/error_category are the signals/sigil.md-sourced keys, never invented).
const providerCallErrorMsg = "provider call failed"

var providerErrorClasses = []struct{ typ, cat string }{
	{"overloaded_error", "server"},
	{"rate_limit_error", "rate_limit"},
	{"api_error", "server"},
	{"timeout", "timeout"},
}

// FailureModes are the modes the ai_agent workload responds to, on AxisWorkload (per-instance
// targeting: a blueprint incident targets the workload by its instance name). The proto +
// Lane-B status plumbing is unconditional; these modes drive WHEN it fires.
var FailureModes = []failuremode.Mode{
	{Name: modeProviderCallError, Axis: failuremode.AxisWorkload, Help: "elevated provider/LLM call-error rate on the targeted ai_agent fleet — call_error generations, ERROR spans, error_type/error_category on operation_duration"},
	{Name: modeEvalQualityRegression, Axis: failuremode.AxisWorkload, Help: "online-eval quality regresses on the targeted ai_agent fleet — sigil_eval_score_values_total{passed=false} rate rises"},
}

// failCtx is the per-ProjectBatch resolved failure intensity for each mode (0 ⇒ inactive), read once
// from the shape engine (UNION of scheduled incident windows + live control-plane state).
type failCtx struct {
	providerErr float64
	evalRegress float64
}

// resolveFailures evaluates the workload's failure modes for this batch's instant + scope (the
// workload instance name — the AxisWorkload target a blueprint addresses).
func (w *Workload) resolveFailures(now time.Time, eng *shape.Engine) failCtx {
	var fc failCtx
	if eng == nil {
		return fc
	}
	if active, inten := eng.Eval(now, modeProviderCallError, w.Name()); active {
		fc.providerErr = inten
	}
	if active, inten := eng.Eval(now, modeEvalQualityRegression, w.Name()); active {
		fc.evalRegress = inten
	}
	return fc
}

// applyProviderErrors marks a deterministic ~providerErr fraction of generations as provider call
// errors: sets Generation.CallError, sets the matching Lane-B root span status to ERROR, and stamps
// error_type/error_category onto the matching Lane-C observation. gens[i] ↔ obs[i] by construction
// (buildConversation appends each generation with its observation in the same order). Determinism:
// the per-generation decision is seedUnit(gen.ID,…) — never wall-clock — so two -dump runs agree.
func applyProviderErrors(fc failCtx, gens []sigil.Generation, resources []otlp.Resource, obs []metricObs) {
	if fc.providerErr <= 0 {
		return
	}
	errored := map[string]bool{}
	for i := range gens {
		if seedUnit(gens[i].ID, "callerr") >= fc.providerErr {
			continue
		}
		gens[i].CallError = providerCallErrorMsg
		ec := providerErrorClasses[seedHash(gens[i].ID, "errclass")%uint64(len(providerErrorClasses))]
		if i < len(obs) {
			obs[i].errorType = ec.typ
			obs[i].errorCategory = ec.cat
		}
		errored[gens[i].ID] = true
	}
	if len(errored) == 0 {
		return
	}
	// Flip the matching root LLM span (the only span carrying sigil.generation.id) to ERROR.
	for ri := range resources {
		for si := range resources[ri].Spans {
			sp := &resources[ri].Spans[si]
			if gid, ok := sp.Attrs[sigil.AttrGenerationID].(string); ok && errored[gid] {
				sp.Status = otlp.StatusError
			}
		}
	}
}
