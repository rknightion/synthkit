---
id: SKT-0212
title: Declare a per-resource failed-call hold instead of a shared pre-work constant
status: To Do
assignee: []
created_date: '2026-10-25T19:30:00Z'
labels:
  - causal
dependencies: []
ordinal: 416000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A shared layout pre-work share currently supplies a failed-call hold without a per-resource basis. The declared healthy reference and failure timing need an explicit resource-owned contract.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Each causal resource can declare a failed-call hold with documented units and validation
- [ ] #2 Engine failure holds and app timelines use the declared value consistently, with red/green proof and generated schema aligned
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
