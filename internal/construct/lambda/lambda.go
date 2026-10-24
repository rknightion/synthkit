// SPDX-License-Identifier: AGPL-3.0-only

// Package lambda emits the bounded FunctionName-only CloudWatch Lambda contract.
// Names, units and binary event semantics come from signals/cw.md (cw-lambda)
// and https://docs.aws.amazon.com/lambda/latest/dg/monitoring-metrics-types.html.
// Values are operator assumptions, NOT numerical fidelity to the key-only capture.
package lambda

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// Config declares resources explicitly; no generated identities or metadata aliases.
// Positive-volume rates are constant operator assumptions by default, independent of the
// blueprint shape; a function's opt-in `load: business` scales them by the business
// curve instead. Master volume zero pauses all emission and event accrual.
// Omitted numerical inputs are zero (no traffic), never defaults that create load.
// One instance owns the account-region concurrency series; root must not duplicate it.
type Config struct {
	Functions []Function `yaml:"functions"`
	// ReservedConcurrency is account capacity reserved/provisioned outside these
	// unreserved functions. Must be an integer capacity count. Claimed concurrency
	// = this allocation + modelled activity.
	ReservedConcurrency float64 `yaml:"reserved_concurrency"`
}

// Function is an unqualified AWS function name and an explicit synthetic value model.
// Invocations includes completed execution attempts, including Errors, not Throttles.
// Async invocations (received minus dropped), stream batches and synchronous
// executions are disjoint populations. InvocationsPerMin is their total long-run
// rate and must cover AsyncReceivedPerMin*(1-AsyncDroppedFraction) plus
// StreamBatchesPerMin; the remainder is synchronous execution traffic. These are
// same-function populations, not cross-service joins or an inferred ESM identity.
// Each execution has exactly DurationMS; each invoked async event's age and each
// stream batch's last-record age is constant. No retry observations are modelled.
// Fractions select evenly spaced failures/drops, retaining fractional carry.
type Function struct {
	Name                 string  `yaml:"name"`
	InvocationsPerMin    float64 `yaml:"invocations_per_min"`
	DurationMS           float64 `yaml:"duration_ms"`
	ErrorFraction        float64 `yaml:"error_fraction"`
	ThrottlesPerMin      float64 `yaml:"throttles_per_min"`
	AsyncReceivedPerMin  float64 `yaml:"async_received_per_min"`
	AsyncDroppedFraction float64 `yaml:"async_dropped_fraction"`
	AsyncAgeMS           float64 `yaml:"async_age_ms"`
	StreamBatchesPerMin  float64 `yaml:"stream_batches_per_min"`
	IteratorAgeMS        float64 `yaml:"iterator_age_ms"`
	// Load selects how the arrival rates follow time. Empty or "constant" keeps every
	// rate fixed (the default). "business" scales invocations_per_min,
	// async_received_per_min, stream_batches_per_min and throttles_per_min together by
	// the blueprint's business curve (diurnal x weekly x master volume), so the
	// disjoint populations stay coherent and error_fraction applies to the scaled
	// executions. The configured rates are then the rate at a curve value of 1.0
	// (weekday business-hours plateau). Operator assumption, not an AWS behaviour.
	Load string `yaml:"load"`
	// DurationMSMin is the lower bound of the per-execution duration distribution, in
	// milliseconds. duration_ms stays the mean (Duration _sum and _average unchanged).
	// Must be declared together with duration_ms_max (both zero means constant
	// duration) and not exceed duration_ms. The observed Minimum moves from the mean
	// toward this bound as executions per window increase and is limited so a sample
	// set with the window's sum exists; one execution has no spread. A bound equal to
	// duration_ms leaves no room for spread on either side.
	DurationMSMin float64 `yaml:"duration_ms_min"`
	// DurationMSMax is the upper bound of the per-execution duration distribution, in
	// milliseconds, with the same rules as duration_ms_min. It must not be below
	// duration_ms.
	DurationMSMax float64 `yaml:"duration_ms_max"`
}

type functionState struct {
	cfg                                                        Function
	synchronous, errors, throttles, received, dropped, records eventCarry
	synchronousPerMin                                          float64
	durMin, durMax                                             float64
	business                                                   bool
}

