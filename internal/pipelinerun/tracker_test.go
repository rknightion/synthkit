// SPDX-License-Identifier: AGPL-3.0-only

package pipelinerun

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

func testPipeline() Pipeline {
	return Pipeline{
		Name: "orders", Environment: "airflow-a", Every: "5m", Offset: "1m",
		Stages: []Stage{
			{Name: "extract", Service: ServiceGlue, Resource: "orders-extract", Duration: "1m"},
			{Name: "transform", Service: ServiceGlue, Resource: "orders-transform", Duration: "2m"},
			{Name: "publish", Service: ServiceAirflow, Duration: "30s"},
		},
	}
}

// window returns an EvalFunc that activates mode on scope for now in [from, to).
func window(mode, scope string, from, to time.Time, inten float64) EvalFunc {
	return func(now time.Time, m, s string) (bool, float64) {
		if m == mode && s == scope && !now.Before(from) && now.Before(to) {
			return true, inten
		}
		return false, 0
	}
}

func run(t *testing.T, tr *Tracker, start time.Time, ticks int, eval EvalFunc) []Window {
	t.Helper()
	var out []Window
	for i := 1; i <= ticks; i++ {
		out = append(out, tr.Advance(start.Add(time.Duration(i)*time.Minute), eval))
	}
	return out
}

func mustTracker(t *testing.T, seed string, ps ...Pipeline) *Tracker {
	t.Helper()
	tr, err := NewTracker(seed, ps, time.Minute)
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}
	return tr
}

func TestScheduleIsDeterministic(t *testing.T) {
	p := testPipeline()
	p.Offset = "" // seed-derived phase
	a := run(t, mustTracker(t, "seed-1", p), t0, 180, nil)
	b := run(t, mustTracker(t, "seed-1", p), t0, 180, nil)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed and ticks produced different windows")
	}
	triggered := 0
	for _, w := range a {
		triggered += w.Pipelines[0].Triggered
	}
	if triggered != 36 { // 180 minutes at a 5m cadence
		t.Fatalf("triggered %d runs in 180m, want 36", triggered)
	}
	differs := false
	for _, seed := range []string{"seed-2", "seed-3", "seed-4"} {
		if !reflect.DeepEqual(a, run(t, mustTracker(t, seed, p), t0, 180, nil)) {
			differs = true
		}
	}
	if !differs {
		t.Fatal("seed does not influence the schedule")
	}
}

func TestScheduleTriggersOnOffsetAndCompletes(t *testing.T) {
	ws := run(t, mustTracker(t, "s", testPipeline()), t0, 60, nil)
	succeeded := 0
	for _, w := range ws {
		pw := w.Pipelines[0]
		trigMinute := w.To.Minute()%5 == 1
		if (pw.Triggered == 1) != trigMinute {
			t.Fatalf("window ending %v: triggered=%d", w.To, pw.Triggered)
		}
		if pw.Failed != 0 {
			t.Fatalf("baseline must not fail: %+v", pw)
		}
		for _, s := range pw.Stages {
			if s.Pending != 0 || s.OldestPendingAgeSec != 0 {
				t.Fatalf("baseline must not queue: %+v", s)
			}
		}
		succeeded += pw.Succeeded
		if pw.Succeeded > 0 {
			avg := pw.SucceededDurSec / float64(pw.Succeeded)
			if avg < 0.85*210 || avg > 1.15*210+3 {
				t.Fatalf("run duration %vs outside jittered 210s", avg)
			}
		}
	}
	if succeeded < 11 {
		t.Fatalf("succeeded %d runs in 60m at 5m cadence", succeeded)
	}
}

