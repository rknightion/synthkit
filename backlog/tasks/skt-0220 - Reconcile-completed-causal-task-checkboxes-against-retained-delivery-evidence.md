---
id: SKT-0220
title: Reconcile completed causal task checkboxes against retained delivery evidence
status: To Do
assignee: []
created_date: '2026-10-25T20:12:00Z'
labels:
  - causal
dependencies: []
ordinal: 426000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Composed review found older completed causal task records whose acceptance and definition-of-done boxes remain unchecked. Reconcile against actual retained delivery evidence, not blanket checkbox changes; missing evidence must stay explicit.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Completed records in the identified causal batch have evidence-backed acceptance state or an explicit unresolved disposition
- [ ] #2 No verification checkbox is marked from task status alone and prior implementation history is retained
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
