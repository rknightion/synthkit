// SPDX-License-Identifier: AGPL-3.0-only

// Package qualificationpipeline implements the "qualification_pipeline" construct.
//
// Kind:     "qualification_pipeline"
// Scope:    core.ScopeSubstrate — substrate-scoped; NO blueprint label (I21).
// Group:    core.GroupIntegration
// Signals:  []core.SignalClass{core.Metrics, core.Logs}
// Interval: 60s
//
// Identity: Config-borne (stages, jobs, suites, clouds). No blueprint-name references.
//
// Signal contract: signals/qualification.md [slug: qualification-pipeline]
//
// REAL metric families (mvisonneau/gitlab-ci-pipelines-exporter, docs/metrics.md, 2026-06-15):
//   - gitlab_ci_pipeline_status          gauge — one per status value; labels: project,ref,kind,source,topics,variables,status
//   - gitlab_ci_pipeline_duration_seconds gauge — duration of most recent pipeline; labels: project,ref,kind,source,topics,variables
//   - gitlab_ci_pipeline_run_count        counter — execution count; labels: project,ref,kind,source,topics,variables
//   - gitlab_ci_pipeline_job_status       gauge — per job; labels: ...+stage,job_name,runner_description,tag_list,failure_reason,status
//   - gitlab_ci_pipeline_job_duration_seconds gauge — per job; labels: ...+stage,job_name,runner_description,tag_list,failure_reason
//
// COINED metric families (no exporter provides these suite-level signals; SK-48):
//   - qualification_test_cases_total    gauge
//   - qualification_test_failures_total gauge
//   - qualification_report_generated    gauge (0/1)
//
// Loki stream: {job="qualification-pipeline"} one stream per cloud, with a plausible
// pipeline-run log line per tick.
package qualificationpipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// Kind is the registry key.
const Kind = "qualification_pipeline"

// pipelineStatuses is the complete valid status label value set for gitlab_ci_pipeline_status.
// Source: gitlab-ci-pipelines-exporter docs/metrics.md + sparse-status-metrics section (2026-06-15).
var pipelineStatuses = []string{
	"success", "failed", "running", "pending", "canceled", "skipped", "manual",
}

// defaultStages is the default stage list for a qualification CI pipeline.
var defaultStages = []string{
	"verification", "build", "test", "test-tokens-usage", "autovalidate", "pdf",
}

// defaultJobs is the default job list for a qualification CI pipeline.
var defaultJobs = []string{
	"validation-sbom", "iac-tests", "functional-tests",
}

// defaultSuites is the default test suite list.
var defaultSuites = []string{"infra", "functional"}

// defaultClouds is the default cloud target list.
var defaultClouds = []string{"aws", "azure", "gcp", "common"}

// durationBuckets is a plausible seconds-valued bucket set for CI job/pipeline duration histograms.
// Pipeline durations range from seconds to tens of minutes; bucket upper bound 3600s covers an hour.
// Source: synthkit convention (no exporter documents histogram buckets for these gauges; this
// construct emits duration_seconds as a gauge per the exporter type; the bucket set is kept for
// reference in case histogram is added in future).
var durationBuckets = []float64{
	5, 15, 30, 60, 120, 300, 600, 900, 1800, 3600,
}

// Config is the construct's YAML config struct.
type Config struct {
	// Stages is the list of CI pipeline stage names to spread across label values.
	// Default: ["verification","build","test","test-tokens-usage","autovalidate","pdf"].
	Stages []string `yaml:"stages"`
	// Jobs is the list of CI job names to spread across label values.
	// Default: ["validation-sbom","iac-tests","functional-tests"].
	Jobs []string `yaml:"jobs"`
	// Suites is the list of test suite names (coined qualification_* metrics only).
	// Default: ["infra","functional"].
	Suites []string `yaml:"suites"`
	// Clouds is the list of cloud target names to fan across label spread.
	// Default: ["aws","azure","gcp","common"].
	Clouds []string `yaml:"clouds"`
}

// NewConfig returns a pointer to a zero Config for the YAML decoder.
func NewConfig() any { return &Config{} }

