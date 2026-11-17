// SPDX-License-Identifier: AGPL-3.0-only

package shape

import (
	"fmt"
	"math"
	"time"
)

// Curve is a parameterised business-hours plateau plus a weekday/weekend factor. It is the
// opt-in, per-consumer alternative to the Engine's built-in curve: a consumer that declares its
// own load profile evaluates a Curve in its own local time and keeps using the blueprint Engine
// for the volume knob, pause, incidents and randomness. A Curve holds no state, no RNG and no
// location, so it never clones the Engine.
//
// The diurnal shape is: Floor before RampUpStartHour, a smoothstep ramp to 1.0 at
// PlateauStartHour, a flat 1.0 top until PlateauEndHour, a smoothstep ramp back to Floor at
// RampDownEndHour, and Floor after. Hours are local wall-clock hours in [0,24].
type Curve struct {
	Floor            float64
	RampUpStartHour  float64
	PlateauStartHour float64
	PlateauEndHour   float64
	RampDownEndHour  float64
	// WeekendFactor, when non-nil, multiplies Saturday and Sunday for every reader. Nil keeps
	// the Engine's defaults: 0.3 for BusinessWeekly, 0.2 (prod) or 0.02 (non-prod) for Weekly.
	WeekendFactor *float64
}

// DefaultCurve returns the Engine's built-in plateau parameters (floor 0.1, ramp up 06-10,
// plateau 10-16, ramp down 16-20) with the Engine's default weekend factors.
func DefaultCurve() Curve {
	return Curve{Floor: 0.1, RampUpStartHour: 6, PlateauStartHour: 10, PlateauEndHour: 16, RampDownEndHour: 20}
}

// Validate rejects a curve whose hours are out of order or out of [0,24], whose floor is
// outside [0,1], or whose weekend factor is negative or not finite.
func (c Curve) Validate() error {
	vals := []float64{c.Floor, c.RampUpStartHour, c.PlateauStartHour, c.PlateauEndHour, c.RampDownEndHour}
	for _, v := range vals {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("shape curve: values must be finite")
		}
	}
	if c.Floor < 0 || c.Floor > 1 {
		return fmt.Errorf("shape curve: floor %v out of [0,1]", c.Floor)
	}
	if c.RampUpStartHour < 0 || c.RampDownEndHour > 24 ||
		c.RampUpStartHour > c.PlateauStartHour || c.PlateauStartHour > c.PlateauEndHour ||
		c.PlateauEndHour > c.RampDownEndHour {
		return fmt.Errorf("shape curve: hours must satisfy 0 <= ramp_up_start <= plateau_start <= plateau_end <= ramp_down_end <= 24")
	}
	if c.WeekendFactor != nil {
		if w := *c.WeekendFactor; math.IsNaN(w) || math.IsInf(w, 0) || w < 0 {
			return fmt.Errorf("shape curve: weekend_factor must be finite and >= 0")
		}
	}
	return nil
}

// Diurnal returns the plateau multiplier in [Floor, 1.0] for an already-localised time.
// DefaultCurve().Diurnal equals the Engine's built-in curve exactly.
func (c Curve) Diurnal(local time.Time) float64 {
	const peak = 1.0
	h := float64(local.Hour()) + float64(local.Minute())/60.0 + float64(local.Second())/3600.0
	var v float64
	switch {
	case h < c.RampUpStartHour:
		v = c.Floor
	case h < c.PlateauStartHour:
		frac := (h - c.RampUpStartHour) / (c.PlateauStartHour - c.RampUpStartHour)
		v = c.Floor + (peak-c.Floor)*smoothstep(frac)
	case h < c.PlateauEndHour:
		v = peak
	case h < c.RampDownEndHour:
		frac := (h - c.PlateauEndHour) / (c.RampDownEndHour - c.PlateauEndHour)
		v = peak - (peak-c.Floor)*smoothstep(frac)
	default:
		v = c.Floor
	}
	return math.Max(v, c.Floor)
}

func isWeekend(local time.Time) bool {
	switch local.Weekday() {
	case time.Saturday, time.Sunday:
		return true
	}
	return false
}

// BusinessWeekly is the substrate (no environment) weekday/weekend factor: 1 on weekdays,
// WeekendFactor (default 0.3, as Engine.BusinessFactor) on weekends.
func (c Curve) BusinessWeekly(local time.Time) float64 {
	if !isWeekend(local) {
		return 1.0
	}
	if c.WeekendFactor != nil {
		return *c.WeekendFactor
	}
	return 0.3
}

// Weekly is the environment weekday/weekend factor: 1 on weekdays, WeekendFactor on weekends,
// or the Engine's default 0.2 (prod) / 0.02 (non-prod) when WeekendFactor is nil.
func (c Curve) Weekly(local time.Time, nonProd bool) float64 {
	if c.WeekendFactor != nil {
		if isWeekend(local) {
			return *c.WeekendFactor
		}
		return 1.0
	}
	return weeklyAt(local, nonProd)
}
