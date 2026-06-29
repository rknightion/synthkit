// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
)

// meanReqDurMs mints n requests from a workload built with the given traffic latency p95 and returns
// the mean request duration in ms. Uses the shared test World's shape engine for the random draws.
func meanReqDurMs(t *testing.T, p95 float64, n int) float64 {
	t.Helper()
	cfg := &Config{
		Traffic: Traffic{OffPeakRPS: 20, PeakRPS: 50, RequestLatencyP95Ms: p95},
		Services: []ServiceNode{
			{Name: "web-fe", Type: "frontend", Entry: true, Calls: []string{"backend"}},
			{Name: "backend", Type: "web"},
		},
	}
	w := buildApp(t, cfg)
	world := coretest.World(&coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{})
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	var sum time.Duration
	for i := 0; i < n; i++ {
		sum += w.m.mintOne(now, world.Shape).Duration
	}
	return float64(sum) / float64(n) / float64(time.Millisecond)
}

// TestRequestLatencyConfigScalesDuration: the blueprint knob lifts request latency from HTTP-fast
// (default 200ms p95) to LLM-realistic seconds, so http_server_request_duration + the in-process
// gen_ai span windows reflect real LLM-wait time.
func TestRequestLatencyConfigScalesDuration(t *testing.T) {
	const n = 400
	fast := meanReqDurMs(t, 0, n)    // default → 200ms p95 (lognormal median ~88ms)
	slow := meanReqDurMs(t, 9000, n) // LLM-realistic → 9s p95 (median ~4s)

	if fast > 1000 {
		t.Errorf("default-latency mean = %.0fms, want sub-second (HTTP-fast)", fast)
	}
	if slow < 2000 {
		t.Errorf("LLM-latency mean = %.0fms, want multi-second (>=2000)", slow)
	}
	if slow < fast*10 {
		t.Errorf("p95=9000 mean (%.0fms) should be >>10x the default (%.0fms)", slow, fast)
	}
}
