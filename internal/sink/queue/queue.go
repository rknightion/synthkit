// SPDX-License-Identifier: AGPL-3.0-only

// Package queue decouples synthetic-telemetry delivery from the tick: a construct's
// Write enqueues (non-blocking under capacity) and background senders batch-and-ship.
// Payload-agnostic and stdlib-only — it never imports the OTel SDK, constructs, or
// blueprints, so it stays on the synthetic-data side of the self-obs isolation seam.
package queue

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Options configures one queue. Zero values fall back to defaults (see withDefaults).
type Options struct {
	Shards   int           // parallel ordered senders (SEND_SHARDS, default 8)
	BatchMax int           // items per send; 1 metric series = 1 item (SEND_BATCH_MAX, default 5000)
	Deadline time.Duration // partial-batch flush deadline (SEND_BATCH_DEADLINE, default 5s)
	Capacity int           // total buffered items before backpressure (SEND_QUEUE_CAPACITY, default 200000)
	Sink     string        // "promrw"|"loki"|"otlp"|"pyroscope" — selfobs/log attribution
}

func (o Options) withDefaults() Options {
	if o.Shards <= 0 {
		o.Shards = 8
	}
	if o.BatchMax <= 0 {
		o.BatchMax = 5000
	}
	if o.Deadline <= 0 {
		o.Deadline = 5 * time.Second
	}
	if o.Capacity <= 0 {
		o.Capacity = 200000
	}
	return o
}

// Observer is the stdlib-only seam for the queue events not visible via pushhook. It imports
// neither the OTel SDK nor any construct/blueprint package, so the synthetic-delivery queue can
// report its operational events WITHOUT pulling the self-obs SDK onto the synthetic-data side.
type Observer interface {
	// EnqueueBlocked fires when a tick blocked enqueuing because a shard buffer was full
	// (backpressure). d is the time spent blocked.
	EnqueueBlocked(sink string, d time.Duration)
	// FlushObserved fires once per completed flush of a shard's pending batch: items is the count
	// shipped, d the wall-clock of the flush call(s), err the joined flush error (nil on success).
	// Called on the sender's own goroutine AFTER the (unchanged) synthetic flush; a nil observer
	// is never called.
	FlushObserved(sink string, items int, d time.Duration, err error)
}

// Queue buffers items of type T between produce (tick) and deliver (sink).
type Queue[T any] struct {
	opts  Options
	flush func(context.Context, []T) error
	shard func(T) uint64
	obs   Observer // may be nil

	chans    []chan T
	flushReq []chan chan error // per-shard synchronous-flush barrier (used by Flush)
	pending  int64             // atomic: enqueued-but-not-yet-flushed, for Depth()

	startOnce sync.Once
	stopOnce  sync.Once
	stop      chan struct{}
	wg        sync.WaitGroup

	logMu   sync.Mutex
	lastLog time.Time
}

// New builds a queue. flush ships a batch via the real sink; shard returns a stable
// routing key (same identity → same ordered sender). obs may be nil.
func New[T any](opts Options, flush func(context.Context, []T) error, shard func(T) uint64, obs Observer) *Queue[T] {
	opts = opts.withDefaults()
	q := &Queue[T]{opts: opts, flush: flush, shard: shard, obs: obs, stop: make(chan struct{})}
	perShard := opts.Capacity / opts.Shards
	if perShard < 1 {
		perShard = 1
	}
	q.chans = make([]chan T, opts.Shards)
	q.flushReq = make([]chan chan error, opts.Shards)
	for i := range q.chans {
		q.chans[i] = make(chan T, perShard)
		q.flushReq[i] = make(chan chan error) // unbuffered: a Flush barrier rendezvous
	}
	return q
}

// Write routes each item to shard(item) % Shards, blocking only when a shard is at
// capacity (backpressure). Satisfies core.{Metric,Log,Trace,Pyroscope}Writer.
func (q *Queue[T]) Write(ctx context.Context, batch []T) error {
	for _, item := range batch {
		ch := q.chans[int(q.shard(item)%uint64(q.opts.Shards))]
		// Increment BEFORE the send so a sender can never decrement (on flush) an item the
		// producer hasn't counted yet — Depth() would otherwise transiently read negative.
		// Rolled back below only if the enqueue never happens (ctx cancelled while blocked).
		atomic.AddInt64(&q.pending, 1)
		select {
		case ch <- item: // fast path
			continue
		default:
		}
		start := time.Now()
		select {
		case ch <- item:
		case <-ctx.Done():
			atomic.AddInt64(&q.pending, -1) // never enqueued
			return ctx.Err()
		}
		blocked := time.Since(start)
		if q.obs != nil {
			q.obs.EnqueueBlocked(q.opts.Sink, blocked)
		}
		q.warnFull(blocked)
	}
	return nil
}

// Depth is the current enqueued-but-unflushed item count (for a selfobs observable gauge).
func (q *Queue[T]) Depth() int { return int(atomic.LoadInt64(&q.pending)) }

// SetObserver sets the backpressure observer. Must be called before Start/Run/Write
// (set-once-before-use, mirroring the runner's tick/cycle observer contract); nil allowed.
// The runner uses this to inject self-obs, which is constructed after the runner.
func (q *Queue[T]) SetObserver(obs Observer) { q.obs = obs }

const warnCooldown = 30 * time.Second

