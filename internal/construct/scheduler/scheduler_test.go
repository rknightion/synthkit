// SPDX-License-Identifier: AGPL-3.0-only

package scheduler_test

import (
	"context"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/scheduler"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

type capture struct{ batch []promrw.Series }

func (c *capture) Write(_ context.Context, b []promrw.Series) error { c.batch = b; return nil }

func fx() *fixture.Set {
	return &fixture.Set{Env: &fixture.Env{Name: "prod"}, Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "eu-west-1"}}
}

func build(t *testing.T, s ...scheduler.Schedule) core.Construct {
	t.Helper()
	c, err := scheduler.Build(&scheduler.Config{Schedules: s}, fx())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func tick(t *testing.T, c core.Construct, at time.Time) []promrw.Series {
	t.Helper()
	w := &capture{}
	if err := c.Tick(context.Background(), at, &core.World{Metrics: w}); err != nil {
		t.Fatal(err)
	}
	sort.Slice(w.batch, func(i, j int) bool {
		a, b := w.batch[i], w.batch[j]
		return a.Name+"|"+a.Labels["dimension_ScheduleGroup"] < b.Name+"|"+b.Labels["dimension_ScheduleGroup"]
	})
	return w.batch
}

// get returns the value of series name for a group ("" = account level), or -1.
func get(b []promrw.Series, name, group string) float64 {
	for _, s := range b {
		g, has := s.Labels["dimension_ScheduleGroup"]
		if s.Name == name && g == group && has == (group != "") {
			return s.Value
		}
	}
	return -1
}

var stats = []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"}

// The captured shape: a rate(1 minute) schedule in its own group plus one in the
// default group yields per-group series and one dimensionless account series whose
// Sum and SampleCount are the attempt count and whose Average/Min/Max are 1.
func TestRegistrationAndCapturedInventory(t *testing.T) {
	r := scheduler.Registration()
	if r.Kind != "scheduler" || r.Scope != core.ScopeBlueprint {
		t.Fatal(r)
	}
	cfg := r.NewConfig().(*scheduler.Config)
	cfg.Schedules = []scheduler.Schedule{{Name: "sync", Group: "ops", Rate: "rate(1 minute)"}, {Name: "beat", Rate: "rate(1 minute)"}}
	c, err := r.Build(cfg, fx())
	if err != nil {
		t.Fatal(err)
	}
	b := tick(t, c, time.Unix(1700000000, 0))
	if len(b) != 15 {
		t.Fatalf("want 3 profiles x 5 stats, got %d: %v", len(b), b)
	}
	for _, s := range b {
		if s.Kind != promrw.KindGauge || !strings.HasPrefix(s.Name, "aws_scheduler_invocation_attempt_count_") {
			t.Fatal(s)
		}
		want := map[string]string{"account_id": "111122223333", "region": "eu-west-1", "namespace": "AWS/Scheduler", "job": "cloud/aws/scheduler", "name": "global"}
		for k, v := range want {
			if s.Labels[k] != v {
				t.Fatalf("label %s: %v", k, s)
			}
		}
		for k := range s.Labels {
			if _, ok := want[k]; !ok && k != "dimension_ScheduleGroup" {
				t.Fatalf("unexpected label %q: %v", k, s)
			}
		}
	}
	for _, g := range []string{"ops", "default"} {
		for _, st := range stats {
			if v := get(b, "aws_scheduler_invocation_attempt_count"+st, g); v != 1 {
				t.Fatalf("group %s %s = %g, want 1", g, st, v)
			}
		}
	}
	for st, want := range map[string]float64{"_sum": 2, "_average": 1, "_maximum": 1, "_minimum": 1, "_sample_count": 2} {
		if v := get(b, "aws_scheduler_invocation_attempt_count"+st, ""); v != want {
			t.Fatalf("account %s = %g, want %g", st, v, want)
		}
	}
}

// A rate(N minutes) schedule fires once per N minutes, in whole-minute windows, and
// publishes nothing in the windows between (sparse, as captured for the default group).
func TestCadenceIsFixedRateAndSparse(t *testing.T) {
	for _, tc := range []struct {
		rate   string
		period int
	}{{"rate(1 minute)", 1}, {"rate(5 minutes)", 5}, {"rate(1 hour)", 60}, {"rate(2 hours)", 120}} {
		t.Run(tc.rate, func(t *testing.T) {
			c := build(t, scheduler.Schedule{Name: "job", Group: "batch", Rate: tc.rate})
			at := time.Unix(1700000000, 0).Truncate(time.Minute)
			const windows = 240
			var fired []int
			for i := 0; i < windows; i++ {
				b := tick(t, c, at.Add(time.Duration(i)*time.Minute))
				v := get(b, "aws_scheduler_invocation_attempt_count_sum", "batch")
				switch {
				case v == 1:
					fired = append(fired, i)
				case v == -1:
					if len(b) != 0 {
						t.Fatalf("window %d emitted without an attempt: %v", i, b)
					}
				default:
					t.Fatalf("window %d: %g attempts", i, v)
				}
			}
			if len(fired) != windows/tc.period {
				t.Fatalf("%d firings in %d minutes, want %d", len(fired), windows, windows/tc.period)
			}
			for i := 1; i < len(fired); i++ {
				if fired[i]-fired[i-1] != tc.period {
					t.Fatalf("firings %v not spaced by %d", fired, tc.period)
				}
			}
		})
	}
}

// Distinct schedules of one rate get stable, identity-derived phases: a rebuild of the
// same declaration fires in the same minutes.
func TestPhaseIsStable(t *testing.T) {
	minutes := func() []int {
		c := build(t, scheduler.Schedule{Name: "a", Rate: "rate(10 minutes)"})
		at := time.Unix(1700000000, 0).Truncate(time.Minute)
		var out []int
		for i := 0; i < 30; i++ {
			if get(tick(t, c, at.Add(time.Duration(i)*time.Minute)), "aws_scheduler_invocation_attempt_count_sum", "default") == 1 {
				out = append(out, i)
			}
		}
		return out
	}
	a, b := minutes(), minutes()
	if len(a) != 3 || len(b) != 3 || a[0] != b[0] {
		t.Fatalf("unstable phase: %v vs %v", a, b)
	}
}

// Schedules sharing a rate get identity-derived phases, so they do not all fire in
// the same minute: twelve rate(1 hour) schedules must occupy more than one minute.
func TestSharedRateSchedulesHaveDistinctPhases(t *testing.T) {
	var s []scheduler.Schedule
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		s = append(s, scheduler.Schedule{Name: "job-" + n, Group: "batch", Rate: "rate(1 hour)"})
	}
	c := build(t, s...)
	at := time.Unix(1700000000, 0).Truncate(time.Hour)
	minutes, total := 0, 0.0
	for i := 1; i <= 60; i++ {
		if v := get(tick(t, c, at.Add(time.Duration(i)*time.Minute)), "aws_scheduler_invocation_attempt_count_sum", "batch"); v > 0 {
			minutes++
			total += v
		}
	}
	if total != 12 {
		t.Fatalf("%g attempts in one hour, want 12", total)
	}
	if minutes < 2 {
		t.Fatalf("all twelve schedules fired in %d minute(s): phases are not distinct", minutes)
	}
}

