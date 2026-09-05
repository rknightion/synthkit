// SPDX-License-Identifier: AGPL-3.0-only

package otlp

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
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

// --- Characterisation: the live-validated web_service OTLP shape must not move ---------------

// sortDataPointAttrs orders every attribute slice in a ResourceMetrics by key so the
// canonical digest is stable despite kvs() iterating a Go map.
func sortDataPointAttrs(rm *metricspb.ResourceMetrics) {
	sortKVs := func(kv []*commonpb.KeyValue) {
		sort.Slice(kv, func(i, j int) bool { return kv[i].GetKey() < kv[j].GetKey() })
	}
	sortKVs(rm.GetResource().GetAttributes())
	for _, sm := range rm.GetScopeMetrics() {
		sortKVs(sm.GetScope().GetAttributes())
		for _, m := range sm.GetMetrics() {
			for _, p := range m.GetGauge().GetDataPoints() {
				sortKVs(p.GetAttributes())
			}
			for _, p := range m.GetSum().GetDataPoints() {
				sortKVs(p.GetAttributes())
			}
			for _, p := range m.GetHistogram().GetDataPoints() {
				sortKVs(p.GetAttributes())
			}
			for _, p := range m.GetExponentialHistogram().GetDataPoints() {
				sortKVs(p.GetAttributes())
			}
		}
	}
}

