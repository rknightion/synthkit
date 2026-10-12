---
id: SKT-0124
title: Investigate missing Sigil aggregate observation on between-stage cancellation
status: Done
assignee:
  - '@loop-root'
created_date: '2026-10-11 15:20'
updated_date: '2026-10-12 07:57'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 246000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Read-only review of current Sigil delivery finds bare context-error returns between confirmed stages in internal/sink/sigil/sigil.go134-136 and147-149, before aggregate observation is emitted. Neither the original nor corrected three-stage deadline test exercises that window. This is source-visible potential missing operational accounting, not the established cause of the historical zero-confirmation test failure or a demonstrated live incident. The current proof-only correction intentionally leaves production behavior untouched.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A bounded fake HTTP and gate reproduction establishes whether cancellation after a confirmed stage emits exactly one truthful aggregate event with confirmed count and terminal error.
- [x] #2 Any separately authorized production repair preserves one absolute whole-write deadline, no later-stage admission after cancellation, positively joined work and unchanged emission behavior.
- [x] #3 Source and test evidence classify observed behavior without presenting the historical scheduling failure as a proven runtime cause.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Convert retained cancellation reproduction into a fail-first real test; repair exactly one terminal aggregate for admitted nonempty writes, preserve deadline/join/admission and queue-loss contracts; compare inventory and gate/review before exact CI.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Bounded investigation: reproduced on b8a0ef1da85ed9b0942be2398d35083ce90561f5, no production edits. Command go test ./internal/sink/sigil -run ^TestS124ScratchAggregateBetweenConfirmedStages$ -count=3 -timeout=20s -v, one invocation/three repetitions/nine subtests, exit1 intentional assertion failure. Three uncanceled controls passed; after one and two confirmed HTTP stages both windows reproduced all three times: confirmed=1 or2, handler_records=1 or2, later_stage_posts=0, bare_canceled=true, observations=0. Exact scratch source SHA256 523df7c722c8b0e318b5ee55201d719efec6081921356af3c2356b6abc476fa0 retained as probe.test.evidence. Real gate/HTTP response consumption and handler confirmation precede cancellation; no sleeps or invented success. Bare returns in internal/sink/sigil/sigil.go:134-135 and147-148 bypass aggregate observe. Recommendation: exactly one sanitized terminal aggregate per admitted nonempty write, confirmed logical Items, last actual HTTP status202, terminal canceled code and full-write duration; preserve caller context error and no later-stage admission. Consumers: both command callback fanouts, selfobs push outcomes/items/duration, pushstatus error/readiness folds, control/optional lane status; queue dropped envelopes remain separate accounting. Define pre-admission cancellation and per-blueprint success-only rollup explicitly before any repair. Source-only pre-first-stage gap remains adjacent; this is deterministic reachability, not incident frequency or the historical scheduling failure cause. Zero implementation attempts, no CodeRabbit for scratch investigation, no criteria checked; remains To Do for separately authorized production repair.

loop16: one implementation cycle, one independent routine reviewPASS, no CodeRabbit by owner decision. Genuine both-window fail-first3repetitions red before then green, focusedrace0, inventory byte-identical. One terminal aggregate for each admitted nonempty write retains confirmed logical items, last actual status, terminal error and whole-write duration; pre-admission cancellation silent, queue-envelope loss separate; no emission/deadline/join weakening. Exact landed58ebd188b0ade45972d7eff75f59496b4e2a7478 full lane gate0 and composedC124gate0, CI38181876319 all11requiredjobs success. Evidence is deterministic source reachability, not incident frequency or historical scheduling cause. just gen not applicable, no blueprint/config/skill surface changed. Baseline gate extra invocation by runner disclosed separately, no inferred clearance of prior unrelated Fleet timeout.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Implemented truthful terminal write accounting and cancellation regressions; local HTTP/real gate proofs, unchanged inventory, independent review, exact composed gate and exact all-required CI green. Routine CodeRabbit skipped as granted.
<!-- SECTION:FINAL_SUMMARY:END -->
