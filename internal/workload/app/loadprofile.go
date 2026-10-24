// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
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
		if n.decl.ShapeProfile == nil {
			n.load = wl
			continue
		}
		lp, err := resolveLoadProfile(mergeProfiles(workload, n.decl.ShapeProfile))
		if err != nil {
			return fmt.Errorf("app service %q: %w", n.decl.Name, err)
		}
		n.load = lp
	}
	return nil
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
