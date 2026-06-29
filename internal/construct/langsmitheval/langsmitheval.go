// SPDX-License-Identifier: AGPL-3.0-only

// Package langsmitheval implements the "langsmith_eval" construct.
//
// Kind:     "langsmith_eval"
// Scope:    core.ScopeSubstrate — never carries a blueprint label (ARCHITECTURE §5/I21)
// Group:    core.GroupIntegration
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
//
// Config: projects []string, use_cases []string, evaluators []string.
// Defaults: projects=["assistant-prod"], use_cases=["assistant","summarization"],
//
//	evaluators=["faithfulness","completeness","relevance"].
//
// Signal contract: signals/langsmith.md [slug: langsmith-eval]
//
// All metrics are GAUGES (state.Set) EXCEPT langsmith_eval_token_spend_total
// which is a COUNTER (state.Add).
//
// Label sets per signals/langsmith.md:
//   - completeness_ratio, faithfulness_ratio: {project, use_case, agent, env}
//   - env_consistency_ratio, schema_validity_ratio, passthrough_exactness_ratio: {project, env}
//   - recall_at_k, precision_at_k, mrr, ndcg: {project, k, use_case}
//   - latency_seconds, token_spend_total, retry_rate, fallback_rate: {project, use_case}
//   - hitl_rate: {project}
//   - score: {project, evaluator, run_outcome}
package langsmitheval

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// Kind is the registry key.
const Kind = "langsmith_eval"

// defaultProjects, defaultUseCases, defaultEvaluators are the generic fallback sets.
var (
	defaultProjects   = []string{"assistant-prod"}
	defaultUseCases   = []string{"assistant", "summarization"}
	defaultEvaluators = []string{"faithfulness", "completeness", "relevance"}
)

// defaultAgents and defaultEnvs are fixed generic values used for label dimensions
// that are not directly configured (they are observation-context, not user config).
var (
	defaultAgents = []string{"agent-v1", "agent-v2"}
	defaultEnvs   = []string{"prod", "staging"}
)

// kValues are the @k values used for recall/precision/mrr/ndcg metrics.
var kValues = []string{"1", "3", "5", "10"}

// runOutcomes are the allowed values for the run_outcome label on langsmith_eval_score.
var runOutcomes = []string{"success", "error", "pending"}

// Config is the construct's YAML config struct.
type Config struct {
	// Projects is the list of LangSmith project names to emit (default ["assistant-prod"]).
	Projects []string `yaml:"projects"`
	// UseCases is the list of use-case dimension values (default ["assistant","summarization"]).
	UseCases []string `yaml:"use_cases"`
	// Evaluators is the list of evaluator keys for langsmith_eval_score
	// (default ["faithfulness","completeness","relevance"]).
	Evaluators []string `yaml:"evaluators"`
	// UseCaseWeights is an optional per-use-case relative traffic weight. Keys must match values
	// in UseCases. A missing or zero entry defaults to 1.0 (neutral). Used to scale the
	// langsmith_eval_token_spend_total increment: high-volume use cases produce proportionally
	// more token spend than low-volume ones.
	UseCaseWeights map[string]float64 `yaml:"use_case_weights"`
}

// Construct is the per-instance langsmith_eval renderer. Not exported; callers use Build.
type Construct struct {
	projects       []string
	useCases       []string
	evaluators     []string
	useCaseWeights map[string]float64
	st             *state.State
	// Env-scoping (Spec 3): when the fixture carries an Env, the construct is fanned per-env —
	// envName is the single env to emit and magnitudes scale by Shape.Factor(now, weight, nonProd).
	// Otherwise (aggregate) it keeps the defaultEnvs loop + Shape.BusinessFactor (n-1: unchanged).
	envScoped bool
	envName   string
	weight    float64
	nonProd   bool
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates cfg and fx, applies defaults, and returns a ready core.Construct.
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfg.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("langsmitheval: Build called with %T, want *Config", cfg)
	}

	projects := c.Projects
	if len(projects) == 0 {
		projects = defaultProjects
	}
	useCases := c.UseCases
	if len(useCases) == 0 {
		useCases = defaultUseCases
	}
	evaluators := c.Evaluators
	if len(evaluators) == 0 {
		evaluators = defaultEvaluators
	}

	// Env-scoped fan-out (Spec 3): the fixture's Env drives per-env weight scaling and
	// collapses the defaultEnvs loop to a single env. Aggregate (nil Env) keeps the
	// defaultEnvs 2-value loop + weight 1.0 (n-1: unchanged behavior).
	weight, nonProd, envScoped := 1.0, false, false
	var envName string
	if fx != nil && fx.Env != nil {
		envName = fx.Env.Name
		weight = fx.Env.Weight
		nonProd = fx.Env.NonProd
		envScoped = true
	}

	return &Construct{
		projects:       projects,
		useCases:       useCases,
		evaluators:     evaluators,
		useCaseWeights: c.UseCaseWeights,
		st:             state.NewState(),
		envScoped:      envScoped,
		envName:        envName,
		weight:         weight,
		nonProd:        nonProd,
	}, nil
}

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	c.render(now, w)
	if w.Metrics == nil {
		return nil
	}
	return w.Metrics.Write(ctx, c.st.Collect(now))
}

