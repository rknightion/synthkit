// SPDX-License-Identifier: AGPL-3.0-only

// Package portkeypoller implements the "portkey_poller" construct.
//
// Kind:     "portkey_poller"
// Scope:    core.ScopeSubstrate — substrate-scoped; NO blueprint label (ARCHITECTURE §5/I21)
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
// Config:   workspace (default "ws-demo"), use_cases (default ["assistant","summarization"]),
//
//	models (default ["gpt-4o"])
//
// Signal contract: signals/portkey.md [slug: portkey-poller]
//
// ⚠⚠ CRITICAL: ALL portkey_api_* series are GAUGES — INCLUDING the _total-suffixed ones.
// The Portkey Analytics API returns windowed aggregates, NOT live counters. Use state.Set
// for ALL SEVEN portkey_api_* metrics. NEVER state.Add (that would incorrectly accumulate).
//
// Label schema (signals/portkey.md §portkey-poller):
//   - workspace:          on all portkey_api_* series (poller_* self-telemetry uses `api` only)
//   - ai_model:           on requests_total, cost_usd, tokens_total, latency_seconds
//   - metadata_use_case:  on requests_total, cost_usd, tokens_total, latency_seconds,
//     error_rate, cache_hit_rate, rescued_requests_total
//   - status_class:       on requests_total only (∈ {2xx,4xx,5xx})
//   - quantile:           on latency_seconds only (∈ {"0.5","0.9","0.99"})
//   - api:                on poller_* self-telemetry
//
// I13: absent label ⇒ OMITTED (never "" or "NA").
package portkeypoller

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/state"
)

// Kind is the registry key.
const Kind = "portkey_poller"

// defaultUseCases is the default set of use-case metadata values.
var defaultUseCases = []string{"assistant", "summarization"}

// defaultModels is the default set of AI model strings (generic — no customer values).
var defaultModels = []string{"gpt-4o", "gpt-4o-mini", "gpt-4.1-mini"}

// statusClasses are the request status classes for portkey_api_requests_total.
var statusClasses = []string{"2xx", "4xx", "5xx"}

// quantiles are the pre-computed percentile keys for portkey_api_latency_seconds.
var quantiles = []string{"0.5", "0.9", "0.99"}

// Config is the construct's YAML config struct.
type Config struct {
	// Workspace is the Portkey workspace identifier (default "ws-demo").
	Workspace string `yaml:"workspace"`
	// UseCases is the set of metadata_use_case values to spread over (default ["assistant","summarization"]).
	UseCases []string `yaml:"use_cases"`
	// Models is the set of ai_model values to spread over (default ["gpt-4o","gpt-4o-mini","gpt-4.1-mini"]).
	Models []string `yaml:"models"`
	// UseCaseWeights optionally sets a per-use-case volume multiplier (default 1.0 for any absent key).
	// Higher weight ⇒ proportionally more requests/tokens/cost for that use case.
	UseCaseWeights map[string]float64 `yaml:"use_case_weights"`
}

// Construct is the per-instance portkey_poller renderer. Not exported; callers use Build.
type Construct struct {
	cfg       *Config
	workspace string
	useCases  []string
	models    []string
	st        *state.State
	// Env-scoping (Spec 3): when the fixture carries an Env, the construct is fanned per-env —
	// envName is stamped as the `env` label and magnitudes are unchanged (poller emits flat
	// values, so weight is recorded but the scaling path is available for future use).
	// Aggregate (nil Env) keeps current behavior byte-for-byte (n-1).
	envScoped bool
	envName   string
	weight    float64
	nonProd   bool
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates cfg and returns a ready core.Construct instance.
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfg.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("portkeypoller: Build called with %T, want *Config", cfg)
	}

	workspace := c.Workspace
	if workspace == "" {
		workspace = "ws-demo"
	}

	useCases := c.UseCases
	if len(useCases) == 0 {
		useCases = defaultUseCases
	}

	models := c.Models
	if len(models) == 0 {
		models = defaultModels
	}

	// Env-scoped fan-out (Spec 3): when the fixture carries an Env, stamp the env label and
	// record weight/nonProd for magnitude scaling. Aggregate (nil Env) keeps current behavior
	// byte-for-byte (n-1): no env label, flat magnitudes.
	weight, nonProd, envScoped, envName := 1.0, false, false, ""
	if fx != nil && fx.Env != nil {
		weight = fx.Env.Weight
		nonProd = fx.Env.NonProd
		envScoped = true
		envName = fx.Env.Name
	}

	return &Construct{
		cfg:       c,
		workspace: workspace,
		useCases:  useCases,
		models:    models,
		st:        state.NewState(),
		envScoped: envScoped,
		envName:   envName,
		weight:    weight,
		nonProd:   nonProd,
	}, nil
}

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// useCaseWeight returns the volume multiplier for a use-case, falling back to 1.0 for any
// absent or non-positive key (I13: absent entry = neutral, not zero).
func (c *Construct) useCaseWeight(uc string) float64 {
	if c.cfg != nil {
		if w, ok := c.cfg.UseCaseWeights[uc]; ok && w > 0 {
			return w
		}
	}
	return 1.0
}

