// SPDX-License-Identifier: AGPL-3.0-only

package qualificationpipeline_test

// qualificationpipeline_test.go — contract tests for the qualification_pipeline construct.
//
// (a) Real gitlab_ci_pipeline_* families present with correct label keys.
// (b) Coined qualification_* families present with correct label keys (SK-48).
// (c) Loki stream present: {job="qualification-pipeline"} per cloud; lines non-empty.
// (d) Env-scoped: Build with Env → env label stamped on all series + streams.
// (e) Aggregate: Build without Env → env label absent on all series + streams (I13).
// (f) Interface conformance: Kind / Scope / Group / Signals / Interval.
// (g) Registration: Kind / Scope / Group / NewConfig / Build present.
// (h) Nil Metrics and nil Logs writers are safe (no panic).
// (i) gitlab_ci_pipeline_run_count is monotone across two ticks (counter).
// (j) gitlab_ci_pipeline_status is not accumulated (gauge / state.Set).
// (k) Pipeline status label spread: all 7 statuses emitted for each pipeline label set.
// (l) Custom Clouds config fans correctly.

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/qualificationpipeline"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

var testNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// buildWith builds a qualification_pipeline construct with the given Config and fixture.
func buildWith(t *testing.T, cfg *qualificationpipeline.Config, fx *fixture.Set) core.Construct {
	t.Helper()
	if fx == nil {
		fx = &fixture.Set{Seed: "test"}
	}
	c, err := qualificationpipeline.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

// buildDefault builds with a zero Config and no Env (aggregate).
func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	return buildWith(t, &qualificationpipeline.Config{}, nil)
}

// tickOnce calls Tick once and returns MetricCapture and LogCapture.
func tickOnce(t *testing.T, c core.Construct) (*coretest.MetricCapture, *coretest.LogCapture) {
	t.Helper()
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := coretest.World(mc, lc, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc, lc
}

// seriesPresent returns true if any series with the exact name exists.
func seriesPresent(mc *coretest.MetricCapture, name string) bool {
	return len(mc.Find(name)) > 0
}

// ── (f) Interface conformance ─────────────────────────────────────────────────

func TestInterfaceConformance(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "qualification_pipeline" {
		t.Errorf("Kind()=%q want %q", c.Kind(), "qualification_pipeline")
	}
	sigs := c.Signals()
	if len(sigs) != 2 {
		t.Fatalf("Signals() len=%d want 2", len(sigs))
	}
	hasMetrics, hasLogs := false, false
	for _, s := range sigs {
		switch s {
		case core.Metrics:
			hasMetrics = true
		case core.Logs:
			hasLogs = true
		}
	}
	if !hasMetrics {
		t.Error("Signals(): Metrics missing")
	}
	if !hasLogs {
		t.Error("Signals(): Logs missing")
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v want 60s", c.Interval())
	}
}

// ── (g) Registration ──────────────────────────────────────────────────────────

func TestRegistration(t *testing.T) {
	reg := qualificationpipeline.Registration()
	if reg.Kind != "qualification_pipeline" {
		t.Errorf("Registration Kind=%q want %q", reg.Kind, "qualification_pipeline")
	}
	if reg.Scope != core.ScopeSubstrate {
		t.Errorf("Registration Scope=%v want ScopeSubstrate", reg.Scope)
	}
	if reg.Group != core.GroupIntegration {
		t.Errorf("Registration Group=%v want GroupIntegration", reg.Group)
	}
	if reg.NewConfig == nil {
		t.Error("Registration NewConfig is nil")
	}
	if reg.Build == nil {
		t.Error("Registration Build is nil")
	}
}

// ── (a) Real gitlab_ci_pipeline_* families present ────────────────────────────

func TestRealFamiliesPresent(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)

	realFamilies := []struct {
		name       string
		desc       string
		wantLabels []string
	}{
		{
			"gitlab_ci_pipeline_status",
			"pipeline status gauge",
			[]string{"project", "ref", "kind", "source", "topics", "variables", "status", "cloud"},
		},
		{
			"gitlab_ci_pipeline_duration_seconds",
			"pipeline duration gauge",
			[]string{"project", "ref", "kind", "source", "topics", "variables", "cloud"},
		},
		{
			"gitlab_ci_pipeline_run_count",
			"pipeline run counter",
			[]string{"project", "ref", "kind", "source", "topics", "variables", "cloud"},
		},
		{
			"gitlab_ci_pipeline_job_status",
			"job status gauge",
			[]string{"project", "ref", "kind", "source", "topics", "variables", "stage", "job_name", "runner_description", "tag_list", "failure_reason", "status", "cloud"},
		},
		{
			"gitlab_ci_pipeline_job_duration_seconds",
			"job duration gauge",
			[]string{"project", "ref", "kind", "source", "topics", "variables", "stage", "job_name", "runner_description", "tag_list", "failure_reason", "cloud"},
		},
	}

	for _, tc := range realFamilies {
		if !seriesPresent(mc, tc.name) {
			t.Errorf("real family %q (%s): absent", tc.name, tc.desc)
			continue
		}
		// Verify required label keys present on at least one series.
		keys := mc.LabelKeys(tc.name)
		keySet := make(map[string]bool, len(keys))
		for _, k := range keys {
			keySet[k] = true
		}
		for _, wantKey := range tc.wantLabels {
			if !keySet[wantKey] {
				t.Errorf("real family %q: label %q absent (have: %v)", tc.name, wantKey, keys)
			}
		}
	}
}

