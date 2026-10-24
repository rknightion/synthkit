// SPDX-License-Identifier: AGPL-3.0-only

// Package cloudwatchlogs emits only explicitly declared log-group intake metrics.
// Source: signals/cw.md, AWS/Logs IncomingBytes (Bytes, uncompressed) and
// IncomingLogEvents (AWS unit None, NOT Count), selected P36 dimension projection.
// This is an operator-assumption per-event sampling model, not captured numerical
// fidelity, a log-body renderer, subscription delivery or a native metrics producer.
package cloudwatchlogs

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

// Config owns explicit identities. No inventory, rate or size is defaulted.
type Config struct {
	LogGroups []LogGroup `yaml:"log_groups"`
}

// LogGroup is a synthetic upload model. Required pointers distinguish omission
// from a legitimate zero rate. Bytes remain float64 until Build proves integrality
// (decoding directly into an integer can silently truncate fractional YAML values).
type LogGroup struct {
	Name                      string   `yaml:"name"`
	EventsPerSecond           *float64 `yaml:"events_per_second"`
	UncompressedBytesPerEvent *float64 `yaml:"uncompressed_bytes_per_event"`
}

func Registration() core.ConstructReg {
	return core.ConstructReg{Kind: "cloudwatch_logs", Doc: "AWS/Logs log-group intake metrics (remote-write only)", Scope: core.ScopeBlueprint, NewConfig: func() any { return &Config{} }, Build: Build}
}

// Engineering refusal bounds, NOT AWS quotas or recommended production volumes.
const (
	maxGroups = 100
	maxRate   = 1000000
	maxBytes  = 65536
	// A window accrues at most 60*maxRate plus a prior fraction <1.
	maxWindowEvents = 60*maxRate + 1
	maxExactInteger = 1 << 53
)

type groupState struct {
	labels map[string]string
	bytes  int64
	rate   big.Rat
	carry  big.Rat
}

type Construct struct {
	groups  []groupState
	last    time.Time
	started bool
	paused  bool
}

var _ core.Construct = (*Construct)(nil)

func identity(s string) bool { return s != "" && strings.TrimSpace(s) == s }

