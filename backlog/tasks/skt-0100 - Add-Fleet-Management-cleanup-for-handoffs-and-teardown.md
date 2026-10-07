---
id: SKT-0100
title: Add Fleet Management cleanup for handoffs and teardown
status: To Do
assignee: []
created_date: '2026-10-07 20:31'
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
