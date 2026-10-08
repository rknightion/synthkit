---
id: SKT-0109
title: Resolve late Faro POST in composed lease HA gate
status: Parked
assignee:
  - '@loop-root'
created_date: '2026-10-08 15:06'
updated_date: '2026-10-08 15:10'
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
- [ ] #2 Composed main gate green on an authorized repaired or restored source candidate
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Read failure/source and run only targeted bounded reproduction if needed; no implementation or gate rerun until classification and authority resolved.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Bounded read-only diagnosis: major test-oracle defect at internal/runner/delivery_test.go27-35,62-74; asynchronous server arrivals may increment after client worker join even when admitted before deadline. Test never revokes gate.20 targeted repetitions pass, not resolution of historical red. No runtime defect or revert justified. Attempts0 implementation; owner must authorize test-only direct admission/completion observation and fake-server lifetime synchronization, no assertion loosening. Main composed gate remains red; resume exact narrow correction and new gate only after owner consent.
<!-- SECTION:NOTES:END -->