func TestStageStallAgesArrivalsAndSuppressesCompletions(t *testing.T) {
	stallFrom, stallTo := t0.Add(30*time.Minute), t0.Add(60*time.Minute)
	eval := window(ModeStageStall, StageScope("orders", "transform"), stallFrom, stallTo, 1)
	ws := run(t, mustTracker(t, "s", testPipeline()), t0, 120, eval)

	var lastAge float64
	sawAge := false
	drained := false
	for _, w := range ws {
		pw := w.Pipelines[0]
		ex, tf, pub := pw.Stages[0], pw.Stages[1], pw.Stages[2]
		stalled := !w.To.Before(stallFrom) && w.To.Before(stallTo)
		switch {
		case stalled:
			if tf.Started != 0 {
				t.Fatalf("%v: stalled stage started %d", w.To, tf.Started)
			}
			// In-flight work at the stall onset may finish in the first minutes; nothing
			// started under the stall can, so after one nominal duration completions stop.
			if w.To.Sub(stallFrom) > 3*time.Minute && (tf.Succeeded != 0 || pub.Started != 0 || pw.Succeeded != 0) {
				t.Fatalf("%v: completions not suppressed: transform=%+v publish=%+v", w.To, tf, pub)
			}
			if ex.Succeeded+ex.Running == 0 && w.To.Minute()%5 == 2 {
				t.Fatalf("%v: upstream must keep running under a downstream stall", w.To)
			}
			if tf.OldestPendingAgeSec > 0 {
				if tf.OldestPendingAgeSec <= lastAge {
					t.Fatalf("%v: pending age %v did not rise from %v", w.To, tf.OldestPendingAgeSec, lastAge)
				}
				sawAge = true
			}
			lastAge = tf.OldestPendingAgeSec
		case w.To.Equal(stallTo):
			if tf.Started < 5 {
				t.Fatalf("on clear the backlog must start: started %d", tf.Started)
			}
			if tf.Pending != 0 || tf.OldestPendingAgeSec != 0 {
				t.Fatalf("on clear nothing may stay pending: %+v", tf)
			}
		case w.To.After(stallTo.Add(10 * time.Minute)):
			if pub.Succeeded > 0 {
				drained = true
			}
		}
	}
	if !sawAge {
		t.Fatal("stall never produced a pending age")
	}
	if lastAge < 25*60 {
		t.Fatalf("oldest pending age at stall end %vs, want close to 30m", lastAge)
	}
	if !drained {
		t.Fatal("pipeline did not resume after the stall cleared")
	}
}

func TestStageStallWithoutIncidentIsInert(t *testing.T) {
	// Red/green anchor: the same scenario with no eval never ages or suppresses.
	ws := run(t, mustTracker(t, "s", testPipeline()), t0, 60, nil)
	for _, w := range ws {
		if w.Pipelines[0].Stages[1].OldestPendingAgeSec != 0 {
			t.Fatal("age without an incident")
		}
	}
}

func TestRunFailureRecordsFailedTerminalOutcome(t *testing.T) {
	failFrom, failTo := t0.Add(20*time.Minute), t0.Add(40*time.Minute)
	eval := window(ModeRunFailure, StageScope("orders", "transform"), failFrom, failTo, 1)
	ws := run(t, mustTracker(t, "s", testPipeline()), t0, 70, eval)
	failed, succeededAfter := 0, 0
	for _, w := range ws {
		pw := w.Pipelines[0]
		active := !w.To.Before(failFrom) && w.To.Before(failTo)
		if active {
			if pw.Stages[1].Succeeded != 0 {
				t.Fatalf("%v: transform succeeded under run_failure intensity 1", w.To)
			}
			if pw.Stages[2].Started != 0 {
				t.Fatalf("%v: failed runs must not reach later stages", w.To)
			}
			if pw.Failed != pw.Stages[1].Failed {
				t.Fatalf("%v: terminal failures %d != stage failures %d", w.To, pw.Failed, pw.Stages[1].Failed)
			}
			if pw.Failed > 0 && pw.FailedDurSec <= 0 {
				t.Fatalf("%v: failed run has no duration", w.To)
			}
		} else if w.To.After(failTo.Add(5 * time.Minute)) {
			succeededAfter += pw.Succeeded
			if pw.Failed != 0 {
				t.Fatalf("%v: failure outlived its incident", w.To)
			}
		}
		failed += pw.Failed
	}
	if failed < 3 {
		t.Fatalf("failed %d runs in a 20m window at 5m cadence", failed)
	}
	if succeededAfter == 0 {
		t.Fatal("runs did not succeed again after the incident")
	}
}

