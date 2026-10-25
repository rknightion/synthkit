// SPDX-License-Identifier: AGPL-3.0-only

package lambda_test

import (
	"context"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/construct/lambda"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// causalSet declares a 20-slot concurrency resource, 900 ms executions including a 120 ms call
// to a dependency that is 4x slower at full intensity, and a constant 10 rps demand (9 offered).
func causalSet() causal.Set {
	return causal.Set{Envs: []causal.Declaration{{Env: "prod", Weight: 1, Resources: []causal.Resource{
		{Name: "dep", Kind: causal.KindDependency, LatencyMS: 120, SlowFactor: 4},
		{Name: "fn-conc", Kind: causal.KindConcurrency, Size: 20, HoldMS: 900, HeldAcross: "dep",
			Demand: &causal.Demand{OffPeakRPS: 10, PeakRPS: 10, Load: causal.LoadConstant}},
		{Name: "pool", Kind: causal.KindPool, Size: 5, HoldMS: 10, AcquireTimeoutMS: 100,
			Demand: &causal.Demand{OffPeakRPS: 1, PeakRPS: 1, Load: causal.LoadConstant}},
	}}}}
}

func boundConfig(fns ...lambda.Function) *lambda.Config {
	cfg := &lambda.Config{Functions: fns}
	cfg.UseResources(causalSet())
	return cfg
}

func seriesByName(w *capture, fn string) map[string]float64 {
	out := map[string]float64{}
	for _, s := range w.batch {
		if s.Labels["dimension_FunctionName"] == fn {
			out[s.Name] = s.Value
		}
	}
	return out
}

func TestBoundBuildRejections(t *testing.T) {
	for name, tc := range map[string]struct {
		fn   lambda.Function
		cfg  func(*lambda.Config)
		want string
	}{
		"rate field":     {fn: lambda.Function{Name: "a", Resource: "fn-conc", InvocationsPerMin: 5}, want: "invocations_per_min"},
		"several fields": {fn: lambda.Function{Name: "a", Resource: "fn-conc", DurationMS: 5, Load: "business"}, want: "duration_ms, load"},
		"wrong kind":     {fn: lambda.Function{Name: "a", Resource: "pool"}, want: "want kind concurrency"},
		"dependency":     {fn: lambda.Function{Name: "a", Resource: "dep"}, want: "want kind concurrency"},
		"unknown":        {fn: lambda.Function{Name: "a", Resource: "nope"}, want: "not a declared resource"},
		"no resources": {fn: lambda.Function{Name: "a", Resource: "fn-conc"}, want: "declares no environments[].resources",
			cfg: func(c *lambda.Config) { c.UseResources(causal.Set{}) }},
	} {
		cfg := boundConfig(tc.fn)
		if tc.cfg != nil {
			tc.cfg(cfg)
		}
		if _, err := lambda.Build(cfg, fx()); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error %v, want %q", name, err, tc.want)
		}
	}
	twins := boundConfig(lambda.Function{Name: "a", Resource: "fn-conc"}, lambda.Function{Name: "b", Resource: "fn-conc"})
	if _, err := lambda.Build(twins, fx()); err == nil || !strings.Contains(err.Error(), "bind the same resource") {
		t.Fatalf("two functions on one resource: %v", err)
	}
}

// A bound function is declared by its resource: it is not a function-axis target, and the
// registration carries the resource vocabulary beside the function-axis modes.
func TestBoundFunctionIsNotAFunctionAxisTarget(t *testing.T) {
	cfg := boundConfig(lambda.Function{Name: "bound", Resource: "fn-conc"}, lambda.Function{Name: "legacy", InvocationsPerMin: 60, DurationMS: 10})
	got := cfg.FailureTargets()
	if len(got) != 1 || got[0] != (failuremode.Target{Name: "legacy", Axis: failuremode.AxisFunction}) {
		t.Fatalf("targets %v, want only the legacy function", got)
	}
	reg := lambda.Registration()
	for _, m := range append(slices.Clone(lambda.FailureModes), causal.FailureModes...) {
		if !slices.Contains(reg.FailureModes, m) {
			t.Fatalf("registration lacks mode %+v", m)
		}
	}
	if len(reg.FailureModes) != len(lambda.FailureModes)+len(causal.FailureModes) {
		t.Fatalf("registration has %d modes", len(reg.FailureModes))
	}
}

