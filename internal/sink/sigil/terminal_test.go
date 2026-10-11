// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/pushhook"
	nativesigil "github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
)

// s124ConfirmedGate changes scheduling only: the real gate, HTTP client and
// response consumer must finish successfully before the whole-write context is
// canceled. It neither fabricates an HTTP result nor changes the returned error.
type s124ConfirmedGate struct {
	ha.LeaderGate
	handlerDone <-chan string
	cancel      context.CancelFunc
	cancelAfter int
	confirmed   int
	paths       []string
	deadlines   []time.Time
	entered     time.Time
	finished    time.Time
}

func (g *s124ConfirmedGate) Do(ctx context.Context, op ha.Operation, fn func(context.Context) error) error {
	if g.entered.IsZero() {
		g.entered = time.Now()
	}
	deadline, _ := ctx.Deadline()
	g.deadlines = append(g.deadlines, deadline)
	err := g.LeaderGate.Do(ctx, op, fn)
	if err != nil {
		return err
	}
	select {
	case path := <-g.handlerDone:
		g.paths = append(g.paths, path)
	case <-ctx.Done():
		return ctx.Err()
	}
	g.finished = time.Now()
	g.confirmed++
	if g.confirmed == g.cancelAfter {
		g.cancel()
	}
	return err
}

func TestWriteAggregateBetweenConfirmedStages(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cancelAfter int
	}{
		{"control", 0},
		{"after_generations", 1},
		{"after_workflow", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var reqs []capturedRequest
			// Reuse the existing fake-HTTP harness handler. Its original listener
			// stays idle; only the wrapping loopback server below receives traffic.
			harness := testServer(t, &reqs)
			defer harness.Close()
			handlerDone := make(chan string, 3)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				harness.Config.Handler.ServeHTTP(w, r)
				handlerDone <- r.URL.Path
			}))
			defer srv.Close()
			// Synthetic auth is generated at runtime and never logged.
			var secret [24]byte
			if _, err := rand.Read(secret[:]); err != nil {
				t.Fatal("runtime synthetic auth generation failed")
			}
			sink, err := New(srv.URL, "fixture", hex.EncodeToString(secret[:]), false)
			if err != nil {
				t.Fatal("local sink construction failed")
			}
			realGate := ha.NewGate()
			if err := realGate.Activate(ctx, func(context.Context) error { return nil }); err != nil {
				t.Fatal("local gate activation failed")
			}
			defer realGate.Revoke()
			gate := &s124ConfirmedGate{LeaderGate: realGate, handlerDone: handlerDone, cancel: cancel, cancelAfter: tc.cancelAfter}
			sink.SetDelivery(httpretry.Delivery{Gate: gate, HTTPTimeout: time.Second, RetryMaxElapsed: time.Millisecond})
			var events []pushhook.Event
			sink.Observe = func(_ context.Context, event pushhook.Event) { events = append(events, event) }
			value := 1.0
			batch := []nativesigil.Export{{
				Generations:   []nativesigil.Generation{{ID: "gen-001", OperationName: "generateText"}},
				WorkflowSteps: []nativesigil.WorkflowStep{{ID: "step-001", StepName: "route"}},
				Scores:        []nativesigil.Score{{ScoreID: "score-001", GenerationID: "gen-001", Number: &value}},
			}}
			started := time.Now()
			err = sink.Write(ctx, batch)
			elapsed := time.Since(started)
			wantCount := tc.cancelAfter
			wantCode := operationalerr.CodeCanceled
			if tc.cancelAfter == 0 {
				wantCount = 3
				wantCode = operationalerr.CodeNone
				if err != nil {
					t.Fatal("uncanceled control did not complete")
				}
			} else if err != context.Canceled || !errors.Is(err, context.Canceled) {
				t.Fatalf("expected bare context cancellation, got classified code=%s", operationalerr.CodeOf(err))
			}
			wantPaths := []string{"/api/v1/generations:export", "/api/v1/workflow-steps:export", "/api/v1/scores:export"}[:wantCount]
			if gate.confirmed != wantCount || len(reqs) != wantCount || !reflect.DeepEqual(gate.paths, wantPaths) {
				t.Fatalf("wrong real stages: confirmed=%d received=%d paths=%v", gate.confirmed, len(reqs), gate.paths)
			}
			deadline, _ := ctx.Deadline()
			for _, got := range gate.deadlines {
				if !got.Equal(deadline) {
					t.Fatal("whole-write deadline changed between stages")
				}
			}
			// A positive handler channel receipt synchronizes reqs reads; the gate
			// also proves actual HTTP response consumption, not sender entry.
			for i, req := range reqs {
				if req.path != wantPaths[i] || req.parsed == nil {
					t.Fatal("handler did not record the expected decoded stage")
				}
			}
			joined, stopJoin := context.WithTimeout(context.Background(), time.Second)
			defer stopJoin()
			if err := realGate.Wait(joined); err != nil {
				t.Fatal("real gate did not join")
			}
			srv.Close()
			t.Logf("confirmed=%d handler_records=%d later_stage_posts=0 bare_canceled=%t observations=%d code=%s", gate.confirmed, len(reqs), err == context.Canceled, len(events), operationalerr.CodeOf(err))
			// Every admitted write must report its terminal outcome, including
			// cancellation after an actually confirmed stage.
			if len(events) != 1 {
				t.Fatalf("aggregate omitted: got %d events; want one aggregate with confirmed_items=%d status=202 code=%s", len(events), wantCount, wantCode)
			}
			if events[0].Duration < gate.finished.Sub(gate.entered) || events[0].Duration <= 0 || events[0].Duration > elapsed {
				t.Fatal("aggregate duration does not cover the whole write")
			}
			if event := events[0]; event.Items != wantCount || event.Status != http.StatusAccepted || event.ErrorCode != wantCode {
				t.Fatalf("aggregate accounting mismatch: items=%d status=%d code=%s", event.Items, event.Status, event.ErrorCode)
			}
		})
	}
}
