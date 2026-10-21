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

// providerCallErrorMsg is the synthetic native call_error content. SDK span
// attributes and client metric dimensions below use SDK 75a1c548
// generationErrorType/classifyErrorCategory, not raw provider JSON error names.
const providerCallErrorMsg = "provider call failed"

type providerErrorClass struct{ typ, cat string }

// Preserve the existing seeded cause mix: two server-failure slots (5xx),
// one throttling slot (429), and one deadline/cancellation slot. Every cause is
// a provider call failure at the SDK operation level.
var providerErrorClasses = []providerErrorClass{
	{"provider_call_error", "server_error"},
	{"provider_call_error", "rate_limit"},
	{"provider_call_error", "server_error"},
	{"provider_call_error", "timeout"},
}

// FailureModes are the modes the ai_agent workload responds to, on AxisWorkload (per-instance
// targeting: a blueprint incident targets the workload by its instance name). The proto +
// Lane-B status plumbing is unconditional; these modes drive WHEN it fires.
var FailureModes = []failuremode.Mode{
	{Name: modeProviderCallError, Axis: failuremode.AxisWorkload, Help: "elevated provider/LLM call-error rate on the targeted ai_agent fleet — call_error generations, ERROR spans, error_type/error_category on operation_duration"},
	{Name: modeEvalQualityRegression, Axis: failuremode.AxisWorkload, Help: "online-eval quality regresses on the targeted ai_agent fleet — agento11y_eval_scores_total{passed=false} rate rises"},
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
	errored := map[string]providerErrorClass{}
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
		errored[gens[i].ID] = ec
	}
	if len(errored) == 0 {
		return
	}
	// Flip the matching root LLM span (the only span carrying sigil.generation.id) to ERROR.
	for ri := range resources {
		for si := range resources[ri].Spans {
			sp := &resources[ri].Spans[si]
			if gid, ok := sp.Attrs[sigil.AttrGenerationID].(string); ok {
				if ec, failed := errored[gid]; failed {
					sp.Status = otlp.StatusError
					sp.Attrs["error.type"] = ec.typ
					sp.Attrs["error.category"] = ec.cat
				}
			}
		}
	}
}
