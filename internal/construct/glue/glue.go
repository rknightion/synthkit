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
package glue

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// Config is the YAML config struct for the glue construct.
type Config struct {
	// Jobs is the list of AWS Glue job names to emit.
	// If empty, one synthetic job derived from fx.Seed is used.
	Jobs []string `yaml:"jobs"`
}

// Construct is the Glue renderer. Not exported; callers use Build.
type Construct struct {
	jobs      []string
	accountID string
	region    string
	st        *state.State
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

	jobs := c.Jobs
	if len(jobs) == 0 {
		seed := fx.Seed
		if seed == "" {
			seed = "default"
		}
		jobs = []string{"glue-job-" + fixture.HexID(seed, 6, "glue")}
	}

	return &Construct{
		jobs:      jobs,
		accountID: fx.Cloud.AccountID,
		region:    fx.Cloud.Region,
		st:        state.NewState(),
	}, nil
}

func (c *Construct) Kind() string                { return "glue" }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics.
// All series use state.Set (per-period gauges, ARCHITECTURE I5 — NEVER state.Add).
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	bf := w.Shape.BusinessFactor(now)

	for _, jobName := range c.jobs {
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

	return w.Metrics.Write(ctx, c.st.Collect(now))
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
