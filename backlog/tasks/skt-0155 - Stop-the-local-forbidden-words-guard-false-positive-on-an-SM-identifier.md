---
id: SKT-0155
title: Stop the local forbidden-words guard false-positive on an SM identifier
status: Done
assignee:
  - '@loop51'
created_date: '2026-10-20T22:20:00Z'
updated_date: '2026-10-21T01:05:00Z'
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
- [x] #1 The local guard passes on a clean tree with the full local list
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
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

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Renamed the no-checks SM helper and test, with no behavior/assertion/pattern/exclusion changes; removed the repeated matching substring from tracker prose. Full ignored local-list guard passed on the exact committed composed tree (1872 files). Delivered in 097743b00341cf0248a0bf72d819d460763f69bd; pushed batch tip e009f1955fc883aa8bb55cd8454272c37d18abfe passed composed check including race. No schema or renderer change.
<!-- SECTION:FINAL_SUMMARY:END -->
