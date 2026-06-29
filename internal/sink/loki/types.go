// SPDX-License-Identifier: AGPL-3.0-only

// Package loki pushes synthetic log streams to a Loki push endpoint using the 3-tuple
// form [ts, line, {structured metadata}]. Stream labels are low-cardinality ONLY; the
// sink asserts on every push that no high-cardinality key (trace_id, span_id,
// request_id, session_id, correlation_id, …) appears in Stream.Labels — high-card
// identifiers ride in structured metadata. See ARCHITECTURE.md invariants I14/I15.
//
// types.go is part of the Phase-0 frozen seam (single owner: the wiring pass).
// The sink implementation lives in loki.go (Phase-1 lane).
package loki

import "time"

// Line is one log entry. Meta is structured metadata (queryable, not a stream label).
type Line struct {
	T    time.Time
	Body string
	Meta map[string]string
}

// Stream is a set of lines sharing low-cardinality labels.
type Stream struct {
	Labels map[string]string
	Lines  []Line
}
