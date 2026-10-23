---
id: SKT-0171
title: Preserve explicit zero intensity in scheduled blueprint incidents
status: Done
assignee:
  - '@loop54'
created_date: '2026-10-23T08:01:00Z'
updated_date: '2026-10-23T20:42:00Z'
labels:
  - blueprint
  - incidents
dependencies: []
priority: medium
ordinal: 322000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The scheduled-incident encoder omits an intensity suffix when the decoded value is zero, so an authored zero becomes the engine default of one. This predates the optional custom metric incident endpoint but makes a disabled scheduled effect act at full strength. Preserve explicit presence separately from omission without changing legacy omitted defaults or live resolved-zero behavior.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A public-loader reproduction observes authored zero as no active effect, while omitted intensity retains its documented default
- [x] #2 Single-mode and scenario-reference scheduling preserve fractional, zero and omitted intensity consistently without weakening scope validation
- [x] #3 Red then green real-consumer tests and canonical incident guidance agree with the presence contract
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop54: isolate scheduled intensity presence seam; public-loader red reproduction, fix zero/omission/fractional consistency in both scheduling forms, update canonical guidance, remote focused proof and guarded review.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Loop54 landed task f737cc37c31cf6f397a4974aa09df3f86dda6368, composed tipff9248e7e96f557a900ae341e51b0080764c436d/tree384c26fb75220170ad110f403bb36b0f0ea307fb. Authored scheduled zero now retains presence through inline and scenario effects; omission/fractions/live and programmatic legacy preserved. Actual loader+registered workload ordinary/native red-green, guarded review, remote canonical generation and final gen/docs/skills checks passed. Earlier generation-related dsreview FAIL is historical, its major/minor composition prerequisites resolved. CodeRabbit complete11/11 zero major; retained metric-guide minor is scoped to intensity-scaled metric and links canonical Active-only exceptions. Full remote check and standalone race passed in parallel on one exact clean tree; local private denylist hygiene clean2004files. Explicit full-catalog remote just dump and canonical conformance resolve3810/3810 metric names, no parse gaps; name comparison is not backend/label-envelope proof. Exact hosted CI pending.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Preserved explicit scheduled zero intensity without changing omission, fractional or live/Active-only contracts. Canonical schema/guidance and meaningful real-consumer proof reconciled; exact integrated checks, race, review and inventory conformance passed before/after publication as recorded.
<!-- SECTION:FINAL_SUMMARY:END -->
