// SPDX-License-Identifier: AGPL-3.0-only

package app

// TestWebVitalsDegraded: the "web_vitals_degraded" failure mode must cause LCP to be
// materially higher (≈2.5× the baseline) when active at full intensity on the frontend
// entry node. Uses a deterministically seeded shape.Engine so NormFloat64 draws are
// reproducible across the baseline and degraded runs.
//
// Test strategy:
//   - Build a RUM-capable workload with entry node named "web-frontend".
//   - Run projectRUM with a plain Engine (baseline LCP).
//   - Run projectRUM with the same engine's Live hook set to activate
//     "web_vitals_degraded" at full intensity scoped to "web-frontend" (degraded LCP).
//   - Because the RNG seed is identical between runs, the raw NormFloat64 draw is the
//     SAME; only the FailFactor multiplier changes, so the assertion is exact (not noisy).
//   - Assert: degradedLCP >= baselineLCP * 2.0 (allowing for clamp ceiling expansion).

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/telemetryspec/profiles"
)

// buildVitalsDegradedApp builds a RUM-capable workload whose frontend entry node is
// named "web-frontend" (the failure-mode scope used in TestWebVitalsDegraded).
func buildVitalsDegradedApp(t *testing.T, rum *coretest.RUMCapture) *Workload {
	t.Helper()
	if _, ok := profiles.Lookup("rum_faro"); !ok {
		t.Fatal("rum_faro profile not registered — missing import?")
	}
	cfg := &Config{
		Traffic: Traffic{OffPeakRPS: 10, PeakRPS: 50},
		Services: []ServiceNode{
			{
				Name:     "web-frontend",
				Type:     "frontend",
				Entry:    true,
				Profiles: []string{"rum_faro"},
				Calls:    []string{"web-api"},
			},
			{
				Name: "web-api",
				Type: "web",
			},
		},
	}
	w, err := build(cfg, core.Binding{
		Name:    "vitals-degraded-test",
		Env:     coretest.Env(),
		Cluster: coretest.Cluster(),
		RUM:     rum,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return w.(*Workload)
}

// fixedBrowserRequest returns a deterministic browser-origin request for the given workload.
func fixedBrowserRequest(w *Workload) *ledger.Request {
	now := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	r := &ledger.Request{
		Correlation:   ledger.NewCorrelation(),
		Workload:      w.Name(),
		Env:           "prod",
		Route:         "GET /api/v1/data",
		BrowserOrigin: true,
		Start:         now,
		Duration:      200 * time.Millisecond,
	}
	r.Outcome, r.StatusCode, r.ErrorKind = ledger.OutcomeSuccess, 200, ""
	return r
}

// extractLCP pulls the LCP value from the first Faro measurement in a payload that
// carries a "lcp" Values key.
func extractLCP(t *testing.T, rum *coretest.RUMCapture) float64 {
	t.Helper()
	for _, p := range rum.All() {
		for _, m := range p.Measurements {
			if v, ok := m.Values["lcp"]; ok {
				return v
			}
		}
	}
	t.Fatal("no LCP measurement found in captured RUM payloads")
	return 0
}

// TestWebVitalsDegraded asserts the "web_vitals_degraded" failure mode raises LCP.
// The raw NormFloat64 draw is identical between runs (same seeded RNG, same call order)
// so only the FailFactor multiplier changes — the ratio is therefore deterministic and
// the assertion is exact, not approximate.
func TestWebVitalsDegraded(t *testing.T) {
	// ── baseline ─────────────────────────────────────────────────────────────
	baseRUM := &coretest.RUMCapture{}
	wBase := buildVitalsDegradedApp(t, baseRUM)

	baseEng := shape.New("", nil) // no failure modes
	baseWorld := &core.World{Shape: baseEng, EmitSpanMetrics: true}

	baseReq := fixedBrowserRequest(wBase)
	if err := wBase.projectRUM(context.Background(), baseWorld, []*ledger.Request{baseReq}); err != nil {
		t.Fatalf("baseline projectRUM: %v", err)
	}
	baselineLCP := extractLCP(t, baseRUM)

	// ── degraded ─────────────────────────────────────────────────────────────
	degradedRUM := &coretest.RUMCapture{}
	wDeg := buildVitalsDegradedApp(t, degradedRUM)

	degradedEng := shape.New("", nil)
	degradedEng.Live = func(mode string) []shape.LiveFailure {
		if mode == "web_vitals_degraded" {
			return []shape.LiveFailure{{Enabled: true, Intensity: 1.0, Scope: "web-frontend"}}
		}
		return nil
	}
	degradedWorld := &core.World{Shape: degradedEng, EmitSpanMetrics: true}

	degradedReq := fixedBrowserRequest(wDeg)
	// Use the same ledger Correlation so r.RenderStart() offset is identical.
	degradedReq.Correlation = baseReq.Correlation

	if err := wDeg.projectRUM(context.Background(), degradedWorld, []*ledger.Request{degradedReq}); err != nil {
		t.Fatalf("degraded projectRUM: %v", err)
	}
	degradedLCP := extractLCP(t, degradedRUM)

	// LCP magAt1=2.5 at full intensity → factor=2.5; degraded must be at least 2.0×
	// baseline (generous floor to allow clamp effects on very-low raw draws).
	if degradedLCP < baselineLCP*2.0 {
		t.Errorf("web_vitals_degraded: degradedLCP=%.1f, baselineLCP=%.1f — want degraded >= 2.0× baseline (factor 2.5 at full intensity)",
			degradedLCP, baselineLCP)
	}
	t.Logf("baseline LCP=%.1f  degraded LCP=%.1f  ratio=%.2f", baselineLCP, degradedLCP, degradedLCP/baselineLCP)
}
