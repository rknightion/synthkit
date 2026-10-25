// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
)

// lambdaCausalYAML is a test-only declaration (never a deployable blueprint): one function bound
// to a concurrency resource, one legacy function beside it, and three scheduled incidents that
// perturb the resource model. The constant 10 rps demand against 900 ms executions offers 9 of
// 20 concurrent executions, so the healthy function never throttles; partner-api (120 ms,
// 4x slow) is the dependency held across the execution.
const lambdaCausalYAML = `
name: lambda-causal
label: lambda-causal
environments:
  - name: prod
    cloud:
      provider: aws
      account_id: "111122223333"
      region: eu-west-1
      vpc_id: vpc-fixture
      cloudwatch: {albs: 0, s3_buckets: 0, nlb: false, ebs: false, nat_gateway: false, eks: false, firehose: false, private_link: false}
      lambda:
        functions:
          - name: enrich-fn
            resource: enrich-fn-concurrency
          - name: legacy-fn
            invocations_per_min: 120
            duration_ms: 50
    resources:
      - { name: partner-api, kind: dependency, latency_ms: 120, slow_factor: 4 }
      - name: enrich-fn-concurrency
        kind: concurrency
        size: 20
        hold_ms: 900
        held_across: partner-api
        demand: { off_peak_rps: 10, peak_rps: 10, load: constant }
scenarios:
  - name: reduced
    effects: [{ mode: concurrency_reduced, target: enrich-fn-concurrency, intensity: 0.7 }]
  - name: unavailable
    effects: [{ mode: dependency_unavailable, target: partner-api, intensity: 0.5 }]
  - name: slow
    effects: [{ mode: dependency_slow, target: partner-api, intensity: 1 }]
incidents:
  - { scenario: reduced, at: "2026-10-24T09:10", for: 10m }
  - { scenario: unavailable, at: "2026-10-24T09:25", for: 5m }
  - { scenario: slow, at: "2026-10-24T09:35", for: 5m }
`

type lambdaMinute struct {
	invocations, throttles, errors, concurrent, duration float64
}

func lambdaFamilies(mc *coretest.MetricCapture, fn string) lambdaMinute {
	var m lambdaMinute
	for _, s := range mc.All() {
		if s.Labels["dimension_FunctionName"] != fn {
			continue
		}
		switch s.Name {
		case "aws_lambda_invocations_sum":
			m.invocations = s.Value
		case "aws_lambda_throttles_sum":
			m.throttles = s.Value
		case "aws_lambda_errors_sum":
			m.errors = s.Value
		case "aws_lambda_concurrent_executions_maximum":
			m.concurrent = s.Value
		case "aws_lambda_duration_average":
			m.duration = s.Value
		}
	}
	return m
}

