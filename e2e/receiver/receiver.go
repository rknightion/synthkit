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

// Instrument types outside inventory's four-value vocabulary. Prometheus remote-write
// metadata can declare any OpenMetrics type, and a producer that declares one is real
// evidence: it is recorded verbatim rather than folded into an approximation.
const (
	instrumentSummary  = "summary"
	instrumentInfo     = "info"
	instrumentStateSet = "stateset"
)

// receiptRW1Metadata counts decoded prompb.MetricMetadata records. Remote-write v1 carries them
// in their own requests, so whether a producer sends any at all is a separate observation from
// how many samples it sends, and an absent receipt is the honest record of a producer that sent
// none.
const receiptRW1Metadata = "prometheus_remote_write_v1_metadata"

// Receiver accepts each synthkit egress lane over HTTP and accumulates the schema
// (metric names + label values, log sources + stream-label values, trace services + span names,
// sigil ingest kinds + operation names).
type Receiver struct {
	mu  sync.Mutex
	inv inventory.Schema
	// declaredInstruments maps a metric family name to the instrument type its producer
	// declared in remote-write metadata. Remote-write v1 carries metadata in separate
	// write requests from the samples, so the declaration is held here and applied to the
	// inventory on Snapshot rather than at sample-decode time.
	declaredInstruments map[string]string
}

// New returns a zero-state Receiver ready to use.
func New() *Receiver {
	return &Receiver{inv: inventory.New(), declaredInstruments: map[string]string{}}
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
	switch remoteWriteVersion(req) {
	case "v1":
		decoded, metadata := r.decodeRW1(raw)
		r.inv.AddReceipt(inventory.TransportPrometheusRW1, decoded)
		r.inv.AddReceipt(receiptRW1Metadata, metadata)
	case "v2":
		decoded, natives := r.decodeRW2(raw)
		r.inv.AddReceipt(inventory.TransportPrometheusRW2, decoded)
		writtenHeaders(w, decoded-natives, natives)
	default:
		decoded, natives := r.decodeRW2(raw)
		if decoded > 0 {
			r.inv.AddReceipt(inventory.TransportPrometheusRW2, decoded)
			writtenHeaders(w, decoded-natives, natives)
			break
		}
		decoded, metadata := r.decodeRW1(raw)
		r.inv.AddReceipt(inventory.TransportPrometheusRW1, decoded)
		r.inv.AddReceipt(receiptRW1Metadata, metadata)
	}
	w.WriteHeader(http.StatusNoContent)
}

