// SPDX-License-Identifier: AGPL-3.0-only

package cloudflare_test

// cloudflare_test.go — contract tests for the cloudflare construct.
//
// (a) Exact metric inventory: all 14 zone families + 4 tunnel families present.
// (b) zone label == cfg.Zone on every zone series.
// (c) Colocation series per configured colos — one entry per colo for both
//     cloudflare_zone_colocation_requests_total and cloudflare_zone_colocation_visits.
// (d) tunnel_id values are deterministic from fx.Seed (same seed = same IDs).
// (e) Counter behaviour: zone series are cumulative (second tick > first tick value).
// (f) Tunnel gauges do NOT accumulate across ticks (Set semantics — value stays stable).
// (g) Label keys per extract §3.4 — zone base, tunnel base, connector base.
// (h) Interface conformance: Kind / Signals / Interval.
// (i) Build errors: empty zone, wrong cfg type.
// (j) Default colocations applied when none configured.
// (k) Default tunnel applied when none configured.

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/cloudflare"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// ── helpers ──────────────────────────────────────────────────────────────────

const (
	testZone    = "example.com"
	testAccount = "cf-test-acct"
	testSeed    = "test-blueprint"
)

var testColocations = []string{"IAD", "FRA"}

var testTunnels = []cloudflare.TunnelConfig{
	{Name: "example.com-tunnel"},
}

// makeConfig returns a standard test Config.
func makeConfig() *cloudflare.Config {
	return &cloudflare.Config{
		Zone:        testZone,
		Account:     testAccount,
		Colocations: testColocations,
		Tunnels:     testTunnels,
	}
}

// buildConstruct builds a Construct from cfg and the standard test seed.
func buildConstruct(t *testing.T, cfg *cloudflare.Config) core.Construct {
	t.Helper()
	fx := &fixture.Set{Seed: testSeed}
	c, err := cloudflare.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

// runOnce ticks the construct once and returns the captured series.
func runOnce(t *testing.T, c core.Construct) *coretest.MetricCapture {
	t.Helper()
	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	now := time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return cap
}

// runTwice ticks the construct twice and returns both captures.
func runTwice(t *testing.T, c core.Construct) (*coretest.MetricCapture, *coretest.MetricCapture) {
	t.Helper()
	cap1 := &coretest.MetricCapture{}
	cap2 := &coretest.MetricCapture{}
	w1 := coretest.World(cap1, nil, nil)
	w2 := coretest.World(cap2, nil, nil)
	now1 := time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)
	now2 := now1.Add(60 * time.Second)
	if err := c.Tick(context.Background(), now1, w1); err != nil {
		t.Fatalf("Tick1: %v", err)
	}
	if err := c.Tick(context.Background(), now2, w2); err != nil {
		t.Fatalf("Tick2: %v", err)
	}
	return cap1, cap2
}

// ── (a) Exact inventory ───────────────────────────────────────────────────────

var expectedZoneMetrics = []string{
	"cloudflare_zone_requests_total",
	"cloudflare_zone_requests_cached",
	"cloudflare_zone_requests_status",
	"cloudflare_zone_requests_country",
	"cloudflare_zone_bandwidth_total",
	"cloudflare_zone_bandwidth_cached",
	"cloudflare_zone_threats_total",
	"cloudflare_zone_threats_type",
	"cloudflare_zone_pageviews_total",
	"cloudflare_zone_uniques_total",
	"cloudflare_zone_colocation_requests_total",
	"cloudflare_zone_colocation_visits",
	"cloudflare_zone_firewall_events_count",
	"cloudflare_zone_health_check_events_origin_count",
}

var expectedTunnelMetrics = []string{
	"cloudflare_tunnel_info",
	"cloudflare_tunnel_health_status",
	"cloudflare_tunnel_connector_active_connections",
	"cloudflare_tunnel_connector_info",
}

// TestInventoryZoneMetrics checks all 14 cloudflare_zone_* families are present.
func TestInventoryZoneMetrics(t *testing.T) {
	cap := runOnce(t, buildConstruct(t, makeConfig()))
	present := map[string]bool{}
	for _, n := range cap.Names() {
		present[n] = true
	}
	for _, want := range expectedZoneMetrics {
		if !present[want] {
			t.Errorf("missing expected zone metric %q", want)
		}
	}
}

