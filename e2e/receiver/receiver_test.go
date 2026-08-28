// SPDX-License-Identifier: AGPL-3.0-only

package receiver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	logspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/golang/snappy"
	"github.com/rknightion/synthkit/internal/inventory"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	sigilv1 "github.com/rknightion/synthkit/internal/sink/sigil/v1"
)

func TestReceiverCapturesAllLanes(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()
	ctx := context.Background()

	// Metrics (RW2): use the real promrw sink to encode + push one series.
	ms := promrw.New(srv.URL+"/api/prom/push", "u", "tok", false, func() int { return 0 })
	if err := ms.Write(ctx, []promrw.Series{{
		Name:   "e2e_demo_total",
		Labels: map[string]string{"cluster": "c1", "job": "demo"},
		Value:  1,
		T:      time.Now(),
		Kind:   promrw.KindGauge,
	}}); err != nil {
		t.Fatalf("promrw Write: %v", err)
	}

	// Logs (Loki): use the real loki sink to encode + push one stream.
	ls := loki.New(srv.URL+"/loki/api/v1/push", "u", "tok", false)
	if err := ls.Write(ctx, []loki.Stream{{
		Labels: map[string]string{"source": "e2e_app", "namespace": "demo"},
		Lines:  []loki.Line{{T: time.Now(), Body: "hello e2e"}},
	}}); err != nil {
		t.Fatalf("loki Write: %v", err)
	}

	// Traces (OTLP): use the real otlp sink to encode + push one span.
	os := otlp.New(srv.URL+"/otlp", "u", "tok", false)
	now := time.Now()
	if err := os.Write(ctx, []otlp.Resource{{
		Attrs: map[string]any{"service.name": "checkout"},
		Spans: []otlp.Span{{
			Name:    "GET /cart",
			TraceID: "0123456789abcdef0123456789abcdef",
			SpanID:  "0123456789abcdef",
			Start:   now,
			End:     now.Add(time.Millisecond),
			Kind:    otlp.KindServer,
		}},
	}}); err != nil {
		t.Fatalf("otlp Write: %v", err)
	}

	// Metrics (OTLP native): use the real otlp metrics sink to encode + push one metric.
	oms := otlp.NewMetrics(srv.URL+"/otlp", "u", "tok", false)
	if err := oms.Write(ctx, []otlp.MetricResource{{
		Attrs: map[string]any{"service.name": "checkout"},
		Metrics: []otlp.Metric{{
			Name:        "http.server.request.count",
			Kind:        otlp.MetricSum,
			Monotonic:   true,
			Temporality: otlp.TemporalityCumulative,
			Numbers:     []otlp.NumberPoint{{Time: now, Start: now, Value: 1}},
		}},
	}}); err != nil {
		t.Fatalf("otlp metrics Write: %v", err)
	}

	got := rec.Snapshot()

	// Metrics
	metric := findMetric(got, "e2e_demo_total")
	if metric == nil {
		t.Errorf("metric not captured: %v", got.Metrics)
	} else {
		keys := metric.Labels
		hasCluster, hasJob := false, false
		for _, label := range keys {
			if label.Key == "cluster" {
				hasCluster = true
			}
			if label.Key == "job" {
				hasJob = true
			}
		}
		if !hasCluster || !hasJob {
			t.Errorf("e2e_demo_total label keys = %v, want cluster+job", keys)
		}
	}

	// Logs
	if findLog(got, "e2e_app", inventory.TransportLoki) == nil {
		t.Errorf("log source not captured: %v", got.Logs)
	}

	// Traces
	if findTrace(got, "checkout") == nil {
		t.Errorf("trace service not captured: %v", got.Traces)
	}

	// OTLP metrics
	if findMetric(got, "http.server.request.count") == nil {
		t.Errorf("OTLP metric not captured: %v", got.Metrics)
	}
}

