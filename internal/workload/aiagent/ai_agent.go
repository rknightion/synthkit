// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"context"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	kind     = "ai_agent"
	interval = 60 * time.Second // metric lane cadence (DPM floor, I10)
)

// Workload is one ai_agent fleet instance: a roster of agents (coding + general) bound to an
// env/cluster, emitting the three sigil lanes per arriving conversation.
type Workload struct {
	cfg Config
	b   core.Binding

	env     string
	cluster string

	m     *minter
	st    *state.State // cumulative metric state (Lane C) — flushed in Tick (R-M4)
	evals *evalEngine
}

// Registration returns the core.WorkloadReg for the "ai_agent" kind. Call this from the composition
// root's catalog wiring file; no init() self-registration.
func Registration() core.WorkloadReg {
	return core.WorkloadReg{
		Kind:      kind,
		Doc:       "ai_agent — agent CONVERSATIONS (coding + general archetypes); emits native sigil generation/workflow-step/score ingest + gen_ai OTLP spans + gen_ai_client_*/sigil_eval_* metrics",
		NewConfig: func() any { return &Config{} },
		Build:     build,
		// Substrate-scoped: sigil data carries no blueprint selector label (real sigil data has
		// none; the ingest proto has no field for it). Disambiguation is by service.name/agent_name/
		// conversation_id — like other substrate constructs. The runner stamps no blueprint label.
		Scope:        core.ScopeSubstrate,
		FailureModes: FailureModes,
	}
}

// build constructs a Workload: freeze identity from the binding, build the minter over the full
// agent roster, and the eval apparatus. Mirrors app.build's binding usage.
func build(cfgAny any, b core.Binding) (core.Workload, error) {
	cfg, _ := cfgAny.(*Config)
	if cfg == nil {
		cfg = &Config{}
	}
	w := &Workload{cfg: *cfg, b: b, st: state.NewState()}
	w.resolveIdentity()
	w.m = newMinter(w.b.Name, w.env, w.cluster, cfg.Agents)
	w.evals = newEvalEngine(cfg.Evaluators, cfg.Rules)
	return w, nil
}

// resolveIdentity pins env/cluster from the binding once.
func (w *Workload) resolveIdentity() {
	if w.b.Env != nil {
		w.env = w.b.Env.Name
	}
	if w.b.Cluster != nil {
		w.cluster = w.b.Cluster.Name
	}
}

// Kind implements core.Workload.
func (w *Workload) Kind() string { return kind }

// Name implements core.Workload.
func (w *Workload) Name() string { return w.b.Name }

// Signals declares the classes this instance emits: native sigil ingest + OTLP traces + metrics.
func (w *Workload) Signals() []core.SignalClass {
	return []core.SignalClass{core.Sigil, core.Traces, core.Metrics}
}

// Interval implements core.Workload (metric lane cadence; ≥60s for the metric lane).
func (w *Workload) Interval() time.Duration { return interval }

// Minter implements core.Workload.
func (w *Workload) Minter() ledger.Minter { return w.m }

// agentByName recovers an AgentDecl from a request Route (= agent name; unique within the fleet).
func (w *Workload) agentByName(name string) (AgentDecl, bool) {
	for _, a := range w.cfg.Agents {
		if a.Name == name {
			return a, true
		}
	}
	return AgentDecl{}, false
}

// Tick is the metric lane (R-M4): it FLUSHES the cumulative Lane-C totals accumulated by
// ProjectBatch. It never recomputes observations here. No-op when no metrics writer is wired.
func (w *Workload) Tick(ctx context.Context, now time.Time, world *core.World) error {
	if world.Metrics == nil {
		return nil
	}
	return world.Metrics.Write(ctx, w.st.Collect(now))
}

// ProjectBatch builds the three lanes for each minted conversation Request. It maps req.Route to the
// AgentDecl, builds the conversation (Lane A generations/workflow-steps + Lane B span tree) plus the
// eval scores, writes Lane A via world.Sigil and Lane B via world.Traces (both guarded for nil), and
// ACCUMULATES Lane-C metric observations into w.st (flushed by Tick — never emitted here).
func (w *Workload) ProjectBatch(ctx context.Context, now time.Time, world *core.World, batch []*ledger.Request) error {
	if len(batch) == 0 {
		return nil
	}
	var exports []sigil.Export
	var resources []otlp.Resource

	// Resolve the active failure intensities once for this batch instant + scope (UNION of scheduled
	// incidents + live control-plane state); 0 ⇒ inactive. Drives provider_call_error (per-generation
	// call errors → ERROR spans + error_type/error_category metric labels) and eval_quality_regression
	// (biases eval scores downward).
	fc := w.resolveFailures(now, world.Shape)

	for _, r := range batch {
		agent, ok := w.agentByName(r.Route)
		if !ok {
			continue // a request for an agent not in this fleet (defensive; minter only mints ours)
		}
		gens, steps, spans, obs := buildConversation(w.cfg.Resource, agent, r)
		if len(gens) == 0 {
			continue
		}
		// Failure mode: mark a deterministic fraction of generations as provider call errors (mutates
		// gens/spans/obs in place) before the lanes are accumulated/exported.
		applyProviderErrors(fc, gens, spans, obs)
		// Lane C: accumulate per-turn observations into the workload state (flushed by Tick).
		for _, o := range obs {
			accumulate(w.st, o)
		}
		// Evals: score sampled generations → Lane A scores + sigil_eval_* observations. Under an
		// eval_quality_regression the scores are biased toward failing (passed=false rate rises).
		scores := w.evals.scoreConversation(agent, gens, w.st, fc.evalRegress)

		exports = append(exports, sigil.Export{
			Generations:   gens,
			WorkflowSteps: steps,
			Scores:        scores,
			ConvKey:       r.SessionID,
		})
		resources = append(resources, spans...)
	}

	if world.Sigil != nil && len(exports) > 0 {
		if err := world.Sigil.Write(ctx, exports); err != nil {
			return err
		}
	}
	if world.Traces != nil && len(resources) > 0 {
		if err := world.Traces.Write(ctx, resources); err != nil {
			return err
		}
	}
	return nil
}
