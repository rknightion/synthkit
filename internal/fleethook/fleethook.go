// SPDX-License-Identifier: AGPL-3.0-only

// Package fleethook defines the seam between the Fleet Management controller and the
// generator's self-observability — the FM counterpart to internal/pushhook. It is
// deliberately stdlib-only: it imports neither the OTel SDK nor any construct/workload/
// blueprint package, so the FM lifecycle driver (internal/fleet) can report registration
// and heartbeat outcomes WITHOUT pulling the self-observability SDK into its dependency graph.
//
// fleet.Controller exposes an Observe field of type Observer (default nil). When set — by
// package main, only when self-observability and/or the control-plane status view are wired —
// the controller calls it once per register/heartbeat/unregister outcome. When nil the
// lifecycle is byte-for-byte unchanged. internal/selfobs implements an Observer that records
// OTel metrics; internal/fleetstatus implements one that folds outcomes for the control panel.
package fleethook

import (
	"context"
	"time"
)

// Op names the controller operation that produced an Event.
const (
	OpRegister   = "register"
	OpHeartbeat  = "heartbeat"
	OpUnregister = "unregister"
)

// Event is the outcome of one FM controller operation against a single collector.
type Event struct {
	Collector string        // collector ID the operation targeted
	Op        string        // OpRegister | OpHeartbeat | OpUnregister
	Duration  time.Duration // wall-clock of the call (0 when not timed)
	DryRun    bool          // true = dry-run client (no HTTP happened)
	Err       error         // non-nil on failure
}

// Observer receives one FM Event. ctx carries any active trace context.
type Observer func(ctx context.Context, ev Event)