// render accumulates all langsmith_eval_* series into c.st for the current tick.
func (c *Construct) render(now time.Time, w *core.World) {
	// Magnitude factor (B1): env-scoped uses the env-weighted Factor (per-env weekend collapse);
	// aggregate keeps BusinessFactor byte-for-byte (Factor(now,1,false) ≠ BusinessFactor on
	// weekends — 0.2 vs 0.3 — so a blanket swap would regress committed blueprints).
	bf := 1.0
	if w.Shape != nil {
		if c.envScoped {
			bf = w.Shape.Factor(now, c.weight, c.nonProd) * w.Shape.Noise(0.05)
		} else {
			bf = w.Shape.BusinessFactor(now) * w.Shape.Noise(0.05)
		}
	}
	// Clamp bf to a safe positive range so ratios stay in [0,1].
	if bf < 0.01 {
		bf = 0.01
	}
	if bf > 1.0 {
		bf = 1.0
	}

	// Failure-mode factors for eval_quality_degraded.
	// scope: env-scoped → the single env name; aggregate → "" (unscoped, matches all).
	// downF (0.4 at intensity=1.0): multiplies all "higher-is-better" quality scores.
	// upF   (4.0 at intensity=1.0): multiplies all "lower-is-better" bad-event rates.
	downF, upF := 1.0, 1.0
	if w.Shape != nil {
		scope := ""
		if c.envScoped {
			scope = c.envName
		}
		downF = w.Shape.FailFactor(now, "eval_quality_degraded", scope, 0.4)
		upF = w.Shape.FailFactor(now, "eval_quality_degraded", scope, 4.0)
	}

	// Env emission: env-scoped iterates the single fx.Env.Name; aggregate iterates defaultEnvs.
	envs := defaultEnvs
	if c.envScoped {
		envs = []string{c.envName}
	}

	for _, proj := range c.projects {
		c.emitProjectMetrics(w, now, proj, bf, downF, upF, envs)
	}
}

// seriesVar returns a stable-but-living per-series multiplier ≈ 1: a deterministic baseline
// OFFSET (Spread — so peer series that share a formula get distinct, stable values instead of
// emitting byte-identical numbers) times a slow per-series DRIFT (Wander — so the value is not
// frozen). amp sets the magnitude; quality ratios use a small amp (stay near target), volume
// and rate metrics use a larger one. Returns 1.0 when no shape engine is wired.
func (c *Construct) seriesVar(w *core.World, now time.Time, key string, amp float64) float64 {
	if w == nil || w.Shape == nil {
		return 1.0
	}
	return w.Shape.Spread(key, amp) * w.Shape.Wander(key, now, amp*0.4)
}

// seriesVarDown is the downward-only variant for "fraction-correct" quality ratios whose ceiling
// is 1.0: it returns a stable-but-drifting multiplier in ≈[1-amp, 1.0] so each series sits just
// below its target by a distinct, small deficit (and drifts) instead of all pinning at the clamp.
// This avoids the collision symmetric variation causes when base*Spread exceeds 1.0 for several
// peers at once. Returns 1.0 when no shape engine is wired.
func (c *Construct) seriesVarDown(w *core.World, now time.Time, key string, amp float64) float64 {
	if w == nil || w.Shape == nil {
		return 1.0
	}
	frac := w.Shape.Spread(key, 0.5) - 0.5 // ∈ [0,1]: per-series deficit fraction
	drift := w.Shape.Wander(key, now, 0.3) // ∈ [0.7,1.3]: slow drift so the deficit isn't frozen
	d := amp * frac * drift
	if d < 0 {
		d = 0
	}
	return 1 - d
}

