// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"errors"
	"hash/fnv"
	"io"
	"sort"
	"sync/atomic"
	"time"

	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/faro"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	pyroscope "github.com/rknightion/synthkit/internal/sink/pyroscope"
	"github.com/rknightion/synthkit/internal/sink/queue"
)

// queueSet holds the per-signal delivery queues (nil when the sink is absent). Each queue
// decorates the raw sink: a construct's Write enqueues; background senders batch-and-ship.
type queueSet struct {
	Metrics     *queue.Queue[promrw.Series]
	Logs        *queue.Queue[loki.Stream]
	Traces      *queue.Queue[otlp.Resource]
	Profiles    *queue.Queue[pyroscope.Series]
	RUM         *queue.Queue[faro.Payload]
	OTLPMetrics *queue.Queue[otlp.MetricResource]
	Sigil       *queue.Queue[sigil.Export]
}

// drainable is the lifecycle view of a queue, independent of its payload type.
// *queue.Queue[T] satisfies this (none of these methods mention T).
type drainable interface {
	Start()
	Run(ctx context.Context, drainDeadline time.Duration)
	Flush(ctx context.Context) error
	Drain(ctx context.Context)
	SetObserver(queue.Observer)
}

func hashStringMap(h io.Writer, m map[string]string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(m[k]))
		_, _ = h.Write([]byte{0})
	}
}

// shardSeries routes by metric identity (name + sorted labels) so consecutive snapshots of
// the same series always reach the same ordered sender — preserving the cumulative-counter
// timestamp order required by ARCHITECTURE I3.
func shardSeries(s promrw.Series) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s.Name))
	_, _ = h.Write([]byte{0})
	hashStringMap(h, s.Labels)
	return h.Sum64()
}

// shardStream routes by Loki stream identity (preserves per-stream line order).
func shardStream(s loki.Stream) uint64 {
	h := fnv.New64a()
	hashStringMap(h, s.Labels)
	return h.Sum64()
}

// shardProfile routes by Pyroscope series identity.
func shardProfile(s pyroscope.Series) uint64 {
	h := fnv.New64a()
	for _, lp := range s.Labels {
		_, _ = h.Write([]byte(lp.Name))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(lp.Value))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

// shardFaro routes by Faro browser session id so each session's beacons keep order
// (sessions are independent; inter-session ordering does not matter).
func shardFaro(p faro.Payload) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(p.Meta.Session.ID))
	return h.Sum64()
}

// shardSigil routes by conversation_id so a conversation's generations reach the same ordered
// sender (a conversation's generations should ingest in order; cross-conversation order is free).
func shardSigil(e sigil.Export) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(e.ConvKey))
	return h.Sum64()
}

// roundRobin distributes items evenly regardless of content — for order-independent signals
// (traces: Tempo spans carry no cross-resource order dependency).
func roundRobin[T any]() func(T) uint64 {
	var n uint64
	return func(T) uint64 { return atomic.AddUint64(&n, 1) }
}

