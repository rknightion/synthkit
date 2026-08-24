// SPDX-License-Identifier: AGPL-3.0-only

// Package receiver is the e2e sidecar: it decodes every synthkit egress lane (RW2 metrics,
// OTLP traces, OTLP metrics, Loki logs, sigil native ingest) into an inventory.Schema for
// -dump correlation. It is a TEST harness — not on the synthetic-data path — so it may import
// the sinks + otlp proto + sigilv1.
package receiver

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/golang/snappy"
	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/rknightion/synthkit/internal/inventory"
	writev2 "github.com/rknightion/synthkit/internal/sink/promrw/writev2"
	sigilv1 "github.com/rknightion/synthkit/internal/sink/sigil/v1"
)

// Receiver accepts each synthkit egress lane over HTTP and accumulates the schema
// (metric names + label values, log sources + stream-label values, trace services + span names,
// sigil ingest kinds + operation names).
type Receiver struct {
	mu  sync.Mutex
	inv inventory.Schema
}

// New returns a zero-state Receiver ready to use.
func New() *Receiver {
	return &Receiver{inv: inventory.New()}
}

// Handler returns an http.Handler routing all synthkit egress paths.
func (r *Receiver) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/prom/push", r.handleRW2)
	mux.HandleFunc("POST /otlp/v1/traces", r.handleTraces)
	mux.HandleFunc("POST /otlp/v1/metrics", r.handleOTLPMetrics)
	mux.HandleFunc("POST /otlp/v1/logs", r.handleOTLPLogs)
	// Some OTLP clients are configured with /v1 as their base path rather than /otlp.
	mux.HandleFunc("POST /v1/logs", r.handleOTLPLogs)
	mux.HandleFunc("POST /loki/api/v1/push", r.handleLoki)
	// Sigil native-ingest lanes (plain protojson, no gzip).
	mux.HandleFunc("POST /api/v1/generations:export", r.handleSigilGenerations)
	mux.HandleFunc("POST /api/v1/workflow-steps:export", r.handleSigilWorkflowSteps)
	mux.HandleFunc("POST /api/v1/scores:export", r.handleSigilScores)
	mux.HandleFunc("GET /__inventory", r.handleInventory)
	return mux
}

