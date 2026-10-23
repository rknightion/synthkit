---
id: SKT-0171
title: Preserve explicit zero intensity in scheduled blueprint incidents
status: To Do
assignee: []
created_date: '2026-10-23T08:01:00Z'
labels:
  - blueprint
  - incidents
dependencies: []
priority: medium
ordinal: 322000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The scheduled-incident encoder omits an intensity suffix when the decoded value is zero, so an authored zero becomes the engine default of one. This predates the optional custom metric incident endpoint but makes a disabled scheduled effect act at full strength. Preserve explicit presence separately from omission without changing legacy omitted defaults or live resolved-zero behavior.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A public-loader reproduction observes authored zero as no active effect, while omitted intensity retains its documented default
- [ ] #2 Single-mode and scenario-reference scheduling preserve fractional, zero and omitted intensity consistently without weakening scope validation
- [ ] #3 Red then green real-consumer tests and canonical incident guidance agree with the presence contract
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
