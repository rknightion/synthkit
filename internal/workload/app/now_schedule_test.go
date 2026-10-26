// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

func scheduleSpec(name, mode string, period, phase time.Duration) telemetryspec.MetricSpec {
	return telemetryspec.MetricSpec{
		Name: name, Source: "t", Instrument: telemetryspec.InstrumentGauge,
		Value: telemetryspec.ValueModel{Now: &telemetryspec.NowModel{StallMode: mode, Period: period, Phase: phase}},
	}
}

// boundary is the epoch-aligned schedule boundary at or before at, in Unix seconds.
func boundary(at time.Time, period, phase time.Duration) int64 {
	p, ph := int64(period/time.Second), int64(phase/time.Second)
	u := at.Unix() - ph
	return (u - ((u%p)+p)%p) + ph
}

// TestHeartbeatNowScheduleHoldsAcrossStall: a 60 s tick series with a 15 minute schedule reads the
// last 15 minute boundary; a stall holds the last pre-stall boundary, a boundary that falls inside
// the stall is a missed run (it does not advance the reading even after the incident ends), and
// the first boundary after recovery resumes it.
func TestHeartbeatNowScheduleHoldsAcrossStall(t *testing.T) {
	w := buildApp(t, decodeStrict(t, nowStallDoc2))
	id := nodeIdentity{service: "collector"}
	spec := scheduleSpec("last_success", "stall", 15*time.Minute, 0)
	t0 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	tick := func(i int) time.Time { return t0.Add(time.Duration(i) * time.Minute) }
	// Stall 13:07 to 13:21 (ticks 7..20): it spans the 13:15 boundary, so that run is missed and
	// the reading stays 13:00 until the 13:30 boundary.
	incidents := []string{"stall@" + tick(7).Format(time.RFC3339) + "/14m@collector"}
	world := coretest.World(&coretest.MetricCapture{}, nil, nil)
	world.Shape = shape.New("UTC", incidents)
	for i := range 50 {
		got := w.heartbeatNow(tick(i), world, id, nil, spec, nil).Unix()
		want := boundary(tick(i), 15*time.Minute, 0)
		switch {
		case i >= 7 && i < 30:
			want = t0.Unix() // held at the last pre-stall boundary, through the missed 13:15 run
		}
		if got != want {
			t.Fatalf("tick %d (%s) = %s, want %s", i, tick(i).Format("15:04"),
				time.Unix(got, 0).UTC().Format("15:04"), time.Unix(want, 0).UTC().Format("15:04"))
		}
	}
}

// TestHeartbeatNowScheduleWithoutStallIsStepwise: with no stall mode the series needs no hold and
// reads the last boundary every tick.
func TestHeartbeatNowScheduleWithoutStallIsStepwise(t *testing.T) {
	w := buildApp(t, decodeStrict(t, nowStallDoc2))
	id := nodeIdentity{service: "collector"}
	spec := scheduleSpec("last_success", "", 15*time.Minute, 4*time.Minute)
	t0 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	world := coretest.World(&coretest.MetricCapture{}, nil, nil)
	world.Shape = shape.New("UTC", nil)
	for i := range 40 {
		at := t0.Add(time.Duration(i) * time.Minute)
		if got, want := w.heartbeatNow(at, world, id, nil, spec, nil).Unix(), boundary(at, 15*time.Minute, 4*time.Minute); got != want {
			t.Fatalf("tick %d = %d, want %d", i, got, want)
		}
	}
}

// TestHeartbeatNowSchedulesAreIndependent: two series with different schedules and stall modes
// hold and resume on their own.
func TestHeartbeatNowSchedulesAreIndependent(t *testing.T) {
	w := buildApp(t, decodeStrict(t, nowStallDoc2))
	id := nodeIdentity{service: "collector"}
	fast := scheduleSpec("job_fast", "stall_fast", 5*time.Minute, 0)
	slow := scheduleSpec("job_slow", "stall_slow", 30*time.Minute, 10*time.Minute)
	t0 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	tick := func(i int) time.Time { return t0.Add(time.Duration(i) * time.Minute) }
	incidents := []string{"stall_fast@" + tick(11).Format(time.RFC3339) + "/6m@collector"} // 13:11..13:17
	world := coretest.World(&coretest.MetricCapture{}, nil, nil)
	world.Shape = shape.New("UTC", incidents)
	for i := range 60 {
		gotF := w.heartbeatNow(tick(i), world, id, nil, fast, nil).Unix()
		gotS := w.heartbeatNow(tick(i), world, id, nil, slow, nil).Unix()
		wantF := boundary(tick(i), 5*time.Minute, 0)
		if i >= 11 && i < 20 { // missed 13:15 run; resumes at 13:20
			wantF = t0.Add(10 * time.Minute).Unix()
		}
		if wantS := boundary(tick(i), 30*time.Minute, 10*time.Minute); gotF != wantF || gotS != wantS {
			t.Fatalf("tick %d: fast %d slow %d, want fast %d slow %d", i, gotF, gotS, wantF, wantS)
		}
	}
}

// TestNowScheduleThroughTick: the schedule reaches the emitted sample through the strict decoder
// and a real Tick, and the default declaration still reads the tick time.
func TestNowScheduleThroughTick(t *testing.T) {
	const doc = `
traffic: {off_peak_rps: 0, peak_rps: 10}
services:
  - name: collector
    type: web
    entry: true
    custom_metrics:
      - name: job_last_success_timestamp_seconds
        source: test fixture
        instrument: gauge
        labels:
          source:
            enum:
              - {value: a}
        value: {now: {period: 15m, phase: 2m}}
      - name: heartbeat_timestamp_seconds
        source: test fixture
        instrument: gauge
        labels:
          source:
            enum:
              - {value: a}
        value: {now: {}}
`
	t0 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	var ticks []time.Time
	for i := range 20 {
		ticks = append(ticks, t0.Add(time.Duration(i)*time.Minute))
	}
	sched := runSeries(t, doc, "job_last_success_timestamp_seconds", "source", nil, ticks)
	beat := runSeries(t, doc, "heartbeat_timestamp_seconds", "source", nil, ticks)
	for i, at := range ticks {
		if got, want := sched[i]["a"], float64(boundary(at, 15*time.Minute, 2*time.Minute)); got != want {
			t.Fatalf("tick %d scheduled = %v, want %v", i, got, want)
		}
		if got, want := beat[i]["a"], float64(at.Unix()); got != want {
			t.Fatalf("tick %d default = %v, want %v", i, got, want)
		}
	}
}
