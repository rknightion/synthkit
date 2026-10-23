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
