// SPDX-License-Identifier: AGPL-3.0-only

// Package agentcore emits AWS/Bedrock-AgentCore CloudWatch metrics and Loki log
// streams for one declared AgentCore deployment.
//
// Kind:     "agentcore"
// Scope:    core.ScopeBlueprint
// Group:    (none — declared under cloud.agentcore)
// Signals:  []core.SignalClass{core.Metrics, core.Logs}
// Interval: 60s
//
// Config knobs:
//   - agents      []string  — agent logical names (no customer strings); default ["planner","retriever"]
//   - sub_signals []string  — which families to emit; empty ⇒ runtime+resource_usage+app_logs
//     valid values: runtime, resource_usage, usage_logs, gateway (deferred Spec 3)
//
// Build requires:
//   - fx.Cloud non-nil (account_id, region)
//
// Signal contract (signals/agentcore.md):
//
//	Invocation-class [slug: agentcore-invocation] — dims: base labels ONLY (no dimension_Service/Resource/Name)
//	  aws_bedrock_agentcore_invocations, aws_bedrock_agentcore_latency,
//	  aws_bedrock_agentcore_throttles, aws_bedrock_agentcore_system_errors,
//	  aws_bedrock_agentcore_user_errors, aws_bedrock_agentcore_total_errors,
//	  aws_bedrock_agentcore_session_count, aws_bedrock_agentcore_active_streaming_connections,
//	  aws_bedrock_agentcore_inbound_streaming_bytes_processed,
//	  aws_bedrock_agentcore_outbound_streaming_bytes_processed
//
//	Resource-usage [slug: agentcore-resource-usage] — dims: dimension_Service, dimension_Resource, dimension_Name
//	  aws_bedrock_agentcore_cpu_used_v_cpu_hours, aws_bedrock_agentcore_memory_used_gb_hours
//
//	Logs [slug: agentcore-logs]:
//	  source=agentcore_usage — 1-s USAGE_LOGS (vended; Firehose→Loki); gated by usage_logs sub-signal
//	  source=agentcore_app  — APPLICATION_LOGS, content-stripped diagnostics; always emitted
//
// All five CW stat suffixes emitted per base; _sum is a per-period GAUGE (never Add).
package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/state"
)

// Config carries the YAML-decoded agentcore knobs.
type Config struct {
	// Agents is the list of agent logical names rendered for resource-usage dims.
	// Default: ["planner","retriever"] — generic, no customer/blueprint strings.
	Agents []string `yaml:"agents"`
	// SubSignals selects which families to emit.
	// Valid: runtime, resource_usage, usage_logs, gateway (deferred Spec 3).
	// Empty ⇒ runtime + resource_usage + app_logs (always) emit; usage_logs is opt-in.
	SubSignals []string `yaml:"sub_signals"`
}

// defaults applies Config defaults in-place.
func (c *Config) defaults() {
	if len(c.Agents) == 0 {
		c.Agents = []string{"planner", "retriever"}
	}
}

// wantRuntime reports whether the runtime (invocation-class) family should emit.
func (c *Config) wantRuntime() bool {
	if len(c.SubSignals) == 0 {
		return true
	}
	for _, s := range c.SubSignals {
		if s == "runtime" {
			return true
		}
	}
	return false
}

// wantResourceUsage reports whether the resource_usage family should emit.
func (c *Config) wantResourceUsage() bool {
	if len(c.SubSignals) == 0 {
		return true
	}
	for _, s := range c.SubSignals {
		if s == "resource_usage" {
			return true
		}
	}
	return false
}

// wantUsageLogs reports whether the vended USAGE_LOGS lane (source=agentcore_usage)
// should emit. Unlike app logs (which always emit), usage logs are opt-in via the
// "usage_logs" sub-signal (signals/agentcore.md [slug: agentcore-logs]).
func (c *Config) wantUsageLogs() bool {
	for _, s := range c.SubSignals {
		if s == "usage_logs" {
			return true
		}
	}
	return false
}

