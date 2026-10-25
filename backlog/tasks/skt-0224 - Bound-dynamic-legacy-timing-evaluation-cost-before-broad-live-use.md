---
id: SKT-0224
title: Bound dynamic legacy timing evaluation cost before broad live use
status: In Progress
assignee: []
created_date: '2026-10-25T22:00:00Z'
updated_date: '2026-10-25T22:31:00Z'
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

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop60: map hot paths first, then optimize and measure cold/catch-up/changing inputs on accepted composed stack within model tick budget; no numerical or sampler relaxation.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner closeout: Runtime-cost blocker remains: observed 6.29s per gate and 13.93GB allocation churn. Optimization unattempted; resume exact cold/catch-up/dynamic profiling before broad live use.
<!-- SECTION:NOTES:END -->