// Without a shape engine the model sees no fault: the bound function reports the resource's
// healthy window, and a construct with no bound function ignores AdvanceCausal.
func TestBoundHealthyWindowAndUnboundAdvance(t *testing.T) {
	c, err := lambda.Build(boundConfig(lambda.Function{Name: "bound", Resource: "fn-conc"}), fx())
	if err != nil {
		t.Fatal(err)
	}
	w := &capture{}
	now := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, &core.World{Metrics: w}); err != nil {
		t.Fatal(err)
	}
	got := seriesByName(w, "bound")
	for name, want := range map[string]float64{
		"aws_lambda_invocations_sum": 600, "aws_lambda_duration_average": 900,
		"aws_lambda_concurrent_executions_maximum": 9,
	} {
		if math.Abs(got[name]-want) > 1 {
			t.Fatalf("%s = %v, want %v (all %v)", name, got[name], want, got)
		}
	}
	for _, name := range []string{"aws_lambda_throttles_sum", "aws_lambda_errors_sum"} {
		if _, ok := got[name]; ok {
			t.Fatalf("healthy bound function emitted %s", name)
		}
	}

	unbound := build(t, &lambda.Config{Functions: []lambda.Function{{Name: "u", InvocationsPerMin: 60, DurationMS: 10}}})
	unbound.(causal.Advancer).AdvanceCausal(now, shape.New("UTC", nil)) // no model: a no-op
	if got := seriesByName(&capture{batch: tickBatch(t, unbound, now)}, "u"); got["aws_lambda_invocations_sum"] != 60 {
		t.Fatalf("unbound output changed after AdvanceCausal: %v", got)
	}
}

func tickBatch(t *testing.T, c core.Construct, at time.Time) []promrw.Series {
	t.Helper()
	w := &capture{}
	if err := c.Tick(context.Background(), at, &core.World{Metrics: w}); err != nil {
		t.Fatal(err)
	}
	return w.batch
}

// Zero-valued samples apply to a bound function as to any other: Errors and Throttles observe
// every request.
func TestBoundZeroValuedSamples(t *testing.T) {
	c, err := lambda.Build(boundConfig(lambda.Function{Name: "bound", Resource: "fn-conc", ZeroValuedSamples: true}), fx())
	if err != nil {
		t.Fatal(err)
	}
	eng := shape.New("UTC", []string{"dependency_unavailable@2026-10-24T09:00/30m#0.5@dep"})
	w := &capture{}
	now := time.Date(2026, 10, 24, 9, 5, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, &core.World{Metrics: w, Shape: eng}); err != nil {
		t.Fatal(err)
	}
	got := seriesByName(w, "bound")
	inv, errs, count := got["aws_lambda_invocations_sum"], got["aws_lambda_errors_sum"], got["aws_lambda_errors_sample_count"]
	if math.Abs(errs-inv/2) > 1 || count != inv {
		t.Fatalf("errors %v of %v invocations (sample count %v): %v", errs, inv, count, got)
	}
}

// A bound function's error_fraction is a background share of the admitted invocations: it is the
// whole of Errors at baseline, is added to the model's failures during an incident without ever
// exceeding Invocations, and a share outside [0,1] is rejected.
func TestBoundBackgroundErrorFraction(t *testing.T) {
	c, err := lambda.Build(boundConfig(lambda.Function{Name: "bound", Resource: "fn-conc", ErrorFraction: 0.1}), fx())
	if err != nil {
		t.Fatal(err)
	}
	eng := shape.New("UTC", []string{"dependency_unavailable@2026-10-24T09:10/10m#1@dep"})
	tick := func(at time.Time) map[string]float64 {
		w := &capture{}
		if err := c.Tick(context.Background(), at, &core.World{Metrics: w, Shape: eng}); err != nil {
			t.Fatal(err)
		}
		return seriesByName(w, "bound")
	}
	base := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	for i := 1; i <= 5; i++ {
		c.(causal.Advancer).AdvanceCausal(base.Add(time.Duration(i)*time.Minute), eng)
		if got := tick(base.Add(time.Duration(i) * time.Minute)); math.Abs(got["aws_lambda_errors_sum"]-60) > 1 {
			t.Fatalf("minute %d: baseline errors %v, want 10%% of 600", i, got)
		}
	}
	for i := 12; i <= 15; i++ { // full unavailability fails everything admitted; the share cannot exceed it
		at := base.Add(time.Duration(i) * time.Minute)
		c.(causal.Advancer).AdvanceCausal(at, eng)
		got := tick(at)
		if got["aws_lambda_errors_sum"] != got["aws_lambda_invocations_sum"] || got["aws_lambda_invocations_sum"] < 590 {
			t.Fatalf("minute %d: errors %v of %v invocations, want all failed", i, got["aws_lambda_errors_sum"], got["aws_lambda_invocations_sum"])
		}
	}
	// The share the full unavailability could not add is not carried into the recovery.
	for i := 16; i <= 22; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		c.(causal.Advancer).AdvanceCausal(at, eng)
		got := tick(at)
		if i == 21 && math.Abs(got["aws_lambda_errors_sum"]-60) > 1 {
			t.Fatalf("minute 21: recovery errors %v, want 10%% of 600 with no carried burst", got["aws_lambda_errors_sum"])
		}
	}
	for _, v := range []float64{-0.1, 1.1, math.NaN()} {
		if _, err := lambda.Build(boundConfig(lambda.Function{Name: "a", Resource: "fn-conc", ErrorFraction: v}), fx()); err == nil || !strings.Contains(err.Error(), "error_fraction must be finite in [0,1]") {
			t.Fatalf("error_fraction %v: %v", v, err)
		}
	}
}
