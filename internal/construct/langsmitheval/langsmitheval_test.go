// SPDX-License-Identifier: AGPL-3.0-only

package langsmitheval_test

// langsmitheval_test.go — contract tests for the langsmith_eval construct.
//
// (a) All 15 langsmith_eval_* names present after one tick.
// (b) Four "often-dropped" names explicitly asserted:
//     schema_validity_ratio, passthrough_exactness_ratio, retry_rate, fallback_rate.
// (c) langsmith_eval_score carries evaluator + run_outcome labels;
//     run_outcome only ∈ {success, error, pending}.
// (d) langsmith_eval_token_spend_total is a COUNTER (state.Add — value grows across two ticks).
// (e) A ratio gauge does NOT accumulate across ticks (state.Set — value stays in [0,1]).
// (f) Interface conformance: Kind / Signals / Interval.
// (g) No blueprint label on any series.
// (h) Nil Metrics writer is safe.
// (i) Registration fields: Kind / Scope / Group.

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/langsmitheval"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// testNow is a fixed mid-business-hours time (noon UTC).
var testNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// buildDefault builds a langsmith_eval construct with zero config (defaults).
func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	cfg := &langsmitheval.Config{}
	c, err := langsmitheval.Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

// tickOnce calls Tick once and returns the MetricCapture.
func tickOnce(t *testing.T, c core.Construct) *coretest.MetricCapture {
	t.Helper()
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc
}

// seriesPresent returns true if any series with the exact name exists.
func seriesPresent(mc *coretest.MetricCapture, name string) bool {
	return len(mc.Find(name)) > 0
}

// all15Names lists every metric name in the langsmith_eval family per signals/langsmith.md [slug: langsmith-eval].
var all15Names = []string{
	"langsmith_eval_completeness_ratio",
	"langsmith_eval_faithfulness_ratio",
	"langsmith_eval_env_consistency_ratio",
	"langsmith_eval_schema_validity_ratio",
	"langsmith_eval_passthrough_exactness_ratio",
	"langsmith_eval_recall_at_k",
	"langsmith_eval_precision_at_k",
	"langsmith_eval_mrr",
	"langsmith_eval_ndcg",
	"langsmith_eval_latency_seconds",
	"langsmith_eval_token_spend_total",
	"langsmith_eval_retry_rate",
	"langsmith_eval_fallback_rate",
	"langsmith_eval_hitl_rate",
	"langsmith_eval_score",
}

// ── (f) Interface conformance ─────────────────────────────────────────────────

func TestInterfaceConformance(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "langsmith_eval" {
		t.Errorf("Kind()=%q want %q", c.Kind(), "langsmith_eval")
	}
	sigs := c.Signals()
	if len(sigs) != 1 {
		t.Fatalf("Signals() len=%d want 1", len(sigs))
	}
	if sigs[0] != core.Metrics {
		t.Errorf("Signals()[0]=%v want core.Metrics", sigs[0])
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v want 60s", c.Interval())
	}
}

// ── (i) Registration fields ───────────────────────────────────────────────────

func TestRegistration(t *testing.T) {
	reg := langsmitheval.Registration()
	if reg.Kind != "langsmith_eval" {
		t.Errorf("Registration.Kind=%q want %q", reg.Kind, "langsmith_eval")
	}
	if reg.Scope != core.ScopeSubstrate {
		t.Errorf("Registration.Scope=%v want ScopeSubstrate", reg.Scope)
	}
	if reg.Group != core.GroupIntegration {
		t.Errorf("Registration.Group=%v want GroupIntegration", reg.Group)
	}
	if reg.NewConfig == nil {
		t.Error("Registration.NewConfig is nil")
	}
	if reg.Build == nil {
		t.Error("Registration.Build is nil")
	}
}

// ── (a) All 15 langsmith_eval_* names present ─────────────────────────────────

func TestAllFifteenNamesPresent(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)
	for _, name := range all15Names {
		if !seriesPresent(mc, name) {
			t.Errorf("missing series %q", name)
		}
	}
}

// ── (b) Four often-dropped names ─────────────────────────────────────────────

func TestOftenDroppedNamesPresent(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)
	oftenDropped := []string{
		"langsmith_eval_schema_validity_ratio",
		"langsmith_eval_passthrough_exactness_ratio",
		"langsmith_eval_retry_rate",
		"langsmith_eval_fallback_rate",
	}
	for _, name := range oftenDropped {
		if !seriesPresent(mc, name) {
			t.Errorf("often-dropped series %q is missing", name)
		}
	}
}

// ── (c) langsmith_eval_score: evaluator + run_outcome labels, constrained enum ──

