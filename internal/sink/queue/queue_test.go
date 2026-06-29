// SPDX-License-Identifier: AGPL-3.0-only

package queue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// identityShard routes by a caller-supplied key so tests can force shard placement.
func identityShard(i int) func(int) uint64 { return func(int) uint64 { return uint64(i) } }

func TestWriteRoutesAndIsNonBlockingUnderCapacity(t *testing.T) {
	var mu sync.Mutex
	var got []int
	flush := func(_ context.Context, b []int) error { mu.Lock(); got = append(got, b...); mu.Unlock(); return nil }
	q := New[int](Options{Shards: 1, BatchMax: 100, Deadline: time.Hour, Capacity: 100, Sink: "test"},
		flush, func(int) uint64 { return 0 }, nil)
	q.Start()
	defer q.Drain(context.Background())

	if err := q.Write(context.Background(), []int{1, 2, 3}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Depth reflects queued-but-unflushed items immediately after Write.
	if d := q.Depth(); d < 0 || d > 3 {
		t.Fatalf("Depth=%d, want 0..3", d)
	}
}

func drainGot(mu *sync.Mutex, got *[]int) func() []int {
	return func() []int { mu.Lock(); defer mu.Unlock(); out := append([]int(nil), (*got)...); return out }
}

func TestFlushOnBatchMax(t *testing.T) {
	var mu sync.Mutex
	var batches [][]int
	flush := func(_ context.Context, b []int) error {
		mu.Lock()
		batches = append(batches, append([]int(nil), b...))
		mu.Unlock()
		return nil
	}
	q := New[int](Options{Shards: 1, BatchMax: 3, Deadline: time.Hour, Capacity: 100, Sink: "t"}, flush, func(int) uint64 { return 0 }, nil)
	q.Start()
	defer q.Drain(context.Background())

	_ = q.Write(context.Background(), []int{1, 2, 3, 4, 5, 6})
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(batches) >= 2 })
	mu.Lock()
	defer mu.Unlock()
	if len(batches[0]) != 3 || len(batches[1]) != 3 {
		t.Fatalf("want two 3-item batches, got %v", batches)
	}
}

func TestFlushOnDeadline(t *testing.T) {
	var mu sync.Mutex
	var batches [][]int
	flush := func(_ context.Context, b []int) error {
		mu.Lock()
		batches = append(batches, append([]int(nil), b...))
		mu.Unlock()
		return nil
	}
	q := New[int](Options{Shards: 1, BatchMax: 100, Deadline: 50 * time.Millisecond, Capacity: 100, Sink: "t"}, flush, func(int) uint64 { return 0 }, nil)
	q.Start()
	defer q.Drain(context.Background())

	_ = q.Write(context.Background(), []int{1, 2}) // below BatchMax — only the deadline can flush it
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(batches) == 1 && len(batches[0]) == 2 })
}

// waitFor polls cond up to 2s; fails the test on timeout.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

func TestPerShardOrderPreserved(t *testing.T) {
	var mu sync.Mutex
	var got []int
	flush := func(_ context.Context, b []int) error { mu.Lock(); got = append(got, b...); mu.Unlock(); return nil }
	// single shard so all items share one ordered sender
	q := New[int](Options{Shards: 1, BatchMax: 10, Deadline: 20 * time.Millisecond, Capacity: 1000, Sink: "t"}, flush, func(int) uint64 { return 0 }, nil)
	q.Start()
	in := make([]int, 100)
	for i := range in {
		in[i] = i
	}
	_ = q.Write(context.Background(), in)
	q.Drain(context.Background())
	mu.Lock()
	defer mu.Unlock()
	for i := range got {
		if got[i] != i {
			t.Fatalf("order broken at %d: %v", i, got[:i+1])
		}
	}
}

func TestDrainFlushesAllAndIsIdempotent(t *testing.T) {
	var mu sync.Mutex
	n := 0
	flush := func(_ context.Context, b []int) error { mu.Lock(); n += len(b); mu.Unlock(); return nil }
	q := New[int](Options{Shards: 2, BatchMax: 1000, Deadline: time.Hour, Capacity: 1000, Sink: "t"}, flush, func(v int) uint64 { return uint64(v) }, nil)
	q.Start()
	_ = q.Write(context.Background(), []int{1, 2, 3, 4, 5})
	q.Drain(context.Background())
	q.Drain(context.Background()) // second call must not panic/block
	mu.Lock()
	defer mu.Unlock()
	if n != 5 {
		t.Fatalf("drained %d items, want 5", n)
	}
}

func TestBackpressureBlocksWhenFull(t *testing.T) {
	release := make(chan struct{})
	var blockedObs int32
	flush := func(_ context.Context, _ []int) error { <-release; return nil } // stall the sender
	obs := testObs{blocked: func(string, time.Duration) { atomic.AddInt32(&blockedObs, 1) }}
	// Shards=1, Capacity=1 → channel holds 1; sender grabs 1 and stalls in flush; further Writes block.
	q := New[int](Options{Shards: 1, BatchMax: 1, Deadline: time.Hour, Capacity: 1, Sink: "t"}, flush, func(int) uint64 { return 0 }, obs)
	q.Start()

	done := make(chan struct{})
	go func() { _ = q.Write(context.Background(), []int{1, 2, 3}); close(done) }()
	select {
	case <-done:
		t.Fatal("Write returned while queue was full + sender stalled")
	case <-time.After(100 * time.Millisecond):
	}
	close(release) // let the sender drain
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Write never unblocked after sender resumed")
	}
	q.Drain(context.Background())
	if atomic.LoadInt32(&blockedObs) == 0 {
		t.Fatal("expected EnqueueBlocked to fire at least once")
	}
}

