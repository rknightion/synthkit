---
id: SKT-0209
title: Drive database observability from the same causal state as a bound database
status: In Progress
assignee:
  - '@loop59-root'
created_date: '2026-10-25T15:15:00Z'
updated_date: '2026-10-25T19:45:00Z'
labels:
  - causal
dependencies: []
ordinal: 413000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Owner ruling Q07 (loop58): a causally bound database currently rejects enabling dbo11y on the same database. In a real incident a saturated database shows in its own monitoring (connections, waits, slow queries) when the app slows. Bind the dbo11y lane to the database's causal resource and lift the restriction.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 dbo11y connection, wait and slow-query signals for a bound database follow its causal resource state
- [ ] #2 Unbound databases and their dbo11y output unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Compose accepted PostgreSQL and MySQL adapter foundations as local baseline, then wire existing resourceBinder into both dbo configs, remove only blanket bound-dbo rejection, reconcile canonical contracts/authoring skill, and prove real YAML/runner/cloud-plus-dbo coherence and inventory without new scheduler seams or invented wait categories.
<!-- SECTION:PLAN:END -->
