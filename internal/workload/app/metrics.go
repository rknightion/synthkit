// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// Tick is the metric lane: for each node it evaluates the node's DSL metrics into cumulative state
// (gauge=Set, counter=Add-delta, histogram=Observe), stamped with the node identity, then flushes
// to the metrics sink. A `shape`-driven value reads the node's incident state so the value responds
// per node (the INC1 metric lane). spanmetrics/service-graph stay DERIVED unless the per-blueprint
// EmitSpanMetrics opt-in is set (F4b).
func (w *Workload) Tick(ctx context.Context, now time.Time, world *core.World) error {
	if world.Metrics == nil {
		return nil
	}
	for _, n := range w.graph.nodes {
		id := w.identity(n)
		for _, spec := range n.metrics {
			w.observeMetric(w.st, id, spec, w.metricCtx(now, world, id, spec))
		}
	}
	if world.EmitSpanMetrics {
		w.tickSpanMetrics(now, world)
	}
	if err := world.Metrics.Write(ctx, w.st.Collect(now)); err != nil {
		return err
	}
	w.tickProfiles(ctx, now, world)
	return nil
}

// metricCtx builds the eval context for one metric: a `shape` value reads the node's load
// (BusinessFactor) amplified by any active incident on the value's declared mode, scoped to the
// node (per-node incident responsiveness, §6.5). Non-shape values ignore ShapeVal.
func (w *Workload) metricCtx(now time.Time, world *core.World, id nodeIdentity, spec telemetryspec.MetricSpec) telemetryspec.EvalCtx {
	shapeVal := world.Shape.BusinessFactor(now)
	if spec.Value.Kind() == telemetryspec.KindShape && spec.Value.Shape != nil && spec.Value.Shape.Mode != "" {
		shapeVal *= world.Shape.FailFactor(now, spec.Value.Shape.Mode, id.service, incidentAmplification)
	}
	return telemetryspec.EvalCtx{
		ShapeVal: shapeVal,
		Rand:     world.Shape.Float64,
		Norm:     world.Shape.NormFloat64,
	}
}

// incidentAmplification is the magnitude a shape-driven value reaches at full incident intensity
// (×N at intensity 1.0) — e.g. a fallback rate triples under fallback_storm.
const incidentAmplification = 3.0
