// SPDX-License-Identifier: AGPL-3.0-only

// Package fleetstatus folds FM controller outcomes into an aggregate health snapshot for the
// control plane's TELEMETRY panel. It is a SECOND consumer of the fleethook seam (selfobs is
// the first); it imports only stdlib + fleethook, keeping the OTel-SDK ban intact — mirroring
// internal/pushstatus' relationship to internal/pushhook.
package fleetstatus

import (
	"context"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/fleethook"
)

// FleetStat is the aggregate FM lifecycle health across every collector the controller drives.
// One row in the panel; there is no per-collector breakdown (the panel wants a single FM line).
type FleetStat struct {
	Registered  int    `json:"registered"`    // collectors currently registered (register OK, not since unregistered)
	Heartbeats  int64  `json:"heartbeats"`    // total heartbeat attempts
	Failures    int64  `json:"failures"`      // failed ops (register/heartbeat/unregister)
	LastOKMs    int64  `json:"last_ok_ms"`    // last successful op
	LastErrorMs int64  `json:"last_error_ms"` // last failed op
	LastError   string `json:"last_error"`
	DryRun      bool   `json:"dry_run"`
}

// Store folds fleethook events into an aggregate FleetStat. Safe for concurrent observe/read.
// One store is shared by every blueprint's controller (a single FM panel row), so the registration
// map is keyed by collector ID alone — collector IDs are seeded per cluster (fleetmgmt.Roster) and
// assumed globally unique across blueprints. If that ever stops holding, key by blueprint+collector.
type Store struct {
	mu         sync.Mutex
	registered map[string]bool // per-collector registration state, to derive the live count
	stat       FleetStat
	now        func() time.Time
}

// NewStore returns an empty store using the wall clock.
func NewStore() *Store {
	return &Store{registered: map[string]bool{}, now: time.Now}
}

// Observer returns a fleethook.Observer that folds each FM event into the aggregate stat.
func (s *Store) Observer() fleethook.Observer {
	return func(_ context.Context, ev fleethook.Event) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if ev.DryRun {
			s.stat.DryRun = true
		}
		ms := s.now().UnixMilli()
		if ev.Err != nil {
			s.stat.Failures++
			s.stat.LastErrorMs = ms
			s.stat.LastError = ev.Err.Error()
		} else {
			s.stat.LastOKMs = ms
		}
		switch ev.Op {
		case fleethook.OpRegister:
			if ev.Err == nil {
				s.registered[ev.Collector] = true
			}
		case fleethook.OpHeartbeat:
			s.stat.Heartbeats++
		case fleethook.OpUnregister:
			if ev.Err == nil {
				delete(s.registered, ev.Collector)
			}
		}
		s.stat.Registered = len(s.registered)
	}
}

// Snapshot returns a copy of the current aggregate FM health.
func (s *Store) Snapshot() FleetStat {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stat
}
