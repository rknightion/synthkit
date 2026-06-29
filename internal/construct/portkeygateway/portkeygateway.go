// SPDX-License-Identifier: AGPL-3.0-only

// Package portkeygateway implements the "portkey_gateway" construct.
//
// Kind:     "portkey_gateway"
// Scope:    core.ScopeSubstrate — substrate-scoped; NO blueprint label (I21)
// Group:    core.GroupIntegration
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
//
// Identity: Config-borne (app, env, models, providers). No blueprint-name references.
//
// Signal contract: signals/portkey.md [slug: portkey-gateway]
//
// The 14 custom portkey_* metrics are emitted per (model × provider) label spread.
// cacheStatus ∈ {hit, miss, disabled, error} — see signals/portkey.md ⚠ trap.
// llm_token_sum and llm_cost_sum are cumulative gauges that GROW monotonically across ticks
// (state.Add — they accumulate; query with delta(), never rate()/increase()). request_count is a
// true Counter (state.Add) emitted across a realistic status-code distribution (heavy 2xx).
// All *_duration_*/_time_*/_ms names are histograms (state.Observe, state.LEBare).
//
// node_* runtime subset: gated by sub_signals=["runtime"]; custom metrics: sub_signals=["gateway"].
// Empty sub_signals ⇒ both families.
package portkeygateway

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// Kind is the registry key.
const Kind = "portkey_gateway"

// Config is the construct's YAML config struct.
type Config struct {
	// Models is the list of LLM model names to spread across label values (default ["gpt-4o"]).
	Models []string `yaml:"models"`
	// Providers is the list of provider names for label spread (default ["azure-openai"]).
	Providers []string `yaml:"providers"`
	// App is the service name stamped in the app= label (default "ai-gateway").
	App string `yaml:"app"`
	// Env is the environment name stamped in the env= label (default "prod").
	Env string `yaml:"env"`
	// SubSignals gates metric families. Valid: "gateway", "runtime".
	// Empty ⇒ both families. "gateway" = 14 custom portkey_* metrics.
	// "runtime" = node_* runtime subset.
	SubSignals []string `yaml:"sub_signals"`
}

// NewConfig returns a pointer to a default-zero Config for the YAML decoder.
func NewConfig() any { return &Config{} }

// defaultModels is the default model label value list.
// Expanded to a realistic multi-model set: a flagship (gpt-4o), a high-volume mini (gpt-4o-mini),
// and an Anthropic flagship via Portkey (claude-sonnet-4-6). Weights from genai.VolumeWeight:
// gpt-4o=3.0, gpt-4o-mini=6.0, claude-sonnet-4-6=2.0.
var defaultModels = []string{"gpt-4o", "gpt-4o-mini", "claude-sonnet-4-6"}

// defaultProviders is the default provider label value list.
var defaultProviders = []string{"azure-openai"}

// defaultSubSignals is the full sub-signal set.
var defaultSubSignals = []string{"gateway", "runtime"}

// httpRequestDurationBuckets matches the explicit bucket array from signals/portkey.md.
// Source: portkey.ai docs 2026-06-10, verified. Unit: seconds.
var httpRequestDurationBuckets = []float64{
	0.1, 1, 1.5, 2, 3, 5, 7, 10, 15, 20, 30, 45, 60, 90, 120, 240, 500, 1000, 3000,
}

// llmRequestDurationBuckets matches the explicit bucket array from signals/portkey.md.
// Source: portkey.ai docs 2026-06-10, verified. Unit: milliseconds.
var llmRequestDurationBuckets = []float64{
	0.1, 1, 2, 5, 10, 30, 50, 75, 100, 150, 200, 350, 500, 1000, 2500,
	5000, 10000, 50000, 100000, 300000, 500000, 10000000,
}

// grpcConversionDurationBuckets matches the explicit bucket array from signals/portkey.md.
// Source: portkey.ai docs 2026-06-10, verified. Unit: milliseconds.
var grpcConversionDurationBuckets = []float64{
	0.01, 0.1, 0.5, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000,
}

// assumedMsBuckets is a plausible ascending millisecond bucket set used for portkey_*
// histogram metrics whose bucket arrays are not documented in the signals file. (SK-38)
// buckets assumed (SK-38)
var assumedMsBuckets = []float64{
	0.1, 0.5, 1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000,
}

// nodeGCDurationBuckets covers node_gc_duration_seconds. The predecessor (SIGNALS §2.1) states the
// range endpoints `[0.001 … 6000]` (a real GC pause can run to seconds); the intermediate bounds
// are not enumerated upstream → still v: assumed (SK-38), but the range must reach 6000 (NOT
// truncate at 1.0 — that cannot represent multi-second GC pauses).
var nodeGCDurationBuckets = []float64{
	0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 60, 600, 6000,
}

