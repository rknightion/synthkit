// SPDX-License-Identifier: AGPL-3.0-only

// Package sqs models explicitly declared queues, emitting the nine ordinary AWS/SQS
// bases in signals/cw.md through remote-write. Quiet/noisy-group and metadata info
// families are deliberately withheld: this model has no tenant-group attribution.
package sqs

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// Config requires explicit queue identities; no queues are generated from a seed.
type Config struct {
	Queues []Queue `yaml:"queues"`
	// set is the blueprint's declared resources, injected by the resolver (causal.Consumer) when
	// the blueprint declares any. Only a queue with Resource set reads it.
	set causal.Set `yaml:"-"`
}

var _ causal.Consumer = (*Config)(nil)

// UseResources implements causal.Consumer.
func (c *Config) UseResources(s causal.Set) { c.set = s }

// Queue specifies an operator-assumption workload, not captured numerical values.
// A receive attempt handles at most one message. Every delivery succeeds exactly
// once and is deleted after ProcessingSeconds; no retries, DLQs or joins are modelled.
type Queue struct {
	Name string `yaml:"name"`
	// CollectorName is the stable non-global collector identity (name label), NOT
	// an ARN inferred from the queue name. Both identities must be supplied.
	CollectorName     string  `yaml:"collector_name"`
	ArrivalsPerSecond float64 `yaml:"arrivals_per_second"`
	ReceivesPerSecond float64 `yaml:"receives_per_second"`
	// MessageBytes is a fixed whole-byte payload, in the documented 1..1 MiB range:
	// https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/quotas-messages.html
	MessageBytes      float64 `yaml:"message_bytes"`
	DelaySeconds      int     `yaml:"delay_seconds"`
	ProcessingSeconds int     `yaml:"processing_seconds"`
	// Load selects how the queue's traffic follows the blueprint's business curve.
	// Empty (the default) keeps arrivals_per_second and receives_per_second constant.
	// "business" treats arrivals_per_second as the peak-plateau arrival rate and
	// scales it each second by the shape engine's business factor (diurnal x weekly
	// x volume). Consumers then take what is visible, one message per receive, but
	// never faster than receives_per_second, which becomes the maximum consumer
	// throughput: a backlog forms only while scaled arrivals exceed it and drains
	// once they fall back. Receives follow arrivals, so idle consumers add no
	// empty-receive polling. Without a shape engine the factor is 1.
	Load string `yaml:"load"`
	// Burst, when declared, concentrates the queue's arrivals into the on-fraction of each
	// period instead of spreading them evenly, as an upstream batch pipeline delivers: the same
	// mean volume (arrivals_per_second, scaled by load when that is business) arrives in the first
	// duty x period of each period, offset by phase, and nothing arrives in the rest. Periods are
	// aligned to the Unix epoch on the tick wall clock, so the on-window needs no state and a
	// restart or a master pause resumes on the same schedule. Receives are unchanged, so a backlog
	// forms during a burst while arrivals exceed receives_per_second and drains afterwards.
	// Omitted (the default) leaves arrivals evenly spread. A queue bound to a resource rejects it.
	Burst *Burst `yaml:"burst"`
	// Enrichment selects only the P2 key vocabulary. All values are declared;
	// omitted keys stay absent. P1/P3 tag alternatives cannot be union-filled.
	Enrichment Enrichment `yaml:"enrichment"`
	// Resource binds the queue to a declared `queue` resource (environments[].resources). A bound
	// queue is driven by the causal model: NumberOfMessagesSent is the window's arrivals,
	// NumberOfMessagesReceived and NumberOfMessagesDeleted are what the consumers served,
	// ApproximateNumberOfMessagesVisible is the backlog and ApproximateAgeOfOldestMessage the
	// oldest message's age. The resource owns arrival rate, consumer throughput and processing
	// time, so arrivals_per_second, receives_per_second, processing_seconds, delay_seconds, load and
	// burst must be omitted, and the resource must not set max_depth (a standard queue is
	// unbounded). NotVisible, Delayed and EmptyReceives are not emitted for a bound queue.
	Resource string `yaml:"resource"`
}

