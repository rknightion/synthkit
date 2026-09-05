// SPDX-License-Identifier: AGPL-3.0-only

package beylaagent

// native_otlp.go contains Beyla's internal-metrics OTLP catalogue. The five names below are the
// names observed on the OTLP wire from Beyla v3.32.0; they are deliberately explicit because the
// OTLP semantic names are not a mechanical spelling conversion of the Prometheus families. The
// reporter shape is reconciled against the version-pinned OBI source:
// https://github.com/grafana/opentelemetry-ebpf-instrumentation/blob/6ec4f13df658f5972355b87bbc637547b6e39fc3/pkg/export/otel/metrics_internal.go.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"runtime"
	"sort"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	metricBPFMapEntriesOTLP   = "beyla.bpf.map.entries_total"
	metricBPFMapMaxOTLP       = "beyla.bpf.map.max_entries_total"
	metricBPFProbeExecOTLP    = "beyla.bpf.probe.executions"
	metricBPFProbeLatencyOTLP = "beyla.bpf.probe.latency_seconds_total"
	metricBuildInfoOTLP       = "beyla.internal.build.info"

	nativeScopeName   = "obi_internal"
	nativeServiceName = "opentelemetry-ebpf-instrumentation"
	nativeSDKName     = "beyla"
	// v3.32.0's pinned OBI go.mod resolves the SDK dependency used by
	// attrs.VendorSDKVersion to v1.44.0.
	nativeSDKVersion  = "v1.44.0"
	nativeSDKLanguage = "go"
)

// renderOTLP records the exact five captured OTLP instruments in a state.State. The Prometheus
// state is deliberately separate: changing exporter mode must not change the established scrape
// family set or its cumulative values.
func (c *Construct) renderOTLP(bf float64, now time.Time, w *core.World) {
	if c.stOTLP == nil {
		return
	}
	if c.otlpStart.IsZero() {
		c.otlpStart = now
	}

	// Beyla's build-info gauge is an informational point. The native reporter uses OBI-prefixed
	// datapoint attributes, which are distinct from the Prometheus label keys.
	c.stOTLP.Set(metricBuildInfoOTLP, map[string]string{
		"obi.goarch":    runtime.GOARCH,
		"obi.goos":      runtime.GOOS,
		"obi.goversion": runtime.Version(),
		"obi.revision":  c.revision,
		"obi.version":   c.version,
	}, 1)

	// eBPF probe execution and map statistics use the same deterministic fixture magnitudes as the
	// existing /internal/metrics renderer. Only these five families cross the native OTLP boundary.
	probes := []struct{ id, name, typ string }{
		{"1", "kprobe_tcp_sendmsg", "kprobe"},
		{"2", "uprobe_http_handler", "uprobe"},
	}
	for _, p := range probes {
		attrs := map[string]string{
			"bpf.probe.id":   p.id,
			"bpf.probe.name": p.name,
			"bpf.probe.type": p.typ,
		}
		key := metricBPFProbeExecOTLP + "|" + p.name
		c.stOTLP.Add(metricBPFProbeExecOTLP, attrs, bf*5000*c.seriesVar(w, now, key, 0.18))
		c.stOTLP.Add(metricBPFProbeLatencyOTLP, attrs, bf*0.25*c.seriesVar(w, now, key+"_lat", 0.18))
	}

	bpfMaps := []struct{ id, name, typ string }{
		{"10", "http_requests", "BPF_MAP_TYPE_HASH"},
		{"11", "active_connections", "BPF_MAP_TYPE_LRU_HASH"},
	}
	for _, m := range bpfMaps {
		attrs := map[string]string{
			"bpf.map.id":   m.id,
			"bpf.map.name": m.name,
			"bpf.map.type": m.typ,
		}
		key := metricBPFMapEntriesOTLP + "|" + m.name
		c.stOTLP.Set(metricBPFMapEntriesOTLP, attrs, float64(defaultMapEntries)*bf*c.seriesVar(w, now, key, 0.18))
		c.stOTLP.Set(metricBPFMapMaxOTLP, attrs, defaultMapMax)
	}
}

