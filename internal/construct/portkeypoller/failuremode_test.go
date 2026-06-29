// SPDX-License-Identifier: AGPL-3.0-only

package portkeypoller_test

// failuremode_test.go — TDD tests for the portkey_scrape_degraded failure mode.
//
// Tests assert that with the failure mode active at full intensity (scope "PRD"):
//   - portkey_api_requests_total 5xx share rises (~15×)
//   - portkey_api_error_rate rises (~60×, clamped to 1)
//   - portkey_api_latency_seconds rises (~3×)
//   - poller_window_lag_seconds rises (~3×)
//   - poller_api_errors_total > 0 (active counter increments)
//
// Baseline: same construct, same now, no failure mode active (default coretest.World).
// Failure: separate coretest.World with shape built from a scheduled incident covering now.

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/portkeypoller"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// failNow is a Wednesday business-hours time used as the anchor for failure-mode tests.
var failNow = time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

// buildEnvScoped builds a portkeypoller construct env-scoped to envName "PRD".
func buildEnvScoped(t *testing.T) core.Construct {
	t.Helper()
	c, err := portkeypoller.Build(&portkeypoller.Config{
		// Single model and use-case for predictable label matching.
		Models:   []string{"gpt-4o"},
		UseCases: []string{"assistant"},
	}, &fixture.Set{
		Seed: "failtest",
		Env:  &fixture.Env{Name: "PRD", Weight: 1.0},
	})
	if err != nil {
		t.Fatalf("Build (env-scoped): %v", err)
	}
	return c
}

// incidentWorld builds a *core.World with the portkey_scrape_degraded failure mode
// scheduled at full intensity (1.0) for scope "PRD", covering failNow.
func incidentWorld(mc *coretest.MetricCapture) *core.World {
	// Format: mode@<RFC3339>/dur#intensity@scope
	incident := "portkey_scrape_degraded@2026-06-18T10:00:00Z/4h#1.0@PRD"
	eng := shape.New("", []string{incident})
	w := &core.World{Shape: eng}
	if mc != nil {
		w.Metrics = mc
	}
	return w
}

