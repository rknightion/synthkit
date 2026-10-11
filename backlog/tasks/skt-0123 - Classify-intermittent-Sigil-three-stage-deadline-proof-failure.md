---
id: SKT-0123
title: Classify intermittent Sigil three-stage deadline proof failure
status: In Progress
assignee:
  - '@loop-root'
created_date: '2026-10-11 14:34'
updated_date: '2026-10-11 15:30'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 245000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Clean main678ba23bfa249e11cf17731caab4a7e6fa965a7a just check failed TestHAActualSigilThreeStagesShareDeadline in internal/runner/delivery_test.go177: observed zero confirmed items after107.146584ms rather than the expected one. Test gives each real HTTP handler a70ms delay under a100ms absolute operation deadline and expects two stage paths. Source timing dependence is visible but environment versus product cause is not proved. Scanner correction has no runtime diff and did not cause this failure. No unchanged rerun or weakened assertion was used; this finding must not silently become a permanent baseline waiver.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Classify the failure from bounded exact-source and actual HTTP boundary evidence; distinguish product defect from scheduling-dependent test premise.
- [ ] #2 Any authorized correction preserves one absolute three-stage deadline, actual partial-delivery accounting, no later-stage admission after cancellation, and joined workers; no timeout increase or assertion weakening.
- [ ] #3 Prove corrected behavior with fail-first evidence and exact candidate just check plus unaffected delivery and HA regressions.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Read the existing failed exact-base log and delivery/queue/sink code without rerunning unchanged checks. Determine proved cause versus uncertainty; recommend narrowly scoped repair only if needed for the final composed gate.

Bounded test-only proof correction: replace unguaranteed first-response scheduling premise with direct inherited deadline identity, real handler-synchronised cancellation and explicit client/queue/server joins; separately retain actual absolute expiry proof. Preserve exact partial count/two stages/no score assertions. No runtime or timeout changes. Guarded proof review and local full base/candidate gate before serial root landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
One test-only cycle: unchanged deadline100ms/margin100ms/Flush220ms bounds, actual workflow HTTP receipt cancellation, exact inherited deadlines, one confirmed generation/no score, positive attempts/queue/server joins, separate actual expiry. Base/candidate just check0; four scratch faults fail correct assertions. Full1file CodeRabbit complete0major1validminor: finite100ms receipt/cancellation scheduling can still flake, left without timeout increase. Independent guarded equivalence PASS. Historical timeout causeUNKNOWN; pre-existing between-stage aggregate-observation gap outside scope, recorded separately as SKT-0124 (investigate missing aggregate observation on cancellation). Root landing/exact CI/composed proof pending.
<!-- SECTION:NOTES:END -->