// ── (b) Coined qualification_* families present ───────────────────────────────

func TestCoinedFamiliesPresent(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)

	coinedFamilies := []struct {
		name       string
		desc       string
		wantLabels []string
	}{
		{
			"qualification_test_cases_total",
			"test cases total gauge (coined SK-48)",
			[]string{"cloud", "suite"},
		},
		{
			"qualification_test_failures_total",
			"test failures total gauge (coined SK-48)",
			[]string{"cloud", "suite"},
		},
		{
			"qualification_report_generated",
			"report generated gauge (coined SK-48)",
			[]string{"cloud", "suite"},
		},
	}

	for _, tc := range coinedFamilies {
		if !seriesPresent(mc, tc.name) {
			t.Errorf("coined family %q (%s): absent", tc.name, tc.desc)
			continue
		}
		keys := mc.LabelKeys(tc.name)
		keySet := make(map[string]bool, len(keys))
		for _, k := range keys {
			keySet[k] = true
		}
		for _, wantKey := range tc.wantLabels {
			if !keySet[wantKey] {
				t.Errorf("coined family %q: label %q absent (have: %v)", tc.name, wantKey, keys)
			}
		}
	}
}

// ── (c) Loki stream present ───────────────────────────────────────────────────

func TestLokiStreamPresent(t *testing.T) {
	c := buildDefault(t)
	_, lc := tickOnce(t, c)

	if len(lc.Streams) == 0 {
		t.Fatal("no log streams emitted")
	}
	for _, stream := range lc.Streams {
		if stream.Labels["job"] != "qualification-pipeline" {
			t.Errorf("stream job=%q want %q", stream.Labels["job"], "qualification-pipeline")
		}
		if stream.Labels["cloud"] == "" {
			t.Error("stream cloud label missing or empty")
		}
		if len(stream.Lines) == 0 {
			t.Errorf("stream (cloud=%q): no lines", stream.Labels["cloud"])
		}
	}

	// One stream per default cloud (4 clouds by default).
	if len(lc.Streams) != 4 {
		t.Errorf("expected 4 streams (one per default cloud), got %d", len(lc.Streams))
	}
}

// ── (d) Env-scoped: env label stamped ────────────────────────────────────────

func TestEnvScopedStampsEnvLabel(t *testing.T) {
	fx := &fixture.Set{Seed: "s", Env: &fixture.Env{Name: "tst1", Weight: 0.3, NonProd: true}}
	c := buildWith(t, &qualificationpipeline.Config{}, fx)
	mc, lc := tickOnce(t, c)

	for _, s := range mc.All() {
		if s.Labels["env"] != "tst1" {
			t.Errorf("series %q: env=%q want %q", s.Name, s.Labels["env"], "tst1")
		}
	}
	for _, stream := range lc.Streams {
		if stream.Labels["env"] != "tst1" {
			t.Errorf("log stream cloud=%q: env=%q want %q", stream.Labels["cloud"], stream.Labels["env"], "tst1")
		}
	}
}