// Tick renders one Analytics poll window into w.Metrics.
//
// Emits:
//   - portkey_api_requests_total    GAUGE (windowed aggregate; state.Set — NOT Add)
//   - portkey_api_cost_usd          GAUGE (state.Set)
//   - portkey_api_tokens_total      GAUGE (state.Set — NOT Add)
//   - portkey_api_latency_seconds   GAUGE per quantile (state.Set)
//   - portkey_api_error_rate        GAUGE workspace/use-case rollup; no ai_model (state.Set)
//   - portkey_api_cache_hit_rate    GAUGE workspace/use-case rollup; no ai_model (state.Set)
//   - portkey_api_rescued_requests_total GAUGE workspace/use-case rollup (state.Set — NOT Add)
//   - poller_last_success_timestamp_seconds GAUGE = now.Unix()
//   - poller_window_lag_seconds     GAUGE jittered analytic lag
//   - poller_api_errors_total       COUNTER ~0 baseline (state.Add)
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	c.emit(now, w)
	if w.Metrics != nil {
		return w.Metrics.Write(ctx, c.st.Collect(now))
	}
	return nil
}

// apiLabels returns the base label map for portkey_api_* series, optionally including the
// env label when this construct is env-scoped (I13: absent ⇒ omitted, never "").
func (c *Construct) apiLabels(extra map[string]string) map[string]string {
	m := make(map[string]string, len(extra)+1)
	for k, v := range extra {
		m[k] = v
	}
	if c.envScoped {
		m["env"] = c.envName
	}
	return m
}

