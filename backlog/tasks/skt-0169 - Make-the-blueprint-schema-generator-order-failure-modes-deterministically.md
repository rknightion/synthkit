---
id: SKT-0169
title: Make the blueprint schema generator order failure modes deterministically
status: Done
assignee:
  - '@loop53'
created_date: '2026-10-22T15:23:00Z'
updated_date: '2026-10-23T08:01:00Z'
labels:
  - gen
  - schema
dependencies: []
priority: medium
ordinal: 320000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Found by loop52 lane l1683: just gen reorders about 15 unrelated failure-mode rows in BLUEPRINT-SCHEMA.md / fielddocs.json between runs because failure modes are sorted unstably. just gen-check can then flake or produce noisy diffs.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Two consecutive just gen runs produce byte-identical BLUEPRINT-SCHEMA.md and fielddocs.json
- [x] #2 A test with ties in the sort key is red at base and green after
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Compose the accepted total-order comparator candidate, review once, run integrated gate, land exact green tree.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Landed total-order comparator and permutation regression in 4b24a9f, pushed as 13e4258 after the exact clean-tree composed just check passed remotely. CodeRabbit complete: zero findings, all three paths reviewed. Real schema generator run twice with identical artifact hashes. No emitted signal or blueprint field behavior changed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Deterministic failure-mode rows now sort by the complete dedup tuple (name, axis, help). The tied-row regression was red before the comparator change, green after; two generator runs and the full integrated gate passed.
<!-- SECTION:FINAL_SUMMARY:END -->
