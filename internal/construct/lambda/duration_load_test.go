// SPDX-License-Identifier: AGPL-3.0-only

package lambda_test

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/lambda"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"gopkg.in/yaml.v3"
)

// loadFunction builds a one-function construct from YAML so the test also proves the keys
// decode strictly (KnownFields), exactly as a blueprint would.
func loadFunction(t *testing.T, body string) core.Construct {
	t.Helper()
	cfg := lambda.Registration().NewConfig()
	dec := yaml.NewDecoder(strings.NewReader("functions:\n  - name: nightly-batch\n" + body))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		t.Fatal(err)
	}
	c, err := lambda.Registration().Build(cfg, fx())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// tickAtHour returns the second (steady-state) minute at a Tuesday wall-clock hour.
func tickAtHour(t *testing.T, c core.Construct, hour int) []promrw.Series {
	t.Helper()
	out := &capture{}
	world := &core.World{Shape: shape.New("UTC", nil), Metrics: out}
	at := time.Date(2023, 11, 14, hour, 0, 0, 0, time.UTC)
	for _, now := range []time.Time{at, at.Add(time.Minute)} {
		out.batch = nil
		if e := c.Tick(context.Background(), now, world); e != nil {
			t.Fatal(e)
		}
	}
	return out.batch
}

const batchBody = "    load: business\n    invocations_per_min: 600\n    duration_ms: 4000\n"

func TestDurationFollowsTheLoadCurveWhenOptedIn(t *testing.T) {
	body := batchBody + "    duration_ms_idle: 500\n"
	night, peak := tickAtHour(t, loadFunction(t, body), 3), tickAtHour(t, loadFunction(t, body), 12)
	dn, dp := value(night, "aws_lambda_duration_average"), value(peak, "aws_lambda_duration_average")
	if dp != 4000 {
		t.Fatalf("plateau must reproduce duration_ms: %g", dp)
	}
	if !(dn >= 500 && dn < dp/2) {
		t.Fatalf("night duration %g not below the peak %g within [500,4000]", dn, dp)
	}
	// Sum stays executions x mean at both ticks, and executions still follow the curve.
	for _, b := range [][]promrw.Series{night, peak} {
		n, avg := value(b, "aws_lambda_duration_sample_count"), value(b, "aws_lambda_duration_average")
		if n <= 0 || value(b, "aws_lambda_duration_sum") != n*avg {
			t.Fatalf("sum != count x average: %v", b)
		}
		if value(b, "aws_lambda_duration_minimum") != avg || value(b, "aws_lambda_duration_maximum") != avg {
			t.Fatal("no spread declared: all stats must equal the mean")
		}
	}
	if value(night, "aws_lambda_duration_sample_count") >= value(peak, "aws_lambda_duration_sample_count") {
		t.Fatal("executions did not follow the curve")
	}
	// Modelled concurrency follows the shorter batch.
	if value(night, "aws_lambda_concurrent_executions_sum") >= value(peak, "aws_lambda_concurrent_executions_sum") {
		t.Fatal("concurrency did not follow the duration")
	}
}

func TestDurationLoadKeepsSpreadBoundsAroundTheMean(t *testing.T) {
	body := batchBody + "    duration_ms_min: 1000\n    duration_ms_max: 9000\n    duration_ms_idle: 500\n"
	for _, hour := range []int{3, 12} {
		b := tickAtHour(t, loadFunction(t, body), hour)
		min, avg, max := value(b, "aws_lambda_duration_minimum"), value(b, "aws_lambda_duration_average"), value(b, "aws_lambda_duration_maximum")
		if !(min <= avg && avg <= max) || avg < 500 || avg > 4000 {
			t.Fatalf("hour %d: min=%g avg=%g max=%g", hour, min, avg, max)
		}
	}
}

func TestDurationIsUnchangedWithoutTheOptIn(t *testing.T) {
	night, peak := tickAtHour(t, loadFunction(t, batchBody), 3), tickAtHour(t, loadFunction(t, batchBody), 12)
	for _, b := range [][]promrw.Series{night, peak} {
		if value(b, "aws_lambda_duration_average") != 4000 {
			t.Fatalf("duration moved without opt-in: %v", value(b, "aws_lambda_duration_average"))
		}
	}
	// Identical to the pre-existing configuration, stat for stat.
	base := lambda.Function{Name: "nightly-batch", Load: "business", InvocationsPerMin: 600, DurationMS: 4000}
	if !reflect.DeepEqual(sorted(night), sorted(businessTick(t, base, 3))) || !reflect.DeepEqual(sorted(peak), sorted(businessTick(t, base, 12))) {
		t.Fatal("output differs from base when not opted in")
	}
}

