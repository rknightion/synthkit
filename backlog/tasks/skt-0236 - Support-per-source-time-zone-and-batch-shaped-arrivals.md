---
id: SKT-0236
title: Support per-source time zone and batch-shaped arrivals
status: To Do
assignee: []
created_date: '2026-10-26T07:05:00Z'
labels:
  - realism
dependencies: []
priority: high
ordinal: 442000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Each source follows its own regional day, and arrivals can come in batches at a configured cadence instead of a smooth curve.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