// emit builds the full per-tick batch of portkey_api_* and poller_* series.
func (c *Construct) emit(now time.Time, w *core.World) {
	ws := c.workspace

	// Business factor (diurnal × weekly × weight): mirrors the portkeygateway idiom —
	// env-scoped uses the env-weighted Factor (per-env weekend collapse); aggregate keeps
	// BusinessFactor (Factor(now,1,false) ≠ BusinessFactor on weekends: 0.2 vs 0.3).
	var bf float64
	if c.envScoped {
		bf = w.Shape.Factor(now, c.weight, c.nonProd)
	} else {
		bf = w.Shape.BusinessFactor(now)
	}

	// Failure-mode amplifiers — AxisCloud, scoped to env when env-scoped.
	// scope="" for aggregate (un-scoped) so a mode targeting a specific env does not bleed
	// into the aggregate poller's series.
	var fmScope string
	if c.envScoped {
		fmScope = c.envName
	}
	errF := w.Shape.FailFactor(now, "portkey_scrape_degraded", fmScope, 15.0)  // 4xx/5xx share
	rateF := w.Shape.FailFactor(now, "portkey_scrape_degraded", fmScope, 60.0) // error_rate
	latF := w.Shape.FailFactor(now, "portkey_scrape_degraded", fmScope, 3.0)   // latency
	lagF := w.Shape.FailFactor(now, "portkey_scrape_degraded", fmScope, 3.0)   // poller window lag

	// ── portkey_api_requests_total ─────────────────────────────────────────────
	// GAUGE (windowed Analytics aggregate). Labels: workspace, ai_model,
	// metadata_use_case, status_class. Loop Models×UseCases×statusClasses.
	for _, model := range c.models {
		mw := genai.VolumeWeight(model)
		for _, uc := range c.useCases {
			ucw := c.useCaseWeight(uc)
			// Slow wander per (model, use_case) key: smooth low-frequency drift (±15%)
			// so each combination traces a distinct curve across time.
			wob := w.Shape.Wander(model+"|"+uc, now, 0.15)

			for si, sc := range statusClasses {
				// Most traffic is 2xx; 4xx and 5xx are proportionally small.
				// Under portkey_scrape_degraded, error classes (4xx/5xx) are amplified by
				// errF so the error share climbs; 2xx base is left unchanged.
				var base float64
				switch si {
				case 0: // 2xx
					base = 1200
				case 1: // 4xx
					base = 40 * errF
				case 2: // 5xx
					base = 8 * errF
				}
				c.st.Set("portkey_api_requests_total", c.apiLabels(map[string]string{
					"workspace":         ws,
					"ai_model":          model,
					"metadata_use_case": uc,
					"status_class":      sc,
				}), bf*base*mw*ucw*wob*w.Shape.Noise(0.04))
			}

			// ── portkey_api_tokens_total ───────────────────────────────────────────
			// GAUGE despite _total (windowed aggregate). Labels: workspace, ai_model,
			// metadata_use_case. Emitted ONCE per (model, uc) OUTSIDE the status loop.
			tokens := bf * 48000 * mw * ucw * wob * w.Shape.Noise(0.04)
			c.st.Set("portkey_api_tokens_total", c.apiLabels(map[string]string{
				"workspace":         ws,
				"ai_model":          model,
				"metadata_use_case": uc,
			}), tokens)

			// ── portkey_api_cost_usd ───────────────────────────────────────────────
			// GAUGE. Labels: workspace, ai_model, metadata_use_case.
			// Driven by real model pricing via genai.BlendedCostPerToken so different
			// models produce genuinely different spend levels. Emitted OUTSIDE status loop.
			cost := tokens * genai.BlendedCostPerToken(model, 0.3)
			if cost == 0 {
				// Uncatalogued model: fall back to the flat baseline scaled by mw/ucw.
				cost = bf * 0.024 * mw * ucw
			}
			c.st.Set("portkey_api_cost_usd", c.apiLabels(map[string]string{
				"workspace":         ws,
				"ai_model":          model,
				"metadata_use_case": uc,
			}), cost)

			// ── portkey_api_latency_seconds ────────────────────────────────────────
			// GAUGE per quantile (pre-computed percentiles — NOT histogram_quantile).
			// Labels: workspace, ai_model, metadata_use_case, quantile.
			// Percentiles are NOT scaled by bf (they are latency percentiles, not volume);
			// light Noise adds tick-to-tick jitter: Noise(0.10) for p50/p90, Noise(0.15) for p99.
			latencyBase := map[string]float64{
				"0.5":  1.2,
				"0.9":  2.8,
				"0.99": 5.4,
			}
			latencyNoise := map[string]float64{
				"0.5":  0.10,
				"0.9":  0.10,
				"0.99": 0.15,
			}
			for _, q := range quantiles {
				c.st.Set("portkey_api_latency_seconds", c.apiLabels(map[string]string{
					"workspace":         ws,
					"ai_model":          model,
					"metadata_use_case": uc,
					"quantile":          q,
				}), latencyBase[q]*latF*w.Shape.Noise(latencyNoise[q]))
			}
		}
	}

	// ── workspace/use-case rollup series (no ai_model label) ──────────────────

	for _, uc := range c.useCases {
		// portkey_api_error_rate — GAUGE; 0–1 ratio. No ai_model.
		// Light Noise(0.2) for jitter; clamp to [0,1].
		// Under portkey_scrape_degraded, rateF amplifies the baseline rate.
		errorRate := math.Min(1, math.Max(0, 0.004*rateF*w.Shape.Noise(0.2)))
		c.st.Set("portkey_api_error_rate", c.apiLabels(map[string]string{
			"workspace":         ws,
			"metadata_use_case": uc,
		}), errorRate)

		// portkey_api_cache_hit_rate — GAUGE; 0–1 ratio. No ai_model.
		// Light Noise(0.1) for jitter; clamp to [0,1].
		cacheHitRate := math.Min(1, math.Max(0, 0.31*w.Shape.Noise(0.1)))
		c.st.Set("portkey_api_cache_hit_rate", c.apiLabels(map[string]string{
			"workspace":         ws,
			"metadata_use_case": uc,
		}), cacheHitRate)

		// portkey_api_rescued_requests_total — GAUGE despite _total (windowed retry/fallback
		// visibility). No ai_model.
		c.st.Set("portkey_api_rescued_requests_total", c.apiLabels(map[string]string{
			"workspace":         ws,
			"metadata_use_case": uc,
		}), bf*3*w.Shape.Noise(0.05))
	}

	// ── poller_* self-telemetry ────────────────────────────────────────────────

	// Self-telemetry describes the poller's OWN health, keyed by `api` only (signals/portkey.md
	// [slug: portkey-poller] poller family — NO workspace dim on self-telemetry).
	// Self-telemetry carries env when env-scoped: each fanned env runs its OWN poller process, so
	// env disambiguates the per-env poller_* series (without it, fanned instances + any aggregate
	// poller push byte-identical poller_* → Mimir duplicate-sample rejection). Aggregate omits env (I13).
	apiLabel := c.apiLabels(map[string]string{
		"api": "portkey-analytics",
	})

	// poller_last_success_timestamp_seconds — GAUGE = unix epoch at poll completion.
	c.st.Set("poller_last_success_timestamp_seconds", apiLabel, float64(now.Unix()))

	// poller_window_lag_seconds — GAUGE; small jittered analytic lag (5–15 min typical).
	// Use a deterministic jitter based on unix epoch so it varies tick-to-tick.
	// Under portkey_scrape_degraded, lagF amplifies the window lag.
	lag := (360.0 + math.Mod(float64(now.Unix()), 300.0)) * lagF // 360–660 s (6–11 min) baseline
	c.st.Set("poller_window_lag_seconds", apiLabel, lag)

	// poller_api_errors_total — true COUNTER; ~0 baseline (no errors in steady state).
	// Under portkey_scrape_degraded, add errors proportional to the failure intensity.
	var pollerErrDelta float64
	if w.Shape.Active(now, "portkey_scrape_degraded", fmScope) {
		_, intensity := w.Shape.Eval(now, "portkey_scrape_degraded", fmScope)
		pollerErrDelta = math.Round(50 * intensity)
	}
	c.st.Add("poller_api_errors_total", apiLabel, pollerErrDelta)
}
