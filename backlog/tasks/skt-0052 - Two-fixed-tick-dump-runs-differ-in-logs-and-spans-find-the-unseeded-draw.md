---
id: SKT-0052
title: 'Two fixed-tick dump runs differ in logs and spans: find the unseeded draw'
status: Done
assignee:
  - '@codex'
created_date: '2026-09-06 20:17'
updated_date: '2026-09-06 21:43'
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
- [x] #1 The differing fields between two fixed-tick safe dumps are enumerated by construct/workload and field, from a diff pasted into the task
- [x] #2 Every unseeded draw found is corrected with a failing-first test that runs the dump twice and asserts byte identity for the affected lane
- [x] #3 Any by-design variance is named per field with its reason, and the dump comparison excludes only those fields
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2026-09-09: bisect the saved fixed-tick diff by field, fix each nondeterministic draw at source with a failing-first two-run test, document only evidenced by-design fields, and prove the final two-run diff.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-09-09 pre-fix field inventory from the saved fixed-tick diff:

| Construct/workload | Field | First dump | Repeat dump |
|---|---|---|---|
| k8s_cluster | OTLP pod-log inventory service entry | alloy present | absent |
| k8s_cluster | OTLP pod-log inventory service entry | application-controller present | absent |
| k8s_cluster | OTLP pod-log inventory service entry | absent | cert-manager-controller present |
| ai_agent / acme-datagen-analysis | trace spans inventory | includes execute_tool classify_request | missing execute_tool classify_request |
| ai_agent / acme-datagen-extraction-api | trace spans inventory | includes execute_tool grammar_validate | missing execute_tool grammar_validate |

Failing-first tests pinned the wall-clock-derived first-tick trace selection and the pod-log draw. Fresh minters now derive first-tick IDs from declared entity identity plus tick ordinal; request timestamps remain time-based, and later ticks remain distinct. just check passed after the required just gen. Two consecutive explicit grafana-ai-o11y dumps were byte-identical with SHA-256 84e02f1fb16bbc9d906f8aca6ba29fc595edf5899c5300bcf598d7123e5fcd87. No by-design field variance remained in inventory shape, so no comparison exclusion was added. The single permitted e2e invocation and exact-head CI passed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Removed fixed-tick pod-log and trace inventory drift at each draw, with failing-first coverage and a byte-identical whole-dump proof. No variance exclusion was needed.
<!-- SECTION:FINAL_SUMMARY:END -->
