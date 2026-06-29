// SPDX-License-Identifier: AGPL-3.0-only

package agentcore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/highcard"
)

func TestAgentCore_InvocationClass_NoServiceDims(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	c, err := Build(&Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Invocation-class: aws_bedrock_agentcore_invocations_* must exist.
	invSeries := mc.Find("aws_bedrock_agentcore_invocations_sum")
	if len(invSeries) == 0 {
		t.Fatal("missing aws_bedrock_agentcore_invocations_sum")
	}

	// Critical correctness: invocation-class must NOT carry dimension_Service/Resource/Name.
	for _, s := range invSeries {
		if _, ok := s.Labels["dimension_Service"]; ok {
			t.Errorf("aws_bedrock_agentcore_invocations_sum must NOT carry dimension_Service (undocumented dim); got labels %v", s.Labels)
		}
		if _, ok := s.Labels["dimension_Resource"]; ok {
			t.Errorf("aws_bedrock_agentcore_invocations_sum must NOT carry dimension_Resource; got labels %v", s.Labels)
		}
		if _, ok := s.Labels["dimension_Name"]; ok {
			t.Errorf("aws_bedrock_agentcore_invocations_sum must NOT carry dimension_Name; got labels %v", s.Labels)
		}
		if s.Labels["namespace"] != "AWS/Bedrock-AgentCore" {
			t.Errorf("namespace=%q want AWS/Bedrock-AgentCore", s.Labels["namespace"])
		}
		if s.Labels["account_id"] != "111122223333" {
			t.Errorf("account_id=%q want 111122223333", s.Labels["account_id"])
		}
		if s.Labels["region"] != "us-east-1" {
			t.Errorf("region=%q want us-east-1", s.Labels["region"])
		}
	}

	// All five stat suffixes must be present for a representative invocation-class base.
	for _, suffix := range []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"} {
		if got := mc.Find("aws_bedrock_agentcore_invocations" + suffix); len(got) == 0 {
			t.Errorf("missing aws_bedrock_agentcore_invocations%s", suffix)
		}
	}

	// Spot-check a few other invocation-class bases exist.
	for _, name := range []string{
		"aws_bedrock_agentcore_latency_sum",
		"aws_bedrock_agentcore_throttles_sum",
		"aws_bedrock_agentcore_system_errors_sum",
		"aws_bedrock_agentcore_user_errors_sum",
		"aws_bedrock_agentcore_total_errors_sum",
		"aws_bedrock_agentcore_session_count_sum",
		"aws_bedrock_agentcore_active_streaming_connections_sum",
		"aws_bedrock_agentcore_inbound_streaming_bytes_processed_sum",
		"aws_bedrock_agentcore_outbound_streaming_bytes_processed_sum",
	} {
		if got := mc.Find(name); len(got) == 0 {
			t.Errorf("missing invocation-class series %s", name)
		}
	}
}

func TestAgentCore_ResourceUsage_HasServiceDims(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	cfg := &Config{Agents: []string{"planner", "retriever"}}
	c, err := Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Resource-usage: aws_bedrock_agentcore_cpu_used_v_cpu_hours_sum must exist WITH dims.
	cpuSeries := mc.Find("aws_bedrock_agentcore_cpu_used_v_cpu_hours_sum")
	if len(cpuSeries) == 0 {
		t.Fatal("missing aws_bedrock_agentcore_cpu_used_v_cpu_hours_sum")
	}

	// Must carry all three documented resource-usage dims.
	for _, s := range cpuSeries {
		if s.Labels["dimension_Service"] != "AgentCore.Runtime" {
			t.Errorf("dimension_Service=%q want AgentCore.Runtime", s.Labels["dimension_Service"])
		}
		if s.Labels["dimension_Resource"] == "" {
			t.Errorf("dimension_Resource must be non-empty on resource-usage series")
		}
		if s.Labels["dimension_Name"] == "" {
			t.Errorf("dimension_Name must be non-empty on resource-usage series")
		}
		if s.Labels["namespace"] != "AWS/Bedrock-AgentCore" {
			t.Errorf("namespace=%q want AWS/Bedrock-AgentCore", s.Labels["namespace"])
		}
	}

	// One series per agent (planner, retriever).
	agents := map[string]bool{}
	for _, s := range cpuSeries {
		agents[s.Labels["dimension_Resource"]] = true
	}
	if len(agents) != 2 {
		t.Errorf("want 2 agent ARNs in dimension_Resource, got %d: %v", len(agents), agents)
	}

	// dimension_Name must follow the <agent>::DEFAULT pattern.
	for _, s := range cpuSeries {
		name := s.Labels["dimension_Name"]
		if name != "planner::DEFAULT" && name != "retriever::DEFAULT" {
			t.Errorf("unexpected dimension_Name=%q (want <agent>::DEFAULT)", name)
		}
	}

	// memory series must also carry dims.
	memSeries := mc.Find("aws_bedrock_agentcore_memory_used_gb_hours_sum")
	if len(memSeries) == 0 {
		t.Fatal("missing aws_bedrock_agentcore_memory_used_gb_hours_sum")
	}
	for _, s := range memSeries {
		if s.Labels["dimension_Service"] != "AgentCore.Runtime" {
			t.Errorf("memory: dimension_Service=%q want AgentCore.Runtime", s.Labels["dimension_Service"])
		}
	}
}

