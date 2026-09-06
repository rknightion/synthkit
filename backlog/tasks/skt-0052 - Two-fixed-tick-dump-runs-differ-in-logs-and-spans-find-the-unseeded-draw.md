---
id: SKT-0052
title: 'Two fixed-tick dump runs differ in logs and spans: find the unseeded draw'
status: In Progress
assignee:
  - '@codex'
created_date: '2026-09-06 20:17'
updated_date: '2026-09-06 21:06'
labels:
  - determinism
dependencies: []
priority: high
type: bug
ordinal: 148000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The 2026-09-08 wave could not prove whole-dump byte identity for the CSP lanes because two unchanged, fixed-tick baseline runs of the safe explicit dump already differed in unrelated log and span output; only the metric and Loki inventory prefix was byte-identical (SHA-256 f85e7080d4002a3b9e3eb028861f0ffd02472b1e79f32122822c3e8b8438adf7). The wave operating model requires every value to derive from a seed unit (generation id, entity identity), never wall clock or a global RNG, precisely so the inventory gate can compare two runs. Bisect which lane and which field differs (diff the two dumps, group by construct/workload and field), find the draw that is wall-clock or global-RNG derived, and fix it at source with a failing-first two-run test. If a difference is by design (SKT-0004 recorded that generation and score COUNTS may vary), record exactly which field and why, and make the dump comparison exclude only that field explicitly rather than tolerating unexplained drift.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The differing fields between two fixed-tick safe dumps are enumerated by construct/workload and field, from a diff pasted into the task
- [ ] #2 Every unseeded draw found is corrected with a failing-first test that runs the dump twice and asserts byte identity for the affected lane
- [ ] #3 Any by-design variance is named per field with its reason, and the dump comparison excludes only those fields
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2026-09-09: bisect the saved fixed-tick diff by field, fix each nondeterministic draw at source with a failing-first two-run test, document only evidenced by-design fields, and prove the final two-run diff.
<!-- SECTION:PLAN:END -->