// testObs adapts callbacks to the Observer interface; either callback may be nil.
type testObs struct {
	blocked func(string, time.Duration)
	flushed func(string, int, time.Duration, error)
}

func (o testObs) EnqueueBlocked(sink string, d time.Duration) {
	if o.blocked != nil {
		o.blocked(sink, d)
	}
}

func (o testObs) FlushObserved(sink string, items int, d time.Duration, err error) {
	if o.flushed != nil {
		o.flushed(sink, items, d, err)
	}
}

func TestFlushObservedFiresWithItemsAndError(t *testing.T) {
	wantErr := errors.New("boom")
	var fail atomic.Bool
	flush := func(_ context.Context, _ []int) error {
		if fail.Load() {
			return wantErr
		}
		return nil
	}
	type obsRec struct {
		sink  string
		items int
		err   error
	}
	var mu sync.Mutex
	var recs []obsRec
	obs := testObs{flushed: func(sink string, items int, _ time.Duration, err error) {
		mu.Lock()
		defer mu.Unlock()
		recs = append(recs, obsRec{sink, items, err})
	}}
	q := New[int](Options{Shards: 1, BatchMax: 1000, Deadline: time.Hour, Capacity: 1000, Sink: "t"}, flush, func(int) uint64 { return 0 }, obs)

	// First flush: 3 items, success.
	if err := q.Write(context.Background(), []int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := q.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Second flush: errors propagate to the observer.
	fail.Store(true)
	if err := q.Write(context.Background(), []int{4, 5}); err != nil {
		t.Fatal(err)
	}
	_ = q.Flush(context.Background())
	q.Drain(context.Background())

	mu.Lock()
	defer mu.Unlock()
	// Empty drains (no pending) must NOT fire the observer — flushPending returns early on len 0.
	var nonEmpty []obsRec
	for _, r := range recs {
		if r.items > 0 {
			nonEmpty = append(nonEmpty, r)
		}
	}
	if len(nonEmpty) != 2 {
		t.Fatalf("want 2 non-empty flush observations, got %d (%+v)", len(nonEmpty), recs)
	}
	if nonEmpty[0].sink != "t" || nonEmpty[0].items != 3 || nonEmpty[0].err != nil {
		t.Fatalf("first flush obs wrong: %+v", nonEmpty[0])
	}
	if nonEmpty[1].items != 2 || nonEmpty[1].err == nil {
		t.Fatalf("second flush obs should carry items=2 + error: %+v", nonEmpty[1])
	}
}

func TestRunStartsAndDrainsOnContextCancel(t *testing.T) {
	var mu sync.Mutex
	n := 0
	flush := func(_ context.Context, b []int) error { mu.Lock(); n += len(b); mu.Unlock(); return nil }
	q := New[int](Options{Shards: 1, BatchMax: 1000, Deadline: time.Hour, Capacity: 1000, Sink: "t"}, flush, func(int) uint64 { return 0 }, nil)
	ctx, cancel := context.WithCancel(context.Background())
	ran := make(chan struct{})
	go func() { q.Run(ctx, time.Second); close(ran) }()
	_ = q.Write(context.Background(), []int{1, 2, 3})
	cancel()
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
	mu.Lock()
	defer mu.Unlock()
	if n != 3 {
		t.Fatalf("Run drained %d, want 3", n)
	}
}

func TestFlushIsSynchronousReusableAndReturnsErrors(t *testing.T) {
	var mu sync.Mutex
	n := 0
	failNext := false
	flush := func(_ context.Context, b []int) error {
		mu.Lock()
		defer mu.Unlock()
		if failNext {
			return errFlush
		}
		n += len(b)
		return nil
	}
	q := New[int](Options{Shards: 2, BatchMax: 1000, Deadline: time.Hour, Capacity: 1000, Sink: "t"}, flush, func(v int) uint64 { return uint64(v) }, nil)
	q.Start()
	defer q.Drain(context.Background())

	_ = q.Write(context.Background(), []int{1, 2, 3})
	if err := q.Flush(context.Background()); err != nil { // barrier: all 3 shipped, no stop
		t.Fatalf("Flush: %v", err)
	}
	mu.Lock()
	got := n
	mu.Unlock()
	if got != 3 {
		t.Fatalf("after first Flush n=%d, want 3", got)
	}
	// Queue is still usable — second round (proves NOT one-shot).
	_ = q.Write(context.Background(), []int{4, 5})
	if err := q.Flush(context.Background()); err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	mu.Lock()
	got = n
	mu.Unlock()
	if got != 5 {
		t.Fatalf("after second Flush n=%d, want 5", got)
	}
	// A failing flush surfaces through Flush's return.
	mu.Lock()
	failNext = true
	mu.Unlock()
	_ = q.Write(context.Background(), []int{6})
	if err := q.Flush(context.Background()); err == nil {
		t.Fatal("Flush returned nil despite a failing sink")
	}
}

var errFlush = errors.New("flush boom")

// Ensure drainGot helper compiles (used to suppress unused warning).
var _ = drainGot
