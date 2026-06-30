// SPDX-License-Identifier: AGPL-3.0-only

package receiver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

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
	ms := promrw.New(srv.URL+"/api/v1/write", "u", "tok", false, func() int { return 0 })
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
	os := otlp.New(srv.URL, "u", "tok", false)
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
	oms := otlp.NewMetrics(srv.URL, "u", "tok", false)
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
	if _, ok := got.Metrics["e2e_demo_total"]; !ok {
		t.Errorf("metric not captured: %v", got.Metrics)
	} else {
		keys := got.Metrics["e2e_demo_total"]
		hasCluster, hasJob := false, false
		for _, k := range keys {
			if k == "cluster" {
				hasCluster = true
			}
			if k == "job" {
				hasJob = true
			}
		}
		if !hasCluster || !hasJob {
			t.Errorf("e2e_demo_total label keys = %v, want cluster+job", keys)
		}
	}

	// Logs
	if _, ok := got.LogSources["e2e_app"]; !ok {
		t.Errorf("log source not captured: %v", got.LogSources)
	}

	// Traces
	if _, ok := got.Traces["checkout"]; !ok {
		t.Errorf("trace service not captured: %v", got.Traces)
	}

	// OTLP metrics
	if _, ok := got.Metrics["http.server.request.count"]; !ok {
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
	ops, ok := got.Sigil["generations"]
	if !ok {
		t.Fatalf("sigil 'generations' kind not captured: %v", got.Sigil)
	}
	opSet := map[string]bool{}
	for _, op := range ops {
		opSet[op] = true
	}
	if !opSet["generateText"] {
		t.Errorf("missing operation 'generateText' in %v", ops)
	}
	if !opSet["streamText"] {
		t.Errorf("missing operation 'streamText' in %v", ops)
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
	if _, ok := got.Sigil["scores"]; !ok {
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
	if _, ok := got.Sigil["workflow_steps"]; !ok {
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
	if _, ok := got.Sigil["generations"]; !ok {
		t.Fatalf("generations not in Sigil: %v", got.Sigil)
	}

	// expected ⊆ received — same data on both sides
	missing := got.Subset(got)
	if len(missing) > 0 {
		t.Errorf("self-subset found missing entries: %v", missing)
	}
}
