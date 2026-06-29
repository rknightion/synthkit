// SPDX-License-Identifier: AGPL-3.0-only

// Package mwaa emits AWS/MWAA and AmazonMWAA CloudWatch metrics for one MWAA environment.
//
// Kind:     "mwaa"
// Scope:    core.ScopeBlueprint
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
// Config:   Environments []string — one series-set per environment name
//
// Build requires:
//   - fx.Cloud non-nil (account_id, region)
//
// Identity is carried in Config (Environments) + fx.Cloud (account/region).
// NO fx.DB required.
//
// Signal contract (signals/cw.md [slug: cw-mwaa]):
//
//	AWS/MWAA → aws_mwaa_* (17 base): infra metrics for workers, scheduler, Aurora-metadata-DB.
//	AmazonMWAA → aws_amazonmwaa_* (31 base): Airflow StatsD operational metrics.
//	All five stat suffixes emitted per base.
//
// ⚠ _p90/_p95/_p99 suffixes are OPERATOR-ADDED extended statistics — NOT default.
//
// ⚠ aws_amazonmwaa_* series are sparse/event-driven in production; synthkit emits
// representative values so dashboards remain non-empty.
package mwaa

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// Config is the YAML config struct for the mwaa construct.
type Config struct {
	// Environments is the list of MWAA environment names to emit.
	// If empty, one synthetic environment derived from fx.Seed is used.
	Environments []string `yaml:"environments"`
}

// Construct is the MWAA renderer. Not exported; callers use Build.
type Construct struct {
	environments []string
	accountID    string
	region       string
	st           *state.State
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates cfg and fx, resolves environments, and returns a ready core.Construct instance.
func Build(cfgAny any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfgAny.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("mwaa: Build called with %T, want *Config", cfgAny)
	}
	if fx == nil || fx.Cloud == nil {
		return nil, fmt.Errorf("mwaa: Build requires a non-nil fixture.Cloud")
	}

	environments := c.Environments
	if len(environments) == 0 {
		seed := fx.Seed
		if seed == "" {
			seed = "default"
		}
		environments = []string{"mwaa-" + fixture.HexID(seed, 6, "mwaa")}
	}

	return &Construct{
		environments: environments,
		accountID:    fx.Cloud.AccountID,
		region:       fx.Cloud.Region,
		st:           state.NewState(),
	}, nil
}

func (c *Construct) Kind() string                { return "mwaa" }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics.
// All series use state.Set (per-period gauges, ARCHITECTURE I5 — NEVER state.Add).
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	bf := w.Shape.BusinessFactor(now)

	for _, envName := range c.environments {
		c.emitMWAA(bf, envName)
		c.emitAmazonMWAA(bf, envName)
	}

	return w.Metrics.Write(ctx, c.st.Collect(now))
}

