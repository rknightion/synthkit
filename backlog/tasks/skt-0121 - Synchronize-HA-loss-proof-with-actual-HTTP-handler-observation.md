---
id: SKT-0121
title: Synchronize HA loss proof with actual HTTP handler observation
status: Done
assignee:
  - '@loop-root'
created_date: '2026-10-11 00:30'
updated_date: '2026-10-11 08:44'
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
- [x] #1 The drain loss proof waits for acknowledgement derived only from its existing real HTTP handler recording the intended request, never from sender entry or a synthetic probe.
- [x] #2 All original scenarios, partial-batch exclusion, no admission after fence, hard exit assertions and existing five-second subprocess and production time budgets remain unchanged.
- [x] #3 A controlled delayed-observation regression is seen failing on the old synchronization and passing after repair; focused race check and full just check pass on the exact candidate.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Extend the existing drain fixture with one delayed-observation process-edge regression; preserve five-second bound and all assertions. Add parent-to-child acknowledgement after the actual handler records /push, before drain/revocation. Keep the existing post-observation pause without claiming certified retry-sleep entry. Observe fail-first then focused/full gates, guarded CodeRabbit and independent security review before root landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
One test-only implementation cycle. Delayed-handler negative control failed the unchanged no-real-HTTP assertion before repair; parent ACK follows actual handler observation, same five-second subprocess bound, all original scenarios/fence/partial/hard-exit assertions preserved. Focused race/fullgate green, CodeRabbit complete0findings and independent securityPASS. Exact landed428278ae3d07ddf75b12c863dfe1ad9508fbd001 composedjustcheck0 and exactCIgreen; no retry-sleep entry claim or production change. Remaining default generation/inventory DoD entries are not applicable to test-only synchronization.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Strengthened real HTTP loss proof with bounded parent-handler acknowledgement; deterministic delayed observation catches old schedule. Exact landed SHA local composed gate and CI success, all original safety assertions retained.
<!-- SECTION:FINAL_SUMMARY:END -->
