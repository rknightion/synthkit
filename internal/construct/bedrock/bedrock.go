// SPDX-License-Identifier: AGPL-3.0-only

// Package bedrock emits AWS/Bedrock, AWS/Bedrock/Agents, and AWS/Bedrock/Guardrails
// CloudWatch metrics for a declared Bedrock account identity.
//
// Kind:     "bedrock"
// Scope:    core.ScopeBlueprint
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
// Config:   Models []string; SubSignals []string (models/agents/guardrails/invocation_logs)
//
// Build requires:
//   - fx.Cloud non-nil (account_id, region)
//
// Signal contract (signals/bedrock.md):
//
//	aws_bedrock_*  [slug: bedrock-core]   — core model invocation family, dim_ModelId
//	aws_bedrock_agents_*  [slug: bedrock-agents]  — agents family, dim_Operation/ModelId/AgentAliasArn
//	aws_bedrock_guardrails_*  [slug: bedrock-guardrails]  — guardrails family, 5-dim set
//
// All five stat suffixes emitted per base (_sum/_average/_maximum/_minimum/_sample_count).
// _sum is a per-period GAUGE (never rate()); state.Set semantics throughout.
//
// Mangling traps (cw-law):
//
//	EstimatedTPMQuotaUsage → estimated_tpmquota_usage (tpmquota NOT tpm_quota)
//	outputTokenCount → output_token_count
//	CloudWatch → cloud_watch, S3 → s3
package bedrock

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/state"
)

// defaultModels are the generic AWS model IDs emitted when Config.Models is empty.
var defaultModels = []string{
	"anthropic.claude-sonnet-4-6",
	"anthropic.claude-haiku-4-5-20251001-v1:0",
	"amazon.nova-micro-v1:0",
	"amazon.nova-pro-v1:0",
	"meta.llama3-1-8b-instruct-v1:0",
	"amazon.titan-embed-text-v2:0",
}

// validSubSignals is the canonical set of sub_signal values.
var validSubSignals = map[string]bool{
	"models":          true,
	"agents":          true,
	"guardrails":      true,
	"invocation_logs": true,
}

// Config carries YAML knobs for the Bedrock construct.
type Config struct {
	// Models is the list of model IDs to emit per-model series for.
	// Defaults to defaultModels when empty.
	Models []string `yaml:"models"`
	// SubSignals selects which metric families to emit.
	// Valid values: "models", "agents", "guardrails", "invocation_logs".
	// Empty (or omitted) means all four families are emitted.
	SubSignals []string `yaml:"sub_signals"`
}

func (c *Config) emits(name string) bool {
	if len(c.SubSignals) == 0 {
		return true
	}
	for _, s := range c.SubSignals {
		if s == name {
			return true
		}
	}
	return false
}

