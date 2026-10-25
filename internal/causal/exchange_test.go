// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"fmt"
	"testing"
	"time"
)

func exchangeSet() Set {
	return Set{Envs: []Declaration{{Env: "prod", Weight: 1, Resources: []Resource{
		{Name: "q", Kind: KindQueue, Size: 1, HoldMS: 100, Demand: &Demand{PeakRPS: 1, OffPeakRPS: 1}},
		{Name: "dep", Kind: KindDependency, LatencyMS: 10},
	}}}}
}

// Every copy of a Set reaches the same exchange; a separately declared Set with identical names
// reaches another; only a queue has one.
func TestQueueExchangeBelongsToItsSet(t *testing.T) {
	a, b := exchangeSet(), exchangeSet()
	copyOfA := a
	copyOfA.Loads = append(copyOfA.Loads, Load{Resource: "q", Share: 1})
	xa, ok := a.QueueExchange("q")
	if !ok {
		t.Fatal("no exchange for a queue")
	}
	if x, _ := copyOfA.QueueExchange("q"); x != xa {
		t.Fatal("a copy of the Set reached another exchange")
	}
	if xb, _ := b.QueueExchange("q"); xb == xa {
		t.Fatal("two Sets declaring the same names share an exchange")
	}
	if _, ok := a.QueueExchange("dep"); ok {
		t.Fatal("a dependency has an exchange")
	}
	if _, ok := a.QueueExchange("missing"); ok {
		t.Fatal("an unknown name has an exchange")
	}
}

// An exchange holds at most MaxPendingMessages: past that the oldest are dropped and the rest stay
// first in, first out; with no consumer it keeps none; Take honours the producer-start cutoff.
func TestExchangeBoundsItsBacklog(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := exchangeSet()
	x, _ := s.QueueExchange("q")
	x.Publish(Message{Span: "x"})
	if x.Pending() != 0 {
		t.Fatalf("an exchange with no consumer kept %d messages", x.Pending())
	}
	x.AddConsumer()
	extra := 10
	for i := range MaxPendingMessages + extra {
		x.Publish(Message{Start: now, Span: fmt.Sprintf("m%d", i)})
	}
	if x.Pending() != MaxPendingMessages {
		t.Fatalf("kept %d messages, want %d", x.Pending(), MaxPendingMessages)
	}
	if _, ok := x.Take(now.Add(-time.Second)); ok {
		t.Fatal("took a message published after the cutoff")
	}
	first, seen, ok := x.TakeWithWatermark(now)
	if !ok || first.Span != fmt.Sprintf("m%d", extra) {
		t.Fatalf("oldest message %+v, want m%d (the first %d dropped)", first, extra, extra)
	}
	if first.Sequence != uint64(extra+1) || seen != uint64(MaxPendingMessages+extra) {
		t.Fatalf("oldest sequence %d, watermark %d after bounded eviction", first.Sequence, seen)
	}
	// Another consumer can take a leftover without making a newer publication part of the
	// old observation. Publish owns the sequence even when passed a message from another take.
	x.AddConsumer()
	if _, ok := x.Take(now); !ok {
		t.Fatal("competing consumer could not take a leftover")
	}
	x.Publish(first)
	var last Message
	for {
		msg, ok := x.Take(now)
		if !ok {
			break
		}
		last = msg
	}
	if last.Span != first.Span || last.Sequence != seen+1 {
		t.Fatalf("republished message %+v, want the same content at a position after %d", last, seen)
	}
	// Emptying the backlog must not reset publication identity or the observed watermark.
	if _, mark, ok := x.TakeWithWatermark(now); ok || mark != last.Sequence {
		t.Fatalf("empty take returned watermark %d, ok %v, want %d false", mark, ok, last.Sequence)
	}
	x.Publish(Message{Start: now.Add(time.Second), Span: "future"})
	if _, mark, ok := x.TakeWithWatermark(now); ok || mark != last.Sequence+1 {
		t.Fatalf("future message cutoff/watermark = %d, %v", mark, ok)
	}
	if msg, _, ok := x.TakeWithWatermark(now.Add(time.Second)); !ok || msg.Span != "future" || msg.Sequence != last.Sequence+1 {
		t.Fatalf("future message after the cutoff %+v, ok %v", msg, ok)
	}
}