// maybeEnv stamps the env label on a {project,…} family that has no native env dimension, but
// ONLY when env-scoped (fanned). Without this, per-env fan-out would push byte-identical env-less
// series from every instance (Mimir duplicate-sample rejection). Aggregate (single instance) keeps
// the env-less families env-less (emitted once; I13 — absent dimension omitted).
func (c *Construct) maybeEnv(lbls map[string]string) map[string]string {
	if c.envScoped {
		lbls["env"] = c.envName
	}
	return lbls
}

// qualityAmp / rateAmp / volAmp set the per-series Spread+Wander magnitude per metric class.
// Quality ratios stay near their gate target (small amp); rates and volume vary more freely.
const (
	qualityAmp = 0.045 // ±~4.5% baseline spread → e.g. faithfulness 0.87 ranges ~0.83–0.91 across projects
	rateAmp    = 0.30  // bad-event rates differ markedly by project
	volAmp     = 0.18  // latency / token spend per-series spread
)

// emitProjectMetrics emits all langsmith_eval_* families for one project.
// bf   is the diurnal business/traffic factor (volume scaling).
// downF is the failure-mode down-multiplier for "higher-is-better" quality scores
//
//	(1.0 = healthy; 0.4 at full intensity of eval_quality_degraded).
//
// upF  is the failure-mode up-multiplier for "lower-is-better" bad-event rates
//
//	(1.0 = healthy; 4.0 at full intensity of eval_quality_degraded).
//
// envs is either []string{singleEnv} (env-scoped) or defaultEnvs (aggregate).
//
// Each metric is scaled by a per-series seriesVar (Spread×Wander) keyed on the metric name plus
// the series' distinguishing labels, so peer series (different project/use_case/…) emit distinct,
// stable-but-drifting values instead of one shared constant. seriesVar replaces the old single
// per-tick quality jitter (qj), which applied the SAME multiplier to every series → identical rows.
func (c *Construct) emitProjectMetrics(w *core.World, now time.Time, proj string, bf, downF, upF float64, envs []string) {
	// vf is a symmetric per-series multiplier (≈1±amp) for volume/rate metrics; vfDown is the
	// downward-only variant (≈[1-amp,1]) for "fraction-correct" quality ratios capped at 1.0.
	// key uniquely identifies the series.
	vf := func(key string, amp float64) float64 { return c.seriesVar(w, now, key, amp) }
	vfDown := func(key string, amp float64) float64 { return c.seriesVarDown(w, now, key, amp) }

	// ── {project, use_case, agent, env} — completeness_ratio, faithfulness_ratio ──
	// QUALITY RATIOS: per-series spread/drift (not bf) — must stay near target regardless of
	// time of day. downF degrades these on eval_quality_degraded.
	for _, uc := range c.useCases {
		for _, agent := range defaultAgents {
			for _, env := range envs {
				lbls := map[string]string{
					"project":  proj,
					"use_case": uc,
					"agent":    agent,
					"env":      env,
				}
				k := proj + "|" + uc + "|" + agent + "|" + env
				// completeness_ratio gate ≥0.995
				c.st.Set("langsmith_eval_completeness_ratio", lbls, clamp(0.995*vfDown("completeness|"+k, qualityAmp)*downF, 0, 1))
				// faithfulness_ratio gate ≥0.85
				c.st.Set("langsmith_eval_faithfulness_ratio", lbls, clamp(0.87*vfDown("faithfulness|"+k, qualityAmp)*downF, 0, 1))
			}
		}
	}

	// ── {project, env} — env_consistency_ratio, schema_validity_ratio, passthrough_exactness_ratio ──
	// QUALITY RATIOS: per-series spread/drift. downF degrades on eval_quality_degraded.
	for _, env := range envs {
		lbls := map[string]string{
			"project": proj,
			"env":     env,
		}
		k := proj + "|" + env
		// env_consistency_ratio target ≈1.0 (clamped to 1.0; spread pulls it slightly below)
		c.st.Set("langsmith_eval_env_consistency_ratio", lbls, clamp(1.0*vfDown("envcons|"+k, qualityAmp)*downF, 0, 1))
		// schema_validity_ratio <0.995 blocks
		c.st.Set("langsmith_eval_schema_validity_ratio", lbls, clamp(0.998*vfDown("schema|"+k, qualityAmp)*downF, 0, 1))
		// passthrough_exactness_ratio <0.999 blocks
		c.st.Set("langsmith_eval_passthrough_exactness_ratio", lbls, clamp(0.9995*vfDown("passthrough|"+k, qualityAmp)*downF, 0, 1))
	}

	// ── {project, k, use_case} — recall_at_k, precision_at_k, mrr, ndcg ──
	// QUALITY RATIOS: per-series spread/drift. downF degrades on eval_quality_degraded.
	for _, kv := range kValues {
		for _, uc := range c.useCases {
			lbls := c.maybeEnv(map[string]string{
				"project":  proj,
				"k":        kv,
				"use_case": uc,
			})
			k := proj + "|" + kv + "|" + uc
			c.st.Set("langsmith_eval_recall_at_k", lbls, clamp(0.80*vfDown("recall|"+k, qualityAmp)*downF, 0, 1))
			c.st.Set("langsmith_eval_precision_at_k", lbls, clamp(0.75*vfDown("precision|"+k, qualityAmp)*downF, 0, 1))
			c.st.Set("langsmith_eval_mrr", lbls, clamp(0.70*vfDown("mrr|"+k, qualityAmp)*downF, 0, 1))
			c.st.Set("langsmith_eval_ndcg", lbls, clamp(0.78*vfDown("ndcg|"+k, qualityAmp)*downF, 0, 1))
		}
	}

	// ── {project, use_case} — latency_seconds, token_spend_total (COUNTER), retry_rate, fallback_rate ──
	for _, uc := range c.useCases {
		lbls := c.maybeEnv(map[string]string{
			"project":  proj,
			"use_case": uc,
		})
		k := proj + "|" + uc
		// VOLUME/LATENCY: keep bf coupling for diurnal shape, plus per-series spread so peers
		// don't sit on one curve. 0.84s idle (bf=0) → 1.44s peak (bf=1), never craters.
		c.st.Set("langsmith_eval_latency_seconds", lbls, 1.2*(0.7+0.5*bf)*vf("latency|"+k, volAmp))
		// COUNTER — state.Add (only counter in the family); volume scales with traffic, per-use-case
		// relative weight (use_case_weights), and a per-series spread for inter-project volume spread.
		c.st.Add("langsmith_eval_token_spend_total", lbls, 1500*bf*c.useCaseWeight(uc)*vf("spend|"+k, volAmp))
		// RATES: small base, per-series spread (not bf); must NOT crater to ~0 off-peak.
		// upF amplifies bad-event rates on eval_quality_degraded.
		c.st.Set("langsmith_eval_retry_rate", lbls, clamp(0.03*vf("retry|"+k, rateAmp)*upF, 0, 1))
		c.st.Set("langsmith_eval_fallback_rate", lbls, clamp(0.02*vf("fallback|"+k, rateAmp)*upF, 0, 1))
	}

	// ── {project} — hitl_rate ──
	// RATE: per-series spread/drift. upF amplifies on eval_quality_degraded.
	c.st.Set("langsmith_eval_hitl_rate", c.maybeEnv(map[string]string{"project": proj}), clamp(0.05*vf("hitl|"+proj, rateAmp)*upF, 0, 1))

	// ── {project, evaluator, run_outcome} — score ──
	// QUALITY: per-series spread/drift. pending stays 0.
	// downF degrades success scores; upF amplifies error scores on eval_quality_degraded.
	for _, evaluator := range c.evaluators {
		for _, outcome := range runOutcomes {
			lbls := c.maybeEnv(map[string]string{
				"project":     proj,
				"evaluator":   evaluator,
				"run_outcome": outcome,
			})
			k := proj + "|" + evaluator
			var v float64
			switch outcome {
			case "success":
				v = clamp(0.88*vfDown("score-ok|"+k, qualityAmp)*downF, 0, 1)
			case "error":
				v = clamp(0.4*vf("score-err|"+k, rateAmp)*upF, 0, 1)
			default: // "pending"
				v = 0.0
			}
			c.st.Set("langsmith_eval_score", lbls, v)
		}
	}
}

// useCaseWeight returns the relative traffic weight for a use case. A missing or non-positive
// entry in c.useCaseWeights defaults to 1.0 (neutral — no differentiation).
func (c *Construct) useCaseWeight(uc string) float64 {
	if w, ok := c.useCaseWeights[uc]; ok && w > 0 {
		return w
	}
	return 1.0
}

// clamp clamps v to [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
