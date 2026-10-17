---
id: SKT-0151
title: >-
  Admit live Synthetic Monitoring per blueprint instead of re-validating the
  whole snapshot
status: To Do
assignee: []
created_date: '2026-10-17 21:48'
labels:
  - synthetic-monitoring
  - control
dependencies: []
priority: low
ordinal: 274000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
With live SM admission (SKT-0146) the SM private snapshot covers the boot set plus the persisted live set, and the registration must match that whole set. An uncovered live SM apply or a live SM DELETE therefore strips SM from every blueprint, boot ones included, at the next restart or HA leader failover until sm-provision runs again. Admit a blueprint when the stored registration validates against the stored snapshot and that snapshot's checks for that blueprint (Source == blueprint, plus their probes) equal its declared specs, so one blueprint's change cannot strip another's SM. Changes the guarded fail-closed contract: needs its own security review.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A live SM apply or DELETE of one blueprint does not strip SM from other blueprints whose registered checks are unchanged, across restart and failover
- [ ] #2 Unregistered or mismatched SM for the changed blueprint still never emits; tests cover file and kubernetes backends
- [ ] #3 docs/synthetic-monitoring.md describes per-blueprint admission
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
