// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/pushhook"
	nativesigil "github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
	sigilsink "github.com/rknightion/synthkit/internal/sink/sigil"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/sink/faro"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/sink/queue"
)

// faroAttemptGate observes the actual per-attempt client.Do/response-processing
// lifetime, not the runner's outer whole-write admission or server scheduling.
type faroAttemptGate struct {
	ha.LeaderGate
	start, finish func()
}

func (g faroAttemptGate) Do(ctx context.Context, op ha.Operation, fn func(context.Context) error) error {
	return g.LeaderGate.Do(ctx, op, func(c context.Context) error {
		g.start()
		defer g.finish()
		return fn(c)
	})
}

func TestHAActualFaro5000WholeWrite(t *testing.T) {
	var posts, active, cancelled atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		active.Add(1)
		defer active.Add(-1)
		// Read through EOF so server-side disconnect detection can observe
		// cancellation; an unread POST body suppresses that notification.
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		select {
		case <-time.After(4 * time.Millisecond):
			w.WriteHeader(202)
		case <-r.Context().Done():
			cancelled.Add(1)
		}
	}))
	defer srv.Close()
	g := ha.NewGate()
	_ = g.Activate(context.Background(), func(context.Context) error { return nil })
	sink := faro.New(srv.URL, "key", false)
	var observation pushhook.Event
	sink.Observe = func(_ context.Context, event pushhook.Event) { observation = event }
	var mu sync.Mutex
	var admitted, completed int
	var joinedClient bool
	attempts := faroAttemptGate{
		LeaderGate: g,
		start: func() {
			mu.Lock()
			defer mu.Unlock()
			admitted++
			if joinedClient {
				t.Errorf("late client POST admission after queue join: %d", admitted)
			}
		},
		finish: func() {
			mu.Lock()
			defer mu.Unlock()
			completed++
		},
	}
	sink.SetDelivery(httpretry.Delivery{Gate: attempts, HTTPTimeout: time.Second, RetryMaxElapsed: 10 * time.Millisecond})
	r := New(Sinks{RUM: sink}, nil, Options{Gate: g, Delivery: ha.Bounded{Timeout: 80 * time.Millisecond, Margin: 200 * time.Millisecond, Crash: func() { t.Error("workers failed to join") }}, SendShards: 1, SendBatchMax: 5000, SendCapacity: 5000, SendDeadline: time.Hour})
	payloads := make([]faro.Payload, 5000)
	for i := range payloads {
		payloads[i] = faro.Payload{Meta: faro.Meta{Session: faro.Session{ID: "session"}}, Measurements: []faro.Measurement{{Type: "web-vitals", Values: map[string]float64{"lcp": 100}}}}
	}
	if err := r.queues.RUM.Write(context.Background(), payloads); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := r.Flush(context.Background()); err == nil {
		t.Fatal("5000-beacon fanout escaped outer deadline")
	}
	if time.Since(start) > 350*time.Millisecond {
		t.Fatal("whole write deadline restarted across waves")
	}
	joined, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.JoinQueues(joined); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	joinedClient = true
	clientPosts, clientCompleted := admitted, completed
	mu.Unlock()
	if clientPosts <= 0 || clientPosts >= 5000 || clientCompleted != clientPosts {
		t.Fatalf("client fanout not partial/joined: admitted=%d completed=%d", clientPosts, clientCompleted)
	}

	// Client join cannot synchronize independently scheduled server arrivals.
	// Keep the server alive through all admitted client attempts, then explicitly
	// join its handlers before taking the final HTTP accounting snapshot.
	srv.Close()
	n := posts.Load()
	if n <= 0 || n >= 5000 {
		t.Fatalf("actual fanout not partial: %d", n)
	}
	mu.Lock()
	finalAdmitted, finalCompleted := admitted, completed
	mu.Unlock()
	if finalAdmitted != clientPosts || finalCompleted != clientPosts || active.Load() != 0 {
		t.Fatalf("late POSTs/workers: before=%d after=%d completed=%d active=%d", clientPosts, finalAdmitted, finalCompleted, active.Load())
	}
	if observation.Items <= 0 || observation.Items > int(n) || observation.ErrorCode == "" {
		t.Fatalf("partial Faro outcome not truthful: observed=%+v HTTP=%d", observation, n)
	}
	t.Logf("5000 real beacons; admitted=%d completed=%d posted=%d confirmed=%d cancelled=%d; joined without later client POSTs", clientPosts, clientCompleted, n, observation.Items, cancelled.Load())
}

