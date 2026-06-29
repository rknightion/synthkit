// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/sink/faro"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/sink/queue"
)

func TestShardSeriesStableAndLabelOrderIndependent(t *testing.T) {
	a := promrw.Series{Name: "m", Labels: map[string]string{"x": "1", "y": "2"}}
	b := promrw.Series{Name: "m", Labels: map[string]string{"y": "2", "x": "1"}} // same identity, different map order
	c := promrw.Series{Name: "m", Labels: map[string]string{"x": "1", "y": "3"}} // different value
	if shardSeries(a) != shardSeries(b) {
		t.Fatal("shardSeries not stable across label map ordering")
	}
	// fnv64a of these fixed, distinct vectors does not collide; asserting inequality catches a
	// shardSeries that ignores label VALUES (a real bug). Do NOT t.Skip here.
	if shardSeries(a) == shardSeries(c) {
		t.Fatalf("shardSeries ignores label values: a(%d)==c(%d)", shardSeries(a), shardSeries(c))
	}
}

// TestRunOnceFlushesQueueBeforeReturn proves the delivery queue is flushed synchronously
// before RunOnce returns — the capture sink must have received the constructs' series even
// though delivery is now asynchronous (background senders). If RunOnce returned before the
// Flush barrier completed, the capture would be empty.
func TestRunOnceFlushesQueueBeforeReturn(t *testing.T) {
	r, mc, _, _, _, _, _ := newTestRunner(t)
	if err := r.AddBlueprint(buildTestResolved("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := r.RunOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(mc.All()) == 0 {
		t.Fatal("capture sink empty after RunOnce — queue not flushed synchronously")
	}
}

// fakeRUMSink records every batch it receives (implements core.RUMSink).
type fakeRUMSink struct {
	mu      sync.Mutex
	batches [][]faro.Payload
}

func (f *fakeRUMSink) Write(_ context.Context, payloads []faro.Payload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]faro.Payload, len(payloads))
	copy(cp, payloads)
	f.batches = append(f.batches, cp)
	return nil
}

func (f *fakeRUMSink) all() []faro.Payload {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []faro.Payload
	for _, b := range f.batches {
		out = append(out, b...)
	}
	return out
}

// TestShardFaroSameSessionSameShard verifies that identical session ids always hash to
// the same shard key, and that distinct session ids (generally) produce different keys.
func TestShardFaroSameSessionSameShard(t *testing.T) {
	p1 := faro.Payload{Meta: faro.Meta{Session: faro.Session{ID: "sess-abc"}}}
	p2 := faro.Payload{Meta: faro.Meta{Session: faro.Session{ID: "sess-abc"}}}
	p3 := faro.Payload{Meta: faro.Meta{Session: faro.Session{ID: "sess-xyz"}}}
	if shardFaro(p1) != shardFaro(p2) {
		t.Fatal("shardFaro: same session id produced different shard keys")
	}
	// fnv64a of these two distinct strings does not collide in practice.
	if shardFaro(p1) == shardFaro(p3) {
		t.Fatalf("shardFaro: different session ids produced same shard key (%d)", shardFaro(p1))
	}
}

// TestFaroQueueRoundTrip builds a faro queue via queue.New (the same path buildQueues uses),
// enqueues two payloads, flushes, and asserts the fake sink received them.
func TestFaroQueueRoundTrip(t *testing.T) {
	sink := &fakeRUMSink{}
	opts := queue.Options{Shards: 2, BatchMax: 100, Deadline: 50 * time.Millisecond, Capacity: 1000, Sink: "faro"}
	q := queue.New[faro.Payload](opts, sink.Write, shardFaro, nil)
	q.Start()

	ctx := context.Background()
	payloads := []faro.Payload{
		{Meta: faro.Meta{Session: faro.Session{ID: "s1"}}},
		{Meta: faro.Meta{Session: faro.Session{ID: "s2"}}},
	}
	for _, p := range payloads {
		if err := q.Write(ctx, []faro.Payload{p}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := q.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	got := sink.all()
	if len(got) != 2 {
		t.Fatalf("expected 2 payloads received by fake sink, got %d", len(got))
	}
}

// TestQueueDepthsIncludesFaroKey verifies that QueueDepths returns a "faro" key when
// a RUM sink is present, and that eachQueue includes the RUM queue.
func TestQueueDepthsIncludesFaroKey(t *testing.T) {
	mc := &coretest.MetricCapture{}
	rum := &fakeRUMSink{}
	r := New(Sinks{Metrics: mc, RUM: rum}, testRegistry(&[]*fakeConstruct{}, &[]*fakeConstruct{}, &[]*fakeWorkload{}), Options{})

	depths := r.QueueDepths()
	if _, ok := depths["faro"]; !ok {
		t.Fatalf("QueueDepths missing 'faro' key when RUM sink is set; got keys: %v", depths)
	}

	qs := r.eachQueue()
	// Must include the RUM queue (r.queues.RUM != nil).
	if r.queues.RUM == nil {
		t.Fatal("r.queues.RUM is nil after buildQueues with a non-nil RUM sink")
	}
	// Verify the RUM queue appears in eachQueue output — it must be present for lifecycle management.
	found := false
	for _, q := range qs {
		if q == r.queues.RUM {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("eachQueue did not include the RUM queue (len=%d)", len(qs))
	}
}

// TestQueueDepthsNoFaroKeyWhenRUMAbsent confirms "faro" is absent when no RUM sink is configured.
func TestQueueDepthsNoFaroKeyWhenRUMAbsent(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t) // newTestRunner sets no RUM sink
	depths := r.QueueDepths()
	if _, ok := depths["faro"]; ok {
		t.Fatal("QueueDepths contains 'faro' key when no RUM sink is present")
	}
}
