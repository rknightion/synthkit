// SPDX-License-Identifier: AGPL-3.0-only

// Package cspazure is the synthkit construct for the Grafana CSP Azure integration:
// the azure_* metric families produced by the Azure Monitor / azure_exporter path, plus
// Azure Event Hubs log streams.
//
// Kind:     "csp_azure"
// Scope:    core.ScopeSubstrate  (NO blueprint label — ARCHITECTURE §5 / I21)
// Signals:  []{core.Metrics, core.Logs}
// Interval: 60 s
//
// # Window-gauge invariant (CRITICAL — ARCHITECTURE I5 / extract §1.3)
//
// EVERY Azure metric is a PER-PT5M-WINDOW total delivered by Azure Monitor — NOT a
// running cumulative counter. ALL series MUST use st.Set, NEVER st.Add, even those whose
// names end in _total_count. Using Add would make them monotonically increasing and break
// any increase()/rate() panel that queries them.
//
// # Identity shapes (extract §4.1)
//
//	subscriptionID   = "00000000-0000-0000-0000-<12-digit zero-padded 1-based index>"
//	subscriptionName = "<Company capitalised>-<2-digit index>"  (e.g. "Demo-01")
//	resourceID       = PATH-AWARE (see identity.go): azure_exporter = fully lowercased ARM path
//	                   (last segment == resourceName); serverless = PascalCase-preserved ARM path
//	                   (resourceGroups capital-G, Microsoft.X namespaces), instance == resourceID.
//
// # Fixture wiring
//
//   - fx.Seed is REQUIRED: all identity is seeded from it (I12).
//   - fx.DBs: when present, even-index Postgres fixtures provide the PG Flexible Server
//     resource names (the dbo11y↔CSP join); otherwise self-contained names are used.
package cspazure

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/state"
)

// ── Registration ──────────────────────────────────────────────────────────────

// Registration returns the core.ConstructReg for the "csp_azure" kind.
// Called by the composition root's catalog wiring file — no init() self-registration.
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "csp_azure",
		Doc:       "Grafana CSP Azure integration: azure_* window-gauge metrics + Event Hubs log streams",
		Scope:     core.ScopeSubstrate,
		Group:     core.GroupIntegration, // external source GC ingests (integrations: section)
		NewConfig: func() any { return &Config{} },
		Build: func(cfg any, fx *fixture.Set) (core.Construct, error) {
			c := cfg.(*Config)
			applyDefaults(c)
			if fx == nil {
				return nil, fmt.Errorf("csp_azure: fx is required")
			}
			if c.IngestionPath != pathServerless && c.IngestionPath != pathExporter {
				return nil, fmt.Errorf("csp_azure: ingestion_path must be %q or %q, got %q",
					pathServerless, pathExporter, c.IngestionPath)
			}
			// On the serverless path applyDefaults guarantees a non-empty credential
			// (defaults to "azure" when omitted), so the credential label is always present.
			return &construct{cfg: *c, fx: fx, st: state.NewState()}, nil
		},
	}
}

// ── Config ────────────────────────────────────────────────────────────────────