// Burst declares periodic batch arrivals. The on-window of period n is
// [n x period + phase, n x period + phase + duty x period) from the Unix epoch.
type Burst struct {
	// Period is the repeat interval as a Go duration, 1s to 168h ("30m" gives two bursts an hour).
	Period string `yaml:"period"`
	// Duty is the on-fraction of each period, in (0,1]. Arrivals are concentrated into it, so
	// the on-window rate is arrivals_per_second / duty; 1 is a continuous stream.
	Duty float64 `yaml:"duty"`
	// Phase offsets the start of the on-window within the period as a Go duration,
	// 0 <= phase < period (default "": the burst starts on each epoch-aligned period boundary).
	Phase string `yaml:"phase"`
}

const (
	minBurstPeriod = time.Second
	maxBurstPeriod = 168 * time.Hour
)

// burstWindow is a validated Burst on integer nanoseconds.
type burstWindow struct{ period, on, phase int64 }

// compile validates a declared burst against the queue's own arrival rate.
func (b *Burst) compile(q Queue) (*burstWindow, error) {
	period, err := time.ParseDuration(b.Period)
	if err != nil {
		return nil, fmt.Errorf("sqs %s: burst.period %q: %w", q.Name, b.Period, err)
	}
	if period < minBurstPeriod || period > maxBurstPeriod {
		return nil, fmt.Errorf("sqs %s: burst.period must be %s..%s, got %s", q.Name, minBurstPeriod, maxBurstPeriod, period)
	}
	if math.IsNaN(b.Duty) || b.Duty <= 0 || b.Duty > 1 {
		return nil, fmt.Errorf("sqs %s: burst.duty must be in (0,1], got %v", q.Name, b.Duty)
	}
	var phase time.Duration
	if b.Phase != "" {
		if phase, err = time.ParseDuration(b.Phase); err != nil {
			return nil, fmt.Errorf("sqs %s: burst.phase %q: %w", q.Name, b.Phase, err)
		}
	}
	if phase < 0 || phase >= period {
		return nil, fmt.Errorf("sqs %s: burst.phase must be in [0,period), got %s", q.Name, phase)
	}
	if q.ArrivalsPerSecond <= 0 {
		return nil, fmt.Errorf("sqs %s: burst needs a positive arrivals_per_second to concentrate", q.Name)
	}
	if q.ArrivalsPerSecond/b.Duty > 1e6 {
		return nil, fmt.Errorf("sqs %s: burst on-window rate arrivals_per_second/duty must be at most 1000000", q.Name)
	}
	on := int64(math.Round(b.Duty * float64(period)))
	if on < 1 {
		on = 1
	}
	return &burstWindow{period: int64(period), on: on, phase: int64(phase)}, nil
}

// cumulativeOn is the total on-time from the epoch up to wall.
func (b *burstWindow) cumulativeOn(wall time.Time) int64 {
	x := wall.UnixNano() - b.phase
	k := x / b.period
	r := x % b.period
	if r < 0 {
		k--
		r += b.period
	}
	return k*b.on + min(r, b.on)
}

// factor is the arrival multiplier for the one-second step ending at wall: the share of that
// second inside the on-window, scaled by period/on so the mean over a period is unchanged.
func (b *burstWindow) factor(wall time.Time) float64 {
	overlap := b.cumulativeOn(wall) - b.cumulativeOn(wall.Add(-time.Second))
	return float64(overlap) / float64(time.Second) * float64(b.period) / float64(b.on)
}

// Enrichment is optional collector metadata, never an AWS dimension.
type Enrichment struct {
	AssertsEnv  string `yaml:"asserts_env"`
	AssertsSite string `yaml:"asserts_site"`
	Service     string `yaml:"service"`
	Environment string `yaml:"environment"`
	Owner       string `yaml:"owner"`
	Purpose     string `yaml:"purpose"`
	Terraform   string `yaml:"terraform"`
}

