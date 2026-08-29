---
id: SKT-0035
title: >-
  Make upgrade, rollback, and snapshot persistence reproducible from public
  releases
status: To Do
assignee: []
created_date: '2026-08-29 19:05'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: medium
type: feature
ordinal: 126000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenarios F2-F4 could prove the running digest identity but could not locate a second verified public digest for upgrade/rollback, and a bounded restart did not establish selected-blueprint and control-state persistence. The documented lifecycle path is not independently reproducible from a clean clone.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The documentation identifies two immutable published digests suitable for an upgrade and rollback exercise
- [ ] #2 Upgrade and rollback verify reported version and revision against each digest
- [ ] #3 Counter behavior across restart is checked without interpreting resets as spikes
- [ ] #4 Selected blueprints and control state are read back after restart from a config snapshot
- [ ] #5 The lifecycle acceptance path is runnable without private release metadata
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
