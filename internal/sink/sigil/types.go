// SPDX-License-Identifier: AGPL-3.0-only

// Package sigil provides an HTTP-protojson sink for the Grafana AI Observability
// (sigil) native ingest API. It POSTs generations, workflow-steps, and scores to
// the three ingest endpoints using protojson encoding and HTTP Basic auth.
// NO gRPC; NO OTel SDK.
package sigil

// Inventory records dry-run counts for the three export resource types.
// It is returned by Sink.Inventory() and surfaced by printInventory in cmd/synthkit.
type Inventory struct {
	Generations   int64
	WorkflowSteps int64
	Scores        int64
}
