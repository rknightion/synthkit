---
id: SKT-0175
title: Reject non-finite incident intensity values
status: Done
assignee:
  - '@loop54'
created_date: '2026-10-23T19:51:00Z'
updated_date: '2026-10-23T20:42:00Z'
labels: []
dependencies: []
priority: low
type: bug
ordinal: 326000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Source review of scheduled-intensity presence found that existing numeric range comparisons do not independently reject NaN, which is not a member of the documented [0,1] domain. Keep this separate from authored zero-versus-omission semantics. Confirm the public-loader behavior before changing it; no broader Active-only, live activation, timing-bound or emitter change is implied.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Public-loader regression rejects NaN and non-finite incident/effect intensity without accepting it as an omitted default or usable numeric value
- [x] #2 Omitted, explicit zero and finite fractional values retain their documented scheduling and live semantics; mode/target/scope validation is not bypassed
- [x] #3 Meaningful base red and candidate green evidence crosses the actual loader, with canonical guidance reconciled if changed
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Confirm non-finite public-loader behavior on unchanged production code first, reject only values outside the existing finite [0,1] contract, preserve scope and zero/omission/live behavior; focused remote proof and guarded review before integration.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Loop54 landed ff9248e7e96f557a900ae341e51b0080764c436d. Closed-range predicate rejects NaN; public loader accepted both NaN forms on base and rejects after fix, infinities already rejected. Finite zero/fraction/omission and all target/scope/live semantics unchanged. Guarded review/dsreview PASS, complete composed CodeRabbit and exact clean remote fullcheck+parallel standalone race passed. Full-catalog remote dump canonical comparison3810/3810 names resolved, no parse gaps. No new fields/structs/skills/docs contract change, so generation inapplicable to this task; companion presence task generated its own artifacts. Hosted exact-SHA CI pending.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Rejected NaN through the existing finite [0,1] incident contract with a one-condition change and real-loader regression. No presence, Active or live behavior broadening; reviewed and exact-tree gated with companion presence repair.
<!-- SECTION:FINAL_SUMMARY:END -->