// writeOTLPMetrics materializes the state snapshots in stable family and attribute order. The
// native sink expects semantic names and datapoint attributes, so no Prometheus suffix or name
// conversion is applied here.
func (c *Construct) writeOTLPMetrics(ctx context.Context, now time.Time, w *core.World) error {
	if c.stOTLP == nil || w == nil || w.OTLPMetrics == nil {
		return nil
	}
	if c.otlpStart.IsZero() {
		c.otlpStart = now
	}
	scalars := c.stOTLP.Collect(now)
	metrics := make([]otlp.Metric, 0, 5)
	for _, spec := range []struct {
		name        string
		description string
		unit        string
		kind        otlp.MetricKind
		promKind    promrw.Kind
		monotonic   bool
	}{
		{name: metricBPFMapEntriesOTLP, description: "Number of entries in the eBPF map", kind: otlp.MetricGauge, promKind: promrw.KindGauge},
		{name: metricBPFMapMaxOTLP, description: "Max number of entries in the eBPF map", kind: otlp.MetricGauge, promKind: promrw.KindGauge},
		{name: metricBPFProbeExecOTLP, description: "Total number of eBPF probe executions", unit: "{call}", kind: otlp.MetricSum, promKind: promrw.KindCounter, monotonic: true},
		{name: metricBPFProbeLatencyOTLP, description: "Total latency of the eBPF probe in seconds", unit: "s", kind: otlp.MetricSum, promKind: promrw.KindCounter, monotonic: true},
		{name: metricBuildInfoOTLP, description: "A metric with a constant '1' value labeled by version, revision, branch, goversion, goos and goarch during build.", kind: otlp.MetricGauge, promKind: promrw.KindGauge},
	} {
		points := nativeScalarPoints(scalars, spec.name, spec.promKind, now, c.otlpStart)
		if len(points) == 0 {
			continue
		}
		metric := otlp.Metric{
			Name: spec.name, Description: spec.description, Unit: spec.unit, Kind: spec.kind, Numbers: points,
		}
		if spec.kind == otlp.MetricSum {
			metric.Monotonic = spec.monotonic
			metric.Temporality = otlp.TemporalityCumulative
		}
		metrics = append(metrics, metric)
	}
	if len(metrics) == 0 {
		return nil
	}

	// The native reporter creates one internal meter and one resource block. Its UUID instance ID
	// is represented by a deterministic UUID-shaped value so repeated synthkit ticks remain stable.
	return w.OTLPMetrics.Write(ctx, []otlp.MetricResource{
		{
			Attrs:   c.nativeResourceAttrs(),
			Scope:   otlp.Scope{Name: nativeScopeName},
			Metrics: metrics,
		},
	})
}

func (c *Construct) nativeResourceAttrs() map[string]any {
	attrs := map[string]any{
		"service.name":             nativeServiceName,
		"service.instance.id":      c.nativeServiceInstanceID(),
		"telemetry.sdk.language":   nativeSDKLanguage,
		"telemetry.sdk.name":       nativeSDKName,
		"telemetry.sdk.version":    nativeSDKVersion,
		"telemetry.distro.name":    nativeServiceName,
		"telemetry.distro.version": c.version,
	}
	// NodeMeta.HostID is not represented by this construct's config. Do not map the
	// node/host name into host.id: that would claim a value the source contract does
	// not provide here.
	return attrs
}

func (c *Construct) nativeServiceInstanceID() string {
	identity := c.host
	if c.mode != "standalone" {
		identity = c.cluster + "\x00" + c.node
	}
	digest := sha256.Sum256([]byte("beyla.internal:" + identity))
	// Match the UUID version/variant bits used by uuid.New().String() while keeping
	// the value stable for one configured substrate identity.
	digest[6] = (digest[6] & 0x0f) | 0x50
	digest[8] = (digest[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", digest[0:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16])
}

func nativeScalarPoints(series []promrw.Series, name string, kind promrw.Kind, now, start time.Time) []otlp.NumberPoint {
	filtered := make([]promrw.Series, 0)
	for _, s := range series {
		if s.Name == name && s.Kind == kind && s.Native == nil {
			filtered = append(filtered, s)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return state.LabelSig(filtered[i].Labels) < state.LabelSig(filtered[j].Labels)
	})
	points := make([]otlp.NumberPoint, 0, len(filtered))
	for _, s := range filtered {
		attrs := make(map[string]any, len(s.Labels))
		for key, value := range s.Labels {
			attrs[key] = value
		}
		point := otlp.NumberPoint{Attrs: attrs, Time: now, Value: s.Value}
		if kind == promrw.KindCounter {
			point.Start = start
		}
		points = append(points, point)
	}
	return points
}