func TestAgentCore_SubSignals_OnlyRuntime(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	cfg := &Config{SubSignals: []string{"runtime"}}
	c, err := Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if got := mc.Find("aws_bedrock_agentcore_invocations_sum"); len(got) == 0 {
		t.Fatal("runtime sub-signal: missing invocations_sum")
	}
	if got := mc.Find("aws_bedrock_agentcore_cpu_used_v_cpu_hours_sum"); len(got) != 0 {
		t.Fatal("runtime-only: resource_usage series must NOT emit when sub_signals=[runtime]")
	}
}

func TestAgentCore_SubSignals_OnlyResourceUsage(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	cfg := &Config{SubSignals: []string{"resource_usage"}}
	c, err := Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if got := mc.Find("aws_bedrock_agentcore_cpu_used_v_cpu_hours_sum"); len(got) == 0 {
		t.Fatal("resource_usage sub-signal: missing cpu_used_v_cpu_hours_sum")
	}
	if got := mc.Find("aws_bedrock_agentcore_invocations_sum"); len(got) != 0 {
		t.Fatal("resource_usage-only: invocation-class series must NOT emit when sub_signals=[resource_usage]")
	}
}

func TestAgentCore_BuildRequiresCloud(t *testing.T) {
	// nil fixture.Set
	if _, err := Build(&Config{}, nil); err == nil {
		t.Fatal("want error for nil fixture.Set")
	}
	// nil Cloud
	if _, err := Build(&Config{}, &fixture.Set{}); err == nil {
		t.Fatal("want error for nil fixture.Cloud")
	}
}