// loadBusiness is the only non-default Load value; empty or loadConstant is the default.
const (
	loadBusiness = "business"
	loadConstant = "constant"
)

func Registration() core.ConstructReg {
	return core.ConstructReg{Kind: "sqs", Doc: "AWS/SQS ordinary queue metrics (remote-write only)", Scope: core.ScopeBlueprint, NewConfig: func() any { return &Config{} }, Build: Build, FailureModes: causal.FailureModes}
}

type cohort struct {
	born, ready time.Time
	count       float64
}
type queueState struct {
	cfg                      Queue
	labels                   map[string]string
	delayed, visible, flying cohortQueue
	arrival, drain           float64
	activeUntil              time.Time
	active                   bool
	// res is the resolved name of the bound causal resource ("" for an unbound queue); the
	// carries keep sub-message residuals of the model's fluid counts across windows.
	res                    string
	sentCarry, servedCarry float64
	// burst is the compiled on-window of an unbound queue that declares one, else nil.
	burst *burstWindow
}

type Construct struct {
	model     *causal.Model // nil unless a queue is bound to a resource
	queues    []queueState
	last      time.Time
	started   bool
	paused    bool          // an observed pause requires a fresh positive boundary before accrual
	pausedFor time.Duration // active-time model excludes master-paused intervals
}

var (
	_ core.Construct  = (*Construct)(nil)
	_ causal.Advancer = (*Construct)(nil)
)

// AdvanceCausal implements causal.Advancer: the composition root advances the model on every
// master tick so bound consumers sample live faults at the same instants.
func (c *Construct) AdvanceCausal(now time.Time, clk causal.Clock) {
	if c.model != nil {
		c.model.Advance(now, clk)
	}
}

