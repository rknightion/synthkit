// SPDX-License-Identifier: AGPL-3.0-only

// Package otlp pushes synthetic TRACES (only) to a Grafana Cloud OTLP/HTTP endpoint as
// hand-encoded ResourceSpans protobuf. No OTel SDK: one Write carries multiple Resource
// blocks (one per service in a fabricated multi-service trace) with explicit timestamps
// and ledger-minted trace/span IDs. Do NOT import the otlp collector trace/v1 package
// (it drags grpc-gateway/grpc); the export envelope is hand-encoded with protowire.
// See ARCHITECTURE.md invariant I2.
//
// types.go is part of the Phase-0 frozen seam (single owner: the wiring pass).
// The sink implementation lives in otlp.go (Phase-1 lane).
package otlp

import "time"

// SpanKind mirrors the OTLP span-kind enum without exposing the proto type.
type SpanKind int

const (
	KindInternal SpanKind = iota
	KindServer
	KindClient
	KindProducer
	KindConsumer
)

// StatusCode mirrors the OTLP status-code enum.
type StatusCode int

const (
	StatusUnset StatusCode = iota
	StatusOK
	StatusError
)

// Span is one span in a trace. Trace/Span/Parent IDs are hex strings from the request
// ledger: TraceID = 32 hex chars (16 bytes); SpanID/ParentID = 16 hex chars (8 bytes).
// ParentID "" means root span. Attrs values: string | int | int64 | float64 | bool
// (others stringified).
type Span struct {
	Name      string
	TraceID   string
	SpanID    string
	ParentID  string
	Kind      SpanKind
	Start     time.Time
	End       time.Time
	Attrs     map[string]any
	Status    StatusCode
	StatusMsg string
	// Events are the span's timestamped events (OTLP Span.events, field 11), e.g. the
	// OTel "exception" event. nil or empty encodes byte-identically to a span without them.
	Events []Event `json:",omitempty"`
	// Links are the span's links to other spans' contexts (OTLP Span.links, field 13), e.g. a
	// messaging CONSUMER span's link to the message's creation context. nil or empty encodes
	// byte-identically to a span without them.
	Links []Link `json:",omitempty"`
}

// Link is one OTLP Span.Link: a reference to another span's context. TraceID is 32 hex chars and
// SpanID 16 hex chars; a link with a malformed id is dropped (the span is kept). Attrs values
// follow the same rules as Span.Attrs.
type Link struct {
	TraceID string
	SpanID  string
	Attrs   map[string]any
}

// Event is one OTLP Span.Event: a named, timestamped annotation on a span. Attrs values
// follow the same rules as Span.Attrs. A zero Time encodes as time_unix_nano 0.
type Event struct {
	Name  string
	Time  time.Time
	Attrs map[string]any
}

// Scope is the OTLP InstrumentationScope (the real instrumentation library name+version,
// e.g. "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp" / "0.58.0"). The
// zero value falls back to the legacy "synthkit" scope name at encode time. Shared by
// trace Resource and MetricResource.
type Scope struct {
	Name    string
	Version string
}

// Resource is one service's resource block carrying its spans. Multiple Resources in a
// single Write form one export carrying one trace across several service.name values.
type Resource struct {
	Attrs map[string]any // resource attributes (service.name, deployment.environment, …)
	Scope Scope          // instrumentation scope; zero ⇒ "synthkit"
	Spans []Span
}
