---
id: SKT-0109
title: Resolve late Faro POST in composed lease HA gate
status: Done
assignee:
  - '@loop-root'
created_date: '2026-10-08 15:06'
updated_date: '2026-10-09 22:04'
labels: []
dependencies: []
type: bug
ordinal: 231000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
After integrating the reviewed lease HA candidate, composed just check failed TestHAActualFaro5000WholeWrite: late POSTs/workers before121 after122 active0. Isolated candidate gate passed, so code defect versus timing flake is unresolved. The accepted proof cycle authorized no further implementation; investigate without changing tests or code, then retain precise repair requirements for owner approval.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Classify the actual late POST with source/runtime evidence, without weakening the test
- [x] #2 Composed main gate green on an authorized repaired or restored source candidate
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Read failure/source and run only targeted bounded reproduction if needed; no implementation or gate rerun until classification and authority resolved.

Owner-authorized loop5 test-only direct admission/completion oracle and fake-server lifetime fix; injected late POST must fail once, race count50, full gate, high review, composed main gate.

Reapply retained Faro oracle unchanged; test-only deterministic terminal admission coverage and reconciled coordinator observer; negative mutation, focused race repetitions, full gate, high and security reviews, separate root commits and composed main gate.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Bounded read-only diagnosis: major test-oracle defect at internal/runner/delivery_test.go27-35,62-74; asynchronous server arrivals may increment after client worker join even when admitted before deadline. Test never revokes gate.20 targeted repetitions pass, not resolution of historical red. No runtime defect or revert justified. Attempts0 implementation; owner must authorize test-only direct admission/completion observation and fake-server lifetime synchronization, no assertion loosening. Main composed gate remains red; resume exact narrow correction and new gate only after owner consent.

loop5 attempt1 completed; late-POST injection failed as required and focused runner race count50 passed, but full just check red in TestHACoordinatorRenewalLossSuppressesCleanup/sigterm (seal after exit1). Read-only diagnosis: earlier-admitted invocation resumes during test exit300ms delay; observer logs entry not successful sealing. Independent existing contract/observer discrepancy, no runtime violation established; targeted40 pass does not clear red. Faro-only owned-file retry cannot repair cause, so remaining retry unused. Candidate uncommitted in the retained local candidate worktree. Resume after owner authorizes separate HA contract/test correction, then exact candidate gate and high review; no land.

loop6 (reconstructed by planner after the loop was interrupted by a host OS update): retained oracle committed b88833e, observer reconciliation 50892a2, both reviews accepted 0 critical/major. Composed just check exit 0 at 14719af3aa2714e4823b4c6229e68a6f377aa4f2 (evidence operator-held local evidence). CI 37855685768 at that SHA was red on an unrelated control-dash race-package timeout, tracked separately; CI 37889782301 at bb4c4e2 is green.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Late Faro POST resolved test-side: direct admission/completion oracle plus HA observer reconciled to the terminal local-admission contract. Verified by negative injections, focused race repetitions and composed just check exit 0 at 14719af.
<!-- SECTION:FINAL_SUMMARY:END -->
