---
id: SKT-0101
title: Profile and reduce the per-process memory baseline
status: To Do
assignee: []
created_date: '2026-10-07 20:31'
labels:
  - performance
dependencies: []
ordinal: 223000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A single small blueprint has a heap floor of about 250 MiB (docs/kubernetes.md measurements), mostly fixed cost rather than per-blueprint. Deployments running many small instances pay it per pod, twice under HA. Profile the floor on Linux under live delivery (not DRY_RUN), identify the fixed allocations (registry, catalogue, queue preallocation), and reduce what is safe.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Heap profile of a one-blueprint process on Linux attached to the task
- [ ] #2 Baseline reduced with measurements before and after; no inventory change in just dump
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
