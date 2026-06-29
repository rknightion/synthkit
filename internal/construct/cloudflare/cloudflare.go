// SPDX-License-Identifier: AGPL-3.0-only

// Package cloudflare implements the "cloudflare" construct.
//
// Kind:     "cloudflare"
// Scope:    core.ScopeBlueprint (blueprint-scoped — ARCHITECTURE §5)
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
// Fixtures: fx.Seed only (no cluster/cloud required)
//
// Config (yaml):
//
//	zone         string        required; e.g. "example.com"
//	account      string        optional; default = zone (config-driven ⓐ)
//	colocations  []string      optional; default ["IAD","FRA"]
//	tunnels      []TunnelConfig optional; default one {name: "<zone>-tunnel"}
//
// Signal contract (signals/cloudflare.md):
//
// Zone metrics (cloudflare_zone_*) — all cumulative counters (st.Add), zone-global:
//
//	cloudflare_zone_requests_total
//	cloudflare_zone_requests_cached
//	cloudflare_zone_requests_status          (status ∈ {"200","301","404","429","500"})
//	cloudflare_zone_requests_country         (country+region)
//	cloudflare_zone_bandwidth_total
//	cloudflare_zone_bandwidth_cached
//	cloudflare_zone_threats_total
//	cloudflare_zone_threats_type             (type ∈ {"jsDosAttempt","badHost","secureClientError"})
//	cloudflare_zone_pageviews_total
//	cloudflare_zone_uniques_total
//	cloudflare_zone_colocation_requests_total (colocation+host per configured colo)
//	cloudflare_zone_colocation_visits         (colocation+host per configured colo)
//	cloudflare_zone_firewall_events_count
//	cloudflare_zone_health_check_events_origin_count
//
// Tunnel metrics (cloudflare_tunnel_*) — all instantaneous gauges (st.Set), per-tunnel:
//
//	cloudflare_tunnel_info
//	cloudflare_tunnel_health_status
//	cloudflare_tunnel_connector_active_connections
//	cloudflare_tunnel_connector_info
//
// Zone counter semantics (I3): zone metrics are exporter-style Prometheus counters —
// cumulative across ticks. state.Add is used so wire shape is monotonically increasing,
// matching lablabs/cloudflare-exporter output and matching the predecessor's st.Add calls.
//
// Tunnel gauge semantics: tunnel metrics are instantaneous (health/connection state) so
// state.Set is correct. This matches the predecessor's st.Set calls for tunnel series.
//
// Label layout (extract §3.4):
//
//	zone metrics:      zone, account, job="cloudflare_exporter"  (+ optional extras per metric)
//	tunnel metrics:    account, tunnel_id, tunnel_name, tunnel_type="cfd_tunnel", job
//	connector metrics: tunnel base + client_id
//
// Determinism (ARCHITECTURE I12): tunnel_id and client_id are derived from fx.Seed + tunnel
// index via fixture.HexID. The zone label equals cfg.Zone verbatim.
//
// I13: absent dimensions are omitted (no ""/NA).
// I18: zero blueprint-name references in this file.
package cloudflare

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	kind            = "cloudflare"
	interval        = 60 * time.Second
	jobLabel        = "cloudflare_exporter"
	tunnelTypeLabel = "cfd_tunnel"
)

// TunnelConfig holds per-tunnel configuration.
type TunnelConfig struct {
	Name string `yaml:"name"`
}

// Config is the YAML config struct for the cloudflare construct.
// All synthetic values are config-driven per extract §3.5 (ⓐ).
type Config struct {
	Zone        string         `yaml:"zone"`
	Account     string         `yaml:"account"`
	Colocations []string       `yaml:"colocations"`
	Tunnels     []TunnelConfig `yaml:"tunnels"`
}

// resolvedConfig is Config after defaults are applied.
type resolvedConfig struct {
	zone        string
	account     string
	colocations []string
	tunnels     []TunnelConfig
}

func resolveConfig(cfg *Config) resolvedConfig {
	r := resolvedConfig{
		zone:        cfg.Zone,
		account:     cfg.Account,
		colocations: cfg.Colocations,
		tunnels:     cfg.Tunnels,
	}
	if r.account == "" {
		r.account = cfg.Zone
	}
	if len(r.colocations) == 0 {
		r.colocations = []string{"IAD", "FRA"}
	}
	if len(r.tunnels) == 0 {
		r.tunnels = []TunnelConfig{{Name: cfg.Zone + "-tunnel"}}
	}
	return r
}

