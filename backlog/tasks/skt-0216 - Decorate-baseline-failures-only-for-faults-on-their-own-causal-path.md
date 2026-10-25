---
id: SKT-0216
title: Decorate baseline failures only for faults on their own causal path
status: In Progress
assignee: []
created_date: '2026-10-25T19:30:00Z'
updated_date: '2026-10-25T23:10:00Z'
labels:
  - causal
dependencies: []
ordinal: 420000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Baseline failures touched by any slowdown are decorated globally, suppressing their normal error and warning log streams even when the fault is unrelated.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An unrelated ambient slowdown does not replace baseline failure decoration or its error and warning logs
- [ ] #2 A fault affecting the failed request path still decorates the causal failure with consistent timing
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement own-path baseline-fault decoration on the immutable composed causal source snapshot; public-boundary red/green must retain unrelated baseline error/warning streams and related-path causal timing. No shared engine/schema or rate change. Candidate waits final foundation repair and composed landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner closeout: Own-path fault decoration held for causal/app composition.
<!-- SECTION:NOTES:END -->