// writtenHeaders sets the response headers the Remote-Write 2.0 specification makes mandatory
// for a receiver: a sender reads them to distinguish a full write from a partial or empty one,
// and a real sender treats their absence as nothing having been written. The counts are of
// decoded series, since this receiver keeps one observation per series rather than per sample.
// The header names are the ones the specification fixes. This receiver decodes no exemplars,
// and reports that as the zero it is.
func writtenHeaders(w http.ResponseWriter, samples, histograms int) {
	w.Header().Set("X-Prometheus-Remote-Write-Samples-Written", strconv.Itoa(samples))
	w.Header().Set("X-Prometheus-Remote-Write-Histograms-Written", strconv.Itoa(histograms))
	w.Header().Set("X-Prometheus-Remote-Write-Exemplars-Written", "0")
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

// decodeRW2 returns the number of decoded series and, of those, how many carried a native
// histogram sample. The split is what the mandatory written-count response headers report.
func (r *Receiver) decodeRW2(raw []byte) (int, int) {
	var pb writev2.Request
	if err := proto.Unmarshal(raw, &pb); err != nil {
		return 0, 0
	}
	decoded := 0
	natives := 0
	for _, ts := range pb.Timeseries {
		if ts == nil {
			continue
		}
		name, labels := rw2Labels(pb.Symbols, ts.LabelsRefs)
		if name == "" {
			continue
		}
		// The instrument type comes from the producer's own RW2 metadata, or from a
		// native histogram sample, and never from the series name. A classic-histogram
		// suffix establishes the family's bucket shape but declares no type.
		instrument := rw2Instrument(ts)
		histogram := rw2Histogram(name, labels, ts)
		if instrument == inventory.InstrumentUnknown {
			instrument = seriesInstrument(labels, histogram)
		}
		if histogram == nil && instrument == inventory.InstrumentHistogram {
			// RW2 metadata can describe a classic histogram family even when this
			// particular series carries no histogram sample. Keep the family shape.
			histogram = &inventory.Histogram{Classic: true, BucketBounds: []float64{}, NativeSchemas: []int32{}}
		}
		name = normalizedHistogramName(name, histogram != nil)
		r.inv.AddMetric(name, inventory.TransportPrometheusRW2, instrument, labels, histogram)
		decoded++
		if len(ts.Histograms) > 0 {
			natives++
		}
	}
	return decoded, natives
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

// decodeRW1 walks a Prometheus remote-write v1 WriteRequest. Field numbers are those of
// prompb.WriteRequest in Prometheus v3.12.0 (the pin recorded beside the vendored RW2 proto):
// timeseries = 1, metadata = 3. Field 2 is reserved. Metadata records arrive in their own
// write requests on the producer's metadata cadence, so a body can carry either or both.
// It returns the number of decoded series and the number of decoded metadata records.
func (r *Receiver) decodeRW1(raw []byte) (int, int) {
	decoded := 0
	metadata := 0
	b := raw
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if typ != protowire.BytesType || (num != 1 && num != 3) {
			skip := protowire.ConsumeFieldValue(num, typ, b)
			if skip < 0 {
				break
			}
			b = b[skip:]
			continue
		}
		recordBytes, n := protowire.ConsumeBytes(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if num == 3 {
			family, instrument := decodeRW1MetricMetadata(recordBytes)
			if family == "" || instrument == "" {
				continue
			}
			r.declaredInstruments[family] = instrument
			metadata++
			continue
		}
		name, labels, histogram := decodeRW1TimeSeries(recordBytes)
		if name == "" {
			continue
		}
		instrument := seriesInstrument(labels, histogram)
		if histogram != nil {
			name = normalizedHistogramName(name, true)
		}
		r.inv.AddMetric(name, inventory.TransportPrometheusRW1, instrument, labels, histogram)
		decoded++
	}
	return decoded, metadata
}

// seriesInstrument reads the instrument type out of the series itself, never out of its name.
// Two label names carry it: Prometheus reserves `le` for classic histogram bucket series and
// `quantile` for summary quantile series, so a series carrying one is that instrument by the
// exposition contract rather than by resemblance. A native histogram sample is equally direct.
// Everything else stays unknown until the producer's own metadata declares a type: the
// `_sum`, `_count` and `_total` suffixes look like evidence and are not, because a histogram
// and a summary expose identically named component series.
func seriesInstrument(labels map[string]string, histogram *inventory.Histogram) string {
	switch {
	case labels["le"] != "":
		return inventory.InstrumentHistogram
	case labels["quantile"] != "":
		return instrumentSummary
	case histogram != nil && histogram.Native:
		return inventory.InstrumentHistogram
	default:
		return inventory.InstrumentUnknown
	}
}

// decodeRW1MetricMetadata decodes one prompb.MetricMetadata record: type = 1 (MetricType
// enum), metric_family_name = 2, help = 4, unit = 5. Only the declared type and the family
// it applies to are retained.
func decodeRW1MetricMetadata(raw []byte) (string, string) {
	family := ""
	instrument := ""
	b := raw
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		switch {
		case num == 1 && typ == protowire.VarintType:
			value, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return "", ""
			}
			b = b[n:]
			instrument = rw1MetadataInstrument(value)
		case num == 2 && typ == protowire.BytesType:
			value, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return "", ""
			}
			b = b[n:]
			family = string(value)
		default:
			skip := protowire.ConsumeFieldValue(num, typ, b)
			if skip < 0 {
				return "", ""
			}
			b = b[skip:]
		}
	}
	return family, instrument
}

