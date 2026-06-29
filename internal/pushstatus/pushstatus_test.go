// SPDX-License-Identifier: AGPL-3.0-only

package pushstatus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/pushhook"
)

func fixedNow(ms int64) func() time.Time {
	return func() time.Time { return time.UnixMilli(ms) }
}

func TestObserverRecordsSuccessAndError(t *testing.T) {
	s := NewStore()
	s.now = fixedNow(1000)
	obs := s.Observer()

	obs(context.Background(), pushhook.Event{Sink: "promrw", Items: 12, Status: 200})
	s.now = fixedNow(2000)
	obs(context.Background(), pushhook.Event{Sink: "promrw", Status: 0, Err: errors.New("boom")})

	snap := s.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 sink, got %d", len(snap))
	}
	g := snap[0]
	if g.Sink != "promrw" || g.Pushes != 2 || g.Failures != 1 {
		t.Fatalf("counts wrong: %+v", g)
	}
	if g.LastSuccessMs != 1000 || g.LastErrorMs != 2000 || g.LastError != "boom" {
		t.Fatalf("timestamps/error wrong: %+v", g)
	}
}

func TestDryRunDoesNotCountAsPush(t *testing.T) {
	s := NewStore()
	s.now = fixedNow(5000)
	s.Observer()(context.Background(), pushhook.Event{Sink: "loki", Items: 3, DryRun: true})
	snap := s.Snapshot()
	if snap[0].Pushes != 0 || !snap[0].DryRun {
		t.Fatalf("dry-run mishandled: %+v", snap[0])
	}
}

func TestTotalsRateAndSparkline(t *testing.T) {
	s := NewStore()
	obs := s.Observer()

	// Three real pushes 5s apart: 10, 20, 30 items.
	s.now = fixedNow(0)
	obs(context.Background(), pushhook.Event{Sink: "promrw", Items: 10, Status: 200})
	s.now = fixedNow(5000)
	obs(context.Background(), pushhook.Event{Sink: "promrw", Items: 20, Status: 200})
	s.now = fixedNow(10000)
	obs(context.Background(), pushhook.Event{Sink: "promrw", Items: 30, Status: 200})

	g := s.Snapshot()[0]
	if g.TotalItems != 60 {
		t.Fatalf("TotalItems = %d, want 60", g.TotalItems)
	}
	// Rate counts only items delivered within the span (samples[1:] = 20+30 over 10s) → 300/min.
	// (Counting the leading 10 too would over-report: 60/10*60 = 360.)
	if g.RatePerMin != 300 {
		t.Fatalf("RatePerMin = %v, want 300", g.RatePerMin)
	}
	if len(g.Spark) != 3 || g.Spark[0] != 10 || g.Spark[2] != 30 {
		t.Fatalf("Spark = %v, want [10 20 30]", g.Spark)
	}
}

func TestSparklineCappedAndDryRunExcluded(t *testing.T) {
	s := NewStore()
	s.now = fixedNow(1000)
	obs := s.Observer()

	// A dry-run push must not enter the ring or totals.
	obs(context.Background(), pushhook.Event{Sink: "loki", Items: 99, DryRun: true})
	for range maxSamples + 10 {
		obs(context.Background(), pushhook.Event{Sink: "loki", Items: 1, Status: 200})
	}
	g := s.Snapshot()[0]
	if len(g.Spark) != maxSamples {
		t.Fatalf("Spark len = %d, want cap %d", len(g.Spark), maxSamples)
	}
	if g.TotalItems != int64(maxSamples+10) {
		t.Fatalf("TotalItems = %d, want %d (dry-run excluded)", g.TotalItems, maxSamples+10)
	}
}

func TestRateZeroWithSingleSample(t *testing.T) {
	s := NewStore()
	s.now = fixedNow(1000)
	s.Observer()(context.Background(), pushhook.Event{Sink: "otlp", Items: 5, Status: 200})
	if g := s.Snapshot()[0]; g.RatePerMin != 0 {
		t.Fatalf("RatePerMin = %v, want 0 for a single sample", g.RatePerMin)
	}
}

func TestSnapshotByBlueprint(t *testing.T) {
	s := NewStore()
	obs := s.Observer()
	ctx := context.Background()
	obs(ctx, pushhook.Event{Sink: "promrw", Blueprint: "bpA", Items: 10})
	obs(ctx, pushhook.Event{Sink: "promrw", Blueprint: "bpA", Items: 5})
	obs(ctx, pushhook.Event{Sink: "loki", Blueprint: "bpB", Items: 3})
	by := s.SnapshotByBlueprint()
	if by["bpA"].TotalItems != 15 || by["bpB"].TotalItems != 3 {
		t.Fatalf("per-blueprint totals wrong: %+v", by)
	}
}

func TestSnapshotSortedBySink(t *testing.T) {
	s := NewStore()
	obs := s.Observer()
	obs(context.Background(), pushhook.Event{Sink: "otlp"})
	obs(context.Background(), pushhook.Event{Sink: "faro"})
	obs(context.Background(), pushhook.Event{Sink: "loki"})
	snap := s.Snapshot()
	if snap[0].Sink != "faro" || snap[1].Sink != "loki" || snap[2].Sink != "otlp" {
		t.Fatalf("not sorted: %+v", snap)
	}
}