func TestAgentCore_DefaultAgents(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	// Empty Config — defaults applied.
	c, err := Build(&Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Default agents = ["planner","retriever"] → 2 resource-usage series.
	cpuSeries := mc.Find("aws_bedrock_agentcore_cpu_used_v_cpu_hours_sum")
	if len(cpuSeries) != 2 {
		t.Errorf("want 2 cpu_used_v_cpu_hours_sum series (one per default agent), got %d", len(cpuSeries))
	}
}

// forbiddenStreamLabelKeys is the canonical high-card set that must never appear as
// stream labels (sourced from internal/highcard for consistency with the Loki sink).
var forbiddenStreamLabelKeys = func() map[string]struct{} {
	m := make(map[string]struct{})
	for _, k := range highcard.Fields() {
		m[k] = struct{}{}
	}
	return m
}()

func TestAgentCore_Logs_AppStreamAlwaysEmitted(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
		Env:   &fixture.Env{Name: "prod"},
	}
	c, err := Build(&Config{Agents: []string{"planner", "retriever"}}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	world := coretest.World(mc, lc, nil)
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Must have at least one stream with source=agentcore_app.
	var appStreams []int
	for i, s := range lc.Streams {
		if s.Labels["source"] == "agentcore_app" {
			appStreams = append(appStreams, i)
		}
	}
	if len(appStreams) == 0 {
		t.Fatalf("want at least one stream with source=agentcore_app, got none; all streams: %v",
			func() []string {
				var ss []string
				for _, s := range lc.Streams {
					ss = append(ss, s.Labels["source"])
				}
				return ss
			}())
	}

	// Check required stream labels on the app stream.
	for _, idx := range appStreams {
		st := lc.Streams[idx]
		for _, want := range []string{"account_id", "region", "job", "source", "level", "env"} {
			if st.Labels[want] == "" {
				t.Errorf("agentcore_app stream missing label %q; labels=%v", want, st.Labels)
			}
		}
		if st.Labels["source"] != "agentcore_app" {
			t.Errorf("source=%q want agentcore_app", st.Labels["source"])
		}
		if st.Labels["account_id"] != "111122223333" {
			t.Errorf("account_id=%q want 111122223333", st.Labels["account_id"])
		}
		if st.Labels["region"] != "us-east-1" {
			t.Errorf("region=%q want us-east-1", st.Labels["region"])
		}

		// No high-cardinality keys in stream labels (I14/I15).
		for k := range st.Labels {
			if _, bad := forbiddenStreamLabelKeys[k]; bad {
				t.Errorf("high-card key %q found in agentcore_app stream labels (must be Line.Meta)", k)
			}
		}

		// Lines must exist, each with session_id + trace_id in Meta (not Labels).
		if len(st.Lines) == 0 {
			t.Errorf("agentcore_app stream has no lines")
		}
		for _, ln := range st.Lines {
			if ln.Meta["session_id"] == "" {
				t.Errorf("agentcore_app line missing session_id in Meta")
			}
			if ln.Meta["trace_id"] == "" {
				t.Errorf("agentcore_app line missing trace_id in Meta")
			}
			// Body must be valid JSON with "msg" + "agent" + "event" keys.
			var m map[string]any
			if err := json.Unmarshal([]byte(ln.Body), &m); err != nil {
				t.Errorf("agentcore_app line body is not valid JSON: %v — body=%q", err, ln.Body)
				continue
			}
			for _, key := range []string{"msg", "agent", "event"} {
				if _, ok := m[key]; !ok {
					t.Errorf("agentcore_app body missing key %q; body=%q", key, ln.Body)
				}
			}
		}
	}

	// No source=agentcore_usage stream emitted when usage_logs not in sub_signals.
	for _, s := range lc.Streams {
		if s.Labels["source"] == "agentcore_usage" {
			t.Errorf("source=agentcore_usage emitted without usage_logs sub-signal; labels=%v", s.Labels)
		}
	}
}

func TestAgentCore_Logs_UsageStreamGated(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
		Env:   &fixture.Env{Name: "prod"},
	}
	cfg := &Config{
		Agents:     []string{"planner", "retriever"},
		SubSignals: []string{"usage_logs"},
	}
	c, err := Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	world := coretest.World(mc, lc, nil)
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Must have a source=agentcore_usage stream.
	var usageIdx = -1
	for i, s := range lc.Streams {
		if s.Labels["source"] == "agentcore_usage" {
			usageIdx = i
			break
		}
	}
	if usageIdx < 0 {
		t.Fatalf("want source=agentcore_usage stream when usage_logs in sub_signals, got none")
	}

	st := lc.Streams[usageIdx]

	// Required stream labels on usage stream.
	for _, want := range []string{"account_id", "region", "job", "source"} {
		if st.Labels[want] == "" {
			t.Errorf("agentcore_usage stream missing label %q; labels=%v", want, st.Labels)
		}
	}

	// No high-cardinality keys in stream labels (I14/I15).
	for k := range st.Labels {
		if _, bad := forbiddenStreamLabelKeys[k]; bad {
			t.Errorf("high-card key %q found in agentcore_usage stream labels (must be Line.Meta)", k)
		}
	}

	// Lines must exist — one per agent.
	if len(st.Lines) != len(cfg.Agents) {
		t.Errorf("agentcore_usage: want %d lines (one per agent), got %d", len(cfg.Agents), len(st.Lines))
	}
	for _, ln := range st.Lines {
		if ln.Meta["session_id"] == "" {
			t.Errorf("agentcore_usage line missing session_id in Meta")
		}
		// Body must be valid JSON with "cpu" + "memory" keys.
		var m map[string]any
		if err := json.Unmarshal([]byte(ln.Body), &m); err != nil {
			t.Errorf("agentcore_usage body not valid JSON: %v — body=%q", err, ln.Body)
			continue
		}
		for _, key := range []string{"cpu", "memory"} {
			if _, ok := m[key]; !ok {
				t.Errorf("agentcore_usage body missing key %q; body=%q", key, ln.Body)
			}
		}
	}
}

