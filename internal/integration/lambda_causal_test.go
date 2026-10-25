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
			// = 5.01 executions in use, a peak of 6. Duration reports the same mix (SKT-0202): a
			// mean of 501 ms, so concurrency = invocations/s x mean duration (Little's law).
			if !near(g.invocations, 600) || !near(g.errors, 300) || g.throttles != 0 || g.concurrent != 6 || math.Abs(g.duration-501) > 1e-6 {
				t.Fatalf("minute %d under dependency_unavailable: %+v", i, g)
			}
			if little := g.invocations / 60 * g.duration / 1000; g.concurrent != math.Ceil(little) {
				t.Fatalf("minute %d: ConcurrentExecutions %v disagree with invocations/s x Duration %v", i, g.concurrent, little)
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
	out := map[int]lambdaMinute{}
	for i, mc := range lambdaCausalCaptures(t, text) {
		out[i] = lambdaFamilies(mc, fn)
	}
	return out
}

// lambdaCausalCaptures is lambdaCausalMinutes' run, returning each minute's whole capture.
func lambdaCausalCaptures(t *testing.T, text string) map[int]*coretest.MetricCapture {
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
	out := map[int]*coretest.MetricCapture{}
	for i := 1; i <= 45; i++ {
		now := start.Add(time.Duration(i) * time.Minute)
		adv.AdvanceCausal(now, eng)
		mc := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), now, &core.World{Shape: eng, Metrics: mc}); err != nil {
			t.Fatal(err)
		}
		out[i] = mc
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

// lambdaCertYAML is a test-only declaration (never a deployable blueprint) with the certificate
// story's numbers (e2e/fixtures/causal-stories.yaml): a 900 ms execution held across the 120 ms
// partner API, whose server certificate expires. A failed handshake ends the execution after
// PreWorkFraction of the hold and of the partner's latency, 0.1 x 900 + 0.1 x 120 = 102 ms, and
// a credential failure is never resent. The constant 10 rps demand offers 9 of 20 executions, so
// nothing throttles. Every handshake fails in the first incident and half in the second.
const lambdaCertYAML = `
name: lambda-cert
label: lambda-cert
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
      - { name: partner-api, kind: dependency, latency_ms: 120 }
      - { name: partner-api-cert, kind: credential, of: partner-api, credential_type: server_certificate }
      - name: enrich-fn-concurrency
        kind: concurrency
        size: 20
        hold_ms: 900
        held_across: partner-api
        demand: { off_peak_rps: 10, peak_rps: 10, load: constant }
scenarios:
  - name: expired
    effects: [{ mode: credential_expired, target: partner-api-cert }]
  - name: half
    effects: [{ mode: credential_expired, target: partner-api-cert, intensity: 0.5 }]
incidents:
  - { scenario: expired, at: "2026-10-24T09:10", for: 10m }
  - { scenario: half, at: "2026-10-24T09:25", for: 5m }
`

// lambdaDuration is one minute's Duration statistics for fn.
type lambdaDuration struct{ sum, average, minimum, maximum, count float64 }

func lambdaDurationStats(mc *coretest.MetricCapture, fn string) lambdaDuration {
	var d lambdaDuration
	for _, s := range mc.All() {
		if s.Labels["dimension_FunctionName"] != fn {
			continue
		}
		switch s.Name {
		case "aws_lambda_duration_sum":
			d.sum = s.Value
		case "aws_lambda_duration_average":
			d.average = s.Value
		case "aws_lambda_duration_minimum":
			d.minimum = s.Value
		case "aws_lambda_duration_maximum":
			d.maximum = s.Value
		case "aws_lambda_duration_sample_count":
			d.count = s.Value
		}
	}
	return d
}

// A bound function's Duration is the mix of the executions the model completes and those it
// fails (SKT-0202): a failed execution ends at its failed handshake (102 ms with the
// certificate story's numbers), not at the 900 ms completing hold. Average is the mean, Sum the
// total, Minimum and Maximum the two populations and SampleCount the invocations, so Duration
// agrees with ConcurrentExecutions by Little's law (concurrency = invocations/s x mean duration).
// The legacy sibling's Duration is untouched.
func TestBoundLambdaDurationReportsFailedExecutions(t *testing.T) {
	caps := lambdaCausalCaptures(t, lambdaCertYAML)
	near := func(a, b, tol float64) bool { return math.Abs(a-b) <= tol }
	for i := 1; i <= 45; i++ {
		g, d := lambdaFamilies(caps[i], "enrich-fn"), lambdaDurationStats(caps[i], "enrich-fn")
		if l := lambdaDurationStats(caps[i], "legacy-fn"); l != (lambdaDuration{sum: 120 * 50, average: 50, minimum: 50, maximum: 50, count: 120}) {
			t.Fatalf("minute %d: legacy sibling Duration %+v", i, l)
		}
		if d.count != g.invocations || !near(d.sum, d.average*d.count, 1e-6*d.sum) || d.minimum > d.average || d.average > d.maximum {
			t.Fatalf("minute %d: inconsistent Duration statistics %+v for %v invocations", i, d, g.invocations)
		}
		// Little's law: the window's peak in-use concurrency is never below invocations/s x mean
		// duration, and in a steady minute it is that mean rounded up to whole executions.
		mean := g.invocations / 60 * d.average / 1000
		if g.concurrent < mean-0.05 {
			t.Fatalf("minute %d: ConcurrentExecutions %v below %v invocations x %v ms", i, g.concurrent, g.invocations, d.average)
		}
		steady := i <= 9 || (i >= 11 && i <= 19) || (i >= 21 && i <= 24) || (i >= 26 && i <= 29) || i >= 31
		if steady && g.concurrent != math.Ceil(mean-1e-9) {
			t.Fatalf("minute %d: ConcurrentExecutions %v, want ceil(%v invocations/s x %v ms)", i, g.concurrent, g.invocations/60, d.average)
		}
		switch {
		case i >= 11 && i <= 19: // every handshake fails: every execution lasts 102 ms
			want := lambdaDuration{sum: 600 * 102, average: 102, minimum: 102, maximum: 102, count: 600}
			if !near(d.sum, want.sum, 1e-6) || !near(d.average, 102, 1e-9) || !near(d.minimum, 102, 1e-9) || !near(d.maximum, 102, 1e-9) || d.count != 600 {
				t.Fatalf("minute %d under credential_expired: Duration %+v, want %+v", i, d, want)
			}
			if !near(g.errors, 600, 0) || g.concurrent != 2 { // 10 rps x 0.102 s = 1.02 in use
				t.Fatalf("minute %d under credential_expired: %+v", i, g)
			}
		case i >= 26 && i <= 29: // half fail: 300 x 900 + 300 x 102 ms, a mean of 501 ms
			if !near(g.errors, 300, 1) || !near(d.sum, 300*900+300*102, 900) || !near(d.average, 501, 1.5) ||
				!near(d.minimum, 102, 1e-9) || !near(d.maximum, 900, 1e-9) || d.count != 600 || g.concurrent != 6 {
				t.Fatalf("minute %d under half credential_expired: Duration %+v, families %+v", i, d, g)
			}
		case i <= 9 || (i >= 21 && i <= 24) || i >= 31: // healthy: the completing hold alone
			if d != (lambdaDuration{sum: 600 * 900, average: 900, minimum: 900, maximum: 900, count: 600}) || g.errors != 0 {
				t.Fatalf("minute %d healthy: Duration %+v, families %+v", i, d, g)
			}
		}
	}
}