// cacheStatuses is the complete valid cacheStatus label value set (signals/portkey.md ⚠).
var cacheStatuses = []string{"hit", "miss", "disabled", "error"}

// methods is a plausible HTTP method spread.
var methods = []string{"POST", "GET"}

// endpoints is a plausible endpoint spread.
var endpoints = []string{"/v1/chat/completions", "/v1/completions", "/v1/embeddings"}

// codeWeights is the realistic per-combo status-code VOLUME distribution for request_count:
// 2xx dominant, small 4xx, smaller 5xx (≈97% / 2.25% / 0.75%). It sums to 20 (the prior flat
// per-combo aggregate) so total request volume is preserved while the error mix is realistic —
// the dashboards compute error rate as (4xx+5xx)/total, which a flat 1:1:1 split made read 66.7%.
var codeWeights = []struct {
	code   string
	weight float64
}{
	{"200", 19.4},
	{"400", 0.45},
	{"500", 0.15},
}

// sources is a plausible source label spread (SDK variant).
var sources = []string{"openai-python", "node-sdk"}

// Construct is the per-instance portkey_gateway renderer. Not exported; callers use Build.
type Construct struct {
	app        string
	env        string
	models     []string
	providers  []string
	subSignals map[string]bool
	st         *state.State
	// Env-scoping (Spec 3): when the fixture carries an Env, the construct is fanned per-env —
	// env is the env name and magnitudes scale by Shape.Factor(now, weight, nonProd). Otherwise
	// (aggregate) it keeps the config env + Shape.BusinessFactor (n-1: unchanged weekend output).
	envScoped bool
	weight    float64
	nonProd   bool
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates cfg and fx, applies defaults, and returns a ready core.Construct.
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfg.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("portkeygateway: Build called with %T, want *Config", cfg)
	}

	models := c.Models
	if len(models) == 0 {
		models = defaultModels
	}
	providers := c.Providers
	if len(providers) == 0 {
		providers = defaultProviders
	}
	app := c.App
	if app == "" {
		app = "ai-gateway"
	}
	env := c.Env
	if env == "" {
		env = "prod"
	}
	subSignals := c.SubSignals
	if len(subSignals) == 0 {
		subSignals = defaultSubSignals
	}
	sigs := make(map[string]bool, len(subSignals))
	for _, s := range subSignals {
		sigs[s] = true
	}

	// Env-scoped fan-out (Spec 3): the fixture's Env overrides the config env and drives
	// per-env weight scaling. Aggregate (nil Env) keeps the config env + weight 1.0.
	weight, nonProd, envScoped := 1.0, false, false
	if fx != nil && fx.Env != nil {
		env = fx.Env.Name
		weight = fx.Env.Weight
		nonProd = fx.Env.NonProd
		envScoped = true
	}

	return &Construct{
		app:        app,
		env:        env,
		models:     models,
		providers:  providers,
		subSignals: sigs,
		st:         state.NewState(),
		envScoped:  envScoped,
		weight:     weight,
		nonProd:    nonProd,
	}, nil
}

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	batch := c.renderMetrics(now, w)
	if w.Metrics != nil {
		if err := w.Metrics.Write(ctx, batch); err != nil {
			return err
		}
	}
	return nil
}

// renderMetrics builds the full per-tick batch. Separated so tests can call without a full World.
func (c *Construct) renderMetrics(now time.Time, w *core.World) []promrw.Series {
	// Magnitude factor (B1): env-scoped uses the env-weighted Factor (per-env weekend collapse);
	// aggregate keeps BusinessFactor byte-for-byte (Factor(now,1,false) ≠ BusinessFactor on
	// weekends — 0.2 vs 0.3 — so a blanket swap would regress committed blueprints).
	var bf float64
	if c.envScoped {
		bf = w.Shape.Factor(now, c.weight, c.nonProd)
	} else {
		bf = w.Shape.BusinessFactor(now)
	}

	if c.subSignals["gateway"] {
		c.emitGateway(now, bf, w)
	}
	if c.subSignals["runtime"] {
		c.emitRuntime(bf)
	}

	return c.st.Collect(now)
}