// ── (e) Aggregate: env label absent ──────────────────────────────────────────

func TestAggregateEnvLabelAbsent(t *testing.T) {
	c := buildDefault(t)
	mc, lc := tickOnce(t, c)

	for _, s := range mc.All() {
		if _, ok := s.Labels["env"]; ok {
			t.Errorf("series %q: env label present in aggregate mode (want absent per I13)", s.Name)
		}
	}
	for _, stream := range lc.Streams {
		if _, ok := stream.Labels["env"]; ok {
			t.Errorf("log stream cloud=%q: env label present in aggregate mode (want absent per I13)", stream.Labels["cloud"])
		}
	}
}

// ── (h) Nil writers are safe ─────────────────────────────────────────────────

func TestNilWritersSafe(t *testing.T) {
	c := buildDefault(t)
	w := coretest.World(nil, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick with nil writers: %v", err)
	}
}

// ── (i) gitlab_ci_pipeline_run_count is monotone (counter) ───────────────────

func TestRunCountMonotone(t *testing.T) {
	c := buildDefault(t)

	mc1 := &coretest.MetricCapture{}
	mc2 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, nil, nil)
	w2 := coretest.World(mc2, nil, nil)

	if err := c.Tick(context.Background(), testNow, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if err := c.Tick(context.Background(), testNow.Add(60*time.Second), w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	const counterName = "gitlab_ci_pipeline_run_count"
	s1 := mc1.Find(counterName)
	s2 := mc2.Find(counterName)
	if len(s1) == 0 || len(s2) == 0 {
		t.Fatalf("counter %q not found (tick1=%d tick2=%d)", counterName, len(s1), len(s2))
	}
	if s2[0].Value <= s1[0].Value {
		t.Errorf("counter %q not monotone: tick1=%.4f tick2=%.4f", counterName, s1[0].Value, s2[0].Value)
	}
}

// ── (j) gitlab_ci_pipeline_status is not accumulated (gauge / Set) ────────────

func TestPipelineStatusNotAccumulated(t *testing.T) {
	c := buildDefault(t)

	mc1 := &coretest.MetricCapture{}
	mc2 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, nil, nil)
	w2 := coretest.World(mc2, nil, nil)

	if err := c.Tick(context.Background(), testNow, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if err := c.Tick(context.Background(), testNow.Add(60*time.Second), w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	// All status values are 0 or 1 — none should accumulate to 2+.
	for _, s := range mc2.Find("gitlab_ci_pipeline_status") {
		if s.Value > 1.5 {
			t.Errorf("gitlab_ci_pipeline_status accumulated to %v (>1.5) — looks like Add instead of Set", s.Value)
		}
	}
}

// ── (k) Pipeline status label spread: all 7 statuses emitted ─────────────────

func TestPipelineStatusSpread(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)

	wantStatuses := []string{"success", "failed", "running", "pending", "canceled", "skipped", "manual"}
	foundStatuses := map[string]bool{}
	for _, s := range mc.Find("gitlab_ci_pipeline_status") {
		foundStatuses[s.Labels["status"]] = true
	}
	for _, want := range wantStatuses {
		if !foundStatuses[want] {
			t.Errorf("gitlab_ci_pipeline_status: status=%q not found in label spread", want)
		}
	}
}

// ── (l) Custom Clouds config fans correctly ───────────────────────────────────

func TestCustomCloudsConfig(t *testing.T) {
	c := buildWith(t, &qualificationpipeline.Config{Clouds: []string{"aws"}}, nil)
	_, lc := tickOnce(t, c)

	if len(lc.Streams) != 1 {
		t.Errorf("custom Clouds=[aws]: expected 1 stream, got %d", len(lc.Streams))
	}
	if len(lc.Streams) > 0 && lc.Streams[0].Labels["cloud"] != "aws" {
		t.Errorf("custom Clouds=[aws]: stream cloud=%q want %q", lc.Streams[0].Labels["cloud"], "aws")
	}
}

// ── (m) Per-cloud series must emit distinct values (lockstep fix) ─────────────

// TestPeerCloudPipelineDurationDistinct asserts that per-cloud pipeline duration
// values are NOT all identical — each cloud gets a stable per-series Spread offset.
func TestPeerCloudPipelineDurationDistinct(t *testing.T) {
	c := buildWith(t, &qualificationpipeline.Config{
		Clouds: []string{"aws", "azure", "gcp", "common"},
	}, nil)
	mc, _ := tickOnce(t, c)

	const name = "gitlab_ci_pipeline_duration_seconds"
	series := mc.Find(name)
	if len(series) < 4 {
		t.Fatalf("%s: expected ≥4 series (one per cloud), got %d", name, len(series))
	}
	byCloud := map[string]float64{}
	for _, s := range series {
		byCloud[s.Labels["cloud"]] = s.Value
	}
	seen := map[float64]string{}
	for cloud, v := range byCloud {
		if prev, ok := seen[v]; ok {
			t.Errorf("%s: clouds %q and %q emit identical value %.6f (lockstep)", name, prev, cloud, v)
		}
		seen[v] = cloud
		if v <= 0 {
			t.Errorf("%s: cloud %q value %.4f ≤ 0", name, cloud, v)
		}
	}
}

// TestPeerCloudJobDurationDistinct asserts that per-cloud job duration values are distinct.
func TestPeerCloudJobDurationDistinct(t *testing.T) {
	c := buildWith(t, &qualificationpipeline.Config{
		Clouds: []string{"aws", "azure", "gcp", "common"},
		Jobs:   []string{"validation-sbom"},
	}, nil)
	mc, _ := tickOnce(t, c)

	const name = "gitlab_ci_pipeline_job_duration_seconds"
	series := mc.Find(name)
	if len(series) < 4 {
		t.Fatalf("%s: expected ≥4 series (one per cloud), got %d", name, len(series))
	}
	byCloud := map[string]float64{}
	for _, s := range series {
		byCloud[s.Labels["cloud"]] = s.Value
	}
	seen := map[float64]string{}
	for cloud, v := range byCloud {
		if prev, ok := seen[v]; ok {
			t.Errorf("%s: clouds %q and %q emit identical value %.6f (lockstep)", name, prev, cloud, v)
		}
		seen[v] = cloud
		if v <= 0 {
			t.Errorf("%s: cloud %q value %.4f ≤ 0", name, cloud, v)
		}
	}
}

// TestPeerCloudTestCasesDistinct asserts that per-cloud+suite test_cases_total values are distinct.
func TestPeerCloudTestCasesDistinct(t *testing.T) {
	c := buildWith(t, &qualificationpipeline.Config{
		Clouds: []string{"aws", "azure", "gcp", "common"},
		Suites: []string{"infra"},
	}, nil)
	mc, _ := tickOnce(t, c)

	const name = "qualification_test_cases_total"
	series := mc.Find(name)
	if len(series) < 4 {
		t.Fatalf("%s: expected ≥4 series (one per cloud), got %d", name, len(series))
	}
	byCloud := map[string]float64{}
	for _, s := range series {
		byCloud[s.Labels["cloud"]] = s.Value
	}
	seen := map[float64]string{}
	for cloud, v := range byCloud {
		if prev, ok := seen[v]; ok {
			t.Errorf("%s: clouds %q and %q emit identical value %.6f (lockstep)", name, prev, cloud, v)
		}
		seen[v] = cloud
		if v <= 0 {
			t.Errorf("%s: cloud %q value %.4f ≤ 0", name, cloud, v)
		}
	}
}

// ── (n) Duration series drifts over time (not frozen at a constant) ───────────

// TestPipelineDurationDriftsOverTime asserts that a pipeline duration series
// produces ≥5 distinct values across 30 ticks at 13-minute steps.
func TestPipelineDurationDriftsOverTime(t *testing.T) {
	c := buildWith(t, &qualificationpipeline.Config{
		Clouds: []string{"aws"},
	}, nil)
	const name = "gitlab_ci_pipeline_duration_seconds"
	seen := map[float64]bool{}
	base := testNow
	for i := 0; i < 30; i++ {
		mc := &coretest.MetricCapture{}
		w := coretest.World(mc, nil, nil)
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
