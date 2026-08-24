// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"context"
	"testing"
	"time"

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
	schema := FromSinks(prom, nil, nil, nil, nil, nil)
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
	schema := FromSinks(prom, nil, nil, nil, nil, nil)
	if len(schema.Metrics) != 1 || schema.Metrics[0].Name != "native_business_sum" {
		t.Fatalf("metrics=%v", schema.Metrics)
	}
}