// Construct is the per-instance AgentCore renderer. Not exported; callers use Build.
type Construct struct {
	cfg   *Config
	cloud *fixture.Cloud
	env   *fixture.Env
	st    *state.State
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates fixtures and returns a ready core.Construct instance.
func Build(cfgAny any, fx *fixture.Set) (core.Construct, error) {
	if fx == nil || fx.Cloud == nil {
		return nil, fmt.Errorf("agentcore: Build requires a non-nil fixture.Cloud")
	}
	cfg, ok := cfgAny.(*Config)
	if !ok || cfg == nil {
		cfg = &Config{}
	}
	cfg.defaults()
	return &Construct{cfg: cfg, cloud: fx.Cloud, env: fx.Env, st: state.NewState()}, nil
}

func (c *Construct) Kind() string                { return "agentcore" }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics, core.Logs} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics.
// All series use state.Set (per-period gauges — ARCHITECTURE I5, NEVER state.Add).
//
// Two dim regimes:
//  1. Invocation-class (runtime sub-signal): base labels ONLY — dimension_Service/Resource/Name
//     are UNDOCUMENTED for this family and are intentionally omitted (signals/agentcore.md §invocation-class).
//  2. Resource-usage (resource_usage sub-signal): carries dimension_Service="AgentCore.Runtime",
//     dimension_Resource=<agent-arn>, dimension_Name="<agent>::DEFAULT" — documented dims (SK-37).
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	bf := w.Shape.BusinessFactor(now)

	// Failure-mode amplifiers — AxisCloud, scoped to region so an incident propagates
	// coherently across all AgentCore series in the affected account/region.
	throttleMult := w.Shape.FailFactor(now, "agentcore_throttle", c.cloud.Region, 4.0)

	// vf is a symmetric per-series multiplier (≈1±amp) for volume/rate metrics.
	// key must uniquely identify the series so peers get distinct, stable baselines.
	vf := func(key string, amp float64) float64 { return c.seriesVar(w, now, key, amp) }

	// ── Invocation-class (runtime sub-signal) ────────────────────────────────────────
	// ⚠ base labels ONLY — dimension_Service/Resource/Name NOT stamped here (undocumented dims).
	// Invocation-class has ONE series per metric (no per-agent dimension here), so we key on
	// metric name + region to give this deployment a stable distinct baseline and slow drift.
	// Bases sourced VERBATIM from signals/agentcore.md [slug: agentcore-invocation].
	if c.cfg.wantRuntime() {
		lbls := c.baseLabels(nil)
		r := c.cloud.Region
		emitGauge(c.st, "aws_bedrock_agentcore_invocations", lbls, (50+150*bf)*vf("invocations|"+r, volAmp))
		emitGauge(c.st, "aws_bedrock_agentcore_latency", lbls, (200+800*bf)*vf("latency|"+r, volAmp))             // milliseconds
		emitGauge(c.st, "aws_bedrock_agentcore_throttles", lbls, 0.5*bf*throttleMult*vf("throttles|"+r, rateAmp)) // amplified
		emitGauge(c.st, "aws_bedrock_agentcore_system_errors", lbls, 0.1*bf*throttleMult*vf("syserr|"+r, rateAmp))
		emitGauge(c.st, "aws_bedrock_agentcore_user_errors", lbls, (1+2*bf)*vf("usererr|"+r, rateAmp))
		emitGauge(c.st, "aws_bedrock_agentcore_total_errors", lbls, (1+2.1*bf*throttleMult)*vf("toterr|"+r, rateAmp))
		emitGauge(c.st, "aws_bedrock_agentcore_session_count", lbls, (10+40*bf)*vf("sessions|"+r, volAmp))
		emitGauge(c.st, "aws_bedrock_agentcore_active_streaming_connections", lbls, (5+20*bf)*vf("streams|"+r, volAmp))
		emitGauge(c.st, "aws_bedrock_agentcore_inbound_streaming_bytes_processed", lbls, (1e4+4e4*bf)*vf("inbytes|"+r, volAmp))
		emitGauge(c.st, "aws_bedrock_agentcore_outbound_streaming_bytes_processed", lbls, (2e4+8e4*bf)*vf("outbytes|"+r, volAmp))
	}

	// ── Resource-usage (resource_usage sub-signal) ───────────────────────────────────
	// Carries documented dims: dimension_Service, dimension_Resource, dimension_Name.
	// Loop per declared agent (Config.Agents).
	// Each agent is keyed on its name so peer agents get distinct stable baselines.
	// Bases sourced VERBATIM from signals/agentcore.md [slug: agentcore-resource-usage].
	if c.cfg.wantResourceUsage() {
		for _, agent := range c.cfg.Agents {
			agentARN := fmt.Sprintf("arn:aws:bedrock-agentcore:%s:%s:runtime/%s",
				c.cloud.Region, c.cloud.AccountID, agent)
			lbls := c.baseLabels(map[string]string{
				"dimension_Service":  "AgentCore.Runtime",
				"dimension_Resource": agentARN,
				"dimension_Name":     agent + "::DEFAULT", // <AgentName::EndpointName>
			})
			emitGauge(c.st, "aws_bedrock_agentcore_cpu_used_v_cpu_hours", lbls, (0.05+0.2*bf)*vf("cpu|"+agent, volAmp))
			emitGauge(c.st, "aws_bedrock_agentcore_memory_used_gb_hours", lbls, (0.1+0.4*bf)*vf("mem|"+agent, volAmp))
		}
	}

	// gateway sub-signal — deferred (Spec 3)

	if err := w.Metrics.Write(ctx, c.st.Collect(now)); err != nil {
		return fmt.Errorf("agentcore: metrics write: %w", err)
	}

	// ── Loki log streams (signals/agentcore.md [slug: agentcore-logs]) ───────────────
	streams := c.buildLogs(now, bf)
	if len(streams) > 0 && w.Logs != nil {
		if err := w.Logs.Write(ctx, streams); err != nil {
			return fmt.Errorf("agentcore: logs write: %w", err)
		}
	}
	return nil
}

