// SPDX-License-Identifier: AGPL-3.0-only

package envoygateway

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// nativeMetricSpec is the machine-readable native OTLP contract for one family. Attrs and
// Bounds are copied from the frozen capture record; they are not derived from the scrape
// surface. An empty Attrs slice means the captured family had no datapoint attributes.
type nativeMetricSpec struct {
	Name      string
	Kind      otlp.MetricKind
	Unit      string
	Monotonic bool
	Attrs     []nativeAttrSpec
	Bounds    []float64
}

type nativeAttrSpec struct {
	Key    string
	Values []string
}

// nativeOTLPState is intentionally separate from the scrape state. Native emission therefore
// cannot perturb the established Prometheus lane's draw order, counters, or histograms.
type nativeOTLPState struct {
	start      time.Time
	sums       map[string]float64
	histograms map[string]*nativeHistogramState
}

type nativeHistogramState struct {
	Count        uint64
	Sum          float64
	BucketCounts []uint64
}

func newNativeOTLPState() *nativeOTLPState {
	return &nativeOTLPState{
		sums:       make(map[string]float64),
		histograms: make(map[string]*nativeHistogramState),
	}
}

func (s *nativeOTLPState) begin(now time.Time) time.Time {
	if s.start.IsZero() {
		s.start = now
	}
	return s.start
}

func (s *nativeOTLPState) add(key string, delta float64) float64 {
	s.sums[key] += delta
	return s.sums[key]
}

func (s *nativeOTLPState) observe(key string, bounds []float64, value float64) nativeHistogramState {
	h := s.histograms[key]
	if h == nil || len(h.BucketCounts) != len(bounds)+1 {
		h = &nativeHistogramState{BucketCounts: make([]uint64, len(bounds)+1)}
		s.histograms[key] = h
	}
	h.Count++
	h.Sum += value
	bucket := len(bounds)
	for i, bound := range bounds {
		if value <= bound {
			bucket = i
			break
		}
	}
	h.BucketCounts[bucket]++
	return nativeHistogramState{
		Count:        h.Count,
		Sum:          h.Sum,
		BucketCounts: append([]uint64(nil), h.BucketCounts...),
	}
}

var nativeControlResourceAttrs = map[string]any{
	"service.name":           "unknown_service:envoy-gateway",
	"telemetry.sdk.language": "go",
	"telemetry.sdk.name":     "opentelemetry",
	"telemetry.sdk.version":  "1.45.0",
}

const nativeControlResourceSchemaURL = "https://opentelemetry.io/schemas/1.43.0"

var nativeDataResourceAttrs = map[string]any{
	"telemetry.sdk.language": "cpp",
	"telemetry.sdk.name":     "envoy",
	"telemetry.sdk.version":  "<build-id-elided>/1.39.0/Clean/RELEASE/BoringSSL",
}

// tickOTLPMetrics emits only the selected native OTLP surface(s). A proxy sink produces one
// resource containing the 206 captured data-plane families; a gateway sink produces one resource
// containing the 16 captured control-plane families. The two gates are additive to their
// respective Prometheus switches and never alter scrape-shaped output.
func (c *Construct) tickOTLPMetrics(ctx context.Context, now time.Time, factor float64, w *core.World) error {
	if w == nil || w.OTLPMetrics == nil || (!c.proxyOTLPSink && !c.gatewayOTLPSink) {
		return nil
	}
	if c.native == nil {
		c.native = newNativeOTLPState()
	}
	start := c.native.begin(now)
	resources := make([]otlp.MetricResource, 0, 2)
	if c.gatewayOTLPSink {
		resources = append(resources, c.nativeMetricResource(
			now, start, factor, "control", nativeControlResourceAttrs, otlp.Scope{Name: "envoy-gateway"}, false,
			nativeControlResourceSchemaURL, nativeGatewayMetrics,
		))
	}
	if c.proxyOTLPSink {
		resources = append(resources, c.nativeMetricResource(
			now, start, factor, "data", nativeDataResourceAttrs, otlp.Scope{}, true,
			"", nativeDataPlaneMetrics,
		))
	}
	return w.OTLPMetrics.Write(ctx, resources)
}

func (c *Construct) nativeMetricResource(
	now, start time.Time,
	factor float64,
	plane string,
	attrs map[string]any,
	scope otlp.Scope,
	preserveEmptyScope bool,
	resourceSchemaURL string,
	specs []nativeMetricSpec,
) otlp.MetricResource {
	resourceAttrs := make(map[string]any, len(attrs))
	for key, value := range attrs {
		resourceAttrs[key] = value
	}
	metrics := make([]otlp.Metric, 0, len(specs))
	for _, spec := range specs {
		if !nativeMetricSpecComplete(spec) {
			continue
		}
		metrics = append(metrics, c.nativeMetric(now, start, factor, plane, spec))
	}
	return otlp.MetricResource{
		Attrs:              resourceAttrs,
		Scope:              scope,
		ResourceSchemaURL:  resourceSchemaURL,
		PreserveEmptyScope: preserveEmptyScope,
		Metrics:            metrics,
	}
}

func nativeMetricSpecComplete(spec nativeMetricSpec) bool {
	if spec.Name == "" {
		return false
	}
	if spec.Kind != otlp.MetricGauge && spec.Kind != otlp.MetricSum && spec.Kind != otlp.MetricHistogram {
		return false
	}
	if spec.Kind == otlp.MetricHistogram && len(spec.Bounds) == 0 {
		return false
	}
	for _, attr := range spec.Attrs {
		if attr.Key == "" || len(attr.Values) == 0 {
			return false
		}
	}
	return true
}