func TestScoreLabels(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	series := mc.Find("langsmith_eval_score")
	if len(series) == 0 {
		t.Fatal("langsmith_eval_score: no series found")
	}

	allowedOutcomes := map[string]bool{"success": true, "error": true, "pending": true}

	for _, s := range series {
		if _, ok := s.Labels["evaluator"]; !ok {
			t.Errorf("langsmith_eval_score: series missing 'evaluator' label: %v", s.Labels)
		}
		outcome, ok := s.Labels["run_outcome"]
		if !ok {
			t.Errorf("langsmith_eval_score: series missing 'run_outcome' label: %v", s.Labels)
			continue
		}
		if !allowedOutcomes[outcome] {
			t.Errorf("langsmith_eval_score: run_outcome=%q not in {success,error,pending}", outcome)
		}
	}

	// Verify all three outcomes are present.
	seenOutcomes := map[string]bool{}
	for _, s := range series {
		seenOutcomes[s.Labels["run_outcome"]] = true
	}
	for want := range allowedOutcomes {
		if !seenOutcomes[want] {
			t.Errorf("langsmith_eval_score: run_outcome=%q not emitted", want)
		}
	}
}

// ── (d) langsmith_eval_token_spend_total accumulates (COUNTER / state.Add) ─────

func TestTokenSpendTotalAccumulates(t *testing.T) {
	cfg := &langsmitheval.Config{}
	c, err := langsmitheval.Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	mc1 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, nil, nil)
	if err := c.Tick(context.Background(), testNow, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}

	mc2 := &coretest.MetricCapture{}
	w2 := coretest.World(mc2, nil, nil)
	if err := c.Tick(context.Background(), testNow.Add(60*time.Second), w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	const counterName = "langsmith_eval_token_spend_total"
	s1 := mc1.Find(counterName)
	s2 := mc2.Find(counterName)
	if len(s1) == 0 || len(s2) == 0 {
		t.Fatalf("%q not found (tick1=%d tick2=%d)", counterName, len(s1), len(s2))
	}

	// Build label-sig → value map from tick1.
	prev := make(map[string]float64, len(s1))
	for _, s := range s1 {
		prev[labelSig(s.Labels)] = s.Value
	}

	grew := false
	for _, s := range s2 {
		sig := labelSig(s.Labels)
		v1, ok := prev[sig]
		if !ok {
			continue
		}
		if s.Value > v1 {
			grew = true
		}
		if s.Value < v1 {
			t.Errorf("%q: value decreased tick1=%.2f tick2=%.2f — must be state.Add (counter)", counterName, v1, s.Value)
		}
	}
	if !grew {
		t.Errorf("%q: value did not grow across two ticks — must be state.Add (counter)", counterName)
	}
}

// ── (e) A ratio gauge does NOT accumulate ────────────────────────────────────

func TestRatioGaugeDoesNotAccumulate(t *testing.T) {
	cfg := &langsmitheval.Config{}
	c, err := langsmitheval.Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	mc1 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, nil, nil)
	if err := c.Tick(context.Background(), testNow, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}

	mc2 := &coretest.MetricCapture{}
	w2 := coretest.World(mc2, nil, nil)
	if err := c.Tick(context.Background(), testNow.Add(60*time.Second), w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	const gaugeName = "langsmith_eval_completeness_ratio"
	s2 := mc2.Find(gaugeName)
	if len(s2) == 0 {
		t.Fatalf("%q: no series found in tick 2", gaugeName)
	}

	// Ratio gauges must remain in [0,1]. If state.Add were used instead of state.Set,
	// the value would be roughly 2× the first tick value (well above 1.0).
	for _, s := range s2 {
		if s.Value > 1.0+1e-9 {
			t.Errorf("%q: value %.4f > 1.0 — accumulated across ticks (state.Add used instead of state.Set)", gaugeName, s.Value)
		}
		if s.Value < 0 {
			t.Errorf("%q: value %.4f < 0 — invalid ratio", gaugeName, s.Value)
		}
	}
}

// ── Per-series realism: distinct projects must not emit byte-identical scores ──