// A function bound to a concurrency resource, loaded from blueprint YAML through the real loader
// and catalog, follows the resource model's window: concurrency_reduced throttles, a held-across
// dependency_unavailable fails executions, dependency_slow stretches Duration, and every family
// drains to the healthy baseline after each incident. The legacy sibling is untouched.
func TestBoundLambdaFollowsResourceWindow(t *testing.T) {
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(lambdaCausalYAML), reg)
	if err != nil {
		t.Fatal(err)
	}
	cr, _ := reg.Construct("lambda")
	var ci *blueprint.ConstructInstance
	for i := range res.Constructs {
		if res.Constructs[i].Kind == "lambda" {
			ci = &res.Constructs[i]
		}
	}
	if ci == nil {
		t.Fatal("no lambda construct")
	}
	c, err := cr.Build(ci.Config, ci.Fixtures)
	if err != nil {
		t.Fatal(err)
	}
	eng := shape.New("UTC", res.Incidents)
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	got := map[int]lambdaMinute{}
	legacy := map[int]lambdaMinute{}
	for i := 1; i <= 45; i++ {
		now := start.Add(time.Duration(i) * time.Minute)
		// The runner advances every causal consumer on each master tick before the construct ticks.
		adv, ok := c.(causal.Advancer)
		if !ok {
			t.Fatal("lambda construct is not a causal.Advancer")
		}
		adv.AdvanceCausal(now, eng)
		mc := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), now, &core.World{Shape: eng, Metrics: mc}); err != nil {
			t.Fatal(err)
		}
		got[i] = lambdaFamilies(mc, "enrich-fn")
		legacy[i] = lambdaFamilies(mc, "legacy-fn")
	}
	healthy := lambdaMinute{invocations: 600, concurrent: 9, duration: 900}
	legacyWant := lambdaMinute{invocations: 120, concurrent: 1, duration: 50}
	near := func(a, b float64) bool { return math.Abs(a-b) <= 1 }
	for i := 1; i <= 45; i++ {
		g := got[i]
		if legacy[i] != legacyWant {
			t.Fatalf("minute %d: legacy sibling %+v, want %+v", i, legacy[i], legacyWant)
		}
		if g.errors > g.invocations {
			t.Fatalf("minute %d: errors %v exceed invocations %v", i, g.errors, g.invocations)
		}
		// Counts agree: the constant 10 rps demand is either admitted or throttled, never lost.
		if !near(g.invocations+g.throttles, 600) {
			t.Fatalf("minute %d: invocations %v + throttles %v != 600 arrivals", i, g.invocations, g.throttles)
		}
		switch {
		case i == 10: // one of twelve steps of the window is inside the concurrency_reduced incident
			if g.throttles <= 0 || g.throttles >= 200 || g.invocations >= 600 {
				t.Fatalf("minute 10: partial throttling expected, got %+v", g)
			}
		case i >= 11 && i <= 19: // concurrency 20 -> 6 at intensity 0.7: 9 offered, 1 - 6/9 rejected
			if !near(g.invocations, 400) || !near(g.throttles, 200) || g.errors != 0 || g.concurrent != 6 || g.duration != 900 {
				t.Fatalf("minute %d under concurrency_reduced: %+v", i, g)
			}
		case i >= 26 && i <= 29: // half the calls to the held-across dependency fail
			// A failed execution ends at its failed call (SKT-0195): a tenth of the 900 ms hold,
			// then a tenth of the partner's 120 ms latency, so 10 rps x (0.5 x 900 + 0.5 x 102) ms
			// = 5.01 executions in use, a peak of 6.
			if !near(g.invocations, 600) || !near(g.errors, 300) || g.throttles != 0 || g.concurrent != 6 || g.duration != 900 {
				t.Fatalf("minute %d under dependency_unavailable: %+v", i, g)
			}
		case i >= 36 && i <= 39: // h = 900 + 120 x (4 - 1) = 1260 ms; 12.6 offered
			if !near(g.invocations, 600) || g.errors != 0 || g.throttles != 0 || g.concurrent != 13 || g.duration != 1260 {
				t.Fatalf("minute %d under dependency_slow: %+v", i, g)
			}
		case i == 20 || i == 25 || i == 30 || i == 35 || i == 40: // window straddles an incident edge
			if g == healthy {
				t.Fatalf("minute %d: an edge window shows no effect: %+v", i, g)
			}
		default: // before, between and after: fully drained
			if g != healthy {
				t.Fatalf("minute %d: %+v, want the healthy baseline %+v", i, g, healthy)
			}
		}
	}
}

func loadLambdaCausal(t *testing.T, text string) (*core.Registry, *blueprint.Resolved, error) {
	t.Helper()
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(text), reg)
	return reg, res, err
}