// canonicalDigest hashes the FULL decoded wire content (every field of every message), not a
// hand-picked assertion subset: attribute order is canonicalised, then each ResourceMetrics is
// re-marshalled deterministically and the concatenation SHA-256'd. Any change to any encoded
// value — a field newly set, a temporality flipped, a start time dropped — moves the digest.
func canonicalDigest(t *testing.T, body []byte) string {
	t.Helper()
	rms := decodeResourceMetrics(t, body)
	var buf []byte
	for _, rm := range rms {
		sortDataPointAttrs(rm)
		b, err := (proto.MarshalOptions{Deterministic: true}).Marshal(rm)
		if err != nil {
			t.Fatal(err)
		}
		buf = append(buf, b...)
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

// webServiceShapeFixture reproduces EXACTLY what internal/workload/webservice/otlpmetrics.go
// hands the sink for one k8s_monitoring-mode tick: the 14-bound cumulative explicit histogram
// http.server.request.duration and the non-monotonic cumulative Sum http.server.active_requests,
// under the real otelhttp instrumentation scope and the enriched resource-attribute set.
// Values are fixed, not drawn, so the digest is a pure function of the ENCODER.
func webServiceShapeFixture() MetricResource {
	start, now := time.Unix(1750000000, 0), time.Unix(1750000060, 0)
	bounds := []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}
	return MetricResource{
		Attrs: map[string]any{
			"service.name":                "otlp-api-enriched",
			"service.namespace":           "otlp-api-enriched",
			"service.version":             "1.0.0",
			"service.instance.id":         "otlp-api-enriched-6c9a537dc6-74b6f",
			"deployment.environment.name": "prod",
			"telemetry.sdk.name":          "opentelemetry",
			"telemetry.sdk.language":      "go",
			"telemetry.sdk.version":       "1.34.0",
			"k8s.namespace.name":          "otlp-api-enriched",
			"k8s.pod.name":                "otlp-api-enriched-6c9a537dc6-74b6f",
			"k8s.deployment.name":         "otlp-api-enriched",
			"k8s.cluster.name":            "otlp-native-prod-euw1",
			"k8s.node.name":               "ip-10-0-254-253.eu-west-1.compute.internal",
			"host.name":                   "otlp-api-enriched-6c9a537dc6-74b6f",
			"host.arch":                   "amd64",
			"os.type":                     "linux",
		},
		Scope: Scope{Name: "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp", Version: "0.58.0"},
		Metrics: []Metric{
			{
				Name: "http.server.request.duration", Unit: "s",
				Kind: MetricHistogram, Temporality: TemporalityCumulative,
				Histograms: []HistogramPoint{
					{
						Attrs: map[string]any{
							"http.request.method":       "GET",
							"http.route":                "/api/v1/health",
							"http.response.status_code": "200",
						},
						Start: start, Time: now, Count: 12, Sum: 1.44,
						Bounds:       bounds,
						BucketCounts: []uint64{0, 0, 0, 0, 0, 1, 4, 5, 2, 0, 0, 0, 0, 0, 0},
					},
					{
						Attrs: map[string]any{
							"http.request.method":       "POST",
							"http.route":                "/api/v1/process",
							"http.response.status_code": "500",
						},
						Start: start, Time: now, Count: 3, Sum: 0.51,
						Bounds:       bounds,
						BucketCounts: []uint64{0, 0, 0, 0, 0, 0, 1, 1, 1, 0, 0, 0, 0, 0, 0},
					},
				},
			},
			{
				Name: "http.server.active_requests", Unit: "{request}",
				Kind: MetricSum, Monotonic: false, Temporality: TemporalityCumulative,
				Numbers: []NumberPoint{
					{Attrs: map[string]any{"http.request.method": "GET", "url.scheme": "https"}, Start: start, Time: now, Value: 0.75},
					{Attrs: map[string]any{"http.request.method": "POST", "url.scheme": "https"}, Start: start, Time: now, Value: 0.25},
				},
			},
		},
	}
}

// webServiceGoldenDigest is the canonical digest of the fixture above, RECORDED against the
// encoder as it stood before SKT-0007.02 added the further instrument shapes. A change here
// means the live-validated web_service OTLP output moved and the 2026-06-18 capture in
// signals/otlp-metrics.md no longer describes what synthkit sends. Re-record ONLY alongside a
// fresh live capture.
const webServiceGoldenDigest = "270c094cd14722787443543ac535e9ee1f8747f744336798fe3f91c90827be9b"

func TestWebServiceOTLPShapeByteUnchanged(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	s := NewMetrics(srv.URL, "u", "t", false)
	if err := s.Write(context.Background(), []MetricResource{webServiceShapeFixture()}); err != nil {
		t.Fatal(err)
	}
	if got := canonicalDigest(t, body); got != webServiceGoldenDigest {
		t.Fatalf("web_service OTLP encoding changed:\n got  %s\n want %s", got, webServiceGoldenDigest)
	}
}

// --- Instrument-shape coverage (SKT-0007.02 AC#1/#6) ------------------------------------------

// writeOne posts one MetricResource through a real sink and returns the decoded metrics.
func writeOne(t *testing.T, res MetricResource) []*metricspb.Metric {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	s := NewMetrics(srv.URL, "u", "t", false)
	if err := s.Write(context.Background(), []MetricResource{res}); err != nil {
		t.Fatal(err)
	}
	rms := decodeResourceMetrics(t, body)
	// Check the outer length first: the failure message indexes rms[0], so an empty result must
	// report the intended failure rather than panicking on the way to reporting it.
	if len(rms) != 1 {
		t.Fatalf("want exactly 1 ResourceMetrics, got %d", len(rms))
	}
	if len(rms[0].GetScopeMetrics()) != 1 {
		t.Fatalf("want exactly 1 ScopeMetrics, got %d", len(rms[0].GetScopeMetrics()))
	}
	return rms[0].GetScopeMetrics()[0].GetMetrics()
}

func res1(m Metric) MetricResource {
	return MetricResource{Attrs: map[string]any{"service.name": "svc"}, Metrics: []Metric{m}}
}

var (
	tStart = time.Unix(1750000000, 0)
	tNow   = time.Unix(1750000060, 0)
)

// TestEveryInstrumentShapeReachesTheWire covers the six shapes the lane must carry: Gauge,
// monotonic Sum, non-monotonic Sum (UpDownCounter), explicit-bound Histogram and
// ExponentialHistogram — each landing in the right OTLP data oneof.
func TestEveryInstrumentShapeReachesTheWire(t *testing.T) {
	num := []NumberPoint{{Attrs: map[string]any{"k": "v"}, Start: tStart, Time: tNow, Value: 7}}
	expPt := []ExponentialHistogramPoint{{
		Start: tStart, Time: tNow, Count: 6, Sum: 1.5, Scale: 3,
		Positive: ExponentialBuckets{Offset: -4, BucketCounts: []uint64{2, 0, 4}},
	}}
	histPt := []HistogramPoint{{
		Start: tStart, Time: tNow, Count: 3, Sum: 0.42,
		Bounds: []float64{0.005, 0.01}, BucketCounts: []uint64{1, 1, 1},
	}}

	cases := []struct {
		name   string
		metric Metric
		check  func(*testing.T, *metricspb.Metric)
	}{
		{"gauge", Metric{Name: "queue.length", Kind: MetricGauge, Numbers: num},
			func(t *testing.T, m *metricspb.Metric) {
				g := m.GetGauge()
				if g == nil {
					t.Fatalf("not a Gauge: %T", m.GetData())
				}
				if got := g.GetDataPoints()[0].GetAsDouble(); got != 7 {
					t.Errorf("value = %v, want 7", got)
				}
				// Gauges are point-in-time: no cumulative start time on the wire.
				if st := g.GetDataPoints()[0].GetStartTimeUnixNano(); st != 0 {
					t.Errorf("gauge start = %d, want 0 (omitted)", st)
				}
			}},
		{"monotonic sum", Metric{Name: "http.server.request.count", Kind: MetricSum, Monotonic: true, Numbers: num},
			func(t *testing.T, m *metricspb.Metric) {
				s := m.GetSum()
				if s == nil {
					t.Fatalf("not a Sum: %T", m.GetData())
				}
				if !s.GetIsMonotonic() {
					t.Error("is_monotonic = false, want true (Counter)")
				}
				if st := s.GetDataPoints()[0].GetStartTimeUnixNano(); st != uint64(tStart.UnixNano()) {
					t.Errorf("sum start = %d, want %d", st, tStart.UnixNano())
				}
			}},
		{"non-monotonic sum", Metric{Name: "http.server.active_requests", Kind: MetricSum, Monotonic: false, Numbers: num},
			func(t *testing.T, m *metricspb.Metric) {
				s := m.GetSum()
				if s == nil {
					t.Fatalf("not a Sum: %T", m.GetData())
				}
				if s.GetIsMonotonic() {
					t.Error("is_monotonic = true, want false (UpDownCounter)")
				}
			}},
		{"explicit histogram", Metric{Name: "http.server.request.duration", Kind: MetricHistogram, Histograms: histPt},
			func(t *testing.T, m *metricspb.Metric) {
				h := m.GetHistogram()
				if h == nil {
					t.Fatalf("not a Histogram: %T", m.GetData())
				}
				dp := h.GetDataPoints()[0]
				if len(dp.GetExplicitBounds()) != 2 || len(dp.GetBucketCounts()) != 3 {
					t.Errorf("bounds/counts = %d/%d, want 2/3", len(dp.GetExplicitBounds()), len(dp.GetBucketCounts()))
				}
			}},
		{"exponential histogram", Metric{Name: "traces.span.metrics.duration", Kind: MetricExponentialHistogram, ExpHistograms: expPt},
			func(t *testing.T, m *metricspb.Metric) {
				e := m.GetExponentialHistogram()
				if e == nil {
					t.Fatalf("not an ExponentialHistogram: %T", m.GetData())
				}
				dp := e.GetDataPoints()[0]
				if dp.GetScale() != 3 {
					t.Errorf("scale = %d, want 3", dp.GetScale())
				}
				if dp.GetPositive().GetOffset() != -4 {
					t.Errorf("positive offset = %d, want -4", dp.GetPositive().GetOffset())
				}
				if got := dp.GetPositive().GetBucketCounts(); len(got) != 3 || got[1] != 0 || got[2] != 4 {
					t.Errorf("positive counts = %v, want [2 0 4] (dense, interior zero explicit)", got)
				}
				if dp.GetNegative() != nil {
					t.Error("negative buckets emitted for a non-negative distribution")
				}
				if dp.GetCount() != 6 || dp.GetSum() != 1.5 {
					t.Errorf("count/sum = %d/%v, want 6/1.5", dp.GetCount(), dp.GetSum())
				}
				if dp.GetStartTimeUnixNano() != uint64(tStart.UnixNano()) {
					t.Errorf("exp-histogram start = %d, want %d", dp.GetStartTimeUnixNano(), tStart.UnixNano())
				}
			}},
		{"summary", Metric{Name: "amazonaws.com/AWS/EC2/CPUUtilization", Unit: "%", Kind: MetricSummary,
			Summaries: []SummaryPoint{{Attrs: map[string]any{"Dimensions": map[string]string{"InstanceId": "i-0123"}}, Time: tNow, Count: 2, Sum: 90, Quantiles: map[float64]float64{0: 30, 1: 60}}}},
			func(t *testing.T, m *metricspb.Metric) {
				point := m.GetSummary().GetDataPoints()[0]
				if point.GetCount() != 2 || point.GetSum() != 90 || len(point.GetQuantileValues()) != 2 {
					t.Fatalf("summary=%+v", point)
				}
				dims := point.GetAttributes()[0].GetValue().GetKvlistValue().GetValues()
				if len(dims) != 1 || dims[0].GetKey() != "InstanceId" || dims[0].GetValue().GetStringValue() != "i-0123" {
					t.Fatalf("dimensions=%+v", dims)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := writeOne(t, res1(tc.metric))
			if len(ms) != 1 {
				t.Fatalf("got %d metrics, want 1", len(ms))
			}
			if ms[0].GetName() != tc.metric.Name {
				t.Errorf("name = %q, want %q", ms[0].GetName(), tc.metric.Name)
			}
			tc.check(t, ms[0])
		})
	}
}

func TestMetricsWriteRejectsInvalidSummaryBeforePosting(t *testing.T) {
	cases := []struct {
		name  string
		point SummaryPoint
	}{
		{
			name:  "quantile above one",
			point: SummaryPoint{Time: tNow, Count: 1, Sum: 1, Quantiles: map[float64]float64{1.1: 1}},
		},
		{
			name:  "negative quantile value",
			point: SummaryPoint{Time: tNow, Count: 1, Sum: 1, Quantiles: map[float64]float64{0.5: -1}},
		},
		{
			name:  "zero count with sum",
			point: SummaryPoint{Time: tNow, Count: 0, Sum: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			sink := NewMetrics(srv.URL, "u", "t", false)
			err := sink.Write(context.Background(), []MetricResource{res1(Metric{
				Name: "summary.metric", Kind: MetricSummary, Summaries: []SummaryPoint{tc.point},
			})})
			if err == nil {
				t.Fatal("Write succeeded for invalid Summary")
			}
			if requests != 0 {
				t.Fatalf("requests = %d, want 0", requests)
			}
		})
	}
}

// TestExponentialHistogramOptionalFields pins the fields that are conditional on the wire:
// negative buckets and min/max appear only when the caller supplied them.
func TestExponentialHistogramOptionalFields(t *testing.T) {
	ms := writeOne(t, res1(Metric{
		Name: "rpc.client.duration", Kind: MetricExponentialHistogram,
		ExpHistograms: []ExponentialHistogramPoint{{
			Time: tNow, Count: 5, Sum: 2, Scale: 0, ZeroCount: 1, ZeroThreshold: 1e-6,
			Positive: ExponentialBuckets{Offset: 0, BucketCounts: []uint64{2}},
			Negative: ExponentialBuckets{Offset: -1, BucketCounts: []uint64{2}},
			Min:      0.1, Max: 0.9, HasMinMax: true,
		}},
	}))
	dp := ms[0].GetExponentialHistogram().GetDataPoints()[0]
	if dp.GetNegative().GetOffset() != -1 || len(dp.GetNegative().GetBucketCounts()) != 1 {
		t.Errorf("negative = %+v, want offset -1 with 1 bucket", dp.GetNegative())
	}
	if dp.GetZeroCount() != 1 || dp.GetZeroThreshold() != 1e-6 {
		t.Errorf("zero count/threshold = %d/%v", dp.GetZeroCount(), dp.GetZeroThreshold())
	}
	if dp.Min == nil || dp.Max == nil || dp.GetMin() != 0.1 || dp.GetMax() != 0.9 {
		t.Errorf("min/max not carried: %v/%v", dp.Min, dp.Max)
	}
	// A start time the caller left zero must be OMITTED, never encoded as epoch 0.
	if dp.GetStartTimeUnixNano() != 0 {
		t.Errorf("start = %d, want 0 (omitted)", dp.GetStartTimeUnixNano())
	}
	// No min/max supplied ⇒ both absent.
	ms = writeOne(t, res1(Metric{
		Name: "rpc.client.duration", Kind: MetricExponentialHistogram,
		ExpHistograms: []ExponentialHistogramPoint{{Time: tNow, Count: 1, Sum: 1, Scale: 0,
			Positive: ExponentialBuckets{BucketCounts: []uint64{1}}}},
	}))
	if dp := ms[0].GetExponentialHistogram().GetDataPoints()[0]; dp.Min != nil || dp.Max != nil {
		t.Errorf("min/max set without HasMinMax: %v/%v", dp.Min, dp.Max)
	}
}

// --- Temporality decision (SKT-0007.02 AC#2/#6) -----------------------------------------------

// TestTemporalityDefaultsToCumulative pins the recorded decision: synthkit emits CUMULATIVE, and
// the zero value of Temporality is cumulative for every aggregating shape — so a caller that
// never thinks about temporality cannot accidentally ship delta into a gateway whose delta
// ingestion is experimental. See the evidence block on Temporality in metric_types.go.
func TestTemporalityDefaultsToCumulative(t *testing.T) {
	const want = metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE
	cases := []struct {
		name string
		m    Metric
		got  func(*metricspb.Metric) metricspb.AggregationTemporality
	}{
		{"sum", Metric{Name: "a", Kind: MetricSum, Monotonic: true,
			Numbers: []NumberPoint{{Start: tStart, Time: tNow, Value: 1}}},
			func(m *metricspb.Metric) metricspb.AggregationTemporality {
				return m.GetSum().GetAggregationTemporality()
			}},
		{"histogram", Metric{Name: "b", Kind: MetricHistogram,
			Histograms: []HistogramPoint{{Start: tStart, Time: tNow, Count: 1, Sum: 1, Bounds: []float64{1}, BucketCounts: []uint64{1, 0}}}},
			func(m *metricspb.Metric) metricspb.AggregationTemporality {
				return m.GetHistogram().GetAggregationTemporality()
			}},
		{"exponential histogram", Metric{Name: "c", Kind: MetricExponentialHistogram,
			ExpHistograms: []ExponentialHistogramPoint{{Start: tStart, Time: tNow, Count: 1, Sum: 1,
				Positive: ExponentialBuckets{BucketCounts: []uint64{1}}}}},
			func(m *metricspb.Metric) metricspb.AggregationTemporality {
				return m.GetExponentialHistogram().GetAggregationTemporality()
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Temporality deliberately left at its zero value.
			if got := tc.got(writeOne(t, res1(tc.m))[0]); got != want {
				t.Errorf("temporality = %v, want %v", got, want)
			}
		})
	}
}

// TestTemporalityDeltaEncodedWhenExplicitlySet proves TemporalityDelta round-trips honestly
// rather than being silently rewritten. No synthkit lane sets it — see metric_types.go.
func TestTemporalityDeltaEncodedWhenExplicitlySet(t *testing.T) {
	const want = metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA
	ms := writeOne(t, MetricResource{
		Attrs: map[string]any{"service.name": "svc"},
		Metrics: []Metric{
			{Name: "a", Kind: MetricSum, Monotonic: true, Temporality: TemporalityDelta,
				Numbers: []NumberPoint{{Time: tNow, Value: 1}}},
			{Name: "b", Kind: MetricHistogram, Temporality: TemporalityDelta,
				Histograms: []HistogramPoint{{Time: tNow, Count: 1, Sum: 1, Bounds: []float64{1}, BucketCounts: []uint64{1, 0}}}},
			{Name: "c", Kind: MetricExponentialHistogram, Temporality: TemporalityDelta,
				ExpHistograms: []ExponentialHistogramPoint{{Time: tNow, Count: 1, Sum: 1,
					Positive: ExponentialBuckets{BucketCounts: []uint64{1}}}}},
		},
	})
	if got := ms[0].GetSum().GetAggregationTemporality(); got != want {
		t.Errorf("sum temporality = %v, want %v", got, want)
	}
	if got := ms[1].GetHistogram().GetAggregationTemporality(); got != want {
		t.Errorf("histogram temporality = %v, want %v", got, want)
	}
	if got := ms[2].GetExponentialHistogram().GetAggregationTemporality(); got != want {
		t.Errorf("exp-histogram temporality = %v, want %v", got, want)
	}
}

// --- Instrumentation scope on the wire (SKT-0007.02 AC#3) -------------------------------------

// TestScopeOnTheWire pins the recorded scope decision: exactly one ScopeMetrics per resource,
// carrying the real instrumentation library name+version and NO scope attributes; a zero Scope
// falls back to the "synthkit" name rather than emitting an unnamed scope.
func TestScopeOnTheWire(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	s := NewMetrics(srv.URL, "u", "t", false)
	err := s.Write(context.Background(), []MetricResource{
		{
			Attrs: map[string]any{"service.name": "named"},
			Scope: Scope{Name: "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp", Version: "0.58.0"},
			Metrics: []Metric{{Name: "a", Kind: MetricGauge,
				Numbers: []NumberPoint{{Time: tNow, Value: 1}}}},
		},
		{
			Attrs: map[string]any{"service.name": "unnamed"},
			Metrics: []Metric{{Name: "b", Kind: MetricGauge,
				Numbers: []NumberPoint{{Time: tNow, Value: 1}}}},
		},
		{
			PreserveEmptyScope: true,
			Metrics: []Metric{{Name: "captured", Kind: MetricGauge,
				Numbers: []NumberPoint{{Time: tNow, Value: 1}}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rms := decodeResourceMetrics(t, body)
	if len(rms) != 3 {
		t.Fatalf("got %d ResourceMetrics, want 3", len(rms))
	}
	for i, want := range []struct{ name, version string }{
		{"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp", "0.58.0"},
		{"synthkit", ""},
		{"", ""},
	} {
		sms := rms[i].GetScopeMetrics()
		if len(sms) != 1 {
			t.Fatalf("resource %d: %d ScopeMetrics, want exactly 1", i, len(sms))
		}
		sc := sms[0].GetScope()
		if sc.GetName() != want.name || sc.GetVersion() != want.version {
			t.Errorf("resource %d scope = %q/%q, want %q/%q", i, sc.GetName(), sc.GetVersion(), want.name, want.version)
		}
		if len(sc.GetAttributes()) != 0 {
			t.Errorf("resource %d: scope attributes emitted (%d) — synthkit sends none", i, len(sc.GetAttributes()))
		}
	}
}

// TestDryRunInventoryCoversExponentialHistogram keeps the -dump inventory honest for the new
// shape: the name must be recorded like any other, not dropped by an unhandled kind.
func TestDryRunInventoryCoversExponentialHistogram(t *testing.T) {
	s := NewMetrics("http://unused", "u", "t", true)
	err := s.Write(context.Background(), []MetricResource{{
		Attrs: map[string]any{"service.name": "svc"},
		Metrics: []Metric{{Name: "traces.span.metrics.duration", Kind: MetricExponentialHistogram,
			ExpHistograms: []ExponentialHistogramPoint{{Time: tNow, Count: 1, Sum: 1,
				Positive: ExponentialBuckets{BucketCounts: []uint64{1}}}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, names := s.Inventory()
	if got := names["svc"]; len(got) != 1 || got[0] != "traces.span.metrics.duration" {
		t.Errorf("inventory = %v", got)
	}
}