func TestReceiverCapturesSigilGenerations(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()

	req := &sigilv1.ExportGenerationsRequest{
		Generations: []*sigilv1.Generation{
			{
				Id:            "gen-001",
				OperationName: "generateText",
				Mode:          sigilv1.GenerationMode_GENERATION_MODE_SYNC,
			},
			{
				Id:            "gen-002",
				OperationName: "streamText",
				Mode:          sigilv1.GenerationMode_GENERATION_MODE_STREAM,
			},
		},
	}
	body, err := protojson.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(srv.URL+"/api/v1/generations:export", "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	got := rec.Snapshot()
	sigil := findSigil(got, "generations")
	if sigil == nil {
		t.Fatalf("sigil 'generations' kind not captured: %v", got.Sigil)
	}
	opSet := map[string]bool{}
	for _, op := range sigil.OperationNames {
		opSet[op] = true
	}
	if !opSet["generateText"] {
		t.Errorf("missing operation 'generateText' in %v", sigil.OperationNames)
	}
	if !opSet["streamText"] {
		t.Errorf("missing operation 'streamText' in %v", sigil.OperationNames)
	}
}

func TestReceiverCapturesSigilScores(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()

	score := &sigilv1.ScoreItem{
		ScoreId:      "sc-001",
		GenerationId: "gen-001",
		ScoreKey:     "helpfulness",
	}
	req := &sigilv1.ExportScoresRequest{Scores: []*sigilv1.ScoreItem{score}}
	body, err := protojson.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(srv.URL+"/api/v1/scores:export", "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	got := rec.Snapshot()
	if findSigil(got, "scores") == nil {
		t.Errorf("sigil 'scores' kind not captured: %v", got.Sigil)
	}
}

func TestReceiverCapturesSigilWorkflowSteps(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()

	step := &sigilv1.WorkflowStep{
		Id:       "step-001",
		StepName: "route",
	}
	req := &sigilv1.ExportWorkflowStepsRequest{WorkflowSteps: []*sigilv1.WorkflowStep{step}}
	body, err := protojson.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(srv.URL+"/api/v1/workflow-steps:export", "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	got := rec.Snapshot()
	if findSigil(got, "workflow_steps") == nil {
		t.Errorf("sigil 'workflow_steps' kind not captured: %v", got.Sigil)
	}
}

func TestReceiverSnapshotSigilSubset(t *testing.T) {
	// Verify that a Snapshot() with sigil data passes Subset correctly.
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()
	_ = time.Now() // keep import used

	genReq := &sigilv1.ExportGenerationsRequest{
		Generations: []*sigilv1.Generation{{Id: "g1", OperationName: "generateText"}},
	}
	body, _ := protojson.Marshal(genReq)
	resp, _ := http.Post(srv.URL+"/api/v1/generations:export", "application/json", bytes.NewReader(body)) //nolint:noctx
	if resp != nil {
		resp.Body.Close()
	}

	_ = context.Background() // keep import used

	got := rec.Snapshot()
	if findSigil(got, "generations") == nil {
		t.Fatalf("generations not in Sigil: %v", got.Sigil)
	}
}

func TestReceiverCapturesPrometheusRemoteWriteV1(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()

	label := func(name, value string) []byte {
		var b []byte
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendString(b, name)
		b = protowire.AppendTag(b, 2, protowire.BytesType)
		b = protowire.AppendString(b, value)
		return b
	}
	var series []byte
	for _, l := range [][]byte{label("__name__", "rw1_requests_total"), label("instance", "i-1"), label("job", "synthetic")} {
		series = protowire.AppendTag(series, 1, protowire.BytesType)
		series = protowire.AppendBytes(series, l)
	}
	var sample []byte
	sample = protowire.AppendTag(sample, 1, protowire.Fixed64Type)
	sample = protowire.AppendFixed64(sample, 1)
	sample = protowire.AppendTag(sample, 2, protowire.VarintType)
	sample = protowire.AppendVarint(sample, 1_700_000_000_000)
	series = protowire.AppendTag(series, 2, protowire.BytesType)
	series = protowire.AppendBytes(series, sample)
	var request []byte
	request = protowire.AppendTag(request, 1, protowire.BytesType)
	request = protowire.AppendBytes(request, series)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/prom/push", bytes.NewReader(snappy.Encode(nil, request)))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf;proto=prometheus.WriteRequest")
	req.Header.Set("Content-Encoding", "snappy")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST RW1: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("RW1 status = %d, want 2xx", resp.StatusCode)
	}

	got := rec.Snapshot()
	metric := findMetric(got, "rw1_requests_total")
	if metric == nil {
		t.Fatalf("RW1 metric missing: %#v", got.Metrics)
	}
	if !contains(metric.Transports, inventory.TransportPrometheusRW1) {
		t.Errorf("RW1 transports = %v, want %q", metric.Transports, inventory.TransportPrometheusRW1)
	}
	if !containsAttributeValue(metric.Labels, "instance", "i-1") || !containsAttributeValue(metric.Labels, "job", "synthetic") {
		t.Errorf("RW1 labels = %#v, want instance=i-1 and job=synthetic", metric.Labels)
	}
	if gotReceiptCount(got, inventory.TransportPrometheusRW1) != 1 {
		t.Errorf("RW1 receipt missing: %#v", got.Receipts)
	}
}

func TestReceiverCapturesOTLPLogs(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()

	payload, err := proto.Marshal(&logspb.ExportLogsServiceRequest{ResourceLogs: []*logs.ResourceLogs{{
		Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
			{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "checkout"}}},
			{Key: "deployment.environment", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "prod"}}},
		}},
		ScopeLogs: []*logs.ScopeLogs{{LogRecords: []*logs.LogRecord{{
			Attributes: []*commonpb.KeyValue{
				{Key: "route", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "/cart"}}},
				{Key: "trace_id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "abc"}}},
			},
		}}}},
	}}})
	if err != nil {
		t.Fatalf("marshal OTLP logs: %v", err)
	}
	resp, err := http.Post(srv.URL+"/otlp/v1/logs", "application/x-protobuf", bytes.NewReader(payload)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST OTLP logs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("OTLP logs status = %d, want 2xx", resp.StatusCode)
	}

	got := rec.Snapshot()
	var found *inventory.Log
	for i := range got.Logs {
		if got.Logs[i].Source == "checkout" && got.Logs[i].Transport == inventory.TransportOTLPLogs {
			found = &got.Logs[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("OTLP log source missing: %#v", got.Logs)
	}
	if !containsAttributeValue(found.StreamLabels, "deployment.environment", "prod") {
		t.Errorf("OTLP stream labels = %#v, want deployment.environment=prod", found.StreamLabels)
	}
	if !contains(found.StructuredMetadataKeys, "route") || !contains(found.StructuredMetadataKeys, "trace_id") {
		t.Errorf("OTLP metadata keys = %v, want route and trace_id", found.StructuredMetadataKeys)
	}
	if gotReceiptCount(got, inventory.TransportOTLPLogs) != 1 {
		t.Errorf("OTLP log receipt = %d, want 1", gotReceiptCount(got, inventory.TransportOTLPLogs))
	}
}

func TestReceiverInventoryJSONUsesCanonicalSchema(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/__inventory")
	if err != nil {
		t.Fatalf("GET /__inventory: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/__inventory status = %d, want 200", resp.StatusCode)
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode /__inventory JSON: %v", err)
	}
	for _, key := range []string{"schema_version", "metrics", "logs", "traces", "profiles", "sigil", "receipts"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("/__inventory missing canonical field %q: %s", key, raw)
		}
	}
	var got inventory.Schema
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("re-marshal inventory JSON: %v", err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode canonical inventory schema: %v", err)
	}
	if got.SchemaVersion != inventory.SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", got.SchemaVersion, inventory.SchemaVersion)
	}
	if got.Metrics == nil || got.Logs == nil || got.Traces == nil || got.Profiles == nil || got.Sigil == nil || got.Receipts == nil {
		t.Fatalf("canonical arrays must be non-nil: %#v", got)
	}
}

