---
id: SKT-0120
title: Build current embedded UI before Go validation
status: Done
assignee:
  - '@loop-root'
created_date: '2026-10-10 18:49'
updated_date: '2026-10-11 08:50'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 242000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
After a source fast-forward changing Vite base, ignored ui/dist can still contain an absolute-asset index from the preceding source. Go embeds that old index and TestRuntimeBasePath fails asset-prefix containment even though the current UI source and a newly generated build are correct. just check currently runs Go plain/race before its later UI build, so the gate does not establish its own generation prerequisite. Fix the local task dependency, not the test or URL contract.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Starting with an older absolute-asset dist, the supported local validation task regenerates the current UI before Go reads go:embed inputs and TestRuntimeBasePath passes.
- [x] #2 The task preserves tests, build inputs and the existing single-build runtime-prefix contract; no timeout or assertion is weakened.
- [x] #3 Generation and proof receipts cover actual ignored embedded index/assets rather than treating tracked-file identity as complete runtime input identity.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Regenerate private UI embed prerequisite before Go test/race; seed stale absolute-asset index and retain ignored-input hash receipts; gate and independent review.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
One recipe-only change, original old-index reproduction failed correctly and regenerated runtime-prefix test passed with complete ignored index/assets hashes. First full gate timeout600.692s retained; new-base gate0, exact landeddea393d84ee23c9100510c830b0977185c00a20e composed0 and CI38097611214all10jobs success. Independent reviewPASS; one25fileaggregate routine CodeRabbit complete0findings. No test/assertion/timeout/CI change. Extra generator/inventory DoD not applicable to recipe-only ordering.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Private UI prerequisite generates embedded index/assets before Go validation; supported check proved real generation order and runtime prefix containment. Exact landed CI and composed gate green.
<!-- SECTION:FINAL_SUMMARY:END -->
