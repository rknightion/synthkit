---
id: SKT-0100
title: Add Fleet Management cleanup for handoffs and teardown
status: In Progress
assignee:
  - '@loop-root'
created_date: '2026-10-07 20:31'
updated_date: '2026-10-10 23:45'
labels:
  - feature
  - ha
  - fleet
dependencies:
  - SKT-0094
ordinal: 222000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The FM controller exits without unregistering (StartDynamic, internal/fleet/controller.go), which is right for restarts, but a new leader starts with an empty registered set, so collectors the old leader registered that the new roster lacks are orphaned (k8s-mirror rosters whose scaling differs). Teardown also has no way to unregister without synthkit's roster code. Add: the leader persists its registered set in the state backend and a new leader unregisters departed IDs; a one-shot -fleet-unregister mode that unregisters every collector the selected set would register, then exits.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 New leader unregisters IDs in the persisted set that its roster lacks
- [ ] #2 -fleet-unregister removes all collectors for the selected set and exits 0
- [ ] #3 Restart without teardown still leaves collectors registered
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Persist registered collector set through existing gated backend adapter; handoff reconciliation and one-shot teardown, preserving restart semantics; fake-boundary fail-first tests, env alignment, gate and guarded review.

Use one aggregate selected-roster FM controller with empty-roster reconciliation so a persisted registered set never lets sibling controllers remove each other. Keep existing state-backend signatures and HA gating unchanged; narrowly adapt FM restart cleanup wiring and runner fleet proof.
<!-- SECTION:PLAN:END -->
