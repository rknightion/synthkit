// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"sort"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// nativeState returns the state owned by one app graph node for its inline OTLP instruments.
// Keeping this store separate from st is important: the promrw state carries the node identity
// labels (and profile families), while the OTLP state carries only DSL datapoint attributes.
func (w *Workload) nativeState(n *node) *state.State {
	if w.otlpStates == nil {
		w.otlpStates = make(map[string]*state.State)
	}
	if st := w.otlpStates[n.decl.Name]; st != nil {
		return st
	}
	st := state.NewState()
	w.otlpStates[n.decl.Name] = st
	return st
}

// tickOTLPMetrics emits the app's native OTLP metric resources. Catalog profile families are
// deliberately absent: only the inline blueprint DSL declarations are SDK instruments for this
// lane. Resources follow graph declaration order and metrics follow inline declaration order.
func (w *Workload) tickOTLPMetrics(ctx context.Context, now time.Time, world *core.World) error {
	if world == nil || !w.cfg.otelMetricsEnabled() || world.OTLPMetrics == nil {
		return nil
	}
	if w.otlpColdStart.IsZero() {
		w.otlpColdStart = now
	}

	resources := make([]otlp.MetricResource, 0, len(w.graph.nodes))
	for _, n := range w.graph.nodes {
		st := w.otlpStates[n.decl.Name]
		if st == nil {
			continue
		}
		metrics := nativeNodeMetrics(n, st, now, w.otlpColdStart)
		if len(metrics) == 0 {
			continue
		}
		resources = append(resources, otlp.MetricResource{
			Attrs:   w.identity(n).resourceAttrs(),
			Metrics: metrics,
		})
	}
	if len(resources) == 0 {
		return nil
	}
	return world.OTLPMetrics.Write(ctx, resources)
}

// nativeNodeMetrics materializes one node's inline specs from the state snapshots. Collect uses
// maps internally, so sort datapoints by their stable label signature before returning them;
// this keeps captures deterministic while retaining the declaration order of metric families.
func nativeNodeMetrics(n *node, st *state.State, now, start time.Time) []otlp.Metric {
	if st == nil || n.nativeMetricStart >= len(n.metrics) {
		return nil
	}

	scalars := st.Collect(now)
	histos := st.CollectHistos()
	metrics := make([]otlp.Metric, 0, len(n.metrics)-n.nativeMetricStart)
	for _, spec := range n.metrics[n.nativeMetricStart:] {
		switch spec.Instrument {
		case telemetryspec.InstrumentGauge:
			points := nativeScalarPoints(scalars, spec.Name, promrw.KindGauge, now, time.Time{})
			if len(points) == 0 {
				continue
			}
			metrics = append(metrics, otlp.Metric{
				Name: spec.Name, Unit: spec.Unit, Kind: otlp.MetricGauge, Numbers: points,
			})
		case telemetryspec.InstrumentCounter:
			points := nativeScalarPoints(scalars, spec.Name, promrw.KindCounter, now, start)
			if len(points) == 0 {
				continue
			}
			metrics = append(metrics, otlp.Metric{
				Name: spec.Name, Unit: spec.Unit, Kind: otlp.MetricSum, Monotonic: true,
				Temporality: otlp.TemporalityCumulative, Numbers: points,
			})
		case telemetryspec.InstrumentHistogram:
			points := nativeHistogramPoints(histos, spec.Name, now, start)
			if len(points) == 0 {
				continue
			}
			metrics = append(metrics, otlp.Metric{
				Name: spec.Name, Unit: spec.Unit, Kind: otlp.MetricHistogram,
				Temporality: otlp.TemporalityCumulative, Histograms: points,
			})
		}
	}
	return metrics
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
		points = append(points, otlp.NumberPoint{
			Attrs: labelsToAny(s.Labels), Start: start, Time: now, Value: s.Value,
		})
	}
	return points
}

func nativeHistogramPoints(points []state.HistoPoint, name string, now, start time.Time) []otlp.HistogramPoint {
	filtered := make([]state.HistoPoint, 0)
	for _, p := range points {
		if p.Name == name {
			filtered = append(filtered, p)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return state.LabelSig(filtered[i].Labels) < state.LabelSig(filtered[j].Labels)
	})
	out := make([]otlp.HistogramPoint, 0, len(filtered))
	for _, p := range filtered {
		out = append(out, otlp.HistogramPoint{
			Attrs:        labelsToAny(p.Labels),
			Start:        start,
			Time:         now,
			Count:        p.Count,
			Sum:          p.Sum,
			Bounds:       append([]float64(nil), p.Bounds...),
			BucketCounts: append([]uint64(nil), p.BucketCounts...),
		})
	}
	return out
}

func labelsToAny(labels map[string]string) map[string]any {
	attrs := make(map[string]any, len(labels))
	for k, v := range labels {
		attrs[k] = v
	}
	return attrs
}