// Construct is the per-instance Bedrock renderer. Not exported; callers use Build.
type Construct struct {
	cfg   *Config
	cloud *fixture.Cloud
	st    *state.State
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates fixtures and returns a ready core.Construct instance.
func Build(cfgAny any, fx *fixture.Set) (core.Construct, error) {
	if fx == nil || fx.Cloud == nil {
		return nil, fmt.Errorf("bedrock: Build requires a non-nil fixture.Cloud")
	}
	cfg, ok := cfgAny.(*Config)
	if !ok || cfg == nil {
		cfg = &Config{}
	}
	// Apply model defaults.
	if len(cfg.Models) == 0 {
		cfg.Models = defaultModels
	}
	return &Construct{cfg: cfg, cloud: fx.Cloud, st: state.NewState()}, nil
}

func (c *Construct) Kind() string                { return "bedrock" }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics.
// All series use state.Set (per-period gauges, ARCHITECTURE I5 — NEVER state.Add).
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	bf := w.Shape.BusinessFactor(now)

	// Failure-mode amplifier: bedrock_throttle scoped to account ID.
	throttleMult := w.Shape.FailFactor(now, "bedrock_throttle", c.cloud.AccountID, 5.0)

	// ── aws_bedrock_* (core family, dim_ModelId) ────────────────────────────
	if c.cfg.emits("models") {
		for _, modelID := range c.cfg.Models {
			lbls := c.baseLabels("AWS/Bedrock", map[string]string{
				"dimension_ModelId": modelID,
			})
			// Per-model volume weight + slow wander desync lines across models.
			mw := genai.VolumeWeight(modelID)
			wob := w.Shape.Wander(modelID, now, 0.15)
			volScale := mw * wob

			// Base names sourced VERBATIM from signals/bedrock.md [slug: bedrock-core].
			setGauge(c.st, "aws_bedrock_invocations", lbls, (100+400*bf)*volScale*w.Shape.Noise(0.10))
			// Latency: not scaled by volume — higher-cost models tend to be slower.
			// Apply mild per-model wander only so lines desync without volume distortion.
			latWander := w.Shape.Wander(modelID+"_lat", now, 0.05)
			setGauge(c.st, "aws_bedrock_invocation_latency", lbls, (500+1500*bf)*latWander*w.Shape.Noise(0.05))
			setGauge(c.st, "aws_bedrock_time_to_first_token", lbls, (150+350*bf)*latWander*w.Shape.Noise(0.05))
			// ⚠ tpmquota NOT tpm_quota (consecutive-caps collapse — cw-law trap).
			setGauge(c.st, "aws_bedrock_estimated_tpmquota_usage", lbls, (5000+45000*bf)*volScale*w.Shape.Noise(0.10))
			inputTok := (200 + 800*bf) * volScale * w.Shape.Noise(0.10)
			outputTok := (100 + 400*bf) * volScale * w.Shape.Noise(0.10)
			setGauge(c.st, "aws_bedrock_input_token_count", lbls, inputTok)
			setGauge(c.st, "aws_bedrock_output_token_count", lbls, outputTok)
			setGauge(c.st, "aws_bedrock_cache_read_input_tokens", lbls, (50+150*bf)*volScale*w.Shape.Noise(0.10))
			setGauge(c.st, "aws_bedrock_cache_write_input_tokens", lbls, (20+80*bf)*volScale*w.Shape.Noise(0.10))
			// throttle uses failure-mode amplifier.
			setGauge(c.st, "aws_bedrock_invocation_throttles", lbls, (0+5*bf)*volScale*throttleMult)
			setGauge(c.st, "aws_bedrock_invocation_client_errors", lbls, (0+2*bf)*volScale*w.Shape.Noise(0.10))
			setGauge(c.st, "aws_bedrock_invocation_server_errors", lbls, (0+1*bf)*volScale*w.Shape.Noise(0.10))
			// legacy_model_invocations ≈ 0 (no image-gen models; OutputImageCount omitted).
			setGauge(c.st, "aws_bedrock_legacy_model_invocations", lbls, 0)
		}
	}

	// ── log-delivery six (dim "Across all model IDs") ──────────────────────
	// Part of the core aws_bedrock_* family but use a fixed dim value (not per model).
	if c.cfg.emits("invocation_logs") {
		logLbls := c.baseLabels("AWS/Bedrock", map[string]string{
			"dimension_ModelId": "Across all model IDs",
		})
		// Base names sourced VERBATIM from signals/bedrock.md [slug: bedrock-core].
		setGauge(c.st, "aws_bedrock_model_invocation_logs_cloud_watch_delivery_success", logLbls, 90+10*bf)
		setGauge(c.st, "aws_bedrock_model_invocation_logs_cloud_watch_delivery_failure", logLbls, 0)
		setGauge(c.st, "aws_bedrock_model_invocation_logs_s3_delivery_success", logLbls, 80+20*bf)
		setGauge(c.st, "aws_bedrock_model_invocation_logs_s3_delivery_failure", logLbls, 0)
		setGauge(c.st, "aws_bedrock_model_invocation_large_data_s3_delivery_success", logLbls, 10+5*bf)
		setGauge(c.st, "aws_bedrock_model_invocation_large_data_s3_delivery_failure", logLbls, 0)
	}

	// ── aws_bedrock_agents_* (dim Operation + ModelId + AgentAliasArn) ─────
	// The 3-dim set is emitted together as the signals note. (Low-volume / optional family.)
	if c.cfg.emits("agents") {
		for _, modelID := range c.cfg.Models {
			// Plausible fixed values for Operation and AgentAliasArn dims.
			agentArn := fmt.Sprintf(
				"arn:aws:bedrock:%s:%s:agent-alias/AGENT01/TSTDEFAULT",
				c.cloud.Region, c.cloud.AccountID)
			lbls := c.baseLabels("AWS/Bedrock/Agents", map[string]string{
				"dimension_Operation":     "InvokeAgent",
				"dimension_ModelId":       modelID,
				"dimension_AgentAliasArn": agentArn,
			})
			// Per-model volume weight + wander for agents family.
			mw := genai.VolumeWeight(modelID)
			wob := w.Shape.Wander("agents_"+modelID, now, 0.15)
			volScale := mw * wob

			// Base names sourced VERBATIM from signals/bedrock.md [slug: bedrock-agents].
			setGauge(c.st, "aws_bedrock_agents_invocation_count", lbls, (20+80*bf)*volScale*w.Shape.Noise(0.10))
			// Latency: per-model wander only, not volume-scaled.
			agentLatWander := w.Shape.Wander("agents_lat_"+modelID, now, 0.05)
			setGauge(c.st, "aws_bedrock_agents_total_time", lbls, (2000+8000*bf)*agentLatWander*w.Shape.Noise(0.05))
			setGauge(c.st, "aws_bedrock_agents_ttft", lbls, (300+700*bf)*agentLatWander*w.Shape.Noise(0.05))
			setGauge(c.st, "aws_bedrock_agents_invocation_throttles", lbls, (0+2*bf)*volScale*throttleMult)
			setGauge(c.st, "aws_bedrock_agents_invocation_server_errors", lbls, (0+1*bf)*volScale*w.Shape.Noise(0.10))
			setGauge(c.st, "aws_bedrock_agents_invocation_client_errors", lbls, (0+1*bf)*volScale*w.Shape.Noise(0.10))
			setGauge(c.st, "aws_bedrock_agents_model_latency", lbls, (400+1600*bf)*agentLatWander*w.Shape.Noise(0.05))
			setGauge(c.st, "aws_bedrock_agents_model_invocation_count", lbls, (25+75*bf)*volScale*w.Shape.Noise(0.10))
			setGauge(c.st, "aws_bedrock_agents_model_invocation_throttles", lbls, (0+1*bf)*volScale*throttleMult)
			setGauge(c.st, "aws_bedrock_agents_model_invocation_client_errors", lbls, (0+1*bf)*volScale*w.Shape.Noise(0.10))
			setGauge(c.st, "aws_bedrock_agents_model_invocation_server_errors", lbls, (0+1*bf)*volScale*w.Shape.Noise(0.10))
			setGauge(c.st, "aws_bedrock_agents_input_token_count", lbls, (150+600*bf)*volScale*w.Shape.Noise(0.10))
			setGauge(c.st, "aws_bedrock_agents_output_token_count", lbls, (80+320*bf)*volScale*w.Shape.Noise(0.10))
		}
	}

	// ── aws_bedrock_guardrails_* (5-dim set) ──────────────────────────────
	// Dims: GuardrailArn, GuardrailVersion, GuardrailPolicyType, GuardrailContentSource,
	// Operation=ApplyGuardrail.
	if c.cfg.emits("guardrails") {
		guardrailArn := fmt.Sprintf(
			"arn:aws:bedrock:%s:%s:guardrail/gr01",
			c.cloud.Region, c.cloud.AccountID)
		for _, policyType := range []string{"CONTENT_POLICY", "WORD_POLICY"} {
			for _, contentSource := range []string{"INPUT", "OUTPUT"} {
				lbls := c.baseLabels("AWS/Bedrock/Guardrails", map[string]string{
					"dimension_GuardrailArn":           guardrailArn,
					"dimension_GuardrailVersion":       "DRAFT",
					"dimension_GuardrailPolicyType":    policyType,
					"dimension_GuardrailContentSource": contentSource,
					"dimension_Operation":              "ApplyGuardrail",
				})
				// Base names sourced VERBATIM from signals/bedrock.md [slug: bedrock-guardrails].
				setGauge(c.st, "aws_bedrock_guardrails_invocations", lbls, 50+200*bf)
				setGauge(c.st, "aws_bedrock_guardrails_invocations_intervened", lbls, 2+8*bf)
				setGauge(c.st, "aws_bedrock_guardrails_invocation_latency", lbls, 20+80*bf)
				setGauge(c.st, "aws_bedrock_guardrails_invocation_client_errors", lbls, 0+1*bf)
				setGauge(c.st, "aws_bedrock_guardrails_invocation_server_errors", lbls, 0+1*bf)
				setGauge(c.st, "aws_bedrock_guardrails_invocation_throttles", lbls, (0+2*bf)*throttleMult)
				setGauge(c.st, "aws_bedrock_guardrails_text_unit_count", lbls, 100+400*bf)
			}
		}
	}

	return w.Metrics.Write(ctx, c.st.Collect(now))
}

// baseLabels builds the full CloudWatch label set for one series.
// namespace selects the CW namespace (AWS/Bedrock, AWS/Bedrock/Agents, AWS/Bedrock/Guardrails).
// extra labels (dimension_*, tag_*) are merged in. Absent dimensions are omitted (I13).
func (c *Construct) baseLabels(namespace string, extra map[string]string) map[string]string {
	m := map[string]string{
		"account_id": c.cloud.AccountID,
		"region":     c.cloud.Region,
		"namespace":  namespace,
		"job":        "cloud/aws/bedrock",
		"name": fmt.Sprintf("arn:aws:bedrock:%s:%s:foundation-model/global",
			c.cloud.Region, c.cloud.AccountID),
	}
	for k, v := range extra {
		if v != "" { // I13: absent dimension OMITTED
			m[k] = v
		}
	}
	return m
}

// setGauge emits the full five-stat CW family for one Bedrock per-period metric.
// All values use state.Set (per-period GAUGE — NEVER Add; ARCHITECTURE I5).
func setGauge(st *state.State, name string, lbls map[string]string, v float64) {
	cw.EmitStats(st, name, lbls, cw.StatSet{Sum: v, Average: v, Maximum: v, Minimum: v, SampleCount: 60})
}