// TestPerProjectQualitySpread asserts that peer projects emit DISTINCT quality-ratio
// values (the "every project shows 86.611%" lockstep bug). Each (project, metric) series
// gets a stable per-series Spread baseline, so faithfulness differs across projects.
func TestPerProjectQualitySpread(t *testing.T) {
	cfg := &langsmitheval.Config{
		Projects: []string{"contentgen-agents", "datagen-analysis", "docintel-extraction", "platform-assistant"},
	}
	c, err := langsmitheval.Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	for _, name := range []string{
		"langsmith_eval_faithfulness_ratio",
		"langsmith_eval_completeness_ratio",
		"langsmith_eval_schema_validity_ratio",
	} {
		byProject := map[string]float64{}
		for _, s := range mc.Find(name) {
			byProject[s.Labels["project"]] = s.Value
		}
		if len(byProject) < 4 {
			t.Fatalf("%s: expected 4 projects, got %d", name, len(byProject))
		}
		seen := map[float64]string{}
		for proj, v := range byProject {
			if prev, ok := seen[v]; ok {
				t.Errorf("%s: projects %q and %q emit identical value %.6f (lockstep)", name, prev, proj, v)
			}
			seen[v] = proj
			if v < 0 || v > 1 {
				t.Errorf("%s: project %q value %.6f out of [0,1]", name, proj, v)
			}
		}
	}
}

// TestQualityRatioDriftsOverTime asserts a single project's faithfulness is not frozen —
// it drifts across ticks (Wander) rather than holding one constant.
func TestQualityRatioDriftsOverTime(t *testing.T) {
	c := buildDefault(t)
	const name = "langsmith_eval_faithfulness_ratio"
	seen := map[float64]bool{}
	base := testNow
	for i := 0; i < 30; i++ {
		mc := &coretest.MetricCapture{}
		w := coretest.World(mc, nil, nil)
		// 13-minute steps to sample across Wander's ~37-min period.
		if err := c.Tick(context.Background(), base.Add(time.Duration(i)*13*time.Minute), w); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		s := mc.Find(name)
		if len(s) == 0 {
			t.Fatalf("%s: no series at tick %d", name, i)
		}
		seen[s[0].Value] = true
	}
	if len(seen) < 5 {
		t.Errorf("%s: only %d distinct values across 30 ticks — series is near-frozen", name, len(seen))
	}
}

// ── (g) No blueprint label on any series ─────────────────────────────────────

func TestNoScopeSubstrateBlueprint(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)
	for _, s := range mc.All() {
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("series %q carries blueprint label — langsmith_eval is ScopeSubstrate (I21)", s.Name)
		}
	}
}

// ── (h) Nil Metrics writer is safe ────────────────────────────────────────────

func TestNilWriterSafe(t *testing.T) {
	c := buildDefault(t)
	w := coretest.World(nil, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick with nil Metrics: %v", err)
	}
}

// ── Build error path ──────────────────────────────────────────────────────────

func TestBuildRejectsWrongType(t *testing.T) {
	_, err := langsmitheval.Build("wrong", nil)
	if err == nil {
		t.Error("Build with wrong type: want error, got nil")
	}
}

func TestBuildRejectsNilConfig(t *testing.T) {
	_, err := langsmitheval.Build((*langsmitheval.Config)(nil), nil)
	if err == nil {
		t.Error("Build with nil *Config: want error, got nil")
	}
}

// ── Custom config overrides defaults ─────────────────────────────────────────