// Failure is opt-in. Failed runs are still attempts; each adds one TargetErrorCount
// and one InvocationDroppedCount (no retries). Without the option neither appears.
func TestFailureModeIsOptIn(t *testing.T) {
	at := time.Unix(1700000000, 0).Truncate(time.Minute)
	clean := build(t, scheduler.Schedule{Name: "ok", Rate: "rate(1 minute)"})
	failing := build(t, scheduler.Schedule{Name: "bad", Group: "etl", Rate: "rate(1 minute)", TargetErrorFraction: 0.25},
		scheduler.Schedule{Name: "ok", Group: "etl", Rate: "rate(1 minute)"})
	var attempts, errs, dropped float64
	for i := 0; i < 40; i++ {
		now := at.Add(time.Duration(i) * time.Minute)
		for _, s := range tick(t, clean, now) {
			if !strings.HasPrefix(s.Name, "aws_scheduler_invocation_attempt_count_") {
				t.Fatalf("failure metric without opt-in: %v", s)
			}
		}
		b := tick(t, failing, now)
		e, d := get(b, "aws_scheduler_target_error_count_sum", "etl"), get(b, "aws_scheduler_invocation_dropped_count_sum", "etl")
		if e != d {
			t.Fatalf("window %d: target errors %g != dropped %g", i, e, d)
		}
		if e > 0 {
			errs += e
			dropped += d
			for _, st := range stats[1:4] {
				if get(b, "aws_scheduler_target_error_count"+st, "etl") != 1 || get(b, "aws_scheduler_target_error_count"+st, "") != 1 {
					t.Fatalf("window %d: %s is not one observation of 1", i, st)
				}
			}
			if get(b, "aws_scheduler_target_error_count_sum", "") != e {
				t.Fatalf("window %d: account errors differ from the only group", i)
			}
		} else if e != -1 {
			t.Fatalf("window %d: zero-valued target error emitted", i)
		}
		attempts += get(b, "aws_scheduler_invocation_attempt_count_sum", "etl")
	}
	if attempts != 80 || errs != 10 || dropped != 10 {
		t.Fatalf("attempts %g errors %g dropped %g, want 80/10/10", attempts, errs, dropped)
	}
}

