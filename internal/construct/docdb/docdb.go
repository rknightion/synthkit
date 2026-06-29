// SPDX-License-Identifier: AGPL-3.0-only

// Package docdb emits AWS/DocDB CloudWatch metrics for one declared DocumentDB cluster.
//
// Kind:     "docdb"
// Scope:    core.ScopeBlueprint
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
// Config:   empty struct (no YAML config knobs; the DB fixture carries identity)
//
// Build requires:
//   - fx.DB    non-nil (name = cluster identifier, engine = "docdb")
//   - fx.Cloud non-nil (account_id, region)
//
// Signal contract (signals/cw.md [slug: cw-docdb]):
//
//	aws_docdb_cpuutilization, aws_docdb_database_connections, aws_docdb_freeable_memory,
//	aws_docdb_read_latency, aws_docdb_write_latency, aws_docdb_read_iops, aws_docdb_write_iops,
//	aws_docdb_buffer_cache_hit_ratio, aws_docdb_opcounters_{insert,query,update,delete,getmore,command},
//	aws_docdb_documents_{inserted,returned,updated,deleted}, aws_docdb_swap_usage.
//	All five stat suffixes emitted per base.
//
// Dimensions: dimension_DBClusterIdentifier, dimension_Role ∈ {WRITER,READER}.
package docdb

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// Config carries no YAML knobs — the DB fixture (engine=docdb) carries identity.
type Config struct{}

// Construct is the per-instance DocDB renderer. Not exported; callers use Build.
type Construct struct {
	db    *fixture.DB
	cloud *fixture.Cloud
	env   *fixture.Env
	st    *state.State
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates fixtures and returns a ready core.Construct instance.
func Build(cfgAny any, fx *fixture.Set) (core.Construct, error) {
	if fx == nil || fx.Cloud == nil {
		return nil, fmt.Errorf("docdb: Build requires a non-nil fixture.Cloud")
	}
	if fx.DB == nil {
		return nil, fmt.Errorf("docdb: Build requires a non-nil fixture.DB")
	}
	return &Construct{db: fx.DB, cloud: fx.Cloud, env: fx.Env, st: state.NewState()}, nil
}

func (c *Construct) Kind() string                { return "docdb" }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics.
// All series use state.Set (per-period gauges, ARCHITECTURE I5 — NEVER state.Add).
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	bf := w.Shape.BusinessFactor(now)

	// Failure-mode amplifiers (AxisDatabase, scoped to db.Name — the SAME scope key any
	// future dbo11y construct would use for coherent incident propagation).
	connMult := w.Shape.FailFactor(now, "connection_saturation", c.db.Name, 3.0)
	latMult := w.Shape.FailFactor(now, "slow_query_storm", c.db.Name, 4.0)

	for _, role := range []string{"WRITER", "READER"} {
		lbls := c.baseLabels(map[string]string{
			"dimension_DBClusterIdentifier": c.db.Name,
			"dimension_Role":                role,
		})
		// Base names sourced VERBATIM from signals/cw.md [slug: cw-docdb].
		setGauge(c.st, "aws_docdb_cpuutilization", lbls, 18+12*bf)
		setGauge(c.st, "aws_docdb_database_connections", lbls, (20+80*bf)*connMult)
		setGauge(c.st, "aws_docdb_freeable_memory", lbls, 8e9-2e9*bf)
		setGauge(c.st, "aws_docdb_buffer_cache_hit_ratio", lbls, 99-2*bf)
		setGauge(c.st, "aws_docdb_read_latency", lbls, (0.001+0.003*bf)*latMult)  // seconds
		setGauge(c.st, "aws_docdb_write_latency", lbls, (0.002+0.005*bf)*latMult) // seconds
		setGauge(c.st, "aws_docdb_read_iops", lbls, 100+900*bf)
		setGauge(c.st, "aws_docdb_write_iops", lbls, 50+450*bf)
		setGauge(c.st, "aws_docdb_swap_usage", lbls, 0)
		for _, op := range []string{"insert", "query", "update", "delete", "getmore", "command"} {
			setGauge(c.st, "aws_docdb_opcounters_"+op, lbls, 10+90*bf)
		}
		for _, d := range []string{"inserted", "returned", "updated", "deleted"} {
			setGauge(c.st, "aws_docdb_documents_"+d, lbls, 10+90*bf)
		}
	}
	// Info series — no stat suffix, gauge=1.
	c.st.Set("aws_docdb_info", c.baseLabels(map[string]string{
		"dimension_DBClusterIdentifier": c.db.Name,
	}), 1)
	return w.Metrics.Write(ctx, c.st.Collect(now))
}

// baseLabels builds the full CloudWatch label set for one series.
// extra labels (dimension_*, tag_*) are merged in. Absent dimensions are omitted (I13).
func (c *Construct) baseLabels(extra map[string]string) map[string]string {
	m := map[string]string{
		"account_id": c.cloud.AccountID,
		"region":     c.cloud.Region,
		"namespace":  "AWS/DocDB",
		"job":        "cloud/aws/docdb",
		"name": fmt.Sprintf("arn:aws:rds:%s:%s:cluster:%s",
			c.cloud.Region, c.cloud.AccountID, c.db.Name),
	}
	for k, v := range extra {
		if v != "" { // I13: absent dimension OMITTED
			m[k] = v
		}
	}
	return m
}

// setGauge emits the full five-stat CW family for one DocDB per-period metric.
// All values use state.Set (per-period GAUGE — NEVER Add; ARCHITECTURE I5).
func setGauge(st *state.State, name string, lbls map[string]string, v float64) {
	cw.EmitStats(st, name, lbls, cw.StatSet{Sum: v, Average: v, Maximum: v, Minimum: v, SampleCount: 60})
}