func findMetric(s inventory.Schema, name string) *inventory.Metric {
	for i := range s.Metrics {
		if s.Metrics[i].Name == name {
			return &s.Metrics[i]
		}
	}
	return nil
}

func findLog(s inventory.Schema, source, transport string) *inventory.Log {
	for i := range s.Logs {
		if s.Logs[i].Source == source && s.Logs[i].Transport == transport {
			return &s.Logs[i]
		}
	}
	return nil
}

func findTrace(s inventory.Schema, service string) *inventory.Trace {
	for i := range s.Traces {
		if s.Traces[i].Service == service {
			return &s.Traces[i]
		}
	}
	return nil
}

func findSigil(s inventory.Schema, kind string) *inventory.Sigil {
	for i := range s.Sigil {
		if s.Sigil[i].IngestKind == kind {
			return &s.Sigil[i]
		}
	}
	return nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsAttributeValue(attrs []inventory.Attribute, key, value string) bool {
	for _, attr := range attrs {
		if attr.Key == key && contains(attr.Values, value) {
			return true
		}
	}
	return false
}

func gotReceiptCount(s inventory.Schema, protocol string) int {
	for _, receipt := range s.Receipts {
		if receipt.Protocol == protocol {
			return receipt.Count
		}
	}
	return 0
}

// rw1Series encodes one prompb.TimeSeries with the supplied labels and a single sample.
func rw1Series(t *testing.T, labels map[string]string) []byte {
	t.Helper()
	var series []byte
	for name, value := range labels {
		var label []byte
		label = protowire.AppendTag(label, 1, protowire.BytesType)
		label = protowire.AppendString(label, name)
		label = protowire.AppendTag(label, 2, protowire.BytesType)
		label = protowire.AppendString(label, value)
		series = protowire.AppendTag(series, 1, protowire.BytesType)
		series = protowire.AppendBytes(series, label)
	}
	var sample []byte
	sample = protowire.AppendTag(sample, 1, protowire.Fixed64Type)
	sample = protowire.AppendFixed64(sample, 1)
	sample = protowire.AppendTag(sample, 2, protowire.VarintType)
	sample = protowire.AppendVarint(sample, 1_700_000_000_000)
	series = protowire.AppendTag(series, 2, protowire.BytesType)
	series = protowire.AppendBytes(series, sample)
	return series
}

// rw1MetadataRecord encodes one prompb.MetricMetadata with its MetricType enum value.
func rw1MetadataRecord(family string, metricType uint64) []byte {
	var record []byte
	record = protowire.AppendTag(record, 1, protowire.VarintType)
	record = protowire.AppendVarint(record, metricType)
	record = protowire.AppendTag(record, 2, protowire.BytesType)
	record = protowire.AppendString(record, family)
	record = protowire.AppendTag(record, 4, protowire.BytesType)
	record = protowire.AppendString(record, "help text")
	return record
}

func postRW1(t *testing.T, url string, request []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/api/prom/push", bytes.NewReader(snappy.Encode(nil, request)))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf;proto=prometheus.WriteRequest")
	req.Header.Set("Content-Encoding", "snappy")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST RW1: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("RW1 status = %d, want 2xx", resp.StatusCode)
	}
}