func TestWindowsAreNotReplayed(t *testing.T) {
	c := build(t, scheduler.Schedule{Name: "m", Rate: "rate(1 minute)"})
	at := time.Unix(1700000000, 0)
	if get(tick(t, c, at), "aws_scheduler_invocation_attempt_count_sum", "") != 1 {
		t.Fatal("first window")
	}
	for _, now := range []time.Time{at, at.Add(-time.Minute)} {
		if b := tick(t, c, now); len(b) != 0 {
			t.Fatalf("repeated/backward tick emitted %v", b)
		}
	}
	if v := get(tick(t, c, at.Add(6*time.Hour)), "aws_scheduler_invocation_attempt_count_sum", ""); v != 1 {
		t.Fatalf("gap replayed %g attempts", v)
	}
	if v := get(tick(t, c, at.Add(6*time.Hour+30*time.Second)), "aws_scheduler_invocation_attempt_count_sum", ""); v != -1 && v != 1 {
		t.Fatalf("half window: %g", v)
	}
}

func TestMasterVolumePause(t *testing.T) {
	c := build(t, scheduler.Schedule{Name: "m", Rate: "rate(1 minute)", TargetErrorFraction: 1})
	eng := shape.New("UTC", nil)
	out := &capture{}
	world := &core.World{Shape: eng, Metrics: out}
	run := func(now time.Time) []promrw.Series {
		t.Helper()
		out.batch = nil
		if err := c.Tick(context.Background(), now, world); err != nil {
			t.Fatal(err)
		}
		return out.batch
	}
	at := time.Unix(1700000000, 0)
	run(at)
	eng.SetVolumeMultiplier(0)
	for _, now := range []time.Time{at.Add(time.Minute), at.Add(24 * time.Hour)} {
		if b := run(now); len(b) != 0 {
			t.Fatal("paused emission", b)
		}
	}
	eng.SetVolumeMultiplier(1)
	if b := run(at.Add(48 * time.Hour)); len(b) != 0 {
		t.Fatal("resume fabricated paused activity", b)
	}
	if v := get(run(at.Add(48*time.Hour+time.Minute)), "aws_scheduler_invocation_attempt_count_sum", ""); v != 1 {
		t.Fatalf("after resume: %g", v)
	}
}

