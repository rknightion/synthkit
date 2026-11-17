// SPDX-License-Identifier: AGPL-3.0-only

// Package glue emits Glue CloudWatch metrics for declared AWS Glue ETL jobs.
//
// Kind:     "glue"
// Scope:    core.ScopeBlueprint
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
// Config:   Jobs []string — one series-set per job name
//
// Build requires:
//   - fx.Cloud non-nil (account_id, region)
//
// Identity is carried in Config (Jobs) + fx.Cloud (account/region).
// NO fx.DB required.
//
// Signal contract (signals/cw.md [slug: cw-glue]):
//
//	aws_glue_driver_aggregate_{bytes_read, records_read, num_completed_tasks,
//	num_failed_tasks, num_killed_tasks, num_completed_stages, elapsed_time,
//	shuffle_bytes_written, shuffle_local_bytes_read} — delta aggregates (Type=count)
//
//	aws_glue_driver_block_manager_disk_disk_space_used_mb
//	aws_glue_driver_jvm_heap_usage, aws_glue_all_jvm_heap_usage
//	aws_glue_driver_jvm_heap_used, aws_glue_all_jvm_heap_used
//	aws_glue_driver_s3_filesystem_read_bytes, aws_glue_all_s3_filesystem_read_bytes
//	aws_glue_driver_s3_filesystem_write_bytes, aws_glue_all_s3_filesystem_write_bytes
//	aws_glue_driver_system_cpu_system_load, aws_glue_all_system_cpu_system_load — absolute gauges (Type=gauge)
//	All five stat suffixes emitted per base.
//
// ⚠ Namespace label is the LITERAL string "Glue" (no AWS/ prefix) — this DIVERGES from the
// Prometheus metric prefix "aws_glue_" and from the job label "cloud/aws/glue". baseLabels
// hardcodes "namespace": "Glue" explicitly (never auto-derived).
//
// ⚠ No CloudWatch metric for Glue job SUCCEEDED/FAILED — outcome is EventBridge/API only.
//
// Declared pipelines (internal/pipelinerun, injected through Config.UsePipelines): a job that is a
// glue stage's resource is driven by the shared run simulation instead of the business-hours
// formulas. Activity is the stage's execution seconds in the 60 s window: run-time aggregates and
// executor gauges scale with it, so a stalled stage reads zero completed tasks; a failed stage
// execution reports spark.task.maxFailures (default 4) failed tasks. Idle windows report zero so the
// series set is time-invariant. Jobs that are not pipeline stages keep the legacy formulas.
package glue

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/pipelinerun"
	"github.com/rknightion/synthkit/internal/state"
)

// Config is the YAML config struct for the glue construct.
type Config struct {
	// Jobs is the list of AWS Glue job names to emit.
	// If empty, one synthetic job derived from fx.Seed is used.
	Jobs []string `yaml:"jobs"`

	// Pipelines are the environment's declared pipelines, set by the composition root from the
	// blueprint's single cloud.pipelines declaration (never decoded from this block).
	Pipelines []pipelinerun.Pipeline `yaml:"-"`
}

// UsePipelines implements pipelinerun.Consumer.
func (c *Config) UsePipelines(ps []pipelinerun.Pipeline) { c.Pipelines = ps }

// Construct is the Glue renderer. Not exported; callers use Build.
type Construct struct {
	jobs       []string
	accountID  string
	region     string
	cloud      *fixture.Cloud
	exportMode string
	st         *state.State
	tracker    *pipelinerun.Tracker
	stageJobs  map[string]bool // jobs driven by a declared pipeline stage
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates cfg and fx, resolves jobs, and returns a ready core.Construct instance.
func Build(cfgAny any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfgAny.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("glue: Build called with %T, want *Config", cfgAny)
	}
	if fx == nil || fx.Cloud == nil {
		return nil, fmt.Errorf("glue: Build requires a non-nil fixture.Cloud")
	}

	tracker, err := pipelinerun.NewTracker(fx.Seed, c.Pipelines, 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("glue: %w", err)
	}
	jobs := append([]string(nil), c.Jobs...)
	seen := map[string]bool{}
	for _, j := range jobs {
		seen[j] = true
	}
	stageJobs := map[string]bool{}
	for _, p := range c.Pipelines {
		for _, s := range p.Stages {
			if s.Service != pipelinerun.ServiceGlue {
				continue
			}
			stageJobs[s.Resource] = true
			if !seen[s.Resource] {
				seen[s.Resource] = true
				jobs = append(jobs, s.Resource)
			}
		}
	}
	if len(jobs) == 0 {
		seed := fx.Seed
		if seed == "" {
			seed = "default"
		}
		jobs = []string{"glue-job-" + fixture.HexID(seed, 6, "glue")}
	}

	return &Construct{
		jobs:       jobs,
		accountID:  fx.Cloud.AccountID,
		region:     fx.Cloud.Region,
		cloud:      fx.Cloud,
		exportMode: fx.Cloud.CloudWatchExportMode(),
		st:         state.NewState(),
		tracker:    tracker,
		stageJobs:  stageJobs,
	}, nil
}

