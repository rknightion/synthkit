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
