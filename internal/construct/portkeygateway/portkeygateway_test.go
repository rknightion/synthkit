// SPDX-License-Identifier: AGPL-3.0-only

package portkeygateway_test

// portkeygateway_test.go — contract tests for the portkey_gateway construct.
//
// (a) Inventory: representative series from each metric family are present.
// (b) Types: request_count is a counter (monotone across ticks); llm_token_sum is a
//     plain gauge (no _bucket/_sum/_count; does NOT accumulate across ticks).
// (c) Histograms expand to _bucket/_sum/_count (BASE name passed to Observe).
// (d) cacheStatus ∈ {hit, miss, disabled, error} — never simple_hit/semantic_hit.
// (e) llm_token_sum and llm_cost_sum are plain gauges (no histogram suffixes).
// (f) Sub-signal gating: "gateway" and "runtime" can be independently enabled.
// (g) Interface conformance: Kind / Signals / Interval.
// (h) No blueprint label on any emitted series (ScopeSubstrate).
// (i) Nil writer is safe (no panic).

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/portkeygateway"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// testNow is a fixed mid-business-hours time (Europe/Zurich 14:00).
var testNow = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC) // noon UTC = 14:00 Zurich

// buildDefault builds a portkey_gateway construct with default config.
func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	cfg := &portkeygateway.Config{}
	fx := &fixture.Set{Seed: "test"}
	c, err := portkeygateway.Build(cfg, fx)
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

// seriesPresent returns true if any series with the exact name exists.
func seriesPresent(mc *coretest.MetricCapture, name string) bool {
	return len(mc.Find(name)) > 0
}

// histoPresent returns true if the histogram base name expanded to _bucket/_sum/_count.
func histoPresent(mc *coretest.MetricCapture, base string) bool {
	return seriesPresent(mc, base+"_bucket") &&
		seriesPresent(mc, base+"_sum") &&
		seriesPresent(mc, base+"_count")
}

// ── (g) Interface conformance ─────────────────────────────────────────────────

func TestInterfaceConformance(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "portkey_gateway" {
		t.Errorf("Kind()=%q want %q", c.Kind(), "portkey_gateway")
	}
	sigs := c.Signals()
	hasMetrics := false
	for _, s := range sigs {
		if s == core.Metrics {
			hasMetrics = true
		}
	}
	if !hasMetrics {
		t.Error("Signals(): Metrics missing")
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v want 60s", c.Interval())
	}
}

// ── (a) Series inventory ──────────────────────────────────────────────────────

var gatewayInventory = []string{
	"request_count",
	"http_request_duration_seconds",                  // histogram: check as _bucket below
	"llm_request_duration_milliseconds",              // histogram
	"portkey_request_duration_milliseconds",          // histogram
	"portkey_processing_time_excluding_last_byte_ms", // histogram
	"llm_last_byte_diff_duration_milliseconds",       // histogram
	"authentication_duration_milliseconds",           // histogram
	"api_key_rate_limit_check_duration_milliseconds", // histogram
	"pre_request_processing_duration_milliseconds",   // histogram
	"post_request_processing_duration_milliseconds",  // histogram
	"llm_cache_processing_duration_milliseconds",     // histogram
	"grpc_req_conversion_duration_milliseconds",      // histogram
	"llm_token_sum",
	"llm_cost_sum",
}

var runtimeInventory = []string{
	"node_process_cpu_user_seconds_total",
	"node_process_resident_memory_bytes",
	"node_eventloop_lag_seconds",
	"node_gc_duration_seconds", // histogram
}

func TestGatewayInventory(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)
	for _, name := range gatewayInventory {
		present := seriesPresent(mc, name) || histoPresent(mc, name)
		if !present {
			t.Errorf("gateway: missing series (or histogram) %q", name)
		}
	}
}

func TestRuntimeInventory(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)
	for _, name := range runtimeInventory {
		present := seriesPresent(mc, name) || histoPresent(mc, name)
		if !present {
			t.Errorf("runtime: missing series (or histogram) %q", name)
		}
	}
}

// ── (c) Histograms expand to _bucket/_sum/_count ──────────────────────────────

