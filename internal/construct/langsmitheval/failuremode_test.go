// SPDX-License-Identifier: AGPL-3.0-only

package langsmitheval_test

// failuremode_test.go — TDD test for the eval_quality_degraded failure mode.
//
// Step 1 (red): this test is written BEFORE the implementation; it should fail
// because FailureModes is not yet wired and FailFactor calls are absent.
//
// The test:
//   1. Builds an env-scoped construct (envName "PRD").
//   2. Ticks once WITHOUT the failure mode → captures baseline faithfulness + retry_rate.
//   3. Ticks again WITH eval_quality_degraded active at full intensity for scope "PRD".
//   4. Asserts faithfulness DROPS to ≈0.4× baseline (downF=0.4, within 10%).
//   5. Asserts retry_rate RISES to ≈4× baseline (upF=4.0, within 10%; or clamps at 1.0).

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/langsmitheval"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// testNowFM is a fixed mid-business-hours time (noon UTC on a weekday).
// Chosen so bf ≈ 1.0 (diurnal plateau) and qj ≈ 1.0 (deterministic seed).
var testNowFM = time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

// buildEnvScoped builds an env-scoped langsmith_eval construct for env "PRD".
func buildEnvScoped(t *testing.T) core.Construct {
	t.Helper()
	cfg := &langsmitheval.Config{
		Projects:   []string{"test-proj"},
		UseCases:   []string{"assistant"},
		Evaluators: []string{"faithfulness"},
	}
	c, err := langsmitheval.Build(cfg, &fixture.Set{
		Seed: "fm-test",
		Env:  &fixture.Env{Name: "PRD", Weight: 1.0},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

// worldWithIncidents returns a *core.World wired to mc whose shape engine has the
// given incident strings pre-loaded (format: "mode@<RFC3339>/<dur>[#intensity][@scope]").
func worldWithIncidents(mc *coretest.MetricCapture, incidents []string) *core.World {
	return &core.World{
		Shape:           shape.New("", incidents),
		Metrics:         mc,
		EmitSpanMetrics: true,
	}
}

// firstValue returns the value of the first series matching name, or -1 if absent.
func firstValue(mc *coretest.MetricCapture, name string) float64 {
	ss := mc.Find(name)
	if len(ss) == 0 {
		return -1
	}
	return ss[0].Value
}

// sumValues sums all values across all series matching name (useful when the series
// is split by agent/use_case dimensions).
func sumValues(mc *coretest.MetricCapture, name string) float64 {
	var total float64
	for _, s := range mc.Find(name) {
		total += s.Value
	}
	return total
}

// TestFailureModeEvalQualityDegraded verifies that activating eval_quality_degraded:
//   - Drops faithfulness_ratio to ≈ 0.4× baseline (downF = 0.4).
//   - Raises retry_rate to ≈ 4× baseline (upF = 4.0, accounting for [0,1] clamp).
func TestFailureModeEvalQualityDegraded(t *testing.T) {
	// ── Tick 1: baseline (no failure mode) ───────────────────────────────────────
	c1 := buildEnvScoped(t)
	mc1 := &coretest.MetricCapture{}
	w1 := worldWithIncidents(mc1, nil) // no incidents
	if err := c1.Tick(context.Background(), testNowFM, w1); err != nil {
		t.Fatalf("baseline Tick: %v", err)
	}

	baselineFaith := sumValues(mc1, "langsmith_eval_faithfulness_ratio")
	baselineRetry := sumValues(mc1, "langsmith_eval_retry_rate")
	if baselineFaith <= 0 {
		t.Fatalf("baseline faithfulness_ratio = %.4f — no series emitted", baselineFaith)
	}
	if baselineRetry <= 0 {
		t.Fatalf("baseline retry_rate = %.4f — no series emitted", baselineRetry)
	}
	t.Logf("baseline: faithfulness_ratio=%.4f retry_rate=%.4f", baselineFaith, baselineRetry)

	// ── Tick 2: failure mode active at full intensity (intensity=1.0) for scope PRD ─
	// Incident format: "eval_quality_degraded@<RFC3339>/<dur>#<intensity>@<scope>"
	// Window: starts 1 hour before testNowFM, lasts 3 hours — guaranteed to be active.
	incidentStart := testNowFM.Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	incident := fmt.Sprintf("eval_quality_degraded@%s/3h#1.0@PRD", incidentStart)

	c2 := buildEnvScoped(t) // fresh construct (independent state)
	mc2 := &coretest.MetricCapture{}
	w2 := worldWithIncidents(mc2, []string{incident})
	if err := c2.Tick(context.Background(), testNowFM, w2); err != nil {
		t.Fatalf("failure-mode Tick: %v", err)
	}

	degradedFaith := sumValues(mc2, "langsmith_eval_faithfulness_ratio")
	degradedRetry := sumValues(mc2, "langsmith_eval_retry_rate")
	t.Logf("degraded: faithfulness_ratio=%.4f retry_rate=%.4f", degradedFaith, degradedRetry)

	// ── Assert faithfulness_ratio dropped to ≈ 0.4× baseline ─────────────────────
	// Expected: degraded ≈ baseline × 0.4 (downF at intensity=1.0).
	// Tolerance: 10% relative of the expected value.
	expectedFaith := baselineFaith * 0.4
	if degradedFaith > expectedFaith*1.10 {
		t.Errorf(
			"faithfulness_ratio did not drop sufficiently: baseline=%.4f degraded=%.4f expected≈%.4f (0.4×baseline ±10%%)",
			baselineFaith, degradedFaith, expectedFaith,
		)
	}
	if degradedFaith <= 0 {
		t.Errorf("faithfulness_ratio dropped to zero or below: %.4f", degradedFaith)
	}

	// ── Assert retry_rate rose to ≈ 4× baseline ───────────────────────────────────
	// Expected: degraded ≈ min(baseline × 4.0, 1.0) per [0,1] clamp.
	// Since baseline ≈ 0.03, expected ≈ 0.12 — well below 1.0, so the clamp doesn't fire.
	expectedRetry := baselineRetry * 4.0
	if expectedRetry > 1.0 {
		expectedRetry = 1.0 // saturated at clamp
	}
	if degradedRetry < expectedRetry*0.90 {
		t.Errorf(
			"retry_rate did not rise sufficiently: baseline=%.4f degraded=%.4f expected≈%.4f (4×baseline ±10%%)",
			baselineRetry, degradedRetry, expectedRetry,
		)
	}
}

// TestFailureModeRegistered verifies that FailureModes is non-empty and contains
// the eval_quality_degraded mode — a compile-time guard that the wiring step was done.
func TestFailureModeRegistered(t *testing.T) {
	reg := langsmitheval.Registration()
	if len(reg.FailureModes) == 0 {
		t.Fatal("Registration().FailureModes is empty — eval_quality_degraded not wired")
	}
	found := false
	for _, m := range reg.FailureModes {
		if m.Name == "eval_quality_degraded" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("eval_quality_degraded not found in Registration().FailureModes: %+v", reg.FailureModes)
	}
}