// A bound function is declared by its resource: every arrival-rate and duration field except
// error_fraction (the background error share, SKT-0168.09.28) is rejected when the lambda construct is built (the loader and the runner build path), naming the
// field; a resource of the wrong kind, an unknown one and a doubly bound one are rejected too.
func TestBoundLambdaRejectsForbiddenFieldsAndBindings(t *testing.T) {
	for _, field := range []string{
		"invocations_per_min: 60", "async_received_per_min: 10", "async_dropped_fraction: 0.1", "async_age_ms: 100",
		"stream_batches_per_min: 5", "iterator_age_ms: 100", "throttles_per_min: 1", "duration_ms: 100",
		"duration_ms_min: 10", "duration_ms_max: 200", "duration_ms_idle: 10", "duration_load: business",
		"load: business",
	} {
		name, _, _ := strings.Cut(field, ":")
		text := strings.Replace(lambdaCausalYAML, "            resource: enrich-fn-concurrency\n", "            resource: enrich-fn-concurrency\n            "+field+"\n", 1)
		reg, res, err := loadLambdaCausal(t, text)
		if err != nil {
			t.Fatalf("%s: load failed before the build check: %v", field, err)
		}
		err = buildLambda(reg, res)
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s on a bound function: build error %v, want one naming %q", field, err, name)
		}
	}
	for name, tc := range map[string]struct{ text, want string }{
		"wrong kind": {strings.Replace(lambdaCausalYAML, "resource: enrich-fn-concurrency", "resource: partner-api", 1), "want kind concurrency"},
		"unknown":    {strings.Replace(lambdaCausalYAML, "resource: enrich-fn-concurrency", "resource: nope", 1), "not a declared resource"},
		"shared": {strings.Replace(lambdaCausalYAML, "          - name: legacy-fn\n            invocations_per_min: 120\n            duration_ms: 50\n",
			"          - name: twin-fn\n            resource: enrich-fn-concurrency\n", 1), "bind the same resource"},
		"no resources": {lambdaWithoutResources, "declares no environments[].resources"},
	} {
		reg, res, err := loadLambdaCausal(t, tc.text)
		if err != nil {
			t.Fatalf("%s: load: %v", name, err)
		}
		if err := buildLambda(reg, res); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: build error %v, want %q", name, err, tc.want)
		}
	}
}

// lambdaWithoutResources binds a function while the blueprint declares no resources at all.
var lambdaWithoutResources = lambdaCausalYAML[:strings.Index(lambdaCausalYAML, "    resources:")]

func buildLambda(reg *core.Registry, res *blueprint.Resolved) error {
	cr, _ := reg.Construct("lambda")
	for _, ci := range res.Constructs {
		if ci.Kind == "lambda" {
			_, err := cr.Build(ci.Config, ci.Fixtures)
			return err
		}
	}
	return nil
}

// The function-axis modes cannot target a bound function: it is not a function-axis target, so
// the loader rejects the incident, while the legacy function beside it still accepts each mode.
func TestBoundLambdaRejectsFunctionAxisModes(t *testing.T) {
	for _, mode := range []string{"function_error_spike", "function_throttle_storm", "function_duration_storm"} {
		base := strings.Replace(lambdaCausalYAML, "effects: [{ mode: dependency_slow, target: partner-api, intensity: 1 }]",
			"effects: [{ mode: "+mode+", target: %s, intensity: 1 }]", 1)
		if _, _, err := loadLambdaCausal(t, strings.Replace(base, "%s", "enrich-fn", 1)); err == nil || !strings.Contains(err.Error(), `unknown target "enrich-fn"`) {
			t.Fatalf("%s on the bound function: load error %v, want an unknown-target rejection naming enrich-fn", mode, err)
		}
		if _, _, err := loadLambdaCausal(t, strings.Replace(base, "%s", "legacy-fn", 1)); err != nil {
			t.Fatalf("%s on the legacy function must still load: %v", mode, err)
		}
	}
}

// lambdaCausalMinutes loads text through the real loader and catalog, ticks its lambda construct
// for 45 one-minute windows from 09:00 as the runner does, and returns fn's families per minute.
func lambdaCausalMinutes(t *testing.T, text, fn string) map[int]lambdaMinute {
	t.Helper()
	reg, res, err := loadLambdaCausal(t, text)
	if err != nil {
		t.Fatal(err)
	}
	cr, _ := reg.Construct("lambda")
	var ci *blueprint.ConstructInstance
	for i := range res.Constructs {
		if res.Constructs[i].Kind == "lambda" {
			ci = &res.Constructs[i]
		}
	}
	if ci == nil {
		t.Fatal("no lambda construct")
	}
	c, err := cr.Build(ci.Config, ci.Fixtures)
	if err != nil {
		t.Fatal(err)
	}
	adv, ok := c.(causal.Advancer)
	if !ok {
		t.Fatal("lambda construct is not a causal.Advancer")
	}
	eng := shape.New("UTC", res.Incidents)
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	out := map[int]lambdaMinute{}
	for i := 1; i <= 45; i++ {
		now := start.Add(time.Duration(i) * time.Minute)
		adv.AdvanceCausal(now, eng)
		mc := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), now, &core.World{Shape: eng, Metrics: mc}); err != nil {
			t.Fatal(err)
		}
		out[i] = lambdaFamilies(mc, fn)
	}
	return out
}

