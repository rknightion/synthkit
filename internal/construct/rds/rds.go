// SPDX-License-Identifier: AGPL-3.0-only

// Package rds emits AWS/RDS CloudWatch metrics for one declared database instance.
//
// Kind:     "rds"
// Scope:    core.ScopeBlueprint (CW families are blueprint-scoped — ARCHITECTURE §5)
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
// Config:   empty struct (no YAML config knobs; the DB fixture carries everything)
//
// Build requires:
//   - fx.DB    non-nil (the database instance to render)
//   - fx.Cloud non-nil (account_id, region, vpc_id)
//
// ONE construct instance renders ONE database. The composition root instantiates one
// rds construct per declared database entry in the blueprint.
//
// Signal contract (signals/cw.md [slug: cw-rds]):
//
//	Universal metrics (all engines):
//	  aws_rds_cpuutilization
//	  aws_rds_database_connections
//	  aws_rds_freeable_memory
//	  aws_rds_free_storage_space
//	  aws_rds_read_iops / aws_rds_write_iops
//	  aws_rds_read_latency / aws_rds_write_latency      (seconds)
//	  aws_rds_read_throughput / aws_rds_write_throughput
//	  aws_rds_network_receive_throughput / aws_rds_network_transmit_throughput
//	  aws_rds_disk_queue_depth
//	  aws_rds_swap_usage
//	  aws_rds_burst_balance
//
//	Postgres-only:
//	  aws_rds_transaction_logs_disk_usage
//	  aws_rds_transaction_logs_generation
//	  aws_rds_maximum_used_transaction_ids
//	  aws_rds_replication_slot_disk_usage
//
// All five stat suffixes are emitted for every metric family:
//
//	_sum | _average | _maximum | _minimum | _sample_count
//
// per the CloudWatch naming law (ARCHITECTURE I6).
//
// CloudWatch _sum series are per-period GAUGES — never rate() (ARCHITECTURE I5).
// state.Set is used for every series (instantaneous gauge semantics match CW per-period).
//
// Connection/IOPS values scale with w.Shape.Factor(now, env.Weight, env.NonProd).
//
// DB failure modes (AxisDatabase, scoped to db.Name — see failuremodes.go) modulate the matching
// CW families when the control plane fires an incident: connection_saturation→connections/cpu,
// lock_contention→disk_queue_depth, slow_query_storm→read/write latency. The scope key matches the
// dbo11y constructs', so one fired incident moves both lanes. (No replica_lag: a standalone primary
// does not emit ReplicaLag — live-confirmed absent from a live reference cluster 2026-06-14; it is replica-only.)
//
// dimension_DBInstanceIdentifier = fx.DB.Name byte-exact (the dbo11y↔cloud join key, I12).
//
// aws_rds_info carries tag_VpcId per the extract universal-labels spec.
package rds

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// Config is empty — the DB fixture carries all identity and the blueprint topology
// resolver owns instance config. This satisfies the ConstructReg.NewConfig contract
// (a pointer to a zero-value struct decodes cleanly from an absent YAML node).
type Config struct{}

// Construct is the per-instance RDS renderer. Not exported; callers use Build.
type Construct struct {
	db    *fixture.DB
	cloud *fixture.Cloud
	env   *fixture.Env
	st    *state.State
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates fixtures and returns a ready core.Construct instance.
// Returns an error if fx.DB or fx.Cloud is nil.
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx.DB == nil {
		return nil, fmt.Errorf("rds: Build requires a non-nil fixture.DB")
	}
	if fx.Cloud == nil {
		return nil, fmt.Errorf("rds: Build requires a non-nil fixture.Cloud")
	}
	return &Construct{
		db:    fx.DB,
		cloud: fx.Cloud,
		env:   fx.Env,
		st:    state.NewState(),
	}, nil
}

func (c *Construct) Kind() string                { return "rds" }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics.
//
// Per-period rule (I5): every series uses state.Set (instantaneous gauge), matching CW
// metric-stream semantics where _sum is the per-period total, not a running counter.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	batch := c.render(now, w.Shape)
	return w.Metrics.Write(ctx, batch)
}