func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfg.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("sqs: want *Config, got %T", cfg)
	}
	if fx == nil || fx.Cloud == nil || strings.TrimSpace(fx.Cloud.AccountID) == "" || strings.TrimSpace(fx.Cloud.Region) == "" {
		return nil, fmt.Errorf("sqs: requires cloud account_id and region")
	}
	if fx.Cloud.CloudWatchExportMode() != "remote_write" {
		return nil, fmt.Errorf("sqs: CloudWatch export %q unsupported; initial consumer supports remote_write only (otlp not admitted)", fx.Cloud.CloudWatchExportMode())
	}
	if len(c.Queues) == 0 || len(c.Queues) > 64 {
		return nil, fmt.Errorf("sqs: declare 1..64 queues")
	}
	out := &Construct{}
	seen := map[string]bool{}
	collectors := map[string]bool{}
	boundBy := map[string]string{}
	for _, q := range c.Queues {
		if strings.TrimSpace(q.Name) == "" || strings.TrimSpace(q.CollectorName) == "" || q.CollectorName == "global" {
			return nil, fmt.Errorf("sqs: queue name and explicit non-global collector_name required")
		}
		if seen[q.Name] || collectors[q.CollectorName] {
			return nil, fmt.Errorf("sqs: duplicate queue or collector identity %q", q.Name)
		}
		seen[q.Name] = true
		collectors[q.CollectorName] = true
		for key, v := range map[string]float64{"arrivals_per_second": q.ArrivalsPerSecond, "receives_per_second": q.ReceivesPerSecond} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e6 {
				return nil, fmt.Errorf("sqs %s: %s must be finite in [0,1000000]", q.Name, key)
			}
		}
		if math.IsNaN(q.MessageBytes) || math.IsInf(q.MessageBytes, 0) || q.MessageBytes != math.Trunc(q.MessageBytes) || q.MessageBytes < 0 || q.MessageBytes > 1048576 || q.ArrivalsPerSecond > 0 && q.MessageBytes < 1 {
			return nil, fmt.Errorf("sqs %s: message_bytes must be whole finite bytes, 1..1048576 for nonzero arrivals", q.Name)
		}
		res := ""
		if q.Resource != "" {
			var err error
			if res, err = c.bind(q, fx, boundBy); err != nil {
				return nil, err
			}
		}
		if q.Load != "" && q.Load != loadConstant && q.Load != loadBusiness {
			return nil, fmt.Errorf("sqs %s: load must be empty, %q or %q, got %q", q.Name, loadConstant, loadBusiness, q.Load)
		}
		var burst *burstWindow
		if q.Burst != nil {
			var err error
			if burst, err = q.Burst.compile(q); err != nil {
				return nil, err
			}
		}
		if q.DelaySeconds < 0 || q.DelaySeconds > 900 || q.ProcessingSeconds < 0 || q.ProcessingSeconds > 43200 {
			return nil, fmt.Errorf("sqs %s: delay_seconds must be 0..900, processing_seconds 0..43200", q.Name)
		}
		labels := map[string]string{"account_id": fx.Cloud.AccountID, "region": fx.Cloud.Region, "namespace": "AWS/SQS", "job": "cloud/aws/sqs", "name": q.CollectorName, "dimension_QueueName": q.Name}
		e := q.Enrichment
		for k, v := range map[string]string{"asserts_env": e.AssertsEnv, "asserts_site": e.AssertsSite, "service": e.Service, "tag_Environment": e.Environment, "tag_Owner": e.Owner, "tag_Purpose": e.Purpose, "tag_Terraform": e.Terraform} {
			if strings.TrimSpace(v) != "" {
				labels[k] = v
			}
		}
		out.queues = append(out.queues, queueState{cfg: q, labels: labels, res: res, burst: burst})
	}
	if len(boundBy) > 0 {
		m, err := causal.Compile(c.set)
		if err != nil {
			return nil, fmt.Errorf("sqs: resources: %w", err)
		}
		out.model = m
	}
	return out, nil
}

