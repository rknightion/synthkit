// SPDX-License-Identifier: AGPL-3.0-only

// Package fleet drives the Fleet Management API registration lifecycle for synthkit's
// fake collector fleet. It is the controller-side counterpart to the fleetmgmt construct:
// the construct emits Alloy self-metrics labelled with collector IDs derived from
// fleetmgmt.Roster; this package registers those same IDs with the FM API and keeps them
// alive via periodic GetConfig heartbeats.
//
// Design principles:
//   - No package-level mutable state: config is passed to the Controller at construction.
//   - DRY_RUN mode: when Config.DryRun is true the Client logs every call without I/O.
//   - Roster identity is delegated to fleetmgmt.Roster — never duplicated here.
package fleet

import (
	"context"
	"errors"

	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/fleethook"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
)

// Collector is the fleet's resolved fake collector identity. It is a direct alias
// of fleetmgmt.Collector so the construct (metric emitter) and the controller
// (API registrar) always name the same byte-identical identities.
type Collector = fleetmgmt.Collector

// ReceiptState says whether a GetConfig result is usable as configuration-delivery evidence.
// It deliberately says nothing about parsing or executing the returned configuration.
type ReceiptState string

const (
	ReceiptReceived    ReceiptState = "received"
	ReceiptStale       ReceiptState = "stale"
	ReceiptUnavailable ReceiptState = "unavailable"
	ReceiptError       ReceiptState = "error"
)

// Receipt retains only a bounded digest of returned configuration. Digest is populated only
// for ReceiptReceived and is SHA-256 of the response content; raw configuration never escapes
// the client.
type Receipt struct {
	State  ReceiptState
	Digest string
}

// ReceiptObserver receives the sanitized GetConfig receipt independently of the lifecycle
// observer. It exists so configuration delivery remains distinct from heartbeat liveness.
type ReceiptObserver func(ctx context.Context, collector string, receipt Receipt)

var ErrLedgerDestinationMismatch = errors.New("Fleet ledger destination mismatch; cleanup intent retained")
var ErrLedgerUnbound = errors.New("Fleet ledger has unbound cleanup intent; explicit ownership recovery required")

// LifecycleErrorMessage exposes only known safe ledger diagnostics, never upstream errors.
func LifecycleErrorMessage(err error) string {
	for _, safe := range []error{ErrLedgerDestinationMismatch, ErrLedgerUnbound, ErrTeardownAuthority, ErrTeardownNoLedger, ErrTeardownLedgerAbsent, ErrTeardownIncomplete} {
		if errors.Is(err, safe) {
			return safe.Error()
		}
	}
	return operationalerr.Message(operationalerr.CodeOf(err))
}

// RegisteredStore is the durable cleanup ledger. Set(present=true) commits before registration;
// Set(present=false) commits only after successful unregistration. Implementations fence writes
// and reject destination mismatches before returning IDs or changing intent. The document binds
// IDs to a non-secret destination identity, never credentials or returned configuration.
type RegisteredStore interface {
	Load(context.Context) ([]string, error)
	Set(context.Context, string, bool) error
}

// Config is the runtime configuration for the fleet controller.
type Config struct {
	Store    RegisteredStore
	Delivery httpretry.Delivery
	Bounded  ha.Bounded
	// FMURL is the Fleet Management API base URL,
	// e.g. "https://fm.example".
	FMURL string
	// StackID is the FM stack identifier, used as the Basic-auth username.
	StackID string
	// Token is the FM API token, used as the Basic-auth password.
	Token string
	// HeartbeatInterval is how often GetConfig is called for each registered collector
	// (in seconds). Defaults to 45s when ≤ 0 (predecessor controller.go:20).
	HeartbeatInterval int
	// DryRun: when true, no HTTP calls are made — every call is logged instead.
	DryRun bool
	// Observe, when non-nil, is called once per register/heartbeat/unregister outcome via the
	// stdlib-only fleethook seam (self-observability + control-plane status). nil ⇒ the
	// lifecycle is byte-for-byte unchanged.
	Observe fleethook.Observer
	// ObserveReceipt, when non-nil, receives the bounded configuration-receipt result from each
	// GetConfig call. It never receives raw configuration or response fields.
	ObserveReceipt ReceiptObserver
	// Paused, when non-nil and true, suspends the lifecycle loop: a reconcile makes no request of
	// any kind (no registration, heartbeat or unregistration) and leaves registered collectors as
	// they are. Explicit teardown (Unregister, Cleanup) is unaffected. nil ⇒ never paused.
	Paused func() bool
}

// RosterProvider returns the desired collector set at call time. A static fleet uses a closure
// that returns a fixed slice; a k8s-mirror fleet returns a slice recomputed from the live node
// set so DaemonSet collectors register/unregister as the cluster scales.
type RosterProvider func() []Collector

const defaultHeartbeatSeconds = 45
