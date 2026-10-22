---
id: SKT-0169
title: Make the blueprint schema generator order failure modes deterministically
status: To Do
assignee: []
created_date: '2026-10-22T15:23:00Z'
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
- [ ] #1 Two consecutive just gen runs produce byte-identical BLUEPRINT-SCHEMA.md and fielddocs.json
- [ ] #2 A test with ties in the sort key is red at base and green after
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
