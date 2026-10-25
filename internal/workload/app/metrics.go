// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/state"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// Tick is the metric lane: for each node it evaluates the node's DSL metrics into cumulative state
// (gauge=Set, counter=Add-delta, histogram=Observe), stamped with the node identity, then flushes
// to the metrics sink. A `shape`-driven value reads the node's incident state so the value responds
// per node (the INC1 metric lane). spanmetrics/service-graph stay DERIVED unless the per-blueprint
// EmitSpanMetrics opt-in is set (F4b).
func (w *Workload) Tick(ctx context.Context, now time.Time, world *core.World) error {
	if w.pipeline != nil && (world == nil || world.EmitSpanMetrics) {
		return pipelineError("local synthkit spanmetrics unsupported for agent_pipeline")
	}
	nativeEnabled := w.cfg.otelMetricsEnabled() && world.OTLPMetrics != nil
	if world.Metrics == nil && !nativeEnabled {
		w.tickProfiles(ctx, now, world)
		return nil
	}
	// An explicit master volume of 0 is a pause (SKT-0147): spec metrics follow
	// observePausedMetricStates instead of evaluating shape values, whose evaluator treats a zero
	// load factor as full load.
	paused := world.Shape.Paused()
	pausedCtx := telemetryspec.EvalCtx{Rand: world.Shape.Float64, Norm: world.Shape.NormFloat64, Now: now}
	dt := interval.Seconds()
	if !w.lastMetricTick.IsZero() && now.After(w.lastMetricTick) {
		dt = now.Sub(w.lastMetricTick).Seconds()
	}
	w.lastMetricTick = now
	for _, n := range w.graph.nodes {
		id := w.identity(n)
		var incs map[string]float64
		derived := n.hasDerived()
		if derived {
			incs = make(map[string]float64)
		}
		if !n.decl.metricsEnabled() {
			// Consume this node's value draws in declaration order so disabling one producer does
			// not shift the generated samples of enabled services later in the same tick.
			for _, spec := range n.metrics {
				w.observeMetricStates(nil, nil, id, spec, w.metricCtx(now, world, id, n, spec), incs, nil)
			}
			if derived {
				for _, spec := range n.metrics {
					if isDerived(spec) {
						w.observeDerived(nil, nil, id, n, spec, w.metricCtx(now, world, id, n, spec), incs, dt, paused)
					}
				}
			}
			continue
		}
		nativeFor := func(i int) *state.State {
			if w.cfg.otelMetricsEnabled() && world.OTLPMetrics != nil && i >= n.nativeMetricStart {
				return w.nativeState(n)
			}
			return nil
		}
		for i, spec := range n.metrics {
			native := nativeFor(i)
			if paused {
				w.observePausedMetricStates(w.st, native, id, spec, pausedCtx)
				continue
			}
			metricCtx := w.metricCtx(now, world, id, n, spec)
			w.observeMetricStates(w.st, native, id, spec, metricCtx, incs, w.seriesShape(now, world, id, n, i, spec))
		}
		// Derived specs read this tick's counter increments, so they run after the node's
		// ordinary specs; they consume draws only when declared (count_of observations).
		if derived {
			for i, spec := range n.metrics {
				if !isDerived(spec) {
					continue
				}
				ctx := pausedCtx
				if !paused {
					ctx = w.metricCtx(now, world, id, n, spec)
				}
				w.observeDerived(w.st, nativeFor(i), id, n, spec, ctx, incs, dt, paused)
			}
		}
	}
	if world.EmitSpanMetrics {
		w.tickSpanMetrics(now, world)
	}
	w.tickTargetInfo()
	if world.Metrics != nil {
		if err := world.Metrics.Write(ctx, w.st.Collect(now)); err != nil {
			return err
		}
	}
	if w.cfg.otelMetricsEnabled() && world.OTLPMetrics != nil {
		if err := w.tickOTLPMetrics(ctx, now, world); err != nil {
			return err
		}
	}
	w.tickProfiles(ctx, now, world)
	return nil
}

// metricCtx builds the eval context for one metric: a `shape` value reads the node's load
// (BusinessFactor) amplified by any active incident on the value's declared mode, scoped to the
// node (per-node incident responsiveness, §6.5). With incident_value, load and scoped intensity
// stay separate: the DSL interpolates the declared endpoints instead of multiplying by FailFactor.
// Non-shape values ignore these inputs.
func (w *Workload) metricCtx(now time.Time, world *core.World, id nodeIdentity, n *node, spec telemetryspec.MetricSpec) telemetryspec.EvalCtx {
	shapeVal := n.load.business(now, world.Shape)
	var intensity float64
	if spec.Value.Kind() == telemetryspec.KindShape && spec.Value.Shape != nil && spec.Value.Shape.Mode != "" {
		if spec.Value.Shape.IncidentValue != nil {
			_, intensity = world.Shape.Eval(now, spec.Value.Shape.Mode, id.service)
		} else {
			shapeVal *= world.Shape.FailFactor(now, spec.Value.Shape.Mode, id.service, incidentAmplification)
		}
	}
	return telemetryspec.EvalCtx{
		ShapeVal: shapeVal,
		// A shape_profile can reach an exact zero load (floor 0, weekend_factor 0); that must
		// read 0, like a paused shape gauge. The blueprint curve never reaches 0 (its pause
		// takes the paused path), so the legacy zero-means-1.0 fallback is kept without one.
		ExactLoad:         n.load != nil,
		IncidentIntensity: intensity,
		Rand:              world.Shape.Float64,
		Norm:              world.Shape.NormFloat64,
		Now:               now, // the `now` value model reads the tick time, never the wall clock
	}
}

// incidentAmplification is the magnitude a shape-driven value reaches at full incident intensity
// (×N at intensity 1.0) — e.g. a fallback rate triples under fallback_storm.
const incidentAmplification = 3.0