// handleRW2 decodes a snappy-compressed Prometheus remote-write body. The route is retained
// for compatibility with the old receiver API; the Content-Type proto parameter selects RW1 or
// RW2 when present, while an absent parameter tries the current RW2 shape before the legacy RW1
// shape.
func (r *Receiver) handleRW2(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	raw, err := snappy.Decode(nil, body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	version := remoteWriteVersion(req)
	decoded := 0
	switch version {
	case "v1":
		decoded = r.decodeRW1(raw)
		r.inv.AddReceipt(inventory.TransportPrometheusRW1, decoded)
	case "v2":
		decoded = r.decodeRW2(raw)
		r.inv.AddReceipt(inventory.TransportPrometheusRW2, decoded)
	default:
		decoded = r.decodeRW2(raw)
		if decoded > 0 {
			r.inv.AddReceipt(inventory.TransportPrometheusRW2, decoded)
		} else {
			decoded = r.decodeRW1(raw)
			r.inv.AddReceipt(inventory.TransportPrometheusRW1, decoded)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func remoteWriteVersion(req *http.Request) string {
	_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err == nil {
		protoName := strings.ToLower(params["proto"])
		switch {
		case strings.Contains(protoName, "write.v2"), strings.Contains(protoName, "writev2"):
			return "v2"
		case strings.Contains(protoName, "writerequest"), strings.Contains(protoName, "write.v1"), strings.Contains(protoName, "writev1"):
			return "v1"
		}
	}
	if strings.HasPrefix(req.Header.Get("X-Prometheus-Remote-Write-Version"), "2.") {
		return "v2"
	}
	return ""
}

func (r *Receiver) decodeRW2(raw []byte) int {
	var pb writev2.Request
	if err := proto.Unmarshal(raw, &pb); err != nil {
		return 0
	}
	decoded := 0
	for _, ts := range pb.Timeseries {
		if ts == nil {
			continue
		}
		name, labels := rw2Labels(pb.Symbols, ts.LabelsRefs)
		if name == "" {
			continue
		}
		instrument := rw2Instrument(ts)
		histogram := rw2Histogram(name, labels, ts)
		if histogram != nil {
			instrument = inventory.InstrumentHistogram
		} else if instrument == inventory.InstrumentHistogram {
			// RW2 metadata can describe a classic histogram family even when this
			// particular series carries no histogram sample. Keep the family shape.
			histogram = &inventory.Histogram{Classic: true, BucketBounds: []float64{}, NativeSchemas: []int32{}}
		}
		name = normalizedHistogramName(name, histogram != nil)
		r.inv.AddMetric(name, inventory.TransportPrometheusRW2, instrument, labels, histogram)
		decoded++
	}
	return decoded
}

func rw2Labels(symbols []string, refs []uint32) (string, map[string]string) {
	labels := map[string]string{}
	name := ""
	for i := 0; i+1 < len(refs); i += 2 {
		ni, vi := uint64(refs[i]), uint64(refs[i+1])
		if ni >= uint64(len(symbols)) || vi >= uint64(len(symbols)) {
			continue
		}
		key, value := symbols[ni], symbols[vi]
		if key == "__name__" {
			name = value
		} else if key != "" {
			labels[key] = value
		}
	}
	return name, labels
}

func rw2Instrument(ts *writev2.TimeSeries) string {
	if ts.Metadata == nil {
		if len(ts.Histograms) > 0 {
			return inventory.InstrumentHistogram
		}
		return inventory.InstrumentUnknown
	}
	switch ts.Metadata.Type {
	case writev2.Metadata_METRIC_TYPE_COUNTER:
		return inventory.InstrumentCounter
	case writev2.Metadata_METRIC_TYPE_GAUGE:
		return inventory.InstrumentGauge
	case writev2.Metadata_METRIC_TYPE_HISTOGRAM, writev2.Metadata_METRIC_TYPE_GAUGEHISTOGRAM:
		return inventory.InstrumentHistogram
	default:
		return inventory.InstrumentUnknown
	}
}

func rw2Histogram(name string, labels map[string]string, ts *writev2.TimeSeries) *inventory.Histogram {
	classic := strings.HasSuffix(name, "_bucket") || strings.HasSuffix(name, "_sum") || strings.HasSuffix(name, "_count")
	if len(ts.Histograms) == 0 && !classic {
		return nil
	}
	out := &inventory.Histogram{Classic: classic, Native: len(ts.Histograms) > 0, BucketBounds: []float64{}, NativeSchemas: []int32{}}
	if classic {
		if rawBound := labels["le"]; rawBound != "" && rawBound != "+Inf" {
			if bound, err := strconv.ParseFloat(rawBound, 64); err == nil && !math.IsInf(bound, 0) && !math.IsNaN(bound) {
				out.BucketBounds = append(out.BucketBounds, bound)
			}
		}
	}
	for _, histogram := range ts.Histograms {
		if histogram != nil {
			out.NativeSchemas = append(out.NativeSchemas, histogram.Schema)
		}
	}
	return out
}

func normalizedHistogramName(name string, histogram bool) string {
	if !histogram {
		return name
	}
	for _, suffix := range []string{"_bucket", "_sum", "_count"} {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}
	return name
}

func (r *Receiver) decodeRW1(raw []byte) int {
	decoded := 0
	b := raw
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if num != 1 || typ != protowire.BytesType {
			skip := protowire.ConsumeFieldValue(num, typ, b)
			if skip < 0 {
				break
			}
			b = b[skip:]
			continue
		}
		tsBytes, n := protowire.ConsumeBytes(b)
		if n < 0 {
			break
		}
		b = b[n:]
		name, labels, histogram := decodeRW1TimeSeries(tsBytes)
		if name == "" {
			continue
		}
		instrument := inventory.InstrumentUnknown
		if histogram != nil {
			instrument = inventory.InstrumentHistogram
			name = normalizedHistogramName(name, true)
		}
		r.inv.AddMetric(name, inventory.TransportPrometheusRW1, instrument, labels, histogram)
		decoded++
	}
	return decoded
}

func decodeRW1TimeSeries(raw []byte) (string, map[string]string, *inventory.Histogram) {
	labels := map[string]string{}
	nativeSchemas := []int32{}
	b := raw
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if num == 1 && typ == protowire.BytesType {
			labelBytes, n := protowire.ConsumeBytes(b)
			if n < 0 {
				break
			}
			b = b[n:]
			key, value := decodeRW1Label(labelBytes)
			if key == "__name__" {
				labels[key] = value
			} else if key != "" {
				labels[key] = value
			}
			continue
		}
		if num == 4 && typ == protowire.BytesType {
			histogramBytes, n := protowire.ConsumeBytes(b)
			if n < 0 {
				break
			}
			b = b[n:]
			if schema, ok := decodeRW1HistogramSchema(histogramBytes); ok {
				nativeSchemas = append(nativeSchemas, schema)
			}
			continue
		}
		skip := protowire.ConsumeFieldValue(num, typ, b)
		if skip < 0 {
			break
		}
		b = b[skip:]
	}
	name := labels["__name__"]
	delete(labels, "__name__")
	var histogram *inventory.Histogram
	classic := strings.HasSuffix(name, "_bucket") || strings.HasSuffix(name, "_sum") || strings.HasSuffix(name, "_count")
	if classic || len(nativeSchemas) > 0 {
		histogram = &inventory.Histogram{Classic: classic, Native: len(nativeSchemas) > 0, BucketBounds: []float64{}, NativeSchemas: nativeSchemas}
		if classic {
			if rawBound := labels["le"]; rawBound != "" && rawBound != "+Inf" {
				if bound, err := strconv.ParseFloat(rawBound, 64); err == nil && !math.IsInf(bound, 0) && !math.IsNaN(bound) {
					histogram.BucketBounds = append(histogram.BucketBounds, bound)
				}
			}
		}
	}
	return name, labels, histogram
}

func decodeRW1HistogramSchema(raw []byte) (int32, bool) {
	b := raw
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return 0, false
		}
		b = b[n:]
		if num == 4 && typ == protowire.VarintType {
			encoded, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return 0, false
			}
			schema := int64(encoded>>1) ^ -int64(encoded&1)
			return int32(schema), true
		}
		skip := protowire.ConsumeFieldValue(num, typ, b)
		if skip < 0 {
			return 0, false
		}
		b = b[skip:]
	}
	return 0, false
}