// bind validates a queue's resource binding and returns the resolved resource name. The
// resource owns the queue's traffic and consumers, so every field that would state them again
// is rejected rather than silently ignored.
func (c *Config) bind(q Queue, fx *fixture.Set, boundBy map[string]string) (string, error) {
	if c.set.Empty() {
		return "", fmt.Errorf("sqs %s: resource %q requires environments[].resources to declare it", q.Name, q.Resource)
	}
	env := ""
	if fx.Env != nil {
		env = fx.Env.Name
	}
	r, _, ok := c.set.Resolve(q.Resource, env)
	if !ok {
		return "", fmt.Errorf("sqs %s: resource %q names no declared resource", q.Name, q.Resource)
	}
	if r.Kind != causal.KindQueue {
		return "", fmt.Errorf("sqs %s: resource %q is a %s, not a queue", q.Name, r.Name, r.Kind)
	}
	if r.MaxDepth != 0 {
		return "", fmt.Errorf("sqs %s: resource %q sets max_depth, which is not SQS behaviour (a standard queue is unbounded); omit it", q.Name, r.Name)
	}
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"arrivals_per_second", q.ArrivalsPerSecond != 0},
		{"receives_per_second", q.ReceivesPerSecond != 0},
		{"processing_seconds", q.ProcessingSeconds != 0},
		{"delay_seconds", q.DelaySeconds != 0},
		{"load", q.Load != ""},
		{"burst", q.Burst != nil},
	} {
		if f.set {
			return "", fmt.Errorf("sqs %s: %s must be omitted on a queue bound to resource %q (the resource owns it)", q.Name, f.name, r.Name)
		}
	}
	if q.MessageBytes < 1 {
		return "", fmt.Errorf("sqs %s: message_bytes must be 1..1048576 on a bound queue", q.Name)
	}
	if other, dup := boundBy[r.Name]; dup {
		return "", fmt.Errorf("sqs %s: resource %q is already bound by queue %q", q.Name, r.Name, other)
	}
	boundBy[r.Name] = q.Name
	return r.Name, nil
}
func (*Construct) Kind() string                { return "sqs" }
func (*Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (*Construct) Interval() time.Duration     { return time.Minute }

// Tick observes at most one cadence window. It never replays missed windows;
// duplicate/backward timestamps do not advance the model or re-emit old periods.
// First observation models the preceding minute, as an explicit startup assumption,
// unless a pause was observed. The first positive observation after a pause only
// establishes a resume boundary; no part of that unknown interval accrues activity.
// Master pause freezes all queue mechanics (including cohort age/deadlines), keeps
// fractional active residuals, and advances only the wall-clock observation fence.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	if c.started && !now.After(c.last) {
		return nil
	}
	if w != nil && w.Shape != nil && w.Shape.Paused() {
		if c.started {
			c.pausedFor += now.Sub(c.last)
		}
		c.last = now
		c.started = true
		c.paused = true
		return nil
	}
	if c.paused {
		// The transition happened at an unknown point since the last paused
		// observation. Freeze that entire interval too, including cohort clocks.
		c.pausedFor += now.Sub(c.last)
		c.last = now
		c.paused = false
		return nil
	}
	if w == nil || w.Metrics == nil {
		return fmt.Errorf("sqs: metric writer required")
	}
	if c.model != nil {
		if w.Shape == nil {
			return fmt.Errorf("sqs: a queue bound to a resource requires the shape engine")
		}
		c.model.Advance(now, w.Shape)
	}
	start := now.Add(-time.Minute)
	if c.started && c.last.After(start) {
		start = c.last
	}
	// Subsecond scheduler calls accumulate until there is a whole observation second.
	seconds := int(now.Sub(start) / time.Second)
	if seconds == 0 {
		return nil
	}
	end := start.Add(time.Duration(seconds) * time.Second)
	window := start                 // bound queues read the model on the wall clock
	start = start.Add(-c.pausedFor) // cohort deadlines live on the unpaused clock
	st := state.NewState()          // sparse output: never retain a prior period's observations
	factors := c.businessFactors(w, start, seconds)
	for i := range c.queues {
		q := &c.queues[i]
		var age, delayed, invisible, visible cw.StatSet
		var sent, received, deleted, empty, size cw.StatSet
		bound := q.res != ""
		if bound {
			c.observeBound(q, window, end, &age, &visible, &sent, &received, &deleted, &size)
		}
		for s := 1; !bound && s <= seconds; s++ {
			at := start.Add(time.Duration(s) * time.Second)
			// Default SQS retention is four days. Expiration is not a delete call.
			q.delayed.expire(at)
			q.visible.expire(at)
			q.flying.expire(at)
			var completed float64
			for b := q.flying.front(); b != nil && !b.ready.After(at); b = q.flying.front() {
				// Reconcile completed stock after an elapsed gap, but do not
				// attribute its prior delete call to this observed window.
				if b.ready.After(start) {
					completed += b.count
				}
				q.flying.take(b.count)
			}
			arrivalRate := q.cfg.ArrivalsPerSecond
			business := q.cfg.Load == loadBusiness
			if business {
				arrivalRate *= factors[s-1]
			}
			if q.burst != nil {
				// The burst clock is the tick wall clock: the unpaused cohort clock plus every
				// master-paused interval, like the business factor.
				arrivalRate *= q.burst.factor(at.Add(c.pausedFor))
			}
			arrivals := whole(&q.arrival, arrivalRate)
			if arrivals > 0 {
				q.delayed.push(cohort{born: at, ready: at.Add(time.Duration(q.cfg.DelaySeconds) * time.Second), count: arrivals})
			}
			for b := q.delayed.front(); b != nil && !b.ready.After(at); b = q.delayed.front() {
				q.visible.push(q.delayed.take(b.count))
			}
			// Each token is an attempted one-message ReceiveMessage call. Whole
			// idle tokens become empty receives, not banked future service capacity.
			polls := whole(&q.drain, q.cfg.ReceivesPerSecond)
			if business {
				// Consumers poll for what is visible, up to their throughput ceiling.
				polls = math.Min(polls, q.visible.total)
			}
			remaining := polls
			var deliveries float64
			for q.visible.front() != nil && remaining > 0 {
				b := q.visible.take(remaining)
				remaining -= b.count
				deliveries += b.count
				if q.cfg.ProcessingSeconds == 0 {
					completed += b.count
				} else {
					b.ready = at.Add(time.Duration(q.cfg.ProcessingSeconds) * time.Second)
					q.flying.push(b)
				}
			}
			if arrivals > 0 || polls > 0 {
				q.activeUntil = at.Add(6 * time.Hour)
				q.active = true
			}
			d, v, f := q.delayed.total, q.visible.total, q.flying.total
			var oldest float64
			for _, stock := range []*cohortQueue{&q.delayed, &q.visible, &q.flying} {
				if b := stock.front(); b != nil {
					oldest = math.Max(oldest, at.Sub(b.born).Seconds())
				}
			}
			// The synthetic sampler observes stock once per active second. These
			// are observations, not invented AWS period/sample-count guarantees.
			if d+v+f > 0 || q.active && !at.After(q.activeUntil) {
				observe(&age, oldest, 1)
				observe(&delayed, d, 1)
				observe(&visible, v, 1)
				observe(&invisible, f, 1)
			}
			// Event counters observe one value of 1 per successful call/message.
			// Size instead observes bytes once per sent message, never bytes as count.
			observe(&sent, 1, arrivals)
			observe(&received, 1, deliveries)
			observe(&deleted, 1, completed)
			observe(&empty, 1, remaining)
			observe(&size, q.cfg.MessageBytes, arrivals)
		}
		if age.SampleCount > 0 {
			cw.EmitStats(st, "aws_sqs_approximate_age_of_oldest_message", q.labels, age)
		}
		if delayed.SampleCount > 0 {
			cw.EmitStats(st, "aws_sqs_approximate_number_of_messages_delayed", q.labels, delayed)
		}
		if invisible.SampleCount > 0 {
			cw.EmitStats(st, "aws_sqs_approximate_number_of_messages_not_visible", q.labels, invisible)
		}
		if visible.SampleCount > 0 {
			cw.EmitStats(st, "aws_sqs_approximate_number_of_messages_visible", q.labels, visible)
		}
		if empty.SampleCount > 0 {
			cw.EmitStats(st, "aws_sqs_number_of_empty_receives", q.labels, empty)
		}
		if deleted.SampleCount > 0 {
			cw.EmitStats(st, "aws_sqs_number_of_messages_deleted", q.labels, deleted)
		}
		if received.SampleCount > 0 {
			cw.EmitStats(st, "aws_sqs_number_of_messages_received", q.labels, received)
		}
		if sent.SampleCount > 0 {
			cw.EmitStats(st, "aws_sqs_number_of_messages_sent", q.labels, sent)
		}
		if size.SampleCount > 0 {
			cw.EmitStats(st, "aws_sqs_sent_message_size", q.labels, size)
		}
	}
	c.last = end
	c.started = true
	batch := st.Collect(now)
	if len(batch) == 0 {
		return nil
	}
	return w.Metrics.Write(ctx, batch)
}

