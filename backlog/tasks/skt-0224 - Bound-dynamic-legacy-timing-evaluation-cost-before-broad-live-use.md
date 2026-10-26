---
id: SKT-0224
title: Bound dynamic legacy timing evaluation cost before broad live use
status: In Progress
assignee: []
created_date: '2026-10-25T22:00:00Z'
updated_date: '2026-10-26T11:09:00Z'
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

Second loop60 convergence stop: latest unlanded two-worker candidate remains unapproved. First fresh confirmation measured 4.140158258s above the unchanged four-second margin; remaining calls/processes stopped unmeasured. Ordered cold still exceeds 300s. Independent native review found an uncontained helper-goroutine panic and a caller-panic path that skips retained-plan recount. No more algorithm, lifecycle or repeated confirmation repair without owner reopening. Prior serial correctness verdicts remain scoped to their original bytes; no timed, concurrent, private or whole-runner admission. Candidate, failure evidence and offline-only limit proposal retained; private deployment stays held.

Loop60 closeout: Second convergence stop remains binding: fresh4.140158258s exceeds4s, ordered cold exceeds300s and native worker panic/accounting findings remain open. No algorithm/lifecycle repair, confirmations, private-runtime admission or renamed bypass without explicit owner reopening; unsupported operating-limit proposal remains unapplied.

Owner explicitly reopens up to TWO bounded defect-repair rounds on senior/Astra, limited to helper panic containment, complete join and post-panic accounting. Preserve all prior failure evidence, numerical semantics, four-second margin and operating limits; no general optimization/reset. After correctness, exact whole-private-runner startup/catch-up/fault/RSS and integration gates decide admission; no deployment by approval alone.

Closeout: numerical worker panic/join/accounting round1 is source-correctness approved on both architectures, but unlanded integrated source still fails the app600-second race budget. Generic fresh4.140158s above4s and orderedcold above300s remain; private whole-runner measurement never ran because its supervisor reached a separate second owner stop. Explicit offline operating limits are authored, not five-second runtime admission. No further algorithm work or deployment; remaining bounded numerical lifecycle allowance is not repurposed to bypass owner holds.

loop61: resumed under the owner's Q04 reopening (charter) after the realism set deployed; needs-owner removed.
<!-- SECTION:NOTES:END -->
