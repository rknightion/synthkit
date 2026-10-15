// SPDX-License-Identifier: AGPL-3.0-only

package queue

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type receiptObserver struct{ events chan FlushEvent }

func (o receiptObserver) EnqueueBlocked(string, time.Duration) {}
func (o receiptObserver) FlushObserved(e FlushEvent)           { o.events <- e }

func TestFlushRetainsCompletedBackgroundFailure(t *testing.T) {
	obs := receiptObserver{make(chan FlushEvent, 2)}
	var calls atomic.Int32
	q := New[int](Options{Shards: 1, Capacity: 2, BatchMax: 1, Deadline: time.Hour}, func(context.Context, []int) error {
		if calls.Add(1) == 1 {
			return errors.New("receiver rejected")
		}
		return nil
	}, func(int) uint64 { return 0 }, obs)
	q.Start()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	defer q.Drain(ctx)
	if err := q.Write(ctx, []int{1}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-obs.events:
		if event.Dropped != 1 {
			t.Fatal("fixture did not observe failed flush")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := q.Flush(ctx); err == nil {
		t.Fatal("barrier lost a completed background delivery failure")
	}
	if err := q.Write(ctx, []int{2}); err != nil {
		t.Fatal(err)
	}
	if err := q.Flush(ctx); err != nil {
		t.Fatalf("already reported error leaked into later successful barrier: %v", err)
	}
}