// observeBound fills a bound queue's statistics for the wall-clock window (from, to] from the
// causal model: the window's arrivals are the sends, what the consumers served is both received
// and deleted (the resource has no processing time or retries), and backlog and oldest age are
// sampled at every grid step in the window. Counts keep their fractional residual across
// windows so no message is invented or lost. Delayed, NotVisible and EmptyReceives have no
// sourced derivation from the model and stay unobserved.
func (c *Construct) observeBound(q *queueState, from, to time.Time, age, visible, sent, received, deleted, size *cw.StatSet) {
	win, ok := c.model.Window(q.res, from, to)
	if !ok {
		return
	}
	arrivals := whole(&q.sentCarry, win.Arrivals)
	served := whole(&q.servedCarry, win.Served)
	observe(sent, 1, arrivals)
	observe(received, 1, served)
	observe(deleted, 1, served)
	observe(size, q.cfg.MessageBytes, arrivals)
	if arrivals > 0 || served > 0 {
		q.activeUntil = to.Add(6 * time.Hour)
		q.active = true
	}
	for at := from.Truncate(causal.Step).Add(causal.Step); !at.After(to); at = at.Add(causal.Step) {
		st, ok := c.model.State(q.res, at)
		if !ok {
			continue
		}
		if st.Backlog > 0 || q.active && !at.After(q.activeUntil) {
			observe(age, math.Round(st.OldestAgeSec), 1)
			observe(visible, math.Round(st.Backlog), 1)
		}
	}
}

