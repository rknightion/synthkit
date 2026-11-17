// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// loadProfile is a resolved shape_profile: the parameterised curve plus its location (nil
// location reads the blueprint Engine's). Methods on a nil *loadProfile defer to the Engine.
type loadProfile struct {
	curve shape.Curve
	loc   *time.Location
}

func resolveLoadProfile(p *ShapeProfile) (*loadProfile, error) {
	if p == nil {
		return nil, nil
	}
	c := shape.DefaultCurve()
	for _, f := range []struct {
		src *float64
		dst *float64
	}{
		{p.Floor, &c.Floor},
		{p.RampUpStartHour, &c.RampUpStartHour},
		{p.PlateauStartHour, &c.PlateauStartHour},
		{p.PlateauEndHour, &c.PlateauEndHour},
		{p.RampDownEndHour, &c.RampDownEndHour},
	} {
		if f.src != nil {
			*f.dst = *f.src
		}
	}
	if p.WeekendFactor != nil {
		wf := *p.WeekendFactor
		c.WeekendFactor = &wf
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("app shape_profile: %w", err)
	}
	lp := &loadProfile{curve: c}
	if p.Timezone != "" {
		loc, err := time.LoadLocation(p.Timezone)
		if err != nil {
			return nil, fmt.Errorf("app shape_profile: timezone %q: %w", p.Timezone, err)
		}
		lp.loc = loc
	}
	return lp, nil
}

// mergeProfiles overlays a service's shape_profile on the workload's (nil when neither is set).
func mergeProfiles(base, over *ShapeProfile) *ShapeProfile {
	if over == nil {
		return base
	}
	out := ShapeProfile{}
	if base != nil {
		out = *base
	}
	if over.Timezone != "" {
		out.Timezone = over.Timezone
	}
	for _, f := range []struct {
		dst **float64
		src *float64
	}{
		{&out.Floor, over.Floor},
		{&out.RampUpStartHour, over.RampUpStartHour},
		{&out.PlateauStartHour, over.PlateauStartHour},
		{&out.PlateauEndHour, over.PlateauEndHour},
		{&out.RampDownEndHour, over.RampDownEndHour},
		{&out.WeekendFactor, over.WeekendFactor},
	} {
		if f.src != nil {
			*f.dst = f.src
		}
	}
	return &out
}

// resolveNodeLoads gives every node its effective load profile: its own shape_profile merged
// over the workload's, the workload's, or nil (the blueprint curve).
func resolveNodeLoads(g *graph, workload *ShapeProfile, wl *loadProfile) error {
	for _, n := range g.nodes {
		eff := mergeProfiles(workload, n.decl.ShapeProfile)
		if n.decl.ShapeProfile == nil {
			n.load = wl
		} else {
			lp, err := resolveLoadProfile(eff)
			if err != nil {
				return fmt.Errorf("app service %q: %w", n.decl.Name, err)
			}
			n.load = lp
		}
		if err := resolveSeriesLoads(n, eff); err != nil {
			return fmt.Errorf("app service %q: %w", n.decl.Name, err)
		}
	}
	return nil
}

// resolveSeriesLoads gives every series that declares its own clock its load profile: the node's
// effective profile (eff, nil for the blueprint curve), overlaid by the metric's shape_profile,
// overlaid by each chosen enum value's, so a timezone-only declaration keeps the node's curve
// and an undeclared node reads the Engine's default curve in the declared zone. Series that
// declare nothing keep nil and follow the node.
func resolveSeriesLoads(n *node, eff *ShapeProfile) error {
	for i, spec := range n.metrics {
		combos := scaledLabelCombos(spec.Labels)
		var loads []*loadProfile
		if spec.ShapeProfile != nil || hasComboClocks(combos) {
			metricProfile := mergeProfiles(eff, shapeProfileOf(spec.ShapeProfile))
			var metricLoad *loadProfile
			if spec.ShapeProfile != nil {
				lp, err := resolveLoadProfile(metricProfile)
				if err != nil {
					return fmt.Errorf("metric %q: %w", spec.Name, err)
				}
				metricLoad = lp
			}
			loads = make([]*loadProfile, len(combos))
			for ci, combo := range combos {
				if len(combo.clocks) == 0 {
					loads[ci] = metricLoad
					continue
				}
				p := metricProfile
				for _, c := range combo.clocks {
					p = mergeProfiles(p, shapeProfileOf(c))
				}
				lp, err := resolveLoadProfile(p)
				if err != nil {
					return fmt.Errorf("metric %q %v: %w", spec.Name, combo.labels, err)
				}
				loads[ci] = lp
			}
		}
		if loads == nil {
			continue
		}
		if n.seriesLoads == nil {
			n.seriesLoads = make([][]*loadProfile, len(n.metrics))
		}
		n.seriesLoads[i] = loads
	}
	return nil
}

