// SPDX-License-Identifier: AGPL-3.0-only

// Package pushhook defines the seam between the synthetic-data sinks and the generator's
// self-observability. It is deliberately stdlib-only: it imports neither the OTel SDK nor any
// construct/workload/blueprint package, so the synthetic I/O layer (promrw/loki/otlp/faro) can
// report push outcomes WITHOUT transitively pulling the self-observability SDK into the
// synthetic-data path.
//
// A sink exposes an Observe field of type Observer (default nil). When set — only ever by package
// main, only when self-observability is enabled — the sink calls it once per push with the outcome.
// When nil the push path is byte-for-byte unchanged. internal/selfobs implements an Observer that
// records OTel metrics + a child span; nothing else in the process ever sets it.
//
// This is one half of the self-obs isolation; the other is the runner's stdlib-only tick seam
// (runner.TickFunc), which keeps the OTel SDK out of the composition root's scheduler.
package pushhook

import (
	"context"
	"time"
)

// Event is the outcome of one sink push. The sink populates whatever it knows; fields it cannot
// cheaply determine are left zero (documented per-sink — e.g. promrw cannot surface Bytes/Status
// because its remote_write client owns marshalling and the HTTP round-trip).
type Event struct {
	Sink      string        // sink name: "promrw" | "loki" | "otlp" | "faro" | "pyroscope"
	Blueprint string        // blueprint selector label stamped on the batch ("" when substrate/unscoped)
	Items     int           // logical items pushed: series | (streams+lines) | spans | beacons
	Bytes     int           // wire bytes of the request body (0 when the sink doesn't build it itself)
	Status    int           // HTTP status code (0 = transport/marshal error before any response)
	Duration  time.Duration // wall-clock of the push call
	DryRun    bool          // true = dry-run (no network push happened; Status/Bytes not meaningful)
	Err       error         // non-nil on failure
}

// Observer receives one push Event. ctx carries any active trace span (the tick span the runner's
// TickFunc established), so an implementation can attach the push as a child span.
type Observer func(ctx context.Context, ev Event)