// TestInventoryTunnelMetrics checks all 4 cloudflare_tunnel_* families are present.
func TestInventoryTunnelMetrics(t *testing.T) {
	cap := runOnce(t, buildConstruct(t, makeConfig()))
	present := map[string]bool{}
	for _, n := range cap.Names() {
		present[n] = true
	}
	for _, want := range expectedTunnelMetrics {
		if !present[want] {
			t.Errorf("missing expected tunnel metric %q", want)
		}
	}
}

// TestExactInventory asserts no unexpected series names are emitted.
func TestExactInventory(t *testing.T) {
	cap := runOnce(t, buildConstruct(t, makeConfig()))
	expected := map[string]bool{}
	for _, n := range append(expectedZoneMetrics, expectedTunnelMetrics...) {
		expected[n] = true
	}
	for _, got := range cap.Names() {
		if !expected[got] {
			t.Errorf("unexpected series %q", got)
		}
	}
}

// ── (b) zone label == cfg.Zone ────────────────────────────────────────────────

// TestZoneLabelEqualsConfig verifies every zone series carries zone == cfg.Zone.
func TestZoneLabelEqualsConfig(t *testing.T) {
	cfg := makeConfig()
	cap := runOnce(t, buildConstruct(t, cfg))
	for _, s := range cap.All() {
		if z, ok := s.Labels["zone"]; ok {
			if z != testZone {
				t.Errorf("series %q: zone=%q want %q", s.Name, z, testZone)
			}
		}
	}
}

// ── (c) Colocation series per configured colos ────────────────────────────────

// TestColocationSeriesPerConfiguredColo verifies that colocation_requests_total and
// colocation_visits each have one series per configured colo.
func TestColocationSeriesPerConfiguredColo(t *testing.T) {
	cfg := makeConfig()
	cap := runOnce(t, buildConstruct(t, cfg))

	for _, metricName := range []string{
		"cloudflare_zone_colocation_requests_total",
		"cloudflare_zone_colocation_visits",
	} {
		colosSeen := map[string]bool{}
		for _, s := range cap.Find(metricName) {
			if c := s.Labels["colocation"]; c != "" {
				colosSeen[c] = true
			}
		}
		for _, want := range testColocations {
			if !colosSeen[want] {
				t.Errorf("%s: missing colocation %q; got %v", metricName, want, colosSeen)
			}
		}
		if len(colosSeen) != len(testColocations) {
			t.Errorf("%s: expected %d colocations, got %d: %v",
				metricName, len(testColocations), len(colosSeen), colosSeen)
		}
	}
}

// ── (d) Tunnel IDs are deterministic from fx.Seed ─────────────────────────────

// TestTunnelIDsDeterministic verifies two constructs built with the same Seed produce
// the same tunnel_id values (ARCHITECTURE I12).
func TestTunnelIDsDeterministic(t *testing.T) {
	fx := &fixture.Set{Seed: testSeed}
	c1, _ := cloudflare.Build(makeConfig(), fx)
	c2, _ := cloudflare.Build(makeConfig(), fx)

	cap1 := runOnce(t, c1)
	cap2 := runOnce(t, c2)

	ids1 := tunnelIDsFrom(cap1)
	ids2 := tunnelIDsFrom(cap2)

	if len(ids1) == 0 {
		t.Fatal("no tunnel_info series found")
	}
	if !equalStringSets(ids1, ids2) {
		t.Errorf("tunnel_ids not deterministic: run1=%v run2=%v", ids1, ids2)
	}
}

// TestTunnelIDsDifferentSeed verifies different Seeds produce different tunnel_ids.
func TestTunnelIDsDifferentSeed(t *testing.T) {
	fxA := &fixture.Set{Seed: "seed-alpha"}
	fxB := &fixture.Set{Seed: "seed-beta"}
	cA, _ := cloudflare.Build(makeConfig(), fxA)
	cB, _ := cloudflare.Build(makeConfig(), fxB)

	idsA := tunnelIDsFrom(runOnce(t, cA))
	idsB := tunnelIDsFrom(runOnce(t, cB))

	if equalStringSets(idsA, idsB) {
		t.Error("different seeds should produce different tunnel_ids but got the same")
	}
}

// ── (e) Counter behaviour: zone series are cumulative ─────────────────────────

// TestZoneCountersCumulative verifies that zone metrics (state.Add) grow monotonically
// across ticks (I3 — push running totals, never deltas).
func TestZoneCountersCumulative(t *testing.T) {
	c := buildConstruct(t, makeConfig())
	cap1, cap2 := runTwice(t, c)

	for _, name := range []string{
		"cloudflare_zone_requests_total",
		"cloudflare_zone_bandwidth_total",
		"cloudflare_zone_threats_total",
		"cloudflare_zone_pageviews_total",
	} {
		v1 := firstValue(cap1, name)
		v2 := firstValue(cap2, name)
		if v2 <= v1 {
			t.Errorf("%s: tick2 value %v not > tick1 value %v (must be cumulative, I3)", name, v2, v1)
		}
	}
}

