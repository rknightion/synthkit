---
id: SKT-0107
title: >-
  Keep live app trace identities unique across restarts without destabilizing
  inventory
status: In Progress
assignee:
  - '@loop-root'
created_date: '2026-10-08 02:19'
updated_date: '2026-10-08 09:55'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 229000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Final aggregate review found that app request correlation IDs depend only on workload/environment/cluster and minter-local tick/request ordinals. Restarting an identical minter repeats TraceIDs and can merge unrelated live traces across restarts or handoffs. A random nonce alone also changes agent/tool choices because they hash SpanID, reintroducing full dump drift. Preserve the intended deterministic inventory choice while separating live correlation uniqueness. No repair was dispatched because the owner closed the run.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Live requests from two equivalent newly initialized minters have distinct correlation identities across restart/handoff
- [ ] #2 Fixed-input agent/tool inventory remains deterministic without filtering or test-special-case behavior
- [ ] #3 Failing-first public-boundary regression proves the repeated-ID bug and a proportionate gate/review verifies the repair
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop2 attempt 3: reproduce repeated live identities at public minter boundary; separate correlation uniqueness from deterministic inventory selection; gate and independent review.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop1: new follow-up implementation attempts 0; origin SKT-0106 (deterministic app inventory) consumed 2 implementation attempts, preserve its ceiling history for a direct repair instead of resetting counters. Full 10-file aggregate CodeRabbit completed with one verified major; no fix or extra review was dispatched after owner close-out. Resume in an authorized run with a design that separates deterministic inventory selection from live correlation uniqueness. Evidence /tmp/H92I-root-rescue/routine-aggregate-coderabbit.log; do not blindly add randomness to inventory selection.

Owner decision: authorized as the next small repair. Keep live correlation/trace uniqueness across minter reconstruction separate from deterministic inventory selection; carry the two prior attempts from the deterministic-inventory work.
<!-- SECTION:NOTES:END -->
