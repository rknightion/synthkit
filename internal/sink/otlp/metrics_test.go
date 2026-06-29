// SPDX-License-Identifier: AGPL-3.0-only

package otlp

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func readBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// decodeResourceMetrics gunzips the captured body, walks the ExportMetricsServiceRequest
// envelope (field 1, repeated LEN ResourceMetrics) and unmarshals each.
func decodeResourceMetrics(t *testing.T, body []byte) []*metricspb.ResourceMetrics {
	t.Helper()
	raw := gunzip(t, body) // sibling helper in metrics_test.go (see Step 2b)
	var out []*metricspb.ResourceMetrics
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 || num != 1 || typ != protowire.BytesType {
			t.Fatalf("unexpected envelope tag num=%d typ=%d", num, typ)
		}
		raw = raw[n:]
		v, n := protowire.ConsumeBytes(raw)
		if n < 0 {
			t.Fatal("bad LEN record")
		}
		raw = raw[n:]
		rm := &metricspb.ResourceMetrics{}
		if err := proto.Unmarshal(v, rm); err != nil {
			t.Fatal(err)
		}
		out = append(out, rm)
	}
	return out
}

func TestMetricsWriteEncodesSumAndHistogram(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r) // sibling helper: io.ReadAll(r.Body)
		if r.URL.Path != "/v1/metrics" {
			t.Errorf("path = %q, want /v1/metrics", r.URL.Path)
		}
		if ce := r.Header.Get("Content-Encoding"); ce != "gzip" {
			t.Errorf("content-encoding = %q, want gzip", ce)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	s := NewMetrics(srv.URL, "u", "t", false)
	start, now := time.Unix(100, 0), time.Unix(160, 0)
	res := MetricResource{
		Attrs: map[string]any{"service.name": "checkout", "service.namespace": "shop"},
		Scope: Scope{Name: "go.opentelemetry.io/otel", Version: "1.34.0"},
		Metrics: []Metric{
			{
				Name: "http.server.request.count", Unit: "1", Kind: MetricSum, Monotonic: true, Temporality: TemporalityCumulative,
				Numbers: []NumberPoint{{Attrs: map[string]any{"http.response.status_code": int64(200)}, Start: start, Time: now, Value: 42}},
			},
			{
				Name: "http.server.request.duration", Unit: "s", Kind: MetricHistogram, Temporality: TemporalityCumulative,
				Histograms: []HistogramPoint{{
					Attrs: map[string]any{"http.route": "/checkout"}, Start: start, Time: now,
					Count: 3, Sum: 0.42, Bounds: []float64{0.005, 0.01, 0.025},
					BucketCounts: []uint64{1, 1, 0, 1}, // len == len(Bounds)+1
				}},
			},
		},
	}
	if err := s.Write(context.Background(), []MetricResource{res}); err != nil {
		t.Fatal(err)
	}

	rms := decodeResourceMetrics(t, body)
	if len(rms) != 1 {
		t.Fatalf("got %d ResourceMetrics, want 1", len(rms))
	}
	rm := rms[0]
	if rm.ScopeMetrics[0].Scope.Name != "go.opentelemetry.io/otel" {
		t.Errorf("scope = %q", rm.ScopeMetrics[0].Scope.Name)
	}
	ms := rm.ScopeMetrics[0].Metrics
	if len(ms) != 2 {
		t.Fatalf("got %d metrics, want 2", len(ms))
	}
	sum := ms[0].GetSum()
	if sum == nil || !sum.IsMonotonic || sum.AggregationTemporality != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
		t.Fatalf("sum shape wrong: %+v", sum)
	}
	if got := sum.DataPoints[0].GetAsDouble(); got != 42 {
		t.Errorf("sum value = %v, want 42", got)
	}
	if sum.DataPoints[0].StartTimeUnixNano != uint64(start.UnixNano()) {
		t.Errorf("sum start = %d", sum.DataPoints[0].StartTimeUnixNano)
	}
	h := ms[1].GetHistogram()
	if h == nil || len(h.DataPoints[0].BucketCounts) != 4 || len(h.DataPoints[0].ExplicitBounds) != 3 {
		t.Fatalf("histogram shape wrong: %+v", h)
	}
	if h.DataPoints[0].GetSum() != 0.42 || h.DataPoints[0].Count != 3 {
		t.Errorf("histogram sum/count wrong")
	}
}

func TestMetricsDryRunRecordsInventory(t *testing.T) {
	s := NewMetrics("http://unused", "u", "t", true)
	res := MetricResource{
		Attrs:   map[string]any{"service.name": "checkout"},
		Metrics: []Metric{{Name: "http.server.request.count", Kind: MetricSum, Monotonic: true}},
	}
	if err := s.Write(context.Background(), []MetricResource{res}); err != nil {
		t.Fatal(err)
	}
	_, names := s.Inventory()
	if got := names["checkout"]; len(got) != 1 || got[0] != "http.server.request.count" {
		t.Errorf("inventory = %v", got)
	}
}
