// SPDX-License-Identifier: AGPL-3.0-only

package queue

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// Empty queues must not reserve payload storage for every potential item.
func TestIdleCapacityDoesNotReservePayloads(t *testing.T) {
	type payload [256]byte
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	q := New[payload](Options{Capacity: 100000}, func(context.Context, []payload) error { return nil }, func(payload) uint64 { return 0 }, nil)
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(q)
	if got := after.TotalAlloc - before.TotalAlloc; got > 4*1024*1024 {
		t.Fatalf("idle queue allocated %d bytes; want <=4 MiB for 100000 pointer slots and metadata", got)
	}
}

func BenchmarkPayloadDelivery(b *testing.B) {
	type payload struct {
		key  uint64
		data [248]byte
	}
	q := New[payload](Options{Capacity: 10000, BatchMax: 1000}, func(context.Context, []payload) error { return nil }, func(p payload) uint64 { return p.key }, nil)
	q.Start()
	defer q.Drain(context.Background())
	batch := make([]payload, 1000)
	for i := range batch {
		batch[i].key = uint64(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := q.Write(context.Background(), batch); err != nil {
			b.Fatal(err)
		}
		if err := q.Flush(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSkewedQueueRetention(t *testing.T) {
	type payload struct {
		key uint64
		pad [248]byte
	}
	release := make(chan struct{})
	var fast atomic.Int64
	q := New[payload](Options{Shards: 2, Capacity: 2048, BatchMax: 1}, func(_ context.Context, b []payload) error {
		if b[0].key == 0 {
			<-release
		} else {
			fast.Add(int64(len(b)))
		}
		return nil
	}, func(p payload) uint64 { return p.key }, nil)
	q.Start()
	defer func() {
		close(release)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := q.DrainJoined(ctx); err != nil {
			t.Errorf("drain: %v", err)
		}
	}()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	batch := make([]payload, 256)
	for i := 1; i < len(batch); i++ {
		batch[i].key = 1
	}
	for i := 0; i < 1000; i++ {
		if err := q.Write(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	for fast.Load() != 255000 {
		select {
		case <-deadline.C:
			t.Fatal("fast shard did not drain")
		default:
			runtime.Gosched()
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(q)
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("1000 blocked items after 255000 fast items drained: retained=%d bytes", retained)
	if retained > 4*1024*1024 {
		t.Fatalf("skew retains %d bytes for 1000 blocked 256-byte items", retained)
	}
}
