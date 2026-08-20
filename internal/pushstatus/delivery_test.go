// SPDX-License-Identifier: AGPL-3.0-only

package pushstatus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/pushhook"
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
	obs(context.Background(), pushhook.Event{Sink: "promrw", Err: errors.New("dial refused")})
	failed := byLane(s.SnapshotLanes())["promrw"]
	if !failed.Attempted || failed.LastAttemptMs != 1_000 || failed.LastError != "dial refused" || failed.State != LaneError || failed.LiveReady {
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
	obs(context.Background(), pushhook.Event{Sink: "promrw", Err: errors.New("dial refused")})

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

func byLane(lanes []LaneStatus) map[string]LaneStatus {
	out := make(map[string]LaneStatus, len(lanes))
	for _, lane := range lanes {
		out[lane.Name] = lane
	}
	return out
}