func TestCustomConfig(t *testing.T) {
	cfg := &langsmitheval.Config{
		Projects:   []string{"my-proj"},
		UseCases:   []string{"qa"},
		Evaluators: []string{"accuracy"},
	}
	c, err := langsmitheval.Build(cfg, &fixture.Set{Seed: "test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	// project label must be "my-proj"
	found := false
	for _, s := range mc.All() {
		if s.Labels["project"] == "my-proj" {
			found = true
			break
		}
	}
	if !found {
		t.Error("custom project='my-proj' not found in any series")
	}

	// langsmith_eval_score must carry evaluator="accuracy"
	for _, s := range mc.Find("langsmith_eval_score") {
		if s.Labels["evaluator"] != "accuracy" {
			t.Errorf("langsmith_eval_score: evaluator=%q want %q", s.Labels["evaluator"], "accuracy")
		}
	}
}

// ── Env-scoped fan-out (Spec 3) ───────────────────────────────────────────────

// TestEnvScopedSingleEnv: Build with an Env fixture → only that env in all series;
// "prod" and "staging" must NOT appear.
func TestEnvScopedSingleEnv(t *testing.T) {
	cfg := &langsmitheval.Config{}
	c, err := langsmitheval.Build(cfg, &fixture.Set{Seed: "s", Env: &fixture.Env{Name: "tst1", Weight: 0.3}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	for _, s := range mc.All() {
		env, ok := s.Labels["env"]
		if !ok {
			// Not every series carries the env label (e.g. recall_at_k, score) — skip those.
			continue
		}
		if env != "tst1" {
			t.Errorf("series %q: env=%q want %q (env-scoped must emit only the fixture env)", s.Name, env, "tst1")
		}
	}

	// Sanity: at least some series must carry the env label.
	found := false
	for _, s := range mc.All() {
		if s.Labels["env"] != "" {
			found = true
			break
		}
	}
	if !found {
		t.Error("no series carried an env label at all — something is wrong with emitProjectMetrics")
	}
}

// TestAggregateKeepsDefaultEnvs: Build with no Env → BOTH default env values present.
func TestAggregateKeepsDefaultEnvs(t *testing.T) {
	cfg := &langsmitheval.Config{}
	c, err := langsmitheval.Build(cfg, &fixture.Set{Seed: "s"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	seenEnvs := map[string]bool{}
	for _, s := range mc.All() {
		if env, ok := s.Labels["env"]; ok && env != "" {
			seenEnvs[env] = true
		}
	}

	for _, want := range []string{"prod", "staging"} {
		if !seenEnvs[want] {
			t.Errorf("aggregate mode: env=%q not found in any series — defaultEnvs loop broken", want)
		}
	}
}

// ── Quality metrics do NOT collapse at off-peak times ────────────────────────

// testOffPeak is 03:00 UTC on a weekday — diurnal is at floor (~0.1), so bf ≈ 0.1.
// Quality ratios must NOT crater to 0.1× their target; they should stay ≥ 0.80.
var testOffPeak = time.Date(2026, 6, 16, 3, 0, 0, 0, time.UTC) // Tuesday 03:00 UTC

// TestQualityRatiosStableOffPeak asserts that faithfulness_ratio and retry_rate
// do NOT collapse when bf is low (off-peak).
//
// Before the fix, faithfulness_ratio ≈ 0.87 * 0.1 ≈ 0.087 at 03:00 UTC.
// After the fix it must be ≥ 0.80 (target 0.87, ±3% jitter, no bf scaling).
func TestQualityRatiosStableOffPeak(t *testing.T) {
	cfg := &langsmitheval.Config{}
	c, err := langsmitheval.Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), testOffPeak, w); err != nil {
		t.Fatalf("Tick at off-peak: %v", err)
	}

	// faithfulness_ratio: target 0.87, gate ≥ 0.85.
	// With pure qj jitter (±3%) the value must be ≥ 0.80 (generous floor).
	// Old code: 0.87 * 0.1 * ~1.0 ≈ 0.087 — would fail this check.
	for _, s := range mc.Find("langsmith_eval_faithfulness_ratio") {
		if s.Value < 0.80 {
			t.Errorf("langsmith_eval_faithfulness_ratio=%.4f at off-peak — craters with bf (want ≥ 0.80)", s.Value)
		}
	}

	// retry_rate: target 0.03, must NOT crater near 0.
	// After fix: clamp(0.03 * qj, 0, 1) ≈ 0.03 regardless of time.
	// Floor: 0.015 (half of target, very generous).
	// Old code: 0.03 * 0.1 = 0.003 — would fail this check.
	for _, s := range mc.Find("langsmith_eval_retry_rate") {
		if s.Value < 0.015 {
			t.Errorf("langsmith_eval_retry_rate=%.4f at off-peak — craters with bf (want ≥ 0.015)", s.Value)
		}
	}
}

// ── UseCaseWeights differentiation ───────────────────────────────────────────

// TestUseCaseWeightsDifferentiate: use_case_weights {a:5.0, b:0.4} must produce a higher
// token-spend increment for use_case "a" than for "b" after one tick. Since state.Add starts
// at zero, the tick-1 value IS the increment.
func TestUseCaseWeightsDifferentiate(t *testing.T) {
	cfg := &langsmitheval.Config{
		Projects:       []string{"test-proj"},
		UseCases:       []string{"a", "b"},
		UseCaseWeights: map[string]float64{"a": 5.0, "b": 0.4},
	}
	c, err := langsmitheval.Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	// Find token_spend_total series for use_case="a" and use_case="b".
	var valA, valB float64
	foundA, foundB := false, false
	for _, s := range mc.Find("langsmith_eval_token_spend_total") {
		switch s.Labels["use_case"] {
		case "a":
			valA = s.Value
			foundA = true
		case "b":
			valB = s.Value
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Fatalf("missing series: foundA=%v foundB=%v", foundA, foundB)
	}
	if valA <= valB {
		t.Errorf("use_case_weights not applied: valA(weight=5.0)=%.2f valB(weight=0.4)=%.2f — want valA > valB", valA, valB)
	}
}

// ── labelSig ─────────────────────────────────────────────────────────────────

// labelSig produces a stable string key from a label map (test-local; same
// algorithm as state.LabelSig but kept test-local to avoid cross-package dep).
func labelSig(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	var b []byte
	for _, k := range keys {
		b = append(b, k...)
		b = append(b, '=')
		b = append(b, m[k]...)
		b = append(b, ';')
	}
	return string(b)
}
