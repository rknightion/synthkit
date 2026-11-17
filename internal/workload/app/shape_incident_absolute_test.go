// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/shape"
)

const incidentAbsoluteDoc = `
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - name: ref_duration_seconds
        source: test fixture
        instrument: gauge
        value: {shape: {base: 100, mode: latency_storm, incident_value: 4600%s}}
`

// TestShapeIncidentAbsoluteIgnoresLoadCurve: at a weekday night and a weekend night the business
// curve is far below 1. The default incident_value endpoint is scaled down with it; with
// incident_absolute the incident share holds its full level while the calm baseline still follows
// the curve.
func TestShapeIncidentAbsoluteIgnoresLoadCurve(t *testing.T) {
	read := func(t *testing.T, flag string, at time.Time, incident bool) (float64, float64) {
		t.Helper()
		w := buildApp(t, decodeStrict(t, sprintfDoc(flag)))
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		var intensity float64
		if incident {
			world.Shape = shape.New("UTC", []string{"latency_storm@" + at.Format(time.RFC3339) + "/1h@api"})
			_, intensity = world.Shape.Eval(at, "latency_storm", "api")
		}
		if err := w.Tick(context.Background(), at, world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		s := mc.Find("ref_duration_seconds")
		if len(s) != 1 {
			t.Fatalf("series %d, want 1", len(s))
		}
		return s[0].Value, intensity
	}
	nights := map[string]time.Time{
		"weekday night": time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC),
		"weekend night": time.Date(2026, 6, 13, 3, 0, 0, 0, time.UTC),
	}
	for name, at := range nights {
		t.Run(name, func(t *testing.T) {
			calm, _ := read(t, "", at, false)
			load := calm / 100
			if load >= 0.5 || load <= 0 {
				t.Fatalf("precondition: night load %v should be well below 1", load)
			}
			// Default: the whole incident reading is scaled by load.
			got, i := read(t, "", at, true)
			if i <= 0 {
				t.Fatalf("precondition: incident intensity %v must be active", i)
			}
			if want := ((1-i)*100 + i*4600) * load; math.Abs(got-want) > 1e-9*want {
				t.Fatalf("default: got %v want %v", got, want)
			}
			if got >= 4600*0.9 {
				t.Fatalf("default incident level %v should shrink with night load", got)
			}
			// Opt-in: the incident share is absolute, the baseline share still follows load.
			gotAbs, _ := read(t, ", incident_absolute: true", at, true)
			wantAbs := (1-i)*100*load + i*4600
			if math.Abs(gotAbs-wantAbs) > 1e-9*wantAbs {
				t.Fatalf("incident_absolute: got %v want %v", gotAbs, wantAbs)
			}
			if gotAbs <= got {
				t.Fatalf("incident_absolute %v must exceed the load-scaled %v at night", gotAbs, got)
			}
			// No incident: the flag changes nothing.
			calmAbs, _ := read(t, ", incident_absolute: true", at, false)
			if calmAbs != calm {
				t.Fatalf("calm output changed by the flag: %v vs %v", calmAbs, calm)
			}
		})
	}
}

func sprintfDoc(flag string) string { return fmt.Sprintf(incidentAbsoluteDoc, flag) }

// TestShapeIncidentAbsoluteExactZeroLoadReadsZero: a shape_profile that reaches an exact zero
// load emits 0 during an incident too, rather than the absolute incident level.
func TestShapeIncidentAbsoluteExactZeroLoadReadsZero(t *testing.T) {
	doc := `
shape_profile: {timezone: America/New_York, weekend_factor: 0, floor: 0}
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: api
    type: web
    entry: true
    custom_metrics:
      - name: ref_duration_seconds
        source: test fixture
        instrument: gauge
        value: {shape: {base: 100, mode: latency_storm, incident_value: 4600, incident_absolute: true}}
`
	w := buildApp(t, decodeStrict(t, doc))
	at := time.Date(2026, 6, 13, 17, 0, 0, 0, time.UTC) // Saturday midday New York, weekend_factor 0
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Shape = shape.New("UTC", []string{"latency_storm@" + at.Format(time.RFC3339) + "/1h@api"})
	if err := w.Tick(context.Background(), at, world); err != nil {
		t.Fatal(err)
	}
	if s := mc.Find("ref_duration_seconds"); len(s) != 1 || s[0].Value != 0 {
		t.Fatalf("exact zero load mid-incident = %v, want one series at 0", s)
	}
}