func TestHistogramExpands(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	histogramBases := []string{
		"http_request_duration_seconds",
		"llm_request_duration_milliseconds",
		"portkey_request_duration_milliseconds",
		"grpc_req_conversion_duration_milliseconds",
		"node_gc_duration_seconds",
	}
	for _, base := range histogramBases {
		if !histoPresent(mc, base) {
			t.Errorf("histogram %q: _bucket/_sum/_count not all present", base)
		}
		// Base name must NOT appear as a plain series.
		if seriesPresent(mc, base) {
			t.Errorf("histogram %q: plain base name present — Observe must pass BASE name only", base)
		}
	}
}

// TestHTTPRequestDurationBucketCount verifies that http_request_duration_seconds uses
// the 19 explicit buckets from signals/portkey.md → 20 _bucket series (19 finite + +Inf).
func TestHTTPRequestDurationBucketCount(t *testing.T) {
	cfg := &portkeygateway.Config{
		Models:     []string{"gpt-4o"},
		Providers:  []string{"azure-openai"},
		SubSignals: []string{"gateway"},
	}
	c, err := portkeygateway.Build(cfg, &fixture.Set{Seed: "test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	bucketSeries := mc.Find("http_request_duration_seconds_bucket")
	if len(bucketSeries) == 0 {
		t.Fatal("no _bucket series for http_request_duration_seconds")
	}
	// Count unique le values for the first label combo.
	leCounts := map[string]bool{}
	first := bucketSeries[0]
	for _, s := range bucketSeries {
		if s.Labels["model"] == first.Labels["model"] && s.Labels["provider"] == first.Labels["provider"] {
			leCounts[s.Labels["le"]] = true
		}
	}
	// signals/portkey.md: 19 finite bounds → 20 with +Inf.
	const wantBounds = 20
	if len(leCounts) != wantBounds {
		t.Errorf("http_request_duration_seconds: got %d unique le values, want %d (19 finite + +Inf)", len(leCounts), wantBounds)
	}
}

// TestLLMRequestDurationBucketCount verifies that llm_request_duration_milliseconds uses
// the 22 explicit buckets from signals/portkey.md → 23 _bucket series (22 finite + +Inf).
func TestLLMRequestDurationBucketCount(t *testing.T) {
	cfg := &portkeygateway.Config{
		Models:     []string{"gpt-4o"},
		Providers:  []string{"azure-openai"},
		SubSignals: []string{"gateway"},
	}
	c, err := portkeygateway.Build(cfg, &fixture.Set{Seed: "test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	bucketSeries := mc.Find("llm_request_duration_milliseconds_bucket")
	if len(bucketSeries) == 0 {
		t.Fatal("no _bucket series for llm_request_duration_milliseconds")
	}
	leCounts := map[string]bool{}
	first := bucketSeries[0]
	for _, s := range bucketSeries {
		if s.Labels["model"] == first.Labels["model"] && s.Labels["provider"] == first.Labels["provider"] {
			leCounts[s.Labels["le"]] = true
		}
	}
	// signals/portkey.md: 22 finite bounds → 23 with +Inf.
	const wantBounds = 23
	if len(leCounts) != wantBounds {
		t.Errorf("llm_request_duration_milliseconds: got %d unique le values, want %d (22 finite + +Inf)", len(leCounts), wantBounds)
	}
}

// ── (d) cacheStatus constraint ────────────────────────────────────────────────

var validCacheStatuses = map[string]bool{
	"hit": true, "miss": true, "disabled": true, "error": true,
}

func TestCacheStatusValuesConstrained(t *testing.T) {
	// Use 4 providers and 4 models to exercise all cacheStatus index paths.
	cfg := &portkeygateway.Config{
		Models:     []string{"gpt-4o", "gpt-4-turbo", "gpt-3.5-turbo", "text-embedding-3-small"},
		Providers:  []string{"azure-openai", "openai", "anthropic", "cohere"},
		SubSignals: []string{"gateway"},
	}
	c, err := portkeygateway.Build(cfg, &fixture.Set{Seed: "test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	// Check every series that has a cacheStatus label.
	for _, s := range mc.All() {
		cs, ok := s.Labels["cacheStatus"]
		if !ok {
			continue
		}
		if !validCacheStatuses[cs] {
			t.Errorf("series %q: cacheStatus=%q is not in {hit,miss,disabled,error}", s.Name, cs)
		}
	}

	// Verify at least 2 distinct cacheStatus values are emitted across the full spread.
	seen := map[string]bool{}
	for _, s := range mc.All() {
		if cs, ok := s.Labels["cacheStatus"]; ok {
			seen[cs] = true
		}
	}
	if len(seen) < 2 {
		t.Errorf("expected ≥2 distinct cacheStatus values, got %v", seen)
	}
}

// ── (e) llm_token_sum and llm_cost_sum are plain gauges ──────────────────────

func TestLLMTokenSumIsPlainGauge(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	// Plain gauge: must be present, no histogram suffixes.
	if !seriesPresent(mc, "llm_token_sum") {
		t.Error("llm_token_sum: plain gauge series absent — expected state.Set emission")
	}
	if seriesPresent(mc, "llm_token_sum_bucket") {
		t.Error("llm_token_sum: _bucket present — must be plain gauge (state.Set), not histogram")
	}
	if seriesPresent(mc, "llm_token_sum_sum") {
		t.Error("llm_token_sum: _sum present — must be plain gauge (state.Set), not histogram")
	}
	if seriesPresent(mc, "llm_token_sum_count") {
		t.Error("llm_token_sum: _count present — must be plain gauge (state.Set), not histogram")
	}
}

func TestLLMCostSumIsPlainGauge(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	if !seriesPresent(mc, "llm_cost_sum") {
		t.Error("llm_cost_sum: plain gauge series absent — expected state.Set emission")
	}
	if seriesPresent(mc, "llm_cost_sum_bucket") {
		t.Error("llm_cost_sum: _bucket present — must be plain gauge (state.Set), not histogram")
	}
}

// TestLLMTokenSumAccumulates verifies that llm_token_sum is a CUMULATIVE gauge that grows
// monotonically across ticks (state.Add) — at a roughly-constant business factor tick2 ≈ 2× tick1.
// It is queried with delta(), never rate(); a non-accumulating value (state.Set) would make
// delta() read ~0, which is the bug this guards against.
func TestLLMTokenSumAccumulates(t *testing.T) {
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

	s1 := mc1.Find("llm_token_sum")
	s2 := mc2.Find("llm_token_sum")
	if len(s1) == 0 || len(s2) == 0 {
		t.Fatalf("llm_token_sum not found (tick1=%d tick2=%d)", len(s1), len(s2))
	}

	// Cumulative gauge: tick2 must be strictly greater than tick1 (a running sum grows). Equality
	// would mean state.Set overwrote each tick — the bug that made delta()/the spend KPI read 0.
	for _, a := range s1 {
		for _, b := range s2 {
			if a.Labels["model"] == b.Labels["model"] && a.Labels["provider"] == b.Labels["provider"] {
				if b.Value <= a.Value {
					t.Errorf("llm_token_sum must accumulate (cumulative gauge): tick1=%.2f tick2=%.2f (not growing — looks like state.Set)",
						a.Value, b.Value)
				}
			}
		}
	}
}

// ── (b) request_count is a counter (monotone across ticks) ───────────────────

func TestRequestCountIsMonotone(t *testing.T) {
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

	s1 := mc1.Find("request_count")
	s2 := mc2.Find("request_count")
	if len(s1) == 0 || len(s2) == 0 {
		t.Fatalf("request_count not found (tick1=%d tick2=%d)", len(s1), len(s2))
	}

	// Build label-sig → value map from tick 1.
	prev := make(map[string]float64, len(s1))
	for _, s := range s1 {
		prev[labelSig(s.Labels)] = s.Value
	}
	for _, s := range s2 {
		sig := labelSig(s.Labels)
		v1, ok := prev[sig]
		if !ok {
			continue
		}
		if s.Value <= v1 {
			t.Errorf("request_count NOT monotone: tick1=%.2f tick2=%.2f labels=%v", v1, s.Value, s.Labels)
		}
	}
}

// ── (f) Sub-signal gating ─────────────────────────────────────────────────────

func TestGatewayOnlyGating(t *testing.T) {
	cfg := &portkeygateway.Config{SubSignals: []string{"gateway"}}
	c, err := portkeygateway.Build(cfg, &fixture.Set{Seed: "test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	// Gateway series must be present.
	if !seriesPresent(mc, "request_count") {
		t.Error("gateway-only: request_count absent")
	}
	// Runtime series must be absent.
	if seriesPresent(mc, "node_process_cpu_user_seconds_total") {
		t.Error("gateway-only: node_process_cpu_user_seconds_total present but runtime sub-signal disabled")
	}
}

func TestRuntimeOnlyGating(t *testing.T) {
	cfg := &portkeygateway.Config{SubSignals: []string{"runtime"}}
	c, err := portkeygateway.Build(cfg, &fixture.Set{Seed: "test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	// Runtime series must be present.
	if !seriesPresent(mc, "node_process_cpu_user_seconds_total") {
		t.Error("runtime-only: node_process_cpu_user_seconds_total absent")
	}
	// Gateway series must be absent.
	if seriesPresent(mc, "request_count") {
		t.Error("runtime-only: request_count present but gateway sub-signal disabled")
	}
}

func TestEmptySubSignalsDefaultsToAll(t *testing.T) {
	cfg := &portkeygateway.Config{} // zero — defaults apply: both families
	c, err := portkeygateway.Build(cfg, &fixture.Set{Seed: "test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	// Both families must be present.
	if !seriesPresent(mc, "request_count") {
		t.Error("default sub_signals: request_count absent")
	}
	if !seriesPresent(mc, "node_process_cpu_user_seconds_total") {
		t.Error("default sub_signals: node_process_cpu_user_seconds_total absent")
	}
}

// ── (h) No blueprint label ────────────────────────────────────────────────────

func TestNoScopeSubstrateBlueprint(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)
	for _, s := range mc.All() {
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("series %q carries blueprint label — portkey_gateway is ScopeSubstrate (I21)", s.Name)
		}
	}
}

// ── (i) Nil writer is safe ────────────────────────────────────────────────────

func TestNilWriterSafe(t *testing.T) {
	c := buildDefault(t)
	w := coretest.World(nil, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick with nil Metrics writer: %v", err)
	}
}

// ── Model × Provider label spread ─────────────────────────────────────────────

func TestModelProviderSpread(t *testing.T) {
	cfg := &portkeygateway.Config{
		Models:    []string{"gpt-4o", "gpt-3.5-turbo"},
		Providers: []string{"azure-openai", "openai"},
	}
	c, err := portkeygateway.Build(cfg, &fixture.Set{Seed: "test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	models := map[string]bool{}
	providers := map[string]bool{}
	for _, s := range mc.Find("request_count") {
		if m, ok := s.Labels["model"]; ok {
			models[m] = true
		}
		if p, ok := s.Labels["provider"]; ok {
			providers[p] = true
		}
	}
	for _, want := range cfg.Models {
		if !models[want] {
			t.Errorf("request_count: model=%q not emitted", want)
		}
	}
	for _, want := range cfg.Providers {
		if !providers[want] {
			t.Errorf("request_count: provider=%q not emitted", want)
		}
	}
}

// ── Registration correctness ───────────────────────────────────────────────────

func TestRegistration(t *testing.T) {
	reg := portkeygateway.Registration()
	if reg.Kind != "portkey_gateway" {
		t.Errorf("Registration.Kind=%q want %q", reg.Kind, "portkey_gateway")
	}
	if reg.Scope != core.ScopeSubstrate {
		t.Errorf("Registration.Scope=%v want ScopeSubstrate", reg.Scope)
	}
	if reg.Group != core.GroupIntegration {
		t.Errorf("Registration.Group=%v want GroupIntegration", reg.Group)
	}
	if reg.NewConfig == nil {
		t.Error("Registration.NewConfig is nil")
	}
	if reg.Build == nil {
		t.Error("Registration.Build is nil")
	}
}

// ── Env-awareness (Spec 3 Task 4 — the recipe exemplar) ─────────────────────────

// tickAt ticks once at the given time and returns the capture.
func tickAt(t *testing.T, c core.Construct, at time.Time) *coretest.MetricCapture {
	t.Helper()
	mc := &coretest.MetricCapture{}
	if err := c.Tick(context.Background(), at, coretest.World(mc, nil, nil)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc
}

// totalRequestCount returns the SUM of all request_count samples in one tick. Summing (rather than
// picking s[0]) is order-independent: state.Collect emits series in map order, so s[0] was a random
// (model,code) combo — the old firstRequestCount made the magnitude comparison flaky. Both the
// aggregate and the env-scoped build emit the SAME combo set, so the total scales purely by the
// shape factor (BusinessFactor vs Factor), which is exactly what the magnitude-branch test asserts.
func totalRequestCount(t *testing.T, c core.Construct, at time.Time) float64 {
	t.Helper()
	s := tickAt(t, c, at).Find("request_count")
	if len(s) == 0 {
		t.Fatal("request_count not emitted")
	}
	var sum float64
	for _, x := range s {
		sum += x.Value
	}
	return sum
}

// When the fixture carries an Env, the env label = Env.Name (overriding the config default).
func TestEnvScopedStampsEnvLabel(t *testing.T) {
	fx := &fixture.Set{Seed: "s", Env: &fixture.Env{Name: "tst1", Weight: 0.3, NonProd: true}}
	c, err := portkeygateway.Build(&portkeygateway.Config{}, fx) // no Env in config
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	saw := false
	for _, s := range tickOnce(t, c).All() {
		if v, ok := s.Labels["env"]; ok {
			if v != "tst1" {
				t.Fatalf("series %q env=%q, want tst1 (from fixture)", s.Name, v)
			}
			saw = true
		}
	}
	if !saw {
		t.Fatal("no env-labelled series emitted")
	}
}

// Aggregate (no Env fixture) keeps the config/default env — back-compat (n-1).
func TestAggregateKeepsConfigEnv(t *testing.T) {
	c, err := portkeygateway.Build(&portkeygateway.Config{Env: "prod"}, &fixture.Set{Seed: "s"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, s := range tickOnce(t, c).All() {
		if v, ok := s.Labels["env"]; ok && v != "prod" {
			t.Fatalf("aggregate series %q env=%q, want prod (config)", s.Name, v)
		}
	}
}

// B1: the magnitude branch must differ on weekends. Aggregate uses BusinessFactor (weekend 0.3);
// an env-scoped non-prod env uses Factor with weekly=0.02 → strictly lower. (Weekdays are equal,
// so this Saturday case is the n-1 guard against a blanket BusinessFactor→Factor swap.)
func TestWeekendScalingBranch(t *testing.T) {
	sat := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC) // Saturday 14:00 Zurich
	agg, err := portkeygateway.Build(&portkeygateway.Config{}, &fixture.Set{Seed: "s"})
	if err != nil {
		t.Fatalf("Build agg: %v", err)
	}
	env, err := portkeygateway.Build(&portkeygateway.Config{}, &fixture.Set{Seed: "s", Env: &fixture.Env{Name: "dev", Weight: 1.0, NonProd: true}})
	if err != nil {
		t.Fatalf("Build env: %v", err)
	}
	aggVal := totalRequestCount(t, agg, sat)
	envVal := totalRequestCount(t, env, sat)
	if !(envVal < aggVal) {
		t.Fatalf("weekend: env-scoped non-prod total request_count (%.4f) must be < aggregate BusinessFactor total (%.4f)", envVal, aggVal)
	}
}

// ── Per-model volume differentiation ─────────────────────────────────────────
//
// gpt-4o-mini (VolumeWeight=6.0) should emit substantially more requests than
// o4-mini (VolumeWeight=2.0) over a single tick. We compare the summed
// request_count across all status codes for each model and assert that the
// higher-weight model emits at least 1.5× the lower-weight model (the ratio
// is 3× in weights; even with Wander/Noise the ordering must be preserved at
// the population level so we use a relaxed threshold).
func TestPerModelVolumeDifferentiation(t *testing.T) {
	cfg := &portkeygateway.Config{
		Models:     []string{"gpt-4o-mini", "o4-mini"},
		Providers:  []string{"azure-openai"},
		SubSignals: []string{"gateway"},
	}
	c, err := portkeygateway.Build(cfg, &fixture.Set{Seed: "test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	// Sum request_count values per model.
	totals := map[string]float64{}
	for _, s := range mc.Find("request_count") {
		if m, ok := s.Labels["model"]; ok {
			totals[m] += s.Value
		}
	}

	miniTotal, miniOK := totals["gpt-4o-mini"]
	o4Total, o4OK := totals["o4-mini"]
	if !miniOK || !o4OK {
		t.Fatalf("missing request_count for one or both models; got totals: %v", totals)
	}

	// gpt-4o-mini weight=6.0, o4-mini weight=2.0 → expect ≥1.5× difference.
	const minRatio = 1.5
	if miniTotal < o4Total*minRatio {
		t.Errorf("per-model differentiation: gpt-4o-mini total=%.2f, o4-mini total=%.2f; want gpt-4o-mini ≥%.1f× o4-mini",
			miniTotal, o4Total, minRatio)
	}
}

// ── labelSig is a local helper (avoids cross-package dep on state.LabelSig) ──

func labelSig(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	var sb []byte
	for _, k := range keys {
		sb = append(sb, k...)
		sb = append(sb, '=')
		sb = append(sb, m[k]...)
		sb = append(sb, ';')
	}
	return string(sb)
}
