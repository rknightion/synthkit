// SPDX-License-Identifier: AGPL-3.0-only

package receiver

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
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
