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
// their own clock. It carries the tick time, the incident amplification the node-level load
// already carries, and the metric's pre-resolved per-combination profiles. A nil *seriesShape
// leaves every series on the node's load.
type seriesShape struct {
	now   time.Time
	eng   *shape.Engine
	amp   float64
	loads []*loadProfile
}

// seriesShape returns the per-series load reader for node n's metric i, or nil when none of its
// series declares a clock (the common case, which then does no extra work).
func (w *Workload) seriesShape(now time.Time, world *core.World, id nodeIdentity, n *node, i int, spec telemetryspec.MetricSpec) *seriesShape {
	if i >= len(n.seriesLoads) || n.seriesLoads[i] == nil {
		return nil
	}
	amp := 1.0
	if spec.Value.Kind() == telemetryspec.KindShape && spec.Value.Shape != nil &&
		spec.Value.Shape.Mode != "" && spec.Value.Shape.IncidentValue == nil {
		amp = world.Shape.FailFactor(now, spec.Value.Shape.Mode, id.service, incidentAmplification)
	}
	return &seriesShape{now: now, eng: world.Shape, amp: amp, loads: n.seriesLoads[i]}
}

// ctx returns base with the shape load of combination ci's own clock, or base unchanged when
// that combination follows the node.
func (s *seriesShape) ctx(base telemetryspec.EvalCtx, ci int) telemetryspec.EvalCtx {
	if s == nil || ci >= len(s.loads) || s.loads[ci] == nil {
		return base
	}
	base.ShapeVal = s.loads[ci].business(s.now, s.eng) * s.amp
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

// factor is the environment-weighted load, the shape_profile counterpart of Engine.Factor.
func (lp *loadProfile) factor(now time.Time, eng *shape.Engine, weight float64, nonProd bool) float64 {
	if lp == nil {
		return eng.Factor(now, weight, nonProd)
	}
	t := lp.local(now, eng)
	return lp.curve.Diurnal(t) * lp.curve.Weekly(t, nonProd) * weight * eng.VolumeMultiplier()
}