func TestRunFailureIntensityIsAFraction(t *testing.T) {
	eval := func(time.Time, string, string) (bool, float64) { return true, 0.5 }
	failOnly := func(now time.Time, m, s string) (bool, float64) {
		if m != ModeRunFailure {
			return false, 0
		}
		return eval(now, m, s)
	}
	ws := run(t, mustTracker(t, "s", testPipeline()), t0, 24*60, failOnly)
	ok, bad := 0, 0
	for _, w := range ws {
		ok += w.Pipelines[0].Succeeded
		bad += w.Pipelines[0].Failed
	}
	// Three stages each failing half: about 1/8 of runs survive.
	if bad == 0 || ok == 0 || float64(ok)/float64(ok+bad) > 0.25 {
		t.Fatalf("intensity 0.5 on all stages: ok=%d failed=%d", ok, bad)
	}
}

func TestCompletionDurationExtremaMatchTerminalObservations(t *testing.T) {
	p := testPipeline()
	coarse, fine := mustTracker(t, "s", p), mustTracker(t, "s", p)
	coarse.Advance(t0, nil)
	fine.Advance(t0, nil)
	stall := window(ModeStageStall, StageScope(p.Name, "transform"), t0, t0.Add(time.Hour), 1)
	fail := window(ModeRunFailure, StageScope(p.Name, "transform"), t0, t0.Add(2*time.Hour), 0.5)

	// First accumulate a backlog, then drain it with mixed terminal outcomes. The fine tracker
	// exposes individual terminal durations through the existing sums, independently of extrema.
	from := t0
	for phase, eval := range []EvalFunc{stall, fail} {
		to := from.Add(30 * time.Minute)
		got := coarse.Advance(to, eval).Pipelines[0]
		var successes, failures []float64
		for at := from.Add(time.Second); !at.After(to); at = at.Add(time.Second) {
			pw := fine.Advance(at, eval).Pipelines[0]
			if pw.Succeeded > 1 || pw.Failed > 1 {
				t.Fatalf("fine window must isolate terminal observations: %+v", pw)
			}
			if pw.Succeeded == 1 {
				successes = append(successes, pw.SucceededDurSec)
			}
			if pw.Failed == 1 {
				failures = append(failures, pw.FailedDurSec)
			}
		}
		for _, outcome := range []struct {
			name          string
			observations  []float64
			count         int
			sum, min, max float64
		}{
			{"success", successes, got.Succeeded, got.SucceededDurSec, got.SucceededDurMinSec, got.SucceededDurMaxSec},
			{"failure", failures, got.Failed, got.FailedDurSec, got.FailedDurMinSec, got.FailedDurMaxSec},
		} {
			if phase == 1 && len(outcome.observations) < 2 {
				t.Fatalf("need multiple %s observations, got %v", outcome.name, outcome.observations)
			}
			var sum, min, max float64
			for i, dur := range outcome.observations {
				sum += dur
				if i == 0 || dur < min {
					min = dur
				}
				if dur > max {
					max = dur
				}
			}
			if outcome.count != len(outcome.observations) || outcome.sum != sum || outcome.min != min || outcome.max != max {
				t.Errorf("phase %d %s: got count/sum/min/max %d/%g/%g/%g, want %d/%g/%g/%g from terminal observations %v",
					phase, outcome.name, outcome.count, outcome.sum, outcome.min, outcome.max,
					len(outcome.observations), sum, min, max, outcome.observations)
			}
			if phase == 1 && (min == max || max <= 10*60) {
				t.Fatalf("%s observations must have distinct extrema and include backlog wait: %v", outcome.name, outcome.observations)
			}
		}
		from = to
	}

	// A repeated timestamp records no new completions; previous extrema must not leak forward.
	empty := coarse.Advance(from, fail).Pipelines[0]
	if empty.Succeeded != 0 || empty.Failed != 0 || empty.SucceededDurSec != 0 || empty.FailedDurSec != 0 ||
		empty.SucceededDurMinSec != 0 || empty.SucceededDurMaxSec != 0 || empty.FailedDurMinSec != 0 || empty.FailedDurMaxSec != 0 {
		t.Fatalf("empty window retained terminal observations: %+v", empty)
	}
}