// Construct is the per-instance qualification_pipeline renderer. Not exported; callers use Build.
type Construct struct {
	stages []string
	jobs   []string
	suites []string
	clouds []string
	st     *state.State
	// Env-scoping (Spec 3): when the fixture carries an Env, the construct is fanned per-env —
	// envName is stamped as the `env` label and magnitudes scale by Shape.Factor(now, weight, nonProd).
	// Aggregate (nil Env) omits the env label entirely (I13) and uses Shape.BusinessFactor (n-1).
	envScoped bool
	envName   string
	weight    float64
	nonProd   bool
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates cfg and fx and returns a ready core.Construct instance.
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfg.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("qualificationpipeline: Build called with %T, want *Config", cfg)
	}

	stages := c.Stages
	if len(stages) == 0 {
		stages = defaultStages
	}
	jobs := c.Jobs
	if len(jobs) == 0 {
		jobs = defaultJobs
	}
	suites := c.Suites
	if len(suites) == 0 {
		suites = defaultSuites
	}
	clouds := c.Clouds
	if len(clouds) == 0 {
		clouds = defaultClouds
	}

	// Env-scoped fan-out (Spec 3): the fixture's Env drives per-env weight scaling and stamps
	// the env label. Aggregate (nil Env) omits the env label (I13) and uses weight 1.0.
	weight, nonProd, envScoped := 1.0, false, false
	var envName string
	if fx != nil && fx.Env != nil {
		envName = fx.Env.Name
		weight = fx.Env.Weight
		nonProd = fx.Env.NonProd
		envScoped = true
	}

	return &Construct{
		stages:    stages,
		jobs:      jobs,
		suites:    suites,
		clouds:    clouds,
		st:        state.NewState(),
		envScoped: envScoped,
		envName:   envName,
		weight:    weight,
		nonProd:   nonProd,
	}, nil
}

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics, core.Logs} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics and w.Logs.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	batch := c.renderMetrics(now, w)
	if w.Metrics != nil {
		if err := w.Metrics.Write(ctx, batch); err != nil {
			return err
		}
	}
	if w.Logs != nil {
		streams := c.renderLogs(now)
		if err := w.Logs.Write(ctx, streams); err != nil {
			return err
		}
	}
	return nil
}

// seriesVar returns a stable-but-living per-series multiplier ≈ 1: a deterministic baseline
// offset (Spread — peer series sharing a formula get distinct, stable values) times a slow
// per-series drift (Wander — value is not frozen). amp sets the magnitude; volume/latency
// metrics use ≈0.18; counts/rates use ≈0.30. Returns 1.0 when no shape engine is wired.
func (c *Construct) seriesVar(w *core.World, now time.Time, key string, amp float64) float64 {
	if w == nil || w.Shape == nil {
		return 1.0
	}
	return w.Shape.Spread(key, amp) * w.Shape.Wander(key, now, amp*0.4)
}

// qualityAmp / volAmp / rateAmp set the per-series Spread+Wander magnitude per metric class.
const (
	qualityAmp = 0.045 // ±~4.5% for quality ratios (near-1.0; kept near target)
	volAmp     = 0.18  // ±~18% for latency / volume (duration_seconds, test_cases_total)
	rateAmp    = 0.30  // ±~30% for raw counts / failure counts
)