// baseLabels returns the universal label set for one (model, provider, method, endpoint,
// code, source, stream, cacheStatus) combination. I13: absent labels are omitted.
func (c *Construct) baseLabels(
	method, endpoint, code, provider, source, stream, cacheStatus string,
	model, configName, apiKeyName, useCase, ctxVal, agent string,
) map[string]string {
	m := map[string]string{
		"app":         c.app,
		"env":         c.env,
		"method":      method,
		"endpoint":    endpoint,
		"code":        code,
		"provider":    provider,
		"source":      source,
		"stream":      stream,
		"cacheStatus": cacheStatus,
	}
	// Opt-in labels: emit ON per signals/portkey.md ("emitted ON" note).
	if model != "" {
		m["model"] = model
	}
	if configName != "" {
		m["config_name"] = configName
	}
	if apiKeyName != "" {
		m["api_key_name"] = apiKeyName
	}
	if useCase != "" {
		m["metadata_use_case"] = useCase
	}
	if ctxVal != "" {
		m["metadata_context"] = ctxVal
	}
	if agent != "" {
		m["metadata_agent"] = agent
	}
	return m
}

// emitGateway emits the 14 custom portkey_* metrics per models×providers spread.
// w is required for Wander/Noise per-model desync; it may be nil in unit tests that pass
// w=nil, in which case shape calls are skipped (factor 1.0 used).
func (c *Construct) emitGateway(now time.Time, bf float64, w *core.World) {
	for mi, model := range c.models {
		// ── Per-model volume weight (intrinsic) ────────────────────────────────
		// genai.VolumeWeight returns 1.0 for unknown model ids (neutral fallback).
		mw := genai.VolumeWeight(model)

		// ── Wander desync ─────────────────────────────────────────────────────
		// Wander gives each model a slow, deterministic sinusoidal drift (keyed on model name)
		// so series don't perfectly co-move. It is deterministic in (key, now) — no RNG — so
		// ordinal comparisons in tests remain stable. Noise (IID) is applied to latency
		// histogram observations below, not to counters/cumulatives where it would break
		// relative-ordering guarantees across constructs sharing the global RNG pool.
		wob := 1.0
		if w != nil && w.Shape != nil {
			wob = w.Shape.Wander(model, now, 0.15)
		}

		// Combined per-model magnitude multiplier applied to request volume and token/cost increments.
		modelMag := mw * wob

		// Per-tick IID jitter for latency histograms (cosmetic texture, not KPI-bearing).
		latencyJitter := 1.0
		if w != nil && w.Shape != nil {
			latencyJitter = w.Shape.Noise(0.12)
		}

		for pi, provider := range c.providers {
			// Spread deterministically across label dimension arrays using (mi+pi) index.
			idx := mi + pi
			method := methods[idx%len(methods)]
			endpoint := endpoints[idx%len(endpoints)]
			source := sources[idx%len(sources)]
			stream := "false"
			if idx%3 == 0 {
				stream = "true"
			}
			// Spread cacheStatus across all 4 valid values using idx.
			cacheStatus := cacheStatuses[idx%len(cacheStatuses)]

			// Opt-in configured labels (always emit ON per signals contract).
			configName := "default-config"
			apiKeyName := "prod-key"
			useCase := "chat"
			ctxVal := "web"
			agent := "none"

			// ── Counter ────────────────────────────────────────────────────────────
			// request_count: true counter (state.Add); ~5-50 rps diurnal. Emitted across the
			// status-code dimension with realistic weights (heavy 2xx) so the dashboards'
			// (4xx+5xx)/total error rate reflects reality rather than a flat 1:1:1 split.
			// Per-model magnitude applied via modelMag; the codeWeights per-status split is preserved.
			for _, cw := range codeWeights {
				codeLbls := c.baseLabels(
					method, endpoint, cw.code, provider, source, stream, cacheStatus,
					model, configName, apiKeyName, useCase, ctxVal, agent,
				)
				c.st.Add("request_count", codeLbls, bf*cw.weight*modelMag)
			}

			// Latency histograms + cumulative gauges observe the SUCCESS path (code=200).
			lbls := c.baseLabels(
				method, endpoint, "200", provider, source, stream, cacheStatus,
				model, configName, apiKeyName, useCase, ctxVal, agent,
			)

			// ── Histograms ────────────────────────────────────────────────────────
			// latencyJitter is IID Noise (±12%) applied to all histogram observations for
			// per-tick texture. It is NOT applied to counters or cumulative gauges.
			lj := latencyJitter

			// http_request_duration_seconds: explicit buckets from signals/portkey.md. Unit: seconds.
			c.st.Observe("http_request_duration_seconds", lbls, httpRequestDurationBuckets, state.LEBare, bf*0.5*lj)

			// llm_request_duration_milliseconds: explicit buckets from signals/portkey.md. Unit: ms.
			c.st.Observe("llm_request_duration_milliseconds", lbls, llmRequestDurationBuckets, state.LEBare, bf*800*lj)

			// portkey_request_duration_milliseconds: buckets assumed (SK-38)
			c.st.Observe("portkey_request_duration_milliseconds", lbls, assumedMsBuckets, state.LEBare, bf*600*lj)

			// portkey_processing_time_excluding_last_byte_ms: buckets assumed (SK-38)
			c.st.Observe("portkey_processing_time_excluding_last_byte_ms", lbls, assumedMsBuckets, state.LEBare, bf*200*lj)

			// llm_last_byte_diff_duration_milliseconds: buckets assumed (SK-38)
			c.st.Observe("llm_last_byte_diff_duration_milliseconds", lbls, assumedMsBuckets, state.LEBare, bf*150*lj)

			// authentication_duration_milliseconds: buckets assumed (SK-38)
			c.st.Observe("authentication_duration_milliseconds", lbls, assumedMsBuckets, state.LEBare, bf*5*lj)

			// api_key_rate_limit_check_duration_milliseconds: buckets assumed (SK-38)
			c.st.Observe("api_key_rate_limit_check_duration_milliseconds", lbls, assumedMsBuckets, state.LEBare, bf*2*lj)

			// pre_request_processing_duration_milliseconds: buckets assumed (SK-38)
			c.st.Observe("pre_request_processing_duration_milliseconds", lbls, assumedMsBuckets, state.LEBare, bf*10*lj)

			// post_request_processing_duration_milliseconds: buckets assumed (SK-38)
			c.st.Observe("post_request_processing_duration_milliseconds", lbls, assumedMsBuckets, state.LEBare, bf*8*lj)

			// llm_cache_processing_duration_milliseconds: buckets assumed (SK-38)
			c.st.Observe("llm_cache_processing_duration_milliseconds", lbls, assumedMsBuckets, state.LEBare, bf*3*lj)

			// grpc_req_conversion_duration_milliseconds: explicit buckets from signals/portkey.md. Unit: ms.
			c.st.Observe("grpc_req_conversion_duration_milliseconds", lbls, grpcConversionDurationBuckets, state.LEBare, bf*0.5*lj)

			// ── Cumulative gauges (GROW monotonically across ticks — delta(), never rate()) ──
			// state.Add accumulates a running total (counters map); state.Set would overwrite each
			// tick and the value would never change, so delta()/increase() would read ~0.
			//
			// Token increment is weighted by mw (intrinsic model volume weight) so heavier models
			// accumulate tokens faster. Wob is deliberately omitted from the cumulative increment
			// so the running total drifts smoothly rather than oscillating.
			//
			// llm_token_sum: cumulative tokens. input≈3-10× output per signals/portkey.md note.
			tokenIncrement := bf * 1200 * mw
			c.st.Add("llm_token_sum", lbls, tokenIncrement)

			// llm_cost_sum: cumulative USD. Use BlendedCostPerToken when available (0.3 input frac);
			// fall back to a fixed constant for unknown model ids.
			costPerToken := genai.BlendedCostPerToken(model, 0.3)
			var costIncrement float64
			if costPerToken > 0 {
				costIncrement = tokenIncrement * costPerToken
			} else {
				costIncrement = bf * 0.042 * mw
			}
			c.st.Add("llm_cost_sum", lbls, costIncrement)
		}
	}
}

