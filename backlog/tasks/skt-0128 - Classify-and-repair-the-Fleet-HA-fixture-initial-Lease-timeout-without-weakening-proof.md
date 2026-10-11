---
id: SKT-0128
title: >-
  Classify and repair the Fleet HA fixture initial Lease timeout without
  weakening proof
status: Parked
assignee:
  - '@loop-root'
created_date: '2026-10-11 22:10'
updated_date: '2026-10-11 22:11'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 251000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
An exact clean detached pre-publication composed gate at ef9aa7e5eed726632fee6dc41327570d9591f1e6 failed in the race tier: TestFleetHAGracefulRestartKeepsIDsAndExplicitTeardown/teardown child failed its initial named Lease GET at the existing 100ms deadline, before any Fleet registration/unregistration. The same source in the earlier lane gate passed; plain tests passed in the red composed run. Cause is unknown, not established as timing flake or runtime defect. This blocks publication of the admitted chart portability, terminal accounting and SM-fixture candidates. No failed candidate was published. Existing completed planned-release shutdown work concerns a different failure.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Exact source/baseline and bounded diagnostic evidence classify the initial local named Lease lookup failure without calling isolated green proof a cleared composed gate.
- [ ] #2 Any evidenced test-only repair is fail-first and preserves the 100ms request bound, existing lifecycle/admission/fencing/cleanup assertions, no sleeps, retries or production-contract changes; otherwise the exact missing authority/evidence is parked.
- [ ] #3 A repaired exact candidate receives independent review and one full composed gate before publication, then exact-SHA CI; no unchanged failed full-gate rerun is substituted.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Read exact red/green logs and unchanged fixture source; one bounded focused race diagnostic. Root decides source-proven small test-only prerequisite scope versus owner/evidence park. Preserve all original timing and proof contracts, then review and exact composed gate only on a changed candidate if repair is warranted.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop16: implementation attempts0, one bounded read-only race diagnostic3repetitions passed2.674s; exact red/green fixture and production dependency bytes unchanged. Original initial named Lease GET100ms timeout cause remains unknown; no source-proven test-only repair, original request/scheduling evidence absent. This is not the completed planned-release HTTP shutdown defect and isolated green does not clear the composed failure. No probe, timeout, assertion, fixture, runtime or test changed; no unchanged full-gate rerun. Park dependency: resume with actual request/scheduling evidence establishing a bounded fixture repair or separate owner-authorized diagnostic scope. Later distinct candidates require their own full exact gates and CI; prior red remains disclosed.
<!-- SECTION:NOTES:END -->