func TestParseRate(t *testing.T) {
	for expr, want := range map[string]time.Duration{
		"rate(1 minute)": time.Minute, "rate(15 minutes)": 15 * time.Minute, "rate(1 hour)": time.Hour,
		"rate(12 hours)": 12 * time.Hour, "rate(1 day)": 24 * time.Hour, "rate(7 days)": 7 * 24 * time.Hour,
	} {
		if got, err := scheduler.ParseRate(expr); err != nil || got != want {
			t.Fatalf("%s: %v %v", expr, got, err)
		}
	}
	for _, expr := range []string{"", "rate(0 minutes)", "rate(5 seconds)", "rate(1.5 hours)", "rate(-1 minutes)", "rate(5minutes)",
		"cron(0 12 * * ? *)", "at(2026-10-24T12:00:00)", "5m", "rate(367 days)", "rate( 1 minute)"} {
		if _, err := scheduler.ParseRate(expr); err == nil {
			t.Fatalf("%q accepted", expr)
		}
	}
}

func TestBuildRejects(t *testing.T) {
	ok := scheduler.Schedule{Name: "a", Rate: "rate(1 hour)"}
	cases := map[string]struct {
		cfg any
		fx  *fixture.Set
	}{
		"wrong type":         {&struct{}{}, fx()},
		"nil config":         {(*scheduler.Config)(nil), fx()},
		"no cloud":           {&scheduler.Config{Schedules: []scheduler.Schedule{ok}}, &fixture.Set{}},
		"no account":         {&scheduler.Config{Schedules: []scheduler.Schedule{ok}}, &fixture.Set{Cloud: &fixture.Cloud{Region: "eu-west-1"}}},
		"otlp export":        {&scheduler.Config{Schedules: []scheduler.Schedule{ok}}, &fixture.Set{Cloud: &fixture.Cloud{AccountID: "1", Region: "r", CloudWatchExport: "otlp"}}},
		"empty":              {&scheduler.Config{}, fx()},
		"bad name":           {&scheduler.Config{Schedules: []scheduler.Schedule{{Name: "a b", Rate: "rate(1 hour)"}}}, fx()},
		"bad group":          {&scheduler.Config{Schedules: []scheduler.Schedule{{Name: "a", Group: "g/1", Rate: "rate(1 hour)"}}}, fx()},
		"long name":          {&scheduler.Config{Schedules: []scheduler.Schedule{{Name: strings.Repeat("a", 65), Rate: "rate(1 hour)"}}}, fx()},
		"cron":               {&scheduler.Config{Schedules: []scheduler.Schedule{{Name: "a", Rate: "cron(0 * * * ? *)"}}}, fx()},
		"duplicate":          {&scheduler.Config{Schedules: []scheduler.Schedule{ok, ok}}, fx()},
		"default dup":        {&scheduler.Config{Schedules: []scheduler.Schedule{ok, {Name: "a", Group: "default", Rate: "rate(1 day)"}}}, fx()},
		"negative fraction":  {&scheduler.Config{Schedules: []scheduler.Schedule{{Name: "a", Rate: "rate(1 hour)", TargetErrorFraction: -0.1}}}, fx()},
		"fraction above one": {&scheduler.Config{Schedules: []scheduler.Schedule{{Name: "a", Rate: "rate(1 hour)", TargetErrorFraction: 1.5}}}, fx()},
		"NaN fraction":       {&scheduler.Config{Schedules: []scheduler.Schedule{{Name: "a", Rate: "rate(1 hour)", TargetErrorFraction: math.NaN()}}}, fx()},
	}
	for name, tc := range cases {
		if _, err := scheduler.Build(tc.cfg, tc.fx); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The same name in two groups is two schedules.
	if _, err := scheduler.Build(&scheduler.Config{Schedules: []scheduler.Schedule{ok, {Name: "a", Group: "other", Rate: "rate(1 hour)"}}}, fx()); err != nil {
		t.Fatal(err)
	}
}
