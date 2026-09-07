// SPDX-License-Identifier: AGPL-3.0-only

// Package fleetstatus folds FM controller outcomes into an aggregate health snapshot for the
// control plane's TELEMETRY panel. It is a SECOND consumer of the fleethook seam (selfobs is
// the first); it imports only stdlib + fleethook, keeping the OTel-SDK ban intact — mirroring
// internal/pushstatus' relationship to internal/pushhook.
package fleetstatus

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/fleet"
	"github.com/rknightion/synthkit/internal/fleethook"
	"github.com/rknightion/synthkit/internal/operationalerr"
)

// FleetStat is the aggregate FM lifecycle health across every collector the controller drives.
// One row in the panel; there is no per-collector breakdown (the panel wants a single FM line).
type FleetStat struct {
	Registered        int                 `json:"registered"`           // collectors currently registered (register OK, not since unregistered)
	HeartbeatHealthy  int                 `json:"heartbeat_healthy"`    // registered collectors whose latest heartbeat succeeded
	Heartbeats        int64               `json:"heartbeats"`           // total heartbeat attempts
	Failures          int64               `json:"failures"`             // failed ops (register/heartbeat/unregister)
	LastOKMs          int64               `json:"last_ok_ms"`           // last successful op
	LastHeartbeatOKMs int64               `json:"last_heartbeat_ok_ms"` // last successful heartbeat
	LastErrorMs       int64               `json:"last_error_ms"`        // last failed op
	LastError         string              `json:"last_error"`
	LastErrorCode     operationalerr.Code `json:"last_error_code"`
	DryRun            bool                `json:"dry_run"`
	Collectors        []CollectorStat     `json:"collectors"`
}

// CollectorStat is the sanitized current diagnostic for one active synthetic collector. It
// contains lifecycle facts and a bounded configuration digest, never local attributes, raw
// configuration, remote hash values, or credentials.
type CollectorStat struct {
	ID                    string             `json:"id"`
	Registered            bool               `json:"registered"`
	HeartbeatHealthy      bool               `json:"heartbeat_healthy"`
	LastHeartbeatOKMs     int64              `json:"last_heartbeat_ok_ms"`
	ReceiptState          fleet.ReceiptState `json:"receipt_state"`
	LastReceiptObservedMs int64              `json:"last_receipt_observed_ms"`
	LastConfigReceiptMs   int64              `json:"last_config_receipt_ms"`
	ConfigDigest          string             `json:"config_digest,omitempty"`
}

// Store folds fleethook events into an aggregate FleetStat. Safe for concurrent observe/read.
// One store is shared by every blueprint's controller (a single FM panel row), so the registration
// map is keyed by collector ID alone — collector IDs are seeded per cluster (fleetmgmt.Roster) and
// assumed globally unique across blueprints. If that ever stops holding, key by blueprint+collector.
type Store struct {
	mu          sync.Mutex
	registered  map[string]bool // per-collector registration state, to derive the live count
	heartbeatOK map[string]bool // latest heartbeat outcome for each registered collector
	collectors  map[string]CollectorStat
	stat        FleetStat
	now         func() time.Time
}

// NewStore returns an empty store using the wall clock.
func NewStore() *Store {
	return &Store{registered: map[string]bool{}, heartbeatOK: map[string]bool{}, collectors: map[string]CollectorStat{}, now: time.Now}
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
		code := operationalerr.Normalize(ev.ErrorCode)
		if code != operationalerr.CodeNone {
			s.stat.Failures++
			s.stat.LastErrorMs = ms
			s.stat.LastErrorCode = code
			s.stat.LastError = operationalerr.Message(code)
		} else {
			s.stat.LastOKMs = ms
		}
		switch ev.Op {
		case fleethook.OpRegister:
			if code == operationalerr.CodeNone {
				s.registered[ev.Collector] = true
				s.heartbeatOK[ev.Collector] = false
				s.collectors[ev.Collector] = CollectorStat{ID: ev.Collector, Registered: true, ReceiptState: fleet.ReceiptUnavailable}
			}
		case fleethook.OpHeartbeat:
			s.stat.Heartbeats++
			collector := s.collectors[ev.Collector]
			if code == operationalerr.CodeNone {
				s.heartbeatOK[ev.Collector] = true
				s.stat.LastHeartbeatOKMs = ms
				collector.HeartbeatHealthy = true
				collector.LastHeartbeatOKMs = ms
			} else {
				s.heartbeatOK[ev.Collector] = false
				collector.HeartbeatHealthy = false
			}
			if s.registered[ev.Collector] {
				s.collectors[ev.Collector] = collector
			}
		case fleethook.OpUnregister:
			if code == operationalerr.CodeNone {
				delete(s.registered, ev.Collector)
				delete(s.heartbeatOK, ev.Collector)
				delete(s.collectors, ev.Collector)
			}
		}
		s.stat.Registered = len(s.registered)
		s.stat.HeartbeatHealthy = 0
		for collector := range s.registered {
			if s.heartbeatOK[collector] {
				s.stat.HeartbeatHealthy++
			}
		}
	}
}

// ReceiptObserver returns the configuration-delivery observer. It records a bounded digest only
// after a non-empty response; stale, unavailable, and failed checks retain the previous digest
// without claiming it was delivered again.
func (s *Store) ReceiptObserver() fleet.ReceiptObserver {
	return func(_ context.Context, collectorID string, receipt fleet.Receipt) {
		s.mu.Lock()
		defer s.mu.Unlock()
		collector, ok := s.collectors[collectorID]
		if !ok || !collector.Registered {
			return
		}
		ms := s.now().UnixMilli()
		collector.LastReceiptObservedMs = ms
		switch receipt.State {
		case fleet.ReceiptReceived:
			if isSHA256Digest(receipt.Digest) {
				collector.ReceiptState = fleet.ReceiptReceived
				collector.ConfigDigest = receipt.Digest
				collector.LastConfigReceiptMs = ms
			} else {
				collector.ReceiptState = fleet.ReceiptUnavailable
			}
		case fleet.ReceiptStale, fleet.ReceiptUnavailable, fleet.ReceiptError:
			collector.ReceiptState = receipt.State
		default:
			collector.ReceiptState = fleet.ReceiptUnavailable
		}
		s.collectors[collectorID] = collector
	}
}

// Snapshot returns a copy of the current aggregate FM health.
func (s *Store) Snapshot() FleetStat {
	s.mu.Lock()
	defer s.mu.Unlock()
	stat := s.stat
	stat.Collectors = make([]CollectorStat, 0, len(s.collectors))
	for _, collector := range s.collectors {
		stat.Collectors = append(stat.Collectors, collector)
	}
	sort.Slice(stat.Collectors, func(i, j int) bool { return stat.Collectors[i].ID < stat.Collectors[j].ID })
	return stat
}

func isSHA256Digest(value string) bool {
	if len(value) != sha256HexLength {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

const sha256HexLength = 64