type construct struct {
	functions       []functionState
	account, region string
	reserved        float64
	last            time.Time
	started         bool
	paused          bool
}

// Registration is consumed by the composition root, never a global registry.
func Registration() core.ConstructReg {
	return core.ConstructReg{Kind: "lambda", Doc: "FunctionName-only Lambda CloudWatch metrics (remote-write only)", Scope: core.ScopeBlueprint, NewConfig: func() any { return &Config{} }, Build: Build}
}

// Build validates the complete configuration, copying it to isolate live state.
func Build(raw any, fx *fixture.Set) (core.Construct, error) {
	cfg, ok := raw.(*Config)
	if !ok || cfg == nil {
		return nil, fmt.Errorf("lambda: want *Config, got %T", raw)
	}
	if fx == nil || fx.Cloud == nil {
		return nil, fmt.Errorf("lambda: fixture.Cloud is required")
	}
	if mode := fx.Cloud.CloudWatchExportMode(); mode != "remote_write" {
		return nil, fmt.Errorf("lambda: unsupported CloudWatch export %q; only remote_write is admitted (otlp/native is not implemented)", mode)
	}
	if fx.Cloud.AccountID == "" || fx.Cloud.Region == "" {
		return nil, fmt.Errorf("lambda: explicit cloud account_id and region are required")
	}
	if len(cfg.Functions) == 0 || len(cfg.Functions) > 1000 {
		return nil, fmt.Errorf("lambda: functions must contain 1..1000 explicit resources")
	}
	if !bounded(cfg.ReservedConcurrency, 1e6) || math.Trunc(cfg.ReservedConcurrency) != cfg.ReservedConcurrency {
		return nil, fmt.Errorf("lambda: reserved_concurrency must be an integer in [0,1000000]")
	}
	c := &construct{account: fx.Cloud.AccountID, region: fx.Cloud.Region, reserved: cfg.ReservedConcurrency}
	names := map[string]bool{}
	// AWS unqualified function names, not Resource/ARN/version aliases.
	namePattern := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	for _, f := range cfg.Functions {
		if !namePattern.MatchString(f.Name) || names[f.Name] {
			return nil, fmt.Errorf("lambda: function %q must be a unique unqualified function name (1..64 letters/digits/_/-)", f.Name)
		}
		names[f.Name] = true
		for _, v := range []struct {
			name       string
			value, max float64
		}{
			{"invocations_per_min", f.InvocationsPerMin, 1e9}, {"throttles_per_min", f.ThrottlesPerMin, 1e9},
			{"async_received_per_min", f.AsyncReceivedPerMin, 1e9}, {"stream_batches_per_min", f.StreamBatchesPerMin, 1e9},
			{"duration_ms", f.DurationMS, 60000}, {"duration_ms_min", f.DurationMSMin, 60000}, {"duration_ms_max", f.DurationMSMax, 60000}, {"async_age_ms", f.AsyncAgeMS, 604800000}, {"iterator_age_ms", f.IteratorAgeMS, 604800000},
			{"error_fraction", f.ErrorFraction, 1}, {"async_dropped_fraction", f.AsyncDroppedFraction, 1},
		} {
			if !bounded(v.value, v.max) {
				return nil, fmt.Errorf("lambda: function %q %s must be finite in [0,%g]", f.Name, v.name, v.max)
			}
		}
		if f.Load != "" && f.Load != "constant" && f.Load != "business" {
			return nil, fmt.Errorf("lambda: function %q load must be \"constant\" or \"business\", got %q", f.Name, f.Load)
		}
		if (f.DurationMSMin > 0) != (f.DurationMSMax > 0) {
			return nil, fmt.Errorf("lambda: function %q duration_ms_min and duration_ms_max must be declared together", f.Name)
		}
		if (f.DurationMSMin > 0 && f.DurationMSMin > f.DurationMS) || (f.DurationMSMax > 0 && f.DurationMSMax < f.DurationMS) {
			return nil, fmt.Errorf("lambda: function %q duration_ms_min <= duration_ms <= duration_ms_max is required when a bound is set", f.Name)
		}
		// Resolved duration bounds; both equal duration_ms when no spread is declared.
		durMin, durMax := f.DurationMS, f.DurationMS
		if f.DurationMSMin > 0 {
			durMin = f.DurationMSMin
		}
		if f.DurationMSMax > 0 {
			durMax = f.DurationMSMax
		}
		synchronous := f.InvocationsPerMin - f.AsyncReceivedPerMin*(1-f.AsyncDroppedFraction) - f.StreamBatchesPerMin
		if synchronous < 0 {
			return nil, fmt.Errorf("lambda: function %q invocations_per_min must cover disjoint admitted async and stream batch rates", f.Name)
		}
		c.functions = append(c.functions, functionState{cfg: f, synchronousPerMin: synchronous, durMin: durMin, durMax: durMax, business: f.Load == "business"})
	}
	return c, nil
}
func bounded(v, max float64) bool              { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= max }
func (*construct) Kind() string                { return "lambda" }
func (*construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (*construct) Interval() time.Duration     { return time.Minute }

// Tick observes a window, not a durable simulation. First tick models one interval;
// subsequent ticks use elapsed time capped at Interval: missed windows are not replayed.
// Repeated/backward timestamps emit nothing and do not consume fractional carry.
// Master pause advances the observation clock without event accrual or emission;
// the first resumed tick establishes a new active boundary, excluding the unknown
// pause-to-resume gap. Legitimate pre-pause fractional carry is retained.
// Fresh state prevents old event observations surviving into an idle window.
// All five stats are per-period gauges (never rate/increase of _sum).
func (c *construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w == nil || w.Metrics == nil {
		return fmt.Errorf("lambda: Metrics writer is required")
	}
	seconds := c.Interval().Seconds()
	if c.started {
		if !now.After(c.last) {
			return nil
		}
		seconds = math.Min(seconds, now.Sub(c.last).Seconds())
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
	st := state.NewState()
	active := 0.0
	for i := range c.functions {
		f := &c.functions[i]
		cfg := f.cfg
		labels := c.labels(cfg.Name)
		// Constant load multiplies by exactly 1: default output is unchanged.
		bf := 1.0
		if f.business && w.Shape != nil {
			bf = w.Shape.BusinessFactor(now)
		}
		received := take(&f.received, cfg.AsyncReceivedPerMin*bf*seconds/60)
		dropped := take(&f.dropped, received*cfg.AsyncDroppedFraction)
		invokedAsync := received - dropped
		records := take(&f.records, cfg.StreamBatchesPerMin*bf*seconds/60)
		// Sum actual constituent counts, not an independent total-rate carry: even
		// short sparse windows cannot observe ages without the same executions.
		n := invokedAsync + records + take(&f.synchronous, f.synchronousPerMin*bf*seconds/60)
		errors := take(&f.errors, n*cfg.ErrorFraction)
		throttles := take(&f.throttles, cfg.ThrottlesPerMin*bf*seconds/60)
		// Binary event populations consist of one observation of 1 per event. Failed
		// executions alone contribute Errors; successful executions are not fake zeros.
		if n > 0 {
			cw.EmitStats(st, "aws_lambda_invocations", labels, cw.StatSet{Sum: n, Average: 1, Minimum: 1, Maximum: 1, SampleCount: n})
			// Execution observations: count is executions, not milliseconds. The mean is the
			// declared duration; extremes move from the mean toward the declared bounds as
			// executions accumulate (a single execution has none) and are clamped so a
			// sample set with this Sum, SampleCount, Minimum and Maximum exists. Without
			// bounds all stats equal the constant duration.
			lo, hi := durationExtremes(cfg.DurationMS, f.durMin, f.durMax, n)
			cw.EmitStats(st, "aws_lambda_duration", labels, cw.StatSet{Sum: n * cfg.DurationMS, Average: cfg.DurationMS, Minimum: lo, Maximum: hi, SampleCount: n})
			// Operator schedule: uniformly spaced executions, constant duration, all in the
			// unreserved pool. This is a synthetic peak estimate, not an AWS transfer curve.
			concurrent := math.Min(n, math.Ceil(n*cfg.DurationMS/(seconds*1000)))
			active += concurrent
			cw.EmitStats(st, "aws_lambda_concurrent_executions", labels, cw.StatSet{Sum: concurrent, Average: concurrent, Minimum: concurrent, Maximum: concurrent, SampleCount: 1})
		}
		if errors > 0 {
			cw.EmitStats(st, "aws_lambda_errors", labels, cw.StatSet{Sum: errors, Average: 1, Minimum: 1, Maximum: 1, SampleCount: errors})
		}
		if throttles > 0 {
			cw.EmitStats(st, "aws_lambda_throttles", labels, cw.StatSet{Sum: throttles, Average: 1, Minimum: 1, Maximum: 1, SampleCount: throttles})
		}
		if received > 0 {
			cw.EmitStats(st, "aws_lambda_async_events_received", labels, cw.StatSet{Sum: received, Average: 1, Minimum: 1, Maximum: 1, SampleCount: received})
		}
		// Dropped ingress never reaches an invocation and has no invocation-age sample.
		if invokedAsync > 0 {
			cw.EmitStats(st, "aws_lambda_async_event_age", labels, cw.StatSet{Sum: invokedAsync * cfg.AsyncAgeMS, Average: cfg.AsyncAgeMS, Minimum: cfg.AsyncAgeMS, Maximum: cfg.AsyncAgeMS, SampleCount: invokedAsync})
		}
		if dropped > 0 {
			cw.EmitStats(st, "aws_lambda_async_events_dropped", labels, cw.StatSet{Sum: dropped, Average: 1, Minimum: 1, Maximum: 1, SampleCount: dropped})
		}
		if records > 0 {
			cw.EmitStats(st, "aws_lambda_iterator_age", labels, cw.StatSet{Sum: records * cfg.IteratorAgeMS, Average: cfg.IteratorAgeMS, Minimum: cfg.IteratorAgeMS, Maximum: cfg.IteratorAgeMS, SampleCount: records})
		}
	}
	// One account-region snapshot per tick, including zero traffic; no function tags.
	claimed := c.reserved + active
	cw.EmitStats(st, "aws_lambda_claimed_account_concurrency", c.labels(""), cw.StatSet{Sum: claimed, Average: claimed, Minimum: claimed, Maximum: claimed, SampleCount: 1})
	cw.EmitStats(st, "aws_lambda_unreserved_concurrent_executions", c.labels(""), cw.StatSet{Sum: active, Average: active, Minimum: active, Maximum: active, SampleCount: 1})
	return w.Metrics.Write(ctx, st.Collect(now))
}

// durationExtremes returns the observed Minimum and Maximum of n executions whose mean
// is mean and whose per-execution duration lies in [lo, hi]. No spread (lo == hi ==
// mean) returns the mean exactly. Otherwise they move from the mean
// toward the bounds by 1-1/n, then are clamped so that n samples with sum n*mean exist:
// max <= sum-(n-1)*min and min >= sum-(n-1)*max. n == 1 or equal bounds give the mean.
func durationExtremes(mean, lo, hi, n float64) (float64, float64) {
	if lo == mean && hi == mean {
		return mean, mean
	}
	spread := 1 - 1/n
	minimum := mean - (mean-lo)*spread
	maximum := mean + (hi-mean)*spread
	sum := n * mean
	maximum = math.Min(maximum, sum-(n-1)*minimum)
	minimum = math.Max(minimum, sum-(n-1)*maximum)
	// Rounding in the feasibility bounds must never invert or move the mean.
	minimum, maximum = math.Min(minimum, mean), math.Max(maximum, mean)
	return minimum, maximum
}

// eventCarry retains fractional arrivals, not unused service capacity. Compensated
// addition and a boundary correction (1e-12 events or eight ULPs, whichever is
// larger) prevent short fractional windows from rounding away boundary events.
// Any tiny early rounding is retained as a
// fractional debt, not discarded or accumulated as extra traffic.
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
func (c *construct) labels(function string) map[string]string {
	labels := map[string]string{"account_id": c.account, "region": c.region, "namespace": "AWS/Lambda", "job": "cloud/aws/lambda", "name": "global"}
	if function != "" {
		labels["dimension_FunctionName"] = function
	}
	return labels
}
