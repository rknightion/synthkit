// SPDX-License-Identifier: AGPL-3.0-only

package lambda_test

import (
	"context"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/lambda"
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
func build(t *testing.T, cfg *lambda.Config) core.Construct {
	t.Helper()
	c, e := lambda.Build(cfg, fx())
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func tick(t *testing.T, c core.Construct, at time.Time) []promrw.Series {
	t.Helper()
	w := &capture{}
	if e := c.Tick(context.Background(), at, &core.World{Metrics: w}); e != nil {
		t.Fatal(e)
	}
	// State collection is a map-backed batch; wire order is not a value contract.
	sort.Slice(w.batch, func(i, j int) bool {
		a, b := w.batch[i], w.batch[j]
		return a.Name+a.Labels["dimension_FunctionName"] < b.Name+b.Labels["dimension_FunctionName"]
	})
	return w.batch
}
func value(b []promrw.Series, n string) float64 {
	for _, s := range b {
		if s.Name == n {
			return s.Value
		}
	}
	return -1
}
func TestRegistrationAndInventory(t *testing.T) {
	r := lambda.Registration()
	if r.Kind != "lambda" || r.Scope != core.ScopeBlueprint {
		t.Fatal(r)
	}
	cfg := r.NewConfig().(*lambda.Config)
	cfg.Functions = []lambda.Function{{Name: "worker", InvocationsPerMin: 12, DurationMS: 25, ErrorFraction: 0.25, ThrottlesPerMin: 2, AsyncReceivedPerMin: 8, AsyncDroppedFraction: 0.25, AsyncAgeMS: 15, StreamBatchesPerMin: 6, IteratorAgeMS: 20}, {Name: "idle"}}
	c, e := r.Build(cfg, fx())
	if e != nil {
		t.Fatal(e)
	}
	now := time.Unix(1700000000, 0)
	b := tick(t, c, now)
	bases := []string{"async_event_age", "async_events_dropped", "async_events_received", "claimed_account_concurrency", "concurrent_executions", "duration", "errors", "invocations", "iterator_age", "throttles", "unreserved_concurrent_executions"}
	seen := map[string]int{}
	for _, s := range b {
		if s.Kind != promrw.KindGauge || math.IsNaN(s.Value) || math.IsInf(s.Value, 0) || s.Value < 0 {
			t.Fatal(s)
		}
		for _, base := range bases {
			if strings.HasPrefix(s.Name, "aws_lambda_"+base+"_") {
				seen[base]++
			}
		}
		if s.Labels["name"] != "global" || s.Labels["namespace"] != "AWS/Lambda" {
			t.Fatal(s)
		}
		if strings.Contains(s.Name, "account_concurrency") || strings.Contains(s.Name, "unreserved_concurrent") {
			if _, ok := s.Labels["dimension_FunctionName"]; ok {
				t.Fatal(s)
			}
		} else if s.Labels["dimension_FunctionName"] != "worker" {
			t.Fatal(s)
		}
		for k := range s.Labels {
			if k == "dimension_Resource" || strings.HasPrefix(k, "tag_") || strings.HasPrefix(k, "asserts_") || k == "blueprint" {
				t.Fatal(s)
			}
		}
	}
	if len(b) != 55 || len(seen) != 11 {
		t.Fatalf("inventory: %d series %v", len(b), seen)
	}
	for _, base := range bases {
		if seen[base] != 5 {
			t.Fatal(base, seen)
		}
	}
	for n, want := range map[string]float64{"duration_sum": 300, "duration_sample_count": 12, "duration_average": 25, "invocations_sum": 12, "invocations_sample_count": 12, "errors_sum": 3, "errors_sample_count": 3, "async_event_age_sum": 90, "async_event_age_sample_count": 6, "iterator_age_sample_count": 6} {
		if got := value(b, "aws_lambda_"+n); got != want {
			t.Fatalf("%s=%v want %v", n, got, want)
		}
	}
	if got := tick(t, c, now); len(got) != 0 {
		t.Fatal("repeated window emitted", got)
	}
	if next := tick(t, c, now.Add(time.Minute)); value(next, "aws_lambda_invocations_sum") != 12 {
		t.Fatal("cumulative count")
	}
	other := build(t, cfg)
	if !reflect.DeepEqual(b, tick(t, other, now)) {
		t.Fatal("not deterministic")
	}
}
func TestSparseZeroAndElapsedWindows(t *testing.T) {
	c := build(t, &lambda.Config{Functions: []lambda.Function{{Name: "sparse", InvocationsPerMin: 0.5, DurationMS: 10}}})
	now := time.Unix(1700000000, 0)
	if b := tick(t, c, now); value(b, "aws_lambda_invocations_sum") != -1 {
		t.Fatal("fabricated traffic", b)
	}
	if b := tick(t, c, now.Add(30*time.Second)); value(b, "aws_lambda_invocations_sum") != -1 {
		t.Fatal(b)
	}
	if b := tick(t, c, now.Add(time.Minute)); value(b, "aws_lambda_invocations_sum") != 1 {
		t.Fatal("lost fractional arrival", b)
	}
	if b := tick(t, c, now.Add(24*time.Hour)); value(b, "aws_lambda_invocations_sum") != -1 {
		t.Fatal("backfilled gap", b)
	}
	idle := build(t, &lambda.Config{Functions: []lambda.Function{{Name: "idle"}}})
	for i := 0; i < 3; i++ {
		b := tick(t, idle, now.Add(time.Duration(i)*time.Minute))
		if len(b) != 10 || value(b, "aws_lambda_unreserved_concurrent_executions_sum") != 0 {
			t.Fatal(b)
		}
	}
}
func TestMasterVolumePause(t *testing.T) {
	for _, f := range []lambda.Function{
		{Name: "busy", InvocationsPerMin: 12, DurationMS: 25, ErrorFraction: 0.25, ThrottlesPerMin: 2, AsyncReceivedPerMin: 8, AsyncDroppedFraction: 0.25, AsyncAgeMS: 15, StreamBatchesPerMin: 6, IteratorAgeMS: 20},
		{Name: "carry", InvocationsPerMin: 1, DurationMS: 25, ErrorFraction: 0.5, ThrottlesPerMin: 0.5, AsyncReceivedPerMin: 0.5, StreamBatchesPerMin: 0.5, AsyncAgeMS: 15, IteratorAgeMS: 20},
	} {
		t.Run(f.Name, func(t *testing.T) {
			c := build(t, &lambda.Config{ReservedConcurrency: 3, Functions: []lambda.Function{f}})
			eng := shape.New("UTC", nil)
			out := &capture{}
			world := &core.World{Shape: eng, Metrics: out}
			at := time.Unix(1700000000, 0)
			run := func(now time.Time) []promrw.Series {
				t.Helper()
				out.batch = nil
				if e := c.Tick(context.Background(), now, world); e != nil {
					t.Fatal(e)
				}
				return out.batch
			}
			run(at)
			eng.SetVolumeMultiplier(0)
			for _, now := range []time.Time{at.Add(10 * time.Second), at.Add(24 * time.Hour)} {
				if b := run(now); len(b) != 0 {
					t.Fatal("master pause emitted a batch", b)
				}
			}
			eng.SetVolumeMultiplier(2) // Positive master volume is allowed to retain fixed rates.
			resume := at.Add(48 * time.Hour)
			if b := run(resume); len(b) != 0 {
				t.Fatal("resume fabricated paused activity", b)
			}
			b := run(resume.Add(time.Minute))
			want := 12.0
			if f.Name == "carry" {
				want = 2
			}
			if n := value(b, "aws_lambda_invocations_sum"); n != want {
				t.Fatalf("active carry lost or paused arrivals accrued: %g want %g", n, want)
			}
			if f.Name == "carry" && value(b, "aws_lambda_throttles_sum") != 1 {
				t.Fatal("lost throttle carry", b)
			}
		})
	}
}
func TestSparseDisjointPopulations(t *testing.T) {
	c := build(t, &lambda.Config{Functions: []lambda.Function{{Name: "mixed", InvocationsPerMin: 1.5, DurationMS: 25, AsyncReceivedPerMin: 1, AsyncDroppedFraction: 0.5, AsyncAgeMS: 15, StreamBatchesPerMin: 0.5, IteratorAgeMS: 20}}})
	at := time.Unix(1700000000, 0)
	count := func(b []promrw.Series, name string) float64 { return math.Max(0, value(b, "aws_lambda_"+name)) }
	total, received, dropped, stream := 0.0, 0.0, 0.0, 0.0
	for i := 0; i <= 120; i++ {
		b := tick(t, c, at.Add(time.Duration(i)*10*time.Second))
		n := count(b, "invocations_sum")
		a := count(b, "async_event_age_sample_count")
		s := count(b, "iterator_age_sample_count")
		r := count(b, "async_events_received_sum")
		d := count(b, "async_events_dropped_sum")
		if a != r-d || n < a+s || count(b, "duration_sample_count") != n {
			t.Fatalf("incoherent short window %d: executions=%g async=%g stream=%g received=%g dropped=%g", i, n, a, s, r, d)
		}
		total += n
		received += r
		dropped += d
		stream += s
	}
	// 21 active minutes: each disjoint population retains its fractional remainder.
	if total != 31 || received != 21 || dropped != 10 || stream != 10 {
		t.Fatalf("lost configured long-run rates: total=%g received=%g dropped=%g stream=%g", total, received, dropped, stream)
	}
}
func TestPopulationAndCapacityValidation(t *testing.T) {
	for _, cfg := range []*lambda.Config{
		{ReservedConcurrency: 0.5, Functions: []lambda.Function{{Name: "fractional"}}},
		{Functions: []lambda.Function{{Name: "async-no-execution", AsyncReceivedPerMin: 8}}},
		{Functions: []lambda.Function{{Name: "stream-no-execution", StreamBatchesPerMin: 1}}},
		{Functions: []lambda.Function{{Name: "overlap", InvocationsPerMin: 11, AsyncReceivedPerMin: 8, AsyncDroppedFraction: 0.25, StreamBatchesPerMin: 6}}},
	} {
		if _, e := lambda.Build(cfg, fx()); e == nil {
			t.Errorf("accepted incompatible populations/capacity: %+v", cfg)
		}
	}
	// Entirely dropped ingress legitimately requires no execution population.
	build(t, &lambda.Config{ReservedConcurrency: 3, Functions: []lambda.Function{{Name: "dropped", AsyncReceivedPerMin: 8, AsyncDroppedFraction: 1}}})
}
func TestValidation(t *testing.T) {
	f := fx()
	f.Cloud.CloudWatchExport = "otlp"
	if _, e := lambda.Build(&lambda.Config{}, f); e == nil || !strings.Contains(e.Error(), "otlp") {
		t.Fatal(e)
	}
	for _, cfg := range []*lambda.Config{nil, {}, {Functions: []lambda.Function{{Name: ""}}}, {Functions: []lambda.Function{{Name: "x"}, {Name: "x"}}}, {Functions: []lambda.Function{{Name: "x", InvocationsPerMin: math.NaN()}}}, {Functions: []lambda.Function{{Name: "x", DurationMS: math.Inf(1)}}}, {Functions: []lambda.Function{{Name: "x", ErrorFraction: 1.1}}}, {Functions: []lambda.Function{{Name: "x", ThrottlesPerMin: -1}}}} {
		if _, e := lambda.Build(cfg, fx()); e == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
	if _, e := lambda.Build("wrong", fx()); e == nil {
		t.Fatal("wrong config")
	}
	if _, e := lambda.Build(&lambda.Config{}, nil); e == nil {
		t.Fatal("nil fixtures")
	}
}

// businessTick runs one function for two consecutive minutes at the given weekday
// wall-clock hour (UTC engine) and returns the second, steady-state batch.
func businessTick(t *testing.T, f lambda.Function, hour int) []promrw.Series {
	t.Helper()
	c := build(t, &lambda.Config{Functions: []lambda.Function{f}})
	out := &capture{}
	world := &core.World{Shape: shape.New("UTC", nil), Metrics: out}
	at := time.Date(2023, 11, 14, hour, 0, 0, 0, time.UTC) // a Tuesday
	for _, now := range []time.Time{at, at.Add(time.Minute)} {
		out.batch = nil
		if e := c.Tick(context.Background(), now, world); e != nil {
			t.Fatal(e)
		}
	}
	return out.batch
}

func TestBusinessLoadFollowsTheBusinessCurve(t *testing.T) {
	f := lambda.Function{Name: "api", Load: "business", InvocationsPerMin: 600, DurationMS: 40, ErrorFraction: 0.1,
		ThrottlesPerMin: 30, AsyncReceivedPerMin: 100, AsyncDroppedFraction: 0.2, AsyncAgeMS: 5, StreamBatchesPerMin: 60, IteratorAgeMS: 9}
	noon, night := businessTick(t, f, 12), businessTick(t, f, 3)
	nn, nt := value(noon, "aws_lambda_invocations_sum"), value(night, "aws_lambda_invocations_sum")
	if nn != 600 {
		t.Fatalf("business plateau must reproduce the configured rate: %g", nn)
	}
	if nt <= 0 || nt >= nn*0.25 {
		t.Fatalf("overnight invocations %g not scaled below the plateau %g", nt, nn)
	}
	// Disjoint populations scale together and stay coherent.
	for _, b := range [][]promrw.Series{noon, night} {
		n := value(b, "aws_lambda_invocations_sum")
		a := value(b, "aws_lambda_async_event_age_sample_count")
		s := value(b, "aws_lambda_iterator_age_sample_count")
		if value(b, "aws_lambda_duration_sample_count") != n || n < a+s || a != value(b, "aws_lambda_async_events_received_sum")-value(b, "aws_lambda_async_events_dropped_sum") {
			t.Fatalf("incoherent scaled populations: n=%g async=%g stream=%g", n, a, s)
		}
	}
	// error_fraction applies to the scaled executions, not the configured rate.
	for _, b := range [][]promrw.Series{noon, night} {
		n, e := value(b, "aws_lambda_invocations_sum"), value(b, "aws_lambda_errors_sum")
		if math.Abs(e-n*0.1) > 1 {
			t.Fatalf("errors %g not error_fraction of scaled executions %g", e, n)
		}
	}
	if value(night, "aws_lambda_throttles_sum") >= value(noon, "aws_lambda_throttles_sum") {
		t.Fatal("throttles did not follow the curve")
	}
}

func TestConstantLoadIgnoresTheShapeEngine(t *testing.T) {
	for _, load := range []string{"", "constant"} {
		f := lambda.Function{Name: "api", Load: load, InvocationsPerMin: 600, DurationMS: 40}
		if n, d := value(businessTick(t, f, 12), "aws_lambda_invocations_sum"), value(businessTick(t, f, 3), "aws_lambda_invocations_sum"); n != 600 || d != 600 {
			t.Fatalf("load %q must stay constant: noon=%g night=%g", load, n, d)
		}
	}
}

func TestBusinessLoadWithoutShapeIsUnscaled(t *testing.T) {
	c := build(t, &lambda.Config{Functions: []lambda.Function{{Name: "api", Load: "business", InvocationsPerMin: 12, DurationMS: 5}}})
	if v := value(tick(t, c, time.Unix(1700000000, 0)), "aws_lambda_invocations_sum"); v != 12 {
		t.Fatal(v)
	}
}

func TestDurationSpreadDifferentiatesFiveStats(t *testing.T) {
	f := lambda.Function{Name: "api", InvocationsPerMin: 120, DurationMS: 40, DurationMSMin: 12, DurationMSMax: 900}
	b := tick(t, build(t, &lambda.Config{Functions: []lambda.Function{f}}), time.Unix(1700000000, 0))
	min, avg, max, sum, cnt := value(b, "aws_lambda_duration_minimum"), value(b, "aws_lambda_duration_average"), value(b, "aws_lambda_duration_maximum"), value(b, "aws_lambda_duration_sum"), value(b, "aws_lambda_duration_sample_count")
	if cnt != 120 || avg != 40 || sum != 4800 {
		t.Fatalf("mean/sum must stay the declared duration: sum=%g avg=%g count=%g", sum, avg, cnt)
	}
	if !(min < avg && avg < max) || min < 12 || max > 900 || min == max {
		t.Fatalf("spread not realised: min=%g avg=%g max=%g", min, avg, max)
	}
	// One execution cannot have a spread.
	one := tick(t, build(t, &lambda.Config{Functions: []lambda.Function{{Name: "one", InvocationsPerMin: 1, DurationMS: 40, DurationMSMin: 12, DurationMSMax: 900}}}), time.Unix(1700000000, 0))
	if value(one, "aws_lambda_duration_minimum") != 40 || value(one, "aws_lambda_duration_maximum") != 40 {
		t.Fatalf("single execution spread: %v", one)
	}
	// Deterministic.
	again := tick(t, build(t, &lambda.Config{Functions: []lambda.Function{f}}), time.Unix(1700000000, 0))
	if !reflect.DeepEqual(b, again) {
		t.Fatal("not deterministic")
	}
}

func TestSpreadAndLoadValidation(t *testing.T) {
	for _, f := range []lambda.Function{
		{Name: "x", Load: "bursty"},
		{Name: "x", DurationMS: 40, DurationMSMax: 900}, // half-open: no silent no-op
		{Name: "x", DurationMS: 40, DurationMSMin: 12},
		{Name: "x", DurationMS: 40, DurationMSMin: 50},
		{Name: "x", DurationMS: 40, DurationMSMax: 30},
		{Name: "x", DurationMS: 40, DurationMSMin: 30, DurationMSMax: 20},
		{Name: "x", DurationMSMax: 60001},
		{Name: "x", DurationMSMin: math.NaN()},
		{Name: "x", DurationMSMin: -1},
	} {
		if _, e := lambda.Build(&lambda.Config{Functions: []lambda.Function{f}}, fx()); e == nil {
			t.Errorf("accepted %+v", f)
		}
	}
}

func durationStats(t *testing.T, n int, mean, lo, hi float64) (min, max, sum, cnt float64) {
	t.Helper()
	f := lambda.Function{Name: "api", InvocationsPerMin: float64(n), DurationMS: mean, DurationMSMin: lo, DurationMSMax: hi}
	b := tick(t, build(t, &lambda.Config{Functions: []lambda.Function{f}}), time.Unix(1700000000, 0))
	return value(b, "aws_lambda_duration_minimum"), value(b, "aws_lambda_duration_maximum"), value(b, "aws_lambda_duration_sum"), value(b, "aws_lambda_duration_sample_count")
}

func TestDurationExtremesAreFeasibleForTheSample(t *testing.T) {
	// Two executions averaging 40 ms with bounds 12..900 cannot have max 470 and min 26.
	min, max, sum, cnt := durationStats(t, 2, 40, 12, 900)
	if sum != 80 || cnt != 2 {
		t.Fatalf("sum/count changed: %g %g", sum, cnt)
	}
	if min+max != sum {
		t.Fatalf("n=2 extremes must be the whole sample: min=%g max=%g sum=%g", min, max, sum)
	}
	// Property: for every small n and bound set a sample with this sum and these
	// extremes exists, and the extremes stay inside the declared bounds.
	const eps = 1e-9
	for _, c := range [][3]float64{{40, 12, 900}, {40, 39, 41}, {40, 1, 40}, {40, 40, 900}, {1, 0.5, 60000}, {250, 10, 300}} {
		for n := 1; n <= 12; n++ {
			min, max, sum, cnt := durationStats(t, n, c[0], c[1], c[2])
			if cnt != float64(n) || math.Abs(sum-float64(n)*c[0]) > eps {
				t.Fatalf("n=%d %v: sum/count %g %g", n, c, sum, cnt)
			}
			mean := sum / cnt
			if min > mean+eps || max < mean-eps || min < c[1]-eps || max > c[2]+eps {
				t.Fatalf("n=%d %v: min=%g mean=%g max=%g outside bounds", n, c, min, mean, max)
			}
			if max > sum-float64(n-1)*min+eps || min < sum-float64(n-1)*max-eps {
				t.Fatalf("n=%d %v: infeasible min=%g max=%g sum=%g", n, c, min, max, sum)
			}
			if n == 1 && (min != c[0] || max != c[0]) {
				t.Fatalf("n=1 %v: %g %g", c, min, max)
			}
		}
	}
}

func TestUnboundedDurationStaysExactForInexactMeans(t *testing.T) {
	// 10*0.1 and 9*0.1 do not round-trip in binary; the feasibility clamp must not run
	// (or invert the stats) when no spread is declared.
	for n := 1; n <= 12; n++ {
		min, max, _, _ := durationStats(t, n, 0.1, 0, 0)
		if min != 0.1 || max != 0.1 {
			t.Fatalf("n=%d: %v %v", n, min, max)
		}
	}
}
