// SPDX-License-Identifier: AGPL-3.0-only

package fleet

// controller.go — FM registration lifecycle driver.
//
// Key differences from the upstream predecessor pattern:
//   - Config is passed at construction; no package-level mutable globals.
//   - DRY_RUN mode forwarded to the client (no HTTP when Config.DryRun true).
//   - Roster is passed to Start() instead of read from a global fleet.Roster().
//   - Unregister-on-cancel uses context.Background() exactly as predecessor lines 30–32.
//   - StartDynamic drives a RosterProvider (B2): diffs on each tick, registers new ids,
//     unregisters departed ids, heartbeats current ids.

import (
	"context"
	"log"
	"time"

	"github.com/rknightion/synthkit/internal/fleethook"
)

// Controller drives the FM registration lifecycle. Construct one per blueprint that
// declares a fleet_management kind, then call Start(ctx, roster) or StartDynamic(ctx, provider).
type Controller struct {
	cfg        Config
	heartbeat  time.Duration
	registered map[string]bool
	client     *Client // built once in NewController; reused across heartbeats (conn-pool churn fix, L4)
}

// NewController returns a ready Controller. heartbeat defaults to defaultHeartbeatSeconds
// when cfg.HeartbeatInterval ≤ 0 (predecessor controller.go:20).
func NewController(cfg Config) *Controller {
	d := time.Duration(cfg.HeartbeatInterval) * time.Second
	if d <= 0 {
		d = defaultHeartbeatSeconds * time.Second
	}
	c := &Controller{
		cfg:        cfg,
		heartbeat:  d,
		registered: map[string]bool{},
	}
	// Build the FM client (and its HTTP connection pool) ONCE and reuse it across every
	// heartbeat; the previous code allocated a fresh *http.Client per tick, churning the pool.
	c.client = c.newClient()
	return c
}

// Start registers a fixed roster and heartbeats it until ctx is cancelled (back-compat shim).
// It delegates the heartbeat loop to StartDynamic, then on return unregisters every
// still-registered id (backward-compatible shutdown behaviour, predecessor controller.go:30–32).
func (c *Controller) Start(ctx context.Context, roster []Collector) {
	c.StartDynamic(ctx, func() []Collector { return roster })
	c.unregisterRegistered(context.Background()) //nolint:contextcheck
}

// StartDynamic drives the FM lifecycle against a roster recomputed each heartbeat. New ids are
// registered, departed ids unregistered, and all current ids heartbeated. On ctx cancel the
// loop exits without unregistering — callers that need cleanup should call unregisterRegistered
// themselves (Start does this for the fixed-roster case). Blocks until ctx is done (run in a goroutine).
func (c *Controller) StartDynamic(ctx context.Context, provider RosterProvider) {
	c.reconcile(ctx, provider()) // initial register

	t := time.NewTicker(c.heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.reconcile(ctx, provider())
		}
	}
}

// authMissing reports the token-less non-dry-run state where registration never happens, so the
// unregister paths must be no-ops (mirrors the old unregisterAll guard, controller.go:114-121).
func (c *Controller) authMissing() bool {
	return c.cfg.FMURL == "" || (c.cfg.Token == "" && !c.cfg.DryRun)
}

// reconcile registers any id in want not yet registered, unregisters any registered id no longer
// in want, then heartbeats every id in want.
func (c *Controller) reconcile(ctx context.Context, want []Collector) {
	client := c.client
	wantSet := make(map[string]bool, len(want))
	for _, col := range want {
		wantSet[col.ID] = true
	}
	// Unregister departed (skip entirely when auth is missing — nothing was registered).
	if !c.authMissing() {
		for id := range c.registered {
			if !wantSet[id] {
				err := client.UnregisterCollector(ctx, id)
				c.observe(ctx, fleethook.OpUnregister, id, 0, err)
				if err != nil {
					log.Printf("fleet: unregister (departed) %s: %v", id, err)
					continue
				}
				delete(c.registered, id)
			}
		}
	}
	// Register new + heartbeat current.
	for _, col := range want {
		if !c.registered[col.ID] {
			err := client.RegisterCollector(ctx, col)
			c.observe(ctx, fleethook.OpRegister, col.ID, 0, err)
			if err != nil {
				log.Printf("fleet: register %s: %v", col.ID, err)
				continue
			}
			c.registered[col.ID] = true
		}
		start := time.Now()
		err := client.GetConfig(ctx, col)
		c.observe(ctx, fleethook.OpHeartbeat, col.ID, time.Since(start), err)
		if err != nil {
			log.Printf("fleet: heartbeat %s: %v", col.ID, err)
		}
	}
}

// unregisterRegistered unwinds every still-registered id (shutdown path).
func (c *Controller) unregisterRegistered(ctx context.Context) {
	if c.authMissing() {
		c.registered = map[string]bool{}
		return
	}
	client := c.client
	for id := range c.registered {
		err := client.UnregisterCollector(ctx, id)
		c.observe(ctx, fleethook.OpUnregister, id, 0, err)
		if err != nil {
			log.Printf("fleet: unregister %s: %v", id, err)
			continue
		}
		delete(c.registered, id)
	}
}

// observe fires the fleethook seam (no-op when unset), stamping the controller's dry-run mode.
func (c *Controller) observe(ctx context.Context, op, collector string, dur time.Duration, err error) {
	if c.cfg.Observe == nil {
		return
	}
	c.cfg.Observe(ctx, fleethook.Event{
		Collector: collector, Op: op, Duration: dur, DryRun: c.cfg.DryRun, Err: err,
	})
}

// newClient constructs a Client from the current config. DryRun is forwarded.
func (c *Controller) newClient() *Client {
	if c.cfg.DryRun {
		return NewDryRunClient(c.cfg.FMURL, c.cfg.StackID, c.cfg.Token)
	}
	return NewClient(c.cfg.FMURL, c.cfg.StackID, c.cfg.Token)
}