// A bound function may declare error_fraction: a background share of the admitted invocations
// that fail whatever the model says. At baseline Errors are exactly that share (60 of 600), a
// held-across dependency_unavailable adds the model's failures on top (half of the admitted
// calls, so 300 + 60), concurrency_reduced still throttles without erroring beyond the share,
// and every other family is identical to the same
// function declared without it.
func TestBoundLambdaBackgroundErrorFraction(t *testing.T) {
	bound := strings.Replace(lambdaCausalYAML, "            resource: enrich-fn-concurrency\n", "            resource: enrich-fn-concurrency\n            error_fraction: 0.1\n", 1)
	if bound == lambdaCausalYAML {
		t.Fatal("test setup: error_fraction not injected")
	}
	with := lambdaCausalMinutes(t, bound, "enrich-fn")
	without := lambdaCausalMinutes(t, lambdaCausalYAML, "enrich-fn")
	near := func(a, b float64) bool { return math.Abs(a-b) <= 1 }
	for i := 1; i <= 45; i++ {
		w, wo := with[i], without[i]
		if w.invocations != wo.invocations || w.throttles != wo.throttles || w.concurrent != wo.concurrent || w.duration != wo.duration {
			t.Fatalf("minute %d: error_fraction moved a non-error family: %+v vs %+v", i, w, wo)
		}
		if w.errors > w.invocations {
			t.Fatalf("minute %d: errors %v exceed invocations %v", i, w.errors, w.invocations)
		}
		switch {
		case i <= 5 || (i >= 31 && i <= 34): // healthy baseline: the background share alone
			if !near(w.errors, 0.1*w.invocations) || wo.errors != 0 {
				t.Fatalf("minute %d: errors %v (without %v), want 10%% of %v invocations", i, w.errors, wo.errors, w.invocations)
			}
		case i >= 26 && i <= 29: // half fail through the dependency and the background share of all admitted work adds to them
			if !near(w.errors, 300+0.1*600) || !near(wo.errors, 300) {
				t.Fatalf("minute %d under dependency_unavailable: errors %v (without %v), want 360 (300)", i, w.errors, wo.errors)
			}
		case i >= 11 && i <= 19: // throttled work is not invoked, so the share is of the 400 admitted
			if !near(w.errors, 40) {
				t.Fatalf("minute %d under concurrency_reduced: errors %v, want 40", i, w.errors)
			}
		}
	}
	// The share is fractional-carry deterministic: a second run is identical.
	again := lambdaCausalMinutes(t, bound, "enrich-fn")
	for i := range with {
		if with[i] != again[i] {
			t.Fatalf("minute %d: not deterministic: %+v vs %+v", i, with[i], again[i])
		}
	}
}

// error_fraction on a bound function is bounded like the unbound field: a share above 1 is
// rejected when the construct is built.
func TestBoundLambdaErrorFractionRange(t *testing.T) {
	text := strings.Replace(lambdaCausalYAML, "            resource: enrich-fn-concurrency\n", "            resource: enrich-fn-concurrency\n            error_fraction: 1.5\n", 1)
	reg, res, err := loadLambdaCausal(t, text)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := buildLambda(reg, res); err == nil || !strings.Contains(err.Error(), "error_fraction must be finite in [0,1]") {
		t.Fatalf("error_fraction 1.5 on a bound function: build error %v, want the range rejection", err)
	}
}
