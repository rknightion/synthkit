---
id: SKT-0234
title: Drive histogram observation counts from the same arrival counters
status: To Do
assignee: []
created_date: '2026-10-26T07:05:00Z'
labels:
  - realism
dependencies: []
priority: high
ordinal: 440000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Histogram observation counts follow the arrival counters the blueprint declares, instead of a separate fixed plateau rate, so counts and timings are internally consistent.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