func TestCompletionDurationExtremaIgnoreDeclarationOrder(t *testing.T) {
	p, q := testPipeline(), testPipeline()
	q.Name = "other"
	a, b := mustTracker(t, "s", p, q), mustTracker(t, "s", q, p)
	eval := func(_ time.Time, mode, _ string) (bool, float64) {
		return mode == ModeRunFailure, 0.5
	}
	for i := 0; i < 3; i++ {
		at := t0.Add(time.Duration(i) * time.Hour)
		aw, bw := a.Advance(at, eval), b.Advance(at, eval)
		if !reflect.DeepEqual(aw.Pipelines[0], bw.Pipelines[1]) || !reflect.DeepEqual(aw.Pipelines[1], bw.Pipelines[0]) {
			t.Fatalf("declaration order changed pipeline observations at %v: %+v vs %+v", at, aw, bw)
		}
	}
}

func TestStagesForAndForEnvironment(t *testing.T) {
	w := mustTracker(t, "s", testPipeline()).Advance(t0, nil)
	if got := w.StagesFor(ServiceGlue, "orders-transform"); len(got) != 1 || got[0].Name != "transform" {
		t.Fatalf("StagesFor: %+v", got)
	}
	if got := w.ForEnvironment("airflow-a"); len(got) != 1 {
		t.Fatalf("ForEnvironment: %+v", got)
	}
	if got := w.ForEnvironment(""); len(got) != 0 {
		t.Fatalf("empty environment matched: %+v", got)
	}
}

func TestValidate(t *testing.T) {
	ok := testPipeline()
	if err := Validate([]Pipeline{ok}); err != nil {
		t.Fatalf("valid pipeline rejected: %v", err)
	}
	cases := map[string]func(p *Pipeline){
		"name must match":                  func(p *Pipeline) { p.Name = "Orders.Daily" },
		"every":                            func(p *Pipeline) { p.Every = "30s" },
		"offset":                           func(p *Pipeline) { p.Offset = "5m" },
		"at least one stage":               func(p *Pipeline) { p.Stages = nil },
		"unknown service":                  func(p *Pipeline) { p.Stages[0].Service = "kinesis" },
		"needs a resource":                 func(p *Pipeline) { p.Stages[0].Resource = "" },
		"at least 1s":                      func(p *Pipeline) { p.Stages[0].Duration = "10ms" },
		"stage \"extract\" declared twice": func(p *Pipeline) { p.Stages[1].Name = "extract" },
	}
	for want, mut := range cases {
		p := testPipeline()
		p.Stages = append([]Stage(nil), p.Stages...)
		mut(&p)
		err := Validate([]Pipeline{p})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
	if err := Validate([]Pipeline{ok, ok}); err == nil || !strings.Contains(err.Error(), "declared twice") {
		t.Errorf("duplicate pipeline: %v", err)
	}
}

func TestFailureModesAreOnStageAxis(t *testing.T) {
	if len(FailureModes) != 2 {
		t.Fatalf("modes: %+v", FailureModes)
	}
	for _, m := range FailureModes {
		if m.Axis != AxisStage {
			t.Fatalf("mode %q on axis %q", m.Name, m.Axis)
		}
	}
	if StageScope("a", "b") != "a.b" {
		t.Fatal("scope form")
	}
}