func Build(raw any, fx *fixture.Set) (core.Construct, error) {
	cfg, ok := raw.(*Config)
	if !ok || cfg == nil {
		return nil, fmt.Errorf("cloudwatch_logs: want *Config, got %T", raw)
	}
	if fx == nil || fx.Cloud == nil || !identity(fx.Cloud.AccountID) || !identity(fx.Cloud.Region) {
		return nil, fmt.Errorf("cloudwatch_logs: requires unpadded cloud account_id and region")
	}
	if mode := fx.Cloud.CloudWatchExportMode(); mode != "remote_write" {
		return nil, fmt.Errorf("cloudwatch_logs: export %q unsupported; remote_write only (otlp not admitted)", mode)
	}
	if len(cfg.LogGroups) < 1 || len(cfg.LogGroups) > maxGroups {
		return nil, fmt.Errorf("cloudwatch_logs: declare 1..100 log_groups")
	}
	// Bound aggregate observations and byte sums before allocating model state.
	// Even all 100 maximum-rate groups together remain exactly representable as
	// integer float64 sums. No count-to-int conversion or per-event allocation occurs.
	if uint64(len(cfg.LogGroups))*maxWindowEvents*maxBytes > maxExactInteger {
		return nil, fmt.Errorf("cloudwatch_logs: aggregate observation product exceeds exact integer bound")
	}
	seen := make(map[string]bool, len(cfg.LogGroups))
	out := &Construct{groups: make([]groupState, len(cfg.LogGroups))}
	for i, g := range cfg.LogGroups {
		if !identity(g.Name) || seen[g.Name] {
			return nil, fmt.Errorf("cloudwatch_logs: explicit unique unpadded log-group name required: %q", g.Name)
		}
		seen[g.Name] = true
		if g.EventsPerSecond == nil || g.UncompressedBytesPerEvent == nil {
			return nil, fmt.Errorf("cloudwatch_logs %q: events_per_second and uncompressed_bytes_per_event required", g.Name)
		}
		r, b := *g.EventsPerSecond, *g.UncompressedBytesPerEvent
		if math.IsNaN(r) || math.IsInf(r, 0) || r < 0 || r > maxRate {
			return nil, fmt.Errorf("cloudwatch_logs %q: events_per_second must be finite in [0,1000000]", g.Name)
		}
		if math.IsNaN(b) || math.IsInf(b, 0) || b < 1 || b > maxBytes || math.Trunc(b) != b {
			return nil, fmt.Errorf("cloudwatch_logs %q: uncompressed_bytes_per_event must be finite whole bytes in [1,65536]", g.Name)
		}
		owned := &out.groups[i]
		owned.rate.SetFloat64(r)
		owned.bytes = int64(b) // validated finite, whole and bounded above
		owned.labels = map[string]string{"account_id": fx.Cloud.AccountID, "region": fx.Cloud.Region, "namespace": "AWS/Logs", "job": "cloud/aws/logs", "name": "global", "dimension_LogGroupName": g.Name}
	}
	return out, nil
}
func (*Construct) Kind() string                { return "cloudwatch_logs" }
func (*Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (*Construct) Interval() time.Duration     { return time.Minute }

// Tick admits strictly increasing observations. The first ordinary active tick
// models an explicit synthetic minute bootstrap, not measured prior activity.
// Thereafter only NEW accrual is capped at a minute; already-earned fractions are
// retained across jitter, idle, gaps and observed master pause. No replay exists.
// The first positive observation after any observed pause is a silent boundary.
// A pause/resume entirely between observations is unobservable. Positive master
// multipliers do not rescale the explicit rates or apply diurnal modulation.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	if c.started && !now.After(c.last) {
		return nil
	}
	if w == nil || w.Metrics == nil {
		return fmt.Errorf("cloudwatch_logs: metric writer required")
	}
	if w.Shape != nil && w.Shape.Paused() {
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
	elapsed := time.Minute
	if c.started {
		elapsed = now.Sub(c.last)
		if elapsed > time.Minute {
			elapsed = time.Minute
		}
	}
	c.last = now
	c.started = true
	st := state.NewState() // never re-emit an earlier sparse window
	var seconds big.Rat
	seconds.SetFrac64(int64(elapsed), int64(time.Second))
	for i := range c.groups {
		g := &c.groups[i]
		// Exact rational arithmetic over the admitted binary float64 rate and integer
		// nanoseconds avoids summation drift and any epsilon that invents events.
		// Decimal rates represented just below their mathematical value may delay an
		// exact-boundary event; we never round that traffic up. Denominators are bounded
		// by float64 precision/range and nanoseconds, not the number of observations.
		var earned big.Rat
		earned.Mul(&g.rate, &seconds)
		g.carry.Add(&g.carry, &earned)
		var whole big.Int
		whole.Quo(g.carry.Num(), g.carry.Denom())
		// Build's aggregate bound and carry <1 prove this conversion and every stat
		// product fit exactly in float64; there is no unchecked integer conversion.
		n, _ := whole.Float64()
		earned.SetInt(&whole)
		g.carry.Sub(&g.carry, &earned)
		if n == 0 {
			continue
		}
		b := float64(g.bytes)
		cw.EmitStats(st, "aws_logs_incoming_bytes", g.labels, cw.StatSet{Sum: n * b, Average: b, Maximum: b, Minimum: b, SampleCount: n})
		cw.EmitStats(st, "aws_logs_incoming_log_events", g.labels, cw.StatSet{Sum: n, Average: 1, Maximum: 1, Minimum: 1, SampleCount: n})
	}
	batch := st.Collect(now)
	if len(batch) == 0 {
		return nil
	}
	return w.Metrics.Write(ctx, batch)
}