// emitMWAA emits the AWS/MWAA namespace series (17 base, infra metrics).
// Base names sourced VERBATIM from signals/cw.md [slug: cw-mwaa].
func (c *Construct) emitMWAA(bf float64, envName string) {
	st := c.st

	// Env-level metrics (dimension_Environment only).
	envLbls := c.mwaaLabels(envName, nil)
	setGauge(st, "aws_mwaa_active_connection_count", envLbls, 5+15*bf)
	setGauge(st, "aws_mwaa_approximate_age_of_oldest_task", envLbls, bf*30)
	setGauge(st, "aws_mwaa_queued_tasks", envLbls, bf*3)
	setGauge(st, "aws_mwaa_running_tasks", envLbls, 2+8*bf)

	// Cluster-level metrics (dimension_Environment + dimension_Cluster).
	for _, cluster := range []string{"AdditionalWorker", "BaseWorker", "Scheduler", "WebServer"} {
		clLbls := c.mwaaLabels(envName, map[string]string{"dimension_Cluster": cluster})
		setGauge(st, "aws_mwaa_cpuutilization", clLbls, 20+30*bf)
		setGauge(st, "aws_mwaa_memory_utilization", clLbls, 40+20*bf)
	}

	// Database-level metrics (dimension_Environment + dimension_DatabaseRole).
	for _, role := range []string{"READER", "WRITER"} {
		dbLbls := c.mwaaLabels(envName, map[string]string{"dimension_DatabaseRole": role})
		setGauge(st, "aws_mwaa_database_connections", dbLbls, 5+10*bf)
		setGauge(st, "aws_mwaa_disk_queue_depth", dbLbls, bf*0.5)
		setGauge(st, "aws_mwaa_freeable_memory", dbLbls, 4e9-1e9*bf)
		setGauge(st, "aws_mwaa_read_iops", dbLbls, 50+100*bf)
		setGauge(st, "aws_mwaa_write_iops", dbLbls, 20+50*bf)
		setGauge(st, "aws_mwaa_read_latency", dbLbls, 0.001+0.002*bf)  // seconds
		setGauge(st, "aws_mwaa_write_latency", dbLbls, 0.002+0.003*bf) // seconds
		setGauge(st, "aws_mwaa_read_throughput", dbLbls, 1e6+2e6*bf)
		setGauge(st, "aws_mwaa_write_throughput", dbLbls, 0.5e6+1e6*bf)
		setGauge(st, "aws_mwaa_network_receive_throughput", dbLbls, 0.5e6+0.5e6*bf)
		setGauge(st, "aws_mwaa_network_transmit_throughput", dbLbls, 0.3e6+0.3e6*bf)
	}

	// Info series.
	st.Set("aws_mwaa_info", c.mwaaLabels(envName, nil), 1)
}