// ── (f) Tunnel gauges do NOT accumulate across ticks ──────────────────────────

// TestTunnelGaugesNotCumulative verifies cloudflare_tunnel_info stays at 1 after two
// ticks (state.Set semantics — not a counter).
func TestTunnelGaugesNotCumulative(t *testing.T) {
	c := buildConstruct(t, makeConfig())
	cap1, cap2 := runTwice(t, c)

	for _, name := range []string{
		"cloudflare_tunnel_info",
		"cloudflare_tunnel_health_status",
		"cloudflare_tunnel_connector_info",
	} {
		v1 := firstValue(cap1, name)
		v2 := firstValue(cap2, name)
		if v1 != 1 {
			t.Errorf("%s tick1: want 1, got %v", name, v1)
		}
		if v2 != 1 {
			t.Errorf("%s tick2: want 1, got %v", name, v2)
		}
	}
}

// ── (g) Label keys per extract §3.4 ──────────────────────────────────────────

// TestZoneBaseLabels verifies zone, account, job on every zone series.
func TestZoneBaseLabels(t *testing.T) {
	cfg := makeConfig()
	cap := runOnce(t, buildConstruct(t, cfg))
	for _, s := range cap.All() {
		// Only check zone series (tunnel series don't carry zone).
		if _, ok := s.Labels["zone"]; !ok {
			continue
		}
		for _, k := range []string{"zone", "account", "job"} {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("zone series %q missing required label %q", s.Name, k)
			}
		}
		if s.Labels["job"] != "cloudflare_exporter" {
			t.Errorf("zone series %q: job=%q want cloudflare_exporter", s.Name, s.Labels["job"])
		}
	}
}

// TestTunnelBaseLabels verifies account, tunnel_id, tunnel_name, tunnel_type, job
// on every tunnel series.
func TestTunnelBaseLabels(t *testing.T) {
	cfg := makeConfig()
	cap := runOnce(t, buildConstruct(t, cfg))
	for _, s := range cap.All() {
		if _, ok := s.Labels["tunnel_id"]; !ok {
			continue
		}
		for _, k := range []string{"account", "tunnel_id", "tunnel_name", "tunnel_type", "job"} {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("tunnel series %q missing required label %q", s.Name, k)
			}
		}
		if s.Labels["tunnel_type"] != "cfd_tunnel" {
			t.Errorf("tunnel series %q: tunnel_type=%q want cfd_tunnel", s.Name, s.Labels["tunnel_type"])
		}
	}
}

// TestConnectorLabels verifies client_id present on connector metrics.
func TestConnectorLabels(t *testing.T) {
	cap := runOnce(t, buildConstruct(t, makeConfig()))
	for _, name := range []string{
		"cloudflare_tunnel_connector_active_connections",
		"cloudflare_tunnel_connector_info",
	} {
		series := cap.Find(name)
		if len(series) == 0 {
			t.Errorf("%s: no series found", name)
			continue
		}
		for _, s := range series {
			if _, ok := s.Labels["client_id"]; !ok {
				t.Errorf("%s missing client_id label", name)
			}
		}
	}
}

// ── (h) Interface conformance ────────────────────────────────────────────────

// TestInterfaceConformance verifies Kind / Signals / Interval match the spec.
func TestInterfaceConformance(t *testing.T) {
	c := buildConstruct(t, makeConfig())
	if c.Kind() != "cloudflare" {
		t.Errorf("Kind()=%q want cloudflare", c.Kind())
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals()=%v want [Metrics]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v want 60s", c.Interval())
	}
}

// ── (i) Build errors ─────────────────────────────────────────────────────────

// TestBuildErrorEmptyZone verifies Build returns an error when zone is empty.
func TestBuildErrorEmptyZone(t *testing.T) {
	fx := &fixture.Set{Seed: testSeed}
	_, err := cloudflare.Build(&cloudflare.Config{}, fx)
	if err == nil {
		t.Error("Build with empty zone must return an error, got nil")
	}
}

