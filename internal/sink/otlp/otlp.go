// SPDX-License-Identifier: AGPL-3.0-only

// Package otlp pushes synthetic trace data to a Grafana Cloud OTLP/HTTP endpoint.
//
// Design: we build OTLP protobuf directly (NO OpenTelemetry SDK) so that a single
// Write call can carry multiple Resource blocks — one per service in the fabricated
// multi-service trace tree. The OTel trace SDK forces one Resource per
// provider/exporter, making multi-resource exports impossible without running multiple
// providers.
//
// We do NOT import go.opentelemetry.io/proto/otlp/collector/trace/v1 because its
// sibling trace_service.pb.gw.go pulls in grpc-gateway / google.golang.org/grpc
// which are not in go.sum. Instead we hand-encode the ExportTraceServiceRequest
// proto envelope: it is a single repeated field (field-number 1, wire-type 2 = LEN)
// containing each serialised ResourceSpans. We use protowire for the tag/length
// prefix encoding. Wire format is identical to what collectortracepb would produce.
//
// This sink carries TRACES ONLY. All metrics go via promrw (Mimir remote_write).
// See ARCHITECTURE.md §6 / invariant I2.
package otlp

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"sync"

	"github.com/rknightion/synthkit/internal/pushhook"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// Sink holds connection state for the OTLP/HTTP traces endpoint.
type Sink struct {
	eg     egress
	dryRun bool

	// Observe, when non-nil, is called once per push with the outcome (self-observability seam,
	// set only by package main when enabled). nil ⇒ the push path is unchanged.
	Observe pushhook.Observer

	// Quiet, when true, suppresses the per-push "[dry-run otlp] …" log line. Set on throwaway dry
	// sinks used for offline projection (bpsource cardinality preview) so a validate/save click
	// doesn't spew lines into a live process log.
	Quiet bool

	invMu        sync.Mutex
	invSpanNames map[string]map[string]struct{} // dry-run: service.name → span names
	invSpanAttrs map[string]map[string]struct{} // dry-run: service.name → span attr keys
	invResAttrs  map[string]map[string]struct{} // dry-run: service.name → resource attr keys
}

// New creates an OTLP traces sink. endpoint is the base gateway URL (e.g.
// "https://otlp-gateway-prod-gb-south-1.grafana.net/otlp"); the "/v1/traces" path
// is appended automatically. user/token are used for HTTP Basic auth.
func New(endpoint, user, token string, dryRun bool) *Sink {
	return &Sink{
		eg:     newEgress(endpoint, "/v1/traces", user, token, "otlp"),
		dryRun: dryRun,
	}
}

// Write serialises resources as a single ExportTraceServiceRequest and POSTs it
// to the OTLP endpoint. Multiple Resource blocks in one call = one multi-service
// trace tree (the whole point of bypassing the OTel SDK).
func (s *Sink) Write(ctx context.Context, resources []Resource) error {
	if len(resources) == 0 {
		return nil
	}

	rspans := make([]*tracepb.ResourceSpans, 0, len(resources))
	totalSpans := 0

	for _, r := range resources {
		scopeName, scopeVer := r.Scope.Name, r.Scope.Version
		if scopeName == "" {
			scopeName = "synthkit"
		}

		spans, skipped := convertSpans(r.Spans)
		if skipped > 0 {
			log.Printf("[otlp] skipped %d span(s) with invalid trace/span IDs", skipped)
		}
		totalSpans += len(spans)

		rspans = append(rspans, &tracepb.ResourceSpans{
			Resource: &resourcepb.Resource{
				Attributes: kvs(r.Attrs),
			},
			ScopeSpans: []*tracepb.ScopeSpans{
				{
					Scope: &commonpb.InstrumentationScope{Name: scopeName, Version: scopeVer},
					Spans: spans,
				},
			},
		})
	}

	// Recover the blueprint from the first resource's stamped attribute. The stampedTraces writer
	// (internal/runner/writers.go) stamps BlueprintLabel == "blueprint" as a resource attr before
	// calling Write, mirroring how promrw/loki recover it from series/stream labels.
	// We use the string literal "blueprint" here to avoid importing internal/runner (layering:
	// runner imports the sinks, not the other way around).
	blueprint := ""
	if v, ok := resources[0].Attrs["blueprint"]; ok {
		blueprint, _ = v.(string)
	}

	if s.dryRun {
		s.record(resources)
		firstSvc := ""
		if v, ok := resources[0].Attrs["service.name"]; ok {
			firstSvc = fmt.Sprint(v)
		}
		firstName := ""
		if len(resources[0].Spans) > 0 {
			firstName = resources[0].Spans[0].Name
		}
		if !s.Quiet {
			log.Printf("[dry-run otlp] %d resource(s), %d span(s); first service.name=%q first span=%q",
				len(resources), totalSpans, firstSvc, firstName)
		}
		if s.Observe != nil {
			s.Observe(ctx, pushhook.Event{Sink: "otlp", Blueprint: blueprint, Items: totalSpans, DryRun: true})
		}
		return nil
	}

	// Hand-encode ExportTraceServiceRequest proto envelope (field 1 = repeated ResourceSpans,
	// wire type 2 = LEN). Equivalent to collectortracepb.ExportTraceServiceRequest{ResourceSpans:…}
	// without importing that package (which drags in grpc-gateway deps absent from go.sum).
	var buf []byte
	for _, rs := range rspans {
		rsBytes, err := proto.Marshal(rs)
		if err != nil {
			return fmt.Errorf("otlp traces: marshal ResourceSpans: %w", err)
		}
		buf = protowire.AppendTag(buf, 1, protowire.BytesType)
		buf = protowire.AppendBytes(buf, rsBytes)
	}

	return s.eg.post(ctx, buf, totalSpans, blueprint, s.Observe)
}

