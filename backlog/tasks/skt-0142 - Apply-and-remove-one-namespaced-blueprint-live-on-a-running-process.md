---
id: SKT-0142
title: Apply and remove one namespaced blueprint live on a running process
status: Done
assignee: []
created_date: '2026-10-17 15:12'
updated_date: '2026-10-17 20:02'
labels:
  - control
  - blueprints
dependencies: []
references:
  - internal/control/http.go
  - docs/custom-blueprints.md
  - docs/control-plane.md
  - internal/blueprint/load.go
priority: high
type: feature
ordinal: 265000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Custom and git blueprints are staged through the control API but only load at process start: docs/custom-blueprints.md:114-118 states the runner loads the selected snapshot at startup, writes the boot manifest and "runs with that fixed set for its lifetime. There is no hot-reload." The only live levers are enable and disable of an already-loaded blueprint (internal/control/http.go:686-687) and staging upload and delete (internal/control/http.go:848-888, staged only). An operator that drives a running process, with no restart in the loop, therefore cannot add or retire a blueprint's telemetry on demand. Owner ruling 2026-10-17 approves one generic control operation to apply or remove a blueprint live. It must not be vendor-specific, product-specific or customer-specific: it takes any blueprint under a namespace, the way uploads and git sources already do.

Scope notes. Substrate-scoped constructs carry no blueprint label and rely on declared identity plus load-time rejection of collisions (blueprint.ValidateSet, internal/blueprint/load.go:333-350), so a live apply has to repeat that check against the running set rather than only against staged files. Control state persists in the snapshot file at CONFIG_SNAPSHOT_PATH (internal/config/config.go:75,190; control.State, internal/control/control.go:46-58); the owner brief called this CONTROL_STATE, which is the same snapshot. The runner builds its set once (internal/runner/runner.go:276, Runner), so the live set needs a safe mutation seam under the tick loop.

Out of scope, deliberately. No backfill of history for an applied blueprint. No TTL or expiry: an applied blueprint lives until it is removed, and any lease or expiry policy belongs to the downstream operator or client that calls the remove operation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 An authenticated control operation applies one namespaced blueprint (identity namespace/name plus an optional opaque owner label string) to a running process without restart; a runner test shows the blueprint emits on the next tick and that GET /control/inventory lists it with its owner label and its construct and workload lanes
- [x] #2 A second control operation removes that blueprint; a runner test enumerates its lanes from GET /control/inventory before removal and asserts that after removal none of them (construct, workload, substrate-scoped, cumulative state) appear in inventory or in the next tick pushes, while every other running blueprint is unchanged
- [x] #3 Apply is atomic and rejects, with the running set unchanged and no partial lanes, any identity collision against the running set (a substrate identity claim as in blueprint.ValidateSet, or a duplicate namespaced name), invalid YAML, or a name outside the namespace rules; tests cover each rejection and assert the error names the colliding claim
- [x] #4 Applied blueprints and their owner labels persist in the CONFIG_SNAPSHOT_PATH snapshot and are restored after a restart, and a removal persists so the blueprint does not return; a restart round-trip test covers apply, remove, and the precedence between this live set and BLUEPRINT_NAMES, with that precedence documented
- [x] #5 The operation contains no vendor, product or customer specific logic or names, internal/archtest passes unchanged (TestCatalogImportIsolation), and a `just race` test applies and removes while ticks are running without a data race
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
- [x] #4 docs/custom-blueprints.md (Restart to apply) and docs/control-plane.md reconciled with the new operation, and just docs-check passes
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Landed 29583af (rc v1.6.0-rc.201) in loop50. just check and just race green on composed tree c167866f; just gen no drift. DoD #3 just dump not run: no renderer change (control plane only).
<!-- SECTION:NOTES:END -->
