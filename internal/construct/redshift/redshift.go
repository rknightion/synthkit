// SPDX-License-Identifier: AGPL-3.0-only

// Package redshift emits a bounded provisioned-cluster snapshot and optional
// fixed-latency I/O observations. Names and dimension projections are sourced in
// signals/cw.md; numerical assumptions are operator supplied, not AWS fidelity.
package redshift

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

type Config struct {
	Clusters []Cluster `yaml:"clusters"`
}
type Cluster struct {
	Name          string     `yaml:"name"`
	CollectorName string     `yaml:"collector_name"`
	Snapshot      *Snapshot  `yaml:"snapshot"`
	ReadIO        *IO        `yaml:"read_io"`
	WriteIO       *IO        `yaml:"write_io"`
	WLMQueues     []WLMQueue `yaml:"wlm_queues"`
}

// Snapshot requires all six assumptions, including legitimate zero/false values.
type Snapshot struct {
	CPUPercent          *float64 `yaml:"cpu_percent"`
	DatabaseConnections *float64 `yaml:"database_connections"`
	Healthy             *bool    `yaml:"healthy"`
	Maintenance         *bool    `yaml:"maintenance"`
	DiskUsedPercent     *float64 `yaml:"disk_used_percent"`
	TotalTables         *float64 `yaml:"total_tables"`
}
type IO struct {
	OperationsPerSecond float64  `yaml:"operations_per_second"`
	LatencySeconds      *float64 `yaml:"latency_seconds"`
}
type WLMQueue struct {
	Name    string   `yaml:"name"`
	Waiting *float64 `yaml:"waiting"`
	Running *float64 `yaml:"running"`
}

func Registration() core.ConstructReg {
	return core.ConstructReg{Kind: "redshift", Doc: "Provisioned AWS/Redshift snapshots and sparse I/O latency (remote-write only)", Scope: core.ScopeBlueprint, NewConfig: func() any { return &Config{} }, Build: Build}
}

type observation struct {
	base   string
	value  float64
	labels map[string]string
}
type ioState struct {
	base    string
	latency float64
	// Exact rational accumulation of the declared binary float rate and integer
	// nanoseconds avoids drift, cancellation, and epsilon-created events. Denominators
	// stay bounded by a float64's binary denominator times 1e9; no per-event objects.
	// Decimal intent can still differ from the decoded float: an exact boundary may
	// be delayed rather than rounded up. This is not numerical capture fidelity.
	rate, carry big.Rat
	labels      map[string]string
}
type Construct struct {
	stocks          []observation
	ios             []ioState
	last            time.Time
	started, paused bool
}

var _ core.Construct = (*Construct)(nil)

