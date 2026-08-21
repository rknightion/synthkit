// SPDX-License-Identifier: AGPL-3.0-only

package pushstatus

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/pushhook"
	"github.com/rknightion/synthkit/internal/sink/queue"
)

func TestDeliverySnapshotFreshConfiguredLanesAreNotGreen(t *testing.T) {
	s := NewStore()
	s.ConfigureLanes([]LaneConfig{
		{Name: "promrw", FreshAfter: time.Minute},
		{Name: "loki", Disabled: true, DisabledReason: "dry_run"},
	})

	lanes := byLane(s.SnapshotLanes())
	if got := lanes["promrw"]; !got.Configured || got.Disabled || got.Attempted || got.State != LaneNotAttempted || got.LiveReady {
		t.Fatalf("fresh configured lane must be waiting, got %+v", got)
	}
	if got := lanes["loki"]; !got.Configured || !got.Disabled || got.DisabledReason != "dry_run" || got.State != LaneDisabled || got.LiveReady {
		t.Fatalf("dry-run lane must be explicitly disabled, got %+v", got)
	}
}

func TestDeliverySnapshotFoldsFailureRecoveryAndStaleness(t *testing.T) {
	s := NewStore()
	s.ConfigureLanes([]LaneConfig{{Name: "promrw", FreshAfter: time.Minute}})
	obs := s.Observer()

	s.now = fixedNow(1_000)
	obs(context.Background(), pushhook.Event{Sink: "promrw", ErrorCode: operationalerr.CodeTransport})
	failed := byLane(s.SnapshotLanes())["promrw"]
	if !failed.Attempted || failed.LastAttemptMs != 1_000 || failed.LastError != operationalerr.Message(operationalerr.CodeTransport) || failed.State != LaneError || failed.LiveReady {
		t.Fatalf("failed attempt must be current error, got %+v", failed)
	}

	s.now = fixedNow(2_000)
	obs(context.Background(), pushhook.Event{Sink: "promrw", Status: 200, Items: 1})
	recovered := byLane(s.SnapshotLanes())["promrw"]
	if !recovered.Attempted || recovered.LastSuccessMs != 2_000 || recovered.CurrentError || recovered.Stale || recovered.State != LaneSuccess || !recovered.LiveReady {
		t.Fatalf("newer success must recover current error, got %+v", recovered)
	}

	s.now = fixedNow(62_001)
	stale := byLane(s.SnapshotLanes())["promrw"]
	if !stale.Stale || stale.State != LaneStaleSuccess || stale.LiveReady {
		t.Fatalf("success older than declared freshness must be stale, got %+v", stale)
	}
}

func TestDeliverySnapshotFailureAfterSuccessAtSameMillisecondIsCurrent(t *testing.T) {
	s := NewStore()
	s.ConfigureLanes([]LaneConfig{{Name: "promrw", FreshAfter: time.Minute}})
	s.now = fixedNow(1_000)
	obs := s.Observer()

	obs(context.Background(), pushhook.Event{Sink: "promrw", Status: 200, Items: 1})
	obs(context.Background(), pushhook.Event{Sink: "promrw", ErrorCode: operationalerr.CodeTransport})

	got := byLane(s.SnapshotLanes())["promrw"]
	if !got.CurrentError || got.State != LaneError || got.LiveReady {
		t.Fatalf("failure after same-millisecond success must be current error, got %+v", got)
	}
}

func TestDeliverySnapshotKeepsConfiguredStateWhenAnUnknownLaneReports(t *testing.T) {
	s := NewStore()
	s.ConfigureLanes([]LaneConfig{{Name: "promrw", FreshAfter: time.Minute}})
	s.now = fixedNow(1_000)
	s.Observer()(context.Background(), pushhook.Event{Sink: "loki", Status: 200})

	got := byLane(s.SnapshotLanes())["loki"]
	if got.Configured || got.State != LaneUnconfigured || got.LiveReady {
		t.Fatalf("an event must not invent configured readiness, got %+v", got)
	}
}

func TestQueueLossRecoveryIsAffectedShardAware(t *testing.T) {
	s := NewStore()
	s.now = fixedNow(1_000)
	s.EnqueueBlocked("promrw", time.Millisecond)
	s.FlushObserved(queue.FlushEvent{Sink: "promrw", Shard: 0, Sequence: 1, Attempted: 2, Dropped: 2, Code: operationalerr.CodeTransport})

	// A healthy unrelated shard cannot recover shard 0.
	s.now = fixedNow(2_000)
	s.FlushObserved(queue.FlushEvent{Sink: "promrw", Shard: 1, Sequence: 2, Attempted: 1})
	got := s.SnapshotQueues(map[string]int{"promrw": 7})[0]
	if !got.CurrentLoss || got.AffectedShards != 1 || got.DroppedItems != 2 || got.Depth != 7 || got.BlockedEnqueues != 1 {
		t.Fatalf("unrelated shard cleared or lost state: %+v", got)
	}

	// A second affected shard joins the loss set. Recovering shard 0 alone is insufficient.
	s.now = fixedNow(3_000)
	s.FlushObserved(queue.FlushEvent{Sink: "promrw", Shard: 1, Sequence: 3, Attempted: 1, Dropped: 1, Code: operationalerr.CodeRejected})
	s.now = fixedNow(4_000)
	s.FlushObserved(queue.FlushEvent{Sink: "promrw", Shard: 0, Sequence: 4, Attempted: 1})
	got = s.SnapshotQueues(nil)[0]
	if !got.CurrentLoss || got.AffectedShards != 1 || got.LastRecoveryMs != 0 {
		t.Fatalf("partial affected-shard recovery reported healthy: %+v", got)
	}

	// An out-of-order sequence for shard 1 cannot clear its later loss.
	s.FlushObserved(queue.FlushEvent{Sink: "promrw", Shard: 1, Sequence: 3, Attempted: 1})
	if got = s.SnapshotQueues(nil)[0]; !got.CurrentLoss {
		t.Fatalf("same-sequence success cleared loss: %+v", got)
	}

	s.now = fixedNow(5_000)
	s.FlushObserved(queue.FlushEvent{Sink: "promrw", Shard: 1, Sequence: 5, Attempted: 1})
	got = s.SnapshotQueues(nil)[0]
	if got.CurrentLoss || got.AffectedShards != 0 || got.DroppedItems != 3 || got.LastLossMs != 3_000 || got.LastRecoveryMs != 5_000 {
		t.Fatalf("full recovery did not preserve historical loss: %+v", got)
	}
}

func byLane(lanes []LaneStatus) map[string]LaneStatus {
	out := make(map[string]LaneStatus, len(lanes))
	for _, lane := range lanes {
		out[lane.Name] = lane
	}
	return out
}