func TestDurationIdleValidation(t *testing.T) {
	for _, f := range []lambda.Function{
		{Name: "x", DurationMS: 100, DurationMSIdle: 10},                   // needs load: business
		{Name: "x", Load: "constant", DurationMS: 100, DurationMSIdle: 10}, // needs load: business
		{Name: "x", Load: "business", DurationMS: 100, DurationMSIdle: 101},
		{Name: "x", Load: "business", DurationMS: 100, DurationMSIdle: -1},
		{Name: "x", Load: "business", DurationMS: 100, DurationMSIdle: 60001},
	} {
		if _, e := lambda.Build(&lambda.Config{Functions: []lambda.Function{f}}, fx()); e == nil {
			t.Errorf("accepted %+v", f)
		}
	}
}

// sorted orders a batch by series name and function so two batches compare by value.
func sorted(b []promrw.Series) []promrw.Series {
	out := append([]promrw.Series(nil), b...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name+out[i].Labels["dimension_FunctionName"] < out[j].Name+out[j].Labels["dimension_FunctionName"]
	})
	return out
}

// scheduledBody is a fixed-cadence function: load stays constant, only the duration opts in.
const scheduledBody = "    invocations_per_min: 600\n    duration_ms: 4000\n    duration_ms_idle: 500\n    duration_load: business\n"

func TestScheduledFunctionKeepsInvocationsWhileDurationFollowsTheCurve(t *testing.T) {
	night, peak := tickAtHour(t, loadFunction(t, scheduledBody), 3), tickAtHour(t, loadFunction(t, scheduledBody), 12)
	if nn, np := value(night, "aws_lambda_invocations_sum"), value(peak, "aws_lambda_invocations_sum"); nn != 600 || np != 600 {
		t.Fatalf("invocations must stay constant: night=%g peak=%g", nn, np)
	}
	dn, dp := value(night, "aws_lambda_duration_average"), value(peak, "aws_lambda_duration_average")
	if dp != 4000 {
		t.Fatalf("plateau must reproduce duration_ms: %g", dp)
	}
	if !(dn >= 500 && dn < dp/2) {
		t.Fatalf("night duration %g not below the peak %g within [500,4000]", dn, dp)
	}
	for _, b := range [][]promrw.Series{night, peak} {
		n, avg := value(b, "aws_lambda_duration_sample_count"), value(b, "aws_lambda_duration_average")
		if n != 600 || value(b, "aws_lambda_duration_sum") != n*avg {
			t.Fatalf("sum != count x average: %v", b)
		}
	}
}

func TestDurationLoadWithLoadBusinessMatchesLoadBusinessAlone(t *testing.T) {
	both := batchBody + "    duration_ms_idle: 500\n    duration_load: business\n"
	alone := batchBody + "    duration_ms_idle: 500\n"
	for _, hour := range []int{3, 12} {
		if !reflect.DeepEqual(sorted(tickAtHour(t, loadFunction(t, both), hour)), sorted(tickAtHour(t, loadFunction(t, alone), hour))) {
			t.Fatalf("hour %d: duration_load changed load: business output", hour)
		}
	}
}

func TestDurationLoadValidation(t *testing.T) {
	for _, f := range []lambda.Function{
		{Name: "x", DurationMS: 100, DurationLoad: "business"},                      // needs duration_ms_idle
		{Name: "x", DurationMS: 100, DurationMSIdle: 10, DurationLoad: "constant"},  // opted out, no load: business
		{Name: "x", DurationMS: 100, DurationMSIdle: 10, DurationLoad: "bursty"},    // unknown value
		{Name: "x", DurationMS: 100, DurationMSIdle: 101, DurationLoad: "business"}, // above duration_ms
	} {
		if _, e := lambda.Build(&lambda.Config{Functions: []lambda.Function{f}}, fx()); e == nil {
			t.Errorf("accepted %+v", f)
		}
	}
	ok := lambda.Function{Name: "x", DurationMS: 100, DurationMSIdle: 10, DurationLoad: "business"}
	if _, e := lambda.Build(&lambda.Config{Functions: []lambda.Function{ok}}, fx()); e != nil {
		t.Errorf("rejected %+v: %v", ok, e)
	}
}
