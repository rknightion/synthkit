// SPDX-License-Identifier: AGPL-3.0-only

// Package scheduler emits the bounded EventBridge Scheduler CloudWatch contract for
// fixed-rate schedules. Names, units and dimensions come from signals/cw.md
// (cw-scheduler) and
// https://docs.aws.amazon.com/scheduler/latest/UserGuide/monitoring-cloudwatch.html.
// InvocationAttemptCount, its account-level (no dimension) and ScheduleGroup profiles
// and its one-observation-of-1-per-attempt statistics were observed in a lab-account
// capture (eu-west-1, 2026-10-24, rate(1 minute) schedule). TargetErrorCount and
// InvocationDroppedCount are documented, not observed; they appear only under the
// opt-in failure mode. Values are operator assumptions.
package scheduler

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// Config declares every schedule of one account and region explicitly. Only fixed-rate
// schedules are modelled; one instance owns the account-level series and every
// ScheduleGroup series, so the root must not declare a second one for the same
// account and region.
type Config struct {
	Schedules []Schedule `yaml:"schedules"`
}

// Schedule is one EventBridge Scheduler schedule with a fixed rate. It fires once per
// period at a stable synthetic minute offset; every firing is one invocation attempt.
// Retries are not modelled: a failed run is one attempt, as with a retry policy of
// zero attempts. A schedule whose target is a Lambda function invokes it
// asynchronously, so a run that fails inside the function is a Lambda Errors
// observation on that function, never a Scheduler target error. Model it on the
// Lambda declaration of the target function, not here.
type Schedule struct {
	// Name is the schedule name, unique within its group (1..64 letters, digits, '.', '_' or '-').
	Name string `yaml:"name"`
	// Group is the schedule group and the ScheduleGroup dimension value. Omitted means
	// "default", the group EventBridge Scheduler uses for a schedule created without one.
	Group string `yaml:"group"`
	// Rate is an EventBridge Scheduler rate expression, rate(<value> <unit>) with a
	// whole positive value and unit minute(s), hour(s) or day(s), for example
	// "rate(5 minutes)". Cron and one-time at() expressions are rejected.
	Rate string `yaml:"rate"`
	// TargetErrorFraction is the opt-in failure mode: the fraction of runs, in [0,1],
	// whose target API call returns an error. Each failed run adds one TargetErrorCount
	// and, with no retries modelled, one InvocationDroppedCount. Failed runs are evenly
	// spaced with fractional carry. Zero (the default) emits neither metric.
	TargetErrorFraction float64 `yaml:"target_error_fraction"`
}

const defaultGroup = "default"

var (
	// AWS CreateSchedule/CreateScheduleGroup name constraint: [0-9a-zA-Z-_.]+, 1..64.
	namePattern = regexp.MustCompile(`^[0-9A-Za-z._-]{1,64}$`)
	ratePattern = regexp.MustCompile(`^rate\(([1-9][0-9]{0,6}) (minute|minutes|hour|hours|day|days)\)$`)
)

// maxPeriod bounds a declared rate; a longer schedule has no observable cadence here.
const maxPeriod = 366 * 24 * time.Hour

type scheduleState struct {
	cfg    Schedule
	group  string
	period int64 // seconds
	phase  int64 // seconds, a whole minute in [0, period)
	failed eventCarry
}

type construct struct {
	schedules       []scheduleState
	groups          []string // declaration order, unique
	account, region string
	last            time.Time
	started         bool
	paused          bool
}

// Registration is consumed by the composition root, never a global registry.
func Registration() core.ConstructReg {
	return core.ConstructReg{Kind: "scheduler", Doc: "EventBridge Scheduler fixed-rate schedule CloudWatch metrics (remote-write only)", Scope: core.ScopeBlueprint, NewConfig: func() any { return &Config{} }, Build: Build}
}

// ParseRate returns the period of an EventBridge Scheduler rate expression.
func ParseRate(expr string) (time.Duration, error) {
	m := ratePattern.FindStringSubmatch(expr)
	if m == nil {
		return 0, fmt.Errorf("rate %q must be rate(<whole value> minute(s)|hour(s)|day(s)); cron() and at() are not modelled", expr)
	}
	v, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("rate %q: %v", expr, err)
	}
	unit := time.Minute
	switch m[2] {
	case "hour", "hours":
		unit = time.Hour
	case "day", "days":
		unit = 24 * time.Hour
	}
	if time.Duration(v) > maxPeriod/unit {
		return 0, fmt.Errorf("rate %q exceeds the modelled maximum period of %s", expr, maxPeriod)
	}
	return time.Duration(v) * unit, nil
}