// rw1MetadataInstrument maps a prompb.MetricMetadata.MetricType enum value to the instrument
// vocabulary. UNKNOWN (0) is a producer declaring the type is untyped, which is not evidence
// of any instrument, so it records nothing.
func rw1MetadataInstrument(value uint64) string {
	switch value {
	case 1:
		return inventory.InstrumentCounter
	case 2:
		return inventory.InstrumentGauge
	case 3, 4:
		// HISTOGRAM and GAUGEHISTOGRAM both expose the bucket contract the inventory
		// records as a histogram.
		return inventory.InstrumentHistogram
	case 5:
		return instrumentSummary
	case 6:
		return instrumentInfo
	case 7:
		return instrumentStateSet
	default:
		return ""
	}
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

// handleLoki decodes a Loki push body in either wire form.
//
// synthkit's own loki sink sends gzip+JSON. A real Grafana Alloy `loki.write` component does
// NOT: it sends a snappy-compressed logproto.PushRequest with Content-Type
// application/x-protobuf, which is the only form the k3d capture lab's Loki-native pod-log
// permutation ever produces. Decoding one form only made the whole Loki lane invisible to the
// lab while every other lane worked, so a capture recorded zero Loki evidence and there was
// nothing to say whether that meant the collector sent nothing or the receiver could not read
// it. Both forms are decoded, and which one arrived is decided by the request, never guessed.
func (r *Receiver) handleLoki(w http.ResponseWriter, req *http.Request) {
	if lokiPushIsProtobuf(req) {
		r.handleLokiProtobuf(w, req)
		return
	}
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

// lokiPushIsProtobuf decides the wire form from the request rather than by sniffing the body.
// Alloy declares application/x-protobuf; synthkit's own sink declares application/json.
func lokiPushIsProtobuf(req *http.Request) bool {
	contentType := strings.ToLower(req.Header.Get("Content-Type"))
	return strings.Contains(contentType, "protobuf") || strings.Contains(contentType, "x-proto")
}

// handleLokiProtobuf decodes a snappy-compressed logproto.PushRequest by walking the protobuf
// wire format directly, the same way decodeRW1 walks prompb.WriteRequest. The field numbers are
// those of logproto.PushRequest: streams = 1, and inside StreamAdapter labels = 1,
// entries = 2, hash = 3; inside EntryAdapter timestamp = 1, line = 2, structuredMetadata = 3;
// inside LabelPairAdapter name = 1, value = 2.
func (r *Receiver) handleLokiProtobuf(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	raw, err := snappy.Decode(nil, body)
	if err != nil {
		// A snappy block that will not decode is a decode failure, not an empty push: reply 400
		// so the producer's own error metrics show it rather than the lab silently recording
		// nothing.
		http.Error(w, err.Error(), 400)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	streams := 0
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
		record, n := protowire.ConsumeBytes(b)
		if n < 0 {
			break
		}
		b = b[n:]
		labels, metadata := decodeLokiStream(record)
		if len(labels) == 0 {
			continue
		}
		// Keyed EXACTLY as the JSON path is, and as the loki sink's dry-run inventory is:
		// strictly the "source" label, with "" when absent.
		r.inv.AddLog(labels["source"], inventory.TransportLoki, labels, metadata)
		streams++
	}
	r.inv.AddReceipt(inventory.TransportLoki, streams)
	w.WriteHeader(http.StatusNoContent)
}

// decodeLokiStream returns one StreamAdapter's stream labels and the union of the structured
// metadata keys carried by its entries.
func decodeLokiStream(raw []byte) (map[string]string, []string) {
	labels := map[string]string{}
	metadata := make([]string, 0)
	seen := map[string]struct{}{}
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
		record, n := protowire.ConsumeBytes(b)
		if n < 0 {
			break
		}
		b = b[n:]
		switch num {
		case 1:
			for key, value := range parseLokiLabelSet(string(record)) {
				labels[key] = value
			}
		case 2:
			for _, key := range decodeLokiEntryMetadataKeys(record) {
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				metadata = append(metadata, key)
			}
		}
	}
	return labels, metadata
}

// decodeLokiEntryMetadataKeys returns the structured-metadata label names on one EntryAdapter.
func decodeLokiEntryMetadataKeys(raw []byte) []string {
	keys := make([]string, 0)
	b := raw
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if num != 3 || typ != protowire.BytesType {
			skip := protowire.ConsumeFieldValue(num, typ, b)
			if skip < 0 {
				break
			}
			b = b[skip:]
			continue
		}
		record, n := protowire.ConsumeBytes(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if key, _ := decodeRW1Label(record); key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

// parseLokiLabelSet parses the Prometheus label-set text a StreamAdapter carries, for example
// `{cluster="lab", source="kubernetes"}`. It is deliberately a small parser rather than a
// regular expression: a label value may contain a comma or an escaped quote, and splitting on
// commas would silently truncate one.
func parseLokiLabelSet(text string) map[string]string {
	out := map[string]string{}
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "{")
	text = strings.TrimSuffix(text, "}")
	for len(text) > 0 {
		text = strings.TrimLeft(text, ", \t")
		equals := strings.IndexByte(text, '=')
		if equals < 0 {
			break
		}
		key := strings.TrimSpace(text[:equals])
		text = text[equals+1:]
		if len(text) == 0 || text[0] != '"' {
			break
		}
		value, rest, ok := scanQuoted(text)
		if !ok {
			break
		}
		if key != "" {
			out[key] = value
		}
		text = rest
	}
	return out
}

// scanQuoted reads one double-quoted, backslash-escaped string starting at text[0] and returns
// its unescaped value plus whatever follows it.
func scanQuoted(text string) (string, string, bool) {
	var value strings.Builder
	for i := 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			if i+1 >= len(text) {
				return "", "", false
			}
			i++
			switch text[i] {
			case 'n':
				value.WriteByte('\n')
			case 't':
				value.WriteByte('\t')
			default:
				value.WriteByte(text[i])
			}
		case '"':
			return value.String(), text[i+1:], true
		default:
			value.WriteByte(text[i])
		}
	}
	return "", "", false
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
	snapshot := cloneSchema(r.inv)
	applyDeclaredInstruments(&snapshot, r.declaredInstruments)
	return snapshot
}