// render builds a full per-period batch. Separated so tests can call it without a World.
func (c *Construct) render(now time.Time, eng *shape.Engine) []promrw.Series {
	factor := eng.Factor(now, weightOf(c.env), nonProdOf(c.env))

	// Failure-mode amplifiers (AxisDatabase, scoped to db.Name — the SAME scope key the dbo11y
	// construct uses, so one fired DB incident moves both the CloudWatch and the dbo11y lane
	// coherently). Each returns 1.0 when its mode is inactive, so emission is byte-identical to
	// baseline unless the control plane fires the incident. These modulate EXISTING signals/cw.md [slug: cw-rds]
	// CW metrics — no names invented.
	connFactor := eng.FailFactor(now, "connection_saturation", c.db.Name, 5)
	lockFactor := eng.FailFactor(now, "lock_contention", c.db.Name, 5)
	slowFactor := eng.FailFactor(now, "slow_query_storm", c.db.Name, 4)

	dims := map[string]string{
		"dimension_DBInstanceIdentifier": c.db.Name,
	}
	lbls := c.baseLabels(dims)

	// ── Universal metrics (all engines) ──────────────────────────────────────

	// connection_saturation drives connections up and burns CPU.
	cpuPct := 20.0*factor*connFactor + eng.NormFloat64()*3
	cpuPct = clamp(cpuPct, 1, 95)
	setGauge(c.st, "aws_rds_cpuutilization", lbls, cpuPct)

	connections := 15.0*factor*connFactor + eng.NormFloat64()*2
	connections = clamp(connections, 1, 1e9)
	setGauge(c.st, "aws_rds_database_connections", lbls, connections)

	freeableMemBytes := (4.0 - factor*2) * 1024 * 1024 * 1024
	freeableMemBytes = clamp(freeableMemBytes, 512*1024*1024, 1e15)
	setGauge(c.st, "aws_rds_freeable_memory", lbls, freeableMemBytes)

	freeStorageBytes := (80.0 - factor*30) * 1024 * 1024 * 1024
	freeStorageBytes = clamp(freeStorageBytes, 1*1024*1024*1024, 1e15)
	setGauge(c.st, "aws_rds_free_storage_space", lbls, freeStorageBytes)

	readIOPS := factor * 120
	writeIOPS := factor * 60
	setGauge(c.st, "aws_rds_read_iops", lbls, readIOPS)
	setGauge(c.st, "aws_rds_write_iops", lbls, writeIOPS)

	// slow_query_storm stretches the latency right-tail.
	readLatMs := 0.8*slowFactor + eng.NormFloat64()*0.3
	readLatMs = clamp(readLatMs, 0.1, 1e9)
	writeLatMs := 0.5*slowFactor + eng.NormFloat64()*0.2
	writeLatMs = clamp(writeLatMs, 0.1, 1e9)
	setGauge(c.st, "aws_rds_read_latency", lbls, readLatMs/1000)   // seconds
	setGauge(c.st, "aws_rds_write_latency", lbls, writeLatMs/1000) // seconds

	setGauge(c.st, "aws_rds_read_throughput", lbls, readIOPS*16*1024)
	setGauge(c.st, "aws_rds_write_throughput", lbls, writeIOPS*8*1024)

	setGauge(c.st, "aws_rds_network_receive_throughput", lbls, factor*2*1024*1024)
	setGauge(c.st, "aws_rds_network_transmit_throughput", lbls, factor*1024*1024)

	// lock_contention: lock waits back up the I/O queue.
	diskQueueDepth := factor*0.8*lockFactor + eng.NormFloat64()*0.1
	diskQueueDepth = clamp(diskQueueDepth, 0, 1e9)
	setGauge(c.st, "aws_rds_disk_queue_depth", lbls, diskQueueDepth)

	setGauge(c.st, "aws_rds_swap_usage", lbls, factor*128*1024*1024)

	// burst_balance — drains under load (gp2 bucket, 3000 IOPS baseline)
	burstBalance := 3000.0 - (readIOPS+writeIOPS)*0.5
	burstBalance = clamp(burstBalance, 100, 3000)
	setGauge(c.st, "aws_rds_burst_balance", lbls, burstBalance)

	// ── Postgres-only metrics ────────────────────────────────────────────────
	if c.db.Engine == "postgres" {
		setGauge(c.st, "aws_rds_transaction_logs_disk_usage", lbls, factor*512*1024*1024)
		setGauge(c.st, "aws_rds_transaction_logs_generation", lbls, factor*5*1024*1024)
		setGauge(c.st, "aws_rds_maximum_used_transaction_ids", lbls, factor*100000)
		setGauge(c.st, "aws_rds_replication_slot_disk_usage", lbls, factor*64*1024*1024)
	}

	// ── Info series (aws_rds_info, gauge=1, no stat suffix, tag_VpcId) ───────
	infoLbls := c.baseLabels(dims)
	if c.cloud.VpcID != "" {
		infoLbls["tag_VpcId"] = c.cloud.VpcID
	}
	c.st.Set("aws_rds_info", infoLbls, 1)

	return c.st.Collect(now)
}

// baseLabels builds the full CloudWatch label set for one series.
// extra labels (dimension_*, tag_*) are merged in. Returns a fresh map each time.
func (c *Construct) baseLabels(extra map[string]string) map[string]string {
	m := map[string]string{
		"account_id": c.cloud.AccountID,
		"region":     c.cloud.Region,
		"namespace":  "AWS/RDS",
		"job":        "cloud/aws/rds",
		"name": fmt.Sprintf("arn:aws:rds:%s:%s:db:%s",
			c.cloud.Region, c.cloud.AccountID, c.db.Name),
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// setGauge emits the full five-stat family for one CW per-period RDS metric. RDS treats
// the supplied value as the per-period figure itself (flat across _sum/_average/_max/_min);
// sample_count of 60 models 1 sample/second over the 60-second CW window. The per-period-
// gauge rule (I5) + suffix mechanic (I6) + per-suffix label isolation live in cw.EmitStats.
func setGauge(st *state.State, name string, lbls map[string]string, v float64) {
	cw.EmitStats(st, name, lbls, cw.StatSet{Sum: v, Average: v, Maximum: v, Minimum: v, SampleCount: 60})
}

func weightOf(env *fixture.Env) float64 {
	if env == nil {
		return 1.0
	}
	return env.Weight
}

func nonProdOf(env *fixture.Env) bool {
	if env == nil {
		return false
	}
	return env.NonProd
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