func (c *Construct) nativeMetric(now, start time.Time, factor float64, plane string, spec nativeMetricSpec) otlp.Metric {
	points := nativeAttributeCombinations(spec.Attrs)
	if len(points) == 0 {
		return otlp.Metric{Name: spec.Name, Kind: spec.Kind, Unit: spec.Unit, Monotonic: spec.Monotonic, Histograms: nil}
	}

	metric := otlp.Metric{
		Name:        spec.Name,
		Unit:        spec.Unit,
		Kind:        spec.Kind,
		Monotonic:   spec.Monotonic,
		Temporality: otlp.TemporalityCumulative,
	}
	switch spec.Kind {
	case otlp.MetricGauge:
		metric.Numbers = make([]otlp.NumberPoint, 0, len(points))
		for _, attrs := range points {
			metric.Numbers = append(metric.Numbers, otlp.NumberPoint{
				Attrs: attrs,
				Time:  now,
				Value: nativeGaugeValue(spec.Name, factor, now.Sub(start).Seconds()),
			})
		}
	case otlp.MetricSum:
		metric.Numbers = make([]otlp.NumberPoint, 0, len(points))
		delta := nativeSumDelta(spec.Name, factor)
		for _, attrs := range points {
			key := nativeStateKey(plane, spec.Name, attrs)
			metric.Numbers = append(metric.Numbers, otlp.NumberPoint{
				Attrs: attrs,
				Start: start,
				Time:  now,
				Value: c.native.add(key, delta),
			})
		}
	case otlp.MetricHistogram:
		metric.Histograms = make([]otlp.HistogramPoint, 0, len(points))
		observation := nativeHistogramObservation(plane, spec.Name, factor)
		for _, attrs := range points {
			key := nativeStateKey(plane, spec.Name, attrs)
			h := c.native.observe(key, spec.Bounds, observation)
			metric.Histograms = append(metric.Histograms, otlp.HistogramPoint{
				Attrs:        attrs,
				Start:        start,
				Time:         now,
				Count:        h.Count,
				Sum:          h.Sum,
				Bounds:       append([]float64(nil), spec.Bounds...),
				BucketCounts: h.BucketCounts,
			})
		}
	}
	return metric
}

// nativeAttributeCombinations returns one datapoint attribute map for every Cartesian product
// of the exact value sets retained by the capture. This makes a value set omission observable in
// dry-run inventory and keeps each family joined to its own captured keys.
func nativeAttributeCombinations(specs []nativeAttrSpec) []map[string]any {
	combinations := []map[string]any{{}}
	for _, spec := range specs {
		if len(spec.Values) == 0 {
			return nil
		}
		next := make([]map[string]any, 0, len(combinations)*len(spec.Values))
		for _, attrs := range combinations {
			for _, value := range spec.Values {
				copyAttrs := make(map[string]any, len(attrs)+1)
				for key, existing := range attrs {
					copyAttrs[key] = existing
				}
				copyAttrs[spec.Key] = value
				next = append(next, copyAttrs)
			}
		}
		combinations = next
	}
	return combinations
}

func nativeStateKey(plane, name string, attrs map[string]any) string {
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(plane)
	b.WriteByte(0)
	b.WriteString(name)
	for _, key := range keys {
		b.WriteByte(0)
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(nativeStringValue(attrs[key]))
	}
	return b.String()
}

func nativeStringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func nativeSumDelta(name string, factor float64) float64 {
	if factor <= 0 {
		factor = 0.1
	}
	// One nominal event per 60-second construct cadence, scaled by the existing shape factor.
	// Error/reset/watchdog families are rarer event counters, so they receive a small but positive
	// fraction of that cadence rather than a fabricated high-volume request rate.
	if strings.Contains(name, "failure") || strings.Contains(name, "reset") ||
		strings.Contains(name, "miss") || strings.Contains(name, "unknown") ||
		strings.Contains(name, "no_certificate") || strings.Contains(name, "not_found") {
		return factor * 0.05
	}
	return factor
}

func nativeGaugeValue(name string, factor, elapsedSeconds float64) float64 {
	// Exact values mirror the established Prometheus lane's small gateway fixture. The
	// broader captured surface remains at its healthy idle value unless it is an active
	// resource gauge, which follows the same bounded shape factor.
	switch name {
	case "control_plane.connected_state", "server.live":
		return 1
	case "cluster.membership_healthy", "cluster.membership_total":
		return 2
	case "cluster.max_host_weight":
		return 1
	case "server.concurrency":
		return 4
	case "server.days_until_first_cert_expiring":
		return 89
	case "server.memory_allocated":
		return 32 * 1024 * 1024
	case "server.memory_heap_size":
		return 64 * 1024 * 1024
	case "server.memory_physical_size":
		return 128 * 1024 * 1024
	case "server.uptime":
		return max(elapsedSeconds, 0)
	case "listener_manager.total_listeners_active":
		return 1
	case "listener_manager.workers_started":
		return 4
	}
	if strings.HasSuffix(name, "_active") || strings.HasSuffix(name, ".active") ||
		strings.Contains(name, "clusters_inflated") {
		return max(factor, 0.1)
	}
	return 0
}

func nativeHistogramObservation(plane, name string, factor float64) float64 {
	if factor <= 0 {
		factor = 0.1
	}
	if plane == "control" {
		if name == "xds_stream_duration_seconds" {
			return 100 * (0.8 + 0.2*factor)
		}
		return 0.05 * (0.8 + 0.2*factor)
	}
	return 10 * (0.8 + 0.2*factor)
}