func (*Construct) Kind() string                { return "redshift" }
func (*Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (*Construct) Interval() time.Duration     { return time.Minute }

func identity(s string) bool { return s != "" && strings.TrimSpace(s) == s }
func bounded(v, lo, hi float64, whole bool) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= lo && v <= hi && (!whole || math.Trunc(v) == v)
}
func number(v *float64, hi float64, whole bool) bool { return v != nil && bounded(*v, 0, hi, whole) }
func boolean(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func Build(raw any, fx *fixture.Set) (core.Construct, error) {
	cfg, ok := raw.(*Config)
	if !ok || cfg == nil {
		return nil, fmt.Errorf("redshift: want nonnil *Config, got %T", raw)
	}
	if fx == nil || fx.Cloud == nil || !identity(fx.Cloud.AccountID) || !identity(fx.Cloud.Region) {
		return nil, fmt.Errorf("redshift: explicit cloud account_id and region required")
	}
	if fx.Cloud.CloudWatchExportMode() != "remote_write" {
		return nil, fmt.Errorf("redshift: only remote_write CloudWatch export is admitted")
	}
	// Engineering refusal bounds, not AWS quotas or recommended loads. Maximum
	// inventory is 100*(6+100*2+2)*5=104000 series; maximum N is 60000001
	// (including carry), N*latency <=216000003600, all exactly representable
	// integral counts and finite float sums. No count-sized allocations occur.
	if len(cfg.Clusters) < 1 || len(cfg.Clusters) > 100 {
		return nil, fmt.Errorf("redshift: declare 1..100 clusters")
	}
	out := &Construct{}
	names, collectors := map[string]bool{}, map[string]bool{}
	for _, c := range cfg.Clusters {
		if !identity(c.Name) || !identity(c.CollectorName) || c.CollectorName == "global" || names[c.Name] || collectors[c.CollectorName] {
			return nil, fmt.Errorf("redshift: unique cluster name and explicit unique non-global collector_name required")
		}
		names[c.Name], collectors[c.CollectorName] = true, true
		s := c.Snapshot
		if s == nil || !number(s.CPUPercent, 100, false) || !number(s.DatabaseConnections, 1e9, true) || s.Healthy == nil || s.Maintenance == nil || !number(s.DiskUsedPercent, 100, false) || !number(s.TotalTables, 1e9, true) {
			return nil, fmt.Errorf("redshift %s: all snapshot fields required; finite percents 0..100, whole counts 0..1000000000", c.Name)
		}
		labels := map[string]string{"account_id": fx.Cloud.AccountID, "region": fx.Cloud.Region, "namespace": "AWS/Redshift", "job": "cloud/aws/redshift", "name": c.CollectorName, "dimension_ClusterIdentifier": c.Name}
		for _, v := range []struct {
			base  string
			value float64
		}{
			{"aws_redshift_cpuutilization", *s.CPUPercent},
			{"aws_redshift_database_connections", *s.DatabaseConnections},
			{"aws_redshift_health_status", boolean(*s.Healthy)},
			{"aws_redshift_maintenance_mode", boolean(*s.Maintenance)},
			{"aws_redshift_percentage_disk_space_used", *s.DiskUsedPercent},
			{"aws_redshift_total_table_count", *s.TotalTables},
		} {
			out.stocks = append(out.stocks, observation{v.base, v.value, labels})
		}
		for _, v := range []struct {
			base string
			io   *IO
		}{{"aws_redshift_read_latency", c.ReadIO}, {"aws_redshift_write_latency", c.WriteIO}} {
			if v.io == nil {
				continue
			}
			io := v.io
			if !bounded(io.OperationsPerSecond, 0, 1e6, false) || !number(io.LatencySeconds, 3600, false) || *io.LatencySeconds <= 0 {
				return nil, fmt.Errorf("redshift %s: I/O rate must be finite 0..1000000, explicit latency finite (0,3600] seconds", c.Name)
			}
			// Prove worst-window products before any emission/conversion, including carry.
			n := math.Ceil(io.OperationsPerSecond*60 + 1)
			if !bounded(n, 0, 60000001, true) || !bounded(n**io.LatencySeconds, 0, 216000003600, false) {
				return nil, fmt.Errorf("redshift %s: unrepresentable I/O product", c.Name)
			}
			item := ioState{base: v.base, latency: *io.LatencySeconds, labels: labels}
			item.rate.SetFloat64(io.OperationsPerSecond)
			out.ios = append(out.ios, item)
		}
		if len(c.WLMQueues) > 100 {
			return nil, fmt.Errorf("redshift %s: at most 100 WLM queues", c.Name)
		}
		queues := map[string]bool{}
		for _, q := range c.WLMQueues {
			if !identity(q.Name) || queues[q.Name] || !number(q.Waiting, 1e9, true) || !number(q.Running, 1e9, true) {
				return nil, fmt.Errorf("redshift %s: unique queue name and explicit whole waiting/running counts 0..1000000000 required", c.Name)
			}
			queues[q.Name] = true
			qlabels := make(map[string]string, len(labels)+1)
			for k, v := range labels {
				qlabels[k] = v
			}
			qlabels["dimension_QueueName"] = q.Name
			out.stocks = append(out.stocks, observation{"aws_redshift_wlmqueue_length", *q.Waiting, qlabels}, observation{"aws_redshift_wlmrunning_queries", *q.Running, qlabels})
		}
	}
	return out, nil
}

// Tick models one explicit synthetic bootstrap minute on the first ordinary active
// observation. Thereafter only increasing timestamps admit new accrual, capped at
// 60s. Gaps discard NEW unobserved activity, never earned fractions. Master zero
// suppresses every batch; first observed positive only establishes a new boundary.
// Positive master values do not scale these fixed assumptions. Pause/resume wholly
// between observations is unobservable. No durable replay or backend fidelity.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	if c.started && !now.After(c.last) {
		return nil
	}
	if w != nil && w.Shape != nil && w.Shape.Paused() {
		c.last = now
		c.started = true
		c.paused = true
		return nil
	}
	if c.paused {
		c.last = now
		c.paused = false
		return nil
	}
	if w == nil || w.Metrics == nil {
		return fmt.Errorf("redshift: metric writer required")
	}
	dt := time.Minute
	if c.started {
		dt = now.Sub(c.last)
		if dt > time.Minute {
			dt = time.Minute
		}
	}
	c.last = now
	c.started = true
	st := state.NewState() // fresh period: sparse families must never re-emit stale data
	for _, s := range c.stocks {
		cw.EmitStats(st, s.base, s.labels, cw.StatSet{Sum: s.value, Average: s.value, Maximum: s.value, Minimum: s.value, SampleCount: 1})
	}
	for i := range c.ios {
		io := &c.ios[i]
		var seconds, earned big.Rat
		seconds.SetFrac64(int64(dt), int64(time.Second))
		earned.Mul(&io.rate, &seconds)
		io.carry.Add(&io.carry, &earned)
		var count big.Int
		count.Quo(io.carry.Num(), io.carry.Denom())
		var whole big.Rat
		whole.SetInt(&count)
		io.carry.Sub(&io.carry, &whole)
		// Build bounds prove count <=60000001 (<2^53); conversion is exact.
		n := float64(count.Int64())
		if n > 0 {
			cw.EmitStats(st, io.base, io.labels, cw.StatSet{Sum: n * io.latency, Average: io.latency, Maximum: io.latency, Minimum: io.latency, SampleCount: n})
		}
	}
	return w.Metrics.Write(ctx, st.Collect(now))
}
