// SPDX-License-Identifier: AGPL-3.0-only

// LogsSink pushes synthetic NATIVE OTLP logs to the Grafana Cloud OTLP/HTTP gateway as
// hand-encoded ResourceLogs protobuf (no OTel SDK; the same protowire-envelope technique as
// the traces Sink and MetricsSink, for the same reason: the collector/logs/v1 package drags
// grpc-gateway deps that are absent from go.sum).
//
// This lane models the k8s-monitoring `podLogsViaOpenTelemetry` transport. The collector ships
// pod-log CONTENT as OTLP log records carrying RESOURCE attributes (k8s.pod.name, service.name,
// …) and RECORD attributes (log.iostream, logtag), and the DESTINATION decides the observable
// shape: Loki promotes an allowlisted subset of the resource attributes to stream labels
// (sanitising dots to underscores) and drops everything else — record attributes included —
// into structured metadata.
//
// Contrast internal/sink/loki, which puts stream labels and structured metadata ON THE WIRE.
// Both transports carry identical content; only the observable shape differs.
//
// Scope: unlike the traces/metrics lanes this sink does NOT fall back to a "synthkit"
// instrumentation-scope name. A real filelog-collected pod log carries an empty scope, and
// Loki's OTLP ingest only adds `scope_name` structured metadata when the scope name is
// non-empty — the reality corpus (reality-corpus/k8s/k3d-lab.json, k8s-monitoring 4.4.0,
// 2026-08-25) records no `scope_name`, so inventing one would invent an observable.
//
// Carries LOGS ONLY.
package otlp

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"sync"

	"github.com/rknightion/synthkit/internal/pushhook"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// LogsSink holds connection state for the OTLP/HTTP logs endpoint.
type LogsSink struct {
	eg     egress
	dryRun bool
	// Capture retains dry-run resources for the one-shot machine-readable inventory export.
	Capture bool
	// Quiet suppresses the per-push "[dry-run otlplogs] …" line (offline projection).
	Quiet bool

	Observe pushhook.Observer

	invMu       sync.Mutex
	invResAttrs map[string]map[string]struct{} // service.name → resource attr keys
	invRecAttrs map[string]map[string]struct{} // service.name → record attr keys
	captured    []LogResource
}

// NewLogs builds the OTLP logs sink. endpoint is the base gateway URL; "/v1/logs" is appended.
// Reuses the GC_OTLP_* credential triplet (same gateway as traces and native metrics).
func NewLogs(endpoint, user, token string, dryRun bool) *LogsSink {
	return &LogsSink{
		eg:     newEgress(endpoint, "/v1/logs", user, token, "otlplogs"),
		dryRun: dryRun,
	}
}

// Write serialises resources as a single ExportLogsServiceRequest and POSTs it. Multiple
// LogResource blocks in one call = one export carrying several pods' log records.
func (s *LogsSink) Write(ctx context.Context, resources []LogResource) error {
	if len(resources) == 0 {
		return nil
	}

	rls := make([]*logspb.ResourceLogs, 0, len(resources))
	totalRecords := 0
	for _, r := range resources {
		records := convertLogRecords(r.Records)
		totalRecords += len(records)

		scopeLogs := &logspb.ScopeLogs{LogRecords: records}
		// Only carry an InstrumentationScope when the emitter named one — see the package doc.
		if r.Scope.Name != "" {
			scopeLogs.Scope = &commonpb.InstrumentationScope{Name: r.Scope.Name, Version: r.Scope.Version}
		}
		rls = append(rls, &logspb.ResourceLogs{
			Resource:  &resourcepb.Resource{Attributes: kvs(r.Attrs)},
			ScopeLogs: []*logspb.ScopeLogs{scopeLogs},
		})
	}

	// Recover the blueprint from the first resource's stamped attribute (string literal to avoid
	// importing internal/runner — the runner imports the sinks, never the reverse).
	blueprint := ""
	if v, ok := resources[0].Attrs["blueprint"]; ok {
		blueprint, _ = v.(string)
	}

	if s.dryRun {
		s.record(resources)
		if !s.Quiet {
			firstSvc := ""
			if v, ok := resources[0].Attrs["service.name"]; ok {
				firstSvc = fmt.Sprint(v)
			}
			log.Printf("[dry-run otlplogs] %d resource(s), %d record(s); first service.name=%q",
				len(resources), totalRecords, firstSvc)
		}
		if s.Observe != nil {
			s.Observe(ctx, pushhook.Event{Sink: "otlplogs", Blueprint: blueprint, Items: totalRecords, DryRun: true})
		}
		return nil
	}

	// Hand-encode ExportLogsServiceRequest (field 1 = repeated ResourceLogs, wire type 2 = LEN).
	var buf []byte
	for _, rl := range rls {
		b, err := proto.Marshal(rl)
		if err != nil {
			return fmt.Errorf("otlp logs: marshal ResourceLogs: %w", err)
		}
		buf = protowire.AppendTag(buf, 1, protowire.BytesType)
		buf = protowire.AppendBytes(buf, b)
	}
	return s.eg.post(ctx, buf, totalRecords, blueprint, s.Observe)
}