// Config is the YAML config struct for the csp_azure construct.
// Unknown fields are rejected by strict yaml.v3 decoding at blueprint load.
type Config struct {
	// Subscriptions is the number of synthetic Azure subscriptions to emit (default 2).
	Subscriptions int `yaml:"subscriptions"`
	// Company is the slug used to build subscription names (default "demo").
	Company string `yaml:"company"`
	// SubSignals is the set of sub-signals to emit. When empty, all are enabled.
	// Valid values: compute, databases, storage, networking, messaging, logs, ai.
	// NOTE: "ai" is OPT-IN and is NOT included in the default set — it must be named
	// explicitly (e.g. sub_signals: [ai]) to emit Cognitive Services / Azure OpenAI metrics.
	SubSignals []string `yaml:"sub_signals"`
	// IngestionPath selects the Azure→Mimir ingestion path the estate emulates
	// (signals/cspazure.md [slug: cspazure], SK-16): "serverless" (the GC cloud/azure managed scraper — the
	// PREFERRED default) or "azure_exporter" (prometheus.exporter.azure). The two label
	// the same metrics differently (job, resourceID casing, instance, interval/timespan,
	// dimension key form, HttpStatusGroup casing). Default "serverless".
	IngestionPath string `yaml:"ingestion_path"`
	// Credential is the managed-scraper credential name surfaced as the `credential` label
	// on EVERY serverless-path series (e.g. "ps_azure"). Deployment-specific (like an AWS
	// account_id). Ignored on the azure_exporter path (which has no credential label).
	// Defaults to "azure" on the serverless path when omitted.
	Credential string `yaml:"credential"`
	// Tags are resource tags surfaced as `tag_<key>` labels on EVERY series, on both paths
	// (serverless via the managed scraper's `tags` setting; azure_exporter via
	// `included_resource_tags`). OPT-IN: when omitted, NO tag labels are emitted — matching a
	// default managed scraper (live-confirmed: the default scraper surfaces no tags). Use
	// lowercase CAF keys (e.g. app, env, owner, costcenter) for cross-cloud consistency.
	Tags map[string]string `yaml:"tags"`
}

var allSubSignals = []string{"compute", "databases", "storage", "networking", "messaging", "logs"}

func applyDefaults(c *Config) {
	if c.Subscriptions == 0 {
		c.Subscriptions = 2
	}
	if c.Company == "" {
		c.Company = "demo"
	}
	if len(c.SubSignals) == 0 {
		c.SubSignals = allSubSignals
	}
	if c.IngestionPath == "" {
		c.IngestionPath = pathServerless
	}
	if c.IngestionPath == pathServerless && c.Credential == "" {
		c.Credential = "azure"
	}
}

func (c *Config) signalEnabled(sig string) bool {
	for _, s := range c.SubSignals {
		if s == sig {
			return true
		}
	}
	return false
}

// ── construct ─────────────────────────────────────────────────────────────────

type construct struct {
	cfg Config
	fx  *fixture.Set
	st  *state.State
	// subs is built once at first Tick (deterministic from fx.Seed + cfg).
	subs []azureSub
}

func (c *construct) Kind() string { return "csp_azure" }
func (c *construct) Signals() []core.SignalClass {
	return []core.SignalClass{core.Metrics, core.Logs}
}
func (c *construct) Interval() time.Duration { return 60 * time.Second }

func (c *construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	if c.subs == nil {
		c.subs = buildSubs(c.cfg, c.fx)
	}
	bf := w.Shape.Factor(now, 1.0, false)
	if bf < 0 {
		bf = 0
	}
	// Metrics lane.
	if c.cfg.signalEnabled("compute") {
		for _, sub := range c.subs {
			c.emitCompute(now, w, sub, bf)
		}
	}
	if c.cfg.signalEnabled("databases") {
		for _, sub := range c.subs {
			c.emitDatabases(now, w, sub, bf)
		}
	}
	if c.cfg.signalEnabled("storage") {
		for _, sub := range c.subs {
			c.emitStorage(now, w, sub, bf)
		}
	}
	if c.cfg.signalEnabled("networking") {
		for _, sub := range c.subs {
			c.emitNetworking(now, w, sub, bf)
		}
	}
	if c.cfg.signalEnabled("messaging") {
		for _, sub := range c.subs {
			c.emitMessaging(now, w, sub, bf)
		}
	}
	if c.cfg.signalEnabled("ai") {
		for _, sub := range c.subs {
			c.emitAI(now, w, sub, bf)
		}
	}
	if err := w.Metrics.Write(ctx, c.st.Collect(now)); err != nil {
		return err
	}
	// Logs lane.
	if c.cfg.signalEnabled("logs") {
		var streams []loki.Stream
		for _, sub := range c.subs {
			streams = append(streams, c.logsForSub(now, sub)...)
		}
		if len(streams) > 0 {
			if err := w.Logs.Write(ctx, streams); err != nil {
				return err
			}
		}
	}
	return nil
}
