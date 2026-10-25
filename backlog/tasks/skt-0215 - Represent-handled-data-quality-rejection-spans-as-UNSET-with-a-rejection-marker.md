---
id: SKT-0215
title: >-
  Represent handled data-quality rejection spans as UNSET with a rejection
  marker
status: To Do
assignee: []
created_date: '2026-10-25T19:30:00Z'
labels:
  - causal
dependencies: []
ordinal: 419000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Handled data-quality rejections currently produce ERROR consumer spans despite successful processing of the rejection, warning logs and zero function Errors.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Handled business rejections retain warning logs and zero function execution errors while their spans are UNSET with a sourced rejection marker
- [ ] #2 True execution failures remain ERROR and the signal contract documents the distinction
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