// sigilProofGate instruments the real admission callback, including response
// consumption. The whole-write and per-attempt instances observe different seams.
// Neither substitutes for the HTTP client or manufactures a successful result.
type sigilProofGate struct {
	ha.LeaderGate
	start  func(context.Context) context.Context
	finish func()
}

func (g sigilProofGate) Do(ctx context.Context, op ha.Operation, fn func(context.Context) error) error {
	return g.LeaderGate.Do(ctx, op, func(c context.Context) error {
		c = g.start(c)
		defer g.finish()
		return fn(c)
	})
}

func TestHAActualSigilThreeStagesShareDeadline(t *testing.T) {
	// Stage eligibility is no longer determined by two 70ms sleeps competing
	// for a 100ms budget. The partial proof cancels only after the real workflow
	// handler has drained its request; expiry is exercised separately without
	// requiring a first-stage success near the clock boundary.
	for _, scenario := range []string{"controlled-partial", "absolute-expiry"} {
		t.Run(scenario, func(t *testing.T) {
			partial := scenario == "controlled-partial"
			var mu sync.Mutex
			var paths []string
			var active, cancelled atomic.Int32
			workflowObserved := make(chan struct{}, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				active.Add(1)
				defer active.Add(-1)
				mu.Lock()
				paths = append(paths, req.URL.Path)
				mu.Unlock()
				// EOF is essential: server disconnect detection is not reliable
				// while an unread POST body remains. This is the receipt fence.
				if _, err := io.Copy(io.Discard, req.Body); err != nil {
					t.Errorf("drain real Sigil request: %v", err)
					return
				}
				if partial && req.URL.Path == "/api/v1/generations:export" {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				if req.URL.Path == "/api/v1/workflow-steps:export" {
					workflowObserved <- struct{}{}
				}
				// Never acknowledge the cancelled stage. No handler sleeps and
				// no sender-entry notification stand in for an HTTP receipt.
				<-req.Context().Done()
				cancelled.Add(1)
			}))
			defer srv.Close()
			g := ha.NewGate()
			defer g.Revoke()
			if err := g.Activate(context.Background(), func(context.Context) error { return nil }); err != nil {
				t.Fatal(err)
			}
			sink, err := sigilsink.New(srv.URL, "user", "token", false)
			if err != nil {
				t.Fatal(err)
			}
			var wholeDeadline time.Time
			var wholeContext context.Context
			var cancelWhole context.CancelFunc
			var wholeCalls, wholeCompleted, admitted, completed int
			var joinedClient bool
			whole := sigilProofGate{
				LeaderGate: g,
				start: func(c context.Context) context.Context {
					mu.Lock()
					defer mu.Unlock()
					// Add controlled cancellation, retaining the real Bounded
					// deadline. This does not reset or extend its 100ms budget.
					wholeContext, cancelWhole = context.WithCancel(c)
					wholeCalls++
					var ok bool
					wholeDeadline, ok = c.Deadline()
					if !ok {
						t.Error("whole write missing absolute deadline")
					}
					return wholeContext
				},
				finish: func() { mu.Lock(); wholeCompleted++; mu.Unlock() },
			}
			attempts := sigilProofGate{
				LeaderGate: g,
				start: func(c context.Context) context.Context {
					mu.Lock()
					defer mu.Unlock()
					admitted++
					if joinedClient {
						t.Error("late Sigil client admission after queue join")
					}
					deadline, ok := c.Deadline()
					if !ok || !deadline.Equal(wholeDeadline) {
						t.Errorf("attempt %d deadline reset: got %v (present=%t), whole-write %v", admitted, deadline, ok, wholeDeadline)
					}
					return c
				},
				finish: func() { mu.Lock(); completed++; mu.Unlock() },
			}
			sink.SetDelivery(httpretry.Delivery{Gate: attempts, HTTPTimeout: time.Second, RetryMaxElapsed: 10 * time.Millisecond})
			var observation pushhook.Event
			var observations int
			sink.Observe = func(_ context.Context, event pushhook.Event) {
				mu.Lock()
				defer mu.Unlock()
				observation = event
				observations++
			}
			r := New(Sinks{Sigil: sink}, nil, Options{Gate: whole, Delivery: ha.Bounded{Timeout: 100 * time.Millisecond, Margin: 100 * time.Millisecond, Crash: func() { t.Error("stage did not join") }}, SendShards: 1, SendDeadline: time.Hour})
			value := 1.0
			batch := []nativesigil.Export{{Generations: []nativesigil.Generation{{ID: "generation", OperationName: "generateText"}}, WorkflowSteps: []nativesigil.WorkflowStep{{ID: "step", StepName: "route"}}, Scores: []nativesigil.Score{{ScoreID: "score", GenerationID: "generation", Number: &value}}}}
			if err := r.queues.Sigil.Write(context.Background(), batch); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			flushed := make(chan error, 1)
			go func() { flushed <- r.Flush(context.Background()) }()
			if partial {
				select {
				case <-workflowObserved:
					// The generation response has been consumed and the actual
					// next HTTP request received, so cancellation is inside the
					// workflow attempt, not in the between-stage window.
					mu.Lock()
					cancelWhole()
					mu.Unlock()
				case err := <-flushed:
					t.Fatalf("write ended before real workflow receipt: %v", err)
				}
			}
			if err := <-flushed; err == nil {
				t.Fatal("three-stage write escaped cancellation/absolute deadline")
			}
			if elapsed := time.Since(start); elapsed > 220*time.Millisecond {
				t.Fatalf("deadline reset between stages: elapsed=%s", elapsed)
			}
			joined, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := r.JoinQueues(joined); err != nil {
				t.Fatal(err)
			}
			select {
			case <-r.queues.Sigil.Done():
			default:
				t.Fatal("Sigil queue join returned before sender exit")
			}
			mu.Lock()
			joinedClient = true
			clientPosts, clientCompleted := admitted, completed
			mu.Unlock()
			// Positive client completion is not a server handler join.
			srv.Close()
			mu.Lock()
			defer mu.Unlock()
			if wholeCalls != 1 || wholeCompleted != 1 || clientPosts == 0 || clientCompleted != clientPosts || admitted != clientPosts || completed != clientPosts || active.Load() != 0 {
				t.Fatalf("Sigil work not positively joined: whole=%d/%d client=%d/%d final=%d/%d active=%d", wholeCalls, wholeCompleted, clientPosts, clientCompleted, admitted, completed, active.Load())
			}
			wantItems, wantCode := 0, operationalerr.CodeTimeout
			if !partial && wholeContext.Err() != context.DeadlineExceeded {
				t.Fatalf("write did not reach actual absolute expiry: %v", wholeContext.Err())
			}
			if partial {
				wantCode = operationalerr.CodeCanceled
				wantItems = 1
				if len(paths) != 2 || paths[0] != "/api/v1/generations:export" || paths[1] != "/api/v1/workflow-steps:export" || admitted != 2 || cancelled.Load() != 1 {
					t.Fatalf("cancelled write launched later stage or omitted real stages: paths=%v admitted=%d cancelled=%d", paths, admitted, cancelled.Load())
				}
			} else if len(paths) > 1 || (len(paths) == 1 && paths[0] != "/api/v1/generations:export") || admitted != 1 {
				t.Fatalf("expired write admitted later stage: paths=%v admitted=%d", paths, admitted)
			}
			if observations != 1 || observation.Items != wantItems || observation.ErrorCode != wantCode {
				t.Fatalf("partial Sigil delivery not truthful: observations=%d event=%+v wantItems=%d wantCode=%s", observations, observation, wantItems, wantCode)
			}
			t.Logf("real HTTP paths=%v; whole deadline=%v; admitted=%d completed=%d confirmed=%d terminal=%s handlers joined", paths, wholeDeadline, admitted, completed, observation.Items, observation.ErrorCode)
		})
	}
}

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
