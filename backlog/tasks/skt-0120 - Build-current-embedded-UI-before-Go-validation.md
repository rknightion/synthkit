---
id: SKT-0120
title: Build current embedded UI before Go validation
status: In Progress
assignee:
  - '@loop-root'
created_date: '2026-10-10 18:49'
updated_date: '2026-10-10 23:41'
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
- [ ] #1 Starting with an older absolute-asset dist, the supported local validation task regenerates the current UI before Go reads go:embed inputs and TestRuntimeBasePath passes.
- [ ] #2 The task preserves tests, build inputs and the existing single-build runtime-prefix contract; no timeout or assertion is weakened.
- [ ] #3 Generation and proof receipts cover actual ignored embedded index/assets rather than treating tracked-file identity as complete runtime input identity.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Regenerate private UI embed prerequisite before Go test/race; seed stale absolute-asset index and retain ignored-input hash receipts; gate and independent review.
<!-- SECTION:PLAN:END -->