// baseLabels builds the full CloudWatch label set for one series.
// extra carries dimension_* labels; absent dims are omitted (I13).
func (c *Construct) baseLabels(extra map[string]string) map[string]string {
	m := map[string]string{
		"account_id": c.cloud.AccountID,
		"region":     c.cloud.Region,
		"namespace":  "AWS/Bedrock-AgentCore",
		"job":        "cloud/aws/bedrock-agentcore",
		"name": fmt.Sprintf("arn:aws:bedrock-agentcore:%s:%s:runtime/global",
			c.cloud.Region, c.cloud.AccountID),
	}
	for k, v := range extra {
		if v != "" { // I13: absent dimension OMITTED
			m[k] = v
		}
	}
	return m
}

// baseLogLabels returns the low-cardinality stream-label set shared by all agentcore
// Loki streams. source is set by the caller; extra keys must also be low-cardinality
// (no session_id / trace_id — those are Line.Meta).
func (c *Construct) baseLogLabels(source string) map[string]string {
	m := map[string]string{
		"account_id": c.cloud.AccountID,
		"region":     c.cloud.Region,
		"job":        "cloud/aws/bedrock-agentcore",
		"source":     source,
	}
	if c.env != nil && c.env.Name != "" {
		m["env"] = c.env.Name
	}
	return m
}

