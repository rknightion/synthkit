// SPDX-License-Identifier: AGPL-3.0-only

package healthstatus

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTickAndCycleAggregation(t *testing.T) {
	s := NewStore()
	tf := s.TickFunc()
	ctx := context.Background()
	// two ok ticks + one error tick for the same construct
	_ = tf(ctx, "bpA", "cloudflare", "cf1", func(context.Context) error { return nil })
	_ = tf(ctx, "bpA", "cloudflare", "cf1", func(context.Context) error { return nil })
	_ = tf(ctx, "bpA", "cloudflare", "cf1", func(context.Context) error { return errors.New("boom") })
	s.ObserveCycle(ctx, "bpA", 1200*time.Millisecond, 2)

	snap := s.Snapshot()
	if len(snap.Constructs) != 1 {
		t.Fatalf("want 1 construct, got %d", len(snap.Constructs))
	}
	c := snap.Constructs[0]
	if c.Blueprint != "bpA" || c.Kind != "cloudflare" || c.Name != "cf1" {
		t.Fatalf("identity wrong: %+v", c)
	}
	if c.Ticks != 3 || c.Errors != 1 {
		t.Fatalf("want 3 ticks/1 error, got %d/%d", c.Ticks, c.Errors)
	}
	if c.LastOutcome != "error" || c.LastError != "boom" {
		t.Fatalf("last outcome/err wrong: %q %q", c.LastOutcome, c.LastError)
	}
	if len(snap.Blueprints) != 1 || snap.Blueprints[0].DroppedTicks != 2 {
		t.Fatalf("want dropped=2, got %+v", snap.Blueprints)
	}
}

func TestTickFuncCallsFnExactlyOnceAndReturnsErr(t *testing.T) {
	s := NewStore()
	calls := 0
	want := errors.New("x")
	got := s.TickFunc()(context.Background(), "b", "k", "n", func(context.Context) error { calls++; return want })
	if calls != 1 {
		t.Fatalf("fn called %d times, want 1", calls)
	}
	if got != want {
		t.Fatalf("err not returned unchanged")
	}
}