// tickWithWorld calls Tick once at failNow into the given World.
func tickWithWorld(t *testing.T, c core.Construct, w *core.World) {
	t.Helper()
	if err := c.Tick(context.Background(), failNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
}

// sum5xxRequests sums portkey_api_requests_total where status_class=5xx.
func sum5xxRequests(mc *coretest.MetricCapture) float64 {
	var total float64
	for _, s := range mc.Find("portkey_api_requests_total") {
		if s.Labels["status_class"] == "5xx" {
			total += s.Value
		}
	}
	return total
}

// sumMetric sums all values for the named metric.
func sumMetric(mc *coretest.MetricCapture, name string) float64 {
	var total float64
	for _, s := range mc.Find(name) {
		total += s.Value
	}
	return total
}

// firstValue returns the first observed value for the named metric (any labels).
func firstValue(mc *coretest.MetricCapture, name string) float64 {
	series := mc.Find(name)
	if len(series) == 0 {
		return 0
	}
	return series[0].Value
}

// TestFailureModeDeclared verifies that FailureModes contains portkey_scrape_degraded.
func TestFailureModeDeclared(t *testing.T) {
	reg := portkeypoller.Registration()
	var found bool
	for _, m := range reg.FailureModes {
		if m.Name == "portkey_scrape_degraded" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Registration().FailureModes must include portkey_scrape_degraded")
	}
}

// TestFailureMode_5xxShareRises asserts that portkey_api_requests_total 5xx series
// grows substantially when portkey_scrape_degraded is active at full intensity.
func TestFailureMode_5xxShareRises(t *testing.T) {
	// Baseline: fresh construct, default (no failure) world.
	cBase := buildEnvScoped(t)
	mcBase := &coretest.MetricCapture{}
	tickWithWorld(t, cBase, coretest.World(mcBase, nil, nil))
	base5xx := sum5xxRequests(mcBase)
	if base5xx == 0 {
		t.Fatal("baseline: portkey_api_requests_total 5xx is 0 — metric not emitted")
	}

	// Active: fresh construct (independent state), incident world.
	cFail := buildEnvScoped(t)
	mcFail := &coretest.MetricCapture{}
	tickWithWorld(t, cFail, incidentWorld(mcFail))
	fail5xx := sum5xxRequests(mcFail)
	if fail5xx == 0 {
		t.Fatal("failure mode active: portkey_api_requests_total 5xx is 0 — metric not emitted")
	}

	// errF = 1 + (15-1)*1.0 = 15; so fail5xx should be ~15× base5xx.
	// Allow generous tolerance (noise, rng): assert fail5xx >= 5× base5xx.
	ratio := fail5xx / base5xx
	if ratio < 5.0 {
		t.Errorf("portkey_api_requests_total 5xx: failure ratio=%.2f (fail=%g, base=%g) — want >= 5.0 (~15× at full intensity)",
			ratio, fail5xx, base5xx)
	}
}

// TestFailureMode_ErrorRateRises asserts that portkey_api_error_rate rises with the mode.
func TestFailureMode_ErrorRateRises(t *testing.T) {
	cBase := buildEnvScoped(t)
	mcBase := &coretest.MetricCapture{}
	tickWithWorld(t, cBase, coretest.World(mcBase, nil, nil))
	baseER := sumMetric(mcBase, "portkey_api_error_rate")

	cFail := buildEnvScoped(t)
	mcFail := &coretest.MetricCapture{}
	tickWithWorld(t, cFail, incidentWorld(mcFail))
	failER := sumMetric(mcFail, "portkey_api_error_rate")

	if failER == 0 && baseER == 0 {
		t.Fatal("both baseline and failure error_rate are 0 — metric not emitted")
	}

	// rateF = 1 + (60-1)*1.0 = 60; clamped to [0,1] means failER should reach 1.0.
	// Assert failER > baseER or failER reached maximum (1.0 per use-case).
	if failER <= baseER {
		t.Errorf("portkey_api_error_rate: failure (%g) should be > baseline (%g) when mode active", failER, baseER)
	}
}

// TestFailureMode_LatencyRises asserts that portkey_api_latency_seconds rises with the mode.
func TestFailureMode_LatencyRises(t *testing.T) {
	cBase := buildEnvScoped(t)
	mcBase := &coretest.MetricCapture{}
	tickWithWorld(t, cBase, coretest.World(mcBase, nil, nil))
	baseLat := sumMetric(mcBase, "portkey_api_latency_seconds")

	cFail := buildEnvScoped(t)
	mcFail := &coretest.MetricCapture{}
	tickWithWorld(t, cFail, incidentWorld(mcFail))
	failLat := sumMetric(mcFail, "portkey_api_latency_seconds")

	if baseLat == 0 {
		t.Fatal("baseline: portkey_api_latency_seconds is 0 — metric not emitted")
	}
	if failLat == 0 {
		t.Fatal("failure mode active: portkey_api_latency_seconds is 0 — metric not emitted")
	}

	// latF = 1 + (3-1)*1.0 = 3; assert failLat >= 2× baseLat.
	ratio := failLat / baseLat
	if ratio < 2.0 {
		t.Errorf("portkey_api_latency_seconds: failure ratio=%.2f (fail=%g, base=%g) — want >= 2.0 (~3× at full intensity)",
			ratio, failLat, baseLat)
	}
}

// TestFailureMode_WindowLagRises asserts that poller_window_lag_seconds rises with the mode.
func TestFailureMode_WindowLagRises(t *testing.T) {
	cBase := buildEnvScoped(t)
	mcBase := &coretest.MetricCapture{}
	tickWithWorld(t, cBase, coretest.World(mcBase, nil, nil))
	baseLag := firstValue(mcBase, "poller_window_lag_seconds")

	cFail := buildEnvScoped(t)
	mcFail := &coretest.MetricCapture{}
	tickWithWorld(t, cFail, incidentWorld(mcFail))
	failLag := firstValue(mcFail, "poller_window_lag_seconds")

	if baseLag == 0 {
		t.Fatal("baseline: poller_window_lag_seconds is 0 — metric not emitted")
	}
	if failLag == 0 {
		t.Fatal("failure mode active: poller_window_lag_seconds is 0 — metric not emitted")
	}

	// lagF = 1 + (3-1)*1.0 = 3; assert failLag >= 2× baseLag.
	ratio := failLag / baseLag
	if ratio < 2.0 {
		t.Errorf("poller_window_lag_seconds: failure ratio=%.2f (fail=%g, base=%g) — want >= 2.0 (~3× at full intensity)",
			ratio, failLag, baseLag)
	}
}

// TestFailureMode_PollerErrorsIncrement asserts that poller_api_errors_total is > 0
// when portkey_scrape_degraded is active, and == 0 (or stays at baseline) when inactive.
func TestFailureMode_PollerErrorsIncrement(t *testing.T) {
	// Baseline: no failure mode → poller_api_errors_total should be 0 (at tick 1).
	cBase := buildEnvScoped(t)
	mcBase := &coretest.MetricCapture{}
	tickWithWorld(t, cBase, coretest.World(mcBase, nil, nil))
	baseErr := firstValue(mcBase, "poller_api_errors_total")
	if baseErr != 0 {
		t.Errorf("baseline: poller_api_errors_total=%g, expected 0 (no errors in steady state)", baseErr)
	}

	// Active: failure mode → poller_api_errors_total must be > 0.
	cFail := buildEnvScoped(t)
	mcFail := &coretest.MetricCapture{}
	tickWithWorld(t, cFail, incidentWorld(mcFail))
	failErr := firstValue(mcFail, "poller_api_errors_total")
	if failErr <= 0 {
		t.Errorf("failure mode active: poller_api_errors_total=%g, expected > 0", failErr)
	}
}
