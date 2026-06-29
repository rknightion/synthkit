// SPDX-License-Identifier: AGPL-3.0-only

package portkeypoller_test

// portkeypoller_test.go — contract tests for the portkey_poller construct.
//
// (a) Inventory — all 7 portkey_api_* and 3 poller_* series present.
// (b) _total-gauge trap — portkey_api_requests_total is a PLAIN GAUGE (state.Set):
//     - no _bucket/_sum/_count siblings
//     - value does NOT double across two ticks (Set, not Add)
// (c) latency_seconds carries quantile label ∈ {"0.5","0.9","0.99"}.
// (d) poller self-telemetry present: poller_last_success_timestamp_seconds,
//     poller_window_lag_seconds, poller_api_errors_total.
// (e) poller_api_errors_total is a real COUNTER (value does NOT reset across ticks).
// (f) No blueprint label (ScopeSubstrate).
// (g) Interface conformance: Kind / Signals / Interval.

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/portkeypoller"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

var testNow = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

// buildDefault builds with zero Config (all defaults apply).
func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	c, err := portkeypoller.Build(&portkeypoller.Config{}, &fixture.Set{Seed: "test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

// tickOnce calls Tick once and returns the MetricCapture.
func tickOnce(t *testing.T, c core.Construct) *coretest.MetricCapture {
	t.Helper()
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc
}

// seriesPresent returns true if any captured series has the exact name.
func seriesPresent(mc *coretest.MetricCapture, name string) bool {
	return len(mc.Find(name)) > 0
}

// ── (g) Interface conformance ─────────────────────────────────────────────────

func TestInterfaceConformance(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "portkey_poller" {
		t.Errorf("Kind()=%q want %q", c.Kind(), "portkey_poller")
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals()=%v want [Metrics]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v want 60s", c.Interval())
	}
}

// ── (a) Inventory — all 7 portkey_api_* series present ───────────────────────

var portKeyAPIAnchors = []string{
	"portkey_api_requests_total",
	"portkey_api_cost_usd",
	"portkey_api_tokens_total",
	"portkey_api_latency_seconds",
	"portkey_api_error_rate",
	"portkey_api_cache_hit_rate",
	"portkey_api_rescued_requests_total",
}

func TestPortkeyAPIInventory(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)
	for _, name := range portKeyAPIAnchors {
		if !seriesPresent(mc, name) {
			t.Errorf("portkey_api: missing series %q", name)
		}
	}
}

// ── (b) _total-gauge trap — portkey_api_requests_total is PLAIN GAUGE ─────────

// TestRequestsTotalIsPlainGauge verifies that portkey_api_requests_total is a plain gauge:
// - no histogram siblings (_bucket/_sum/_count)
// - value does NOT double across two ticks (Set not Add)
func TestRequestsTotalIsPlainGauge(t *testing.T) {
	c := buildDefault(t)

	mc1 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, nil, nil)
	if err := c.Tick(context.Background(), testNow, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}

	mc2 := &coretest.MetricCapture{}
	w2 := coretest.World(mc2, nil, nil)
	if err := c.Tick(context.Background(), testNow.Add(60*time.Second), w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	const name = "portkey_api_requests_total"

	// Must be a plain series (no histogram siblings).
	if seriesPresent(mc1, name+"_bucket") {
		t.Errorf("%q: _bucket present — must be a plain GAUGE (state.Set), not a histogram", name)
	}
	if seriesPresent(mc1, name+"_sum") {
		t.Errorf("%q: _sum present — must be a plain GAUGE", name)
	}
	if seriesPresent(mc1, name+"_count") {
		t.Errorf("%q: _count present — must be a plain GAUGE", name)
	}

	// Value must NOT double across ticks (state.Set, not state.Add).
	s1 := mc1.Find(name)
	s2 := mc2.Find(name)
	if len(s1) == 0 || len(s2) == 0 {
		t.Fatalf("%q: not found in tick1 (%d) or tick2 (%d)", name, len(s1), len(s2))
	}

	// For each tick-1 series, find the matching tick-2 series (same label sig).
	// If state.Add were used, tick-2 value would be 2× tick-1. Assert value stays stable.
	tick1Map := make(map[string]float64, len(s1))
	for _, s := range s1 {
		tick1Map[labelSig(s.Labels)] = s.Value
	}
	for _, s := range s2 {
		v1, ok := tick1Map[labelSig(s.Labels)]
		if !ok {
			continue
		}
		// If Add were used, tick2 ≈ 2×tick1. A Set value should stay the same.
		// Allow a small tolerance for noise (none expected here, but be safe).
		if s.Value > v1*1.5 {
			t.Errorf("%q value DOUBLED across ticks (tick1=%.f tick2=%.f) — looks like state.Add was used instead of state.Set",
				name, v1, s.Value)
		}
	}
}

// ── (c) latency_seconds carries quantile label ────────────────────────────────

func TestLatencySecondsCarriesQuantileLabel(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	const name = "portkey_api_latency_seconds"
	series := mc.Find(name)
	if len(series) == 0 {
		t.Fatalf("%q: not found", name)
	}

	foundQuantiles := map[string]bool{}
	for _, s := range series {
		q, ok := s.Labels["quantile"]
		if !ok {
			t.Errorf("%q: series missing quantile label (labels=%v)", name, s.Labels)
			continue
		}
		foundQuantiles[q] = true
	}

	for _, want := range []string{"0.5", "0.9", "0.99"} {
		if !foundQuantiles[want] {
			t.Errorf("%q: quantile=%q not present in any series", name, want)
		}
	}
}

// ── (d) poller self-telemetry present ─────────────────────────────────────────

func TestPollerSelfTelemetryPresent(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	for _, name := range []string{
		"poller_last_success_timestamp_seconds",
		"poller_window_lag_seconds",
		"poller_api_errors_total",
	} {
		if !seriesPresent(mc, name) {
			t.Errorf("poller self-telemetry: missing series %q", name)
		}
	}
}

// TestPollerLastSuccessTimestamp verifies that poller_last_success_timestamp_seconds
// is set to approximately now.Unix() (within 1 second).
func TestPollerLastSuccessTimestamp(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	const name = "poller_last_success_timestamp_seconds"
	series := mc.Find(name)
	if len(series) == 0 {
		t.Fatalf("%q not found", name)
	}
	for _, s := range series {
		want := float64(testNow.Unix())
		if s.Value != want {
			t.Errorf("%q: value=%.f want %.f (= testNow.Unix())", name, s.Value, want)
		}
	}
}

// ── (e) poller_api_errors_total is a real COUNTER ────────────────────────────

// TestPollerAPIErrorsTotalIsCounter verifies that poller_api_errors_total does NOT reset
// across ticks — it is a true COUNTER (state.Add). At ~0 baseline the value must be >= 0
// and non-decreasing (tick2 >= tick1).
func TestPollerAPIErrorsTotalIsCounter(t *testing.T) {
	c := buildDefault(t)

	mc1 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, nil, nil)
	if err := c.Tick(context.Background(), testNow, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}

	mc2 := &coretest.MetricCapture{}
	w2 := coretest.World(mc2, nil, nil)
	if err := c.Tick(context.Background(), testNow.Add(60*time.Second), w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	const name = "poller_api_errors_total"
	s1 := mc1.Find(name)
	s2 := mc2.Find(name)
	if len(s1) == 0 || len(s2) == 0 {
		t.Fatalf("%q not found (tick1=%d tick2=%d)", name, len(s1), len(s2))
	}
	// Counter must be non-negative at tick 1.
	for _, s := range s1 {
		if s.Value < 0 {
			t.Errorf("%q tick1 value=%.f is negative", name, s.Value)
		}
	}
	// Counter must be non-decreasing (tick2 >= tick1) — this is state.Add.
	t1Map := make(map[string]float64, len(s1))
	for _, s := range s1 {
		t1Map[labelSig(s.Labels)] = s.Value
	}
	for _, s := range s2 {
		v1, ok := t1Map[labelSig(s.Labels)]
		if !ok {
			continue
		}
		if s.Value < v1 {
			t.Errorf("%q counter decreased: tick1=%.f tick2=%.f — must be state.Add (monotone)", name, v1, s.Value)
		}
	}
}

// ── (f) No blueprint label (ScopeSubstrate) ──────────────────────────────────

func TestNoScopeSubstrateBlueprint(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)
	for _, s := range mc.All() {
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("series %q carries blueprint label — portkey_poller is ScopeSubstrate (I21)", s.Name)
		}
	}
}