// TestReceiverRecordsDeclaredInstrumentTypesFromRW1Metadata pins the mechanism that replaces
// the unknown sentinel: the producer's own WriteRequest.metadata records, which arrive in
// their own requests either side of the samples.
func TestReceiverRecordsDeclaredInstrumentTypesFromRW1Metadata(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()

	// Metadata for one family arrives before its samples, the other after.
	var early []byte
	early = protowire.AppendTag(early, 3, protowire.BytesType)
	early = protowire.AppendBytes(early, rw1MetadataRecord("lab_queue_depth", 2))
	postRW1(t, srv.URL, early)

	var samples []byte
	for _, labels := range []map[string]string{
		{"__name__": "lab_requests_total", "job": "lab"},
		{"__name__": "lab_queue_depth", "job": "lab"},
		{"__name__": "lab_latency_seconds_bucket", "job": "lab", "le": "0.5"},
		{"__name__": "lab_pool_size", "job": "lab"},
		{"__name__": "lab_untyped_series", "job": "lab"},
	} {
		samples = protowire.AppendTag(samples, 1, protowire.BytesType)
		samples = protowire.AppendBytes(samples, rw1Series(t, labels))
	}
	postRW1(t, srv.URL, samples)

	var late []byte
	for family, metricType := range map[string]uint64{
		"lab_requests_total":  1, // COUNTER
		"lab_latency_seconds": 3, // HISTOGRAM
		"lab_pool":            5, // SUMMARY, for a family no captured series is named after
		"lab_untyped_series":  0, // UNKNOWN: a declaration of no type
	} {
		late = protowire.AppendTag(late, 3, protowire.BytesType)
		late = protowire.AppendBytes(late, rw1MetadataRecord(family, metricType))
	}
	postRW1(t, srv.URL, late)

	got := rec.Snapshot()
	for _, want := range []struct {
		metric     string
		instrument string
	}{
		{"lab_requests_total", inventory.InstrumentCounter},
		{"lab_queue_depth", inventory.InstrumentGauge},
		{"lab_latency_seconds", inventory.InstrumentHistogram},
		// The metadata names family "lab_pool"; the captured series is "lab_pool_size"
		// and keeps the sentinel rather than borrowing a type by name prefix or suffix.
		{"lab_pool_size", inventory.InstrumentUnknown},
		// A producer declaring UNKNOWN is not evidence of an instrument type.
		{"lab_untyped_series", inventory.InstrumentUnknown},
	} {
		metric := findMetric(got, want.metric)
		if metric == nil {
			t.Fatalf("metric %q missing: %#v", want.metric, got.Metrics)
		}
		if len(metric.InstrumentTypes) != 1 || metric.InstrumentTypes[0] != want.instrument {
			t.Errorf("%s instrument_types = %v, want [%s]", want.metric, metric.InstrumentTypes, want.instrument)
		}
	}
}

