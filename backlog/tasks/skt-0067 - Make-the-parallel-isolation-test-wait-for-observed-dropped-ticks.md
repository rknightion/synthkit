---
id: SKT-0067
title: Make the parallel-isolation test wait for observed dropped ticks
status: Done
assignee:
  - '@codex'
created_date: '2026-09-08 15:14'
updated_date: '2026-09-08 15:41'
labels: []
dependencies: []
type: bug
ordinal: 163000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The integrated gate failed TestRunParallelIsolation after its fixed 400 ms window expired with no reported dropped tick. A slow cycle can consume the queued pre-overrun ticker timestamp before the later timestamp exposes coalescing, so the assertion depends on scheduler timing rather than the isolation contract.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The test retains all existing isolation and dropped-tick assertions and finishes on observed success under a bounded timeout.
- [x] #2 Retained failing-before output, repeated focused and race checks, CodeRabbit review and the full local gate validate the correction.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Keep production scheduling unchanged. Replace the fixed observation window with cancellation after all existing evidence predicates hold, backed by a five-second deadline. Retain the integrated failure, exercise repeated focused and race runs, then review and gate the root-owned test change.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Test-only correction retains original isolation predicates and waits for observed success within five seconds; production scheduling unchanged. Failure retained in integrated-check.log. Twenty focused repetitions, ten race repetitions, zero CodeRabbit findings and full exact-source gate pass. Evidence in codex/scratch/wave-2026-09-14/: parallel-isolation-repeat.log, parallel-isolation-race.log, coderabbit-runner-test.ndjson, p3-exact-le-sha-check.log. Generation and inventory comparison are not applicable to test-only change.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Test-only correction retains original isolation predicates and waits for observed success within five seconds; production scheduling unchanged. Failure retained in integrated-check.log. Twenty focused repetitions, ten race repetitions, zero CodeRabbit findings and full exact-source gate pass. Evidence in codex/scratch/wave-2026-09-14/: parallel-isolation-repeat.log, parallel-isolation-race.log, coderabbit-runner-test.ndjson, p3-exact-le-sha-check.log. Generation and inventory comparison are not applicable to test-only change.
<!-- SECTION:FINAL_SUMMARY:END -->