// emitAmazonMWAA emits the AmazonMWAA namespace series (31 base, Airflow StatsD operational).
// Base names sourced VERBATIM from signals/cw.md [slug: cw-mwaa].
func (c *Construct) emitAmazonMWAA(bf float64, envName string) {
	st := c.st

	// Most metrics carry dimension_Environment + dimension_Function (or dimension_Pool, etc).
	// For synthkit we emit representative Scheduler-function series.
	schLbls := c.amazonMWAALabels(envName, map[string]string{
		"dimension_Function": "Scheduler",
	})
	execLbls := c.amazonMWAALabels(envName, map[string]string{
		"dimension_Function": "Executor",
	})
	dagLbls := c.amazonMWAALabels(envName, map[string]string{
		"dimension_Function": "DAG Processing",
	})
	poolLbls := c.amazonMWAALabels(envName, map[string]string{
		"dimension_Pool": "default_pool",
	})
	dagFileLbls := c.amazonMWAALabels(envName, map[string]string{
		"dimension_DAG_Filename": "etl_pipeline",
	})

	// Base names sourced VERBATIM from signals/cw.md [slug: cw-mwaa].
	setGauge(st, "aws_amazonmwaa_scheduler_heartbeat", schLbls, 1)
	setGauge(st, "aws_amazonmwaa_scheduler_loop_duration", schLbls, 0.1+0.5*bf)
	setGauge(st, "aws_amazonmwaa_dag_bag_size", dagLbls, 10+5*bf)
	setGauge(st, "aws_amazonmwaa_total_parse_time", dagLbls, 0.5+2*bf)
	setGauge(st, "aws_amazonmwaa_dagfile_processing_last_duration", dagFileLbls, 0.3+1*bf)
	setGauge(st, "aws_amazonmwaa_dagfile_processing_last_num_of_db_queries", dagFileLbls, 5+10*bf)
	setGauge(st, "aws_amazonmwaa_dagfile_processing_last_run_seconds_ago", dagFileLbls, 60+30*bf)
	setGauge(st, "aws_amazonmwaa_file_path_queue_size", dagLbls, 5+5*bf)
	setGauge(st, "aws_amazonmwaa_file_path_queue_update_count", dagLbls, bf*2)
	setGauge(st, "aws_amazonmwaa_import_errors", dagLbls, 0)
	setGauge(st, "aws_amazonmwaa_processes", schLbls, 3+2*bf)
	setGauge(st, "aws_amazonmwaa_queued_tasks", execLbls, bf*3)
	setGauge(st, "aws_amazonmwaa_running_tasks", execLbls, 2+8*bf)
	setGauge(st, "aws_amazonmwaa_tasks_executable", schLbls, 2+6*bf)
	setGauge(st, "aws_amazonmwaa_tasks_starving", schLbls, 0)
	setGauge(st, "aws_amazonmwaa_orphaned", schLbls, 0)
	setGauge(st, "aws_amazonmwaa_orphaned_tasks_adopted", schLbls, 0)
	setGauge(st, "aws_amazonmwaa_orphaned_tasks_cleared", schLbls, 0)
	setGauge(st, "aws_amazonmwaa_open_slots", execLbls, 10-5*bf)
	setGauge(st, "aws_amazonmwaa_job_end", schLbls, bf*1)
	setGauge(st, "aws_amazonmwaa_triggers_running", schLbls, bf*2)
	setGauge(st, "aws_amazonmwaa_triggerer_heartbeat", schLbls, 1)
	setGauge(st, "aws_amazonmwaa_celery_worker_heartbeat", schLbls, 1)
	setGauge(st, "aws_amazonmwaa_critical_section_busy", schLbls, 0)
	setGauge(st, "aws_amazonmwaa_critical_section_duration", schLbls, 0.01+0.05*bf)
	setGauge(st, "aws_amazonmwaa_critical_section_query_duration", schLbls, 0.005+0.02*bf)
	setGauge(st, "aws_amazonmwaa_pool_open_slots", poolLbls, 128-80*bf)
	setGauge(st, "aws_amazonmwaa_pool_running_slots", poolLbls, bf*30)
	setGauge(st, "aws_amazonmwaa_pool_queued_slots", poolLbls, bf*10)
	setGauge(st, "aws_amazonmwaa_pool_scheduled_slots", poolLbls, bf*5)
	setGauge(st, "aws_amazonmwaa_pool_deferred_slots", poolLbls, 0)

	// Info series.
	st.Set("aws_amazonmwaa_info", c.amazonMWAALabels(envName, nil), 1)
}

// mwaaLabels builds labels for the AWS/MWAA namespace.
func (c *Construct) mwaaLabels(envName string, extra map[string]string) map[string]string {
	m := map[string]string{
		"account_id":            c.accountID,
		"region":                c.region,
		"namespace":             "AWS/MWAA",
		"job":                   "cloud/aws/mwaa",
		"name":                  fmt.Sprintf("arn:aws:airflow:%s:%s:environment/%s", c.region, c.accountID, envName),
		"dimension_Environment": envName,
	}
	for k, v := range extra {
		if v != "" { // I13: absent dimension OMITTED
			m[k] = v
		}
	}
	return m
}

// amazonMWAALabels builds labels for the AmazonMWAA namespace.
func (c *Construct) amazonMWAALabels(envName string, extra map[string]string) map[string]string {
	m := map[string]string{
		"account_id":            c.accountID,
		"region":                c.region,
		"namespace":             "AmazonMWAA",
		"job":                   "cloud/aws/amazonmwaa",
		"name":                  fmt.Sprintf("arn:aws:airflow:%s:%s:environment/%s", c.region, c.accountID, envName),
		"dimension_Environment": envName,
	}
	for k, v := range extra {
		if v != "" { // I13: absent dimension OMITTED
			m[k] = v
		}
	}
	return m
}

// setGauge emits the full five-stat CW family for one MWAA per-period metric.
func setGauge(st *state.State, name string, lbls map[string]string, v float64) {
	cw.EmitStats(st, name, lbls, cw.StatSet{Sum: v, Average: v, Maximum: v, Minimum: v, SampleCount: 60})
}