// shardMetricResource routes by OTLP resource identity (service.name + service.namespace +
// blueprint) so successive cumulative snapshots of the same resource reach the same ordered
// sender — preserving cumulative-counter timestamp order (I3). Spans round-robin (order-free);
// OTLP cumulative metrics must not.
func shardMetricResource(r otlp.MetricResource) uint64 {
	h := fnv.New64a()
	for _, k := range []string{"service.name", "service.namespace", "blueprint"} {
		if v, ok := r.Attrs[k].(string); ok {
			_, _ = h.Write([]byte(v))
		}
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

func (r *Runner) baseQueueOpts() queue.Options {
	return queue.Options{
		Shards:   r.opts.SendShards,
		BatchMax: r.opts.SendBatchMax,
		Deadline: r.opts.SendDeadline,
		Capacity: r.opts.SendCapacity,
	}
}

// buildQueues wraps each present raw sink in a delivery queue. Called once from New, before
// AddBlueprint/buildWorld so the world wires the queue, not the raw sink.
func (r *Runner) buildQueues() {
	// obs is nil here: the queues are built in New, but self-obs is constructed AFTER the
	// runner (its gauges read runner state). The observer is injected later via
	// SetQueueObserver, before Run/RunOnce start the senders.
	o := r.baseQueueOpts()
	if r.sinks.Metrics != nil {
		mo := o
		mo.Sink = "promrw"
		r.queues.Metrics = queue.New[promrw.Series](mo, r.sinks.Metrics.Write, shardSeries, nil)
	}
	if r.sinks.Logs != nil {
		lo := o
		lo.Sink = "loki"
		r.queues.Logs = queue.New[loki.Stream](lo, r.sinks.Logs.Write, shardStream, nil)
	}
	if r.sinks.Traces != nil {
		to := o
		to.Sink = "otlp"
		r.queues.Traces = queue.New[otlp.Resource](to, r.sinks.Traces.Write, roundRobin[otlp.Resource](), nil)
	}
	if r.sinks.Profiles != nil {
		po := o
		po.Sink = "pyroscope"
		r.queues.Profiles = queue.New[pyroscope.Series](po, r.sinks.Profiles.Write, shardProfile, nil)
	}
	if r.sinks.RUM != nil {
		fo := o
		fo.Sink = "faro"
		r.queues.RUM = queue.New[faro.Payload](fo, r.sinks.RUM.Write, shardFaro, nil)
	}
	if r.sinks.OTLPMetrics != nil {
		omo := o
		omo.Sink = "otlpmetrics"
		omo.Deadline = 2 * time.Second // Alloy applicationObservability batch cadence
		r.queues.OTLPMetrics = queue.New[otlp.MetricResource](omo, r.sinks.OTLPMetrics.Write, shardMetricResource, nil)
	}
	if r.sinks.Sigil != nil {
		so := o
		so.Sink = "sigil"
		r.queues.Sigil = queue.New[sigil.Export](so, r.sinks.Sigil.Write, shardSigil, nil)
	}
}

// SetQueueObserver injects the delivery-queue backpressure observer (self-obs) into every
// queue. Set once before Run/RunOnce (same set-once-before-use contract as SetTickObserver);
// nil is a no-op. Lives here because self-obs is built after the runner.
func (r *Runner) SetQueueObserver(obs queue.Observer) {
	for _, q := range r.eachQueue() {
		q.SetObserver(obs)
	}
}

// eachQueue returns the present queues as drainables for lifecycle management.
func (r *Runner) eachQueue() []drainable {
	var ds []drainable
	if r.queues.Metrics != nil {
		ds = append(ds, r.queues.Metrics)
	}
	if r.queues.Logs != nil {
		ds = append(ds, r.queues.Logs)
	}
	if r.queues.Traces != nil {
		ds = append(ds, r.queues.Traces)
	}
	if r.queues.Profiles != nil {
		ds = append(ds, r.queues.Profiles)
	}
	if r.queues.RUM != nil {
		ds = append(ds, r.queues.RUM)
	}
	if r.queues.OTLPMetrics != nil {
		ds = append(ds, r.queues.OTLPMetrics)
	}
	if r.queues.Sigil != nil {
		ds = append(ds, r.queues.Sigil)
	}
	return ds
}

// startQueues starts every queue's senders (idempotent). Called by the entry points that
// enqueue outside the live Run loop (MasterTick/RunOnce) so a standalone caller never blocks
// on a full buffer with no sender draining it.
func (r *Runner) startQueues() {
	for _, q := range r.eachQueue() {
		q.Start()
	}
}

// Flush synchronously delivers everything enqueued so far across all queues and returns any
// push errors, keeping the queues reusable (unlike the shutdown drain). Callers that drive
// MasterTick/Tick directly (RunOnce, external batch drivers, tests) call this to force
// delivery; the live Run loop relies on the background senders instead.
func (r *Runner) Flush(ctx context.Context) error {
	var errs []error
	for _, q := range r.eachQueue() {
		if err := q.Flush(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// DrainQueues stops every delivery queue's senders and waits for the final flush, bounded by
// ctx. Use it to fully tear down a runner that was driven via RunOnce/MasterTick outside the
// live Run loop (e.g. a throwaway runner for offline projection) so its sender goroutines exit.
func (r *Runner) DrainQueues(ctx context.Context) {
	for _, q := range r.eachQueue() {
		q.Drain(ctx)
	}
}

// QueueDepths reports current queue depth per sink (for the selfobs depth gauge).
func (r *Runner) QueueDepths() map[string]int {
	m := map[string]int{}
	if r.queues.Metrics != nil {
		m["promrw"] = r.queues.Metrics.Depth()
	}
	if r.queues.Logs != nil {
		m["loki"] = r.queues.Logs.Depth()
	}
	if r.queues.Traces != nil {
		m["otlp"] = r.queues.Traces.Depth()
	}
	if r.queues.Profiles != nil {
		m["pyroscope"] = r.queues.Profiles.Depth()
	}
	if r.queues.RUM != nil {
		m["faro"] = r.queues.RUM.Depth()
	}
	if r.queues.OTLPMetrics != nil {
		m["otlpmetrics"] = r.queues.OTLPMetrics.Depth()
	}
	return m
}
