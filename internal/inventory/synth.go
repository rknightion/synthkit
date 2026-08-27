// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/sink/pyroscope"
	sigilsink "github.com/rknightion/synthkit/internal/sink/sigil"
)

// FromSinks projects one dry-run tick's captured sink payloads into the canonical schema.
func FromSinks(prom *promrw.Sink, lokiSink *loki.Sink, traceSink *otlp.Sink, metricsSink *otlp.MetricsSink, otlpLogsSink *otlp.LogsSink, profileSink *pyroscope.Sink, sigilSink *sigilsink.Sink) Schema {
	out := New()
	if prom != nil {
		for _, series := range prom.Captured() {
			addPromSeries(&out, series)
		}
	}
	if metricsSink != nil {
		for _, resource := range metricsSink.Captured() {
			addOTLPMetricResource(&out, resource)
		}
	}
	if lokiSink != nil {
		for _, stream := range lokiSink.Captured() {
			metadata := map[string]struct{}{}
			for _, line := range stream.Lines {
				for key := range line.Meta {
					metadata[key] = struct{}{}
				}
			}
			keys := make([]string, 0, len(metadata))
			for key := range metadata {
				keys = append(keys, key)
			}
			out.AddLog(stream.Labels["source"], TransportLoki, stream.Labels, keys)
		}
	}
	if otlpLogsSink != nil {
		for _, resource := range otlpLogsSink.Captured() {
			attrs := stringify(resource.Attrs)
			metadata := map[string]struct{}{}
			for _, record := range resource.Records {
				for key := range record.Attrs {
					metadata[key] = struct{}{}
				}
			}
			keys := make([]string, 0, len(metadata))
			for key := range metadata {
				keys = append(keys, key)
			}
			// Resource attributes are the destination's promotion candidates (they become stream
			// labels); record attributes always land as structured metadata.
			out.AddLog(attrs["source"], TransportOTLPLogs, attrs, keys)
		}
	}
	if traceSink != nil {
		for _, resource := range traceSink.Captured() {
			attrs := stringify(resource.Attrs)
			service := attrs["service.name"]
			for _, span := range resource.Spans {
				keys := make([]string, 0, len(span.Attrs))
				for key := range span.Attrs {
					keys = append(keys, key)
				}
				out.AddTrace(service, attrs, span.Name, keys)
			}
		}
	}
	if profileSink != nil {
		for _, series := range profileSink.Captured() {
			labels := make(map[string]string, len(series.Labels))
			profileType := "unknown"
			for _, pair := range series.Labels {
				labels[pair.Name] = pair.Value
				if pair.Name == "__profile_type__" {
					profileType = pair.Value
				} else if pair.Name == "__name__" && profileType == "unknown" {
					profileType = pair.Value
				}
			}
			out.AddProfile(profileType, labels)
		}
	}
	if sigilSink != nil {
		inv := sigilSink.Inventory()
		if inv.Generations > 0 {
			out.AddSigil("generations", inv.OperationNames...)
		}
		if inv.WorkflowSteps > 0 {
			out.AddSigil("workflow_steps")
		}
		if inv.Scores > 0 {
			out.AddSigil("scores")
		}
	}
	out.Normalize()
	return out
}

func addPromSeries(out *Schema, series promrw.Series) {
	name := series.Name
	instrument := InstrumentGauge
	switch series.Kind {
	case promrw.KindCounter:
		instrument = InstrumentCounter
	case promrw.KindHistogram:
		instrument = InstrumentHistogram
		if series.Native == nil {
			switch {
			case strings.HasSuffix(name, "_bucket"):
				name = strings.TrimSuffix(name, "_bucket")
			case strings.HasSuffix(name, "_sum"):
				name = strings.TrimSuffix(name, "_sum")
			case strings.HasSuffix(name, "_count"):
				name = strings.TrimSuffix(name, "_count")
			}
		}
	}
	var histogram *Histogram
	if series.Kind == promrw.KindHistogram {
		histogram = &Histogram{Classic: series.Native == nil, Native: series.Native != nil}
		if le, ok := series.Labels["le"]; ok && le != "+Inf" {
			if bound, err := strconv.ParseFloat(le, 64); err == nil {
				histogram.BucketBounds = []float64{bound}
			}
		}
		if series.Native != nil {
			histogram.NativeSchemas = []int32{series.Native.Schema}
		}
	}
	out.AddMetric(name, TransportPrometheusRW2, instrument, series.Labels, histogram)
}

func addOTLPMetricResource(out *Schema, resource otlp.MetricResource) {
	resourceAttrs := stringify(resource.Attrs)
	for _, metric := range resource.Metrics {
		instrument := InstrumentGauge
		switch metric.Kind {
		case otlp.MetricSum:
			if metric.Monotonic {
				instrument = InstrumentCounter
			}
		case otlp.MetricHistogram, otlp.MetricExponentialHistogram:
			instrument = InstrumentHistogram
		}
		for _, point := range metric.Numbers {
			attrs := mergeStrings(resourceAttrs, stringify(point.Attrs))
			out.AddMetric(metric.Name, TransportOTLPMetrics, instrument, attrs, nil)
		}
		for _, point := range metric.Histograms {
			attrs := mergeStrings(resourceAttrs, stringify(point.Attrs))
			out.AddMetric(metric.Name, TransportOTLPMetrics, instrument, attrs,
				&Histogram{Classic: true, BucketBounds: point.Bounds})
		}
		for _, point := range metric.ExpHistograms {
			attrs := mergeStrings(resourceAttrs, stringify(point.Attrs))
			// Exponential histograms carry no explicit bounds; the scale IS the bucket schema,
			// and it matches the Prometheus native-histogram schema numerically. Mirrors how the
			// e2e receiver records the same shape, so the two sides of the diff agree.
			out.AddMetric(metric.Name, TransportOTLPMetrics, instrument, attrs,
				&Histogram{Native: true, NativeSchemas: []int32{point.Scale}})
		}
		if len(metric.Numbers) == 0 && len(metric.Histograms) == 0 && len(metric.ExpHistograms) == 0 {
			out.AddMetric(metric.Name, TransportOTLPMetrics, instrument, resourceAttrs, nil)
		}
	}
}

func stringify(in map[string]any) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = fmt.Sprint(value)
	}
	return out
}

func mergeStrings(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for key, value := range a {
		out[key] = value
	}
	for key, value := range b {
		out[key] = value
	}
	return out
}