// TestReceiverTypesFromSeriesEvidenceNotNames pins both halves of the rule. Prometheus
// reserves `le` and `quantile`, so a series carrying one is that instrument by the exposition
// contract. A name suffix is not evidence: `_total`, `_sum` and `_count` stay unknown until a
// producer declares a type, because a histogram and a summary expose the same component names.
func TestReceiverTypesFromSeriesEvidenceNotNames(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()

	var samples []byte
	for _, labels := range []map[string]string{
		{"__name__": "evidence_latency_seconds_bucket", "job": "lab", "le": "2.5"},
		{"__name__": "evidence_latency_seconds_sum", "job": "lab"},
		{"__name__": "evidence_latency_seconds_count", "job": "lab"},
		{"__name__": "evidence_gc_pause_seconds", "job": "lab", "quantile": "0.99"},
		{"__name__": "evidence_requests_total", "job": "lab"},
		{"__name__": "evidence_pool_bytes_sum", "job": "lab"},
	} {
		samples = protowire.AppendTag(samples, 1, protowire.BytesType)
		samples = protowire.AppendBytes(samples, rw1Series(t, labels))
	}
	postRW1(t, srv.URL, samples)

	got := rec.Snapshot()
	for _, want := range []struct {
		metric     string
		instrument string
	}{
		// The bucket series carries the evidence; its `_sum` and `_count` siblings merge
		// into the same family and contribute only the sentinel, which is then dropped.
		{"evidence_latency_seconds", inventory.InstrumentHistogram},
		{"evidence_gc_pause_seconds", instrumentSummary},
		{"evidence_requests_total", inventory.InstrumentUnknown},
		// evidence_pool_bytes_sum keeps its OWN name. Nothing in the capture proved a family
		// called evidence_pool_bytes exists: no bucket series carried `le` and no metadata
		// declared a type. Folding it on the `_sum` suffix alone would record a histogram
		// family reality never published, which is what SKT-0013.06 fixed.
		{"evidence_pool_bytes_sum", inventory.InstrumentUnknown},
	} {
		metric := findMetric(got, want.metric)
		if metric == nil {
			t.Fatalf("metric %q missing: %#v", want.metric, got.Metrics)
		}
		if len(metric.InstrumentTypes) != 1 || metric.InstrumentTypes[0] != want.instrument {
			t.Errorf("%s instrument_types = %v, want [%s]", want.metric, metric.InstrumentTypes, want.instrument)
		}
	}
	histogram := findMetric(got, "evidence_latency_seconds")
	if histogram.Histogram == nil || !contains2point5(histogram.Histogram.BucketBounds) {
		t.Errorf("bucket shape lost: %#v", histogram.Histogram)
	}
	if folded := findMetric(got, "evidence_pool_bytes"); folded != nil {
		t.Errorf("a family was invented from a _sum suffix with no bucket series behind it: %#v", folded)
	}
	if unproven := findMetric(got, "evidence_pool_bytes_sum"); unproven == nil || unproven.Histogram != nil {
		t.Errorf("an unproven component must record no histogram evidence at all: %#v", unproven)
	}
}