func hasComboClocks(combos []labelCombo) bool {
	for _, c := range combos {
		if len(c.clocks) > 0 {
			return true
		}
	}
	return false
}

// shapeProfileOf reads a declared shape.Profile as the workload's ShapeProfile (the two share
// their fields, so the conversion fails to compile if they drift apart).
func shapeProfileOf(p *shape.Profile) *ShapeProfile {
	if p == nil {
		return nil
	}
	sp := ShapeProfile(*p)
	return &sp
}

// seriesShape re-reads the shape load for the label combinations of one metric that declare
// their own clock, and releases a batched counter's arrivals on its cadence. It carries the tick
// time, the incident amplification the node-level load already carries, the node's own load, the
// metric's pre-resolved per-combination profiles and, for a batch, the tick spacing and the
// incident mode to re-amplify past ticks with. A nil *seriesShape leaves every series on the node's load.
type seriesShape struct {
	now   time.Time
	step  time.Duration // the tick spacing a batch accumulates over (the metric lane's real cadence)
	eng   *shape.Engine
	amp   float64
	loads []*loadProfile
	// node is the node's effective load (nil reads the Engine's curve); a batch reads it for the
	// combinations that declare no clock of their own.
	node *loadProfile
	// batch, when set, replaces the smooth load with the release of the arrivals accumulated since
	// the previous cadence boundary (zero between releases).
	batch   *shape.Batch
	mode    string // incident mode amplifying the load (empty when none applies)
	service string
	// vols is the node's record of the master volume each past tick of a batch window ran under
	// (nil for an unbatched series).
	vols *volumeLog
}

// seriesShape returns the per-series load reader for node n's metric i, or nil when none of its
// series declares a clock or a batch (the common case, which then does no extra work).
func (w *Workload) seriesShape(now time.Time, step time.Duration, world *core.World, id nodeIdentity, n *node, i int, spec telemetryspec.MetricSpec) *seriesShape {
	var loads []*loadProfile
	if i < len(n.seriesLoads) {
		loads = n.seriesLoads[i]
	}
	var batch *shape.Batch
	if spec.Value.Kind() == telemetryspec.KindShape && spec.Value.Shape != nil {
		batch = spec.Value.Shape.Batch
	}
	if loads == nil && batch == nil {
		return nil
	}
	s := &seriesShape{now: now, step: step, eng: world.Shape, amp: 1.0, loads: loads, node: n.load, batch: batch, service: id.service}
	if batch != nil {
		n.vols.observe(now, world.Shape.VolumeMultiplier())
		s.vols = &n.vols
	}
	if spec.Value.Kind() == telemetryspec.KindShape && spec.Value.Shape != nil &&
		spec.Value.Shape.Mode != "" && spec.Value.Shape.IncidentValue == nil {
		s.mode = spec.Value.Shape.Mode
		s.amp = world.Shape.FailFactor(now, s.mode, id.service, incidentAmplification)
	}
	return s
}

// ctx returns base with the shape load of combination ci's own clock, or base unchanged when
// that combination follows the node and is not batched.
func (s *seriesShape) ctx(base telemetryspec.EvalCtx, ci int) telemetryspec.EvalCtx {
	if s == nil {
		return base
	}
	lp := s.node
	own := ci < len(s.loads) && s.loads[ci] != nil
	if own {
		lp = s.loads[ci]
	}
	if s.batch != nil {
		// Each accumulated tick reads the series' clock at its own time, under the scheduled
		// incident amplification then and the master volume it ran under; the release is exact
		// zero between boundaries. Only the tick being evaluated reads the live fault state, so
		// a fault switched on late in a window never rescales the ticks accumulated before it,
		// and a pause or volume change speaks for the ticks it covered, not the whole window.
		base.ShapeVal = s.batch.Release(s.now, s.step, func(t time.Time) float64 {
			if t.Equal(s.now) {
				amp := 1.0
				if s.mode != "" {
					amp = s.eng.FailFactor(t, s.mode, s.service, incidentAmplification)
				}
				return lp.business(t, s.eng) * amp
			}
			amp := 1.0
			if s.mode != "" {
				amp = scheduledFactor(s.eng, t, s.mode, s.service)
			}
			return lp.businessAt(t, s.eng, s.vols.at(t, s.eng.VolumeMultiplier())) * amp
		})
		base.ExactLoad = true
		return base
	}
	if !own {
		return base
	}
	base.ShapeVal = lp.business(s.now, s.eng) * s.amp
	base.ExactLoad = true
	return base
}