// emitRuntime emits a plausible Node.js runtime metric subset for the gateway pods.
// Metric names are from signals/portkey.md (node_* runtime subset):
//   - node_process_cpu_user_seconds_total (counter)
//   - node_process_resident_memory_bytes (gauge)
//   - node_eventloop_lag_seconds (gauge)
//   - node_gc_duration_seconds (histogram)
func (c *Construct) emitRuntime(bf float64) {
	// Runtime metrics carry minimal identity (app, env); no model/provider spread.
	lbls := map[string]string{
		"app": c.app,
		"env": c.env,
	}

	// node_process_cpu_user_seconds_total: counter (state.Add); cumulative CPU seconds.
	c.st.Add("node_process_cpu_user_seconds_total", lbls, bf*0.3)

	// node_process_resident_memory_bytes: gauge (state.Set); resident set size in bytes.
	c.st.Set("node_process_resident_memory_bytes", lbls, bf*256*1024*1024)

	// node_eventloop_lag_seconds: gauge (state.Set); event-loop lag in seconds.
	c.st.Set("node_eventloop_lag_seconds", lbls, bf*0.005)

	// node_gc_duration_seconds: histogram (state.Observe, LEBare).
	// buckets assumed (SK-38)
	gcLbls := map[string]string{
		"app":  c.app,
		"env":  c.env,
		"kind": "minor",
	}
	c.st.Observe("node_gc_duration_seconds", gcLbls, nodeGCDurationBuckets, state.LEBare, bf*0.008)
}
