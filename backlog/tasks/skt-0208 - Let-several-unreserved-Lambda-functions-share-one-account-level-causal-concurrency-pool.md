---
id: SKT-0208
title: >-
  Let several unreserved Lambda functions share one account-level causal
  concurrency pool
status: In Progress
assignee:
  - '@loop59-root'
created_date: '2026-10-25T15:15:00Z'
updated_date: '2026-10-25T22:00:00Z'
labels:
  - causal
dependencies: []
ordinal: 412000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Owner ruling Q04 (loop58): a bound function's reserved concurrency stays one resource per function, but real unreserved functions share the account's regional concurrency pool, so one busy function can throttle others. Add a shared account-level concurrency resource that several unreserved functions bind to. Owner wants this emitted promptly into the next loop.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Several unreserved functions bound to one account pool throttle together when their combined concurrency exceeds it
- [ ] #2 Reserved-concurrency bindings and unbound functions unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Choose the smallest generic shared-concurrency grouping seam over heterogeneous per-function resources, preserve unbound behavior and scope identity, implement the Lambda account projection from joint group windows, and prove borrowing, enforcement, rejection/resends and account isolation through public loading.
<!-- SECTION:PLAN:END -->
