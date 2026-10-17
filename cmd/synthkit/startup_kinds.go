// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"log"
	"slices"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/construct/sm"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core"
)

// applyStartupDisabledKinds validates STARTUP_DISABLED_KINDS with the same check as POST
// /control/kinds, against every construct kind the catalog registers (not the kinds the boot
// blueprints happen to declare, so setup mode and partial boot sets accept any registrable kind and
// only a typo fails), and removes the Synthetic Monitoring lane when that kind is listed, so no
// provisioning snapshot is published for it. Seeding the control state is separate
// (seedStartupDisabledKinds) because it needs the store.
func applyStartupDisabledKinds(reg *core.Registry, resolved []*blueprint.Resolved, kinds []string) error {
	if len(kinds) == 0 {
		return nil
	}
	if err := control.ValidateKinds(kinds, control.Schema{Kinds: reg.ConstructKinds()}); err != nil {
		return fmt.Errorf("STARTUP_DISABLED_KINDS: %w", err)
	}
	if slices.Contains(kinds, sm.Kind) {
		for _, bp := range resolved {
			kept := bp.Constructs[:0]
			for _, instance := range bp.Constructs {
				if instance.Kind != sm.Kind {
					kept = append(kept, instance)
				}
			}
			bp.Constructs = kept
		}
	}
	return nil
}

// seedStartupDisabledKinds unions the startup kinds into the control store before the first
// snapshot is applied. A file store that cannot persist still holds the kinds in memory and only
// logs, like the startup state-volume probe; strict and backend stores fail startup.
func seedStartupDisabledKinds(ctx context.Context, store *control.Store, kinds []string) error {
	if len(kinds) == 0 {
		return nil
	}
	if _, err := store.SeedDisabledKinds(ctx, kinds); err != nil {
		if store.UsesBackend() || store.HAEnabled() {
			return fmt.Errorf("STARTUP_DISABLED_KINDS: persist: %w", err)
		}
		log.Printf("control: startup disabled kinds not persisted: %v", err)
	}
	log.Printf("control: disabled kinds from first tick: %v", store.Snapshot().DisabledKinds)
	return nil
}

// applyActivatedControl is the leader's activation step: seed the startup kinds (not during a Fleet
// teardown, which must not depend on or alter kind state) and apply the resulting snapshot to the
// runner before production starts.
func applyActivatedControl(ctx context.Context, cfg *config.Config, leader *haView) error {
	if !cfg.FleetUnregister {
		if err := seedStartupDisabledKinds(ctx, leader.store, cfg.StartupDisabledKinds); err != nil {
			return err
		}
	}
	leader.runner.ApplyControl(leader.store.Snapshot())
	return nil
}
