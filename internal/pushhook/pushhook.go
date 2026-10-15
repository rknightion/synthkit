// SPDX-License-Identifier: AGPL-3.0-only

// Package pushhook defines the seam between the synthetic-data sinks and the generator's
// self-observability. It is deliberately stdlib-only: it imports neither the OTel SDK nor any
// construct/workload/blueprint package, so the synthetic I/O layer (promrw/loki/otlp/faro) can
// report push outcomes WITHOUT transitively pulling the self-observability SDK into the
// synthetic-data path.
//
// A sink exposes an Observe field of type Observer (default nil). When set — only ever by package
// main — the sink calls it once per push with the outcome. When nil the push path is byte-for-byte
// unchanged. The composition root fans events to internal/selfobs and the always-on pushstatus
// store without exposing the raw sink error to either consumer.
//
// This is one half of the self-obs isolation; the other is the runner's stdlib-only tick seam
// (runner.TickFunc), which keeps the OTel SDK out of the composition root's scheduler.
package pushhook

import (
	"context"
	"time"

	"github.com/rknightion/synthkit/internal/operationalerr"
)

// Event is the outcome of one sink push. The sink populates whatever it knows; fields it cannot
// cheaply determine are left zero (documented per-sink — e.g. promrw cannot surface Bytes/Status
// because its remote_write client owns marshalling and the HTTP round-trip).
type Event struct {
	Sink            string              // sink name: "promrw" | "loki" | "otlp" | "faro" | "pyroscope"
	Blueprint       string              // blueprint selector label stamped on the batch ("" when substrate/unscoped)
	Items           int                 // logical items pushed: series | (streams+lines) | spans | beacons
	Bytes           int                 // wire bytes of the request body (0 when the sink doesn't build it itself)
	Status          int                 // HTTP status code (0 = transport/marshal error before any response)
	Duration        time.Duration       // wall-clock of the push call
	DryRun          bool                // true = dry-run (no network push happened; Status/Bytes not meaningful)
	ErrorCode       operationalerr.Code // closed failure classification; never carries raw error text
	Acknowledgement Acknowledgement     // Protocol-level acceptance only; not final backend readback or a durable checkpoint receipt.
}

type AckStatus string

const (
	AckUnknown  AckStatus = "unverified" // Missing/ambiguous evidence; never infer acceptance from zero counts.
	AckAccepted AckStatus = "accepted"
	AckPartial  AckStatus = "partial"
	AckRejected AckStatus = "rejected"
	AckDeferred AckStatus = "deferred" // Asynchronous acceptance without complete written counts.
)

// Acknowledgement contains only bounded counts and a closed status. Unit is
// spans, data_points, log_records, or rw2_elements (samples+histograms+exemplars).
// It has no receiver diagnostic text, credential, transaction ID or durable epoch.
type Acknowledgement struct {
	Status   AckStatus `json:"status"`
	Unit     string    `json:"unit"`
	Expected int64     `json:"expected"`
	Accepted int64     `json:"accepted"`
	Rejected int64     `json:"rejected"`
}

// Sanitized rejects inconsistent counts and arbitrary text before status exposure.
func (a Acknowledgement) Sanitized() Acknowledgement {
	unknown := Acknowledgement{Status: AckUnknown}
	if a.Status == "" {
		a.Status = AckUnknown
	}
	switch a.Unit {
	case "spans", "data_points", "log_records", "rw2_elements":
	default:
		return unknown
	}
	if a.Expected < 0 || a.Accepted < 0 || a.Rejected < 0 || a.Accepted > a.Expected || a.Rejected > a.Expected-a.Accepted {
		return unknown
	}
	switch a.Status {
	case AckAccepted:
		if a.Accepted != a.Expected || a.Rejected != 0 {
			return unknown
		}
	case AckPartial:
		if a.Accepted == 0 || a.Rejected == 0 || a.Rejected != a.Expected-a.Accepted {
			return unknown
		}
	case AckRejected:
		if a.Accepted != 0 || a.Rejected != a.Expected {
			return unknown
		}
	case AckDeferred, AckUnknown:
		if a.Rejected != 0 {
			return unknown
		}
	default:
		return unknown
	}
	return a
}

// Observer receives one push Event. ctx carries any active trace span (the tick span the runner's
// TickFunc established), so an implementation can attach the push as a child span.
type Observer func(ctx context.Context, ev Event)