// Construct is the per-instance Cloudflare renderer.
type Construct struct {
	cfg  resolvedConfig
	seed string
	st   *state.State
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates config and returns a ready core.Construct. Only fx.Seed is required.
// Returns an error if cfg.Zone is empty.
func Build(cfgAny any, fx *fixture.Set) (core.Construct, error) {
	cfg, ok := cfgAny.(*Config)
	if !ok {
		return nil, fmt.Errorf("cloudflare: Build received %T, want *Config", cfgAny)
	}
	if cfg.Zone == "" {
		return nil, fmt.Errorf("cloudflare: zone is required")
	}
	return &Construct{
		cfg:  resolveConfig(cfg),
		seed: fx.Seed,
		st:   state.NewState(),
	}, nil
}

func (c *Construct) Kind() string                { return kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return interval }

// Tick renders one 60-second observation into w.Metrics.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	batch := c.render(now, w)
	return w.Metrics.Write(ctx, batch)
}

// render produces the full metric batch. Separated for unit testing without a World.
func (c *Construct) render(now time.Time, w *core.World) []promrw.Series {
	factor := w.Shape.BusinessFactor(now)

	c.renderZone(factor, now, w)
	c.renderTunnels(now, w)

	return c.st.Collect(now)
}

// seriesVar returns a stable-but-living per-series multiplier ≈ 1±amp: a deterministic
// baseline spread combined with a slow per-series temporal drift (Wander).
// Keys should be unique per logical series (e.g. "zone_requests|zone.example.com|account").
func (c *Construct) seriesVar(w *core.World, now time.Time, key string, amp float64) float64 {
	if w == nil || w.Shape == nil {
		return 1.0
	}
	return w.Shape.Spread(key, amp) * w.Shape.Wander(key, now, amp*0.4)
}