// businessFactors returns the per-second business factor for the window
// (start, start+seconds] when any queue follows the business curve, else nil.
// start is on the unpaused clock, so master-paused time is added back to
// reach the wall clock the shape engine is defined on. Without a shape engine
// the factor is 1 (constant load).
func (c *Construct) businessFactors(w *core.World, start time.Time, seconds int) []float64 {
	has := false
	for i := range c.queues {
		if c.queues[i].cfg.Load == loadBusiness {
			has = true
			break
		}
	}
	if !has {
		return nil
	}
	f := make([]float64, seconds)
	for s := range f {
		f[s] = 1
		if w != nil && w.Shape != nil {
			f[s] = w.Shape.BusinessFactor(start.Add(c.pausedFor + time.Duration(s+1)*time.Second))
		}
	}
	return f
}

// whole preserves sub-message residuals across observation windows. The small
// tolerance only repairs floating-point boundary error (e.g. 120 * 1/120).
func whole(residual *float64, rate float64) float64 {
	x := *residual + rate
	n := math.Floor(x + 1e-9)
	*residual = math.Max(0, x-n)
	return n
}

func observe(s *cw.StatSet, value, count float64) {
	if count == 0 {
		return
	}
	if s.SampleCount == 0 {
		s.Minimum = value
		s.Maximum = value
	} else {
		s.Minimum = math.Min(s.Minimum, value)
		s.Maximum = math.Max(s.Maximum, value)
	}
	s.Sum += value * count
	s.SampleCount += count
	s.Average = s.Sum / s.SampleCount
}

// Cohorts are FIFO by birth time, and within each stage by ready time. Processing
// is amortized O(cohorts transitioned), not O(backlog) per second. Four-day retention
// and one-second observations bound retained cohorts independently of message rate.
type cohortQueue struct {
	batches []cohort
	head    int
	total   float64
}

func (q *cohortQueue) front() *cohort {
	if q.head == len(q.batches) {
		return nil
	}
	return &q.batches[q.head]
}
func (q *cohortQueue) push(b cohort) { q.batches = append(q.batches, b); q.total += b.count }
func (q *cohortQueue) take(limit float64) cohort {
	b := *q.front()
	b.count = math.Min(b.count, limit)
	q.total -= b.count
	q.batches[q.head].count -= b.count
	if q.batches[q.head].count == 0 {
		q.head++
	}
	if q.head == len(q.batches) {
		q.batches = q.batches[:0]
		q.head = 0
	} else if q.head >= 1024 && q.head >= len(q.batches)/2 {
		q.batches = append(q.batches[:0], q.batches[q.head:]...)
		q.head = 0
	}
	return b
}
func (q *cohortQueue) expire(now time.Time) {
	for b := q.front(); b != nil && now.Sub(b.born) >= 4*24*time.Hour; b = q.front() {
		q.take(b.count)
	}
}
