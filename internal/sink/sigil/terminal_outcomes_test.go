// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/pushhook"
	nativesigil "github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
	"github.com/rknightion/synthkit/internal/sink/queue"
)

func terminalSink(t *testing.T, endpoint string) *Sink {
	t.Helper()
	var secret [24]byte
	if _, err := rand.Read(secret[:]); err != nil {
		t.Fatal("synthetic auth generation failed")
	}
	s, err := New(endpoint, "fixture", hex.EncodeToString(secret[:]), false)
	if err != nil {
		t.Fatal("fixture sink construction failed")
	}
	s.SetDelivery(httpretry.Delivery{Gate: ha.AlwaysLeader{}, HTTPTimeout: time.Second, RetryMaxElapsed: time.Millisecond})
	return s
}

// Cancel on the first post-admission check, without modifying conversion or
// inventing a stage response. The embedded context owns cancellation identity.
type cancelOnCheck struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelOnCheck) Err() error {
	c.checks++
	if c.checks == 2 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestWriteTerminalAdmissionBoundary(t *testing.T) {
	for _, name := range []string{"pre_admission", "post_admission", "empty"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var input context.Context = ctx
			batch := []nativesigil.Export{{Generations: []nativesigil.Generation{{ID: "g"}}}}
			switch name {
			case "pre_admission":
				cancel()
			case "post_admission":
				input = &cancelOnCheck{Context: ctx, cancel: cancel}
			case "empty":
				batch = nil
			}
			var posts int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { posts++; w.WriteHeader(http.StatusAccepted) }))
			defer srv.Close()
			s := terminalSink(t, srv.URL)
			var events []pushhook.Event
			s.Observe = func(_ context.Context, e pushhook.Event) { events = append(events, e) }
			err := s.Write(input, batch)
			if name == "empty" {
				if err != nil {
					t.Fatal("empty write failed")
				}
			} else if err != context.Canceled {
				t.Fatal("cancellation identity lost")
			}
			if posts != 0 {
				t.Fatal("canceled or empty write posted")
			}
			if name != "post_admission" {
				if len(events) != 0 {
					t.Fatal("unadmitted or empty write observed")
				}
				return
			}
			if len(events) != 1 || events[0].Items != 0 || events[0].Status != 0 || events[0].ErrorCode != operationalerr.CodeCanceled || events[0].Duration <= 0 {
				t.Fatalf("wrong admitted cancellation aggregate: %+v", events)
			}
		})
	}
}

func TestWriteTerminalStageFailures(t *testing.T) {
	for _, name := range []string{"workflow_http", "workflow_marshal", "score_rejected"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var paths []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if name == "workflow_http" && r.URL.Path == "/api/v1/workflow-steps:export" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusAccepted)
				if r.URL.Path == "/api/v1/scores:export" {
					_, _ = w.Write([]byte(`{"results":[{"score_id":"score-001","status":"rejected"}],"rejected":1}`))
				}
			}))
			defer srv.Close()
			s := terminalSink(t, srv.URL)
			var events []pushhook.Event
			s.Observe = func(_ context.Context, e pushhook.Event) { events = append(events, e) }
			value := 1.0
			batch := []nativesigil.Export{{Generations: []nativesigil.Generation{{ID: "g"}}, WorkflowSteps: []nativesigil.WorkflowStep{{ID: "step"}}, Scores: []nativesigil.Score{{ScoreID: "score-001", Number: &value}}}}
			wantItems, wantStatus, wantPosts, wantCode := 1, http.StatusUnauthorized, 2, operationalerr.CodeAuthentication
			if name == "workflow_marshal" {
				batch[0].WorkflowSteps[0].StepName = string([]byte{0xff})
				wantStatus, wantPosts, wantCode = http.StatusAccepted, 1, operationalerr.CodeInternal
			}
			if name == "score_rejected" {
				wantItems, wantStatus, wantPosts, wantCode = 2, http.StatusAccepted, 3, operationalerr.CodeRejected
			}
			err := s.Write(ctx, batch)
			if operationalerr.CodeOf(err) != wantCode {
				t.Fatalf("terminal code=%s want=%s", operationalerr.CodeOf(err), wantCode)
			}
			if len(paths) != wantPosts || len(events) != 1 {
				t.Fatalf("posts=%d aggregates=%d", len(paths), len(events))
			}
			e := events[0]
			if e.Items != wantItems || e.Status != wantStatus || e.ErrorCode != wantCode || e.Duration <= 0 {
				t.Fatalf("wrong terminal aggregate: %+v", e)
			}
		})
	}
}

func TestWritePartialCancellationKeepsQueueEnvelopeLossSeparate(t *testing.T) {
	sendCtx, cancelSend := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelSend()
	barrierCtx, cancelBarrier := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelBarrier()
	handlerDone := make(chan string, 3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		handlerDone <- r.URL.Path
	}))
	defer srv.Close()
	s := terminalSink(t, srv.URL)
	realGate := ha.NewGate()
	if err := realGate.Activate(sendCtx, func(context.Context) error { return nil }); err != nil {
		t.Fatal("gate activation failed")
	}
	defer realGate.Revoke()
	gate := &s124ConfirmedGate{LeaderGate: realGate, handlerDone: handlerDone, cancel: cancelSend, cancelAfter: 1}
	s.SetDelivery(httpretry.Delivery{Gate: gate, HTTPTimeout: time.Second, RetryMaxElapsed: time.Millisecond})
	var aggregates []pushhook.Event
	s.Observe = func(_ context.Context, e pushhook.Event) { aggregates = append(aggregates, e) }
	var flushes []queue.FlushEvent
	q := queue.New(queue.Options{Shards: 1, BatchMax: 4, Deadline: time.Hour, Capacity: 4, Sink: "sigil"}, s.Write, func(nativesigil.Export) uint64 { return 0 }, queueObserver{flushed: func(e queue.FlushEvent) { flushes = append(flushes, e) }})
	// One envelope contains two confirmed logical items, plus one unattempted step.
	if err := q.Write(barrierCtx, []nativesigil.Export{{Generations: []nativesigil.Generation{{ID: "g1"}, {ID: "g2"}}, WorkflowSteps: []nativesigil.WorkflowStep{{ID: "step"}}}}); err != nil {
		t.Fatal("enqueue failed")
	}
	q.StartContext(sendCtx)
	defer q.Drain(barrierCtx)
	if err := q.Flush(barrierCtx); operationalerr.CodeOf(err) != operationalerr.CodeCanceled {
		t.Fatal("queue did not surface terminal cancellation")
	}
	if err := q.DrainJoined(barrierCtx); err != nil {
		t.Fatal("queue did not positively join")
	}
	if err := realGate.Wait(barrierCtx); err != nil {
		t.Fatal("gate did not positively join")
	}
	if len(aggregates) != 1 || aggregates[0].Items != 2 || aggregates[0].Status != http.StatusAccepted || aggregates[0].ErrorCode != operationalerr.CodeCanceled {
		t.Fatalf("wrong logical accounting: %+v", aggregates)
	}
	if len(flushes) != 1 || flushes[0].Attempted != 1 || flushes[0].Dropped != 1 || flushes[0].Code != operationalerr.CodeCanceled || q.Depth() != 0 {
		t.Fatalf("wrong envelope accounting: %+v", flushes)
	}
	if gate.confirmed != 1 || len(gate.paths) != 1 {
		t.Fatal("later stage admitted after cancellation")
	}
	if !errors.Is(sendCtx.Err(), context.Canceled) {
		t.Fatal("fixture cancellation did not fire")
	}
}
