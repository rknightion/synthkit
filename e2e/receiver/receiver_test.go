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
