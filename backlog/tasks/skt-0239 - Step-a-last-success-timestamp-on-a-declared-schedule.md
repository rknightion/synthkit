---
id: SKT-0239
title: Step a last-success timestamp on a declared schedule
status: To Do
assignee: []
created_date: '2026-10-26T07:35:00Z'
labels:
  - realism
dependencies: []
priority: high
ordinal: 445000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A last-success timestamp value for scheduled jobs that advances only at the declared schedule period and phase (and not during a stall), instead of every tick, so a job that runs every few minutes does not claim a success each minute.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