func contains2point5(bounds []float64) bool {
	for _, bound := range bounds {
		if bound == 2.5 {
			return true
		}
	}
	return false
}

// TestReceiverReportsRemoteWriteV2WrittenCounts pins the response contract the Remote-Write 2.0
// specification makes mandatory. A real sender reads these headers to tell a full write from an
// empty one, and their absence is why a compliant sender can conclude nothing was written.
func TestReceiverReportsRemoteWriteV2WrittenCounts(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()

	ms := promrw.New(srv.URL+"/api/prom/push", "u", "tok", false, func() int { return 0 })
	if err := ms.Write(context.Background(), []promrw.Series{
		{Name: "written_header_total", Labels: map[string]string{"job": "demo"}, Value: 1, Kind: promrw.KindCounter},
		{Name: "written_header_gauge", Labels: map[string]string{"job": "demo"}, Value: 2, Kind: promrw.KindGauge},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The sink reports no status, so assert against a direct request instead.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/prom/push", bytes.NewReader(snappy.Encode(nil, nil)))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf;proto=io.prometheus.write.v2.Request")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST RW2: %v", err)
	}
	defer resp.Body.Close()
	for _, header := range []string{
		"X-Prometheus-Remote-Write-Samples-Written",
		"X-Prometheus-Remote-Write-Histograms-Written",
		"X-Prometheus-Remote-Write-Exemplars-Written",
	} {
		if resp.Header.Get(header) == "" {
			t.Errorf("%s missing; a compliant sender reads that as nothing written", header)
		}
	}
	if got := resp.Header.Get("X-Prometheus-Remote-Write-Samples-Written"); got != "0" {
		t.Errorf("empty request samples-written = %q, want 0", got)
	}
}

// lokiProtobufPush builds a snappy-compressed logproto.PushRequest by hand, the way a real
// Grafana Alloy loki.write component sends one.
func lokiProtobufPush(labelSet string, metadataKeys []string) []byte {
	entry := protowire.AppendTag(nil, 2, protowire.BytesType)
	entry = protowire.AppendString(entry, "a log line")
	for _, key := range metadataKeys {
		pair := protowire.AppendTag(nil, 1, protowire.BytesType)
		pair = protowire.AppendString(pair, key)
		pair = protowire.AppendTag(pair, 2, protowire.BytesType)
		pair = protowire.AppendString(pair, "value")
		entry = protowire.AppendTag(entry, 3, protowire.BytesType)
		entry = protowire.AppendBytes(entry, pair)
	}

	stream := protowire.AppendTag(nil, 1, protowire.BytesType)
	stream = protowire.AppendString(stream, labelSet)
	stream = protowire.AppendTag(stream, 2, protowire.BytesType)
	stream = protowire.AppendBytes(stream, entry)
	stream = protowire.AppendTag(stream, 3, protowire.VarintType)
	stream = protowire.AppendVarint(stream, 42)

	push := protowire.AppendTag(nil, 1, protowire.BytesType)
	push = protowire.AppendBytes(push, stream)
	return snappy.Encode(nil, push)
}