// Build validates the complete configuration, copying it to isolate live state.
func Build(raw any, fx *fixture.Set) (core.Construct, error) {
	cfg, ok := raw.(*Config)
	if !ok || cfg == nil {
		return nil, fmt.Errorf("scheduler: want *Config, got %T", raw)
	}
	if fx == nil || fx.Cloud == nil {
		return nil, fmt.Errorf("scheduler: fixture.Cloud is required")
	}
	if mode := fx.Cloud.CloudWatchExportMode(); mode != "remote_write" {
		return nil, fmt.Errorf("scheduler: unsupported CloudWatch export %q; only remote_write is admitted (otlp/native is not implemented)", mode)
	}
	if fx.Cloud.AccountID == "" || fx.Cloud.Region == "" {
		return nil, fmt.Errorf("scheduler: explicit cloud account_id and region are required")
	}
	if len(cfg.Schedules) == 0 || len(cfg.Schedules) > 1000 {
		return nil, fmt.Errorf("scheduler: schedules must contain 1..1000 explicit schedules")
	}
	c := &construct{account: fx.Cloud.AccountID, region: fx.Cloud.Region}
	seen := map[string]bool{}
	groups := map[string]bool{}
	for _, s := range cfg.Schedules {
		group := s.Group
		if group == "" {
			group = defaultGroup
		}
		if !namePattern.MatchString(s.Name) {
			return nil, fmt.Errorf("scheduler: schedule name %q must be 1..64 letters, digits, '.', '_' or '-'", s.Name)
		}
		if !namePattern.MatchString(group) {
			return nil, fmt.Errorf("scheduler: schedule %q group %q must be 1..64 letters, digits, '.', '_' or '-'", s.Name, group)
		}
		key := group + "/" + s.Name
		if seen[key] {
			return nil, fmt.Errorf("scheduler: schedule %q is declared twice in group %q", s.Name, group)
		}
		seen[key] = true
		period, err := ParseRate(s.Rate)
		if err != nil {
			return nil, fmt.Errorf("scheduler: schedule %q: %v", s.Name, err)
		}
		f := s.TargetErrorFraction
		if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1 {
			return nil, fmt.Errorf("scheduler: schedule %q target_error_fraction must be finite in [0,1]", s.Name)
		}
		if !groups[group] {
			groups[group] = true
			c.groups = append(c.groups, group)
		}
		p := int64(period / time.Second)
		c.schedules = append(c.schedules, scheduleState{cfg: s, group: group, period: p, phase: phase(key, p)})
	}
	return c, nil
}

// phase is a stable whole-minute offset in [0, period) derived from the schedule's
// identity, standing in for its unknown creation time. Distinct schedules of one rate
// therefore do not all fire in the same minute.
func phase(key string, period int64) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	minutes := period / 60
	return int64(h.Sum64()%uint64(minutes)) * 60
}

// firings counts fire times t = phase + k*period in the half-open window (from, to].
func firings(from, to time.Time, phase, period int64) float64 {
	return float64(floorDiv(to.Unix()-phase, period) - floorDiv(from.Unix()-phase, period))
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func (*construct) Kind() string                { return "scheduler" }
func (*construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (*construct) Interval() time.Duration     { return time.Minute }

// Tick observes one window. The first tick observes one interval ending at now;
// later ticks observe the elapsed time capped at Interval, so missed windows are not
// replayed. Repeated or backward timestamps emit nothing. A master pause advances the
// clock without emission; the first resumed tick only establishes a new boundary.
// Publication is sparse: a group or the account emits only in a window with at least
// one attempt (as captured), and error metrics only in a window with a failed run.
// Every statistic is a per-period gauge of one observation of 1 per event.
func (c *construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w == nil || w.Metrics == nil {
		return fmt.Errorf("scheduler: Metrics writer is required")
	}
	from := now.Add(-c.Interval())
	if c.started {
		if !now.After(c.last) {
			return nil
		}
		if c.last.After(from) {
			from = c.last
		}
	}
	c.last = now
	c.started = true
	if w.Shape != nil && w.Shape.Paused() {
		c.paused = true
		return nil
	}
	if c.paused {
		c.paused = false
		return nil
	}
	attempts := map[string]float64{}
	failed := map[string]float64{}
	var totalAttempts, totalFailed float64
	for i := range c.schedules {
		s := &c.schedules[i]
		n := firings(from, now, s.phase, s.period)
		if n <= 0 {
			continue
		}
		f := take(&s.failed, n*s.cfg.TargetErrorFraction)
		attempts[s.group] += n
		failed[s.group] += f
		totalAttempts += n
		totalFailed += f
	}
	st := state.NewState()
	emit := func(labels map[string]string, n, f float64) {
		if n > 0 {
			cw.EmitStats(st, "aws_scheduler_invocation_attempt_count", labels, ones(n))
		}
		if f > 0 {
			cw.EmitStats(st, "aws_scheduler_target_error_count", labels, ones(f))
			cw.EmitStats(st, "aws_scheduler_invocation_dropped_count", labels, ones(f))
		}
	}
	for _, g := range c.groups {
		emit(c.labels(g), attempts[g], failed[g])
	}
	emit(c.labels(""), totalAttempts, totalFailed)
	return w.Metrics.Write(ctx, st.Collect(now))
}

// ones is n binary event observations of value 1.
func ones(n float64) cw.StatSet {
	return cw.StatSet{Sum: n, Average: 1, Minimum: 1, Maximum: 1, SampleCount: n}
}

func (c *construct) labels(group string) map[string]string {
	labels := map[string]string{"account_id": c.account, "region": c.region, "namespace": "AWS/Scheduler", "job": "cloud/aws/scheduler", "name": "global"}
	if group != "" {
		labels["dimension_ScheduleGroup"] = group
	}
	return labels
}

// eventCarry retains fractional failed runs between windows (the same compensated
// carry the other CloudWatch consumers use; constructs cannot share it).
type eventCarry struct{ fraction, correction float64 }

func take(carry *eventCarry, amount float64) float64 {
	if amount == 0 {
		return 0
	}
	y := amount - carry.correction
	v := carry.fraction + y
	carry.correction = (v - carry.fraction) - y
	n := math.Max(0, math.Floor(v))
	if n+1-v <= math.Max(1e-12, 8*(math.Nextafter(v, math.Inf(1))-v)) {
		n++
	}
	carry.fraction = v - n
	return n
}
