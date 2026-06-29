// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"testing"

	"github.com/rknightion/synthkit/internal/sink/otlp"
	pyroscope "github.com/rknightion/synthkit/internal/sink/pyroscope"
)

// captureProfileSink implements core.PyroscopeWriter and captures the last Write call.
type captureProfileSink struct {
	last []pyroscope.Series
}

func (c *captureProfileSink) Write(_ context.Context, series []pyroscope.Series) error {
	c.last = series
	return nil
}

// hasProfLabel reports whether any series in the batch has a label with the given key and value.
func hasProfLabel(series []pyroscope.Series, key, value string) bool {
	for _, s := range series {
		for _, lp := range s.Labels {
			if lp.Name == key && lp.Value == value {
				return true
			}
		}
	}
	return false
}

// hasProfLabelKey reports whether any series in the batch has a label with the given key
// (regardless of value).
func hasProfLabelKey(series []pyroscope.Series, key string) bool {
	for _, s := range series {
		for _, lp := range s.Labels {
			if lp.Name == key {
				return true
			}
		}
	}
	return false
}

func TestStampedProfiles(t *testing.T) {
	// ScopeBlueprint: label must be stamped.
	cap := &captureProfileSink{}
	input := []pyroscope.Series{{Labels: []pyroscope.LabelPair{{Name: "service_name", Value: "x"}}}}
	if err := (&stampedProfiles{sink: cap, label: "acme"}).Write(context.Background(), input); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if !hasProfLabel(cap.last, BlueprintLabel, "acme") {
		t.Fatal("blueprint must be stamped for ScopeBlueprint")
	}

	// Verify clone-before-stamp: original input labels slice must not be mutated.
	if len(input[0].Labels) != 1 {
		t.Fatalf("original labels slice was mutated (len=%d, want 1)", len(input[0].Labels))
	}

	// ScopeSubstrate (label=""): blueprint label must NOT be stamped.
	cap.last = nil
	if err := (&stampedProfiles{sink: cap, label: ""}).Write(context.Background(), input); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if hasProfLabelKey(cap.last, BlueprintLabel) {
		t.Fatal("substrate must NOT stamp blueprint label")
	}
}

func TestStampedOTLPMetricsStampsBlueprintAttr(t *testing.T) {
	var got []otlp.MetricResource
	fake := otlpMetricWriterFunc(func(ctx context.Context, rs []otlp.MetricResource) error { got = rs; return nil })
	w := &stampedOTLPMetrics{sink: fake, label: "acme"}
	in := []otlp.MetricResource{{Attrs: map[string]any{"service.name": "svc"}}}
	if err := w.Write(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if got[0].Attrs["blueprint"] != "acme" {
		t.Errorf("blueprint attr = %v, want acme", got[0].Attrs["blueprint"])
	}
	if _, ok := in[0].Attrs["blueprint"]; ok {
		t.Error("source resource was mutated (must clone before stamp)")
	}
}

func TestStampedOTLPMetricsSubstrateNoStamp(t *testing.T) {
	var got []otlp.MetricResource
	fake := otlpMetricWriterFunc(func(ctx context.Context, rs []otlp.MetricResource) error { got = rs; return nil })
	w := &stampedOTLPMetrics{sink: fake, label: ""}
	_ = w.Write(context.Background(), []otlp.MetricResource{{Attrs: map[string]any{"service.name": "svc"}}})
	if _, ok := got[0].Attrs["blueprint"]; ok {
		t.Error("substrate writer must not stamp blueprint")
	}
}

func TestShardMetricResourceStableBySvc(t *testing.T) {
	a := otlp.MetricResource{Attrs: map[string]any{"service.name": "svc", "service.namespace": "ns", "blueprint": "bp"}}
	b := otlp.MetricResource{Attrs: map[string]any{"service.name": "svc", "service.namespace": "ns", "blueprint": "bp"}}
	c := otlp.MetricResource{Attrs: map[string]any{"service.name": "other", "service.namespace": "ns", "blueprint": "bp"}}
	if shardMetricResource(a) != shardMetricResource(b) {
		t.Error("same identity must shard identically")
	}
	if shardMetricResource(a) == shardMetricResource(c) {
		t.Error("different service must (almost always) shard differently")
	}
}

// otlpMetricWriterFunc adapts a func to core.OTLPMetricWriter for tests.
type otlpMetricWriterFunc func(context.Context, []otlp.MetricResource) error

func (f otlpMetricWriterFunc) Write(ctx context.Context, rs []otlp.MetricResource) error {
	return f(ctx, rs)
}