// TestBuildErrorWrongType verifies Build returns an error for a non-*Config argument.
func TestBuildErrorWrongType(t *testing.T) {
	fx := &fixture.Set{Seed: testSeed}
	_, err := cloudflare.Build("not-a-config", fx)
	if err == nil {
		t.Error("Build with wrong cfg type must return an error, got nil")
	}
}

// ── (j) Default colocations applied when none configured ─────────────────────

// TestDefaultColocations verifies IAD and FRA are emitted when colocations is nil.
func TestDefaultColocations(t *testing.T) {
	cfg := &cloudflare.Config{Zone: testZone}
	cap := runOnce(t, buildConstruct(t, cfg))

	colosSeen := map[string]bool{}
	for _, s := range cap.Find("cloudflare_zone_colocation_requests_total") {
		if co := s.Labels["colocation"]; co != "" {
			colosSeen[co] = true
		}
	}
	for _, want := range []string{"IAD", "FRA"} {
		if !colosSeen[want] {
			t.Errorf("default colocations: missing %q, got %v", want, colosSeen)
		}
	}
}

// ── (k) Default tunnel applied when none configured ───────────────────────────

// TestDefaultTunnel verifies a single tunnel is emitted when tunnels is nil,
// named "<zone>-tunnel".
func TestDefaultTunnel(t *testing.T) {
	cfg := &cloudflare.Config{Zone: testZone}
	cap := runOnce(t, buildConstruct(t, cfg))

	tunnelNames := map[string]bool{}
	for _, s := range cap.Find("cloudflare_tunnel_info") {
		if tn := s.Labels["tunnel_name"]; tn != "" {
			tunnelNames[tn] = true
		}
	}
	wantName := testZone + "-tunnel"
	if !tunnelNames[wantName] {
		t.Errorf("default tunnel: want tunnel_name %q, got %v", wantName, tunnelNames)
	}
	if len(tunnelNames) != 1 {
		t.Errorf("default tunnel: expected 1 tunnel, got %d: %v", len(tunnelNames), tunnelNames)
	}
}

// ── extra sanity: account label equals configured account ─────────────────────

// TestAccountLabelEqualsConfig verifies account label equals cfg.Account on all series.
func TestAccountLabelEqualsConfig(t *testing.T) {
	cfg := makeConfig()
	cap := runOnce(t, buildConstruct(t, cfg))
	for _, s := range cap.All() {
		if acct, ok := s.Labels["account"]; ok {
			if acct != testAccount {
				t.Errorf("series %q: account=%q want %q", s.Name, acct, testAccount)
			}
		}
	}
}

// TestDefaultAccountDerivedFromZone verifies that omitting account defaults to zone value.
func TestDefaultAccountDerivedFromZone(t *testing.T) {
	cfg := &cloudflare.Config{Zone: testZone} // no Account set
	cap := runOnce(t, buildConstruct(t, cfg))
	for _, s := range cap.All() {
		if acct, ok := s.Labels["account"]; ok {
			if acct != testZone {
				t.Errorf("series %q: default account=%q want zone %q", s.Name, acct, testZone)
			}
		}
	}
}