func decodeRW1Label(raw []byte) (string, string) {
	var key, value string
	b := raw
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if typ != protowire.BytesType || (num != 1 && num != 2) {
			skip := protowire.ConsumeFieldValue(num, typ, b)
			if skip < 0 {
				break
			}
			b = b[skip:]
			continue
		}
		text, n := protowire.ConsumeString(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if num == 1 {
			key = text
		} else {
			value = text
		}
	}
	return key, value
}

// gunzip decompresses the request body if Content-Encoding is gzip; otherwise returns raw bytes.
func gunzip(req *http.Request) ([]byte, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if req.Header.Get("Content-Encoding") != "gzip" {
		return body, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// handleTraces decodes the otlp sink's hand-rolled envelope:
// repeated field-1 LEN records, each a marshalled ResourceSpans.
// (The sink does NOT emit a TracesData / ExportTraceServiceRequest wrapper.)
func (r *Receiver) handleTraces(w http.ResponseWriter, req *http.Request) {
	raw, err := gunzip(req)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := raw
	decoded := 0
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if num != 1 || typ != protowire.BytesType {
			// Skip any unexpected field rather than bailing out.
			skip := protowire.ConsumeFieldValue(num, typ, b)
			if skip < 0 {
				break
			}
			b = b[skip:]
			continue
		}
		rsBytes, n := protowire.ConsumeBytes(b)
		if n < 0 {
			break
		}
		b = b[n:]
		var rs tracepb.ResourceSpans
		if err := proto.Unmarshal(rsBytes, &rs); err != nil {
			continue
		}
		resourceAttrs := attributeStrings(rs.GetResource().GetAttributes())
		svc := resourceIdentity(resourceAttrs)
		for _, ss := range rs.GetScopeSpans() {
			for _, sp := range ss.GetSpans() {
				spanAttrs := make([]string, 0, len(sp.GetAttributes()))
				for _, attr := range sp.GetAttributes() {
					if attr != nil && attr.GetKey() != "" {
						spanAttrs = append(spanAttrs, attr.GetKey())
					}
				}
				r.inv.AddTrace(svc, resourceAttrs, sp.GetName(), spanAttrs)
				decoded++
			}
		}
	}
	r.inv.AddReceipt(inventory.TransportOTLPTraces, decoded)
	w.WriteHeader(http.StatusOK)
}

// handleOTLPMetrics decodes the otlp metrics sink's hand-rolled envelope:
// repeated field-1 LEN records, each a marshalled ResourceMetrics (the sink hand-encodes an
// ExportMetricsServiceRequest the same way the traces sink does — metrics.go ~88). Decoding
// via the generated metricspb structs + getters means proto field numbers can never drift.
func (r *Receiver) handleOTLPMetrics(w http.ResponseWriter, req *http.Request) {
	raw, err := gunzip(req)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := raw
	decoded := 0
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if num != 1 || typ != protowire.BytesType {
			// Skip any unexpected field rather than bailing out.
			skip := protowire.ConsumeFieldValue(num, typ, b)
			if skip < 0 {
				break
			}
			b = b[skip:]
			continue
		}
		rmBytes, n := protowire.ConsumeBytes(b)
		if n < 0 {
			break
		}
		b = b[n:]
		var rm metricspb.ResourceMetrics
		if err := proto.Unmarshal(rmBytes, &rm); err != nil {
			continue
		}
		decoded += r.addOTLPMetrics(&rm)
	}
	r.inv.AddReceipt(inventory.TransportOTLPMetrics, decoded)
	w.WriteHeader(http.StatusOK)
}

