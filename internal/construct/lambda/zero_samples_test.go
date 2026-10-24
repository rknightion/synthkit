// SPDX-License-Identifier: AGPL-3.0-only

package lambda_test

import (
	"math"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/lambda"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// stats returns the five CloudWatch statistics of one base, -1 where absent.
func stats(b []promrw.Series, base string) map[string]float64 {
	out := map[string]float64{}
	for _, suffix := range []string{"sum", "average", "maximum", "minimum", "sample_count"} {
		out[suffix] = value(b, "aws_lambda_"+base+"_"+suffix)
	}
	return out
}

func near(t *testing.T, what string, got map[string]float64, want map[string]float64) {
	t.Helper()
	for k, w := range want {
		if math.Abs(got[k]-w) > 1e-9 {
			t.Fatalf("%s %s=%v want %v (all %v)", what, k, got[k], w, got)
		}
	}
}

// The 2026-10 lab capture reported Throttles and Errors with a 0 sample per request
// (invocation or throttle), not only for events: Throttles SampleCount 314 = 46
// invocations + 268 throttles, Errors SampleCount 314 with Sum 1.
func TestZeroValuedSamplesObserveEveryRequest(t *testing.T) {
	fn := lambda.Function{Name: "worker", InvocationsPerMin: 12, DurationMS: 25, ErrorFraction: 0.25, ThrottlesPerMin: 2, ZeroValuedSamples: true}
	b := tick(t, build(t, &lambda.Config{Functions: []lambda.Function{fn}}), time.Unix(1700000000, 0))
	near(t, "throttles", stats(b, "throttles"), map[string]float64{"sum": 2, "sample_count": 14, "average": 2.0 / 14, "maximum": 1, "minimum": 0})
	near(t, "errors", stats(b, "errors"), map[string]float64{"sum": 3, "sample_count": 14, "average": 3.0 / 14, "maximum": 1, "minimum": 0})
	// Invocations remain binary events of executions only, throttled requests excluded.
	near(t, "invocations", stats(b, "invocations"), map[string]float64{"sum": 12, "sample_count": 12, "average": 1, "maximum": 1, "minimum": 1})
}

func TestZeroValuedSamplesWithoutThrottlesOrErrors(t *testing.T) {
	fn := lambda.Function{Name: "quiet", InvocationsPerMin: 6, DurationMS: 25, ZeroValuedSamples: true}
	b := tick(t, build(t, &lambda.Config{Functions: []lambda.Function{fn}}), time.Unix(1700000000, 0))
	near(t, "throttles", stats(b, "throttles"), map[string]float64{"sum": 0, "sample_count": 6, "average": 0, "maximum": 0, "minimum": 0})
	near(t, "errors", stats(b, "errors"), map[string]float64{"sum": 0, "sample_count": 6, "average": 0, "maximum": 0, "minimum": 0})
}

func TestZeroValuedSamplesAllRequestsThrottled(t *testing.T) {
	fn := lambda.Function{Name: "saturated", ThrottlesPerMin: 5, ZeroValuedSamples: true}
	b := tick(t, build(t, &lambda.Config{Functions: []lambda.Function{fn}}), time.Unix(1700000000, 0))
	near(t, "throttles", stats(b, "throttles"), map[string]float64{"sum": 5, "sample_count": 5, "average": 1, "maximum": 1, "minimum": 1})
	// Throttled requests still carry a 0 Errors sample.
	near(t, "errors", stats(b, "errors"), map[string]float64{"sum": 0, "sample_count": 5, "average": 0, "maximum": 0, "minimum": 0})
	if got := value(b, "aws_lambda_invocations_sum"); got != -1 {
		t.Fatal("throttled requests are not invocations", got)
	}
}

func TestZeroValuedSamplesIdleFunctionEmitsNothing(t *testing.T) {
	fn := lambda.Function{Name: "idle", ZeroValuedSamples: true}
	b := tick(t, build(t, &lambda.Config{Functions: []lambda.Function{fn}}), time.Unix(1700000000, 0))
	for _, base := range []string{"throttles", "errors"} {
		if got := value(b, "aws_lambda_"+base+"_sum"); got != -1 {
			t.Fatal("no requests, no observation:", base, got)
		}
	}
}

// Default output is unchanged: the same traffic without the opt-in keeps event-only
// observations (Errors and Throttles only when an event occurs).
func TestDefaultKeepsEventOnlyObservations(t *testing.T) {
	quiet := lambda.Function{Name: "quiet", InvocationsPerMin: 6, DurationMS: 25}
	b := tick(t, build(t, &lambda.Config{Functions: []lambda.Function{quiet}}), time.Unix(1700000000, 0))
	for _, base := range []string{"throttles", "errors"} {
		if got := value(b, "aws_lambda_"+base+"_sum"); got != -1 {
			t.Fatal("default emitted a zero-valued observation:", base, got)
		}
	}
	busy := lambda.Function{Name: "worker", InvocationsPerMin: 12, DurationMS: 25, ErrorFraction: 0.25, ThrottlesPerMin: 2}
	b = tick(t, build(t, &lambda.Config{Functions: []lambda.Function{busy}}), time.Unix(1700000000, 0))
	near(t, "throttles", stats(b, "throttles"), map[string]float64{"sum": 2, "sample_count": 2, "average": 1, "maximum": 1, "minimum": 1})
	near(t, "errors", stats(b, "errors"), map[string]float64{"sum": 3, "sample_count": 3, "average": 1, "maximum": 1, "minimum": 1})
}
