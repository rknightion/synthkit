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
	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/fleethook"
)

// Collector is the fleet's resolved fake collector identity. It is a direct alias
// of fleetmgmt.Collector so the construct (metric emitter) and the controller
// (API registrar) always name the same byte-identical identities.
type Collector = fleetmgmt.Collector

// Config is the runtime configuration for the fleet controller.
type Config struct {
	// FMURL is the Fleet Management API base URL,
	// e.g. "https://fleet-management-prod-006.grafana.net".
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
}

// RosterProvider returns the desired collector set at call time. A static fleet uses a closure
// that returns a fixed slice; a k8s-mirror fleet returns a slice recomputed from the live node
// set so DaemonSet collectors register/unregister as the cluster scales.
type RosterProvider func() []Collector

const defaultHeartbeatSeconds = 45