// buildLogs assembles the Loki streams for one tick.
//
//   - source=agentcore_app: APPLICATION_LOGS — content-stripped runtime diagnostics;
//     always emitted (not gated by sub_signals).
//   - source=agentcore_usage: USAGE_LOGS (vended; Firehose→Loki) — per-second CPU +
//     memory readings; gated by "usage_logs" sub-signal.
//
// High-cardinality identifiers (session_id, trace_id) ride in Line.Meta, never in
// stream labels (Loki I14/I15).
func (c *Construct) buildLogs(now time.Time, bf float64) []loki.Stream {
	var streams []loki.Stream

	// ── source=agentcore_app — content-stripped APPLICATION_LOGS ──────────────────
	// One line per declared agent; no prompt/response content (privacy invariant).
	appLabels := c.baseLogLabels("agentcore_app")
	appLabels["level"] = "info"

	appPhases := []string{"agent_start", "tool_invoke", "tool_result", "agent_step", "agent_end"}
	appLines := make([]loki.Line, 0, len(c.cfg.Agents)*len(appPhases))
	for i, agent := range c.cfg.Agents {
		phase := appPhases[i%len(appPhases)]
		// Deterministic session-like ID derived from agent index + tick time —
		// high-card, goes in Meta only.
		sessionID := fmt.Sprintf("sess-%s-%d", agent, now.Unix())
		traceID := fmt.Sprintf("tid-%s-%016x", agent, now.UnixNano())

		body := mustJSON(map[string]any{
			"msg":   "agent step completed",
			"agent": agent,
			"event": phase,
		})
		appLines = append(appLines, loki.Line{
			T:    now,
			Body: body,
			Meta: map[string]string{
				"session_id": sessionID,
				"trace_id":   traceID,
			},
		})
	}
	streams = append(streams, loki.Stream{Labels: appLabels, Lines: appLines})

	// ── source=agentcore_usage — vended USAGE_LOGS (opt-in via usage_logs sub-signal) ──
	if c.cfg.wantUsageLogs() {
		usageLabels := c.baseLogLabels("agentcore_usage")

		usageLines := make([]loki.Line, 0, len(c.cfg.Agents))
		for _, agent := range c.cfg.Agents {
			// cpu/memory are per-second observations consistent with the CW resource-usage
			// metrics (cpu_used_v_cpu_hours ≈ 0.05–0.25 vCPU-h per 60s window ⇒ 3–15 vCPU-s
			// ≈ 0.05–0.25 vCPUs instantaneous). Memory similarly derived from gb_hours.
			cpuSec := 0.05 + 0.20*bf // vCPU fraction — realistic per-second snapshot
			memGB := 0.10 + 0.40*bf  // GB — realistic per-second snapshot
			sessionID := fmt.Sprintf("sess-%s-%d", agent, now.Unix())

			body := mustJSON(map[string]any{
				"cpu":    fmt.Sprintf("%.4f", cpuSec),
				"memory": fmt.Sprintf("%.4f", memGB),
			})
			usageLines = append(usageLines, loki.Line{
				T:    now,
				Body: body,
				Meta: map[string]string{
					"session_id": sessionID,
				},
			})
		}
		streams = append(streams, loki.Stream{Labels: usageLabels, Lines: usageLines})
	}

	return streams
}

// mustJSON marshals v to a compact JSON string. Panics on marshal error (only called
// with fixed-shape map literals — cannot fail in practice).
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic("agentcore: mustJSON: " + err.Error())
	}
	return string(b)
}

// seriesVar returns a stable-but-living per-series multiplier ≈ 1: a deterministic baseline
// OFFSET (shape.Spread — so peer series that share a formula get distinct, stable values instead
// of emitting byte-identical numbers) times a slow per-series DRIFT (Wander — so the value is
// not frozen). amp sets the magnitude; volume and latency metrics use a moderate amp, error/rate
// metrics use a larger one. Returns 1.0 when no shape engine is wired.
func (c *Construct) seriesVar(w *core.World, now time.Time, key string, amp float64) float64 {
	if w.Shape == nil {
		return 1.0
	}
	return w.Shape.Spread(key, amp) * w.Shape.Wander(key, now, amp*0.4)
}

// volAmp / rateAmp set the per-series Spread+Wander magnitude per metric class.
// Volume/latency/bytes vary modestly; error and rate metrics vary more freely.
const (
	volAmp  = 0.18 // invocations, latency, session_count, streaming bytes, cpu/memory
	rateAmp = 0.30 // throttles, errors — these spike during incidents anyway
)

// emitGauge emits the full five-stat CW family for one AgentCore per-period metric.
// All values use state.Set (per-period GAUGE — NEVER Add; ARCHITECTURE I5).
func emitGauge(st *state.State, name string, lbls map[string]string, v float64) {
	cw.EmitStats(st, name, lbls, cw.StatSet{Sum: v, Average: v, Maximum: v, Minimum: v, SampleCount: 60})
}
