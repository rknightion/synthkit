// SPDX-License-Identifier: AGPL-3.0-only

// MetricsSink pushes synthetic NATIVE OTLP metrics to the Grafana Cloud OTLP/HTTP gateway as
// hand-encoded ResourceMetrics protobuf (no OTel SDK; same protowire-envelope technique as the
// traces Sink). Unlike promrw — which ships FINAL pre-mangled Prometheus names — this lane
// emits OTLP semantic names and lets the gateway own normalization (target_info, resource-
// attribute promotion, _total/unit suffixes, otel_scope_*). It is therefore a DOCUMENTED
// EXCEPTION to the pre-mangled-names rule, validated against live GC capture rather than the
// -dump name diff. Carries METRICS ONLY; traces go via Sink, all promrw metrics via promrw.
package otlp

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/rknightion/synthkit/internal/pushhook"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type MetricsSink struct {
	eg     egress
	dryRun bool

	Observe pushhook.Observer

	invMu       sync.Mutex
	invMetrics  map[string]map[string]struct{} // service.name → metric names
	invResAttrs map[string]map[string]struct{} // service.name → resource attr keys
}

// NewMetrics builds the OTLP metrics sink. endpoint is the base gateway URL; "/v1/metrics" is
// appended. Reuses the GC_OTLP_* credential triplet (same gateway as traces).
func NewMetrics(endpoint, user, token string, dryRun bool) *MetricsSink {
	return &MetricsSink{
		eg:     newEgress(endpoint, "/v1/metrics", user, token, "otlpmetrics"),
		dryRun: dryRun,
	}
}

func (s *MetricsSink) Write(ctx context.Context, resources []MetricResource) error {
	if len(resources) == 0 {
		return nil
	}
	rms := make([]*metricspb.ResourceMetrics, 0, len(resources))
	totalPoints := 0
	for _, r := range resources {
		scopeName, scopeVer := r.Scope.Name, r.Scope.Version
		if scopeName == "" {
			scopeName = "synthkit"
		}
		metrics := make([]*metricspb.Metric, 0, len(r.Metrics))
		for _, m := range r.Metrics {
			pm, n := convertMetric(m)
			if pm == nil {
				continue
			}
			totalPoints += n
			metrics = append(metrics, pm)
		}
		rms = append(rms, &metricspb.ResourceMetrics{
			Resource: &resourcepb.Resource{Attributes: kvs(r.Attrs)},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Scope:   &commonpb.InstrumentationScope{Name: scopeName, Version: scopeVer},
				Metrics: metrics,
			}},
		})
	}

	blueprint := ""
	if v, ok := resources[0].Attrs["blueprint"]; ok {
		blueprint, _ = v.(string)
	}

	if s.dryRun {
		s.record(resources)
		log.Printf("[dry-run otlpmetrics] %d resource(s), %d datapoint(s)", len(resources), totalPoints)
		if s.Observe != nil {
			s.Observe(ctx, pushhook.Event{Sink: "otlpmetrics", Blueprint: blueprint, Items: totalPoints, DryRun: true})
		}
		return nil
	}

	// Hand-encode ExportMetricsServiceRequest (field 1 = repeated ResourceMetrics, LEN).
	var buf []byte
	for _, rm := range rms {
		b, err := proto.Marshal(rm)
		if err != nil {
			return fmt.Errorf("otlp metrics: marshal ResourceMetrics: %w", err)
		}
		buf = protowire.AppendTag(buf, 1, protowire.BytesType)
		buf = protowire.AppendBytes(buf, b)
	}
	return s.eg.post(ctx, buf, totalPoints, blueprint, s.Observe)
}

// convertMetric maps an otlp.Metric to a metricspb.Metric and returns the data-point count.
func convertMetric(m Metric) (*metricspb.Metric, int) {
	out := &metricspb.Metric{Name: m.Name, Description: m.Description, Unit: m.Unit}
	switch m.Kind {
	case MetricGauge:
		dps := numberPoints(m.Numbers, false)
		out.Data = &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: dps}}
		return out, len(dps)
	case MetricSum:
		dps := numberPoints(m.Numbers, true)
		out.Data = &metricspb.Metric_Sum{Sum: &metricspb.Sum{
			DataPoints:             dps,
			IsMonotonic:            m.Monotonic,
			AggregationTemporality: temporality(m.Temporality),
		}}
		return out, len(dps)
	case MetricHistogram:
		dps := histoPoints(m.Histograms)
		out.Data = &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
			DataPoints:             dps,
			AggregationTemporality: temporality(m.Temporality),
		}}
		return out, len(dps)
	default:
		return nil, 0
	}
}

// numberPoints builds NumberDataPoints. withStart=false (gauges) omits StartTimeUnixNano.
func numberPoints(pts []NumberPoint, withStart bool) []*metricspb.NumberDataPoint {
	out := make([]*metricspb.NumberDataPoint, 0, len(pts))
	for _, p := range pts {
		dp := &metricspb.NumberDataPoint{
			Attributes:   kvs(p.Attrs),
			TimeUnixNano: uint64(p.Time.UnixNano()),
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: p.Value},
		}
		if withStart && !p.Start.IsZero() {
			dp.StartTimeUnixNano = uint64(p.Start.UnixNano())
		}
		out = append(out, dp)
	}
	return out
}

func histoPoints(pts []HistogramPoint) []*metricspb.HistogramDataPoint {
	out := make([]*metricspb.HistogramDataPoint, 0, len(pts))
	for _, p := range pts {
		sum := p.Sum
		dp := &metricspb.HistogramDataPoint{
			Attributes:     kvs(p.Attrs),
			TimeUnixNano:   uint64(p.Time.UnixNano()),
			Count:          p.Count,
			Sum:            &sum,
			BucketCounts:   p.BucketCounts,
			ExplicitBounds: p.Bounds,
		}
		if !p.Start.IsZero() {
			dp.StartTimeUnixNano = uint64(p.Start.UnixNano())
		}
		if p.HasMinMax {
			mn, mx := p.Min, p.Max
			dp.Min, dp.Max = &mn, &mx
		}
		out = append(out, dp)
	}
	return out
}

func temporality(t Temporality) metricspb.AggregationTemporality {
	if t == TemporalityDelta {
		return metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA
	}
	return metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE
}

// record accumulates the dry-run inventory keyed by service.name (informational — the native
// lane is exempt from the -dump name gate; final names are gateway-assigned).
func (s *MetricsSink) record(resources []MetricResource) {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	if s.invMetrics == nil {
		s.invMetrics = map[string]map[string]struct{}{}
		s.invResAttrs = map[string]map[string]struct{}{}
	}
	add := func(m map[string]map[string]struct{}, svc, key string) {
		set := m[svc]
		if set == nil {
			set = map[string]struct{}{}
			m[svc] = set
		}
		set[key] = struct{}{}
	}
	for _, r := range resources {
		svc := ""
		if v, ok := r.Attrs["service.name"]; ok {
			svc = fmt.Sprint(v)
		}
		for k := range r.Attrs {
			add(s.invResAttrs, svc, k)
		}
		for _, m := range r.Metrics {
			add(s.invMetrics, svc, m.Name)
		}
	}
}

// Inventory returns the captured dry-run inventory per service.name.
func (s *MetricsSink) Inventory() (resAttrs, metricNames map[string][]string) {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	return sortOTLPInv(s.invResAttrs), sortOTLPInv(s.invMetrics)
}
