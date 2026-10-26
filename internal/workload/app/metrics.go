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
	// from opens this tick's metric window (from, now] for resource counters and outcomes.
	from := now.Add(-time.Duration(dt * float64(time.Second)))
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
				w.observeMetricStates(nil, nil, id, spec, w.metricCtx(now, from, world, id, n, spec), incs, nil)
			}
			if derived {
				for _, spec := range n.metrics {
					if isDerived(spec) {
						w.observeDerived(nil, nil, id, n, spec, w.metricCtx(now, from, world, id, n, spec), incs, dt, paused, world)
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
				ctx := pausedCtx
				w.stallProviders(&ctx, now, world, id, n, spec)
				w.observePausedMetricStates(w.st, native, id, spec, ctx)
				continue
			}
			metricCtx := w.metricCtx(now, from, world, id, n, spec)
			w.observeMetricStates(w.st, native, id, spec, metricCtx, incs, w.seriesShape(now, time.Duration(dt*float64(time.Second)), world, id, n, i, spec))
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
					ctx = w.metricCtx(now, from, world, id, n, spec)
				}
				w.observeDerived(w.st, nativeFor(i), id, n, spec, ctx, incs, dt, paused, world)
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
func (w *Workload) metricCtx(now, from time.Time, world *core.World, id nodeIdentity, n *node, spec telemetryspec.MetricSpec) telemetryspec.EvalCtx {
	shapeVal := n.load.business(now, world.Shape)
	var intensity float64
	if spec.Value.Kind() == telemetryspec.KindShape && spec.Value.Shape != nil && spec.Value.Shape.Mode != "" {
		if spec.Value.Shape.IncidentValue != nil {
			_, intensity = world.Shape.Eval(now, spec.Value.Shape.Mode, id.service)
		} else {
			shapeVal *= world.Shape.FailFactor(now, spec.Value.Shape.Mode, id.service, incidentAmplification)
		}
	}
	ctx := telemetryspec.EvalCtx{
		ShapeVal: shapeVal,
		// A shape_profile can reach an exact zero load (floor 0, weekend_factor 0); that must
		// read 0, like a paused shape gauge. The blueprint curve never reaches 0 (its pause
		// takes the paused path), so the legacy zero-means-1.0 fallback is kept without one.
		ExactLoad:         n.load != nil,
		IncidentIntensity: intensity,
		Rand:              world.Shape.Float64,
		Norm:              world.Shape.NormFloat64,
		Now:               now, // the tick time, never the wall clock; stallProviders holds it per series
	}
	w.stallProviders(&ctx, now, world, id, n, spec)
	w.causalProviders(&ctx, spec, n, from, now, world)
	return ctx
}

// incidentAmplification is the magnitude a shape-driven value reaches at full incident intensity
// (×N at intensity 1.0) — e.g. a fallback rate triples under fallback_storm.
const incidentAmplification = 3.0

// heartbeatNow is the time one series of a `now` gauge reads this tick: the tick time, unless the
// series is stalled, or the gauge is arrivals_only and the node had none this tick, when it is the
// last reading taken while it was neither. The series stalls on its own stall mode (an enum label
// value's stall_mode, else the metric's), resolved per service like a shape value's.
//
// The hold is workload state keyed by node, metric, series labels and stall mode, so two series
// with different stall modes hold independently. It advances only on ticks the series is not held,
// so it is deterministic and never reads the wall clock. A hold that is already due on the first
// evaluation has no earlier reading, so that tick reads the tick time and becomes the held value.
// Every other value model gets the tick time unchanged. The hold is not persisted: after a
// process restart inside an incident the gauge re-holds from the first tick it sees. n is the
// node, for the arrivals read; nil counts as arrivals.
func (w *Workload) heartbeatNow(now time.Time, world *core.World, id nodeIdentity, n *node, spec telemetryspec.MetricSpec, labels map[string]string) time.Time {
	if spec.Value.Kind() != telemetryspec.KindNow {
		return now
	}
	mode := spec.SeriesStallMode(labels)
	arrivalsOnly := spec.Value.Now.ArrivalsOnly
	if mode == "" && !arrivalsOnly {
		return now
	}
	key := id.service + "\x00" + incKey(spec.Name, labels) + "\x00" + mode
	held := stallActive(now, world, id, mode) || (arrivalsOnly && !w.hadArrivals(now, world, n))
	if held {
		if at, ok := w.nowHold[key]; ok {
			return at
		}
	}
	if w.nowHold == nil {
		w.nowHold = make(map[string]time.Time)
	}
	w.nowHold[key] = now
	return now
}

// stallActive reports whether the incident mode is active for the service at intensity above 0.
// An active window at intensity 0 is a no-op everywhere else (a shape value reads baseline), so it
// does not stall a producer either.
func stallActive(now time.Time, world *core.World, id nodeIdentity, mode string) bool {
	if mode == "" {
		return false
	}
	active, intensity := world.Shape.Eval(now, mode, id.service)
	return active && intensity > 0
}

// hadArrivals reports whether the node's source had arrivals this tick: its load factor (its own
// shape_profile clock, else the blueprint curve, times the master volume) is above 0. A paused
// workload has none.
func (w *Workload) hadArrivals(now time.Time, world *core.World, n *node) bool {
	if n == nil {
		return true
	}
	return !world.Shape.Paused() && n.load.business(now, world.Shape) > 0
}

// stallProviders wires the stall state a `now` gauge or a `source_read` counter reads for each of
// its series (see heartbeatNow); every other value is unchanged.
func (w *Workload) stallProviders(ctx *telemetryspec.EvalCtx, now time.Time, world *core.World, id nodeIdentity, n *node, spec telemetryspec.MetricSpec) {
	switch spec.Value.Kind() {
	case telemetryspec.KindNow:
		if spec.SeriesStallMode(nil) == "" && !spec.HasSeriesStall() && !spec.Value.Now.ArrivalsOnly {
			return
		}
		ctx.NowFor = func(labels map[string]string) time.Time {
			return w.heartbeatNow(now, world, id, n, spec, labels)
		}
	case telemetryspec.KindSourceRead:
		ctx.Stalled = func(labels map[string]string) bool {
			return stallActive(now, world, id, spec.SeriesStallMode(labels))
		}
	}
}
