// SPDX-License-Identifier: AGPL-3.0-only

// Package neptune emits AWS/Neptune CloudWatch metrics for one declared Neptune cluster.
//
// Kind:     "neptune"
// Scope:    core.ScopeBlueprint
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
// Config:   empty struct (no YAML config knobs; the DB fixture carries identity)
//
// Build requires:
//   - fx.DB    non-nil (name = cluster identifier, engine = "neptune")
//   - fx.Cloud non-nil (account_id, region)
//
// Signal contract (signals/cw.md [slug: cw-neptune]):
//
//	aws_neptune_gremlin_requests_per_sec, aws_neptune_gremlin_client_errors_per_sec,
//	aws_neptune_gremlin_server_errors_per_sec, aws_neptune_total_requests_per_sec,
//	aws_neptune_total_client_errors_per_sec, aws_neptune_total_server_errors_per_sec,
//	aws_neptune_main_request_queue_pending_requests, aws_neptune_cpuutilization,
//	aws_neptune_buffer_cache_hit_ratio, aws_neptune_num_tx_committed, aws_neptune_num_tx_opened,
//	aws_neptune_num_tx_rolled_back, aws_neptune_cluster_replica_lag_maximum.
//	All five stat suffixes emitted per base.
//
// Dimensions: dimension_DBClusterIdentifier, dimension_Role ∈ {WRITER,READER}.
package neptune

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// Config carries no YAML knobs — the DB fixture (engine=neptune) carries identity.
type Config struct{}

// Construct is the per-instance Neptune renderer. Not exported; callers use Build.
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
		return nil, fmt.Errorf("neptune: Build requires a non-nil fixture.Cloud")
	}
	if fx.DB == nil {
		return nil, fmt.Errorf("neptune: Build requires a non-nil fixture.DB")
	}
	return &Construct{db: fx.DB, cloud: fx.Cloud, env: fx.Env, st: state.NewState()}, nil
}

func (c *Construct) Kind() string                { return "neptune" }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics.
// All series use state.Set (per-period gauges, ARCHITECTURE I5 — NEVER state.Add).
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	bf := w.Shape.BusinessFactor(now)

	for _, role := range []string{"WRITER", "READER"} {
		lbls := c.baseLabels(map[string]string{
			"dimension_DBClusterIdentifier": c.db.Name,
			"dimension_Role":                role,
		})
		// Base names sourced VERBATIM from signals/cw.md [slug: cw-neptune].
		setGauge(c.st, "aws_neptune_gremlin_requests_per_sec", lbls, 50+200*bf)
		setGauge(c.st, "aws_neptune_gremlin_client_errors_per_sec", lbls, bf*0.5)
		setGauge(c.st, "aws_neptune_gremlin_server_errors_per_sec", lbls, bf*0.1)
		setGauge(c.st, "aws_neptune_total_requests_per_sec", lbls, 60+240*bf)
		setGauge(c.st, "aws_neptune_total_client_errors_per_sec", lbls, bf*0.6)
		setGauge(c.st, "aws_neptune_total_server_errors_per_sec", lbls, bf*0.1)
		setGauge(c.st, "aws_neptune_main_request_queue_pending_requests", lbls, bf*2)
		setGauge(c.st, "aws_neptune_cpuutilization", lbls, 15+25*bf)
		setGauge(c.st, "aws_neptune_buffer_cache_hit_ratio", lbls, 98-3*bf)
		setGauge(c.st, "aws_neptune_num_tx_committed", lbls, 40+160*bf)
		setGauge(c.st, "aws_neptune_num_tx_opened", lbls, 5+15*bf)
		setGauge(c.st, "aws_neptune_num_tx_rolled_back", lbls, bf*1)
		setGauge(c.st, "aws_neptune_cluster_replica_lag_maximum", lbls, 10+50*bf)
	}
	// Info series — no stat suffix, gauge=1.
	c.st.Set("aws_neptune_info", c.baseLabels(map[string]string{
		"dimension_DBClusterIdentifier": c.db.Name,
	}), 1)
	return w.Metrics.Write(ctx, c.st.Collect(now))
}

// baseLabels builds the full CloudWatch label set for one series.
func (c *Construct) baseLabels(extra map[string]string) map[string]string {
	m := map[string]string{
		"account_id": c.cloud.AccountID,
		"region":     c.cloud.Region,
		"namespace":  "AWS/Neptune",
		"job":        "cloud/aws/neptune",
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

// setGauge emits the full five-stat CW family for one Neptune per-period metric.
func setGauge(st *state.State, name string, lbls map[string]string, v float64) {
	cw.EmitStats(st, name, lbls, cw.StatSet{Sum: v, Average: v, Maximum: v, Minimum: v, SampleCount: 60})
}