func TestAgentCore_Logs_NilLogsWriterNoError(t *testing.T) {
	// Verifies Tick succeeds when w.Logs is nil (backward compat with metric-only worlds).
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	c, err := Build(&Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil) // nil LogCapture → w.Logs is nil
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick with nil w.Logs must not error, got: %v", err)
	}
}

// ── Per-series realism: distinct agents must not emit byte-identical values ──────────────────────

// TestPerAgentResourceUsageSpread asserts that peer agents emit DISTINCT values for the
// resource-usage family (the "every agent emits 0.150 vCPU-h" lockstep bug).
// Each (agent, metric) series gets a stable per-series Spread baseline.
func TestPerAgentResourceUsageSpread(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	cfg := &Config{Agents: []string{"planner", "retriever", "router", "validator"}}
	c, err := Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	for _, metricSuffix := range []string{
		"aws_bedrock_agentcore_cpu_used_v_cpu_hours_sum",
		"aws_bedrock_agentcore_memory_used_gb_hours_sum",
	} {
		series := mc.Find(metricSuffix)
		if len(series) < 4 {
			t.Fatalf("%s: expected ≥4 series (one per agent), got %d", metricSuffix, len(series))
		}
		seen := map[float64]string{}
		for _, s := range series {
			agentLabel := s.Labels["dimension_Resource"]
			if prev, ok := seen[s.Value]; ok {
				t.Errorf("%s: agents %q and %q emit identical value %.6f (lockstep)", metricSuffix, prev, agentLabel, s.Value)
			}
			seen[s.Value] = agentLabel
		}
	}
}

// TestResourceUsageDriftsOverTime asserts that a single agent's cpu metric is not frozen —
// it drifts across ticks (Wander) rather than holding one constant.
func TestResourceUsageDriftsOverTime(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	cfg := &Config{Agents: []string{"planner"}}
	c, err := Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	const metricName = "aws_bedrock_agentcore_cpu_used_v_cpu_hours_sum"
	seen := map[float64]bool{}
	base := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		if err := c.Tick(context.Background(), base.Add(time.Duration(i)*13*time.Minute), world); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		s := mc.Find(metricName)
		if len(s) == 0 {
			t.Fatalf("%s: no series at tick %d", metricName, i)
		}
		seen[s[0].Value] = true
	}
	if len(seen) < 5 {
		t.Errorf("%s: only %d distinct values across 30 ticks — series is near-frozen", metricName, len(seen))
	}
}

// TestInvocationClassDriftsOverTime asserts that the invocation-class (single-series) metrics
// drift over time rather than being frozen at a constant.
func TestInvocationClassDriftsOverTime(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	c, err := Build(&Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	const metricName = "aws_bedrock_agentcore_invocations_sum"
	seen := map[float64]bool{}
	base := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		if err := c.Tick(context.Background(), base.Add(time.Duration(i)*13*time.Minute), world); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		s := mc.Find(metricName)
		if len(s) == 0 {
			t.Fatalf("%s: no series at tick %d", metricName, i)
		}
		seen[s[0].Value] = true
	}
	if len(seen) < 5 {
		t.Errorf("%s: only %d distinct values across 30 ticks — series is near-frozen", metricName, len(seen))
	}
}

func TestAgentCore_SignalsIncludesLogs(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
	}
	c, err := Build(&Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Verify Signals() declares both Metrics and Logs by checking the String()
	// representation (avoids a core import cycle in package agentcore tests).
	sigs := c.Signals()
	sigNames := make(map[string]bool, len(sigs))
	for _, s := range sigs {
		sigNames[s.String()] = true
	}
	if !sigNames["metrics"] {
		t.Errorf("Signals() must include metrics; got %v", sigs)
	}
	if !sigNames["logs"] {
		t.Errorf("Signals() must include logs; got %v", sigs)
	}
}
