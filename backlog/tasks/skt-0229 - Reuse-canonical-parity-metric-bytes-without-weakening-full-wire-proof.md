---
id: SKT-0229
title: Reuse canonical parity metric bytes without weakening full wire proof
status: In Progress
assignee: []
created_date: '2026-10-26T04:57:00Z'
updated_date: '2026-10-26T11:09:00Z'
labels:
  - performance
dependencies: []
priority: medium
type: enhancement
ordinal: 435000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Exact-source race profiles of the integrated app candidate measured material old parity-helper cost: full metric records are JSON-encoded for lexical sorting and then encoded again for wire comparison. Sorting/first encoding sampled16.20s and second wire serialization13.52s cumulative CPU; complete parity took112.24s under profiling. The helper is byte-identical to the published baseline, so this is not a newly introduced parity regression or demonstrated sole cause of the aggregate600-second timeout. Remove the proven redundant serialization without reducing the wire proof or reopening the numerical evaluator.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Canonical metric bytes exactly match the unchanged old full-series lexical sort plus array JSON encoding across all13 declarations and52 existing variants and adverse complete series shapes; an omitted or changed record, label or value is detected by a genuine semantic negative control.
- [ ] #2 All existing real emitters, seeds, declarations, variants, populations, count and trace pins, strict/tolerant comparisons, fixtures and600-second timeout remain unchanged; no emitter-result cache, skipped case, golden regeneration, key-only projection or comparator shortcut weakens coverage.
- [ ] #3 Measured helper CPU/allocation evidence is retained with honest scope; no whole-package budget or landing claim is inferred without the exact final integrated gate passing.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Read exact existing helper/comparator and preserved race profiles; retain the original serializer as an independent oracle, reuse complete already-encoded records in identical lexical order, prove full raw-byte equivalence and semantic omission/value negatives, then focused real parity validation and representative helper cost. One bounded correction only; root owns native review, CodeRabbit delta and final exact full gate. Comparator/tree decoding optimization is excluded. Original aggregate-cost lineage and convergence clock are not reset by this prerequisite task.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Closeout: exact two-file0dbd correction committed locally asaf34, not published. Native and DS PASS; CodeRabbit complete2of2/no findings; all13 declarations/52 actual executions raw-byte equal with semantic negatives, focused parity racePASS82.72s. Representative encoder CPU/allocation savings are scoped only. Exact final full gate still fails app-race600.139s, so DoD/landing remain blocked by aggregate owner stop. No further correction or comparator shortcut; retain candidate/proofs for owner-approved resumption.

loop61: resumed under the owner's Q04 reopening (charter) after the realism set deployed; needs-owner removed.
<!-- SECTION:NOTES:END -->
