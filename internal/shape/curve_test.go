// SPDX-License-Identifier: AGPL-3.0-only

package shape

import (
	"testing"
	"time"
)

// TestDefaultCurveEqualsEngine: the exported parameterised curve with default parameters is
// bit-identical to the Engine's built-in curve for every minute of a week, in both readers.
func TestDefaultCurveEqualsEngine(t *testing.T) {
	eng := New("Europe/Zurich", nil)
	c := DefaultCurve()
	start := time.Date(2026, 6, 8, 0, 0, 30, 0, time.UTC)
	for m := 0; m < 7*24*60; m++ {
		now := start.Add(time.Duration(m) * time.Minute)
		local := now.In(eng.Loc())
		if got, want := c.Diurnal(local)*c.BusinessWeekly(local)*eng.VolumeMultiplier(), eng.BusinessFactor(now); got != want {
			t.Fatalf("%v business: curve %v engine %v", now, got, want)
		}
		for _, np := range []bool{false, true} {
			if got, want := c.Diurnal(local)*c.Weekly(local, np)*0.7*eng.VolumeMultiplier(), eng.Factor(now, 0.7, np); got != want {
				t.Fatalf("%v factor nonProd=%v: curve %v engine %v", now, np, got, want)
			}
		}
	}
}

// TestCurveParameters: custom hours move the plateau and a weekend factor overrides both readers.
func TestCurveParameters(t *testing.T) {
	wf := 0.6
	c := Curve{Floor: 0.2, RampUpStartHour: 8, PlateauStartHour: 9, PlateauEndHour: 22, RampDownEndHour: 23, WeekendFactor: &wf}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	at := func(d, h int) time.Time { return time.Date(2026, 6, d, h, 0, 0, 0, time.UTC) }
	if got := c.Diurnal(at(10, 7)); got != 0.2 {
		t.Fatalf("07:00 = %v, want floor 0.2", got)
	}
	if got := c.Diurnal(at(10, 21)); got != 1 {
		t.Fatalf("21:00 = %v, want plateau 1", got)
	}
	if got := c.BusinessWeekly(at(13, 12)); got != 0.6 {
		t.Fatalf("Saturday business weekly = %v, want 0.6", got)
	}
	if got := c.Weekly(at(14, 12), true); got != 0.6 {
		t.Fatalf("Sunday non-prod weekly = %v, want 0.6", got)
	}
	if got := c.Weekly(at(10, 12), true); got != 1 {
		t.Fatalf("weekday weekly = %v, want 1", got)
	}
	for _, bad := range []Curve{
		{Floor: 0.1, RampUpStartHour: 10, PlateauStartHour: 9, PlateauEndHour: 16, RampDownEndHour: 20},
		{Floor: 1.5, RampUpStartHour: 6, PlateauStartHour: 10, PlateauEndHour: 16, RampDownEndHour: 20},
		{Floor: 0.1, RampUpStartHour: 6, PlateauStartHour: 10, PlateauEndHour: 16, RampDownEndHour: 25},
	} {
		if bad.Validate() == nil {
			t.Fatalf("curve %+v accepted", bad)
		}
	}
}
