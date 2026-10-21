---
id: SKT-0155
title: Stop the local forbidden-words guard false-positive on an SM identifier
status: In Progress
assignee:
  - '@loop51'
created_date: '2026-10-20T22:20:00Z'
updated_date: '2026-10-21T00:39:00Z'
labels: []
dependencies: []
priority: medium
type: task
ordinal: 288000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The full local forbidden-words guard matches a helper identifier in the two SM handoff files. Rename the helper and associated test without changing behavior, exclusions or patterns; avoid repeating the matched identifier in tracker prose.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The local guard passes on a clean tree with the full local list
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Reproduce full local guard false positive, rename identifier only, prove guard and focused SM checks.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Candidate renames only the no-checks helper and test. Base guard fails; scoped candidate guard passes. The tracker repeated the same matching substring, so its description is sanitised as part of the fix. Final whole-tree proof follows composition.
<!-- SECTION:NOTES:END -->