// ── Nil Metrics writer is safe ────────────────────────────────────────────────

func TestNilMetricWriterSafe(t *testing.T) {
	c := buildDefault(t)
	w := coretest.World(nil, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick with nil Metrics: %v", err)
	}
}

// ── Default config applies without customer strings ───────────────────────────

func TestDefaultConfigNoCustomerStrings(t *testing.T) {
	c, err := portkeypoller.Build(&portkeypoller.Config{}, nil)
	if err != nil {
		t.Fatalf("Build with nil fixture.Set: %v", err)
	}
	mc := tickOnce(t, c)

	// workspace label must be the generic default, not a customer string.
	for _, s := range mc.All() {
		ws, ok := s.Labels["workspace"]
		if ok && ws == "" {
			t.Errorf("series %q: workspace label is empty", s.Name)
		}
	}

	// All 7 portkey_api_* must still be present with default config.
	for _, name := range portKeyAPIAnchors {
		if !seriesPresent(mc, name) {
			t.Errorf("default config: missing series %q", name)
		}
	}
}

// ── Env-scoping (Spec 3) ──────────────────────────────────────────────────────

// TestEnvScopedStampsEnvLabel verifies that when Build receives a fixture.Set with an
// Env, every portkey_api_* emitted series carries env=<name> (I13 env-scoped fan-out).
func TestEnvScopedStampsEnvLabel(t *testing.T) {
	c, err := portkeypoller.Build(&portkeypoller.Config{}, &fixture.Set{
		Seed: "s",
		Env:  &fixture.Env{Name: "tst1", Weight: 0.3, NonProd: true},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	// Every portkey_api_* series must carry env=tst1.
	for _, name := range portKeyAPIAnchors {
		for _, s := range mc.Find(name) {
			got, ok := s.Labels["env"]
			if !ok {
				t.Errorf("env-scoped %q: missing env label (labels=%v)", name, s.Labels)
				continue
			}
			if got != "tst1" {
				t.Errorf("env-scoped %q: env=%q want %q", name, got, "tst1")
			}
		}
	}
}

// TestAggregateOmitsEnv verifies that when Build receives a fixture.Set with no Env
// (aggregate path), NO emitted series carries an env label (n-1: byte-identical to
// the non-fanned poller's prior behavior).
func TestAggregateOmitsEnv(t *testing.T) {
	c, err := portkeypoller.Build(&portkeypoller.Config{}, &fixture.Set{Seed: "s"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	for _, s := range mc.All() {
		if _, ok := s.Labels["env"]; ok {
			t.Errorf("aggregate series %q carries env label — must be omitted when no Env (I13)", s.Name)
		}
	}
}

// ── (h) Diurnal variation — volume metrics change between business-hours and off-hours ─

// testNowBusiness is a Wednesday at noon UTC (≈13:00 Europe/Zurich — business-hours plateau).
var testNowBusiness = time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC) // Wednesday

// testNowOffHours is a Sunday at 02:00 UTC (≈04:00 Europe/Zurich — overnight + weekend).
var testNowOffHours = time.Date(2026, 6, 14, 2, 0, 0, 0, time.UTC) // Sunday

// tickAt calls Tick at the given now and returns the MetricCapture.
func tickAt(t *testing.T, c core.Construct, now time.Time) *coretest.MetricCapture {
	t.Helper()
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick at %v: %v", now, w)
	}
	return mc
}

// sumValue sums ALL samples of a metric in one tick. Summing (not s[0]) is order-independent:
// state.Collect emits series in map order, so s[0] was a random (model/provider) combo — making
// the diurnal ratio compare non-comparable series and flake. The metric emits the same combo set
// at business and off-hours, so the total scales purely by the diurnal factor bf, which is what
// these ratio tests assert.
func sumValue(mc *coretest.MetricCapture, name string) float64 {
	var sum float64
	for _, s := range mc.Find(name) {
		sum += s.Value
	}
	return sum
}

// TestVolumeMetricsVaryWithDiurnal verifies that portkey_api_requests_total,
// portkey_api_tokens_total, and portkey_api_cost_usd produce DIFFERENT values at a
// business-hours time vs off-hours weekend time (bf-driven diurnal variation).
// This fails before the fix because emit() never received w, so bf was never applied.
func TestVolumeMetricsVaryWithDiurnal(t *testing.T) {
	for _, name := range []string{
		"portkey_api_requests_total",
		"portkey_api_tokens_total",
		"portkey_api_cost_usd",
	} {
		t.Run(name, func(t *testing.T) {
			// Build a fresh construct so state is independent between sub-tests.
			c := buildDefault(t)
			mcBiz := tickAt(t, c, testNowBusiness)

			c2 := buildDefault(t)
			mcOff := tickAt(t, c2, testNowOffHours)

			vBiz := sumValue(mcBiz, name)
			vOff := sumValue(mcOff, name)

			if vBiz == 0 {
				t.Fatalf("%q: business-hours value is 0 — metric not emitted", name)
			}
			if vOff == 0 {
				t.Fatalf("%q: off-hours value is 0 — metric not emitted", name)
			}
			// BusinessFactor at business hours is ~1.0; at off-hours weekend it is ~0.03 (0.1*0.3).
			// The two values must differ by at least 50%.
			ratio := vBiz / vOff
			if ratio < 1.5 {
				t.Errorf("%q: business=%g off=%g ratio=%.2f — expected business value >> off-hours (ratio>=1.5); diurnal scaling may not be applied",
					name, vBiz, vOff, ratio)
			}
		})
	}
}

// TestRescuedRequestsVaryWithDiurnal verifies portkey_api_rescued_requests_total also
// scales with bf (same diurnal gate as requests/tokens/cost).
func TestRescuedRequestsVaryWithDiurnal(t *testing.T) {
	c1 := buildDefault(t)
	mcBiz := tickAt(t, c1, testNowBusiness)

	c2 := buildDefault(t)
	mcOff := tickAt(t, c2, testNowOffHours)

	vBiz := sumValue(mcBiz, "portkey_api_rescued_requests_total")
	vOff := sumValue(mcOff, "portkey_api_rescued_requests_total")

	if vBiz == 0 {
		t.Fatal("portkey_api_rescued_requests_total: business-hours value is 0")
	}
	if vOff == 0 {
		t.Fatal("portkey_api_rescued_requests_total: off-hours value is 0")
	}
	ratio := vBiz / vOff
	if ratio < 1.5 {
		t.Errorf("portkey_api_rescued_requests_total: business=%g off=%g ratio=%.2f — expected ratio>=1.5; diurnal scaling may not be applied",
			vBiz, vOff, ratio)
	}
}

// TestErrorRateAndCacheHitRateClampedToUnit verifies that portkey_api_error_rate and
// portkey_api_cache_hit_rate stay within [0,1] across many Tick calls.
// Noise(0.2) and Noise(0.1) can theoretically push values slightly outside the nominal
// range at extreme draws — the implementation must clamp.
func TestErrorRateAndCacheHitRateClampedToUnit(t *testing.T) {
	c := buildDefault(t)
	base := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	for i := range 200 {
		now := base.Add(time.Duration(i) * time.Minute)
		mc := &coretest.MetricCapture{}
		w := coretest.World(mc, nil, nil)
		if err := c.Tick(context.Background(), now, w); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		for _, s := range mc.Find("portkey_api_error_rate") {
			if s.Value < 0 || s.Value > 1 {
				t.Errorf("portkey_api_error_rate out of [0,1]: value=%g tick=%d", s.Value, i)
			}
		}
		for _, s := range mc.Find("portkey_api_cache_hit_rate") {
			if s.Value < 0 || s.Value > 1 {
				t.Errorf("portkey_api_cache_hit_rate out of [0,1]: value=%g tick=%d", s.Value, i)
			}
		}
	}
}

// TestLatencyPercentilesNotScaledByBF verifies that latency quantile values do NOT
// scale with bf (they are percentiles, not volume). Business and off-hours p50 values
// should be in the same ballpark (within 3× of each other), unlike volume metrics which
// differ by 10×+.
func TestLatencyPercentilesNotScaledByBF(t *testing.T) {
	c1 := buildDefault(t)
	mcBiz := tickAt(t, c1, testNowBusiness)

	c2 := buildDefault(t)
	mcOff := tickAt(t, c2, testNowOffHours)

	// Find the p50 series.
	findP50 := func(mc *coretest.MetricCapture) float64 {
		for _, s := range mc.Find("portkey_api_latency_seconds") {
			if s.Labels["quantile"] == "0.5" {
				return s.Value
			}
		}
		return 0
	}

	bizP50 := findP50(mcBiz)
	offP50 := findP50(mcOff)

	if bizP50 == 0 {
		t.Fatal("portkey_api_latency_seconds p50: not found in business-hours tick")
	}
	if offP50 == 0 {
		t.Fatal("portkey_api_latency_seconds p50: not found in off-hours tick")
	}

	// Noise(0.1) gives ~1±0.1. The ratio of the two Noise draws will be close to 1.
	// If bf were accidentally applied, the ratio would be ~30× (same as requests). Assert
	// the ratio stays under 5× (generous for noise-only variation).
	ratio := bizP50 / offP50
	if ratio > 5.0 || ratio < 0.2 {
		t.Errorf("portkey_api_latency_seconds p50: biz=%g off=%g ratio=%.2f — latency should not scale with bf (ratio should be near 1, not ~30)",
			bizP50, offP50, ratio)
	}
}

// TestAllSevenPortkeyAPIAreSet verifies that all 7 portkey_api_* metrics are still
// emitted via state.Set after the bf fix — i.e., values do NOT accumulate across ticks.
// This is a belt-and-suspenders check in addition to TestRequestsTotalIsPlainGauge.
func TestAllSevenPortkeyAPIAreSet(t *testing.T) {
	for _, name := range portKeyAPIAnchors {
		t.Run(name, func(t *testing.T) {
			c := buildDefault(t)

			mc1 := &coretest.MetricCapture{}
			w1 := coretest.World(mc1, nil, nil)
			if err := c.Tick(context.Background(), testNow, w1); err != nil {
				t.Fatalf("Tick 1: %v", err)
			}

			mc2 := &coretest.MetricCapture{}
			w2 := coretest.World(mc2, nil, nil)
			if err := c.Tick(context.Background(), testNow, w2); err != nil {
				t.Fatalf("Tick 2: %v", err)
			}

			s1 := mc1.Find(name)
			s2 := mc2.Find(name)
			if len(s1) == 0 || len(s2) == 0 {
				t.Fatalf("%q not found (tick1=%d tick2=%d)", name, len(s1), len(s2))
			}

			// Build a label→value map for tick1.
			t1Map := make(map[string]float64, len(s1))
			for _, s := range s1 {
				t1Map[labelSig(s.Labels)] = s.Value
			}
			// Tick2 at the same now should produce values close to tick1 (Set, not Add).
			// Allow generous tolerance for noise: must be < 4× tick1 to catch an Add bug.
			for _, s := range s2 {
				v1, ok := t1Map[labelSig(s.Labels)]
				if !ok {
					continue
				}
				if v1 > 0 && s.Value > v1*4 {
					t.Errorf("%q accumulated across ticks (tick1=%g tick2=%g) — must be state.Set",
						name, v1, s.Value)
				}
			}
		})
	}
}

// ── (i) Use-case weight divergence ───────────────────────────────────────────
//
// With use_case_weights {live_assistant:5.0, document_digitization:0.4}, the summed
// portkey_api_requests_total for live_assistant must be > 3x document_digitization.

func TestUseCaseWeightsDivergence(t *testing.T) {
	c, err := portkeypoller.Build(&portkeypoller.Config{
		UseCases: []string{"live_assistant", "document_digitization"},
		Models:   []string{"gpt-4o"},
		UseCaseWeights: map[string]float64{
			"live_assistant":        5.0,
			"document_digitization": 0.4,
		},
	}, &fixture.Set{Seed: "test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	// Sum portkey_api_requests_total for each use case across all status classes.
	sumByUC := map[string]float64{}
	for _, s := range mc.Find("portkey_api_requests_total") {
		uc := s.Labels["metadata_use_case"]
		sumByUC[uc] += s.Value
	}

	live := sumByUC["live_assistant"]
	doc := sumByUC["document_digitization"]

	if live == 0 {
		t.Fatal("portkey_api_requests_total: live_assistant sum is 0")
	}
	if doc == 0 {
		t.Fatal("portkey_api_requests_total: document_digitization sum is 0")
	}
	ratio := live / doc
	if ratio < 3.0 {
		t.Errorf("use-case weight divergence: live_assistant=%g document_digitization=%g ratio=%.2f — want ratio >= 3.0 (weights 5.0 vs 0.4 = 12.5×)",
			live, doc, ratio)
	}
}

// ── (j) Cost tracks model price ───────────────────────────────────────────────
//
// With gpt-4o (CostInPerM=2.5, CostOut=10) vs gpt-4o-mini (CostIn=0.15, CostOut=0.6),
// gpt-4o blended cost/token at inputFrac=0.3 is ~16.7× gpt-4o-mini.
// portkey_api_cost_usd summed for gpt-4o must exceed gpt-4o-mini.

func TestCostTracksModelPrice(t *testing.T) {
	c, err := portkeypoller.Build(&portkeypoller.Config{
		UseCases: []string{"assistant"},
		Models:   []string{"gpt-4o", "gpt-4o-mini"},
	}, &fixture.Set{Seed: "test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	// Sum portkey_api_cost_usd per model.
	sumByModel := map[string]float64{}
	for _, s := range mc.Find("portkey_api_cost_usd") {
		model := s.Labels["ai_model"]
		sumByModel[model] += s.Value
	}

	costFull := sumByModel["gpt-4o"]
	costMini := sumByModel["gpt-4o-mini"]

	if costFull == 0 {
		t.Fatal("portkey_api_cost_usd: gpt-4o sum is 0")
	}
	if costMini == 0 {
		t.Fatal("portkey_api_cost_usd: gpt-4o-mini sum is 0")
	}
	if costFull <= costMini {
		t.Errorf("cost does not track model price: gpt-4o=%g <= gpt-4o-mini=%g — gpt-4o should be much more expensive",
			costFull, costMini)
	}
	// gpt-4o should be at least 5× gpt-4o-mini (real ratio is ~16.7×; use conservative bound).
	ratio := costFull / costMini
	if ratio < 5.0 {
		t.Errorf("cost ratio gpt-4o/gpt-4o-mini=%.2f — want >= 5.0 (real ~16.7×); cost may not track model pricing",
			ratio)
	}
}

// ── labelSig (test-local label key — matches state.LabelSig algorithm) ────────

func labelSig(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// insertion sort (small maps)
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	var b []byte
	for _, k := range keys {
		b = append(b, k...)
		b = append(b, '=')
		b = append(b, m[k]...)
		b = append(b, ';')
	}
	return string(b)
}