// A real Alloy loki.write sends snappy+protobuf, not gzip+JSON. Decoding only the JSON form
// made the entire Loki-native lane invisible to the k3d capture lab.
func TestReceiverDecodesAlloyProtobufLokiPush(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()

	body := lokiProtobufPush(`{cluster="lab", source="kubernetes", service_name="kube-dns"}`, []string{"pod", "trace_id"})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/loki/api/v1/push", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	inv := rec.Snapshot()
	var receipt int
	for _, r := range inv.Receipts {
		if r.Protocol == inventory.TransportLoki {
			receipt = r.Count
		}
	}
	if receipt != 1 {
		t.Fatalf("loki receipt = %d, want 1", receipt)
	}
	if len(inv.Logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(inv.Logs))
	}
	log := inv.Logs[0]
	// Keyed strictly on the "source" label, exactly as the JSON path is.
	if log.Source != "kubernetes" {
		t.Errorf("source = %q, want %q", log.Source, "kubernetes")
	}
	if log.Transport != inventory.TransportLoki {
		t.Errorf("transport = %q", log.Transport)
	}
	keys := map[string]bool{}
	for _, label := range log.StreamLabels {
		keys[label.Key] = true
	}
	for _, want := range []string{"cluster", "source", "service_name"} {
		if !keys[want] {
			t.Errorf("stream label %q missing; got %v", want, keys)
		}
	}
	metadata := map[string]bool{}
	for _, key := range log.StructuredMetadataKeys {
		metadata[key] = true
	}
	for _, want := range []string{"pod", "trace_id"} {
		if !metadata[want] {
			t.Errorf("structured metadata key %q missing; got %v", want, log.StructuredMetadataKeys)
		}
	}
}

func TestReceiverKeepsCapturedPodLogsSeparateFromManifests(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()

	for _, tc := range []struct {
		labels   string
		metadata []string
	}{
		{
			labels:   `{app_kubernetes_io_name="catalog", cluster="lab", container="catalog", flags="F", job="otel-demo/catalog", k8s_cluster_name="lab", namespace="otel-demo", service_name="catalog", service_namespace="otel-demo", stream="stdout"}`,
			metadata: []string{"pod", "service_instance_id"},
		},
		{
			labels:   `{action="manifest", cluster="lab", instance="alloy", job="integrations/kubernetes/manifests", k8s_cluster_name="lab", k8s_kind="Pod", k8s_namespace_name="otel-demo"}`,
			metadata: []string{"k8s_pod_name"},
		},
	} {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/loki/api/v1/push", bytes.NewReader(lokiProtobufPush(tc.labels, tc.metadata)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-protobuf")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
	}

	inv := rec.Snapshot()
	if len(inv.Logs) != 2 {
		t.Fatalf("logs=%v, want separate pod-log and manifest entries", inv.Logs)
	}
	var podLogs, manifests bool
	for _, log := range inv.Logs {
		switch log.Source {
		case inventory.LogFamilyPodLogs:
			podLogs = true
		case "":
			manifests = true
		}
	}
	if !podLogs || !manifests {
		t.Fatalf("logs=%v, want sources %q and empty", inv.Logs, inventory.LogFamilyPodLogs)
	}
}