// renderMetrics builds the full per-tick batch. Separated so tests can call without a full World.
func (c *Construct) renderMetrics(now time.Time, w *core.World) []promrw.Series {
	// Magnitude factor (B1): env-scoped uses the env-weighted Factor (per-env weekend collapse);
	// aggregate keeps BusinessFactor byte-for-byte (Factor(now,1,false) ≠ BusinessFactor on
	// weekends — 0.2 vs 0.3 — so a blanket swap would regress committed blueprints).
	var bf float64
	if w.Shape == nil {
		bf = 0.8 // safe offline default
	} else if c.envScoped {
		bf = w.Shape.Factor(now, c.weight, c.nonProd)
	} else {
		bf = w.Shape.BusinessFactor(now)
	}

	// Fixed GitLab CI label values (generic; qualification-specific):
	// - project: generic GitLab project path (config-neutral — no customer strings).
	// - ref: "main" branch as primary ref.
	// - kind: "branch" (the most common ref kind per exporter docs).
	// - source: "schedule" (qualification pipelines typically run on schedule).
	// - topics: "" (no topics set in default config).
	// - variables: "" (no pipeline variables by default).
	const (
		project   = "platform/qualification"
		ref       = "main"
		kind      = "branch"
		source    = "schedule"
		topics    = ""
		variables = ""
	)

	for ci, cloud := range c.clouds {
		// Per-pipeline labels (REAL: mvisonneau/gitlab-ci-pipelines-exporter, docs/metrics.md, 2026-06-15).
		pipelineLbls := c.pipelineLabels(project, ref, kind, source, topics, variables, cloud)

		// gitlab_ci_pipeline_status: gauge, one series per status value (0 or 1).
		// The "active" status cycles across clouds using index to vary which cloud is "success".
		// Source: gitlab-ci-pipelines-exporter docs/metrics.md, 2026-06-15 (real metric name + labels).
		for _, st := range pipelineStatuses {
			lbls := copyLabels(pipelineLbls)
			lbls["status"] = st
			val := 0.0
			if st == "success" && ci%2 == 0 {
				val = 1.0
			} else if st == "failed" && ci%2 != 0 {
				val = 1.0
			}
			c.st.Set("gitlab_ci_pipeline_status", lbls, val)
		}

		// gitlab_ci_pipeline_duration_seconds: gauge — duration of most recent pipeline (seconds).
		// Per-series spread+drift so peer clouds emit distinct, drifting durations (not lockstep).
		// Source: gitlab-ci-pipelines-exporter docs/metrics.md, 2026-06-15.
		c.st.Set("gitlab_ci_pipeline_duration_seconds", pipelineLbls, bf*420*c.seriesVar(w, now, "dur_pipeline|"+cloud, volAmp))

		// gitlab_ci_pipeline_run_count: counter — cumulative pipeline execution count.
		// Per-series spread so peer clouds accumulate at distinct rates.
		// Source: gitlab-ci-pipelines-exporter docs/metrics.md, 2026-06-15.
		c.st.Add("gitlab_ci_pipeline_run_count", pipelineLbls, bf*c.seriesVar(w, now, "run|"+cloud, rateAmp))

		// Per-job metrics spread across (stage × job_name):
		for ji, job := range c.jobs {
			stage := c.stages[ji%len(c.stages)]
			jobLbls := c.jobLabels(project, ref, kind, source, topics, variables, cloud, stage, job)

			// gitlab_ci_pipeline_job_status: gauge, one per status value (0 or 1).
			// Source: gitlab-ci-pipelines-exporter docs/metrics.md, 2026-06-15.
			for _, st := range pipelineStatuses {
				lbls := copyLabels(jobLbls)
				lbls["status"] = st
				val := 0.0
				if st == "success" {
					val = 1.0
				}
				c.st.Set("gitlab_ci_pipeline_job_status", lbls, val)
			}

			// gitlab_ci_pipeline_job_duration_seconds: gauge — duration of most recent job (seconds).
			// Keyed on cloud+job so each (cloud, job) series gets a distinct stable-drifting duration.
			// The ji*30 base still differentiates jobs of the same cloud; seriesVar differentiates clouds.
			// Source: gitlab-ci-pipelines-exporter docs/metrics.md, 2026-06-15.
			c.st.Set("gitlab_ci_pipeline_job_duration_seconds", jobLbls, bf*float64(60+ji*30)*c.seriesVar(w, now, "dur_job|"+cloud+"|"+job, volAmp))
		}

		// ── Coined qualification_* suite metrics (SK-48) ──────────────────────────────
		// These signals have no upstream exporter; synthkit coins them for the qualification
		// suite use case. Labels: cloud + optional env (I13: absent → omit).
		for _, suite := range c.suites {
			suiteLbls := c.suiteLabels(cloud, suite)

			// qualification_test_cases_total: gauge — total test cases executed in this suite run.
			// Per-series spread+drift keyed on cloud+suite so peers (different clouds/suites) emit
			// distinct, drifting counts instead of lockstep arithmetic. Base 120 is plausible; volAmp
			// keeps the spread ≈±18% around the baseline.
			// coined (SK-48): no exporter provides this suite signal.
			c.st.Set("qualification_test_cases_total", suiteLbls, bf*120*c.seriesVar(w, now, "cases|"+cloud+"|"+suite, volAmp))

			// qualification_test_failures_total: gauge — number of test failures in this suite run.
			// Per-series spread; rateAmp allows meaningful variation between clouds/suites. Base 2
			// keeps typical values plausible (a small fraction of 120 test cases). bf suppresses
			// failures off-hours when pipelines don't run.
			// coined (SK-48): no exporter provides this suite signal.
			c.st.Set("qualification_test_failures_total", suiteLbls, bf*2*c.seriesVar(w, now, "failures|"+cloud+"|"+suite, rateAmp))

			// qualification_report_generated: gauge — 1 if the qualification report was generated, 0 otherwise.
			// coined (SK-48): no exporter provides this suite signal.
			c.st.Set("qualification_report_generated", suiteLbls, 1.0)
		}
	}

	return c.st.Collect(now)
}

