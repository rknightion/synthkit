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
	"errors"
	"log"
	"time"

	"github.com/rknightion/synthkit/internal/fleethook"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
)

// Controller drives the FM registration lifecycle. Construct one per blueprint that
// declares a fleet_management kind, then call Start(ctx, roster) or StartDynamic(ctx, provider).
type Controller struct {
	cfg        Config
	heartbeat  time.Duration
	registered map[string]bool
	confirmed  map[string]bool
	client     *Client // built once in NewController; reused across heartbeats (conn-pool churn fix, L4)
}

// NewController returns a ready Controller. heartbeat defaults to defaultHeartbeatSeconds
// when cfg.HeartbeatInterval ≤ 0 (predecessor controller.go:20).
func NewController(cfg Config) *Controller {
	if cfg.Delivery.Gate == nil {
		cfg.Delivery = httpretry.DefaultDelivery()
	}
	cfg.Bounded.Gate = cfg.Delivery.Gate
	d := time.Duration(cfg.HeartbeatInterval) * time.Second
	if d <= 0 {
		d = defaultHeartbeatSeconds * time.Second
	}
	c := &Controller{
		cfg:        cfg,
		heartbeat:  d,
		registered: map[string]bool{},
		confirmed:  map[string]bool{},
	}
	// Build the FM client (and its HTTP connection pool) ONCE and reuse it across every
	// heartbeat; the previous code allocated a fresh *http.Client per tick, churning the pool.
	c.client = c.newClient()
	c.client.SetDelivery(cfg.Delivery, cfg.Bounded)
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
	if ctx.Err() != nil {
		return
	}
	if err := c.reconcile(ctx, provider()); err != nil {
		log.Printf("fleet: reconciliation failed: %s", LifecycleErrorMessage(err))
	}

	t := time.NewTicker(c.heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.reconcile(ctx, provider()); err != nil {
				log.Printf("fleet: reconciliation failed: %s", LifecycleErrorMessage(err))
			}
		}
	}
}

// Reconcile performs one selected-roster reconciliation.
func (c *Controller) Reconcile(ctx context.Context, want []Collector) error {
	return c.reconcile(ctx, want)
}

// Unregister explicitly tears down the selected roster without registering it.
func (c *Controller) Unregister(ctx context.Context, roster []Collector) error {
	if c.authMissing() {
		return errors.New("fleet: registration credentials unavailable")
	}
	return c.cfg.Bounded.Run(ctx, ha.Delivery, func(ctx context.Context) error {
		if err := c.load(ctx); err != nil {
			return err
		}
		selected := map[string]bool{}
		for _, col := range roster {
			selected[col.ID] = true
		}
		return c.unregister(ctx, selected)
	})
}

func (c *Controller) load(ctx context.Context) error {
	if c.cfg.Store == nil || c.cfg.DryRun {
		return ctx.Err()
	}
	ids, err := c.cfg.Store.Load(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		c.registered[id] = true
	}
	return nil
}

func (c *Controller) persist(ctx context.Context, id string, present bool) error {
	if c.cfg.Store == nil || c.cfg.DryRun {
		return ctx.Err()
	}
	bound := c.cfg.Bounded
	if bound.Timeout > 0 {
		bound.Timeout = 2 * time.Second
	}
	return bound.Run(ctx, ha.Mutation, func(ctx context.Context) error { return c.cfg.Store.Set(ctx, id, present) })
}

// authMissing reports the token-less non-dry-run state where registration never happens, so the
// unregister paths must be no-ops (mirrors the old unregisterAll guard, controller.go:114-121).
func (c *Controller) authMissing() bool {
	return c.cfg.FMURL == "" || (c.cfg.Token == "" && !c.cfg.DryRun)
}