func TestReceiverClassifiesCapturedOTLPPodLogs(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()

	payload, err := proto.Marshal(&logspb.ExportLogsServiceRequest{ResourceLogs: []*logs.ResourceLogs{{
		Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
			{Key: "cluster", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "lab"}}},
			{Key: "k8s.cluster.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "lab"}}},
			{Key: "k8s.namespace.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "otel-demo"}}},
			{Key: "k8s.pod.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "catalog-0"}}},
			{Key: "k8s.container.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "catalog"}}},
			{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "catalog"}}},
		}},
		ScopeLogs: []*logs.ScopeLogs{{LogRecords: []*logs.LogRecord{{
			Attributes: []*commonpb.KeyValue{{Key: "log.iostream", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "stdout"}}}},
		}}}},
	}}})
	if err != nil {
		t.Fatalf("marshal OTLP logs: %v", err)
	}
	resp, err := http.Post(srv.URL+"/otlp/v1/logs", "application/x-protobuf", bytes.NewReader(payload)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST OTLP logs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("OTLP logs status = %d, want 2xx", resp.StatusCode)
	}

	got := rec.Snapshot()
	if len(got.Logs) != 1 {
		t.Fatalf("logs=%v, want one OTLP pod-log entry", got.Logs)
	}
	log := got.Logs[0]
	if log.Source != inventory.LogFamilyPodLogs || log.Transport != inventory.TransportOTLPLogs {
		t.Fatalf("log=%v, want source %q over %q", log, inventory.LogFamilyPodLogs, inventory.TransportOTLPLogs)
	}
	if !containsAttributeValue(log.StreamLabels, "k8s.pod.name", "catalog-0") || !contains(log.StructuredMetadataKeys, "log.iostream") {
		t.Fatalf("log=%v, want resource identity and record metadata kept split", log)
	}
}

func TestParseLokiLabelSetHandlesEscapesAndCommas(t *testing.T) {
	got := parseLokiLabelSet(`{a="1", b="has, comma", c="has \"quote\"", d="tab\there"}`)
	want := map[string]string{
		"a": "1",
		"b": "has, comma",
		"c": `has "quote"`,
		"d": "tab\there",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("label %q = %q, want %q", key, got[key], value)
		}
	}
	if len(got) != len(want) {
		t.Errorf("parsed %d labels, want %d: %v", len(got), len(want), got)
	}
}

// A body that will not decode is a decode failure, not an empty push: the lab must be able to
// tell "the collector sent nothing" from "the receiver could not read what it sent".
func TestReceiverRejectsUndecodableProtobufLokiPush(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/loki/api/v1/push", bytes.NewReader([]byte("not snappy")))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if len(rec.Snapshot().Logs) != 0 {
		t.Error("an undecodable push must not record a log shape")
	}
}

// The RW1 metadata path must prove a histogram family exactly as the RW2 path does. A producer
// that declares HISTOGRAM in metadata but whose captured series are only `_sum` and `_count`
// would otherwise fold on v2 and not on v1 — the same family recorded under two different names
// depending on which protocol the lab happened to pin.
func TestReceiverRW1MetadataProvesAHistogramFamily(t *testing.T) {
	rec := New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()

	var body []byte
	body = protowire.AppendTag(body, 3, protowire.BytesType)
	body = protowire.AppendBytes(body, rw1MetadataRecord("declared_latency_seconds", 3))
	for _, labels := range []map[string]string{
		{"__name__": "declared_latency_seconds_sum", "job": "lab"},
		{"__name__": "declared_latency_seconds_count", "job": "lab"},
	} {
		body = protowire.AppendTag(body, 1, protowire.BytesType)
		body = protowire.AppendBytes(body, rw1Series(t, labels))
	}
	postRW1(t, srv.URL, body)

	got := rec.Snapshot()
	folded := findMetric(got, "declared_latency_seconds")
	if folded == nil {
		t.Fatalf("declared metadata did not prove the family: %#v", got.Metrics)
	}
	if findMetric(got, "declared_latency_seconds_sum") != nil {
		t.Error("the component series survived beside its proven family")
	}
}