func (r *Receiver) addOTLPMetrics(rm *metricspb.ResourceMetrics) int {
	resourceAttrs := attributeStrings(rm.GetResource().GetAttributes())
	decoded := 0
	for _, sm := range rm.GetScopeMetrics() {
		for _, metric := range sm.GetMetrics() {
			name := metric.GetName()
			if name == "" {
				continue
			}
			decoded++
			switch {
			case metric.GetGauge() != nil:
				points := metric.GetGauge().GetDataPoints()
				if len(points) == 0 {
					r.inv.AddMetric(name, inventory.TransportOTLPMetrics, inventory.InstrumentGauge, resourceAttrs, nil)
				}
				for _, point := range points {
					r.inv.AddMetric(name, inventory.TransportOTLPMetrics, inventory.InstrumentGauge,
						mergeAttributeStrings(resourceAttrs, attributeStrings(point.GetAttributes())), nil)
				}
			case metric.GetSum() != nil:
				instrument := inventory.InstrumentGauge
				if metric.GetSum().GetIsMonotonic() {
					instrument = inventory.InstrumentCounter
				}
				points := metric.GetSum().GetDataPoints()
				if len(points) == 0 {
					r.inv.AddMetric(name, inventory.TransportOTLPMetrics, instrument, resourceAttrs, nil)
				}
				for _, point := range points {
					r.inv.AddMetric(name, inventory.TransportOTLPMetrics, instrument,
						mergeAttributeStrings(resourceAttrs, attributeStrings(point.GetAttributes())), nil)
				}
			case metric.GetHistogram() != nil:
				points := metric.GetHistogram().GetDataPoints()
				if len(points) == 0 {
					r.inv.AddMetric(name, inventory.TransportOTLPMetrics, inventory.InstrumentHistogram, resourceAttrs,
						&inventory.Histogram{Classic: true, BucketBounds: []float64{}, NativeSchemas: []int32{}})
				}
				for _, point := range points {
					r.inv.AddMetric(name, inventory.TransportOTLPMetrics, inventory.InstrumentHistogram,
						mergeAttributeStrings(resourceAttrs, attributeStrings(point.GetAttributes())),
						&inventory.Histogram{Classic: true, BucketBounds: point.GetExplicitBounds(), NativeSchemas: []int32{}})
				}
			case metric.GetExponentialHistogram() != nil:
				points := metric.GetExponentialHistogram().GetDataPoints()
				if len(points) == 0 {
					r.inv.AddMetric(name, inventory.TransportOTLPMetrics, inventory.InstrumentHistogram, resourceAttrs,
						&inventory.Histogram{Native: true, BucketBounds: []float64{}, NativeSchemas: []int32{}})
				}
				for _, point := range points {
					r.inv.AddMetric(name, inventory.TransportOTLPMetrics, inventory.InstrumentHistogram,
						mergeAttributeStrings(resourceAttrs, attributeStrings(point.GetAttributes())),
						&inventory.Histogram{Native: true, BucketBounds: []float64{}, NativeSchemas: []int32{point.GetScale()}})
				}
			default:
				r.inv.AddMetric(name, inventory.TransportOTLPMetrics, inventory.InstrumentUnknown, resourceAttrs, nil)
			}
		}
	}
	return decoded
}

func attributeStrings(attrs []*commonpb.KeyValue) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		if attr == nil || attr.GetKey() == "" {
			continue
		}
		out[attr.GetKey()] = anyValueString(attr.GetValue())
	}
	return out
}

