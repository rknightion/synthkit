// SPDX-License-Identifier: AGPL-3.0-only

package solace

import (
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
)

// Failure modes on the queue axis, each scoped to one declared queue name (a name repeated in
// several VPNs or brokers addresses every queue carrying it). With no incident active the
// output is unchanged.
const (
	// ModePublishDrop scales the queue's publish rate by 1-intensity: at full intensity nothing
	// is spooled, so the backlog drains and spool usage stops growing.
	ModePublishDrop = "queue_publish_drop"
	// ModeConsumerStall scales the consumers' drain rate by 1-intensity: at full intensity
	// nothing is delivered, so the depth and spool usage grow until the quota discards.
	ModeConsumerStall = "queue_consumer_stall"
)

// FailureModes is the queue-axis vocabulary this construct implements.
var FailureModes = []failuremode.Mode{
	{Name: ModePublishDrop, Axis: failuremode.AxisQueue, Help: "publishes into the queue stop (rate x (1-intensity)): spooled messages flatten and the backlog drains"},
	{Name: ModeConsumerStall, Axis: failuremode.AxisQueue, Help: "the queue's consumers stop draining (rate x (1-intensity)): depth and spool usage grow until the spool quota discards"},
}

// Registration wires this construct into the composition root's catalog
// (single-owner wiring shim - added by the wiring pass).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      Kind,
		Doc:       "Solace event-broker telemetry (solace_prometheus_exporter v1.21.0: per-queue and per-VPN spool, flow, rate, discard and connection series)",
		Scope:     core.ScopeSubstrate,
		Group:     core.GroupIntegration,
		NewConfig: func() any { return &Config{} },
		Build:     Build,
		// Declared queues (Config.FailureTargets).
		FailureModes: FailureModes,
	}
}
