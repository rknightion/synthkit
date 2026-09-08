---
id: SKT-0062
title: Preserve synthetic metric shapes per producer during corpus comparison
status: Done
assignee: []
created_date: '2026-09-08 00:30'
updated_date: '2026-09-08 01:27'
labels: []
dependencies: []
ordinal: 158000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Producer overlap currently selects a family but compares its full synthetic union, so unrelated jobs can introduce false label and histogram contradictions. Preserve directly observed per-producer shapes while retaining the legacy flattened inventory.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A failing behavioral test demonstrates unrelated producer shape leakage and passes after correction
- [x] #2 Matched producer shapes retain genuine contradictions and legacy inventories retain prior behavior
- [x] #3 Targeted checks and required review pass
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Evidence: job-specific shape test failed with an unrelated counter/gauge union contradiction, then passed while retaining a true matched-producer mismatch. Additive producer_metrics preserves the flattened view and JSON round-trip. The comparator also now treats sourced conditional node-label bags, OS fields, owner fields, and opt-in Azure metadata as coverage differences; undocumented fixed keys still contradict. This is a deliberate open-key contract correction, not a contradiction exemption. Existing substrate-evidence tests now use a closed example key contract rather than a known open node-label bag. CodeRabbit reviewed six mechanism files with zero findings; full isolated gate reruns after the fixture correction.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Committed 94263330d2df3199a72813d8fa89854baa44e9a1. Fail-first producer-shape controls and positive contradiction controls passed; CodeRabbit zero findings; isolated just check passed with the staged tree matching the commit. Explicit-selection dump comparison completed. Schema generation was not required for this inventory-only API change.
<!-- SECTION:FINAL_SUMMARY:END -->
