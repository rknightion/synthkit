---
id: SKT-0162
title: Run timing-sensitive HA subprocess tests in an isolated race invocation
status: To Do
assignee: []
created_date: '2026-10-22T04:24:00Z'
labels:
  - test
  - ci
  - ha
dependencies: []
priority: medium
ordinal: 305000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Owner ruling 2026-10-22 (loop52 Q05): SKT-0128 Fleet initial Lease 100ms and SKT-0158 cause 2 (100ms startup lookup, 300ms renew) miss under gate CPU starvation (gate-1, 256 hogs: 4/1600 and 65/130). Keep every bound; run the cmd/synthkit HA subprocess timing tests in their own -race invocation after the parallel sweep (justfile race recipe plus the CI go job), excluded from the parallel sweep so they still run exactly once.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The named HA timing tests run exactly once per race tier, in a separate invocation, with all bounds unchanged
- [ ] #2 Under the gate-1 load reproduction the isolated invocation is green where the shared sweep was red
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