// pipelineLabels builds the pipeline-level label set (no status, no job-level labels).
// Labels follow mvisonneau/gitlab-ci-pipelines-exporter docs/metrics.md (2026-06-15).
// I13: the env label is OMITTED when aggregate (absent dimension omitted).
// cloud is a generic qualifier for the qualification target (not an exporter label).
func (c *Construct) pipelineLabels(project, ref, kind, source, topics, variables, cloud string) map[string]string {
	m := map[string]string{
		"project":   project,
		"ref":       ref,
		"kind":      kind,
		"source":    source,
		"topics":    topics,
		"variables": variables,
		"cloud":     cloud,
	}
	if c.envScoped {
		m["env"] = c.envName
	}
	return m
}

// jobLabels builds the job-level label set (adds stage, job_name, runner_description, tag_list,
// failure_reason per exporter docs). No status — callers add it per status value.
// Source: gitlab-ci-pipelines-exporter docs/metrics.md, 2026-06-15.
func (c *Construct) jobLabels(project, ref, kind, source, topics, variables, cloud, stage, jobName string) map[string]string {
	m := c.pipelineLabels(project, ref, kind, source, topics, variables, cloud)
	m["stage"] = stage
	m["job_name"] = jobName
	m["runner_description"] = "shared-runner"
	m["tag_list"] = ""
	m["failure_reason"] = ""
	return m
}

// suiteLabels builds the label set for coined qualification_* metrics.
// I13: env label omitted when aggregate.
func (c *Construct) suiteLabels(cloud, suite string) map[string]string {
	m := map[string]string{
		"cloud": cloud,
		"suite": suite,
	}
	if c.envScoped {
		m["env"] = c.envName
	}
	return m
}

// copyLabels returns a shallow copy of the label map.
func copyLabels(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// renderLogs returns one Loki stream per cloud with a plausible qualification pipeline run log line.
// Stream labels: {job="qualification-pipeline", cloud=<cloud>} — low-cardinality only (I14/I15).
// High-cardinality fields (pipeline_id, ref) ride in structured metadata.
func (c *Construct) renderLogs(now time.Time) []loki.Stream {
	var out []loki.Stream
	for ci, cloud := range c.clouds {
		streamLabels := map[string]string{
			"job":   "qualification-pipeline",
			"cloud": cloud,
		}
		if c.envScoped {
			streamLabels["env"] = c.envName
		}

		// Plausible qualification pipeline log line (JSON-structured).
		line := fmt.Sprintf(
			`{"timestamp":%q,"level":"info","cloud":%q,"stage":"test","job":"functional-tests","status":"success","duration_s":%d,"pipeline_run":%d}`,
			now.UTC().Format(time.RFC3339),
			cloud,
			420+ci*30,
			1000+ci,
		)
		out = append(out, loki.Stream{
			Labels: streamLabels,
			Lines: []loki.Line{
				{
					T:    now,
					Body: line,
					// High-cardinality pipeline identity in structured metadata (I14).
					Meta: map[string]string{
						"pipeline_id": fmt.Sprintf("%d", 1000+ci),
						"ref":         "main",
					},
				},
			},
		})
	}
	return out
}

// _ ensures durationBuckets is referenced (used for documentation; construct emits gauges not histograms).
var _ = durationBuckets