func anyValueString(value *commonpb.AnyValue) string {
	if value == nil {
		return ""
	}
	switch value.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return value.GetStringValue()
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(value.GetBoolValue())
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(value.GetIntValue(), 10)
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(value.GetDoubleValue(), 'g', -1, 64)
	case *commonpb.AnyValue_BytesValue:
		return fmt.Sprintf("%x", value.GetBytesValue())
	case *commonpb.AnyValue_ArrayValue:
		return fmt.Sprint(value.GetArrayValue().GetValues())
	case *commonpb.AnyValue_KvlistValue:
		return fmt.Sprint(value.GetKvlistValue().GetValues())
	default:
		return ""
	}
}

func mergeAttributeStrings(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func resourceIdentity(attrs map[string]string) string {
	if service := attrs["service.name"]; service != "" {
		return service
	}
	if namespace := attrs["service.namespace"]; namespace != "" {
		return namespace
	}
	return "unknown"
}

// handleOTLPLogs decodes the standard OTLP/HTTP ExportLogsServiceRequest. Both protobuf and
// protojson requests are accepted; the generated request type is already part of the OTLP
// dependency used by synthkit.
func (r *Receiver) handleOTLPLogs(w http.ResponseWriter, req *http.Request) {
	raw, err := gunzip(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var pb collectorlogspb.ExportLogsServiceRequest
	contentType := strings.ToLower(req.Header.Get("Content-Type"))
	if strings.Contains(contentType, "json") {
		err = protojson.Unmarshal(raw, &pb)
	} else {
		err = proto.Unmarshal(raw, &pb)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	decoded := 0
	for _, resourceLogs := range pb.GetResourceLogs() {
		if resourceLogs == nil {
			continue
		}
		resourceAttrs := attributeStrings(resourceLogs.GetResource().GetAttributes())
		source := resourceIdentity(resourceAttrs)
		for _, scopeLogs := range resourceLogs.GetScopeLogs() {
			if scopeLogs == nil {
				continue
			}
			for _, record := range scopeLogs.GetLogRecords() {
				if record == nil {
					continue
				}
				metadata := make([]string, 0, len(record.GetAttributes())+2)
				for _, attr := range record.GetAttributes() {
					if attr != nil && attr.GetKey() != "" {
						metadata = append(metadata, attr.GetKey())
					}
				}
				if len(record.GetTraceId()) > 0 {
					metadata = append(metadata, "trace_id")
				}
				if len(record.GetSpanId()) > 0 {
					metadata = append(metadata, "span_id")
				}
				r.inv.AddLog(source, inventory.TransportOTLPLogs, resourceAttrs, metadata)
				decoded++
			}
		}
	}
	r.inv.AddReceipt(inventory.TransportOTLPLogs, decoded)
	w.WriteHeader(http.StatusOK)
}

// handleLoki decodes a gzip+JSON Loki push body.
func (r *Receiver) handleLoki(w http.ResponseWriter, req *http.Request) {
	raw, err := gunzip(req)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var push struct {
		Streams []struct {
			Stream map[string]string   `json:"stream"`
			Values [][]json.RawMessage `json:"values"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(raw, &push); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range push.Streams {
		// Key EXACTLY as the loki sink's dry-run inventory does (loki.go: src :=
		// st.Labels["source"]) — strictly the "source" label, with "" when absent.
		// No service_name/job/"stream" fallback: a fallback would re-key the
		// source-less manifests stream (job=integrations/kubernetes/manifests) under
		// its service_name on this side while -dump keys it under "", so the two
		// inventories would never correlate. Mirroring the sink keeps both sides aligned.
		src := s.Stream["source"]
		metadata := make([]string, 0)
		for _, value := range s.Values {
			if len(value) < 3 {
				continue
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(value[2], &fields); err != nil {
				continue
			}
			for key := range fields {
				metadata = append(metadata, key)
			}
		}
		r.inv.AddLog(src, inventory.TransportLoki, s.Stream, metadata)
	}
	r.inv.AddReceipt(inventory.TransportLoki, len(push.Streams))
	w.WriteHeader(http.StatusNoContent)
}

// handleSigilGenerations decodes a plain protojson ExportGenerationsRequest.
func (r *Receiver) handleSigilGenerations(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var pb sigilv1.ExportGenerationsRequest
	if err := protojson.Unmarshal(body, &pb); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, g := range pb.GetGenerations() {
		op := g.GetOperationName()
		r.inv.AddSigil("generations", op)
	}
	r.inv.AddReceipt("sigil_generations", len(pb.GetGenerations()))
	w.WriteHeader(http.StatusOK)
}

// handleSigilWorkflowSteps decodes a plain protojson ExportWorkflowStepsRequest.
func (r *Receiver) handleSigilWorkflowSteps(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var pb sigilv1.ExportWorkflowStepsRequest
	if err := protojson.Unmarshal(body, &pb); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Record presence; workflow steps have no operation_name equivalent.
	if len(pb.GetWorkflowSteps()) > 0 {
		r.inv.AddSigil("workflow_steps")
	}
	r.inv.AddReceipt("sigil_workflow_steps", len(pb.GetWorkflowSteps()))
	w.WriteHeader(http.StatusOK)
}

// handleSigilScores decodes a plain protojson ExportScoresRequest.
func (r *Receiver) handleSigilScores(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var pb sigilv1.ExportScoresRequest
	if err := protojson.Unmarshal(body, &pb); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Record presence; scores have no operation_name equivalent.
	if len(pb.GetScores()) > 0 {
		r.inv.AddSigil("scores")
	}
	r.inv.AddReceipt("sigil_scores", len(pb.GetScores()))
	w.WriteHeader(http.StatusOK)
}

// Snapshot returns a point-in-time deep copy of the accumulated canonical schema.
func (r *Receiver) Snapshot() inventory.Schema {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSchema(r.inv)
}

// handleInventory returns the accumulated Schema as JSON (GET /__inventory).
func (r *Receiver) handleInventory(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := r.Snapshot().WriteJSON(w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func cloneSchema(src inventory.Schema) inventory.Schema {
	dst := inventory.New()
	dst.SchemaVersion = src.SchemaVersion
	if src.Provenance != nil {
		provenance := *src.Provenance
		dst.Provenance = &provenance
	}
	dst.Metrics = make([]inventory.Metric, len(src.Metrics))
	for i, metric := range src.Metrics {
		dst.Metrics[i] = inventory.Metric{
			Name:            metric.Name,
			Transports:      cloneStrings(metric.Transports),
			InstrumentTypes: cloneStrings(metric.InstrumentTypes),
			Labels:          cloneAttributes(metric.Labels),
		}
		if metric.Histogram != nil {
			histogram := *metric.Histogram
			histogram.BucketBounds = cloneFloat64s(metric.Histogram.BucketBounds)
			histogram.NativeSchemas = cloneInt32s(metric.Histogram.NativeSchemas)
			dst.Metrics[i].Histogram = &histogram
		}
	}
	dst.Logs = make([]inventory.Log, len(src.Logs))
	for i, entry := range src.Logs {
		dst.Logs[i] = inventory.Log{
			Source:                 entry.Source,
			Transport:              entry.Transport,
			StreamLabels:           cloneAttributes(entry.StreamLabels),
			StructuredMetadataKeys: cloneStrings(entry.StructuredMetadataKeys),
		}
	}
	dst.Traces = make([]inventory.Trace, len(src.Traces))
	for i, trace := range src.Traces {
		dst.Traces[i] = inventory.Trace{
			Service:            trace.Service,
			ResourceAttributes: cloneAttributes(trace.ResourceAttributes),
			SpanNames:          cloneStrings(trace.SpanNames),
			SpanAttributeKeys:  cloneStrings(trace.SpanAttributeKeys),
		}
	}
	dst.Profiles = make([]inventory.Profile, len(src.Profiles))
	for i, profile := range src.Profiles {
		dst.Profiles[i] = inventory.Profile{ProfileType: profile.ProfileType, Labels: cloneAttributes(profile.Labels)}
	}
	dst.Sigil = make([]inventory.Sigil, len(src.Sigil))
	for i, sigil := range src.Sigil {
		dst.Sigil[i] = inventory.Sigil{IngestKind: sigil.IngestKind, OperationNames: cloneStrings(sigil.OperationNames)}
	}
	dst.Receipts = append([]inventory.Receipt(nil), src.Receipts...)
	dst.Normalize()
	return dst
}

func cloneAttributes(attrs []inventory.Attribute) []inventory.Attribute {
	out := make([]inventory.Attribute, len(attrs))
	for i, attr := range attrs {
		out[i] = inventory.Attribute{Key: attr.Key, Values: cloneStrings(attr.Values), ValuesElided: attr.ValuesElided}
	}
	return out
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

func cloneFloat64s(values []float64) []float64 {
	if values == nil {
		return nil
	}
	return append([]float64{}, values...)
}

func cloneInt32s(values []int32) []int32 {
	if values == nil {
		return nil
	}
	return append([]int32{}, values...)
}
