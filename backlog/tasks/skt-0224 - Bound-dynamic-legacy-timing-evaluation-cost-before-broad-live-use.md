---
id: SKT-0224
title: Bound dynamic legacy timing evaluation cost before broad live use
status: To Do
assignee: []
created_date: '2026-10-25T22:00:00Z'
labels:
  - causal
  - performance
dependencies: []
ordinal: 430000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A novel-input two-legacy-sibling gate measured 6.29 seconds and13.93GB cumulative allocations, before solver iterations. Bounded caches help steady inputs but not continuous change. Correct numerical output does not establish restart, catch-up or live cadence admission.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Representative cold-start, catch-up and changing-input flows have exact elapsed/peak-memory evidence against a declared operating budget, or explicit supported-use limits
- [ ] #2 Optimizations preserve parity, numerical reference budgets, deterministic facts and sampler draws; no looser tolerance or hidden skipped path creates a performance pass
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