// record accumulates the dry-run inventory keyed by service.name: resource attr keys, span names,
// and span attr keys (offline diff against signals/traces.md).
func (s *Sink) record(resources []Resource) {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	if s.invSpanNames == nil {
		s.invSpanNames = map[string]map[string]struct{}{}
		s.invSpanAttrs = map[string]map[string]struct{}{}
		s.invResAttrs = map[string]map[string]struct{}{}
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
		for _, sp := range r.Spans {
			add(s.invSpanNames, svc, sp.Name)
			for k := range sp.Attrs {
				add(s.invSpanAttrs, svc, k)
			}
		}
	}
}

// Inventory returns the captured dry-run inventory per service.name.
func (s *Sink) Inventory() (resAttrs, spanNames, spanAttrs map[string][]string) {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	return sortOTLPInv(s.invResAttrs), sortOTLPInv(s.invSpanNames), sortOTLPInv(s.invSpanAttrs)
}

func sortOTLPInv(m map[string]map[string]struct{}) map[string][]string {
	out := make(map[string][]string, len(m))
	for svc, keys := range m {
		ks := make([]string, 0, len(keys))
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		out[svc] = ks
	}
	return out
}

// convertSpans converts a slice of Span into OTLP proto spans, skipping any with
// invalid (empty or non-hex) TraceID or SpanID. Returns the converted spans and
// the number skipped.
func convertSpans(in []Span) ([]*tracepb.Span, int) {
	out := make([]*tracepb.Span, 0, len(in))
	skipped := 0
	for _, sp := range in {
		traceID, err := hex.DecodeString(sp.TraceID)
		if err != nil || len(traceID) != 16 {
			log.Printf("[otlp] skipping span %q: TraceID %q must be exactly 32 hex chars (16 bytes), got %d bytes", sp.Name, sp.TraceID, len(traceID))
			skipped++
			continue
		}
		spanID, err := hex.DecodeString(sp.SpanID)
		if err != nil || len(spanID) != 8 {
			log.Printf("[otlp] skipping span %q: SpanID %q must be exactly 16 hex chars (8 bytes), got %d bytes", sp.Name, sp.SpanID, len(spanID))
			skipped++
			continue
		}

		var parentID []byte
		if sp.ParentID != "" {
			parentID, err = hex.DecodeString(sp.ParentID)
			if err != nil || len(parentID) != 8 {
				log.Printf("[otlp] skipping span %q: ParentID %q must be exactly 16 hex chars (8 bytes), got %d bytes", sp.Name, sp.ParentID, len(parentID))
				skipped++
				continue
			}
		}

		out = append(out, &tracepb.Span{
			TraceId:           traceID,
			SpanId:            spanID,
			ParentSpanId:      parentID,
			Name:              sp.Name,
			Kind:              mapSpanKind(sp.Kind),
			StartTimeUnixNano: uint64(sp.Start.UnixNano()),
			EndTimeUnixNano:   uint64(sp.End.UnixNano()),
			Attributes:        kvs(sp.Attrs),
			Status: &tracepb.Status{
				Code:    mapStatusCode(sp.Status),
				Message: sp.StatusMsg,
			},
		})
	}
	return out, skipped
}

// kvs converts a map[string]any into a []*commonpb.KeyValue slice. Value types
// string, int, int64, float64, and bool are mapped to their native AnyValue
// variants; all other types are stringified via fmt.Sprint.
func kvs(attrs map[string]any) []*commonpb.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]*commonpb.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		out = append(out, &commonpb.KeyValue{
			Key:   k,
			Value: anyVal(v),
		})
	}
	return out
}

func anyVal(v any) *commonpb.AnyValue {
	switch val := v.(type) {
	case string:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: val}}
	case int:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(val)}}
	case int64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: val}}
	case float64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: val}}
	case bool:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: val}}
	default:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprint(val)}}
	}
}

// gzipBytes compresses b with gzip at BestSpeed — ~90% of the ratio at a fraction of the CPU,
// the right tradeoff for a generator pushing continuously. The Grafana Cloud OTLP/HTTP gateway
// accepts Content-Encoding: gzip.
func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func mapSpanKind(k SpanKind) tracepb.Span_SpanKind {
	switch k {
	case KindServer:
		return tracepb.Span_SPAN_KIND_SERVER
	case KindClient:
		return tracepb.Span_SPAN_KIND_CLIENT
	case KindProducer:
		return tracepb.Span_SPAN_KIND_PRODUCER
	case KindConsumer:
		return tracepb.Span_SPAN_KIND_CONSUMER
	default: // KindInternal
		return tracepb.Span_SPAN_KIND_INTERNAL
	}
}

func mapStatusCode(c StatusCode) tracepb.Status_StatusCode {
	switch c {
	case StatusOK:
		return tracepb.Status_STATUS_CODE_OK
	case StatusError:
		return tracepb.Status_STATUS_CODE_ERROR
	default: // StatusUnset
		return tracepb.Status_STATUS_CODE_UNSET
	}
}
