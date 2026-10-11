---
id: SKT-0121
title: Synchronize HA loss proof with actual HTTP handler observation
status: In Progress
assignee:
  - '@loop-root'
created_date: '2026-10-11 00:30'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 243000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The hard-loss subprocess proof signals sender entry before the HTTP sink call, then revokes after a fixed 40ms sleep. Two full race gates reported no observed HTTP request. The unchanged helper call path contains no Fleet controller and no causal delivery change, so sender entry is not a valid proxy for handler observation. Required observation, admission-fence and process-exit assertions must remain intact; this is a test synchronization prerequisite, not a production behavior or budget change.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The drain loss proof waits for acknowledgement derived only from its existing real HTTP handler recording the intended request, never from sender entry or a synthetic probe.
- [ ] #2 All original scenarios, partial-batch exclusion, no admission after fence, hard exit assertions and existing five-second subprocess and production time budgets remain unchanged.
- [ ] #3 A controlled delayed-observation regression is seen failing on the old synchronization and passing after repair; focused race check and full just check pass on the exact candidate.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Extend the existing drain fixture with one delayed-observation process-edge regression; preserve five-second bound and all assertions. Add parent-to-child acknowledgement after the actual handler records /push, before drain/revocation. Keep the existing post-observation pause without claiming certified retry-sleep entry. Observe fail-first then focused/full gates, guarded CodeRabbit and independent security review before root landing.
<!-- SECTION:PLAN:END -->