func (lp *loadProfile) local(now time.Time, eng *shape.Engine) time.Time {
	if lp.loc != nil {
		return now.In(lp.loc)
	}
	return now.In(eng.Loc())
}

// business is the substrate-style load (diurnal x business weekly x master volume), the
// shape_profile counterpart of Engine.BusinessFactor.
func (lp *loadProfile) business(now time.Time, eng *shape.Engine) float64 {
	if lp == nil {
		return eng.BusinessFactor(now)
	}
	t := lp.local(now, eng)
	return lp.curve.Diurnal(t) * lp.curve.BusinessWeekly(t) * eng.VolumeMultiplier()
}

// businessAt is business at an instant whose master volume was vol (the live knob read at a past
// tick is not that tick's): the same curve, scaled by vol instead of the Engine's current multiplier.
func (lp *loadProfile) businessAt(now time.Time, eng *shape.Engine, vol float64) float64 {
	if lp == nil {
		cur := eng.VolumeMultiplier()
		if !(cur > 0) {
			return 0
		}
		return eng.BusinessFactor(now) / cur * vol
	}
	t := lp.local(now, eng)
	return lp.curve.Diurnal(t) * lp.curve.BusinessWeekly(t) * vol
}

// scheduledFactor is Engine.FailFactor at the incident amplification over the scheduled incident
// windows alone: a pure function of the instant, for ticks the live fault state cannot speak for.
func scheduledFactor(eng *shape.Engine, at time.Time, mode, service string) float64 {
	active, intensity := eng.EvalScheduled(at, mode, service)
	if !active {
		return 1.0
	}
	return 1 + (incidentAmplification-1)*intensity
}

// volumeLog records the master volume a node's batched series ran under, as change points, so a
// batch release scales each accumulated tick by the volume of that tick: a paused tick (volume 0)
// reads 0 and adds nothing. Ticks before the first record read the first recorded volume, the
// volume the run started with.
type volumeLog struct {
	last time.Time
	pts  []volPoint // ascending by at
}

type volPoint struct {
	at   time.Time
	mult float64
}

// observe records the volume mult of the unpaused tick now. Observing an instant already seen is
// a no-op.
func (l *volumeLog) observe(now time.Time, mult float64) { l.record(now, mult) }

// paused records that the workload ticked at now under an explicit volume 0, when the batched
// series is not evaluated at all.
func (l *volumeLog) paused(now time.Time) { l.record(now, 0) }

func (l *volumeLog) record(now time.Time, mult float64) {
	if len(l.pts) == 0 {
		l.pts = append(l.pts, volPoint{at: now, mult: mult})
		l.last = now
		return
	}
	if !now.After(l.last) {
		return
	}
	if l.pts[len(l.pts)-1].mult != mult {
		l.pts = append(l.pts, volPoint{at: now, mult: mult})
	}
	l.last = now
	// A window reaches back at most shape.MaxBatchPeriod; keep the point in force at its start.
	cut := now.Add(-shape.MaxBatchPeriod - time.Hour)
	k := 0
	for k+1 < len(l.pts) && !l.pts[k+1].at.After(cut) {
		k++
	}
	if k > 0 {
		l.pts = append([]volPoint(nil), l.pts[k:]...)
	}
}

// notePaused records a paused tick for the node's batched series (spec), which the paused
// metric pass does not evaluate.
func (n *node) notePaused(now time.Time, spec telemetryspec.MetricSpec) {
	if spec.Value.Kind() == telemetryspec.KindShape && spec.Value.Shape != nil && spec.Value.Shape.Batch != nil {
		n.vols.paused(now)
	}
}

// at is the master volume in force at t; def is returned before anything was observed.
func (l *volumeLog) at(t time.Time, def float64) float64 {
	if l == nil || len(l.pts) == 0 {
		return def
	}
	for i := len(l.pts) - 1; i >= 0; i-- {
		if !l.pts[i].at.After(t) {
			return l.pts[i].mult
		}
	}
	return l.pts[0].mult
}

// factor is the environment-weighted load, the shape_profile counterpart of Engine.Factor.
func (lp *loadProfile) factor(now time.Time, eng *shape.Engine, weight float64, nonProd bool) float64 {
	if lp == nil {
		return eng.Factor(now, weight, nonProd)
	}
	t := lp.local(now, eng)
	return lp.curve.Diurnal(t) * lp.curve.Weekly(t, nonProd) * weight * eng.VolumeMultiplier()
}
