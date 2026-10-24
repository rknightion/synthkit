// SPDX-License-Identifier: AGPL-3.0-only

// Package cloudwatchlogs emits explicitly declared log-group intake and optional
// independent Lambda subscription-delivery period observations.
// Source: signals/cw.md [slug: cw-logs-emitted]. Intake selects P36; optional
// delivery selects Lambda/P21 only. Bytes are uncompressed for intake and already
// compressed for delivery; event units remain AWS None, NOT Count. Per-event intake
// sampling and independent period observations are explicit operator assumptions,
// not captured numerical fidelity, a delivery simulator or a native/log producer.
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
	// SubscriptionFilters optionally declare independent Lambda delivery observations;
	// omitted emits only legacy intake. At most 100 filters total across all groups.
	SubscriptionFilters []SubscriptionFilter `yaml:"subscription_filters"`
}

// SubscriptionFilter owns a unique unpadded name within its log group. No target
// resource, function identity, compression or intake relationship is inferred.
type SubscriptionFilter struct {
	// Name is required, unpadded and unique within the owning log group.
	Name string `yaml:"name"`
	// DestinationType must be exactly Lambda, the observed P21 selector literal.
	DestinationType string `yaml:"destination_type"`
	// PeriodObservation requires all four whole finite nonnegative values, including
	// explicit zero. Per active minute four value stats=x, sample_count=1, never x.
	PeriodObservation PeriodObservation `yaml:"period_observation"`
}

// PeriodObservation requires all four whole, finite, nonnegative values (including
// explicit zero). Each active minute observes x once: sum/average/min/max=x and
// sample_count=1. These engineering bounds are not AWS quotas or captured fidelity.
type PeriodObservation struct {
	// DeliveryErrors counts errored log events, not terminal losses; maximum 60000001.
	DeliveryErrors *float64 `yaml:"delivery_errors"`
	// DeliveryThrottling counts throttled log events, not API requests; maximum 60000001.
	DeliveryThrottling *float64 `yaml:"delivery_throttling"`
	// ForwardedLogEvents counts forwarded log events, not invocations; maximum 60000001.
	ForwardedLogEvents *float64 `yaml:"forwarded_log_events"`
	// ForwardedBytes is already-compressed bytes, not intake bytes; maximum 3932160065536.
	ForwardedBytes *float64 `yaml:"forwarded_bytes"`
}

func Registration() core.ConstructReg {
	return core.ConstructReg{Kind: "cloudwatch_logs", Doc: "AWS/Logs intake and optional Lambda delivery observations (remote-write only)", Scope: core.ScopeBlueprint, NewConfig: func() any { return &Config{} }, Build: Build}
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
	labels  map[string]string
	bytes   int64
	rate    big.Rat
	carry   big.Rat
	filters []filterState
}

type filterState struct {
	labels map[string]string
	values [4]float64
}

type Construct struct {
	groups  []groupState
	last    time.Time
	started bool
	paused  bool
	// Integer active nanoseconds, always less than one minute between ticks.
	deliveryProgress time.Duration
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
	// Reuse the existing resource and exact-integer product safety bounds, not
	// AWS subscription quotas. Bound the aggregate before allocating filter state.
	totalFilters := 0
	for _, g := range cfg.LogGroups {
		if len(g.SubscriptionFilters) > maxGroups-totalFilters {
			return nil, fmt.Errorf("cloudwatch_logs: at most 100 subscription_filters total")
		}
		totalFilters += len(g.SubscriptionFilters)
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
		filtersSeen := make(map[string]bool, len(g.SubscriptionFilters))
		for _, f := range g.SubscriptionFilters {
			if !identity(f.Name) || filtersSeen[f.Name] {
				return nil, fmt.Errorf("cloudwatch_logs %q: unique unpadded filter name required: %q", g.Name, f.Name)
			}
			filtersSeen[f.Name] = true
			if f.DestinationType != "Lambda" {
				return nil, fmt.Errorf("cloudwatch_logs %q: destination_type must be Lambda", g.Name)
			}
			p := f.PeriodObservation
			inputs := [4]*float64{p.DeliveryErrors, p.DeliveryThrottling, p.ForwardedLogEvents, p.ForwardedBytes}
			fs := filterState{labels: map[string]string{}}
			for j, v := range inputs {
				if v == nil {
					return nil, fmt.Errorf("cloudwatch_logs %q filter %q: all four period observations required", g.Name, f.Name)
				}
				limit := float64(maxWindowEvents)
				if j == 3 {
					limit *= maxBytes
				}
				if math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > limit || math.Trunc(*v) != *v {
					return nil, fmt.Errorf("cloudwatch_logs %q filter %q: period observations must be finite whole values in [0,%g]", g.Name, f.Name, limit)
				}
				fs.values[j] = *v
			}
			for k, v := range owned.labels {
				fs.labels[k] = v
			}
			fs.labels["dimension_FilterName"] = f.Name
			fs.labels["dimension_DestinationType"] = f.DestinationType
			owned.filters = append(owned.filters, fs)
		}
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
	// One authored period observation per active minute, independent of intake.
	// elapsed caps only NEW progress; earned sub-minute nanoseconds survive gaps
	// and pause. Consume the attempted period before calling the fallible writer.
	c.deliveryProgress += elapsed
	deliveryDue := c.deliveryProgress >= time.Minute
	if deliveryDue {
		c.deliveryProgress -= time.Minute
	}
	var seconds big.Rat
	seconds.SetFrac64(int64(elapsed), int64(time.Second))
	for i := range c.groups {
		g := &c.groups[i]
		if deliveryDue {
			for _, f := range g.filters {
				for j, base := range [...]string{"aws_logs_delivery_errors", "aws_logs_delivery_throttling", "aws_logs_forwarded_log_events", "aws_logs_forwarded_bytes"} {
					x := f.values[j]
					cw.EmitStats(st, base, f.labels, cw.StatSet{Sum: x, Average: x, Maximum: x, Minimum: x, SampleCount: 1})
				}
			}
		}
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