func (c *Construct) Kind() string { return "glue" }
func (c *Construct) Signals() []core.SignalClass {
	if c.exportMode == "otlp" {
		return []core.SignalClass{core.OTLPMetrics}
	}
	return []core.SignalClass{core.Metrics}
}
func (c *Construct) Interval() time.Duration { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics.
// All series use state.Set (per-period gauges, ARCHITECTURE I5 — NEVER state.Add).
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	bf := w.Shape.BusinessFactor(now)
	var win pipelinerun.Window
	if !c.tracker.Empty() {
		win = c.tracker.Advance(now, w.Shape.Eval)
	}

	for _, jobName := range c.jobs {
		if c.stageJobs[jobName] {
			c.emitStageJob(w, jobName, win.StagesFor(pipelinerun.ServiceGlue, jobName))
			continue
		}
		// delta aggregates (dimension_Type=count)
		countLbls := c.baseLabels(jobName, "count")
		// Base names sourced VERBATIM from signals/cw.md [slug: cw-glue].
		setGauge(c.st, "aws_glue_driver_aggregate_bytes_read", countLbls, 100e6+400e6*bf)
		setGauge(c.st, "aws_glue_driver_aggregate_records_read", countLbls, 1e6+4e6*bf)
		setGauge(c.st, "aws_glue_driver_aggregate_num_completed_tasks", countLbls, 50+200*bf)
		setGauge(c.st, "aws_glue_driver_aggregate_num_failed_tasks", countLbls, bf*0.5)
		setGauge(c.st, "aws_glue_driver_aggregate_num_killed_tasks", countLbls, 0)
		setGauge(c.st, "aws_glue_driver_aggregate_num_completed_stages", countLbls, 5+20*bf)
		setGauge(c.st, "aws_glue_driver_aggregate_elapsed_time", countLbls, 30000+120000*bf) // ms
		setGauge(c.st, "aws_glue_driver_aggregate_shuffle_bytes_written", countLbls, 10e6+40e6*bf)
		setGauge(c.st, "aws_glue_driver_aggregate_shuffle_local_bytes_read", countLbls, 8e6+32e6*bf)

		// absolute gauges (dimension_Type=gauge)
		gaugeLbls := c.baseLabels(jobName, "gauge")
		setGauge(c.st, "aws_glue_driver_block_manager_disk_disk_space_used_mb", gaugeLbls, 100+200*bf)
		setGauge(c.st, "aws_glue_driver_jvm_heap_usage", gaugeLbls, 0.3+0.4*bf) // fraction 0–1
		setGauge(c.st, "aws_glue_all_jvm_heap_usage", gaugeLbls, 0.3+0.4*bf)
		setGauge(c.st, "aws_glue_driver_jvm_heap_used", gaugeLbls, 1.5e9+2e9*bf) // bytes
		setGauge(c.st, "aws_glue_all_jvm_heap_used", gaugeLbls, 1.5e9+2e9*bf)
		setGauge(c.st, "aws_glue_driver_s3_filesystem_read_bytes", gaugeLbls, 50e6+200e6*bf)
		setGauge(c.st, "aws_glue_all_s3_filesystem_read_bytes", gaugeLbls, 60e6+240e6*bf)
		setGauge(c.st, "aws_glue_driver_s3_filesystem_write_bytes", gaugeLbls, 20e6+80e6*bf)
		setGauge(c.st, "aws_glue_all_s3_filesystem_write_bytes", gaugeLbls, 25e6+100e6*bf)
		setGauge(c.st, "aws_glue_driver_system_cpu_system_load", gaugeLbls, 0.2+0.5*bf) // fraction
		setGauge(c.st, "aws_glue_all_system_cpu_system_load", gaugeLbls, 0.2+0.5*bf)

		// Info series — no stat suffix, gauge=1.
		c.st.Set("aws_glue_info", gaugeLbls, 1)
	}

	batch := c.st.Collect(now)
	if c.exportMode == "otlp" {
		_, err := cw.WriteMetricStreams(ctx, w.OTLPMetrics, c.cloud, batch)
		return err
	}
	return w.Metrics.Write(ctx, batch)
}

// Per running-minute rates for a pipeline-driven job: the midpoints of the legacy business-hours
// ranges above (one executing run ≈ the legacy bf=0.5 job), so pipeline and non-pipeline jobs
// read on the same scale.
const (
	runBytesReadPerMin      = 300e6
	runRecordsReadPerMin    = 3e6
	runCompletedTasksPerMin = 150
	runCompletedStagesPer   = 15
	runShuffleWrittenPerMin = 30e6
	runShuffleLocalPerMin   = 24e6
	runDriverS3ReadPerMin   = 150e6
	runAllS3ReadPerMin      = 180e6
	runDriverS3WritePerMin  = 60e6
	runAllS3WritePerMin     = 75e6
	// sparkTaskMaxFailures is Spark's spark.task.maxFailures default: a task fails this many
	// times before the stage, and so the Glue job run, aborts.
	sparkTaskMaxFailures = 4
)