// renderZone emits all cloudflare_zone_* counters (state.Add — cumulative, I3).
// Zone metrics are zone-global: no per-cell or per-tunnel fan-out.
func (c *Construct) renderZone(factor float64, now time.Time, w *core.World) {
	// Base label builder for zone metrics (returns a fresh map each call — no aliasing).
	zBase := func(extra map[string]string) map[string]string {
		m := map[string]string{
			"zone":    c.cfg.zone,
			"account": c.cfg.account,
			"job":     jobLabel,
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	// ~50k req/s at factor=1 over the 60-second window, with per-zone spread + drift.
	totalReqs := (factor*0.4 + 0.5) * 50000 * 60 *
		c.seriesVar(w, now, "zone_requests|"+c.cfg.zone+"|"+c.cfg.account, 0.18)

	c.st.Add("cloudflare_zone_requests_total", zBase(nil), totalReqs)
	c.st.Add("cloudflare_zone_requests_cached", zBase(nil), totalReqs*0.35)

	// Per-status counters (extract §3.2 status enum).
	for _, e := range []struct {
		status string
		frac   float64
	}{
		{"200", 0.92},
		{"301", 0.04},
		{"404", 0.025},
		{"429", 0.01},
		{"500", 0.005},
	} {
		c.st.Add("cloudflare_zone_requests_status",
			zBase(map[string]string{"status": e.status}),
			totalReqs*e.frac)
	}

	// Per-country counters (country+region labels per extract §3.2; plausible mix).
	for _, cc := range []struct {
		country, region string
		frac            float64
	}{
		{"US", "Americas", 0.40},
		{"DE", "Europe", 0.35},
		{"GB", "Europe", 0.25},
	} {
		c.st.Add("cloudflare_zone_requests_country",
			zBase(map[string]string{"country": cc.country, "region": cc.region}),
			totalReqs*cc.frac)
	}

	// Bandwidth in bytes (~8 KB average response), with independent per-zone variation.
	bwTotal := totalReqs * 8192 * c.seriesVar(w, now, "zone_bw|"+c.cfg.zone, 0.18)
	c.st.Add("cloudflare_zone_bandwidth_total", zBase(nil), bwTotal)
	c.st.Add("cloudflare_zone_bandwidth_cached", zBase(nil), bwTotal*0.35)

	// Threats (0.1% of total requests), with per-zone threat variation.
	threats := totalReqs * 0.001 * c.seriesVar(w, now, "zone_threats|"+c.cfg.zone, 0.30)
	c.st.Add("cloudflare_zone_threats_total", zBase(nil), threats)
	for _, ttype := range []string{"jsDosAttempt", "badHost", "secureClientError"} {
		c.st.Add("cloudflare_zone_threats_type",
			zBase(map[string]string{"type": ttype}),
			threats/3)
	}

	// Page views and unique visitors.
	c.st.Add("cloudflare_zone_pageviews_total", zBase(nil), totalReqs*0.4)
	c.st.Add("cloudflare_zone_uniques_total", zBase(nil), totalReqs*0.05)

	// Per-colocation counters — one series pair per configured PoP.
	// Spread requests across colos; first colo gets a slightly larger share.
	n := float64(len(c.cfg.colocations))
	for i, colo := range c.cfg.colocations {
		// Linear taper: first colo ~(1/n + small bump), last colo ~(1/n - small bump).
		frac := 1.0/n + (float64(len(c.cfg.colocations)-1-i)*0.05)/n
		// Per-colocation variation: each PoP drifts independently.
		coloReqs := totalReqs * frac * c.seriesVar(w, now, "colo_req|"+c.cfg.zone+"|"+colo, 0.18)
		coloLbls := map[string]string{
			"zone":       c.cfg.zone,
			"account":    c.cfg.account,
			"job":        jobLabel,
			"colocation": colo,
			"host":       c.cfg.zone,
		}
		c.st.Add("cloudflare_zone_colocation_requests_total", coloLbls, coloReqs)
		// Visits are a subset of requests (~60%).
		coloVisitLbls := copyLabels(coloLbls)
		c.st.Add("cloudflare_zone_colocation_visits", coloVisitLbls, coloReqs*0.6)
	}

	// Firewall events (WAF — low baseline, ~70% of threats blocked), with per-zone variation.
	fwEvents := threats * 0.7 * c.seriesVar(w, now, "zone_fw|"+c.cfg.zone, 0.30)
	c.st.Add("cloudflare_zone_firewall_events_count",
		zBase(map[string]string{
			"action":  "block",
			"source":  "waf",
			"rule":    "waf-managed",
			"host":    c.cfg.zone,
			"country": "XX",
		}),
		fwEvents)

	// Health-check events (minimal — 2 per tick as a liveness floor).
	c.st.Add("cloudflare_zone_health_check_events_origin_count", zBase(nil), 2)
}

// renderTunnels emits cloudflare_tunnel_* gauges (state.Set) — one set per configured tunnel.
// Tunnel IDs are deterministic from fx.Seed (ARCHITECTURE I12).
func (c *Construct) renderTunnels(now time.Time, w *core.World) {
	baseCx := 4.0 + w.Shape.NormFloat64()*0.5
	if baseCx < 1 {
		baseCx = 1
	}

	for i, t := range c.cfg.tunnels {
		tid := tunnelID(c.seed, i)
		cid := clientID(c.seed, i)

		tBase := map[string]string{
			"account":     c.cfg.account,
			"tunnel_id":   tid,
			"tunnel_name": t.Name,
			"tunnel_type": tunnelTypeLabel,
			"job":         jobLabel,
		}

		// Info and health are constant gauges — not varied.
		c.st.Set("cloudflare_tunnel_info", copyLabels(tBase), 1)
		c.st.Set("cloudflare_tunnel_health_status", copyLabels(tBase), 1)

		// Connector metrics extend the tunnel base with client_id.
		cBase := copyLabels(tBase)
		cBase["client_id"] = cid

		// Per-tunnel active connections: each tunnel has its own spread + drift.
		activeCx := baseCx * c.seriesVar(w, now, "tunnel_cx|"+tid, 0.18)
		if activeCx < 1 {
			activeCx = 1
		}
		c.st.Set("cloudflare_tunnel_connector_active_connections", copyLabels(cBase), activeCx)
		// Connector info is a constant info gauge.
		c.st.Set("cloudflare_tunnel_connector_info", copyLabels(cBase), 1)
	}
}

// tunnelID returns a deterministic tunnel_id for the i-th tunnel: "tunnel-<8hex>".
// Seeds from fx.Seed so same blueprint = same IDs across runs (ARCHITECTURE I12).
func tunnelID(seed string, i int) string {
	return "tunnel-" + fixture.HexID(seed, 8, "tunnel_id", fmt.Sprintf("%d", i))
}

// clientID returns a deterministic connector client_id for the i-th tunnel.
func clientID(seed string, i int) string {
	return "client-" + fixture.HexID(seed, 8, "client_id", fmt.Sprintf("%d", i))
}

// copyLabels returns a shallow copy of a label map so each series carries an independent
// map (state keying requires non-aliased maps — shared maps cause silent series collisions).
func copyLabels(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