// convertLogRecords maps LogRecords onto the proto form. A record is NEVER dropped: an
// unparseable TraceID/SpanID loses only its correlation field (with a log line), because
// losing a log line would break the both-transports-carry-identical-content contract.
func convertLogRecords(in []LogRecord) []*logspb.LogRecord {
	out := make([]*logspb.LogRecord, 0, len(in))
	for _, rec := range in {
		observed := rec.ObservedTime
		if observed.IsZero() {
			observed = rec.Time
		}
		sevText := rec.SeverityText
		if sevText == "" {
			sevText = rec.Severity.Text()
		}
		pr := &logspb.LogRecord{
			SeverityNumber: logspb.SeverityNumber(rec.Severity),
			SeverityText:   sevText,
			Body:           &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: rec.Body}},
			Attributes:     kvs(rec.Attrs),
		}
		if !rec.Time.IsZero() {
			pr.TimeUnixNano = uint64(rec.Time.UnixNano())
		}
		if !observed.IsZero() {
			pr.ObservedTimeUnixNano = uint64(observed.UnixNano())
		}
		if id, ok := decodeID(rec.TraceID, 16); ok {
			pr.TraceId = id
		} else if rec.TraceID != "" {
			log.Printf("[otlp logs] dropping TraceID %q: want exactly 32 hex chars (16 bytes)", rec.TraceID)
		}
		if id, ok := decodeID(rec.SpanID, 8); ok {
			pr.SpanId = id
		} else if rec.SpanID != "" {
			log.Printf("[otlp logs] dropping SpanID %q: want exactly 16 hex chars (8 bytes)", rec.SpanID)
		}
		out = append(out, pr)
	}
	return out
}

// decodeID hex-decodes an optional correlation ID, reporting whether it is exactly n bytes.
func decodeID(s string, n int) ([]byte, bool) {
	if s == "" {
		return nil, false
	}
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != n {
		return nil, false
	}
	return b, true
}

// record accumulates the dry-run inventory keyed by service.name: resource attr keys and record
// attr keys. The split is the point of this lane — the destination promotes only resource
// attributes to stream labels — so the two sets are kept apart, never merged.
func (s *LogsSink) record(resources []LogResource) {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	if s.Capture {
		s.captured = append(s.captured, resources...)
	}
	if s.invResAttrs == nil {
		s.invResAttrs = map[string]map[string]struct{}{}
		s.invRecAttrs = map[string]map[string]struct{}{}
	}
	add := func(m map[string]map[string]struct{}, svc, key string) {
		set := m[svc]
		if set == nil {
			set = map[string]struct{}{}
			m[svc] = set
		}
		set[key] = struct{}{}
	}
	for _, r := range resources {
		svc := ""
		if v, ok := r.Attrs["service.name"]; ok {
			svc = fmt.Sprint(v)
		}
		for k := range r.Attrs {
			add(s.invResAttrs, svc, k)
		}
		for _, rec := range r.Records {
			for k := range rec.Attrs {
				add(s.invRecAttrs, svc, k)
			}
		}
	}
}

// Captured returns resources retained while Capture was enabled in dry-run mode.
func (s *LogsSink) Captured() []LogResource {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	return append([]LogResource(nil), s.captured...)
}

// Inventory returns the captured dry-run inventory per service.name: resource attribute keys
// (the promotion candidates) and record attribute keys (always structured metadata).
func (s *LogsSink) Inventory() (resAttrs, recordAttrs map[string][]string) {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	return sortOTLPInv(s.invResAttrs), sortOTLPInv(s.invRecAttrs)
}