// emitStageJob renders one pipeline-driven job from its stage windows.
func (c *Construct) emitStageJob(w *core.World, jobName string, stages []pipelinerun.StageWindow) {
	var busy float64
	failed := 0
	for _, s := range stages {
		busy += s.BusySec
		failed += s.Failed
	}
	a := busy / c.Interval().Seconds() // concurrently executing runs, averaged over the window
	occ := a
	if occ > 1 {
		occ = 1
	}
	sp := w.Shape.Spread("glue/"+jobName, 0.1)

	countLbls := c.baseLabels(jobName, "count")
	setGauge(c.st, "aws_glue_driver_aggregate_bytes_read", countLbls, runBytesReadPerMin*a*sp)
	setGauge(c.st, "aws_glue_driver_aggregate_records_read", countLbls, runRecordsReadPerMin*a*sp)
	setGauge(c.st, "aws_glue_driver_aggregate_num_completed_tasks", countLbls, runCompletedTasksPerMin*a*sp)
	setGauge(c.st, "aws_glue_driver_aggregate_num_failed_tasks", countLbls, float64(sparkTaskMaxFailures*failed))
	setGauge(c.st, "aws_glue_driver_aggregate_num_killed_tasks", countLbls, 0)
	setGauge(c.st, "aws_glue_driver_aggregate_num_completed_stages", countLbls, runCompletedStagesPer*a)
	setGauge(c.st, "aws_glue_driver_aggregate_elapsed_time", countLbls, busy*1000) // ms
	setGauge(c.st, "aws_glue_driver_aggregate_shuffle_bytes_written", countLbls, runShuffleWrittenPerMin*a*sp)
	setGauge(c.st, "aws_glue_driver_aggregate_shuffle_local_bytes_read", countLbls, runShuffleLocalPerMin*a*sp)

	gaugeLbls := c.baseLabels(jobName, "gauge")
	setGauge(c.st, "aws_glue_driver_block_manager_disk_disk_space_used_mb", gaugeLbls, 200*occ)
	setGauge(c.st, "aws_glue_driver_jvm_heap_usage", gaugeLbls, 0.5*occ) // fraction 0–1
	setGauge(c.st, "aws_glue_all_jvm_heap_usage", gaugeLbls, 0.5*occ)
	setGauge(c.st, "aws_glue_driver_jvm_heap_used", gaugeLbls, 2.5e9*occ) // bytes
	setGauge(c.st, "aws_glue_all_jvm_heap_used", gaugeLbls, 2.5e9*occ)
	setGauge(c.st, "aws_glue_driver_s3_filesystem_read_bytes", gaugeLbls, runDriverS3ReadPerMin*a*sp)
	setGauge(c.st, "aws_glue_all_s3_filesystem_read_bytes", gaugeLbls, runAllS3ReadPerMin*a*sp)
	setGauge(c.st, "aws_glue_driver_s3_filesystem_write_bytes", gaugeLbls, runDriverS3WritePerMin*a*sp)
	setGauge(c.st, "aws_glue_all_s3_filesystem_write_bytes", gaugeLbls, runAllS3WritePerMin*a*sp)
	setGauge(c.st, "aws_glue_driver_system_cpu_system_load", gaugeLbls, 0.45*occ) // fraction
	setGauge(c.st, "aws_glue_all_system_cpu_system_load", gaugeLbls, 0.45*occ)

	c.st.Set("aws_glue_info", gaugeLbls, 1)
}

// baseLabels builds the full CloudWatch label set for one Glue series.
// ⚠ namespace is the LITERAL string "Glue" — NOT "AWS/Glue".
func (c *Construct) baseLabels(jobName, dimType string) map[string]string {
	m := map[string]string{
		"account_id":         c.accountID,
		"region":             c.region,
		"namespace":          "Glue", // ⚠ literal "Glue" — no AWS/ prefix (signals/cw.md [slug: cw-glue])
		"job":                "cloud/aws/glue",
		"name":               fmt.Sprintf("arn:aws:glue:%s:%s:job/%s", c.region, c.accountID, jobName),
		"dimension_JobName":  jobName,
		"dimension_JobRunId": "ALL",
	}
	if dimType != "" { // I13: absent dimension OMITTED
		m["dimension_Type"] = dimType
	}
	return m
}

// setGauge emits the full five-stat CW family for one Glue per-period metric.
func setGauge(st *state.State, name string, lbls map[string]string, v float64) {
	cw.EmitStats(st, name, lbls, cw.StatSet{Sum: v, Average: v, Maximum: v, Minimum: v, SampleCount: 60})
}