// TestMultipleTunnels verifies multiple configured tunnels each get distinct IDs.
func TestMultipleTunnels(t *testing.T) {
	cfg := &cloudflare.Config{
		Zone:    testZone,
		Account: testAccount,
		Tunnels: []cloudflare.TunnelConfig{
			{Name: "tunnel-a"},
			{Name: "tunnel-b"},
			{Name: "tunnel-c"},
		},
	}
	cap := runOnce(t, buildConstruct(t, cfg))

	tunnelIDs := map[string]bool{}
	for _, s := range cap.Find("cloudflare_tunnel_info") {
		if tid := s.Labels["tunnel_id"]; tid != "" {
			tunnelIDs[tid] = true
		}
	}
	if len(tunnelIDs) != 3 {
		t.Errorf("expected 3 distinct tunnel_ids, got %d: %v", len(tunnelIDs), tunnelIDs)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func tunnelIDsFrom(cap *coretest.MetricCapture) []string {
	seen := map[string]bool{}
	for _, s := range cap.Find("cloudflare_tunnel_info") {
		if tid := s.Labels["tunnel_id"]; tid != "" {
			seen[tid] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func firstValue(cap *coretest.MetricCapture, name string) float64 {
	series := cap.Find(name)
	if len(series) == 0 {
		return 0
	}
	return series[0].Value
}

// ── per-series variation ──────────────────────────────────────────────────────

// TestPerSeriesVariation verifies that distinct zones/tunnels do NOT produce lockstep values.
//
// (1) Cross-zone spread: 5 constructs with distinct zones should have distinct
//
//	cloudflare_zone_requests_total values after one tick.
//
// (2) Temporal drift: a single zone's cloudflare_zone_requests_total should produce
//
//	at least 5 distinct values over 30 ticks at 13-minute intervals.
//
// (3) Per-tunnel spread: a single construct with 5 tunnels should have distinct
//
//	cloudflare_tunnel_connector_active_connections values.
func TestPerSeriesVariation(t *testing.T) {
	// (1) Cross-zone spread — 5 constructs with distinct zones and distinct seeds.
	zones := []string{"zone1.com", "zone2.com", "zone3.com", "zone4.com", "zone5.com"}
	seeds := []string{"seed-z1", "seed-z2", "seed-z3", "seed-z4", "seed-z5"}
	zoneVals := make([]float64, len(zones))
	now := time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)
	for i, zone := range zones {
		cfg := &cloudflare.Config{Zone: zone}
		fx := &fixture.Set{Seed: seeds[i]}
		c, err := cloudflare.Build(cfg, fx)
		if err != nil {
			t.Fatalf("Build zone %s: %v", zone, err)
		}
		cap := &coretest.MetricCapture{}
		w := coretest.World(cap, nil, nil)
		if err := c.Tick(context.Background(), now, w); err != nil {
			t.Fatalf("Tick zone %s: %v", zone, err)
		}
		series := cap.Find("cloudflare_zone_requests_total")
		if len(series) == 0 {
			t.Fatalf("zone %s: no cloudflare_zone_requests_total series", zone)
		}
		zoneVals[i] = series[0].Value
	}
	distinctZone := map[float64]bool{}
	for _, v := range zoneVals {
		distinctZone[v] = true
	}
	if len(distinctZone) < 4 {
		t.Errorf("cross-zone spread: want >=4 distinct cloudflare_zone_requests_total values, got %d: %v",
			len(distinctZone), zoneVals)
	}

	// (2) Temporal drift — single zone, 30 ticks at 13-minute intervals.
	cfgDrift := &cloudflare.Config{Zone: "drift.example.com"}
	fxDrift := &fixture.Set{Seed: "drift-seed"}
	cDrift, err := cloudflare.Build(cfgDrift, fxDrift)
	if err != nil {
		t.Fatalf("Build drift: %v", err)
	}
	driftVals := map[float64]bool{}
	base := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	for tick := range 30 {
		capD := &coretest.MetricCapture{}
		wD := coretest.World(capD, nil, nil)
		tickTime := base.Add(time.Duration(tick) * 13 * time.Minute)
		if err := cDrift.Tick(context.Background(), tickTime, wD); err != nil {
			t.Fatalf("drift tick %d: %v", tick, err)
		}
		series := capD.Find("cloudflare_zone_requests_total")
		if len(series) > 0 {
			driftVals[series[0].Value] = true
		}
	}
	if len(driftVals) < 5 {
		t.Errorf("temporal drift: want >=5 distinct cumulative values over 30 ticks, got %d", len(driftVals))
	}

	// (3) Per-tunnel spread — one construct with 5 tunnels, distinct active_connections values.
	cfgTunnels := &cloudflare.Config{
		Zone:    testZone,
		Account: testAccount,
		Tunnels: []cloudflare.TunnelConfig{
			{Name: "tunnel-1"},
			{Name: "tunnel-2"},
			{Name: "tunnel-3"},
			{Name: "tunnel-4"},
			{Name: "tunnel-5"},
		},
	}
	fxTunnels := &fixture.Set{Seed: testSeed}
	cTunnels, err := cloudflare.Build(cfgTunnels, fxTunnels)
	if err != nil {
		t.Fatalf("Build tunnels: %v", err)
	}
	capT := &coretest.MetricCapture{}
	wT := coretest.World(capT, nil, nil)
	if err := cTunnels.Tick(context.Background(), now, wT); err != nil {
		t.Fatalf("Tick tunnels: %v", err)
	}
	cxSeries := capT.Find("cloudflare_tunnel_connector_active_connections")
	if len(cxSeries) != 5 {
		t.Fatalf("per-tunnel spread: want 5 series, got %d", len(cxSeries))
	}
	distinctCx := map[float64]bool{}
	for _, s := range cxSeries {
		distinctCx[s.Value] = true
	}
	if len(distinctCx) < 4 {
		t.Errorf("per-tunnel spread: want >=4 distinct active_connections values across 5 tunnels, got %d", len(distinctCx))
	}
}