// applyDeclaredInstruments replaces a family's recorded instrument types with the type its
// producer declared in remote-write metadata. The lookup is by exact family name: a metadata
// record for family "foo" never resolves the type of a differently named series, so a summary's
// "foo_sum" and "foo_count" series stay unresolved rather than borrowing a type by suffix.
func applyDeclaredInstruments(schema *inventory.Schema, declared map[string]string) {
	for i := range schema.Metrics {
		if instrument, ok := declared[schema.Metrics[i].Name]; ok && instrument != "" {
			schema.Metrics[i].InstrumentTypes = []string{instrument}
			continue
		}
		// A family's component series are recorded together, and only some of them carry
		// the evidence: a histogram's bucket series carries `le` while its `_sum` and
		// `_count` series carry nothing. Once any series has established the type, the
		// sentinel the others contributed records nothing and is dropped.
		schema.Metrics[i].InstrumentTypes = withoutSupersededSentinel(schema.Metrics[i].InstrumentTypes)
	}
}

func withoutSupersededSentinel(instruments []string) []string {
	if len(instruments) < 2 {
		return instruments
	}
	observed := make([]string, 0, len(instruments))
	for _, instrument := range instruments {
		if instrument != inventory.InstrumentUnknown {
			observed = append(observed, instrument)
		}
	}
	if len(observed) == 0 {
		return instruments
	}
	return observed
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
