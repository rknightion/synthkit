// SPDX-License-Identifier: AGPL-3.0-only

// log_types.go is part of the frozen OTLP seam (single owner: the wiring pass), mirroring
// types.go for traces and metric_types.go for metrics. The logs sink implementation lives
// in logs.go.
//
// This lane models the `podLogsViaOpenTelemetry` transport: the collector ships pod log
// content as OTLP log records carrying resource + record attributes, and the DESTINATION
// promotes only an explicit allowlist to Loki stream labels. That is a genuinely different
// observable shape from the Loki-native push in internal/sink/loki, which carries stream
// labels and structured metadata on the wire. Both transports carry identical content.
package otlp

import "time"

// Severity mirrors the OTLP SeverityNumber enum. The values are the OTLP wire numbers, not
// an ordinal: the spec assigns each level a 4-wide band (TRACE 1-4, DEBUG 5-8, INFO 9-12,
// WARN 13-16, ERROR 17-20, FATAL 21-24) and the band base is what a collector emits.
type Severity int32

const (
	SeverityUnspecified Severity = 0
	SeverityTrace       Severity = 1
	SeverityDebug       Severity = 5
	SeverityInfo        Severity = 9
	SeverityWarn        Severity = 13
	SeverityError       Severity = 17
	SeverityFatal       Severity = 21
)

// Text is the conventional SeverityText for a level, used when a LogRecord leaves
// SeverityText empty. An unrecognised value yields "" so the encoder omits the field
// rather than inventing a level name.
func (s Severity) Text() string {
	switch s {
	case SeverityTrace:
		return "TRACE"
	case SeverityDebug:
		return "DEBUG"
	case SeverityInfo:
		return "INFO"
	case SeverityWarn:
		return "WARN"
	case SeverityError:
		return "ERROR"
	case SeverityFatal:
		return "FATAL"
	default:
		return ""
	}
}

// LogRecord is one OTLP log record. Body is the log line as the container wrote it.
// Attrs are RECORD attributes (per-line: log.iostream, logtag, …), distinct from the
// resource attributes on LogResource. ObservedTime zero ⇒ the encoder uses Time.
// TraceID/SpanID are optional hex strings from the request ledger (32 / 16 chars);
// empty means the record carries no trace correlation, which is the normal case for
// pod logs scraped off a container's stdout.
type LogRecord struct {
	Time         time.Time
	ObservedTime time.Time
	Severity     Severity
	SeverityText string // "" ⇒ derived from Severity via Text()
	Body         string
	Attrs        map[string]any
	TraceID      string
	SpanID       string
}

// LogResource is one resource's block carrying its log records, mirroring trace Resource
// and MetricResource. Attrs are resource attributes (service.name, k8s.pod.name, …) — the
// set the destination's promotion allowlist selects Loki stream labels from. Multiple
// LogResources in one Write form one ExportLogsServiceRequest.
type LogResource struct {
	Attrs   map[string]any
	Scope   Scope
	Records []LogRecord
}