// reconcile registers any id in want not yet registered, unregisters any registered id no longer
// in want, then heartbeats every id in want.
func (c *Controller) reconcile(ctx context.Context, want []Collector) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if c.authMissing() {
		return nil
	}
	if err := c.load(ctx); err != nil {
		return err
	}
	client := c.client
	var errs []error
	wantSet := make(map[string]bool, len(want))
	for _, col := range want {
		wantSet[col.ID] = true
	}
	departed := map[string]bool{}
	for id := range c.registered {
		if !wantSet[id] {
			departed[id] = true
		}
	}
	if err := c.unregister(ctx, departed); err != nil {
		return err
	}
	// Register new + heartbeat current.
	for _, col := range want {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !c.confirmed[col.ID] {
			// Record intent BEFORE HTTP: a lost response or crash must not orphan an ID.
			if !c.registered[col.ID] {
				if err := c.persist(ctx, col.ID, true); err != nil {
					return err
				}
				c.registered[col.ID] = true
			}
			err := client.RegisterCollector(ctx, col)
			code := operationalerr.CodeOf(err)
			c.observe(ctx, fleethook.OpRegister, col.ID, 0, code)
			if err != nil {
				log.Printf("fleet: register failed: %s", operationalerr.Message(code))
				errs = append(errs, err)
				continue
			}
			c.confirmed[col.ID] = true
		}
		start := time.Now()
		receipt, err := client.GetConfig(ctx, col)
		code := operationalerr.CodeOf(err)
		c.observe(ctx, fleethook.OpHeartbeat, col.ID, time.Since(start), code)
		c.observeReceipt(ctx, col.ID, receipt)
		if err != nil {
			log.Printf("fleet: heartbeat failed: %s", operationalerr.Message(code))
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// unregisterRegistered unwinds every still-registered id (shutdown path).
// Cleanup is called only after the producer/reconcile goroutine has positively
// joined. The aggregate inherits the coordinator's global drain deadline.
func (c *Controller) Cleanup(ctx context.Context) error {
	return c.cfg.Bounded.Run(ctx, ha.Delivery, func(ctx context.Context) error { return c.unregisterRegistered(ctx) })
}

func (c *Controller) unregisterRegistered(ctx context.Context) error {
	if c.authMissing() {
		return nil
	}
	return c.unregister(ctx, c.registered)
}

func (c *Controller) unregister(ctx context.Context, ids map[string]bool) error {
	var errs []error
	client := c.client
	for id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := client.UnregisterCollector(ctx, id)
		code := operationalerr.CodeOf(err)
		c.observe(ctx, fleethook.OpUnregister, id, 0, code)
		if err != nil {
			log.Printf("fleet: unregister failed: %s", operationalerr.Message(code))
			errs = append(errs, err)
			continue
		}
		if err := c.persist(ctx, id, false); err != nil {
			return err
		}
		delete(c.registered, id)
		delete(c.confirmed, id)
	}
	return errors.Join(errs...)
}

// observe fires the fleethook seam (no-op when unset), stamping the controller's dry-run mode.
func (c *Controller) observe(ctx context.Context, op, collector string, dur time.Duration, code operationalerr.Code) {
	if c.cfg.Observe == nil {
		return
	}
	c.cfg.Observe(ctx, fleethook.Event{
		Collector: collector, Op: op, Duration: dur, DryRun: c.cfg.DryRun, ErrorCode: operationalerr.Normalize(code),
	})
}

func (c *Controller) observeReceipt(ctx context.Context, collector string, receipt Receipt) {
	if c.cfg.ObserveReceipt != nil {
		c.cfg.ObserveReceipt(ctx, collector, receipt)
	}
}

// newClient constructs a Client from the current config. DryRun is forwarded.
func (c *Controller) newClient() *Client {
	if c.cfg.DryRun {
		return NewDryRunClient(c.cfg.FMURL, c.cfg.StackID, c.cfg.Token)
	}
	return NewClient(c.cfg.FMURL, c.cfg.StackID, c.cfg.Token)
}