// warnFull logs a rate-limited WARN the first time a shard blocks and at most once per
// cooldown thereafter — a full queue means delivery is slower than generation.
func (q *Queue[T]) warnFull(blocked time.Duration) {
	q.logMu.Lock()
	defer q.logMu.Unlock()
	if !q.lastLog.IsZero() && time.Since(q.lastLog) < warnCooldown {
		return
	}
	q.lastLog = time.Now()
	log.Printf("queue: %s sink full — enqueue blocked %v (delivery slower than generation; applying backpressure)", q.opts.Sink, blocked)
}

// Start launches the sender goroutines once (idempotent).
func (q *Queue[T]) Start() {
	q.startOnce.Do(func() {
		for i := range q.chans {
			q.wg.Add(1)
			go func(shard int) { defer q.wg.Done(); q.sender(shard) }(i)
		}
	})
}

// sender accumulates items for one shard and flushes on BatchMax or Deadline. It also
// services synchronous Flush barriers (RunOnce) and the stop signal (shutdown). Items for
// a given identity always reach the same sender, so per-identity order is preserved
// (protects cumulative counters / I3).
func (q *Queue[T]) sender(shard int) {
	ch := q.chans[shard]
	pending := make([]T, 0, q.opts.BatchMax)
	timer := time.NewTimer(q.opts.Deadline)
	defer timer.Stop()

	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(q.opts.Deadline)
	}
	// drainAndFlush pulls every item currently buffered in ch into pending, then ships it
	// all. Returns the joined flush error. Safe because callers ensure no concurrent Write
	// is in flight at a barrier (RunOnce ticks synchronously before Flush) — and even if
	// items arrive after, they are simply caught by the next flush.
	drainAndFlush := func() error {
		for {
			select {
			case item := <-ch:
				pending = append(pending, item)
			default:
				return q.flushPending(&pending)
			}
		}
	}

	for {
		select {
		case item := <-ch:
			pending = append(pending, item)
			if len(pending) >= q.opts.BatchMax {
				_ = q.flushPending(&pending)
				resetTimer()
			}
		case <-timer.C:
			_ = q.flushPending(&pending)
			timer.Reset(q.opts.Deadline)
		case ack := <-q.flushReq[shard]:
			ack <- drainAndFlush() // synchronous barrier: report errors back to Flush
			resetTimer()
		case <-q.stop:
			_ = drainAndFlush() // final flush on shutdown; errors already logged
			return
		}
	}
}

// flushPending ships *pending in ≤BatchMax slices, sequentially (preserves order),
// decrements Depth by what it shipped, and resets the slice. Errors are logged here (the
// live path) and also returned (so the Flush barrier can surface them to RunOnce). The
// real sink owns its own HTTP timeout + retry, so flush uses a background context that
// outlives a cancelled run context (matters during shutdown drain).
func (q *Queue[T]) flushPending(pending *[]T) error {
	items := *pending
	if len(items) == 0 {
		return nil
	}
	var errs []error
	var start0 time.Time
	if q.obs != nil {
		start0 = time.Now()
	}
	for start := 0; start < len(items); start += q.opts.BatchMax {
		end := start + q.opts.BatchMax
		if end > len(items) {
			end = len(items)
		}
		if err := q.flush(context.Background(), items[start:end]); err != nil {
			log.Printf("queue: %s sink flush of %d items failed: %v", q.opts.Sink, end-start, err)
			errs = append(errs, err)
		}
	}
	atomic.AddInt64(&q.pending, -int64(len(items)))
	joined := errors.Join(errs...)
	// Self-obs flush observation (depth/latency/batch + flush span). Additive and guarded — the
	// synthetic flush above is byte-for-byte unchanged whether or not an observer is set.
	if q.obs != nil {
		q.obs.FlushObserved(q.opts.Sink, len(items), time.Since(start0), joined)
	}
	*pending = items[:0]
	return joined
}

// Drain stops the senders after flushing pending items, bounded by ctx. Idempotent.
func (q *Queue[T]) Drain(ctx context.Context) {
	q.stopOnce.Do(func() { close(q.stop) })
	done := make(chan struct{})
	go func() { q.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		log.Printf("queue: %s sink drain exceeded deadline", q.opts.Sink)
	}
}

// Run starts the senders and blocks until ctx is cancelled, then drains within a short
// detached deadline (drainDeadline; ≤0 ⇒ 30s) so the last buffered items still ship on
// shutdown. The runner calls this on its own goroutine joined to the runner WaitGroup.
func (q *Queue[T]) Run(ctx context.Context, drainDeadline time.Duration) {
	q.Start()
	<-ctx.Done()
	if drainDeadline <= 0 {
		drainDeadline = 30 * time.Second
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), drainDeadline)
	defer cancel()
	q.Drain(drainCtx)
}

// Flush synchronously ships everything currently buffered and returns the joined flush
// error, WITHOUT stopping the senders — so the queue stays reusable (RunOnce can be called
// again). Callers must ensure no Write is concurrently in flight (RunOnce ticks
// synchronously before calling Flush). Bounded by ctx.
func (q *Queue[T]) Flush(ctx context.Context) error {
	q.Start() // idempotent: senders must run to service the barrier
	acks := make([]chan error, len(q.flushReq))
	for i := range q.flushReq {
		ack := make(chan error, 1)
		acks[i] = ack
		select {
		case q.flushReq[i] <- ack:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var errs []error
	for _, ack := range acks {
		select {
		case err := <-ack:
			errs = append(errs, err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return errors.Join(errs...)
}
