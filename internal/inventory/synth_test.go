// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

func TestFromSinksMergesClassicAndNativeHistogramFamily(t *testing.T) {
	prom := promrw.New("", "", "", true, nil)
	prom.Capture = true
	now := time.Unix(1, 0)
	if err := prom.Write(context.Background(), []promrw.Series{
		{Name: "request_duration_seconds_bucket", Labels: map[string]string{"job": "api", "le": "0.5"}, Kind: promrw.KindHistogram, T: now},
		{Name: "request_duration_seconds_sum", Labels: map[string]string{"job": "api"}, Kind: promrw.KindHistogram, T: now},
		{Name: "request_duration_seconds_count", Labels: map[string]string{"job": "api"}, Kind: promrw.KindHistogram, T: now},
		{Name: "request_duration_seconds", Labels: map[string]string{"job": "api"}, Kind: promrw.KindHistogram, Native: &promrw.NativeHistogram{Schema: 3}, T: now},
	}); err != nil {
		t.Fatal(err)
	}
	schema := FromSinks(prom, nil, nil, nil, nil, nil, nil)
	if len(schema.Metrics) != 1 {
		t.Fatalf("metrics=%v, want one logical family", schema.Metrics)
	}
	h := schema.Metrics[0].Histogram
	if h == nil || !h.Classic || !h.Native {
		t.Fatalf("histogram=%+v, want classic+native", h)
	}
	if len(h.BucketBounds) != 1 || h.BucketBounds[0] != 0.5 {
		t.Fatalf("bounds=%v", h.BucketBounds)
	}
	if len(h.NativeSchemas) != 1 || h.NativeSchemas[0] != 3 {
		t.Fatalf("native schemas=%v", h.NativeSchemas)
	}
}

func TestFromSinksPreservesNativeHistogramNameWithClassicSuffixText(t *testing.T) {
	prom := promrw.New("", "", "", true, nil)
	prom.Capture = true
	if err := prom.Write(context.Background(), []promrw.Series{{
		Name: "native_business_sum", Kind: promrw.KindHistogram,
		Native: &promrw.NativeHistogram{Schema: 3}, T: time.Unix(1, 0),
	}}); err != nil {
		t.Fatal(err)
	}
	schema := FromSinks(prom, nil, nil, nil, nil, nil, nil)
	if len(schema.Metrics) != 1 || schema.Metrics[0].Name != "native_business_sum" {
		t.Fatalf("metrics=%v", schema.Metrics)
	}
}

// TestFromSinksProjectsExponentialHistogram pins the projection of the OTLP exponential-histogram
// shape. Before this, addOTLPMetricResource had no case for the kind, so such a metric fell through
// to the gauge branch and was recorded with resource attributes only — losing every datapoint
// attribute. The e2e receiver already records it as a native histogram, so a mismatch here would
// fail the correlation check on instrument type the moment any lane emits the shape.
func TestFromSinksProjectsExponentialHistogram(t *testing.T) {
	sink := otlp.NewMetrics("", "", "", true)
	sink.Capture = true
	if err := sink.Write(context.Background(), []otlp.MetricResource{{
		Attrs: map[string]any{"service.name": "svc"},
		Metrics: []otlp.Metric{{
			Name: "http.server.request.duration",
			Kind: otlp.MetricExponentialHistogram,
			ExpHistograms: []otlp.ExponentialHistogramPoint{{
				Attrs:    map[string]any{"http.request.method": "GET"},
				Count:    3,
				Sum:      0.5,
				Scale:    3,
				Positive: otlp.ExponentialBuckets{Offset: 1, BucketCounts: []uint64{1, 2}},
			}},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	schema := FromSinks(nil, nil, nil, sink, nil, nil, nil)
	if len(schema.Metrics) != 1 {
		t.Fatalf("metrics=%v, want exactly one family", schema.Metrics)
	}
	m := schema.Metrics[0]
	if got := m.InstrumentTypes; len(got) != 1 || got[0] != InstrumentHistogram {
		t.Fatalf("instrument_types=%v, want [%s]", got, InstrumentHistogram)
	}
	if m.Histogram == nil || !m.Histogram.Native {
		t.Fatalf("histogram=%+v, want a native histogram", m.Histogram)
	}
	if got := m.Histogram.NativeSchemas; len(got) != 1 || got[0] != 3 {
		t.Fatalf("native_schemas=%v, want [3]", got)
	}
	// The datapoint attribute is the part the gauge fall-through used to drop.
	var keys []string
	for _, l := range m.Labels {
		keys = append(keys, l.Key)
	}
	if !slices.Contains(keys, "http.request.method") {
		t.Fatalf("labels=%v, want the datapoint attribute http.request.method preserved", keys)
	}
}
