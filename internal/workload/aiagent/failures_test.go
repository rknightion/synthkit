// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/state"
)

// TestApplyProviderErrors: at full intensity every generation is marked errored — CallError set, the
// matching root LLM span flips to ERROR, and the observation carries error_type/error_category.
func TestApplyProviderErrors(t *testing.T) {
	agent := generalOrchAgent()
	agent.Activity.TurnsP50 = 4
	r := fixedReq("conv-err-1", 90*time.Second)
	r.Route = agent.Name
	r.Provider = agent.Provider
	r.Model = agent.Models[0]
	gens, _, spans, obs := buildConversation(ResourceID{ServiceName: "chatservice"}, agent, r)
	if len(gens) == 0 {
		t.Fatal("no generations")
	}

	applyProviderErrors(failCtx{providerErr: 1.0}, gens, spans, obs)

	for i := range gens {
		if gens[i].CallError == "" {
			t.Fatalf("gen %d (%s): CallError empty at intensity 1.0", i, gens[i].AgentName)
		}
		if obs[i].errorType == "" || obs[i].errorCategory == "" {
			t.Fatalf("gen %d: obs error_type/category not stamped (%q/%q)", i, obs[i].errorType, obs[i].errorCategory)
		}
	}
	// Every root LLM span (the ones carrying sigil.generation.id) must be ERROR.
	var roots int
	for _, res := range spans {
		for _, sp := range res.Spans {
			if _, ok := sp.Attrs[sigil.AttrGenerationID].(string); ok {
				roots++
				if sp.Status != otlp.StatusError {
					t.Fatalf("root span %s status=%v, want ERROR", sp.SpanID, sp.Status)
				}
			}
		}
	}
	if roots == 0 {
		t.Fatal("no root LLM spans found")
	}
}

// TestApplyProviderErrorsInactive: zero intensity is a complete no-op (no CallError, no error labels).
func TestApplyProviderErrorsInactive(t *testing.T) {
	agent := autonomousAgent()
	r := fixedReq("conv-err-0", 60*time.Second)
	r.Route = agent.Name
	r.Provider = agent.Provider
	r.Model = agent.Models[0]
	gens, _, spans, obs := buildConversation(ResourceID{ServiceName: "chatservice"}, agent, r)

	applyProviderErrors(failCtx{}, gens, spans, obs)

	for i := range gens {
		if gens[i].CallError != "" {
			t.Fatalf("gen %d: CallError=%q at intensity 0, want empty", i, gens[i].CallError)
		}
		if obs[i].errorType != "" {
			t.Fatalf("gen %d: error_type=%q at intensity 0, want empty", i, obs[i].errorType)
		}
	}
}

// TestApplyProviderErrorsDeterministic: the per-generation error decision is a function of the
// generation id only, so two identical conversations agree (the -dump determinism guarantee).
func TestApplyProviderErrorsDeterministic(t *testing.T) {
	mk := func() []bool {
		agent := generalOrchAgent()
		agent.Activity.TurnsP50 = 6
		r := fixedReq("conv-err-det", 120*time.Second)
		r.Route = agent.Name
		r.Provider = agent.Provider
		r.Model = agent.Models[0]
		gens, _, spans, obs := buildConversation(ResourceID{ServiceName: "chatservice"}, agent, r)
		applyProviderErrors(failCtx{providerErr: 0.5}, gens, spans, obs)
		out := make([]bool, len(gens))
		for i := range gens {
			out[i] = gens[i].CallError != ""
		}
		return out
	}
	a, b := mk(), mk()
	if len(a) != len(b) {
		t.Fatalf("len mismatch %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("error decision %d non-deterministic: %v vs %v", i, a[i], b[i])
		}
	}
}

// TestEvalRegressionLowersPassRate: a high eval_quality_regression intensity drives the number-score
// pass rate well below the baseline, deterministically.
func TestEvalRegressionLowersPassRate(t *testing.T) {
	ev := EvalDecl{Name: "helpfulness", ValueType: "number", Threshold: 4}
	rate := func(regress float64) float64 {
		pass, total := 0, 500
		for i := 0; i < total; i++ {
			if _, ok := scoreValue(genIDf(i), ev, regress); ok {
				pass++
			}
		}
		return float64(pass) / float64(total)
	}
	base := rate(0)
	regressed := rate(1.0)
	if regressed >= base {
		t.Fatalf("regression did not lower pass rate: base=%.2f regressed=%.2f", base, regressed)
	}
	if regressed > 0.25 {
		t.Fatalf("full regression pass rate %.2f too high (expected a steep drop)", regressed)
	}
}

func genIDf(i int) string {
	return uuidLike("regress", string(rune('a'+i%26))+string(rune('0'+i/26%10)))
}

// TestProjectBatchProviderErrorFiresViaEngine: an ACTIVE provider_call_error (via the engine Live
// hook scoped to the workload name) makes ProjectBatch flush a gen_ai_client_operation_duration_seconds
// series carrying error_type/error_category; an inactive engine flushes none. End-to-end through the
// shape.Eval seam — the production path, not just the helper.
func TestProjectBatchProviderErrorFiresViaEngine(t *testing.T) {
	agent := autonomousAgent()
	agent.Activity = Activity{SessionsPerMin: 600, TurnsP50: 4, TurnsP95: 6}

	build := func(active bool) *state.State {
		w := &Workload{
			cfg:   Config{Agents: []AgentDecl{agent}},
			b:     core.Binding{Name: "ai-fleet-x"},
			st:    state.NewState(),
			evals: newEvalEngine(nil, nil),
		}
		eng := shape.New("UTC", nil)
		eng.Live = func(mode string) []shape.LiveFailure {
			if active && mode == modeProviderCallError {
				return []shape.LiveFailure{{Enabled: true, Intensity: 1.0, Scope: "ai-fleet-x"}}
			}
			return nil
		}
		// Three conversations so several generations exist to error.
		var batch []*ledger.Request
		for _, id := range []string{"conv-a", "conv-b", "conv-c"} {
			r := fixedReq(id, 60*time.Second)
			r.Route = agent.Name
			r.Provider = agent.Provider
			r.Model = agent.Models[0]
			batch = append(batch, r)
		}
		world := &core.World{Shape: eng} // Metrics/Sigil/Traces nil → accumulate-only
		if err := w.ProjectBatch(context.Background(), time.Now(), world, batch); err != nil {
			t.Fatalf("ProjectBatch: %v", err)
		}
		return w.st
	}

	hasErrLabel := func(st *state.State) bool {
		for _, s := range st.Collect(time.Now()) {
			if s.Name != metricOpDuration+"_count" && s.Name != metricOpDuration+"_sum" && s.Name != metricOpDuration+"_bucket" {
				continue
			}
			if _, ok := s.Labels[labelErrorType]; ok {
				return true
			}
		}
		return false
	}

	if hasErrLabel(build(false)) {
		t.Fatal("inactive engine: error_type label present, want none")
	}
	if !hasErrLabel(build(true)) {
		t.Fatal("active provider_call_error: expected an operation_duration series with error_type label")
	}
}
